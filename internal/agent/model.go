// Package agent 实现会话编排、工具协议、审批网关与 MCP 服务端。
//
// 核心经验（来自上游 AGENTS.md 的沉淀，写进了数据模型）：
//
//	消息必须按 threadId / turnId / itemId 三元归属；实时事件只补充未物化的 turn；
//	历史快照成为权威后不得重复合并。
package agent

import (
	"encoding/json"
	"time"

	"github.com/context-flow/ic/internal/platform"
)

// Backend 是 Agent 后端类型。
type Backend string

// 后端枚举。
const (
	BackendHTTP       Backend = "http"        // 服务端直接调用云端模型
	BackendCodex      Backend = "codex"       // 本机 Codex app-server 桥接
	BackendClaudeCode Backend = "claude_code" // 本机 Claude Code 桥接
)

// ItemKind 是条目类型（前端只认这一层，不再为每个 Agent 写一套渲染）。
type ItemKind string

// 条目类型。
const (
	ItemAgentMessage ItemKind = "agent_message"
	ItemReasoning    ItemKind = "reasoning"
	ItemToolCall     ItemKind = "tool_call"
	ItemToolResult   ItemKind = "tool_result"
	ItemFileChange   ItemKind = "file_change"
	ItemError        ItemKind = "error"
)

// ItemSource 标记条目来源。合并策略由主键保证幂等（INV-7）。
type ItemSource string

// 来源枚举。
const (
	SourceLive     ItemSource = "live"
	SourceSnapshot ItemSource = "snapshot"
)

// TurnStatus 是轮次状态。
type TurnStatus string

// 轮次状态。
const (
	TurnPending  TurnStatus = "pending"
	TurnRunning  TurnStatus = "running"
	TurnAwaiting TurnStatus = "awaiting_approval"
	TurnDone     TurnStatus = "succeeded"
	TurnFailed   TurnStatus = "failed"
	TurnCanceled TurnStatus = "canceled"
)

// ApprovalMode 是工具审批策略。
type ApprovalMode string

// 审批策略。
const (
	ApprovalAuto      ApprovalMode = "auto"      // 只读或完全不产生副作用
	ApprovalConfirm   ApprovalMode = "confirm"   // 写操作或产生费用，必须确认
	ApprovalForbidden ApprovalMode = "forbidden" // 明确禁止
)

// PermissionMode 是会话语义上的权限档位（对齐原项目三种模式）。
type PermissionMode string

// 权限档位。
const (
	PermRequest   PermissionMode = "request"   // 每次写操作都问
	PermAutomatic PermissionMode = "automatic" // 自动放行读操作
	PermFull      PermissionMode = "full"      // 完全信任本次会话
)

// ToolDef 是工具定义。工具表由画布 op schema 生成，避免两套定义漂移。
type ToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
	Approval    ApprovalMode    `json:"approval"`
	Scope       string          `json:"scope"` // read | write
	// CostsMoney 表示该工具会触发真实费用（必须高亮提示）。
	CostsMoney bool `json:"costsMoney,omitempty"`
}

// Item 是会话中的一条条目。
type Item struct {
	ID        string          `json:"id"`
	TurnID    string          `json:"turnId"`
	Seq       int             `json:"seq"`
	Kind      ItemKind        `json:"kind"`
	Payload   json.RawMessage `json:"payload"`
	Source    ItemSource      `json:"source"`
	CreatedAt time.Time       `json:"createdAt"`
}

// Turn 是一次交互轮次（聚合根）。
type Turn struct {
	ID        string                `json:"id"`
	SessionID string                `json:"sessionId"`
	Seq       int                   `json:"seq"`
	Status    TurnStatus            `json:"status"`
	Input     string                `json:"input"`
	Items     []Item                `json:"items"`
	Usage     Usage                 `json:"usage"`
	Pending   *PendingTool          `json:"pending,omitempty"`
	Error     *platform.DomainError `json:"error,omitempty"`
	CreatedAt time.Time             `json:"createdAt"`
	UpdatedAt time.Time             `json:"updatedAt"`
}

// PendingTool 描述等待审批的工具调用。
type PendingTool struct {
	CallID    string          `json:"callId"`
	Tool      string          `json:"tool"`
	Arguments json.RawMessage `json:"arguments"`
	// Ops/影响范围：审批卡片必须展示「将要执行的 op 列表 + 影响节点数 + 预估成本」。
	OpCount       int      `json:"opCount"`
	NodeIDs       []string `json:"nodeIds"`
	EstCostMicros int64    `json:"estCostMicros"`
}

// Usage 是会话计量。
type Usage struct {
	TextTokensIn  int64 `json:"textTokensIn"`
	TextTokensOut int64 `json:"textTokensOut"`
	ToolCalls     int64 `json:"toolCalls"`
	CostMicros    int64 `json:"costMicros"`
}

// Add 累加。
func (u Usage) Add(o Usage) Usage {
	return Usage{
		TextTokensIn:  u.TextTokensIn + o.TextTokensIn,
		TextTokensOut: u.TextTokensOut + o.TextTokensOut,
		ToolCalls:     u.ToolCalls + o.ToolCalls,
		CostMicros:    u.CostMicros + o.CostMicros,
	}
}

// Session 是一次会话。
type Session struct {
	ID          string         `json:"id"`
	WorkspaceID string         `json:"workspaceId"`
	CanvasID    string         `json:"canvasId"`
	Backend     Backend        `json:"backend"`
	ThreadID    string         `json:"threadId"`
	Title       string         `json:"title"`
	Permission  PermissionMode `json:"permission"`
	Turns       []*Turn        `json:"turns"`
	CreatedAt   time.Time      `json:"createdAt"`
}

// SessionSnapshot 是随画布导出的只读会话快照（2.11）。
type SessionSnapshot struct {
	ID        string         `json:"id"`
	CanvasID  string         `json:"canvasId"`
	ThreadID  string         `json:"threadId,omitempty"`
	Title     string         `json:"title,omitempty"`
	CreatedAt time.Time      `json:"createdAt"`
	Turns     []TurnSnapshot `json:"turns"`
}

// TurnSnapshot 是一轮对话的快照。
type TurnSnapshot struct {
	Seq    int            `json:"seq"`
	Status string         `json:"status"`
	Input  string         `json:"input"`
	Items  []ItemSnapshot `json:"items"`
}

// ItemSnapshot 是条目的裁剪表示。
type ItemSnapshot struct {
	Kind string    `json:"kind"`
	Text string    `json:"text,omitempty"`
	At   time.Time `json:"at"`
	// Redacted 表示「这条存在但内容未导出」。显式标记而不是留空：
	// 用户能看出「当时发生过工具调用」，而不是以为会话不完整。
	Redacted bool `json:"redacted,omitempty"`
}

// ToolCallResult 是工具执行结果（对齐 docs/design/07 §4）。
type ToolCallResult struct {
	CallID  string          `json:"callId"`
	Status  string          `json:"status"` // ok | denied | error
	Applied *AppliedInfo    `json:"applied,omitempty"`
	Inverse json.RawMessage `json:"inverse,omitempty"`
	Error   *ToolError      `json:"error,omitempty"`
	// Result 是只读工具的返回值。
	Result json.RawMessage `json:"result,omitempty"`
}

// AppliedInfo 描述一次写操作的应用结果。
type AppliedInfo struct {
	Ops     int   `json:"ops"`
	Version int64 `json:"version"`
}

// ToolError 是工具错误。
type ToolError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
