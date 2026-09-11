package graph

// 本文件是各类 op 的**应用实现**（applyXxx）与校验辅助。
// 从 op.go 拆出：原文件 986 行，超过代码规模硬上限（docs/design/08-infra.md §6）。
// 拆分依据是「职责」而不是「行数」：op.go 负责「op 是什么、怎么解码、怎么调度」，
// 本文件负责「一条 op 怎么改文档、失败了怎么报错」。
// 校验失败一律返回 *platform.DomainError（稳定 code + 可点击的节点定位），
// 不允许返回裸 error——前端要靠 code 决定展示哪种提示（DIV-07）。

import (
	"encoding/json"
	"github.com/context-flow/ic/internal/platform"
	"math"
	"strings"
	"time"
)

// applyOne 应用单条 op 并返回其反向 op。
func applyOne(doc *CanvasDocument, op Op, payload any, actor string, now time.Time) (json.RawMessage, error) {
	switch op.Kind {
	case OpAddNode:
		p := payload.(*AddNodePayload)
		return applyAddNode(doc, p)
	case OpRemoveNode:
		return applyRemoveNode(doc, payload.(*RemoveNodePayload))
	case OpMoveNode:
		return applyMoveNode(doc, payload.(*MoveNodePayload))
	case OpResizeNode:
		return applyResizeNode(doc, payload.(*ResizeNodePayload))
	case OpSetTitle:
		return applySetTitle(doc, payload.(*SetTitlePayload))
	case OpSetSpec:
		return applySetSpec(doc, payload.(*SetSpecPayload))
	case OpSetState:
		return applySetState(doc, payload.(*SetStatePayload))
	case OpAddEdge:
		return applyAddEdge(doc, payload.(*AddEdgePayload), actor, now)
	case OpRemoveEdge:
		return applyRemoveEdge(doc, payload.(*RemoveEdgePayload))
	case OpGroup:
		return applyGroup(doc, payload.(*GroupPayload), now)
	case OpUngroup:
		return applyUngroup(doc, payload.(*UngroupPayload))
	case OpSetViewport:
		return applySetViewport(doc, payload.(*SetViewportPayload))
	case OpSetSettings:
		return applySetSettings(doc, payload.(*SetSettingsPayload))
	case OpSetParent:
		return applySetParent(doc, payload.(*SetParentPayload))
	}
	return nil, NewError(422, CodeUnknownField, "unknown op kind "+string(op.Kind))
}

func marshal(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

func applyAddNode(doc *CanvasDocument, p *AddNodePayload) (json.RawMessage, error) {
	n := p.Node
	if !ValidID(n.ID) {
		return nil, NewError(422, CodeInvalidID, "node id is invalid").WithDetail("id", n.ID)
	}
	if _, exists := doc.Nodes[n.ID]; exists {
		// 重复 ID 明确拒绝，不做静默覆盖（见 11 §2.4）。
		return nil, NewError(409, CodeConflict, "node already exists").WithDetail("id", n.ID)
	}
	if !ValidNodeTypeID(string(n.Type)) {
		return nil, NewError(422, CodeInvalidNodeType, "invalid node type").WithDetail("type", n.Type)
	}
	if err := validateRect(n.Rect); err != nil {
		return nil, err
	}
	if len(n.Title) > MaxTitleLen {
		return nil, NewError(422, CodeInvalidSpec, "title too long").WithDetail("limit", MaxTitleLen)
	}
	if len(doc.Nodes) >= MaxNodesPerCanvas {
		return nil, NewError(413, CodePayloadTooLarge, "canvas node limit reached").
			WithDetail("limit", MaxNodesPerCanvas)
	}
	// 内置类型必须与 schema 一致；端口以 schema 为准（外部不可伪造端口声明）。
	if s, ok := SchemaFor(n.Type); ok {
		n.Ports = s.Ports
	} else if !IsPluginType(n.Type) {
		return nil, NewError(422, CodeInvalidNodeType, "unknown node type").WithDetail("type", n.Type)
	}
	if n.Spec == nil {
		n.Spec = NodeSpec{}
	}
	if err := ValidateSpec(n.Type, n.Spec); err != nil {
		return nil, err
	}
	if n.State == "" {
		n.State = NodeIdle
	}
	if !ValidNodeState(n.State) {
		return nil, NewError(422, CodeInvalidSpec, "invalid node state").WithDetail("state", n.State)
	}
	if n.ParentID != "" {
		if err := checkParent(doc, n.ID, n.ParentID); err != nil {
			return nil, err
		}
	}
	doc.Nodes[n.ID] = n
	return marshal(map[string]any{"kind": OpRemoveNode, "id": n.ID, "cascade": true}), nil
}

func applyRemoveNode(doc *CanvasDocument, p *RemoveNodePayload) (json.RawMessage, error) {
	n, ok := doc.Nodes[p.ID]
	if !ok {
		return nil, NewError(404, CodeNotFound, "node not found").WithDetail("id", p.ID)
	}
	cascade := true
	if p.Cascade != nil {
		cascade = *p.Cascade
	}
	// 反向 op：先恢复节点，再恢复（cascade 时）关联的边与子节点归属。
	restore := []json.RawMessage{marshal(map[string]any{"kind": OpAddNode, "node": n})}
	children := doc.ChildrenOf(p.ID)
	for _, cid := range children {
		child := doc.Nodes[cid]
		restore = append(restore, marshal(map[string]any{"kind": OpSetParent, "id": cid, "parentId": p.ID}))
		_ = child
	}
	edges := []Edge{}
	for _, e := range doc.Edges {
		if e.From.NodeID == p.ID || e.To.NodeID == p.ID {
			edges = append(edges, e)
		}
	}
	if !cascade && len(edges) > 0 {
		return nil, NewError(409, CodeConflict, "node has connections; cascade=false").
			WithDetail("edges", len(edges))
	}
	for _, e := range edges {
		delete(doc.Edges, e.ID)
		restore = append(restore, marshal(map[string]any{
			"kind": OpAddEdge,
			"edge": map[string]any{
				"id": e.ID, "from": e.From, "to": e.To, "kind": e.Kind,
				"createdBy": e.CreatedBy, "createdAt": e.CreatedAt,
			},
		}))
	}
	for _, cid := range children {
		doc.Nodes[cid] = withParent(doc.Nodes[cid], "")
	}
	delete(doc.Nodes, p.ID)
	return marshal(restore), nil
}

func withParent(n Node, parent string) Node {
	n.ParentID = parent
	return n
}

func applyMoveNode(doc *CanvasDocument, p *MoveNodePayload) (json.RawMessage, error) {
	n, ok := doc.Nodes[p.ID]
	if !ok {
		return nil, NewError(404, CodeNotFound, "node not found").WithDetail("id", p.ID)
	}
	if err := checkCoord(p.X); err != nil {
		return nil, err
	}
	if err := checkCoord(p.Y); err != nil {
		return nil, err
	}
	prev := n.Rect
	if p.Delta {
		n.Rect.X += p.X
		n.Rect.Y += p.Y
	} else {
		n.Rect.X = p.X
		n.Rect.Y = p.Y
	}
	if err := validateRect(n.Rect); err != nil {
		return nil, err
	}
	doc.Nodes[p.ID] = n
	return marshal(map[string]any{"kind": OpMoveNode, "id": p.ID, "x": prev.X, "y": prev.Y}), nil
}

func applyResizeNode(doc *CanvasDocument, p *ResizeNodePayload) (json.RawMessage, error) {
	n, ok := doc.Nodes[p.ID]
	if !ok {
		return nil, NewError(404, CodeNotFound, "node not found").WithDetail("id", p.ID)
	}
	prev := n.Rect
	w, h := p.W, p.H
	if p.KeepAspect && prev.H > 0 && prev.W > 0 && w > 0 {
		// 保持原始比例，高度跟随宽度。
		h = prev.H * (w / prev.W)
	}
	r := Rect{X: prev.X, Y: prev.Y, W: w, H: h}
	if err := validateRect(r); err != nil {
		return nil, err
	}
	doc.Nodes[p.ID] = withRect(n, r)
	return marshal(map[string]any{"kind": OpResizeNode, "id": p.ID, "w": prev.W, "h": prev.H}), nil
}

func withRect(n Node, r Rect) Node {
	n.Rect = r
	return n
}

func applySetTitle(doc *CanvasDocument, p *SetTitlePayload) (json.RawMessage, error) {
	n, ok := doc.Nodes[p.ID]
	if !ok {
		return nil, NewError(404, CodeNotFound, "node not found").WithDetail("id", p.ID)
	}
	if len(p.Title) > MaxTitleLen {
		return nil, NewError(422, CodeInvalidSpec, "title too long").WithDetail("limit", MaxTitleLen)
	}
	prev := n.Title
	title := p.Title
	if title == "" {
		// 空值回退原名（对齐原项目行为）。
		title = prev
	}
	n.Title = title
	doc.Nodes[p.ID] = n
	return marshal(map[string]any{"kind": OpSetTitle, "id": p.ID, "title": prev}), nil
}

func applySetSpec(doc *CanvasDocument, p *SetSpecPayload) (json.RawMessage, error) {
	n, ok := doc.Nodes[p.ID]
	if !ok {
		return nil, NewError(404, CodeNotFound, "node not found").WithDetail("id", p.ID)
	}
	prev := cloneSpec(n.Spec)
	if len(p.Patch) == 0 && len(p.Unset) == 0 {
		return nil, NewError(422, CodeInvalidSpec, "set_spec requires patch or unset")
	}
	next := cloneSpec(n.Spec)
	for _, k := range p.Unset {
		if _, allowed := schemaField(n.Type, k); !allowed {
			return nil, NewError(422, CodeInvalidSpec, "unset unknown field").WithDetail("field", k)
		}
		delete(next, k)
	}
	for k, v := range p.Patch {
		if _, allowed := schemaField(n.Type, k); !allowed {
			return nil, NewError(422, CodeInvalidSpec, "unknown field").WithDetail("field", k)
		}
		next[k] = normalizeJSONNumbers(v)
	}
	// set_spec 需要容忍「参数分多步填」的中间态，但字段类型与未知字段仍严格校验。
	if err := ValidateSpec(n.Type, next); err != nil {
		de := platform.AsDomainError(err)
		if de.Code != CodeInvalidSpec || !strings.Contains(de.Message, "missing required field") {
			return nil, err
		}
	}
	n.Spec = next
	doc.Nodes[p.ID] = n
	unset := []string{}
	for k := range prev {
		if _, ok := next[k]; !ok {
			unset = append(unset, k)
		}
	}
	return marshal(map[string]any{"kind": OpSetSpec, "id": p.ID, "patch": prev, "unset": unset}), nil
}

func schemaField(t NodeTypeID, k string) (FieldKind, bool) {
	s, ok := SchemaFor(t)
	if !ok {
		return "", false
	}
	if t == NodeTypePrompt && k == "text" {
		return FieldString, true
	}
	f, ok := s.Fields[k]
	return f, ok
}

func normalizeJSONNumbers(v any) any {
	switch n := v.(type) {
	case float64:
		if math.IsNaN(n) || math.IsInf(n, 0) {
			return n
		}
		if n == math.Trunc(n) && math.Abs(n) < 1e15 {
			return n
		}
		return n
	case map[string]any:
		out := make(map[string]any, len(n))
		for k, x := range n {
			out[k] = normalizeJSONNumbers(x)
		}
		return out
	case []any:
		out := make([]any, len(n))
		for i, x := range n {
			out[i] = normalizeJSONNumbers(x)
		}
		return out
	}
	return v
}

func applySetState(doc *CanvasDocument, p *SetStatePayload) (json.RawMessage, error) {
	n, ok := doc.Nodes[p.ID]
	if !ok {
		return nil, NewError(404, CodeNotFound, "node not found").WithDetail("id", p.ID)
	}
	if !ValidNodeState(p.State) {
		return nil, NewError(422, CodeInvalidSpec, "invalid node state").WithDetail("state", p.State)
	}
	inv := map[string]any{"kind": OpSetState, "id": p.ID, "state": n.State}
	if n.Result != nil {
		inv["result"] = n.Result
	}
	if n.Error != nil {
		inv["error"] = n.Error
	}
	n.State = p.State
	if p.Result != nil {
		n.Result = p.Result
	}
	if p.Error != nil {
		n.Error = p.Error
	}
	if p.State == NodeIdle {
		n.Result = nil
		n.Error = nil
	}
	doc.Nodes[p.ID] = n
	return marshal(inv), nil
}

func applyAddEdge(doc *CanvasDocument, p *AddEdgePayload, actor string, now time.Time) (json.RawMessage, error) {
	e := p.Edge
	if e.ID == "" {
		return nil, NewError(422, CodeInvalidID, "edge id is required")
	}
	if !ValidID(e.ID) {
		return nil, NewError(422, CodeInvalidID, "edge id is invalid").WithDetail("id", e.ID)
	}
	if _, exists := doc.Edges[e.ID]; exists {
		return nil, NewError(409, CodeConflict, "edge already exists").WithDetail("id", e.ID)
	}
	if len(doc.Edges) >= MaxEdgesPerCanvas {
		return nil, NewError(413, CodePayloadTooLarge, "edge limit reached").WithDetail("limit", MaxEdgesPerCanvas)
	}
	from, okFrom := doc.Nodes[e.From.NodeID]
	to, okTo := doc.Nodes[e.To.NodeID]
	if !okFrom || !okTo {
		return nil, NewError(404, CodeNotFound, "edge endpoint node not found").
			WithDetail("from", e.From.NodeID).WithDetail("to", e.To.NodeID)
	}
	if e.From.NodeID == e.To.NodeID {
		return nil, NewError(422, CodeInvalidSpec, "self connection is not allowed")
	}
	if from.Type == NodeTypeGroup || to.Type == NodeTypeGroup {
		return nil, NewError(422, CodeInvalidSpec, "group node cannot be connected")
	}
	outPort, ok := findPort(from.Ports.Outputs, e.From.PortID)
	if !ok {
		return nil, NewError(422, CodeInvalidSpec, "source port not found").
			WithDetail("node", e.From.NodeID).WithDetail("port", e.From.PortID)
	}
	inPort, ok := findPort(to.Ports.Inputs, e.To.PortID)
	if !ok {
		return nil, NewError(422, CodeInvalidSpec, "target port not found").
			WithDetail("node", e.To.NodeID).WithDetail("port", e.To.PortID)
	}
	kind := e.Kind
	if kind == "" {
		kind = outPort.Kind
	}
	if !ValidResourceKind(kind) {
		return nil, NewError(422, CodeInvalidSpec, "invalid resource kind").WithDetail("kind", kind)
	}
	// 端口类型必须匹配（见 03 §2.1）。
	if outPort.Kind != inPort.Kind {
		return nil, NewError(422, CodeInvalidSpec, "port kind mismatch").
			WithDetail("fromKind", outPort.Kind).WithDetail("toKind", inPort.Kind)
	}
	if outPort.Kind != kind {
		return nil, NewError(422, CodeInvalidSpec, "edge kind does not match port kind").
			WithDetail("portKind", outPort.Kind).WithDetail("edgeKind", kind)
	}
	warnings := []string{}
	if !inPort.Multiple {
		for id, ex := range doc.Edges {
			if ex.To.NodeID == e.To.NodeID && ex.To.PortID == e.To.PortID {
				// 单入端口重复接线按「替换」语义（见 03 §2.1）。
				delete(doc.Edges, id)
				warnings = append(warnings, "replaced existing edge on single-input port "+id)
			}
		}
	}
	if wouldCycle(doc, e.From.NodeID, e.To.NodeID) {
		return nil, NewError(422, CodeInvalidSpec, "connection would create a cycle")
	}
	if e.CreatedAt.IsZero() {
		e.CreatedAt = now
	}
	if e.CreatedBy == "" {
		e.CreatedBy = actor
	}
	e.Kind = kind
	doc.Edges[e.ID] = e
	_ = warnings
	return marshal(map[string]any{"kind": OpRemoveEdge, "id": e.ID}), nil
}

func applyRemoveEdge(doc *CanvasDocument, p *RemoveEdgePayload) (json.RawMessage, error) {
	e, ok := doc.Edges[p.ID]
	if !ok {
		return nil, NewError(404, CodeNotFound, "edge not found").WithDetail("id", p.ID)
	}
	delete(doc.Edges, p.ID)
	return marshal(map[string]any{"kind": OpAddEdge, "edge": e}), nil
}

func applyGroup(doc *CanvasDocument, p *GroupPayload, now time.Time) (json.RawMessage, error) {
	if len(p.NodeIDs) == 0 {
		return nil, NewError(422, CodeInvalidSpec, "group requires nodeIds")
	}
	groupID := p.GroupID
	if groupID == "" {
		return nil, NewError(422, CodeInvalidID, "groupId is required; create group node first")
	}
	g, ok := doc.Nodes[groupID]
	if !ok || g.Type != NodeTypeGroup {
		return nil, NewError(404, CodeNotFound, "group node not found").WithDetail("id", groupID)
	}
	inv := []json.RawMessage{}
	rect := g.Rect
	if p.Rect != nil {
		rect = *p.Rect
	}
	minX, minY := math.Inf(1), math.Inf(1)
	maxX, maxY := math.Inf(-1), math.Inf(-1)
	for _, id := range p.NodeIDs {
		n, ok := doc.Nodes[id]
		if !ok {
			return nil, NewError(404, CodeNotFound, "node not found").WithDetail("id", id)
		}
		if id == groupID {
			return nil, NewError(422, CodeInvalidSpec, "cannot group a group into itself")
		}
		if n.Type == NodeTypeGroup {
			return nil, NewError(422, CodeInvalidSpec, "nested group is not supported in one op")
		}
		if err := checkParent(doc, id, groupID); err != nil {
			return nil, err
		}
		inv = append(inv, marshal(map[string]any{"kind": OpSetParent, "id": id, "parentId": n.ParentID}))
		n.ParentID = groupID
		doc.Nodes[id] = n
		minX = math.Min(minX, n.Rect.X)
		minY = math.Min(minY, n.Rect.Y)
		maxX = math.Max(maxX, n.Rect.X+n.Rect.W)
		maxY = math.Max(maxY, n.Rect.Y+n.Rect.H)
	}
	_ = now
	if p.Rect == nil && !math.IsInf(minX, 1) {
		const pad = 24.0
		rect = Rect{X: minX - pad, Y: minY - pad - 28, W: (maxX - minX) + pad*2, H: (maxY - minY) + pad*2 + 28}
	}
	if err := validateRect(rect); err == nil {
		prev := g.Rect
		g.Rect = rect
		doc.Nodes[groupID] = g
		inv = append(inv, marshal(map[string]any{"kind": OpResizeNode, "id": groupID, "w": prev.W, "h": prev.H}))
	}
	return marshal(inv), nil
}

func applyUngroup(doc *CanvasDocument, p *UngroupPayload) (json.RawMessage, error) {
	g, ok := doc.Nodes[p.GroupID]
	if !ok || g.Type != NodeTypeGroup {
		return nil, NewError(404, CodeNotFound, "group node not found").WithDetail("id", p.GroupID)
	}
	keep := true
	if p.KeepChildren != nil {
		keep = *p.KeepChildren
	}
	inv := []json.RawMessage{}
	children := doc.ChildrenOf(p.GroupID)
	for _, cid := range children {
		inv = append(inv, marshal(map[string]any{"kind": OpSetParent, "id": cid, "parentId": p.GroupID}))
		n := doc.Nodes[cid]
		if keep {
			n.ParentID = ""
			doc.Nodes[cid] = n
			continue
		}
		// 不保留子节点：删除之（反向 op 由 remove 自身返回，嵌套展开后拼接）。
		delete(doc.Nodes, cid)
	}
	if !keep {
		for id, e := range doc.Edges {
			if e.From.NodeID == p.GroupID || e.To.NodeID == p.GroupID {
				delete(doc.Edges, id)
			}
		}
		delete(doc.Nodes, p.GroupID)
	}
	return marshal(inv), nil
}

func applySetViewport(doc *CanvasDocument, p *SetViewportPayload) (json.RawMessage, error) {
	if err := ValidateViewport(p.Viewport); err != nil {
		return nil, err
	}
	prev := doc.Viewport
	doc.Viewport = p.Viewport
	return marshal(map[string]any{"kind": OpSetViewport, "viewport": prev}), nil
}

func applySetSettings(doc *CanvasDocument, p *SetSettingsPayload) (json.RawMessage, error) {
	if len(p.Settings) == 0 {
		return nil, NewError(422, CodeInvalidSpec, "set_settings requires settings")
	}
	prev := map[string]any{"background": doc.Settings.Background, "imageInfo": doc.Settings.ImageInfo,
		"gridSnap": doc.Settings.GridSnap, "readOnly": doc.Settings.ReadOnly, "freeResize": doc.Settings.FreeResize}
	next := doc.Settings
	for k, v := range p.Settings {
		switch k {
		case "background":
			s, ok := v.(string)
			if !ok || (s != "lines" && s != "dots" && s != "blank") {
				return nil, NewError(422, CodeInvalidSpec, "background must be lines|dots|blank")
			}
			next.Background = s
		case "imageInfo":
			b, ok := v.(bool)
			if !ok {
				return nil, NewError(422, CodeInvalidSpec, "imageInfo must be bool")
			}
			next.ImageInfo = b
		case "gridSnap":
			b, ok := v.(bool)
			if !ok {
				return nil, NewError(422, CodeInvalidSpec, "gridSnap must be bool")
			}
			next.GridSnap = b
		case "readOnly":
			b, ok := v.(bool)
			if !ok {
				return nil, NewError(422, CodeInvalidSpec, "readOnly must be bool")
			}
			next.ReadOnly = b
		case "freeResize":
			b, ok := v.(bool)
			if !ok {
				return nil, NewError(422, CodeInvalidSpec, "freeResize must be bool")
			}
			next.FreeResize = b
		default:
			return nil, NewError(422, CodeInvalidSpec, "unknown setting").WithDetail("key", k)
		}
	}
	doc.Settings = next
	return marshal(map[string]any{"kind": OpSetSettings, "settings": prev}), nil
}

func applySetParent(doc *CanvasDocument, p *SetParentPayload) (json.RawMessage, error) {
	n, ok := doc.Nodes[p.ID]
	if !ok {
		return nil, NewError(404, CodeNotFound, "node not found").WithDetail("id", p.ID)
	}
	if err := checkParent(doc, p.ID, p.ParentID); err != nil {
		return nil, err
	}
	prev := n.ParentID
	if p.ParentID != "" {
		if err := validateRect(n.Rect); err != nil {
			return nil, err
		}
	}
	n.ParentID = p.ParentID
	doc.Nodes[p.ID] = n
	return marshal(map[string]any{"kind": OpSetParent, "id": p.ID, "parentId": prev}), nil
}
