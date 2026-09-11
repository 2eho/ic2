// Package api 是 HTTP 层：路由、DTO、SSE、鉴权中间件。Handler 不写业务逻辑。
package api

import (
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/context-flow/ic/internal/platform"
)

// Deps 是 API 层的依赖集合（由 cmd 装配）。
type Deps struct {
	Config    platform.Config
	Logger    *slog.Logger
	Graph     GraphService
	Assets    AssetService
	Runs      RunService
	Providers ProviderService
	Prompts   PromptService
	Plugins   PluginService
	Agent     AgentService
	MCP       MCPService
	Auth      AuthService
	Meta      MetaService
	// StaticFS / StaticDir 用于托管前端产物（为空则纯 API 模式）。
	StaticFS  fs.FS
	StaticDir string
}

// Router 构造 HTTP 路由。
func NewRouter(d Deps) http.Handler {
	mux := http.NewServeMux()
	h := &handlers{deps: d}

	// 健康与元信息
	mux.HandleFunc("GET /healthz", h.healthz)
	mux.HandleFunc("GET /readyz", h.readyz)
	mux.HandleFunc("GET /api/v1/meta", h.meta)

	// 鉴权
	mux.HandleFunc("POST /api/v1/auth/login", h.login)
	mux.HandleFunc("POST /api/v1/auth/logout", h.logout)
	mux.HandleFunc("POST /api/v1/auth/register", h.register)
	mux.HandleFunc("GET /api/v1/me", h.me)

	// 工作区与项目
	mux.HandleFunc("GET /api/v1/workspaces", h.listWorkspaces)
	mux.HandleFunc("POST /api/v1/workspaces", h.createWorkspace)
	mux.HandleFunc("GET /api/v1/workspaces/{wid}/projects", h.listProjects)
	mux.HandleFunc("POST /api/v1/workspaces/{wid}/projects", h.createProject)

	// 画布
	mux.HandleFunc("GET /api/v1/projects/{pid}/canvases", h.listCanvases)
	mux.HandleFunc("POST /api/v1/projects/{pid}/canvases", h.createCanvas)
	mux.HandleFunc("GET /api/v1/canvases/{cid}", h.getCanvas)
	mux.HandleFunc("DELETE /api/v1/canvases/{cid}", h.deleteCanvas)
	mux.HandleFunc("POST /api/v1/canvases/{cid}/ops", h.appendOps)
	mux.HandleFunc("GET /api/v1/canvases/{cid}/ops", h.listOps)
	mux.HandleFunc("POST /api/v1/canvases/{cid}/import", h.importCanvas)
	mux.HandleFunc("GET /api/v1/canvases/{cid}/export", h.exportCanvas)
	mux.HandleFunc("GET /api/v1/canvases/{cid}/events", h.canvasEvents)

	// 资产
	mux.HandleFunc("POST /api/v1/workspaces/{wid}/assets", h.uploadAsset)
	mux.HandleFunc("GET /api/v1/assets/{aid}", h.getAsset)
	mux.HandleFunc("GET /api/v1/assets/{aid}/raw", h.getAssetRaw)
	mux.HandleFunc("GET /api/v1/assets/{aid}/thumb", h.getAssetThumb)
	mux.HandleFunc("DELETE /api/v1/assets/{aid}", h.deleteAsset)
	mux.HandleFunc("GET /api/v1/workspaces/{wid}/assets", h.listAssets)

	// 运行
	mux.HandleFunc("POST /api/v1/canvases/{cid}/runs", h.createRun)
	mux.HandleFunc("GET /api/v1/runs/{rid}", h.getRun)
	mux.HandleFunc("POST /api/v1/runs/{rid}/cancel", h.cancelRun)
	mux.HandleFunc("POST /api/v1/runs/{rid}/replay", h.replayRun)
	mux.HandleFunc("GET /api/v1/canvases/{cid}/runs", h.listRuns)

	// 生成（调试/测试用：不落 DB 的直通入口）
	mux.HandleFunc("POST /api/v1/workspaces/{wid}/generate", h.generate)

	// Provider / 凭据 / 模型
	mux.HandleFunc("GET /api/v1/workspaces/{wid}/providers", h.listProviders)
	mux.HandleFunc("POST /api/v1/workspaces/{wid}/providers", h.createProvider)
	mux.HandleFunc("POST /api/v1/workspaces/{wid}/providers/{pid}/credentials", h.createCredential)
	mux.HandleFunc("POST /api/v1/workspaces/{wid}/providers/{pid}/test", h.testProvider)
	mux.HandleFunc("GET /api/v1/workspaces/{wid}/models", h.listModels)

	// 提示词
	mux.HandleFunc("GET /api/v1/workspaces/{wid}/prompt-sources", h.listPromptSources)
	mux.HandleFunc("POST /api/v1/workspaces/{wid}/prompt-sources", h.createPromptSource)
	mux.HandleFunc("POST /api/v1/workspaces/{wid}/prompt-sources/{sid}/sync", h.syncPromptSource)
	mux.HandleFunc("GET /api/v1/prompts", h.searchPrompts)

	// 插件
	mux.HandleFunc("GET /api/v1/workspaces/{wid}/plugins", h.listPlugins)
	mux.HandleFunc("POST /api/v1/workspaces/{wid}/plugins", h.installPlugin)
	mux.HandleFunc("POST /api/v1/workspaces/{wid}/plugins/{key}/enable", h.enablePlugin)
	mux.HandleFunc("GET /api/v1/plugins/{key}/{version}/bundle", h.pluginBundle)
	mux.HandleFunc("GET /api/v1/plugin-registry", h.pluginRegistry)

	// Agent
	mux.HandleFunc("POST /api/v1/agent/sessions", h.createAgentSession)
	mux.HandleFunc("GET /api/v1/agent/sessions/{sid}", h.getAgentSession)
	mux.HandleFunc("POST /api/v1/agent/sessions/{sid}/turns", h.createAgentTurn)
	mux.HandleFunc("POST /api/v1/agent/sessions/{sid}/turns/{tid}/approve", h.approveAgentTurn)
	mux.HandleFunc("GET /api/v1/agent/sessions/{sid}/history", h.agentHistory)
	mux.HandleFunc("POST /api/v1/agent/tool-results", h.agentToolResult)
	mux.HandleFunc("POST /api/v1/mcp", h.mcpHTTP)

	// 前端静态产物：仅在构建存在时挂载，否则 API-only 模式
	if d.StaticFS != nil {
		mux.Handle("/", StaticHandler(d.StaticFS, d.StaticDir))
	}

	return chain(mux, d)
}

func chain(next http.Handler, d Deps) http.Handler {
	h := next
	h = recoverMiddleware(h, d)
	h = traceMiddleware(h)
	h = loggingMiddleware(h, d)
	return h
}

type handlers struct {
	deps Deps
}

// logf 在 handler 内部记录可诊断的细节（脱敏后）。
func (h *handlers) logf(r *http.Request, msg string, args ...any) {
	if h.deps.Logger == nil {
		return
	}
	h.deps.Logger.Warn(msg, append([]any{
		"path", r.URL.Path,
		"trace_id", platform.TraceID(r.Context()),
	}, args...)...)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, r *http.Request, err error) {
	var de *platform.DomainError
	if !platform.IsNil(err) {
		de = platform.AsDomainError(err)
	}
	if de == nil {
		de = platform.NewError(http.StatusInternalServerError, platform.CodeInternal, "internal error")
	}
	if de.Status == 0 {
		de.Status = http.StatusInternalServerError
	}
	// 错误信息限长 300 字符（见 11 §2.9）。
	if len(de.Message) > 300 {
		de.Message = de.Message[:300]
	}
	body := map[string]any{"code": de.Code, "message": platform.Redact(de.Message)}
	if len(de.Details) > 0 {
		body["details"] = de.Details
	}
	if tid := platform.TraceID(r.Context()); tid != "" {
		body["traceId"] = tid
	}
	writeJSON(w, de.Status, body)
}

func (h *handlers) healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (h *handlers) readyz(w http.ResponseWriter, r *http.Request) {
	// 依赖就绪检查：DB ping 由 MetaService 提供。
	if h.deps.Meta != nil {
		if err := h.deps.Meta.Ready(r.Context()); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "degraded", "reason": platform.Redact(err.Error())})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ready"})
}

func (h *handlers) meta(w http.ResponseWriter, _ *http.Request) {
	info := platform.BuildInfo{
		Version: platform.Version, Commit: platform.Commit, Date: platform.Date,
		GoVersion: runtime.Version(), Mode: h.deps.Config.Mode,
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"build": info,
		"features": map[string]any{
			"plugins":      true,
			"agent":        true,
			"localAgent":   h.deps.Config.AgentLocalAllowed,
			"legacyImport": true,
			"registration": h.deps.Config.AllowRegistration,
		},
		"limits": PublicLimits(),
		"time":   time.Now().UTC().Format(time.RFC3339),
	})
}

// clientIP 提取调用方 IP（仅用于审计与限流，不做鉴权依据）。
func clientIP(r *http.Request) string {
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		return strings.TrimSpace(strings.Split(v, ",")[0])
	}
	host := r.RemoteAddr
	if i := strings.LastIndex(host, ":"); i > 0 {
		host = host[:i]
	}
	return host
}
