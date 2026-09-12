package provider

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/context-flow/ic/internal/api"
	"github.com/context-flow/ic/internal/platform"
)

// Service 实现 api.ProviderService：渠道与凭据的持久化 + 模型列表 + 连通性探测。
//
// 关键设计（docs/design/02-domain-model §2.5 / INV-5）：
//   - secret 只进不出：CreateCredential 之后任何接口都只返回 masked；
//   - 解密只发生在「构造 Adapter 的瞬间」，不在 DTO 转换路径上；
//   - 渠道可被多凭据共享（同一渠道换 key 不需要重建节点）。
type Service struct {
	db      *sql.DB
	secrets SecretStore
	guard   *platform.NetGuard
	client  platform.HTTPDoer
	clock   platform.Clock
	ids     platform.IDGen
	// resolver 由装配层注入：ValidateProviderPlan 需要它拿适配器。
	resolver *CredentialResolver
}

// Options 构造参数。
type Options struct {
	DB      *sql.DB
	Secrets SecretStore
	// Guard / Client 用于连通性探测；与执行链路共用同一套 SSRF 防护（ATK-04）。
	Guard  *platform.NetGuard
	Client platform.HTTPDoer
	Clock  platform.Clock
	IDs    platform.IDGen
}

// New 构造渠道服务。
func New(o Options) *Service {
	if o.Clock == nil {
		o.Clock = platform.SystemClock()
	}
	if o.IDs == nil {
		o.IDs = platform.DefaultIDGen()
	}
	if o.Guard == nil {
		o.Guard = platform.NewNetGuard()
	}
	return &Service{db: o.DB, secrets: o.Secrets, guard: o.Guard, client: o.Client, clock: o.Clock, ids: o.IDs}
}

// ListProviders 见 api.ProviderService。
func (s *Service) ListProviders(ctx context.Context, wsID string) ([]api.ProviderDTO, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT p.id, p.name, p.kind, p.base_url, p.auth_kind, COALESCE(p.capabilities, ''), COALESCE(p.enabled, 1),
		       (SELECT COUNT(*) FROM provider_credentials c WHERE c.provider_id = p.id AND c.enabled = 1) AS creds
		FROM providers p WHERE p.workspace_id = ? ORDER BY p.name`, wsID)
	if err != nil {
		return nil, platform.AsError(err)
	}
	defer rows.Close()
	out := []api.ProviderDTO{}
	for rows.Next() {
		var (
			id, name, kind, baseURL, authKind, caps string
			enabled, creds                          int
		)
		if err := rows.Scan(&id, &name, &kind, &baseURL, &authKind, &caps, &enabled, &creds); err != nil {
			return nil, platform.AsError(err)
		}
		out = append(out, api.ProviderDTO{
			ID: id, Workspace: wsID, Kind: kind, Name: name,
			Capabilities: splitCSV(caps), BaseURL: baseURL, AuthKind: authKind, Enabled: enabled == 1,
		})
	}
	return out, rows.Err()
}

// CreateProvider 见 api.ProviderService。同名/同 id 复用已有记录（幂等）。
func (s *Service) CreateProvider(ctx context.Context, wsID string, in api.ProviderInput) (*api.ProviderDTO, error) {
	if wsID == "" {
		return nil, platform.ErrInvalid("workspaceId is required")
	}
	id := strings.TrimSpace(in.ID)
	if id == "" {
		id = "p_" + strings.ToLower(strings.ReplaceAll(strings.TrimSpace(in.Name), " ", "-"))
	}
	if !validID(id) {
		return nil, platform.NewError(422, platform.CodeInvalidID, "provider id 只允许字母、数字、- 和 _").WithDetail("field", "id")
	}
	if strings.TrimSpace(in.BaseURL) == "" {
		return nil, platform.ErrInvalid("baseUrl is required")
	}
	authKind := in.AuthKind
	if authKind == "" {
		authKind = "bearer"
	}
	caps := in.Capabilities
	if len(caps) == 0 {
		// 未声明能力时给一个保守默认（只读模型列表），避免「默认全能力」引发意外计费。
		caps = []string{string(CapModelList)}
	}
	for _, c := range caps {
		if !Valid(Capability(c)) {
			return nil, platform.ErrInvalid("unknown capability: " + c)
		}
	}
	now := fmtTime(s.clock.Now())
	// providers 的主键是 (workspace_id, id)：同一 id 在不同工作区互不影响，
	// 因此冲突目标是两列而不是单列。写错会让「两个工作区各建一个 openai 渠道」直接 500。
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO providers (id, workspace_id, name, kind, base_url, auth_kind, capabilities, enabled, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, 1, ?)
		ON CONFLICT(workspace_id, id) DO UPDATE SET
		  name = excluded.name, base_url = excluded.base_url,
		  auth_kind = excluded.auth_kind, capabilities = excluded.capabilities`,
		id, wsID, in.Name, firstNonEmpty(in.Kind, "custom"), in.BaseURL, authKind, strings.Join(caps, ","), now); err != nil {
		return nil, platform.AsError(err)
	}
	return &api.ProviderDTO{
		ID: id, Workspace: wsID, Kind: firstNonEmpty(in.Kind, "custom"), Name: in.Name,
		Capabilities: caps, BaseURL: in.BaseURL, AuthKind: authKind, Enabled: true,
	}, nil
}

// CreateCredential 见 api.ProviderService。
func (s *Service) CreateCredential(ctx context.Context, wsID, providerID string, in api.CredentialInput) (*api.CredentialDTO, error) {
	if s.secrets == nil {
		return nil, platform.NewError(501, platform.CodeNotImplemented, "secret store is not configured")
	}
	// 先读渠道元数据：下面的「是否脚本渠道」判断需要它。
	var baseURL, providerKind string
	if err := s.db.QueryRowContext(ctx,
		`SELECT base_url, kind FROM providers WHERE id = ? AND workspace_id = ?`, providerID, wsID).
		Scan(&baseURL, &providerKind); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, platform.ErrNotFound("provider") // 不泄露存在性
		}
		return nil, platform.AsError(err)
	}
	// script 渠道可以没有密钥：脚本自己描述协议，鉴权由平台按 authKind 注入
	//（可能是「不需要鉴权」的内网服务）。这里强制要求 secret 会让
	// 「自定义脚本 + 无鉴权」这一合法组合直接不可配置 —— 而它的报错是
	// 「secret is required」，与用户真正想做的事（保存脚本）毫无关系。
	//
	// 其余渠道仍然强制：它们全部依赖密钥，缺密钥只会在调用时失败。
	isScript := strings.EqualFold(providerKind, "script")
	if strings.TrimSpace(in.Secret) == "" && !isScript {
		return nil, platform.ErrInvalid("secret is required")
	}
	if isScript {
		// 反向约束：脚本渠道**必须**带脚本。允许一个没有脚本的脚本渠道，
		// 用户会在生成时拿到「未配置调用脚本」——那时他已经在等结果了。
		if script, _ := in.Limits["script"].(string); strings.TrimSpace(script) == "" {
			return nil, platform.NewError(422, platform.CodeInvalidRequest,
				"脚本渠道必须同时提供调用脚本（limits.script）")
		}
	}
	sealed, err := s.secrets.Seal(in.Secret)
	if err != nil {
		return nil, err
	}
	id := s.ids.NewID("cred")
	masked := MaskSecret(in.Secret)
	limits, _ := json.Marshal(orEmptyMap(in.Limits))
	now := fmtTime(s.clock.Now())
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO provider_credentials (id, workspace_id, provider_id, name, secret_ref, masked, priority, limits, enabled, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 1, ?)`,
		id, wsID, providerID, firstNonEmpty(in.Name, "默认"), sealed, masked, in.Priority, string(limits), now); err != nil {
		return nil, platform.AsError(err)
	}
	return &api.CredentialDTO{
		ID: id, Provider: providerID, Name: firstNonEmpty(in.Name, "默认"),
		Masked: masked, Priority: in.Priority, Limits: in.Limits, Enabled: true, CreatedAt: s.clock.Now().UTC(),
	}, nil
}

// TestProvider 见 api.ProviderService：探测连通性并返回模型列表。
//
// 出网统一经 provider.Transport：它自带 SSRF 防护（ATK-04）、超时、退避与脱敏，
// 与真实生成链路走同一出口。**探测不能另开一条裸 http.Client**——
// 那样会让「探测能通、真实调用被拦」这种前后不一致成为可能。
func (s *Service) TestProvider(ctx context.Context, wsID, providerID string) (*api.ProbeResult, error) {
	if s.client == nil {
		return nil, platform.NewError(501, platform.CodeNotImplemented, "http client is not configured")
	}
	secret, baseURL, kind, err := s.resolveFirstCredential(ctx, wsID, providerID)
	if err != nil {
		return nil, err
	}
	start := time.Now()
	transport := NewTransport(s.client, s.guard, s.clock)
	url := strings.TrimRight(baseURL, "/") + modelsPath(kind)
	headers := map[string]string{}
	if secret != "" {
		headers["Authorization"] = "Bearer " + secret
	}
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := transport.DoJSON(ctx, "GET", url, headers, nil, &payload); err != nil {
		return probeFail(err, start), nil
	}
	names := make([]string, 0, len(payload.Data)+len(payload.Models))
	for _, m := range payload.Data {
		names = append(names, m.ID)
	}
	for _, m := range payload.Models {
		names = append(names, strings.TrimPrefix(m.Name, "models/"))
	}
	sort.Strings(names)
	return &api.ProbeResult{OK: true, LatencyMS: int(time.Since(start).Milliseconds()), Models: names}, nil
}

// ListModels 见 api.ProviderService：按能力过滤，能力由关键词推断 + 显式配置覆盖。
func (s *Service) ListModels(ctx context.Context, wsID, capability string) ([]api.ModelDTO, error) {
	providers, err := s.ListProviders(ctx, wsID)
	if err != nil {
		return nil, err
	}
	out := []api.ModelDTO{}
	for _, p := range providers {
		if !p.Enabled {
			continue
		}
		rows, err := s.db.QueryContext(ctx,
			`SELECT id, display_name, capabilities FROM models WHERE provider_id = ? ORDER BY id`, p.ID)
		if err == nil {
			for rows.Next() {
				var id, display, caps string
				if err := rows.Scan(&id, &display, &caps); err != nil {
					break
				}
				dto := api.ModelDTO{
					ID: id, ProviderID: p.ID, DisplayName: display,
					Capabilities: splitCSV(caps), Healthy: true,
				}
				if capability == "" || contains(dto.Capabilities, capability) {
					out = append(out, dto)
				}
			}
			rows.Close()
		}
		// 已配置模型为空时，退化为「按渠道声明的能力给出占位条目」：
		// 让前端至少能选到渠道，而不是显示空列表后无从下手。
		if capability != "" && contains(p.Capabilities, capability) && !hasProvider(out, p.ID) {
			out = append(out, api.ModelDTO{
				ID: idPlaceholder(p.ID), ProviderID: p.ID,
				Capabilities: []string{capability}, Healthy: true,
			})
		}
	}
	return out, nil
}

// resolveFirstCredential 解密优先级最高的凭据（只在构造请求时调用）。
func (s *Service) resolveFirstCredential(ctx context.Context, wsID, providerID string) (string, string, string, error) {
	var sealedRef, baseURL, kind string
	err := s.db.QueryRowContext(ctx, `
		SELECT c.secret_ref, p.base_url, p.kind
		FROM providers p
		JOIN provider_credentials c ON c.provider_id = p.id AND c.enabled = 1
		WHERE p.id = ? AND p.workspace_id = ?
		ORDER BY c.priority DESC LIMIT 1`, providerID, wsID).Scan(&sealedRef, &baseURL, &kind)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", "", platform.ErrInvalid("该渠道还没有可用凭据")
	}
	if err != nil {
		return "", "", "", platform.AsError(err)
	}
	if s.secrets == nil {
		return "", baseURL, kind, nil
	}
	secret, err := s.secrets.Open(sealedRef)
	if err != nil {
		return "", "", "", err
	}
	return secret, baseURL, kind, nil
}

func probeFail(err error, start time.Time) *api.ProbeResult {
	de := platform.AsDomainError(err)
	code := platform.CodeInternal
	if de != nil {
		code = de.Code
	}
	return &api.ProbeResult{
		OK: false, LatencyMS: int(time.Since(start).Milliseconds()),
		Error: &api.ErrDTO{Code: code, Message: platform.Redact(err.Error())},
	}
}

func modelsPath(kind string) string {
	if kind == "gemini" {
		return "/v1beta/models"
	}
	return "/v1/models"
}

func hasProvider(list []api.ModelDTO, pid string) bool {
	for _, m := range list {
		if m.ProviderID == pid {
			return true
		}
	}
	return false
}

func idPlaceholder(pid string) string { return pid + "/default" }

// GuessCapabilities 按模型名关键词猜测能力。
// 与原项目 guessCapability 对齐（docs/design/10 §8.3）：猜测只是默认值，
// 用户可在界面上覆盖；猜错不会导致「不能选」，只会导致「默认勾选错」。
func GuessCapabilities(modelID string) []string {
	m := strings.ToLower(modelID)
	switch {
	case strings.Contains(m, "upscale") || strings.Contains(m, "superres"):
		return []string{string(CapImageUpscale)}
	case strings.Contains(m, "embed") || strings.Contains(m, "moderation"):
		return []string{string(CapTextTools)}
	case strings.Contains(m, "tts") || strings.Contains(m, "audio") || strings.Contains(m, "speech"):
		return []string{string(CapAudioGenerate)}
	case strings.Contains(m, "video") || strings.Contains(m, "veo") || strings.Contains(m, "sora") || strings.Contains(m, "kling"):
		return []string{string(CapVideoGenerate)}
	case strings.Contains(m, "dall") || strings.Contains(m, "image") || strings.Contains(m, "flux") ||
		strings.Contains(m, "sd") || strings.Contains(m, "imagen") || strings.Contains(m, "gpt-image"):
		return []string{string(CapImageGenerate), string(CapImageEdit)}
	default:
		return []string{string(CapTextGenerate), string(CapModelList)}
	}
}

// MaskSecret 生成掩码：只保留首尾少量字符。
// 刻意不保留完整长度信息（避免「长度侧信道」），但保留前缀便于用户辨认是哪个 key。
func MaskSecret(secret string) string {
	s := strings.TrimSpace(secret)
	if len(s) <= 8 {
		return "****"
	}
	return s[:4] + "****" + s[len(s)-4:]
}

func splitCSV(s string) []string {
	out := []string{}
	for _, part := range strings.Split(s, ",") {
		if t := strings.TrimSpace(part); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func contains(list []string, v string) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}

func orEmptyMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func validID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.', r == '/':
		default:
			return false
		}
	}
	return true
}

func fmtTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// CredentialResolver 按能力选凭据。exec 通过它拿 Adapter + 解密后的凭据。
//
// 缓存策略：只缓存**渠道元数据**（与凭据存在性），不缓存明文 secret——
// 缓存明文会让「密钥轮换后仍用旧 key 发请求」变成静默故障。
type CredentialResolver struct {
	svc *Service
	// pricing 由外部注入（价格表属于计费领域，不属于凭据存储）。
	// 不注入时返回零值，UI 会标注「未配置价格」。
	pricing func(providerID, model string) Pricing

	mu       sync.RWMutex
	adapters map[string]Adapter
	// lastErr 记录最近一次解析失败的原因（原子替换，读多写少）。
	//
	// 用 atomic.Pointer[error] 而不是 atomic.Value：后者要求所有 Store
	// 的值**类型完全一致**，而这里会存 *DomainError / *fmt.wrapError
	// 等多种具体类型——实测会在第二次 Store 时 panic
	//（"inconsistently typed value"），即「第一次失败正常、第二次崩溃」。
	lastErr atomic.Pointer[error]
}

// NewCredentialResolver 构造解析器。
func NewCredentialResolver(svc *Service) *CredentialResolver {
	return &CredentialResolver{svc: svc, adapters: map[string]Adapter{}}
}

// SetPricing 注入价格表查询。
func (r *CredentialResolver) SetPricing(f func(providerID, model string) Pricing) {
	r.pricing = f
}

// Pricing 见 exec.AdapterRegistry。
func (r *CredentialResolver) Pricing(providerID, model string) Pricing {
	if r.pricing == nil {
		return Pricing{}
	}
	return r.pricing(providerID, model)
}

// Register 注册某种协议对应的 Adapter 工厂（openai / gemini）。
func (r *CredentialResolver) Register(providerID string, a Adapter) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.adapters[providerID] = a
}

// Adapter 见 exec.AdapterRegistry。
func (r *CredentialResolver) Adapter(providerID string) (Adapter, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.adapters[providerID]
	return a, ok
}

// PlanValidator 是「在提交前校验脚本产出」的可选能力。
//
// 只有 script 适配器实现它。用类型断言而不是把它加进 Adapter 接口：
// 其余适配器没有「用户提供的脚本」这一层，加一个永远返回 nil 的方法
// 会让接口表达力下降（读者无法从中看出「谁会真的校验」）。
type PlanValidator interface {
	ValidatePlan(ctx context.Context, cred Credential, req Request) error
}

// ValidateProviderPlan 在提交前跑一次脚本，校验产出的请求描述。
//
// 只在脚本渠道上有意义（PlanValidator 由 script 适配器实现）。
// 返回 nil 表示「要么不是脚本渠道，要么校验通过」——调用方不需要分支。
func (s *Service) ValidateProviderPlan(ctx context.Context, cap Capability, providerID, credentialID string, params map[string]any) error {
	adapter, cred, ok := s.Resolver().Resolve(cap, providerID, credentialID)
	if !ok || adapter == nil {
		return nil
	}
	pv, ok := adapter.(PlanValidator)
	if !ok {
		return nil
	}
	prompt, _ := params["prompt"].(string)
	inner, _ := params["params"].(map[string]any)
	count, _ := params["outputCount"].(int)
	refs := stringSliceFrom(params["references"])
	inputs := make([]ResolvedInput, 0, len(refs))
	for i, id := range refs {
		inputs = append(inputs, ResolvedInput{Kind: "image", AssetID: id, Label: "图片" + itoaStr(i+1)})
	}
	err := pv.ValidatePlan(ctx, cred, Request{
		Capability: cap, Model: stringFrom(params["model"]), Prompt: prompt,
		Params: inner, Count: count, Inputs: inputs,
	})
	return asDomain(err)
}

// asDomain 把适配器错误映射成领域错误。
//
// 为什么必须有这一层：适配器返回的是 `*provider.ProviderError`，
// 而它对 HTTP 层是**未知类型**——`writeError` 会把任何非 DomainError
// 转成 500「internal error」。于是一个「脚本写了保留头部」这种
// 纯用户输入问题，会以「平台内部错误」的面目出现。
//
// 映射规则：
//
//	ClassPermanent + CodeInvalidRequest  → 422
//	ClassPermanent + CodeForbidden       → 403
//	ClassPermanent + 其它                → 400
//	ClassRateLimited                     → 429
//	ClassTransient                       → 502（上游暂时不可用）
//
// 刻意保留原始 Code：它是稳定契约（前端按 code 查文案），
// 换成 HttpStatus 命名会破坏这一层。
func asDomain(err error) error {
	if err == nil {
		return nil
	}
	var pe *ProviderError
	if !errors.As(err, &pe) {
		return err
	}
	status := 400
	switch {
	case pe.Class == ClassRateLimited:
		status = 429
	case pe.Class == ClassTransient:
		status = 502
	case pe.Code == platform.CodeForbidden:
		status = 403
	case pe.Code == platform.CodeInvalidRequest, pe.Code == platform.CodeInvalidSpec:
		status = 422
	}
	de := platform.NewError(status, pe.Code, pe.Message)
	if pe.HTTPStatus != 0 {
		de = de.WithDetail("upstreamStatus", pe.HTTPStatus)
	}
	return de
}

// Resolver 返回内部解析器（装配层在构造后需要注册适配器）。
func (s *Service) Resolver() *CredentialResolver { return s.resolver }

// SetResolver 注入解析器（装配期调用一次）。
func (s *Service) SetResolver(r *CredentialResolver) { s.resolver = r }

func stringFrom(v any) string {
	s, _ := v.(string)
	return s
}

func stringSliceFrom(v any) []string {
	list, ok := v.([]string)
	if ok {
		return list
	}
	raw, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func itoaStr(n int) string {
	if n == 0 {
		return "0"
	}
	digits := ""
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	return digits
}

// Resolve 见 exec.AdapterRegistry：按能力与优先级挑选凭据。
//
// 失败时**把原因记下来**（ResolveError）：签名只有 bool，调用方只能报
// 「没有可用凭据」，而真实原因可能是「渠道没声明该能力」「适配器没注册」
// 「密钥解不开」——三者的处理方式完全不同。
// 曾经这里静默丢弃 err，于是 e2e 里「凭据齐全却报没有凭据」无法定位。
func (r *CredentialResolver) Resolve(cap Capability, providerID, credentialID string) (Adapter, Credential, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cred, err := r.svc.resolveCredentialFor(ctx, cap, providerID, credentialID)
	if err != nil {
		storeErr(&r.lastErr, err)
		return nil, Credential{}, false
	}
	a, ok := r.Adapter(cred.ProviderID)
	if !ok {
		storeErr(&r.lastErr, fmt.Errorf("渠道 %s 的协议 %q 没有注册适配器（已注册：%v）",
			cred.SourceProviderID, cred.ProviderID, r.registeredIDs()))
		return nil, Credential{}, false
	}
	// 必须存一个**具体**的 error 值：atomic.Value 的 Store(nil) 会 panic
	//（"store of nil value into Value"），而这是一个会被 recover 中间件
	// 转成 500 的崩溃。用哨兵值表示「上次成功」。
	storeErr(&r.lastErr, errNone)
	return a, cred, true
}

// storeErr 把 error 放进 atomic.Pointer[error]。
//
// 需要一个**中间变量**：`atomic.Pointer[T].Store` 要求的是 *T，
// 直接传 `err`（接口值）会被编译器拒绝；而 `&err` 在 err 是 nil 时
// 仍然合法（指针非 nil，指向一个 nil 接口值），因此这里不需要额外判空。
func storeErr(slot *atomic.Pointer[error], err error) {
	slot.Store(&err)
}

// LastError 返回最近一次 Resolve 失败的原因（成功时返回 nil）。
//
// 用于把「没有可用凭据」这条笼统错误变成可行动的原因。刻意不返回
// 凭据本身：调用方只需要「为什么失败」。
func (r *CredentialResolver) LastError() error {
	p := r.lastErr.Load()
	if p == nil || *p == nil || errors.Is(*p, errNone) {
		return nil
	}
	return *p
}

// errNone 是「上次解析成功」的哨兵。
//
// 用意：atomic.Value 不允许存 nil，而「成功」这个状态必须能表达
// （否则 LastError 会一直返回上一次的失败原因，把已修好的问题报成当前问题）。
var errNone = errors.New("no error")

func (r *CredentialResolver) registeredIDs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.adapters))
	for k := range r.adapters {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// SaveModels 保存渠道下勾选的模型与能力（见 api.ModelSaver）。
//
// 校验归属：只能给本工作区的渠道保存模型，否则会跨工作区污染。
func (s *Service) SaveModels(ctx context.Context, wsID, providerID string, models []api.ModelDTO) ([]api.ModelDTO, error) {
	var exists int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM providers WHERE id = ? AND workspace_id = ?`, providerID, wsID).Scan(&exists); err != nil {
		return nil, platform.AsError(err)
	}
	if exists == 0 {
		return nil, platform.ErrNotFound("provider")
	}
	for _, m := range models {
		if !validID(m.ID) {
			return nil, platform.NewError(422, platform.CodeInvalidID, "模型 id 不合法: "+platform.Redact(m.ID))
		}
		caps := m.Capabilities
		if len(caps) == 0 {
			caps = GuessCapabilities(m.ID)
		}
		for _, c := range caps {
			if !Valid(Capability(c)) {
				return nil, platform.ErrInvalid("未知能力: " + c)
			}
		}
		if _, err := s.db.ExecContext(ctx, `
			INSERT INTO models (id, provider_id, display_name, capabilities, updated_at)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(provider_id, id) DO UPDATE SET
			  display_name = excluded.display_name,
			  capabilities = excluded.capabilities,
			  updated_at = excluded.updated_at`,
			m.ID, providerID, m.DisplayName, strings.Join(caps, ","), fmtTime(s.clock.Now())); err != nil {
			return nil, platform.AsError(err)
		}
	}
	return s.ListModels(ctx, wsID, "")
}

// resolveCredentialFor 按能力找渠道 → 取最高优先级凭据 → 解密。
func (s *Service) resolveCredentialFor(ctx context.Context, cap Capability, providerID, credentialID string) (Credential, error) {
	// 选 p.kind（协议名）而不是 p.id：适配器按**协议**注册（openai/gemini/script），
	// 而 p.id 是用户可自定义的渠道标识（`my-relay`、`dbg-1789...`）。
	// 曾经这里返回 p.id 作为 ProviderID，于是「适配器注册表里找不到它」，
	// 表现是「凭据齐全却报没有可用凭据」——而报错内容与真实原因毫无关系。
	// 只有种子里恰好用 `openai` 作渠道 id 时看起来正常，所以这个缺陷
	// 一直没被本地用例发现（e2e 用自定义 id 才暴露）。
	query := `
		SELECT p.id, p.kind, p.base_url, p.auth_kind, c.id, c.secret_ref, c.priority, COALESCE(c.limits, '{}')
		FROM providers p
		JOIN provider_credentials c ON c.provider_id = p.id AND c.enabled = 1
		WHERE p.enabled = 1 AND c.workspace_id = p.workspace_id`
	args := []any{}
	if providerID != "" {
		query += " AND p.id = ?"
		args = append(args, providerID)
	}
	if credentialID != "" {
		query += " AND c.id = ?"
		args = append(args, credentialID)
	}
	query += " ORDER BY c.priority DESC LIMIT 20"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return Credential{}, platform.AsError(err)
	}
	defer rows.Close()
	n := 0
	// lastReason 累积「为什么这些候选都不行」，用于把笼统的
	// 「没有可用凭据」变成可行动的原因（渠道没声明能力 / 密钥解不开）。
	lastReason := ""
	for rows.Next() {
		n++
		var (
			pid, kind, baseURL, authKind, cid, sealedRef string
			limitsRaw                                    string
			priority                                     int
		)
		if err := rows.Scan(&pid, &kind, &baseURL, &authKind, &cid, &sealedRef, &priority, &limitsRaw); err != nil {
			return Credential{}, platform.AsError(err)
		}
		// limits 解析失败不阻断取凭据：它承载的是「怎么调这个渠道」这类
		// 配置，缺了它应当表现为「该渠道提示需要配置脚本」，而不是
		// 「整个渠道不可用」——后者会让用户以为凭据丢了。
		limits := map[string]any{}
		if limitsRaw != "" {
			_ = json.Unmarshal([]byte(limitsRaw), &limits)
		}
		// 能力过滤放在 Go 侧：capabilities 是逗号分隔文本，SQL 里做集合匹配会失去可读性，
		// 而候选集只有几十行，性能不是问题。
		caps, err := s.providerCapabilities(ctx, pid)
		if err != nil {
			lastReason = fmt.Sprintf("渠道 %s 的能力读取失败: %v", pid, err)
			continue
		}
		if !contains(caps, string(cap)) {
			lastReason = fmt.Sprintf("渠道 %s 未声明能力 %s（已声明：%v）", pid, cap, caps)
			continue
		}
		secret, err := s.secrets.Open(sealedRef)
		if err != nil {
			lastReason = "凭据解密失败: " + platform.Redact(err.Error())
			continue
		}
		return Credential{
			// ProviderID 是**协议名**（用于选适配器）；渠道行 id 记在 SourceProviderID。
			ID: cid, ProviderID: firstNonEmpty(kind, "custom"), SourceProviderID: pid,
			BaseURL: baseURL, AuthKind: authKind,
			Secret: secret, Priority: priority, Limits: limits,
		}, nil
	}
	if lastReason == "" {
		lastReason = fmt.Sprintf("没有匹配的渠道与凭据（cap=%s providerId=%q credentialId=%q，候选行=%d）",
			cap, providerID, credentialID, n)
	}
	return Credential{}, platform.NewError(422, platform.CodeInvalidRequest, lastReason)
}

func (s *Service) providerCapabilities(ctx context.Context, pid string) ([]string, error) {
	var caps string
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(capabilities, '') FROM providers WHERE id = ?`, pid).Scan(&caps); err != nil {
		return nil, err
	}
	return splitCSV(caps), nil
}

// SaveModelsRaw 保存渠道的模型列表（拉取后落库，避免每次进页面都请求上游）。
func (s *Service) SaveModelsRaw(ctx context.Context, providerID string, models []api.ModelDTO) error {
	for _, m := range models {
		caps := m.Capabilities
		if len(caps) == 0 {
			caps = GuessCapabilities(m.ID)
		}
		if _, err := s.db.ExecContext(ctx, `
			INSERT INTO models (id, provider_id, display_name, capabilities)
			VALUES (?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET display_name = excluded.display_name, capabilities = excluded.capabilities`,
			m.ID, providerID, m.DisplayName, strings.Join(caps, ",")); err != nil {
			return platform.AsError(err)
		}
	}
	return nil
}
