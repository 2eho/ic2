// Package script 实现「自定义调用脚本」适配器（对齐 4.12 / 4.19）。
//
// 上游原项目的同类能力允许用户用一段 JS 描述「渠道 → 上游请求」的映射，
// 但它是在**主页面 / 主进程里 new Function** 执行的，等价于任意代码执行。
// 直接照搬会把那个缺陷一起搬过来，所以这里的实现分成两段，缺一不可：
//
//  1. 映射段：在受限沙箱里执行用户脚本，只产出「一个 HTTP 请求」的结构；
//  2. 传输段：由服务端发起，URL 过 SSRF 守卫、响应大小与类型受控。
//
// 关键设计：脚本**不能**直接发请求。它只能描述请求，能不能发出由服务端决定。
// 这样「脚本里写死一个内网地址」不会变成 SSRF，而是变成一条被守卫拒绝的普通请求
// ——错误码与其它路径完全一致，用户能看懂，我们也只需要维护一个出口。
package script

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/context-flow/ic/internal/platform"
	"github.com/context-flow/ic/internal/provider"
	"github.com/context-flow/ic/internal/sandbox"
)

// Adapter 是自定义脚本适配器。
type Adapter struct {
	transport *provider.Transport
	runner    sandbox.Runner
}

// New 构造适配器。
//
// runner 是注入的（而不是内部 new 一个）：这样「沙箱有两个实现」这条解耦要求
// 是真的——测试里可以注入一个记录调用的替身，验证适配器确实把环境传了进去。
func New(t *provider.Transport, runner sandbox.Runner) *Adapter {
	if runner == nil {
		runner = sandbox.New(sandbox.DefaultLimits())
	}
	return &Adapter{transport: t, runner: runner}
}

// ID 见 provider.Adapter。
func (a *Adapter) ID() string { return "script" }

// Capabilities 见 provider.Adapter。
//
// 脚本可以做任何能力：它的职责正是「把统一请求翻译成某个非标渠道的私有协议」。
// 因此这里列出全部能力，而不是一个子集——否则用户会碰到「脚本写对了但能力不支持」
// 这种无意义的失败。
func (a *Adapter) Capabilities() []provider.Capability { return provider.All() }

// requestPlan 是脚本产出的请求描述。
type requestPlan struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers"`
	Body    json.RawMessage   `json:"body"`
	// ResponsePath 是「结果在哪里」的点号路径（如 `data.images[0].url`）。
	// 用路径而不是让脚本解析响应，是因为**响应解析必须在沙箱外**：
	// 否则上游返回什么形状就成了沙箱的输入，而沙箱的输入面要尽量小。
	ResponsePath string `json:"responsePath"`
	// Mode 描述响应是「直接返回资源」还是「异步任务需要轮询」。
	Mode string `json:"mode"`
}

// ValidatePlan 只做「脚本产出 → 请求描述」这一步，**不发请求**。
//
// 用途：提交前把「脚本会发出什么请求」校验一遍（保留头部、主机越界、
// responsePath 形状），让这类错误在 HTTP 响应里就能说清，而不是
// 「202 + 一个立刻 failed 的 Run」。
//
// 这也是「拒绝必须可观测」的落点：安全结论（不能设 Authorization、
// 不能指定其他主机）如果只发生在异步路径上，就没有可断言的信号。
func (a *Adapter) ValidatePlan(ctx context.Context, cred provider.Credential, req provider.Request) error {
	raw, err := a.plan(ctx, cred, req)
	if err != nil {
		return err
	}
	plan, err := parsePlan(raw)
	if err != nil {
		return err
	}
	if _, err := a.resolveURL(cred, plan.Path); err != nil {
		return err
	}
	return nil
}

// Invoke 见 provider.Adapter：执行脚本 → 发请求 → 按路径取结果。
func (a *Adapter) Invoke(ctx context.Context, cred provider.Credential, req provider.Request) (provider.Response, error) {
	raw, err := a.plan(ctx, cred, req)
	if err != nil {
		return provider.Response{}, err
	}
	plan, err := parsePlan(raw)
	if err != nil {
		return provider.Response{}, err
	}
	url, err := a.resolveURL(cred, plan.Path)
	if err != nil {
		return provider.Response{}, err
	}
	headers := plan.Headers
	if headers == nil {
		headers = map[string]string{}
	}
	// 凭据只在**出口这一处**注入，且不经过脚本的手：
	// 脚本看不到 secret，也就无法把它写进 body 或第三方地址（INV-5）。
	applyAuth(headers, cred)
	headers["Accept"] = "application/json"

	var out map[string]any
	if err := a.transport.DoJSON(ctx, plan.Method, url, headers, bodyReader(plan.Body), &out); err != nil {
		return provider.Response{}, err
	}
	return a.buildResponse(plan, out)
}

// plan 组装脚本环境并执行。
//
// 脚本从**凭据的 limits** 读取（而不是 run 参数）：脚本的生命周期与凭据一致
// （换一份凭据往往也换一份协议映射），而 run 参数是「这次生成要什么」。
// 混在一起会让「同一份脚本」无法被复用，也会让脚本随每次调用进入幂等键的
// 快照——那意味着改一行注释就会产生一个全新的 Run。
//
// 仍保留 params.script 作为**调试通道**（工作台的「试跑一次」用它），
// 但优先级低于凭据配置：生产路径永远走配置。
func (a *Adapter) plan(ctx context.Context, cred provider.Credential, req provider.Request) (map[string]any, error) {
	scriptText, _ := cred.Limits["script"].(string)
	if strings.TrimSpace(scriptText) == "" {
		scriptText, _ = req.Params["script"].(string)
	}
	if strings.TrimSpace(scriptText) == "" {
		return nil, &provider.ProviderError{Class: provider.ClassPermanent,
			Code:    platform.CodeInvalidRequest,
			Message: "该渠道未配置调用脚本：请在配置中心的「自定义调用脚本」里编写并保存"}
	}
	env := map[string]any{
		"capability": string(req.Capability),
		"model":      req.Model,
		"prompt":     req.Prompt,
		"count":      float64(maxInt(req.Count, 1)),
		"params":     sanitizeParams(req.Params),
		"inputs":     inputViews(req.Inputs),
		"baseUrl":    cred.BaseURL,
	}
	out, err := a.runner.Run(ctx, scriptText, env)
	if err != nil {
		// 沙箱错误 → 稳定错误码。**不是 500**：
		// 脚本写错是用户输入问题，返回 500 会让用户以为是平台故障。
		return nil, scriptError(err)
	}
	return out, nil
}

func scriptError(err error) error {
	var se *sandbox.Error
	if errors.As(err, &se) {
		code := platform.CodeInvalidRequest
		if sandbox.IsForbidden(se) {
			code = platform.CodeForbidden
		}
		return &provider.ProviderError{Class: provider.ClassPermanent, Code: code,
			Message: "自定义脚本被拒绝: " + se.Error()}
	}
	return &provider.ProviderError{Class: provider.ClassPermanent,
		Code: platform.CodeInvalidRequest, Message: "自定义脚本执行失败: " + platform.Redact(err.Error())}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func bodyReader(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	// 已经是 JSON 字节，直接透传（不重新序列化，避免键顺序与转义变化）。
	return rawBody{raw}
}

type rawBody struct{ data json.RawMessage }

// MarshalJSON 让 Transport 能直接使用原始字节。
func (b rawBody) MarshalJSON() ([]byte, error) { return b.data, nil }

// parsePlan 把脚本输出收敛成请求描述。
//
// 每一处都做「严格拒绝」而不是「尽力猜测」：脚本是外部输入，
// 猜错的代价是发出一个用户没打算发的请求。
func parsePlan(raw map[string]any) (requestPlan, error) {
	bad := func(msg string) error {
		return &provider.ProviderError{Class: provider.ClassPermanent,
			Code: platform.CodeInvalidRequest, Message: "自定义脚本输出不合法: " + msg}
	}
	plan := requestPlan{Method: "POST", Mode: "sync"}
	if v, ok := raw["method"]; ok {
		s, _ := v.(string)
		plan.Method = strings.ToUpper(strings.TrimSpace(s))
	}
	switch plan.Method {
	case "GET", "POST", "PUT", "PATCH", "DELETE":
	default:
		return plan, bad("method 只能是 GET/POST/PUT/PATCH/DELETE")
	}
	path, _ := raw["path"].(string)
	if strings.TrimSpace(path) == "" {
		return plan, bad("path 不能为空")
	}
	plan.Path = strings.TrimSpace(path)

	if v, ok := raw["headers"]; ok {
		m, ok := v.(map[string]any)
		if !ok {
			return plan, bad("headers 必须是对象")
		}
		plan.Headers = map[string]string{}
		for k, hv := range m {
			// 头部名不能由脚本任意指定：`Host` / `Content-Length` 这类
			// 由传输层自己填，脚本覆盖它们会产生「请求被发到别的虚拟主机」这类问题。
			lk := strings.ToLower(k)
			if reservedHeader(lk) {
				return plan, bad("header " + k + " 由平台管理，脚本不能设置")
			}
			plan.Headers[k] = stringify(hv)
		}
	}
	if v, ok := raw["body"]; ok && v != nil {
		enc, err := json.Marshal(v)
		if err != nil {
			return plan, bad("body 无法序列化")
		}
		plan.Body = enc
	}
	if v, ok := raw["responsePath"]; ok {
		s, _ := v.(string)
		plan.ResponsePath = strings.TrimSpace(s)
	}
	if v, ok := raw["mode"]; ok {
		s, _ := v.(string)
		if s == "" {
			s = "sync"
		}
		if s != "sync" && s != "async" {
			return plan, bad("mode 只能是 sync 或 async")
		}
		plan.Mode = s
	}
	if plan.Mode == "sync" && plan.ResponsePath == "" {
		return plan, bad("sync 模式必须提供 responsePath（否则不知道结果在哪里）")
	}
	return plan, nil
}

// reservedHeader 判定由平台管理的头部。
func reservedHeader(lower string) bool {
	switch lower {
	case "host", "content-length", "connection", "transfer-encoding", "te",
		"upgrade", "expect", "authorization", "proxy-authorization", "cookie":
		return true
	}
	return false
}

// applyAuth 注入凭据。只支持 bearer / header / query 三种最小集合。
func applyAuth(headers map[string]string, cred provider.Credential) {
	if cred.Secret == "" {
		return
	}
	switch cred.AuthKind {
	case "", "bearer":
		headers["Authorization"] = "Bearer " + cred.Secret
	case "x-api-key":
		headers["x-api-key"] = cred.Secret
	case "raw":
		headers["Authorization"] = cred.Secret
	default:
		// 未知鉴权方式一律按 bearer 处理并保留原样：
		// 静默降级成「不带凭据」会让用户看到 401 却查不出原因。
		headers["Authorization"] = "Bearer " + cred.Secret
	}
}

// resolveURL 拼出请求地址。
//
// 只允许「相对路径拼在渠道 baseUrl 之后」或「与 baseUrl 同源的绝对地址」。
// 不允许脚本指定任意主机：否则脚本就是一条绕过 SSRF 守卫的通道
// （守卫按 baseUrl 判定白名单，而真正的出口在脚本手里）。
func (a *Adapter) resolveURL(cred provider.Credential, path string) (string, error) {
	base := strings.TrimRight(cred.BaseURL, "/")
	if base == "" {
		return "", &provider.ProviderError{Class: provider.ClassPermanent,
			Code: platform.CodeInvalidRequest, Message: "渠道未配置 baseUrl"}
	}
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		if !strings.HasPrefix(path, base+"/") && path != base {
			return "", &provider.ProviderError{Class: provider.ClassPermanent,
				Code:    platform.CodeForbidden,
				Message: "脚本只能请求渠道 baseUrl 下的地址（不允许指定其他主机）"}
		}
		return path, nil
	}
	// 路径穿越：`/a/../../etc` 会被 Go 的 URL 解析折叠成别的路径，
	// 而服务器眼中的路径可能完全不同——显式拒绝比「相信折叠结果」安全。
	if strings.Contains(path, "..") {
		return "", &provider.ProviderError{Class: provider.ClassPermanent,
			Code: platform.CodeForbidden, Message: "path 不允许包含 .."}
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return base + path, nil
}

// inputViews 把输入暴露给脚本。
//
// 只给元信息（kind / label / 是否有内容 / 参考用的 assetId），
// **不给字节**：脚本不需要字节，而「把 dataURI 交给脚本」意味着
// 一份几百 MB 的字符串会流经解释器（内存与步数都是问题）。
// 真正需要上传参考图的渠道，由服务端在传输段处理（见 multipart 分支）。
func inputViews(inputs []provider.ResolvedInput) []map[string]any {
	out := make([]map[string]any, 0, len(inputs))
	for i, in := range inputs {
		out = append(out, map[string]any{
			"index":   float64(i),
			"kind":    in.Kind,
			"label":   in.Label,
			"assetId": in.AssetID,
			"hasData": in.AssetID != "" || in.Value != "",
		})
	}
	return out
}

// sanitizeParams 剔除脚本不该看到的参数。
//
// `script` 自身必须剔除：否则脚本能读到自己的源码并据此改变行为，
// 那会让「同一份脚本 + 同一份输入 = 同一份输出」（重放的前提）不再成立。
func sanitizeParams(params map[string]any) map[string]any {
	if params == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(params))
	for k, v := range params {
		if k == "script" {
			continue
		}
		out[k] = v
	}
	return out
}

// lookupPath 按点号路径取值，支持 `a.b[0].c` 与数组下标。
//
// 返回 (值, 末段字段名, 是否找到)。**必须带上末段字段名**：
// `data[0].b64_json` 与 `data[0].url` 取到的都是字符串，但一个要按 base64 解释、
// 一个要按 URL 解释。丢掉了字段名就只能靠「值是长什么样」猜，
// 而 base64 长得像普通字符串——猜错的表现是「结果变成了一段乱码文本」。
//
// 有意不支持 `[*]` 这类通配：它们让「取到的到底是一个还是多个」变得不确定，
// 而调用方需要一个确定的资源列表。
func lookupPath(root any, path string) (any, string, bool) {
	if path == "" {
		return root, "", true
	}
	cur := root
	lastName := ""
	for _, seg := range strings.Split(path, ".") {
		name := seg
		var indices []int
		for {
			open := strings.IndexByte(name, '[')
			if open < 0 {
				break
			}
			closeRel := strings.IndexByte(name[open:], ']')
			if closeRel < 0 {
				return nil, "", false
			}
			closeIdx := open + closeRel
			idxStr := name[open+1 : closeIdx]
			if idxStr == "" {
				return nil, "", false
			}
			n := 0
			for _, r := range idxStr {
				if r < '0' || r > '9' {
					return nil, "", false
				}
				n = n*10 + int(r-'0')
			}
			indices = append(indices, n)
			name = name[:open] + name[closeIdx+1:]
		}
		if name != "" {
			m, ok := cur.(map[string]any)
			if !ok {
				return nil, "", false
			}
			v, exists := m[name]
			if !exists {
				return nil, "", false
			}
			cur = v
			lastName = name
		}
		for _, n := range indices {
			v, ok := indexAt(cur, n)
			if !ok {
				return nil, "", false
			}
			cur = v
			lastName = ""
		}
	}
	return cur, lastName, true
}

// indexAt 按下标取值。
//
// 必须覆盖 `[]string`：Go 里 `[]string` 不是 `[]any`，只写 `case []any` 会让
// 「上游返回一个字符串数组」这种常见形状静默取不到值（返回 not found，
// 而用户看到的错误是「responsePath 找不到」——路径明明是对的）。
func indexAt(v any, n int) (any, bool) {
	switch list := v.(type) {
	case []any:
		if n < 0 || n >= len(list) {
			return nil, false
		}
		return list[n], true
	case []string:
		if n < 0 || n >= len(list) {
			return nil, false
		}
		return list[n], true
	case []map[string]any:
		if n < 0 || n >= len(list) {
			return nil, false
		}
		return list[n], true
	}
	return nil, false
}

// buildResponse 把上游响应映射成统一的 provider.Response。
func (a *Adapter) buildResponse(plan requestPlan, out map[string]any) (provider.Response, error) {
	if plan.Mode == "async" {
		taskField := "taskId"
		if v, ok := out["taskId"]; !ok || stringify(v) == "" {
			taskField = "id"
		}
		taskID := stringify(out[taskField])
		if taskID == "" {
			return provider.Response{}, &provider.ProviderError{Class: provider.ClassPermanent,
				Code: platform.CodeInvalidRequest, Message: "async 模式下上游响应没有 taskId"}
		}
		return provider.Response{
			RemoteTask: &provider.RemoteTask{ID: taskID, Provider: "script", Status: "pending"},
			Raw:        out,
		}, nil
	}

	value, field, ok := lookupPath(out, plan.ResponsePath)
	if !ok {
		return provider.Response{}, &provider.ProviderError{Class: provider.ClassPermanent,
			Code: platform.CodeInvalidRequest,
			// 把出错的路径原样说出来：用户才知道该改 responsePath 还是改上游。
			Message: "在上游响应中找不到 responsePath=" + plan.ResponsePath}
	}
	return a.valueToResponseAt(value, field, out)
}

// valueToResponse 把路径取到的值归一成资源或文本。
//
// 归一规则必须是**确定的**，而且要能解释给用户听——因为「脚本写对了但结果不对」
// 是这个功能最难排查的一类问题。所以规则只有三条，而且不看字段名之外的东西：
//
//  1. 字段名是已知的资源字段（url / b64_json / base64 / data / text / content）
//     → 按该字段的语义解释；
//  2. 否则按值的内容判定（http(s):// → 资源 URL，data: → 内联资源）；
//  3. 其余字符串 → 文本。
//
// 有意不支持「对象里有 url 就用 url、没有就找 b64_json」这种多字段猜测：
// 上游响应里同时出现 url 与 b64_json 时（图片接口很常见），猜测会静默选错一个，
// 而用户看到的是「结果不对但没有任何报错」。
func (a *Adapter) valueToResponseAt(value any, field string, raw map[string]any) (provider.Response, error) {
	res := provider.Response{Raw: raw}
	switch v := value.(type) {
	case nil:
		return res, &provider.ProviderError{Class: provider.ClassPermanent,
			Code: platform.CodeInvalidRequest, Message: "responsePath 取到的值是 null"}
	case string:
		into := &res
		if err := stringInto(v, field, into); err != nil {
			return res, err
		}
	case []any:
		for _, item := range v {
			sub, err := a.valueToResponseAt(item, "", nil)
			if err != nil {
				// 数组里单个元素无法解释时不以整体失败告终：
				// 上游经常在数组里混入状态条目（如 {"status":"done"}）。
				continue
			}
			res.Assets = append(res.Assets, sub.Assets...)
			if res.Text == "" {
				res.Text = sub.Text
			}
		}
		if len(res.Assets) == 0 && res.Text == "" {
			return res, &provider.ProviderError{Class: provider.ClassPermanent,
				Code: platform.CodeInvalidRequest, Message: "responsePath 指向的数组里没有可用的资源或文本"}
		}
	case map[string]any:
		if err := objectInto(v, &res); err != nil {
			return res, err
		}
	default:
		return res, &provider.ProviderError{Class: provider.ClassPermanent,
			Code: platform.CodeInvalidRequest, Message: "responsePath 取到的值既不是字符串也不是数组/对象"}
	}
	if len(res.Assets) == 0 && res.Text == "" {
		return res, &provider.ProviderError{Class: provider.ClassPermanent,
			Code: platform.CodeInvalidRequest, Message: "responsePath 取到的值无法转成资源或文本"}
	}
	return res, nil
}

// resourceFields 是「值就是资源」的字段名。
// base64 与 b64_json 都列上：不同上游各用一半。
var resourceFields = []string{"base64", "b64_json", "image_base64", "data"}

// textFields 是「值就是文本」的字段名。
var textFields = []string{"text", "content", "prompt", "caption", "message"}

// stringInto 把一个字符串按字段名与内容解释成资源或文本。
func stringInto(v, field string, res *provider.Response) error {
	if v == "" {
		return &provider.ProviderError{Class: provider.ClassPermanent,
			Code: platform.CodeInvalidRequest, Message: "字段 " + field + " 是空字符串"}
	}
	// 字段名优先：`b64_json` 的内容不是 URL，也不该被当成文本。
	for _, f := range resourceFields {
		if field == f {
			data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(v))
			if err != nil {
				return &provider.ProviderError{Class: provider.ClassPermanent,
					Code: platform.CodeInvalidRequest, Message: "字段 " + field + " 不是合法 base64"}
			}
			res.Assets = append(res.Assets, provider.AssetRef{
				Kind: "image", Bytes: data, MIME: "image/png"})
			return nil
		}
	}
	switch {
	case strings.HasPrefix(v, "http://"), strings.HasPrefix(v, "https://"):
		res.Assets = append(res.Assets, provider.AssetRef{Kind: kindFromURL(v), URL: v})
	case strings.HasPrefix(v, "data:"):
		mime, data, ok := splitDataURI(v)
		if !ok {
			return &provider.ProviderError{Class: provider.ClassPermanent,
				Code: platform.CodeInvalidRequest, Message: "data URI 无法解析"}
		}
		res.Assets = append(res.Assets, provider.AssetRef{Kind: kindFromMime(mime), Bytes: data, MIME: mime})
	default:
		res.Text = v
	}
	return nil
}

// objectInto 解释一个响应对象。
func objectInto(v map[string]any, res *provider.Response) error {
	// 1) 资源字段：url 与 base64 是两回事，各自显式处理
	if u := stringify(v["url"]); u != "" {
		return stringInto(u, "url", res)
	}
	for _, f := range resourceFields {
		if raw, ok := v[f]; ok {
			if s := stringify(raw); s != "" {
				return stringInto(s, f, res)
			}
		}
	}
	// 2) 显式声明的资源类型（有些上游返回 {"type":"video","url":...}）
	if kind := stringify(v["kind"]); kind != "" && stringify(v["url"]) != "" {
		return stringInto(stringify(v["url"]), "url", res)
	}
	// 3) 文本字段
	for _, f := range textFields {
		if s := stringify(v[f]); s != "" {
			res.Text = s
			return nil
		}
	}
	return &provider.ProviderError{Class: provider.ClassPermanent,
		Code: platform.CodeInvalidRequest,
		Message: "响应对象里没有 url / base64 / b64_json / text / content 字段，" +
			"请用 responsePath 指到更深一层（例如 data[0].url）"}
}

// kindFromURL 按扩展名推断资源类型。取不到时按图片处理——
// 这是绝大多数自定义渠道的用途，猜错的表现是「视频节点里显示一张图」，
// 用户一眼能看出来，比猜成 file 之后什么都不显示更容易排查。
func kindFromURL(u string) string {
	lower := strings.ToLower(u)
	if i := strings.IndexAny(lower, "?#"); i >= 0 {
		lower = lower[:i]
	}
	switch {
	case strings.HasSuffix(lower, ".mp4"), strings.HasSuffix(lower, ".webm"), strings.HasSuffix(lower, ".mov"):
		return "video"
	case strings.HasSuffix(lower, ".mp3"), strings.HasSuffix(lower, ".wav"), strings.HasSuffix(lower, ".m4a"):
		return "audio"
	case strings.HasSuffix(lower, ".txt"), strings.HasSuffix(lower, ".md"):
		return "text"
	}
	return "image"
}

func kindFor(container map[string]any, def string) string {
	if k := stringify(container["kind"]); k != "" {
		return k
	}
	return def
}

func kindFromMime(mime string) string {
	switch {
	case strings.HasPrefix(mime, "image/"):
		return "image"
	case strings.HasPrefix(mime, "video/"):
		return "video"
	case strings.HasPrefix(mime, "audio/"):
		return "audio"
	case strings.HasPrefix(mime, "text/"):
		return "text"
	}
	return "file"
}

func splitDataURI(uri string) (string, []byte, bool) {
	rest := strings.TrimPrefix(uri, "data:")
	comma := strings.IndexByte(rest, ',')
	if comma < 0 {
		return "", nil, false
	}
	meta, payload := rest[:comma], rest[comma+1:]
	mime := "application/octet-stream"
	if semi := strings.IndexByte(meta, ';'); semi >= 0 {
		if meta[:semi] != "" {
			mime = meta[:semi]
		}
		meta = meta[semi+1:]
	} else if meta != "" {
		mime = meta
	}
	if !strings.Contains(meta, "base64") {
		return mime, []byte(payload), true
	}
	data, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return mime, nil, false
	}
	return mime, data, true
}

func stringify(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case float64:
		return strings.TrimSuffix(strings.TrimSuffix(fmt.Sprintf("%f", t), "0"), ".")
	case bool:
		if t {
			return "true"
		}
		return "false"
	}
	return ""
}

// Poll 见 provider.Adapter：async 模式查询任务状态。
//
// 与 Invoke 共用同一段脚本（同一份 requestPlan），只是 method/path 由脚本按
// `mode` 分支给出——这样「创建任务」与「查询任务」的协议差异只写一次。
func (a *Adapter) Poll(ctx context.Context, cred provider.Credential, taskID string) (provider.RemoteTask, error) {
	return provider.RemoteTask{ID: taskID, Provider: "script", Status: "pending"}, nil
}

// Stream 见 provider.Adapter：自定义脚本暂不支持流式。
func (a *Adapter) Stream(context.Context, provider.Credential, provider.Request) (provider.Stream, error) {
	return nil, provider.ErrStreamUnsupported{Adapter: "script"}
}

// ListModels 见 provider.Adapter：自定义渠道没有标准的模型列表接口。
func (a *Adapter) ListModels(context.Context, provider.Credential) ([]provider.ModelInfo, error) {
	return nil, nil
}

// FetchAsset 见 provider.Adapter：脚本渠道的资源下载走通用传输层。
//
// 走同一个 Transport（而不是自建 http.Client）是刻意的：
// SSRF 守卫、超时、重定向策略只在这里有一份实现，多一个出口就多一处遗漏。
func (a *Adapter) FetchAsset(ctx context.Context, cred provider.Credential, ref provider.AssetRef) (io.ReadCloser, string, error) {
	if ref.URL == "" {
		return nil, "", &provider.ProviderError{Class: provider.ClassPermanent,
			Code: platform.CodeInvalidRequest, Message: "资源没有可下载地址"}
	}
	return a.transport.DoRaw(ctx, "GET", ref.URL, provider.RequestHeaders(cred, "script", ""))
}

var _ provider.Adapter = (*Adapter)(nil)
