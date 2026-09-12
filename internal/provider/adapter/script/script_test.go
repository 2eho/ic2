package script

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/context-flow/ic/internal/platform"
	"github.com/context-flow/ic/internal/provider"
	"github.com/context-flow/ic/internal/sandbox"
)

// fixedTransport 用 httptest 起真实 HTTP 服务，而不是打桩 Transport。
// 理由：这个适配器的价值就是「脚本描述的请求能真的发出去」，用替身等于自证。
func newTestAdapter(t *testing.T, h http.HandlerFunc) (*Adapter, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	// 测试用的是 httptest（127.0.0.1），因此显式放开私网。
	// 这不削弱 SSRF 断言：TestScriptCannotEscapeBaseURL 验的是「脚本指定的主机被拒绝」，
	// 而那一条在放开私网后依然成立——它的判据是「路径必须落在 baseUrl 之下」。
	guard := platform.NewNetGuard()
	guard.AllowPrivate = true
	tr := provider.NewTransport(http.DefaultClient, guard, nil)
	return New(tr, sandbox.New(sandbox.DefaultLimits())), srv
}

func credFor(srv *httptest.Server) provider.Credential {
	return provider.Credential{ID: "c1", ProviderID: "script", BaseURL: srv.URL, AuthKind: "bearer", Secret: "sk-test"}
}

func TestMapsRequestAndReadsResponse(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody map[string]any
	a, srv := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(`{"code":0,"data":{"images":[{"url":"https://cdn.example/a.png"}]}}`))
	})

	res, err := a.Invoke(context.Background(), credFor(srv), provider.Request{
		Capability: provider.CapImageGenerate,
		Model:      "my-model",
		Prompt:     "一只猫",
		Count:      2,
		Params: map[string]any{"script": `
			return {
				method: "POST",
				path: "/v1/custom/generate",
				headers: { "x-model": model },
				body: { text: prompt, n: count, size: default(params.size, "1k") },
				responsePath: "data.images[0].url",
			};
		`},
	})
	if err != nil {
		t.Fatalf("调用失败: %v", err)
	}
	if gotPath != "/v1/custom/generate" {
		t.Fatalf("路径错误: %s", gotPath)
	}
	// 凭据由平台注入，脚本看不到 secret
	if gotAuth != "Bearer sk-test" {
		t.Fatalf("鉴权头错误: %s", gotAuth)
	}
	if gotBody["text"] != "一只猫" || gotBody["n"] != float64(2) || gotBody["size"] != "1k" {
		t.Fatalf("请求体错误: %#v", gotBody)
	}
	if len(res.Assets) != 1 || res.Assets[0].URL != "https://cdn.example/a.png" {
		t.Fatalf("响应解析错误: %#v", res.Assets)
	}
}

func TestScriptCannotReadItsOwnSource(t *testing.T) {
	// 「同一脚本 + 同一输入 = 同一输出」是重放的前提，
	// 所以 params.script 不能出现在脚本环境里。
	a, srv := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"url":"https://cdn.example/x.png"}`))
	})
	_, err := a.Invoke(context.Background(), credFor(srv), provider.Request{
		Capability: provider.CapImageGenerate,
		Params: map[string]any{"script": `
			return { method: "POST", path: "/x", responsePath: "url", body: { leak: params.script } };
		`},
	})
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
}

// ATK-24：脚本不能设置保留头部（凭据只由平台注入）。
func TestScriptCannotSetAuthorizationHeader(t *testing.T) {
	a, srv := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("不应发出请求")
	})
	_, err := a.Invoke(context.Background(), credFor(srv), provider.Request{
		Capability: provider.CapImageGenerate,
		Params: map[string]any{"script": `
			return { method: "POST", path: "/x", headers: { Authorization: "Bearer stolen" }, responsePath: "url" };
		`},
	})
	if err == nil {
		t.Fatal("脚本设置 Authorization 应当被拒绝（否则凭据注入点就形同虚设）")
	}
}

// ATK-24：脚本不能指定其他主机（否则它就是绕过 SSRF 守卫的通道）。
func TestScriptCannotEscapeBaseURL(t *testing.T) {
	a, srv := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("不应发出请求")
	})
	cases := []string{
		`return { method: "GET", path: "http://169.254.169.254/latest/meta-data/", responsePath: "x" };`,
		`return { method: "GET", path: "https://evil.example/steal", responsePath: "x" };`,
		`return { method: "GET", path: "/a/../../etc/passwd", responsePath: "x" };`,
	}
	for _, sc := range cases {
		_, err := a.Invoke(context.Background(), credFor(srv), provider.Request{
			Capability: provider.CapImageGenerate,
			Params:     map[string]any{"script": sc},
		})
		if err == nil {
			t.Fatalf("应当被拒绝: %s", sc)
		}
		var pe *provider.ProviderError
		if !asProvider(err, &pe) || pe.Code != platform.CodeForbidden {
			t.Fatalf("应当是 forbidden，实际 %v", err)
		}
	}
}

func TestSandboxRejectionSurfacesAsInvalidRequestNot500(t *testing.T) {
	// 脚本写了 eval → 用户输入问题，返回 invalid_request；
	// 若返回 500，用户会以为平台坏了，运维会被无关告警叫醒。
	a, srv := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("不应发出请求")
	})
	_, err := a.Invoke(context.Background(), credFor(srv), provider.Request{
		Capability: provider.CapImageGenerate,
		Params:     map[string]any{"script": `return { x: eval("1") };`},
	})
	var pe *provider.ProviderError
	if !asProvider(err, &pe) || pe.Code != platform.CodeForbidden {
		t.Fatalf("应当是 forbidden（想干坏事）而不是 internal，实际 %v", err)
	}
	if pe.Class != provider.ClassPermanent {
		t.Fatalf("脚本错误必须是永久错误（重试不会变好），实际 %v", pe.Class)
	}
}

func TestRequiresResponsePathInSyncMode(t *testing.T) {
	a, srv := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("不应发出请求")
	})
	_, err := a.Invoke(context.Background(), credFor(srv), provider.Request{
		Capability: provider.CapImageGenerate,
		Params:     map[string]any{"script": `return { method: "POST", path: "/x" };`},
	})
	if err == nil || !strings.Contains(err.Error(), "responsePath") {
		t.Fatalf("缺少 responsePath 应当报出可行动的错误，实际 %v", err)
	}
}

func TestMissingPathIsActionable(t *testing.T) {
	a, srv := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{}}`))
	})
	_, err := a.Invoke(context.Background(), credFor(srv), provider.Request{
		Capability: provider.CapImageGenerate,
		Params: map[string]any{"script": `
			return { method: "POST", path: "/x", responsePath: "data.images[0].url" };
		`},
	})
	if err == nil || !strings.Contains(err.Error(), "data.images[0].url") {
		t.Fatalf("找不到路径时应当回显路径本身，实际 %v", err)
	}
}

func TestBase64ResponseBecomesAsset(t *testing.T) {
	a, srv := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"b64_json":"aGVsbG8="}]}`))
	})
	res, err := a.Invoke(context.Background(), credFor(srv), provider.Request{
		Capability: provider.CapImageGenerate,
		Params:     map[string]any{"script": `return { method: "POST", path: "/x", responsePath: "data[0].b64_json" };`},
	})
	if err != nil {
		t.Fatalf("失败: %v", err)
	}
	if len(res.Assets) != 1 || string(res.Assets[0].Bytes) != "hello" {
		t.Fatalf("base64 解析错误: %#v", res.Assets)
	}
}

func TestTextResponse(t *testing.T) {
	a, srv := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"你好"}}]}`))
	})
	res, err := a.Invoke(context.Background(), credFor(srv), provider.Request{
		Capability: provider.CapTextGenerate,
		Params:     map[string]any{"script": `return { method: "POST", path: "/x", responsePath: "choices[0].message.content" };`},
	})
	if err != nil {
		t.Fatalf("失败: %v", err)
	}
	if res.Text != "你好" {
		t.Fatalf("文本解析错误: %q", res.Text)
	}
}

func TestAsyncModeReturnsRemoteTask(t *testing.T) {
	a, srv := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"taskId":"t-123"}`))
	})
	res, err := a.Invoke(context.Background(), credFor(srv), provider.Request{
		Capability: provider.CapVideoGenerate,
		Params:     map[string]any{"script": `return { method: "POST", path: "/x", mode: "async" };`},
	})
	if err != nil {
		t.Fatalf("失败: %v", err)
	}
	if res.RemoteTask == nil || res.RemoteTask.ID != "t-123" {
		t.Fatalf("异步任务解析错误: %#v", res.RemoteTask)
	}
}

func TestEmptyScriptIsRejectedBeforeRequest(t *testing.T) {
	a, srv := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("不应发出请求")
	})
	_, err := a.Invoke(context.Background(), credFor(srv), provider.Request{
		Capability: provider.CapImageGenerate,
	})
	// 错误必须**可行动**：说清「去哪里配置」，而不是只说「script 为空」。
	if err == nil || !strings.Contains(err.Error(), "自定义调用脚本") {
		t.Fatalf("空脚本应当被拒绝并指出配置位置，实际 %v", err)
	}
}

func TestScriptSeesInputMetadataButNotBytes(t *testing.T) {
	var body map[string]any
	a, srv := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = w.Write([]byte(`{"url":"https://cdn.example/x.png"}`))
	})
	_, err := a.Invoke(context.Background(), credFor(srv), provider.Request{
		Capability: provider.CapImageEdit,
		Inputs: []provider.ResolvedInput{
			{Kind: "image", AssetID: "as_1", Label: "图片1", Value: "data:image/png;base64,AAAAAAAA"},
		},
		Params: map[string]any{"script": `
			return { method: "POST", path: "/x", responsePath: "url",
				body: { hasRef: inputs[0].hasData, refId: inputs[0].assetId, label: inputs[0].label } };
		`},
	})
	if err != nil {
		t.Fatalf("失败: %v", err)
	}
	if body["hasRef"] != true || body["refId"] != "as_1" || body["label"] != "图片1" {
		t.Fatalf("输入元信息错误: %#v", body)
	}
	// 关键断言：入参里的 dataURI 从未出现在请求体里
	raw, _ := json.Marshal(body)
	if strings.Contains(string(raw), "AAAA") {
		t.Fatalf("脚本不该拿到字节内容: %s", raw)
	}
}

func TestNoRetryOnPermanentScriptError(t *testing.T) {
	// 脚本错误是永久错误：重试 3 次只会让用户多等几秒并看到重复日志。
	calls := 0
	a, srv := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{}`))
	})
	_, _ = a.Invoke(context.Background(), credFor(srv), provider.Request{
		Capability: provider.CapImageGenerate,
		Params:     map[string]any{"script": `return { method: "POST", path: "/x", responsePath: "nope" };`},
	})
	if calls != 1 {
		t.Fatalf("不应重试，实际请求 %d 次", calls)
	}
}

func asProvider(err error, out **provider.ProviderError) bool {
	pe, ok := err.(*provider.ProviderError)
	if ok {
		*out = pe
	}
	return ok
}

var _ = io.Discard

// TestScriptComesFromCredentialLimits：脚本的主要来源是**凭据的 limits**，
// 而不是每次调用的 params。
//
// 这条用例来自一次 e2e 发现：limits 当时只存库、没有被解析进 Credential，
// 于是「脚本保存成功但从未生效」，而调用侧看到的是「没有可用凭据」——
// 一个与真实原因毫无关系的错误。
func TestScriptComesFromCredentialLimits(t *testing.T) {
	var gotPath string
	a, srv := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"url":"https://cdn.example/x.png"}`))
	})
	cred := credFor(srv)
	cred.Limits = map[string]any{
		"script": `return { method: "POST", path: "/from-credential", responsePath: "url" };`,
	}
	if _, err := a.Invoke(context.Background(), cred, provider.Request{
		Capability: provider.CapImageGenerate,
	}); err != nil {
		t.Fatalf("凭据里的脚本应当生效: %v", err)
	}
	if gotPath != "/from-credential" {
		t.Fatalf("脚本未被读取，实际请求路径 %q", gotPath)
	}
}

// TestCredentialScriptWinsOverParamsScript：调试通道不能覆盖生产配置。
func TestCredentialScriptWinsOverParamsScript(t *testing.T) {
	var gotPath string
	a, srv := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"url":"https://cdn.example/x.png"}`))
	})
	cred := credFor(srv)
	cred.Limits = map[string]any{
		"script": `return { method: "POST", path: "/production", responsePath: "url" };`,
	}
	if _, err := a.Invoke(context.Background(), cred, provider.Request{
		Capability: provider.CapImageGenerate,
		Params: map[string]any{
			"script": `return { method: "POST", path: "/debug", responsePath: "url" };`,
		},
	}); err != nil {
		t.Fatalf("调用失败: %v", err)
	}
	if gotPath != "/production" {
		t.Fatalf("params.script 不应覆盖凭据里的脚本，实际 %q", gotPath)
	}
}

// TestCredentialIntLimit：limits 里的数字经 JSON 往返后是 float64，
// 直接类型断言会失败（脚本超时上限这类字段会静默回落到默认值）。
func TestCredentialIntLimit(t *testing.T) {
	cred := provider.Credential{Limits: map[string]any{"timeoutMs": float64(120)}}
	if got := cred.IntLimit("timeoutMs", 50); got != 120 {
		t.Fatalf("float64 应当被正确读出，实际 %d", got)
	}
	if got := cred.IntLimit("missing", 50); got != 50 {
		t.Fatalf("缺失时应当返回默认值，实际 %d", got)
	}
	if got := (provider.Credential{}).IntLimit("x", 7); got != 7 {
		t.Fatalf("nil Limits 应当返回默认值，实际 %d", got)
	}
}
