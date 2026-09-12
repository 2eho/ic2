// Package wiring 把各领域服务装配成可运行的应用（见 docs/design/03-backend.md §1）。
package wiring

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/context-flow/ic/internal/agent"
	"github.com/context-flow/ic/internal/api"
	"github.com/context-flow/ic/internal/asset"
	"github.com/context-flow/ic/internal/exec"
	"github.com/context-flow/ic/internal/graph"
	"github.com/context-flow/ic/internal/identity"
	"github.com/context-flow/ic/internal/platform"
	"github.com/context-flow/ic/internal/plugin"
	"github.com/context-flow/ic/internal/prompt"
	"github.com/context-flow/ic/internal/provider"
	"github.com/context-flow/ic/internal/provider/adapter/gemini"
	"github.com/context-flow/ic/internal/provider/adapter/openai"
	"github.com/context-flow/ic/internal/provider/adapter/script"
	"github.com/context-flow/ic/internal/sandbox"
	"github.com/context-flow/ic/internal/workspace"
	"github.com/context-flow/ic/migrations"
)

// App 是装配完成的应用。所有依赖都是具体类型，便于 CLI 与测试直接取用。
type App struct {
	Config platform.Config
	Logger *slog.Logger
	DB     *platform.DB

	Graph     *graph.Service
	Assets    *asset.Service
	Runs      *exec.Engine
	Providers *provider.Service
	Prompts   *prompt.Service
	Plugins   *plugin.Service
	Agent     *agent.Service
	MCP       api.MCPService
	Auth      *identity.Service
	Prefs     *workspace.Service

	// resolver 供提交前校验（缺凭据要在提交时就报错，而不是异步失败）。
	resolver *provider.CredentialResolver

	Router http.Handler
}

// Options 装配参数。
type Options struct {
	Config platform.Config
	Logger *slog.Logger
	// Migrate 是否在装配时执行迁移（CLI 的 doctor/migrate 子命令不需要）。
	Migrate bool
	// StartWorker 是否启动后台 worker（恢复中断的运行 + 到期 GC）。
	StartWorker bool
}

// Build 装配全部服务。
//
// 这一层的存在本身就是对上一轮问题的修复：当时 `cmd/ic-server/main.go` 只装了 3 个依赖，
// exec/asset/provider/prompt/plugin/agent 全部为 nil，接口一律 501。
// 现在装配集中在这里，并由 `wiring/app_test.go` 对**装配结果**做端到端断言，
// 让「忘了接线」在 CI 就暴露。
func Build(ctx context.Context, o Options) (*App, error) {
	if o.Logger == nil {
		o.Logger = platform.NewLogger("info", "text")
	}
	cfg := o.Config

	db, err := platform.OpenDB(cfg)
	if err != nil {
		return nil, fmt.Errorf("打开数据库: %w", err)
	}
	if err := db.SetSQLitePragmas(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("设置 sqlite pragma: %w", err)
	}
	if o.Migrate {
		if err := db.Migrate(ctx, migrations.FS, "."); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("执行迁移: %w", err)
		}
	}

	clock := platform.SystemClock()
	ids := platform.DefaultIDGen()
	app := &App{Config: cfg, Logger: o.Logger, DB: db}

	// ---------------- 出网基础设施（唯一的出网出口，带 SSRF 防护） ----------------
	guard := platform.NewNetGuard()
	guard.AllowPrivate = cfg.SSRFAllowPrivate
	guard.ExtraAllow = cfg.SSRFAllowHosts
	httpClient := platform.DefaultHTTPClient(guard)
	transport := provider.NewTransport(httpClient, guard, clock)
	fetcher := NewGuardedFetcher(guard, httpClient)

	// ---------------- 画布 ----------------
	bus := graph.NewMemoryBus()
	graphStore := graph.NewSQLStore(db.DB, db.Dialect)
	app.Graph = graph.NewService(graphStore, bus, clock, ids)

	// ---------------- 资产 ----------------
	blobs, err := newBlobStore(cfg)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	app.Assets = asset.New(db.DB, blobs, clock, ids)

	// ---------------- 渠道与凭据 ----------------
	secrets, err := provider.NewAESSecretStore(cfg.EffectiveSecretKey())
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("初始化凭据加密: %w", err)
	}
	app.Providers = provider.New(provider.Options{
		DB: db.DB, Secrets: secrets, Guard: guard, Client: httpClient, Clock: clock, IDs: ids,
	})

	// ---------------- 执行引擎 ----------------
	resolver := provider.NewCredentialResolver(app.Providers)
	// 把解析器注入 Providers：提交前校验脚本产出需要它拿适配器。
	app.Providers.SetResolver(resolver)
	resolver.SetPricing(NewPricingTable(db.DB).Pricing)
	// 两个协议适配器共用同一个 Transport：超时/退避/SSRF/脱敏策略只有一处。
	resolver.Register("openai", openai.New(transport))
	resolver.Register("gemini", gemini.New(transport))
	resolver.Register("custom", openai.New(transport)) // 自定义渠道默认按 OpenAI 兼容协议
	// 自定义调用脚本（4.12 / 4.19）：脚本只描述请求，出口仍在服务端。
	// 这里显式注入沙箱，而不是让适配器内部 new —— 注入点让「沙箱有两个实现」
	// 变成真的（测试里可替换），也避免适配器偷偷放宽上限。
	scriptRunner := sandbox.New(sandbox.DefaultLimits())
	resolver.Register("script", script.New(transport, scriptRunner))
	// 编译器与执行引擎共用同一份凭据解析：否则会出现
	// 「手动触发能编译通过、Agent 触发编译失败」这种不一致。
	compiler := exec.NewCompiler(func(cap provider.Capability, providerID, credentialID string) (provider.Credential, bool) {
		_, cred, ok := resolver.Resolve(cap, providerID, credentialID)
		return cred, ok
	})

	runs := exec.New(exec.Options{
		Store:       exec.NewSQLStore(db.DB, db.Dialect),
		Blobs:       NewAssetBlobs(app.Assets, fetcher),
		Adapters:    resolver,
		Canvas:      NewCanvasWriter(app.Graph),
		Sink:        exec.NewMemorySink(),
		Clock:       clock,
		IDs:         ids,
		Concurrency: cfg.WorkerConcurrency,
	})
	runs.SetAssetReader(NewAssetReader(app.Assets))
	app.Runs = runs
	app.resolver = resolver

	// ---------------- 提示词 ----------------
	app.Prompts = prompt.New(prompt.Options{DB: db.DB, Guard: guard, Client: httpClient, Clock: clock, IDs: ids})

	// ---------------- 插件 ----------------
	app.Plugins = plugin.New(db.DB, clock, ids, fetcher, cfg.PluginRegistry)

	// ---------------- 身份 ----------------
	app.Auth = identity.New(db.DB, clock, ids, cfg)
	if cfg.OpenAccess {
		if err := app.Auth.EnsureOpenAccessBootstrap(ctx); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("开放访问引导用户: %w", err)
		}
		o.Logger.Info("IC_OPEN_ACCESS 已启用：引导本地用户就绪")
	}

	// ---------------- 工作区偏好（含凭据 at-rest 加密） ----------------
	app.Prefs = workspace.New(db.DB, cfg.EffectiveSecretKey(), clock)

	// ---------------- Agent ----------------
	agentSvc := agent.New(agent.Options{
		DB:     db.DB,
		Canvas: &agentCanvasGateway{graph: app.Graph, runs: runs},
		Clock:  clock,
		IDs:    ids,
		// 服务端形态的模型客户端：复用同一套渠道适配器与凭据解析。
		Model: newModelClient(app.Providers, transport, resolver),
	})
	// Agent 的外部能力注入（9.5–9.7、9.14）：每一项对应工具表里的一个能力组。
	//
	// defaultWorkspace 抽出来而不是内联：上游工具面里有两个能力
	//（canvas_list_projects / workbench_*）需要「不知道画布时用哪个工作区」，
	// 而这条规则必须与 MCP 的 resolveCanvas 完全一致 ——
	// 两处各写一份会出现「列画布用 A 工作区、执行用 B 工作区」这种错位。
	skillsStore := agent.NewSkillsSQLStore(db.DB, clock)
	defaultWorkspace := func(ctx context.Context) (string, error) {
		var wsID string
		if err := db.DB.QueryRowContext(ctx,
			`SELECT id FROM workspaces ORDER BY created_at LIMIT 1`).Scan(&wsID); err != nil {
			return "", platform.ErrNotFound("workspace")
		}
		return wsID, nil
	}
	agentSvc.SetCapabilities(agent.Wire{
		Runs:    &agentRuns{engine: runs, graph: app.Graph, compiler: compiler},
		Assets:  &agentAssets{assets: app.Assets, fetch: fetcher},
		Prompts: &agentPrompts{svc: app.Prompts},
		Skills:  skillsStore,
		Files:   &agentAssets{assets: app.Assets, fetch: fetcher},
		Projects: &agentProjects{
			graph: app.Graph, auth: app.Auth, defaultWorkspace: defaultWorkspace,
		},
		AdHoc: &agentAdHoc{
			engine: runs, runs: &runsAdapter{engine: runs, preflight: app.preflightGenerate},
			graph: app.Graph,
		},
	})
	app.Agent = agentSvc
	app.MCP = agent.NewMCPHandler(agentSvc, func(ctx context.Context) (string, string, error) {
		// MCP 客户端可能不带 workspace 上下文：默认取第一个工作区。
		var wsID, canvasID string
		if err := db.DB.QueryRowContext(ctx, `SELECT id FROM workspaces ORDER BY created_at LIMIT 1`).Scan(&wsID); err != nil {
			return "", "", platform.ErrNotFound("workspace")
		}
		_ = db.DB.QueryRowContext(ctx, `SELECT id FROM canvases WHERE deleted_at IS NULL ORDER BY updated_at DESC LIMIT 1`).Scan(&canvasID)
		return wsID, canvasID, nil
	})

	// ---------------- HTTP 层 ----------------
	app.Router = api.NewRouter(api.Deps{
		Config:    cfg,
		Logger:    o.Logger,
		Graph:     app.Graph,
		Assets:    app.Assets,
		Runs:      &runsAdapter{engine: runs, preflight: app.preflightGenerate},
		Providers: app.Providers,
		Prompts:   app.Prompts,
		Plugins:   app.Plugins,
		Agent:     agent.NewAPIService(agentSvc),
		Skills:    skillsStore,
		Prefs:     NewPrefsAdapter(app.Prefs),
		MCP:       app.MCP,
		Auth:      app.Auth,
		Meta:      &metaService{db: db},
		StaticFS:  staticFSOf(cfg),
	})
	if fsys, dir, ok := api.ResolveStaticDir(cfg.StaticDir); ok {
		_ = fsys
		app.Logger.Info("已挂载前端静态产物", "dir", dir)
	} else {
		app.Logger.Info("未找到前端产物，以纯 API 模式运行（可先执行 make web-build）")
	}

	if o.StartWorker {
		app.startBackground(ctx)
	}
	return app, nil
}

// Close 释放资源。
func (a *App) Close() error {
	if a.DB != nil {
		return a.DB.Close()
	}
	return nil
}

// newBlobStore 按配置构造 Blob 存储。非 fs 驱动给出明确的「未编译」提示，
// 而不是静默退化成内存实现（那会导致重启后资产全部消失）。
func newBlobStore(cfg platform.Config) (asset.BlobStore, error) {
	switch cfg.BlobDriver {
	case "fs", "":
		store, err := asset.NewFSBlobStore(cfg.BlobFSRoot)
		if err != nil {
			return nil, fmt.Errorf("初始化 Blob 目录 %s: %w", cfg.BlobFSRoot, err)
		}
		return store, nil
	case "s3":
		return nil, errors.New("IC_BLOB_DRIVER=s3 需要带 s3 标签的构建；当前构建不含 S3 实现")
	default:
		return nil, fmt.Errorf("不支持的 IC_BLOB_DRIVER: %s", cfg.BlobDriver)
	}
}

// staticFSOf 解析前端产物目录（未构建时返回 nil，服务以纯 API 模式运行）。
func staticFSOf(cfg platform.Config) fs.FS {
	fsys, _, ok := api.ResolveStaticDir(cfg.StaticDir)
	if !ok {
		return nil
	}
	return fsys
}

// startBackground 启动恢复与后台任务。
//
// 恢复语义（ATK-15）：进程重启后，把上次处于 running/pending 的 Run 收敛——
// 有远程异步任务的继续轮询，没有的标记为 interrupted（明确失败，不静默卡住）。
func (a *App) startBackground(ctx context.Context) {
	go func() {
		// 给服务端一点启动时间，避免恢复任务与首次请求争抢 SQLite 单写者。
		select {
		case <-ctx.Done():
			return
		case <-time.After(1500 * time.Millisecond):
		}
		n, err := a.Runs.ResumeAll(ctx)
		if err != nil {
			a.Logger.Warn("恢复未完成运行失败", "err", platform.Redact(err.Error()))
			return
		}
		if n > 0 {
			a.Logger.Info("已恢复未完成的运行", "count", n)
		}
	}()
}

// metaService 提供就绪检查。
type metaService struct{ db *platform.DB }

// Ready 见 api.MetaService。
func (m *metaService) Ready(ctx context.Context) error {
	if m.db == nil {
		return errors.New("db is not configured")
	}
	return m.db.Ready(ctx)
}

// agentCanvasGateway 让 Agent 通过标准 op 路径改画布（ATK 前置：Agent 不是特权通道）。
type agentCanvasGateway struct {
	graph *graph.Service
	runs  *exec.Engine
}

// AppendOps 见 agent.CanvasGateway。Agent 的写操作走与其他写入完全相同的路径。
func (g *agentCanvasGateway) AppendOps(ctx context.Context, canvasID string, baseVersion int64, ops []json.RawMessage, actor string) (graph.ApplyResult, *graph.CanvasDocument, error) {
	return g.graph.AppendOps(ctx, canvasID, baseVersion, ops, actor)
}

// Get 见 agent.CanvasGateway。
func (g *agentCanvasGateway) Get(ctx context.Context, canvasID string) (*graph.CanvasDocument, error) {
	return g.graph.Get(ctx, canvasID)
}

// runsAdapter 把 exec.Engine 适配为 api.RunService。
type runsAdapter struct {
	engine    *exec.Engine
	preflight func(ctx context.Context, params map[string]any) error
}

// Submit 见 api.RunService。
//
// 两类提交的语义差异必须显式处理：
//   - 画布触发（AdHoc=false）：只创建 Run，执行由编排侧继续（画布是权威输入源）；
//   - 直通生成（AdHoc=true，工作台用）：**必须立刻执行**。
//     上一轮的实现只 Create 不 Execute，导致工作台拿到一个永远 pending 的 Run——
//     接口返回 202 看着「成功」，但永远不会有结果。这里补上执行，
//     并把「提交前校验」提前到 handler（缺凭据等错误在提交时就明确报出）。
func (r *runsAdapter) Submit(ctx context.Context, req api.RunRequest) (*api.RunDTO, error) {
	if req.AdHoc && r.preflight != nil {
		if err := r.preflight(ctx, req.Params); err != nil {
			return nil, err
		}
	}
	run, err := r.engine.Create(ctx, exec.RunRequest{
		WorkspaceID: req.WorkspaceID, CanvasID: req.CanvasID, ProjectID: req.ProjectID,
		TargetNodes: req.TargetNodes, Trigger: req.Trigger, ActorID: req.ActorID,
		Params: req.Params, IdempotencyKey: req.IdempotencyKey, AdHoc: req.AdHoc,
	})
	if err != nil {
		return nil, err
	}
	if req.AdHoc {
		// 异步执行：工作台立刻拿到 Run 去轮询，不阻塞 HTTP 请求。
		// 失败会写进 Run 的状态与 error 字段（可观测，不静默）。
		plan, planErr := exec.CompileAdHoc(*run)
		if planErr != nil {
			return nil, planErr
		}
		go func() {
			// 用独立的 context：请求结束不应取消后台生成。
			bg, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
			defer cancel()
			if err := r.engine.ExecuteAdHoc(bg, run, plan); err != nil {
				r.engine.MarkFailed(bg, run, err)
			}
		}()
	}
	return runToDTO(run), nil
}

// Get 见 api.RunService。
func (r *runsAdapter) Get(ctx context.Context, _, runID string) (*api.RunDTO, error) {
	run, err := r.engine.Get(ctx, runID)
	if err != nil {
		return nil, err
	}
	return runToDTO(run), nil
}

// List 见 api.RunService。
func (r *runsAdapter) List(ctx context.Context, wsID, canvasID string, limit int, cursor string) ([]api.RunDTO, string, error) {
	runs, next, err := r.engine.List(ctx, wsID, canvasID, limit, cursor)
	if err != nil {
		return nil, "", err
	}
	out := make([]api.RunDTO, 0, len(runs))
	for _, run := range runs {
		out = append(out, *runToDTO(run))
	}
	return out, next, nil
}

// Cancel 见 api.RunService。
func (r *runsAdapter) Cancel(ctx context.Context, _, runID string) error {
	return r.engine.CancelByID(ctx, runID)
}

// Replay 见 api.RunService。
func (r *runsAdapter) Replay(ctx context.Context, _, runID string) (*api.RunDTO, error) {
	run, err := r.engine.Replay(ctx, runID)
	if err != nil {
		return nil, err
	}
	return runToDTO(run), nil
}

// Subscribe 见 api.RunService。
func (r *runsAdapter) Subscribe(runID string) (<-chan api.RunEvent, func()) {
	src, cancel := r.engine.Sink().SubscribeRun(runID)
	out := make(chan api.RunEvent, 64)
	go func() {
		defer close(out)
		for ev := range src {
			out <- api.RunEvent{
				RunID: ev.RunID, StepID: ev.StepID, NodeID: ev.NodeID, Type: ev.Type,
				Status: ev.Status, Delta: ev.Delta,
			}
		}
	}()
	return out, cancel
}

func runToDTO(run *exec.Run) *api.RunDTO {
	steps := make([]api.StepDTO, 0, len(run.Steps))
	for _, st := range run.Steps {
		dto := api.StepDTO{
			ID: st.ID, NodeID: st.NodeID, Kind: string(st.Kind), Status: string(st.Status),
			Text: st.Text, StartedAt: st.StartedAt, FinishedAt: st.FinishedAt,
		}
		for _, o := range st.Outputs {
			dto.Outputs = append(dto.Outputs, o.AssetID)
		}
		for _, a := range st.Attempts {
			dto.Attempts = append(dto.Attempts, api.AttemptDTO{
				Index: a.Index, ProviderID: a.ProviderID, ModelID: a.ModelID, RequestID: a.RequestID,
				Status: string(a.Status), HTTPStatus: a.HTTPStatus, LatencyMS: int(a.Latency.Milliseconds()),
				TokensIn: a.Usage.TextTokensIn, TokensOut: a.Usage.TextTokensOut, CostMicros: a.Usage.CostMicros,
			})
		}
		if st.Error != nil {
			dto.Error = &api.ErrDTO{Code: st.Error.Code, Message: st.Error.Message, Class: string(st.Error.Class)}
		}
		steps = append(steps, dto)
	}
	dto := &api.RunDTO{
		ID: run.ID, Workspace: run.WorkspaceID, CanvasID: run.CanvasID, Trigger: run.Trigger,
		Status: string(run.Status), Targets: run.TargetNodes, Steps: steps,
		Usage: api.UsageDTO{
			TextTokensIn: run.Usage.TextTokensIn, TextTokensOut: run.Usage.TextTokensOut,
			Images: run.Usage.Images, VideoMillis: run.Usage.VideoMillis,
			AudioMillis: run.Usage.AudioMillis, CostMicros: run.Usage.CostMicros,
		},
		Params: run.Params, StartedAt: run.StartedAt, FinishedAt: run.FinishedAt,
	}
	if run.Error != nil {
		dto.Error = &api.ErrDTO{Code: run.Error.Code, Message: run.Error.Message, Class: string(run.Error.Class)}
	}
	return dto
}

// EnsureBlobDir 确保 Blob 目录存在（CLI doctor 与启动前检查共用）。
func EnsureBlobDir(cfg platform.Config) error {
	if cfg.BlobDriver != "fs" && cfg.BlobDriver != "" {
		return nil
	}
	if cfg.BlobFSRoot == "" {
		return errors.New("IC_BLOB_FS_ROOT 不能为空")
	}
	return os.MkdirAll(filepath.Dir(cfg.BlobFSRoot), 0o750)
}

func firstNonEmptyStr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// preflightGenerate 在提交直通生成前校验「凭据可用」。
//
// 为什么必须前置：如果等到异步执行时才发现没有凭据，用户拿到的是一个 202 +
// 一个永远 failed 的 Run，他需要去翻 Run 详情才知道「原来是没配 Key」。
// 前置校验让这类「配置缺失」在提交瞬间就以明确的 422 + code 返回。
//
// 同时把选中的渠道/凭据回填进 Run.Params：避免执行期再选一次，
// 也避免「用户提交后改了默认渠道，Run 用了另一个渠道」这种不可复现的行为。
func (a *App) preflightGenerate(ctx context.Context, params map[string]any) error {
	capability, _ := params["capability"].(string)
	if capability == "" {
		capability = string(provider.CapImageGenerate)
	}
	providerID, _ := params["providerId"].(string)
	credID, _ := params["credentialId"].(string)
	adapter, cred, ok := a.resolver.Resolve(provider.Capability(capability), providerID, credID)
	if !ok || adapter == nil {
		err := platform.NewError(422, platform.CodeInvalidRequest,
			"没有可用于该能力的渠道凭据，请先在配置中心添加渠道与 API Key").
			WithDetail("capability", capability)
		// 把解析失败的真实原因一并给出：笼统的「没有可用凭据」在
		// 「渠道没声明该能力 / 适配器没注册 / 密钥解不开」三种情况下
		// 处理方式完全不同，而用户看到的却只有一句话。
		if cause := a.resolver.LastError(); cause != nil {
			err = err.WithDetail("reason", platform.Redact(cause.Error()))
		}
		return err
	}
	// 回填的是**渠道行 id**（SourceProviderID），不是协议名：
	// 执行期要用它再次解析到同一条渠道配置。写协议名会让「有两个
	// 同协议渠道、用户选了 B」变成「实际用了 A」。
	params["providerId"] = firstNonEmptyStr(cred.SourceProviderID, cred.ProviderID)
	params["credentialId"] = cred.ID

	// 自定义脚本的**静态校验**在提交前完成。
	//
	// 为什么不能等到异步执行时再报：脚本写错是**永久**错误（重试不会变好），
	// 而异步报错的表现是「返回 202 + 一个立刻 failed 的 Run」——
	// 用户需要去翻 Run 详情才知道「原来是脚本里写了个 eval」。
	// 更糟的是「拒绝逃逸」这件事在异步路径上没有 HTTP 状态码可断言，
	// 于是「拒绝生效」这条安全结论就没有可观测信号。
	if script, ok := cred.Limits["script"].(string); ok && strings.TrimSpace(script) != "" {
		analysis, err := sandbox.Analyze(script)
		if err != nil {
			return platform.NewError(422, platform.CodeInvalidRequest,
				"渠道的调用脚本无法通过沙箱校验: "+platform.Redact(err.Error())).
				WithDetail("providerId", cred.SourceProviderID)
		}
		if len(analysis.UnknownCalls) > 0 {
			return platform.NewError(422, platform.CodeInvalidRequest,
				"脚本调用了白名单外的函数: "+strings.Join(analysis.UnknownCalls, ", ")).
				WithDetail("providerId", cred.SourceProviderID)
		}
		if len(analysis.Forbidden) > 0 {
			return platform.NewError(422, platform.CodeForbidden,
				"脚本包含被拒绝的写法: "+strings.Join(analysis.Forbidden, "; ")).
				WithDetail("providerId", cred.SourceProviderID)
		}
	}
	// 静态检查之外，还要跑一次「脚本 → 请求描述」的转换（不发请求）。
	//
	// 保留头部（Authorization）、主机越界、responsePath 缺失这三类问题
	// 只有拿到**运行期入参**才能判定（例如 path 可能是变量拼出来的）。
	// 放在提交前做，是为了让它们以 4xx 返回而不是「202 + 立刻失败的 Run」。
	if err := a.Providers.ValidateProviderPlan(ctx, provider.Capability(capability), providerID, credID, params); err != nil {
		return err
	}
	return nil
}
