package graph

import (
	"encoding/json"
	"fmt"
	"github.com/context-flow/ic/internal/platform"
	"time"
)

// OpKind 是 op 类型枚举。
type OpKind string

// op 类型（与 docs/design/03 §2.1 对齐，同时是 Agent 工具集的底层语义）。
const (
	OpAddNode     OpKind = "add_node"
	OpRemoveNode  OpKind = "remove_node"
	OpMoveNode    OpKind = "move_node"
	OpResizeNode  OpKind = "resize_node"
	OpSetTitle    OpKind = "set_title"
	OpSetSpec     OpKind = "set_spec"
	OpSetState    OpKind = "set_state"
	OpAddEdge     OpKind = "add_edge"
	OpRemoveEdge  OpKind = "remove_edge"
	OpGroup       OpKind = "group"
	OpUngroup     OpKind = "ungroup"
	OpSetViewport OpKind = "set_viewport"
	OpSetSettings OpKind = "set_settings"
	OpSetParent   OpKind = "set_parent"
)

// AllOpKinds 列出受支持的 op 类型（供 docs 校验与 Agent 工具生成）。
func AllOpKinds() []OpKind {
	return []OpKind{OpAddNode, OpRemoveNode, OpMoveNode, OpResizeNode, OpSetTitle, OpSetSpec,
		OpSetState, OpAddEdge, OpRemoveEdge, OpGroup, OpUngroup, OpSetViewport, OpSetSettings, OpSetParent}
}

// Op 是画布写操作的统一结构。使用「显式字段 + patch」而不是任意 map，
// 保证未知 op 被拒绝（见 11 §2.4）。
type Op struct {
	Kind OpKind          `json:"kind"`
	Raw  json.RawMessage `json:"-"` // 原始 JSON，用于重放
}

// AddNodePayload 等为各 op 的参数结构。
type AddNodePayload struct {
	Node Node `json:"node"`
}

// RemoveNodePayload 删除节点（Cascade 表示连带删除其连线）。
type RemoveNodePayload struct {
	ID      string `json:"id"`
	Cascade *bool  `json:"cascade,omitempty"` // 默认 true
}

// NodeRef 通用节点引用。
type NodeRef struct {
	ID string `json:"id"`
}

// MoveNodePayload 移动节点。
type MoveNodePayload struct {
	ID string  `json:"id"`
	X  float64 `json:"x"`
	Y  float64 `json:"y"`
	// DeltaOf 非空时以「相对上次位置」语义应用，用于批量拖动合并提交。
	Delta bool `json:"delta,omitempty"`
}

// ResizeNodePayload 缩放节点。
type ResizeNodePayload struct {
	ID         string  `json:"id"`
	W          float64 `json:"w"`
	H          float64 `json:"h"`
	KeepAspect bool    `json:"keepAspect,omitempty"`
}

// SetTitlePayload 重命名。
type SetTitlePayload struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// SetSpecPayload 修改类型化配置。
// Unset 是显式删除语义：只允许移除字段，不接受 null 隐式清空（见 11 §2.4）。
type SetSpecPayload struct {
	ID    string         `json:"id"`
	Patch map[string]any `json:"patch,omitempty"`
	Unset []string       `json:"unset,omitempty"`
}

// SetStatePayload 修改运行态（仅执行层与回写路径使用）。
type SetStatePayload struct {
	ID     string      `json:"id"`
	State  NodeState   `json:"state"`
	Result *NodeResult `json:"result,omitempty"`
	Error  *NodeError  `json:"error,omitempty"`
}

// AddEdgePayload 新增连线。
type AddEdgePayload struct {
	Edge Edge `json:"edge"`
}

// RemoveEdgePayload 删除连线。
type RemoveEdgePayload struct {
	ID string `json:"id"`
}

// GroupPayload 打组：把 NodeIDs 归入 GroupID（GroupID 为空则新建分组节点）。
type GroupPayload struct {
	NodeIDs []string `json:"nodeIds"`
	GroupID string   `json:"groupId,omitempty"`
	Title   string   `json:"title,omitempty"`
	Rect    *Rect    `json:"rect,omitempty"`
}

// UngroupPayload 解散分组。
type UngroupPayload struct {
	GroupID string `json:"groupId"`
	// KeepChildren 为 true 时保留子节点（默认 true）。
	KeepChildren *bool `json:"keepChildren,omitempty"`
}

// SetViewportPayload 设置视口。
type SetViewportPayload struct {
	Viewport Viewport `json:"viewport"`
}

// SetSettingsPayload 修改画布设置（局部）。
type SetSettingsPayload struct {
	Settings map[string]any `json:"settings"`
}

// SetParentPayload 设置节点的父分组（拖入/拖出）。
type SetParentPayload struct {
	ID       string `json:"id"`
	ParentID string `json:"parentId,omitempty"`
}

// DecodeOp 解析一条 op 的 JSON 表示。
func DecodeOp(raw json.RawMessage) (Op, any, error) {
	if len(raw) > MaxRequestBytes {
		return Op{}, nil, NewError(413, CodePayloadTooLarge, "op too large")
	}
	// 只做「kind 可读」的最小校验；payload 的严格性由各 op 的 payload 结构负责。
	var head struct {
		Kind OpKind `json:"kind"`
	}
	if err := lenientUnmarshal(raw, &head); err != nil {
		return Op{}, nil, NewError(422, CodeUnknownField, "op is not readable: "+err.Error())
	}
	if head.Kind == "" {
		return Op{}, nil, NewError(422, CodeUnknownField, "op.kind is required")
	}
	if !knownOpKind(head.Kind) {
		return Op{}, nil, NewError(422, CodeUnknownField, "unknown op kind "+string(head.Kind)).
			WithDetail("known", AllOpKinds())
	}
	var payload any
	switch head.Kind {
	case OpAddNode:
		payload = &AddNodePayload{}
	case OpRemoveNode:
		payload = &RemoveNodePayload{}
	case OpMoveNode:
		payload = &MoveNodePayload{}
	case OpResizeNode:
		payload = &ResizeNodePayload{}
	case OpSetTitle:
		payload = &SetTitlePayload{}
	case OpSetSpec:
		payload = &SetSpecPayload{}
	case OpSetState:
		payload = &SetStatePayload{}
	case OpAddEdge:
		payload = &AddEdgePayload{}
	case OpRemoveEdge:
		payload = &RemoveEdgePayload{}
	case OpGroup:
		payload = &GroupPayload{}
	case OpUngroup:
		payload = &UngroupPayload{}
	case OpSetViewport:
		payload = &SetViewportPayload{}
	case OpSetSettings:
		payload = &SetSettingsPayload{}
	case OpSetParent:
		payload = &SetParentPayload{}
	}
	// 严格模式解析 payload：未知字段即拒绝（additionalProperties:false）。
	if err := strictExceptKind(raw, payload); err != nil {
		return Op{}, nil, NewError(422, CodeUnknownField,
			fmt.Sprintf("op %s payload invalid: %v", head.Kind, err))
	}
	return Op{Kind: head.Kind, Raw: append(json.RawMessage(nil), raw...)}, payload, nil
}

func knownOpKind(k OpKind) bool {
	for _, x := range AllOpKinds() {
		if x == k {
			return true
		}
	}
	return false
}

// OpError 是被拒绝的 op 及原因。
type OpError struct {
	Index  int    `json:"index"`
	Kind   OpKind `json:"kind"`
	Code   string `json:"code"`
	Reason string `json:"reason"`
}

// Error 实现 error。
func (e OpError) Error() string {
	return fmt.Sprintf("op[%d] %s rejected: %s", e.Index, e.Kind, e.Reason)
}

// ApplyResult 是一次批量的应用结果。
type ApplyResult struct {
	Version  int64     `json:"version"`
	Applied  int       `json:"applied"`
	Rejected []OpError `json:"rejected,omitempty"`
	Warnings []string  `json:"warnings,omitempty"`
	// Inverse 是反向 op，供撤销（Agent 操作撤销与前端 undo 复用）。
	Inverse []json.RawMessage `json:"inverse,omitempty"`
	// Rebased 表示本次提交与在途 op 发生了自动 rebase。
	Rebased bool `json:"rebased,omitempty"`
}

// Apply 在文档副本上应用一批 op。
// 语义：整批要么全部通过校验并应用，要么（strict=false 时）逐条跳过非法 op 并记录。
// 默认严格：任一 op 校验失败 → 全部回滚并返回 422 语义错误。
func Apply(doc *CanvasDocument, rawOps []json.RawMessage, actor string, now time.Time) (result ApplyResult, next *CanvasDocument, err error) {
	if len(rawOps) == 0 {
		// 空 op 列表是显式 no-op，不递增版本（见 11 §2.4）。
		return ApplyResult{Version: doc.Version}, doc, nil
	}
	if len(rawOps) > MaxOpBatch {
		return ApplyResult{}, nil, NewError(413, CodePayloadTooLarge, "too many ops in one batch").
			WithDetail("limit", MaxOpBatch).WithDetail("got", len(rawOps))
	}
	next = doc.Clone()
	inverse := make([]json.RawMessage, 0, len(rawOps))
	for i, raw := range rawOps {
		op, payload, derr := DecodeOp(raw)
		if derr != nil {
			return ApplyResult{}, nil, derr
		}
		inv, aerr := applyOne(next, op, payload, actor, now)
		if aerr != nil {
			de := asDomain(aerr, op)
			de.Details = mergeDetail(de.Details, "opIndex", i)
			return ApplyResult{}, nil, de
		}
		inverse = append(inverse, inv)
	}
	next.Version = doc.Version + 1
	next.UpdatedAt = now
	return ApplyResult{Version: next.Version, Applied: len(rawOps), Inverse: inverse}, next, nil
}

func mergeDetail(d map[string]any, k string, v any) map[string]any {
	if d == nil {
		d = map[string]any{}
	}
	d[k] = v
	return d
}

func asDomain(err error, op Op) *platform.DomainError {
	de := platform.AsDomainError(err)
	if de.Details == nil {
		de.Details = map[string]any{}
	}
	if _, ok := de.Details["opKind"]; !ok {
		de.Details["opKind"] = op.Kind
	}
	return de
}
