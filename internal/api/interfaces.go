package api

import (
	"context"
	"encoding/json"
	"io"
	"time"

	"github.com/context-flow/ic/internal/graph"
)

// GraphService 是画布用例接口。
type GraphService interface {
	Create(ctx context.Context, projectID, name string) (*graph.CanvasMeta, error)
	Get(ctx context.Context, canvasID string) (*graph.CanvasDocument, error)
	List(ctx context.Context, projectID string) ([]graph.CanvasMeta, error)
	AppendOps(ctx context.Context, canvasID string, baseVersion int64, ops []json.RawMessage, actor string) (graph.ApplyResult, *graph.CanvasDocument, error)
	Subscribe(canvasID string) (<-chan graph.Event, func())
	OpenReplay(ctx context.Context, canvasID string) error
}

// BlobStore 是资产二进制存储抽象（FS / S3 两种实现）。
type BlobStore interface {
	Put(ctx context.Context, hash string, r io.Reader, size int64, mime string) error
	Open(ctx context.Context, hash string) (io.ReadSeekCloser, int64, error)
	Exists(ctx context.Context, hash string) (bool, error)
	Delete(ctx context.Context, hash string) error
}

// AssetService 是资产用例接口。
type AssetService interface {
	Upload(ctx context.Context, wsID string, name, mime string, r io.Reader, size int64) (*AssetDTO, error)
	Get(ctx context.Context, wsID, id string) (*AssetDTO, error)
	Open(ctx context.Context, wsID, id string) (io.ReadSeekCloser, int64, string, error)
	List(ctx context.Context, wsID string, kind string, limit int, cursor string) ([]AssetDTO, string, error)
	Delete(ctx context.Context, wsID, id string) error
	Thumb(ctx context.Context, wsID, id string, w int) ([]byte, string, error)
}

// AssetDTO 是资产对外表示。
type AssetDTO struct {
	ID        string            `json:"id"`
	Workspace string            `json:"workspaceId"`
	Kind      string            `json:"kind"`
	Hash      string            `json:"hash"`
	Size      int64             `json:"size"`
	MIME      string            `json:"mime"`
	Name      string            `json:"name,omitempty"`
	Meta      map[string]any    `json:"meta,omitempty"`
	Origin    string            `json:"origin"`
	Source    map[string]string `json:"source,omitempty"`
	URL       string            `json:"url,omitempty"`
	CreatedAt time.Time         `json:"createdAt"`
}

// RunService 是执行引擎接口。
type RunService interface {
	Submit(ctx context.Context, req RunRequest) (*RunDTO, error)
	Get(ctx context.Context, wsID, runID string) (*RunDTO, error)
	List(ctx context.Context, wsID, canvasID string, limit int, cursor string) ([]RunDTO, string, error)
	Cancel(ctx context.Context, wsID, runID string) error
	Replay(ctx context.Context, wsID, runID string) (*RunDTO, error)
	Subscribe(runID string) (<-chan RunEvent, func())
}

// RunRequest 是触发运行的请求。
type RunRequest struct {
	WorkspaceID    string         `json:"workspaceId"`
	CanvasID       string         `json:"canvasId"`
	ProjectID      string         `json:"projectId"`
	TargetNodes    []string       `json:"targetNodes"`
	Trigger        string         `json:"trigger"`
	ActorID        string         `json:"actorId"`
	Params         map[string]any `json:"params,omitempty"`
	IdempotencyKey string         `json:"-"`
	// AdHoc 表示不落画布的直通生成（工作台/调试用）。
	AdHoc bool `json:"adHoc,omitempty"`
}

// RunDTO 是运行对外表示。
type RunDTO struct {
	ID         string         `json:"id"`
	Workspace  string         `json:"workspaceId"`
	CanvasID   string         `json:"canvasId,omitempty"`
	Trigger    string         `json:"trigger"`
	Status     string         `json:"status"`
	Targets    []string       `json:"targetNodes,omitempty"`
	Steps      []StepDTO      `json:"steps"`
	Usage      UsageDTO       `json:"usage"`
	Error      *ErrDTO        `json:"error,omitempty"`
	Params     map[string]any `json:"params,omitempty"`
	StartedAt  time.Time      `json:"startedAt"`
	FinishedAt *time.Time     `json:"finishedAt,omitempty"`
}

// StepDTO 是步骤对外表示。
type StepDTO struct {
	ID         string       `json:"id"`
	NodeID     string       `json:"nodeId"`
	Kind       string       `json:"kind"`
	Status     string       `json:"status"`
	Attempts   []AttemptDTO `json:"attempts"`
	Outputs    []string     `json:"outputs,omitempty"`
	Text       string       `json:"text,omitempty"`
	Error      *ErrDTO      `json:"error,omitempty"`
	StartedAt  time.Time    `json:"startedAt"`
	FinishedAt *time.Time   `json:"finishedAt,omitempty"`
}

// AttemptDTO 是一次上游调用尝试。
type AttemptDTO struct {
	Index      int     `json:"index"`
	ProviderID string  `json:"providerId"`
	ModelID    string  `json:"modelId"`
	RequestID  string  `json:"requestId"`
	Status     string  `json:"status"`
	HTTPStatus int     `json:"httpStatus,omitempty"`
	LatencyMS  int     `json:"latencyMs"`
	TokensIn   int64   `json:"tokensIn,omitempty"`
	TokensOut  int64   `json:"tokensOut,omitempty"`
	CostMicros int64   `json:"costMicros,omitempty"`
	RemoteTask string  `json:"remoteTaskId,omitempty"`
	Error      *ErrDTO `json:"error,omitempty"`
}

// UsageDTO 是计量。
type UsageDTO struct {
	TextTokensIn  int64 `json:"textTokensIn"`
	TextTokensOut int64 `json:"textTokensOut"`
	Images        int64 `json:"images"`
	VideoMillis   int64 `json:"videoMillis"`
	AudioMillis   int64 `json:"audioMillis"`
	CostMicros    int64 `json:"costMicros"`
}

// ErrDTO 是稳定错误码 + 文案。
type ErrDTO struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Class   string         `json:"class,omitempty"`
	Details map[string]any `json:"details,omitempty"`
}

// RunEvent 是运行事件（SSE）。
type RunEvent struct {
	RunID  string `json:"runId"`
	StepID string `json:"stepId,omitempty"`
	NodeID string `json:"nodeId,omitempty"`
	Type   string `json:"type"`
	Status string `json:"status,omitempty"`
	Delta  string `json:"delta,omitempty"`
	Data   any    `json:"data,omitempty"`
}

// ProviderService 是渠道/凭据管理接口。
type ProviderService interface {
	ListProviders(ctx context.Context, wsID string) ([]ProviderDTO, error)
	CreateProvider(ctx context.Context, wsID string, in ProviderInput) (*ProviderDTO, error)
	CreateCredential(ctx context.Context, wsID, providerID string, in CredentialInput) (*CredentialDTO, error)
	TestProvider(ctx context.Context, wsID, providerID string) (*ProbeResult, error)
	ListModels(ctx context.Context, wsID, capability string) ([]ModelDTO, error)
}

// ProviderDTO 是渠道对外表示。
type ProviderDTO struct {
	ID           string   `json:"id"`
	Workspace    string   `json:"workspaceId"`
	Kind         string   `json:"kind"`
	Name         string   `json:"name"`
	Capabilities []string `json:"capabilities"`
	BaseURL      string   `json:"baseUrl"`
	AuthKind     string   `json:"authKind"`
	Enabled      bool     `json:"enabled"`
}

// ProviderInput 创建渠道入参。
type ProviderInput struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Kind         string   `json:"kind"`
	BaseURL      string   `json:"baseUrl"`
	AuthKind     string   `json:"authKind"`
	Capabilities []string `json:"capabilities"`
}

// CredentialInput 创建凭据入参（secret 只进不出，INV-5）。
type CredentialInput struct {
	Name     string         `json:"name"`
	Secret   string         `json:"secret"`
	Priority int            `json:"priority"`
	Limits   map[string]any `json:"limits,omitempty"`
}

// CredentialDTO 是凭据对外表示（只有掩码）。
type CredentialDTO struct {
	ID        string         `json:"id"`
	Provider  string         `json:"providerId"`
	Name      string         `json:"name"`
	Masked    string         `json:"masked"`
	Priority  int            `json:"priority"`
	Limits    map[string]any `json:"limits,omitempty"`
	Enabled   bool           `json:"enabled"`
	CreatedAt time.Time      `json:"createdAt"`
}

// ProbeResult 是连通性探测结果。
type ProbeResult struct {
	OK        bool     `json:"ok"`
	LatencyMS int      `json:"latencyMs"`
	Models    []string `json:"models,omitempty"`
	Error     *ErrDTO  `json:"error,omitempty"`
}

// ModelDTO 是模型条目。
type ModelDTO struct {
	ID           string         `json:"id"`
	ProviderID   string         `json:"providerId"`
	DisplayName  string         `json:"displayName,omitempty"`
	Capabilities []string       `json:"capabilities"`
	Params       map[string]any `json:"paramsSchema,omitempty"`
	Healthy      bool           `json:"healthy"`
}

// PromptService 是提示词库接口。
type PromptService interface {
	ListSources(ctx context.Context, wsID string) ([]PromptSourceDTO, error)
	CreateSource(ctx context.Context, wsID string, in PromptSourceInput) (*PromptSourceDTO, error)
	SyncSource(ctx context.Context, wsID, sourceID string) (*SyncResultDTO, error)
	Search(ctx context.Context, wsID, q string, tags []string, limit int, cursor string) ([]PromptDTO, string, error)
}

// PromptSourceDTO 是提示词来源对外表示。
type PromptSourceDTO struct {
	ID              string     `json:"id"`
	Name            string     `json:"name"`
	URL             string     `json:"url"`
	Format          string     `json:"format"`
	RefreshInterval string     `json:"refreshInterval"`
	Enabled         bool       `json:"enabled"`
	Status          string     `json:"status"`
	Count           int        `json:"count"`
	LastSyncedAt    *time.Time `json:"lastSyncedAt,omitempty"`
}

// PromptSourceInput 创建来源入参。
type PromptSourceInput struct {
	Name            string `json:"name"`
	URL             string `json:"url"`
	Format          string `json:"format"`
	RefreshInterval string `json:"refreshInterval"`
	Enabled         *bool  `json:"enabled,omitempty"`
}

// SyncResultDTO 是同步结果。
type SyncResultDTO struct {
	SourceID string  `json:"sourceId"`
	Added    int     `json:"added"`
	Updated  int     `json:"updated"`
	Removed  int     `json:"removed"`
	Total    int     `json:"total"`
	Status   string  `json:"status"`
	Error    *ErrDTO `json:"error,omitempty"`
}

// PromptDTO 是提示词条目。
type PromptDTO struct {
	ID         string   `json:"id"`
	SourceID   string   `json:"sourceId"`
	ExternalID string   `json:"externalId"`
	Title      string   `json:"title"`
	Tags       []string `json:"tags"`
	Content    string   `json:"content"`
	Variables  []string `json:"variables,omitempty"`
	CoverURL   string   `json:"coverUrl,omitempty"`
	Hash       string   `json:"hash"`
}

// PluginService 是插件体系接口。
type PluginService interface {
	List(ctx context.Context, wsID string) ([]PluginDTO, error)
	Install(ctx context.Context, wsID string, in PluginInstallInput) (*PluginDTO, error)
	Enable(ctx context.Context, wsID, key string, enabled bool) (*PluginDTO, error)
	Bundle(ctx context.Context, key, version string) ([]byte, string, error)
	Registry(ctx context.Context) (any, error)
}

// PluginDTO 是插件对外表示。
type PluginDTO struct {
	Key         string          `json:"key"`
	Name        string          `json:"name"`
	Version     string          `json:"version"`
	APIVersion  string          `json:"apiVersion"`
	Author      string          `json:"author,omitempty"`
	Homepage    string          `json:"homepage,omitempty"`
	Permissions []string        `json:"permissions"`
	Nodes       []PluginNodeDTO `json:"nodes"`
	Enabled     bool            `json:"enabled"`
	Builtin     bool            `json:"builtin"`
	Integrity   string          `json:"integrity,omitempty"`
	Signed      bool            `json:"signed"`
	Config      map[string]any  `json:"config,omitempty"`
}

// PluginNodeDTO 是插件节点声明。
type PluginNodeDTO struct {
	Type          string         `json:"type"`
	Title         string         `json:"title"`
	DefaultSize   map[string]int `json:"defaultSize,omitempty"`
	MinSize       map[string]int `json:"minSize,omitempty"`
	MinimapColor  string         `json:"minimapColor,omitempty"`
	ConfigSchema  map[string]any `json:"configSchema,omitempty"`
	ConfigVersion int            `json:"configVersion,omitempty"`
	Interactive   bool           `json:"interactive,omitempty"`
	Ports         graph.Ports    `json:"ports"`
}

// PluginInstallInput 安装插件入参。
type PluginInstallInput struct {
	ManifestURL string         `json:"manifestUrl,omitempty"`
	Manifest    map[string]any `json:"manifest,omitempty"`
	Config      map[string]any `json:"config,omitempty"`
	// Trusted 表示用户已确认权限清单。
	Trusted bool `json:"trusted"`
}

// AuthService 是身份与工作区接口。
type AuthService interface {
	Login(ctx context.Context, email, password string) (*SessionDTO, error)
	Register(ctx context.Context, email, name, password string) (*SessionDTO, error)
	Logout(ctx context.Context, token string) error
	Authenticate(ctx context.Context, token string) (*Principal, error)
	ListWorkspaces(ctx context.Context, userID string) ([]WorkspaceDTO, error)
	CreateWorkspace(ctx context.Context, userID, name string) (*WorkspaceDTO, error)
	ListProjects(ctx context.Context, wsID string) ([]ProjectDTO, error)
	CreateProject(ctx context.Context, wsID, name, description string) (*ProjectDTO, error)
	// IssueAPIKey 生成 MCP/CLI 用的 API Key（只返回一次明文）。
	IssueAPIKey(ctx context.Context, wsID, userID, name string) (string, error)
	RevokeAPIKey(ctx context.Context, wsID, key string) error
}

// SessionDTO 是登录结果。
type SessionDTO struct {
	Token      string         `json:"token"`
	User       UserDTO        `json:"user"`
	Workspaces []WorkspaceDTO `json:"workspaces"`
	ExpiresAt  time.Time      `json:"expiresAt"`
}

// UserDTO 是用户表示。
type UserDTO struct {
	ID     string `json:"id"`
	Email  string `json:"email"`
	Name   string `json:"name"`
	Avatar string `json:"avatar,omitempty"`
}

// WorkspaceDTO 是工作区表示。
type WorkspaceDTO struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
	Role string `json:"role"`
	Plan string `json:"plan"`
}

// ProjectDTO 是项目表示。
type ProjectDTO struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspaceId"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	CoverURL    string    `json:"coverUrl,omitempty"`
	CanvasCount int       `json:"canvasCount"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// Principal 是已认证主体。
type Principal struct {
	UserID      string
	Email       string
	Name        string
	WorkspaceID string
	Role        string
	Scopes      []string
}

// MetaService 提供就绪检查等元能力。
type MetaService interface {
	Ready(ctx context.Context) error
}

// AgentService 是 Agent 网关接口（由 internal/agent 实现）。
type AgentService interface {
	CreateSession(ctx context.Context, wsID, canvasID, backend, title string) (*AgentSessionDTO, error)
	GetSession(ctx context.Context, sessionID string) (*AgentSessionDTO, error)
	SubmitTurn(ctx context.Context, sessionID, input, actor string) (*AgentTurnDTO, error)
	Approve(ctx context.Context, sessionID, turnID, callID, actor string, approve bool) (*AgentTurnDTO, error)
}

// AgentSessionDTO 是会话对外表示。
type AgentSessionDTO struct {
	ID          string         `json:"id"`
	WorkspaceID string         `json:"workspaceId"`
	CanvasID    string         `json:"canvasId"`
	Backend     string         `json:"backend"`
	ThreadID    string         `json:"threadId"`
	Title       string         `json:"title"`
	Permission  string         `json:"permission"`
	Turns       []AgentTurnDTO `json:"turns"`
	CreatedAt   time.Time      `json:"createdAt"`
}

// AgentTurnDTO 是轮次对外表示。
type AgentTurnDTO struct {
	ID        string           `json:"id"`
	Seq       int              `json:"seq"`
	Status    string           `json:"status"`
	Input     string           `json:"input"`
	Items     []AgentItemDTO   `json:"items"`
	Usage     map[string]any   `json:"usage"`
	Pending   *AgentPendingDTO `json:"pending,omitempty"`
	Error     *ErrDTO          `json:"error,omitempty"`
	CreatedAt time.Time        `json:"createdAt"`
}

// AgentPendingDTO 是待审批工具。
type AgentPendingDTO struct {
	CallID        string          `json:"callId"`
	Tool          string          `json:"tool"`
	Arguments     json.RawMessage `json:"arguments"`
	OpCount       int             `json:"opCount"`
	NodeIDs       []string        `json:"nodeIds,omitempty"`
	EstCostMicros int64           `json:"estCostMicros"`
}

// AgentItemDTO 是条目对外表示。
type AgentItemDTO struct {
	ID        string          `json:"id"`
	TurnID    string          `json:"turnId"`
	Seq       int             `json:"seq"`
	Kind      string          `json:"kind"`
	Payload   json.RawMessage `json:"payload"`
	Source    string          `json:"source"`
	CreatedAt time.Time       `json:"createdAt"`
}
