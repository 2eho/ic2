package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/context-flow/ic/internal/graph"
	"github.com/context-flow/ic/internal/platform"
)

// CanvasGateway 是 Agent 修改画布的唯一通道（走标准 op 路径）。
type CanvasGateway interface {
	AppendOps(ctx context.Context, canvasID string, baseVersion int64, ops []json.RawMessage, actor string) (graph.ApplyResult, *graph.CanvasDocument, error)
	Get(ctx context.Context, canvasID string) (*graph.CanvasDocument, error)
}

// ModelClient 是「服务端直接承载 Agent」形态的模型客户端（形态 A）。
type ModelClient interface {
	// Complete 返回文本回复与工具调用意图。
	Complete(ctx context.Context, req CompletionRequest) (CompletionResult, error)
}

// CompletionRequest 是一次模型调用。
type CompletionRequest struct {
	Model    string
	System   string
	Messages []Message
	Tools    []ToolDef
	// Snapshot 是画布快照（压缩后的），供模型理解当前状态。
	Snapshot json.RawMessage
}

// Message 是对话消息。
type Message struct {
	Role    string `json:"role"` // system | user | assistant | tool
	Content string `json:"content"`
}

// CompletionResult 是模型回复。
type CompletionResult struct {
	Text      string
	ToolCalls []ToolCall
	Usage     Usage
}

// ToolCall 是模型请求的工具调用。
type ToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// RunTrigger 触发一次生成运行（由 api/exec 侧实现）。
//
// 为什么用回调而不是直接依赖 exec：internal/agent 与 internal/exec 是同级领域包，
// 互相 import 会形成循环依赖，也会让 agent 的测试被迫拉起整个执行引擎。
// 回调把「触发运行」变成一个明确的注入点，同时保留了「agent 不做特权操作」这一约束。
type RunTrigger interface {
	TriggerRun(ctx context.Context, canvasID string, nodeIDs []string, actor string) (runID string, err error)
}

// AssetLister 在工作区素材库中检索（由 asset 侧实现）。
type AssetLister interface {
	SearchAssets(ctx context.Context, wsID, query, kind string, limit int) ([]map[string]any, error)
}

// PromptSearcher 在提示词库中检索（由 prompt 侧实现）。
type PromptSearcher interface {
	SearchPrompts(ctx context.Context, wsID, query string, tags []string, limit int) ([]map[string]any, error)
}

// RunLister 列出运行记录（由 exec 侧实现）。
type RunLister interface {
	ListRunsBrief(ctx context.Context, wsID, canvasID string, limit int) ([]map[string]any, error)
	GetRunBrief(ctx context.Context, wsID, runID string) (map[string]any, error)
}

// SkillStore 读写 Agent Skills（9.14）。
type SkillStore interface {
	ListSkills(ctx context.Context, wsID string) ([]map[string]any, error)
	UpsertSkill(ctx context.Context, wsID string, skill map[string]any) (map[string]any, error)
	DeleteSkill(ctx context.Context, wsID, name string) error
}

// Service 是 Agent 网关。
type Service struct {
	db     *sql.DB
	canvas CanvasGateway
	model  ModelClient
	clock  platform.Clock
	ids    platform.IDGen
	// snapshotLimit 是画布快照中单节点内容的截断长度（对齐原项目 240 字符）。
	snapshotLimit int

	// 以下为可选能力注入：未注入时对应工具返回明确的 not_implemented，
	// 而不是「返回 ok 但什么都没做」（上一轮 canvas.run_generation 就是这个毛病）。
	runs    RunTrigger
	assets  AssetLister
	prompts PromptSearcher
	runList RunLister
	skills  SkillStore
	// downloads 把 URL 落成资产（附件转节点用）。
	files AttachmentFetcher
}

// AttachmentFetcher 把外部资源/内联数据落成工作区资产（9.7）。
type AttachmentFetcher interface {
	StoreAttachment(ctx context.Context, wsID, name, mime, url, dataURI string) (assetID string, err error)
}

// Wire 注入可选能力。集中成一个方法而不是若干 SetXxx：
// 装配方一眼能看到「agent 依赖哪些外部能力」，漏注入会在测试里直接暴露。
type Wire struct {
	Runs    RunTrigger
	Assets  AssetLister
	Prompts PromptSearcher
	RunList RunLister
	Skills  SkillStore
	Files   AttachmentFetcher
}

// SetCapabilities 注入外部能力。
func (s *Service) SetCapabilities(w Wire) {
	s.runs, s.assets, s.prompts, s.runList, s.skills, s.files = w.Runs, w.Assets, w.Prompts, w.RunList, w.Skills, w.Files
}

// Options 构造参数。
type Options struct {
	DB            *sql.DB
	Canvas        CanvasGateway
	Model         ModelClient
	Clock         platform.Clock
	IDs           platform.IDGen
	SnapshotLimit int
}

// New 构造 Agent 服务。
func New(o Options) *Service {
	if o.Clock == nil {
		o.Clock = platform.SystemClock()
	}
	if o.IDs == nil {
		o.IDs = platform.DefaultIDGen()
	}
	if o.SnapshotLimit <= 0 {
		o.SnapshotLimit = 240
	}
	return &Service{db: o.DB, canvas: o.Canvas, model: o.Model, clock: o.Clock, ids: o.IDs, snapshotLimit: o.SnapshotLimit}
}

// CreateSession 创建会话。
func (s *Service) CreateSession(ctx context.Context, wsID, canvasID string, backend Backend, title string) (*Session, error) {
	if wsID == "" {
		return nil, platform.ErrInvalid("workspaceId is required")
	}
	if backend == "" {
		backend = BackendHTTP
	}
	sess := &Session{
		ID:          s.ids.NewID("as"),
		WorkspaceID: wsID,
		CanvasID:    canvasID,
		Backend:     backend,
		ThreadID:    s.ids.NewID("th"),
		Title:       title,
		Permission:  PermRequest,
		CreatedAt:   s.clock.Now().UTC(),
	}
	if sess.Title == "" {
		sess.Title = "新会话"
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO agent_sessions (id, workspace_id, canvas_id, backend, thread_id, title, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		sess.ID, sess.WorkspaceID, sess.CanvasID, string(sess.Backend), sess.ThreadID, sess.Title, sess.CreatedAt); err != nil {
		return nil, platform.AsError(err)
	}
	return sess, nil
}

// GetSession 读取会话与全部轮次。
func (s *Service) GetSession(ctx context.Context, sessionID string) (*Session, error) {
	var sess Session
	var created string
	if err := s.db.QueryRowContext(ctx,
		`SELECT id, workspace_id, canvas_id, backend, COALESCE(thread_id,''), title, created_at
		 FROM agent_sessions WHERE id = ?`, sessionID).Scan(
		&sess.ID, &sess.WorkspaceID, &sess.CanvasID, &sess.Backend, &sess.ThreadID, &sess.Title, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, platform.ErrNotFound("agent session")
		}
		return nil, platform.AsError(err)
	}
	sess.CreatedAt = parseTime(created)
	sess.Permission = PermRequest

	turns, err := s.turns(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	sess.Turns = turns
	return &sess, nil
}

func (s *Service) turns(ctx context.Context, sessionID string) ([]*Turn, error) {
	// 两阶段：先把行读完并关闭，再逐轮加载条目。
	// 原因：sqlite 在 max_open_conns=1 时，行迭代期间发起新查询会与自身互等。
	type row struct {
		id, status, created string
		seq                 int
	}
	raw, err := func() ([]row, error) {
		rows, err := s.db.QueryContext(ctx,
			`SELECT id, seq, status, created_at FROM agent_turns WHERE session_id = ? ORDER BY seq`, sessionID)
		if err != nil {
			return nil, platform.AsError(err)
		}
		defer rows.Close()
		out := []row{}
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.id, &r.seq, &r.status, &r.created); err != nil {
				return nil, platform.AsError(err)
			}
			out = append(out, r)
		}
		return out, platform.AsError(rows.Err())
	}()
	if err != nil {
		return nil, err
	}

	out := make([]*Turn, 0, len(raw))
	for _, r := range raw {
		t := &Turn{ID: r.id, SessionID: sessionID, Seq: r.seq, Status: TurnStatus(r.status), CreatedAt: parseTime(r.created)}
		items, err := s.items(ctx, t.ID)
		if err != nil {
			return nil, err
		}
		t.Items = items
		out = append(out, t)
	}
	return out, nil
}

func (s *Service) items(ctx context.Context, turnID string) ([]Item, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT item_id, seq, kind, payload, source, created_at FROM agent_items WHERE turn_id = ? ORDER BY seq`, turnID)
	if err != nil {
		return nil, platform.AsError(err)
	}
	defer rows.Close()
	out := []Item{}
	for rows.Next() {
		var it Item
		var payload, created string
		if err := rows.Scan(&it.ID, &it.Seq, &it.Kind, &payload, &it.Source, &created); err != nil {
			return nil, platform.AsError(err)
		}
		it.TurnID = turnID
		it.Payload = json.RawMessage(payload)
		it.CreatedAt = parseTime(created)
		out = append(out, it)
	}
	return out, platform.AsError(rows.Err())
}

// UpsertItem 写入条目。
//
// 幂等由主键 (turn_id, item_id) 保证（INV-7）：
// 实时事件与历史快照写入同一张表，重复写入不会产生重复条目。
// 快照来源的写入会覆盖 live 来源的同 ID 条目（快照为权威）。
func (s *Service) UpsertItem(ctx context.Context, item Item) error {
	if item.ID == "" || item.TurnID == "" {
		return platform.ErrInvalid("item id and turnId are required")
	}
	if item.Source == "" {
		item.Source = SourceLive
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = s.clock.Now().UTC()
	}
	// 冲突时的策略：快照优先。用 CASE 表达式实现，避免读-改-写竞态。
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO agent_items (turn_id, item_id, seq, kind, payload, source, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(turn_id, item_id) DO UPDATE SET
		   payload = CASE WHEN excluded.source = 'snapshot' THEN excluded.payload ELSE agent_items.payload END,
		   source  = CASE WHEN excluded.source = 'snapshot' THEN 'snapshot' ELSE agent_items.source END`,
		item.TurnID, item.ID, item.Seq, string(item.Kind), string(item.Payload), string(item.Source), item.CreatedAt)
	return platform.AsError(err)
}

// Snapshot 生成画布快照（用于模型上下文）。
// 节点内容按 snapshotLimit 截断，避免上下文爆炸（对齐原项目 compactNode 语义）。
func (s *Service) Snapshot(ctx context.Context, canvasID string) (json.RawMessage, error) {
	doc, err := s.canvas.Get(ctx, canvasID)
	if err != nil {
		return nil, err
	}
	type compactNode struct {
		ID       string         `json:"id"`
		Type     string         `json:"type"`
		Title    string         `json:"title"`
		Rect     graph.Rect     `json:"rect"`
		ParentID string         `json:"parentId,omitempty"`
		State    string         `json:"state"`
		Summary  map[string]any `json:"summary,omitempty"`
	}
	nodes := make([]compactNode, 0, len(doc.Nodes))
	for _, id := range doc.NodeIDs() {
		n := doc.Nodes[id]
		summary := map[string]any{}
		if text, ok := n.Spec["text"].(string); ok {
			summary["text"] = truncate(text, s.snapshotLimit)
		}
		if cap, ok := n.Spec["capability"].(string); ok {
			summary["capability"] = cap
		}
		if model, ok := n.Spec["model"].(string); ok {
			summary["model"] = model
		}
		if asset, ok := n.Spec["assetId"].(string); ok {
			summary["assetId"] = asset
		}
		if n.Result != nil {
			summary["variants"] = len(n.Result.Variants)
			summary["primary"] = n.Result.Primary
		}
		nodes = append(nodes, compactNode{
			ID: n.ID, Type: string(n.Type), Title: n.Title, Rect: n.Rect,
			ParentID: n.ParentID, State: string(n.State), Summary: summary,
		})
	}
	edges := make([]map[string]string, 0, len(doc.Edges))
	for _, eid := range doc.EdgeIDs() {
		e := doc.Edges[eid]
		edges = append(edges, map[string]string{
			"id": e.ID, "from": e.From.NodeID + ":" + e.From.PortID,
			"to": e.To.NodeID + ":" + e.To.PortID, "kind": string(e.Kind),
		})
	}
	out, err := json.Marshal(map[string]any{
		"canvasId": doc.ID, "version": doc.Version, "nodes": nodes, "edges": edges,
	})
	if err != nil {
		return nil, platform.AsError(err)
	}
	return out, nil
}

// ExecuteTool 执行一次工具调用。
//
// 写操作走 CanvasGateway.AppendOps —— 与用户编辑、执行回写**完全同一条路径**，
// 不存在「某条路径绕过校验」的隐患。
func (s *Service) ExecuteTool(ctx context.Context, canvasID, actor string, call ToolCall) (ToolCallResult, error) {
	def, ok := ToolByName(call.Name)
	if !ok {
		return ToolCallResult{CallID: call.ID, Status: "error",
			Error: &ToolError{Code: "unknown_tool", Message: call.Name}}, nil
	}
	_ = def
	switch call.Name {
	case "canvas.get_state", "canvas.get_selection", "canvas.export_snapshot":
		snap, err := s.Snapshot(ctx, canvasID)
		if err != nil {
			return ToolCallResult{CallID: call.ID, Status: "error", Error: &ToolError{Code: platform.CodeNotFound, Message: err.Error()}}, nil
		}
		return ToolCallResult{CallID: call.ID, Status: "ok", Result: snap}, nil

	case "assets.search":
		if s.assets == nil {
			return notWired(call, "assets.search"), nil
		}
		var args struct {
			Query string `json:"query"`
			Kind  string `json:"kind"`
			Limit int    `json:"limit"`
		}
		if err := strictDecode(call.Arguments, &args); err != nil {
			return invalidArgs(call, err), nil
		}
		items, err := s.assets.SearchAssets(ctx, s.workspaceOf(ctx, canvasID), args.Query, args.Kind, clampLimit(args.Limit, 20))
		if err != nil {
			return toolError(call, err), nil
		}
		return toolOK(call, map[string]any{"items": items}), nil

	case "prompts.search":
		if s.prompts == nil {
			return notWired(call, "prompts.search"), nil
		}
		var args struct {
			Query string   `json:"query"`
			Tags  []string `json:"tags"`
			Limit int      `json:"limit"`
		}
		if err := strictDecode(call.Arguments, &args); err != nil {
			return invalidArgs(call, err), nil
		}
		items, err := s.prompts.SearchPrompts(ctx, s.workspaceOf(ctx, canvasID), args.Query, args.Tags, clampLimit(args.Limit, 20))
		if err != nil {
			return toolError(call, err), nil
		}
		return toolOK(call, map[string]any{"items": items}), nil

	case "runs.list":
		if s.runList == nil {
			return notWired(call, "runs.list"), nil
		}
		var args struct {
			Limit int `json:"limit"`
		}
		_ = strictDecode(call.Arguments, &args)
		items, err := s.runList.ListRunsBrief(ctx, s.workspaceOf(ctx, canvasID), canvasID, clampLimit(args.Limit, 20))
		if err != nil {
			return toolError(call, err), nil
		}
		return toolOK(call, map[string]any{"items": items}), nil

	case "runs.get":
		if s.runList == nil {
			return notWired(call, "runs.get"), nil
		}
		var args struct {
			RunID string `json:"runId"`
		}
		if err := strictDecode(call.Arguments, &args); err != nil || args.RunID == "" {
			return invalidArgs(call, errors.New("runId is required")), nil
		}
		item, err := s.runList.GetRunBrief(ctx, s.workspaceOf(ctx, canvasID), args.RunID)
		if err != nil {
			return toolError(call, err), nil
		}
		return toolOK(call, item), nil

	case "canvas.create_attachment_nodes":
		return s.createAttachmentNodes(ctx, canvasID, actor, call)

	case "skills.list":
		if s.skills == nil {
			return notWired(call, "skills.list"), nil
		}
		wsID := s.workspaceOf(ctx, canvasID)
		if wsID == "" {
			return toolError(call, platform.ErrNotFound("workspace")), nil
		}
		items, err := s.skills.ListSkills(ctx, wsID)
		if err != nil {
			return toolError(call, err), nil
		}
		return toolOK(call, map[string]any{"items": items}), nil

	case "skills.save":
		if s.skills == nil {
			return notWired(call, "skills.save"), nil
		}
		wsID := s.workspaceOf(ctx, canvasID)
		if wsID == "" {
			return toolError(call, platform.ErrNotFound("workspace")), nil
		}
		var args map[string]any
		if err := strictDecode(call.Arguments, &args); err != nil {
			return invalidArgs(call, err), nil
		}
		saved, err := s.skills.UpsertSkill(ctx, wsID, args)
		if err != nil {
			return toolError(call, err), nil
		}
		return toolOK(call, saved), nil

	case "canvas.apply_ops":
		var args struct {
			Ops []json.RawMessage `json:"ops"`
		}
		if err := strictDecode(call.Arguments, &args); err != nil {
			return ToolCallResult{CallID: call.ID, Status: "error", Error: &ToolError{Code: platform.CodeInvalidRequest, Message: err.Error()}}, nil
		}
		if len(args.Ops) == 0 {
			return ToolCallResult{CallID: call.ID, Status: "error", Error: &ToolError{Code: platform.CodeInvalidRequest, Message: "ops is required"}}, nil
		}
		doc, err := s.canvas.Get(ctx, canvasID)
		if err != nil {
			return ToolCallResult{CallID: call.ID, Status: "error", Error: &ToolError{Code: platform.CodeNotFound, Message: err.Error()}}, nil
		}
		res, _, err := s.canvas.AppendOps(ctx, canvasID, doc.Version, args.Ops, actor)
		if err != nil {
			de := platform.AsDomainError(err)
			return ToolCallResult{CallID: call.ID, Status: "error",
				Error: &ToolError{Code: de.Code, Message: de.Message}}, nil
		}
		inverse, _ := json.Marshal(res.Inverse)
		return ToolCallResult{CallID: call.ID, Status: "ok",
			Applied: &AppliedInfo{Ops: res.Applied, Version: res.Version}, Inverse: inverse}, nil

	case "canvas.create_text_node":
		var args struct {
			Text  string  `json:"text"`
			Title string  `json:"title"`
			X     float64 `json:"x"`
			Y     float64 `json:"y"`
		}
		if err := strictDecode(call.Arguments, &args); err != nil || args.Text == "" {
			return ToolCallResult{CallID: call.ID, Status: "error",
				Error: &ToolError{Code: platform.CodeInvalidRequest, Message: "text is required"}}, nil
		}
		title := args.Title
		if title == "" {
			title = "提示词"
		}
		nodeID := s.ids.NewID("n")
		op, _ := json.Marshal(map[string]any{
			"kind": "add_node",
			"node": map[string]any{
				"id": nodeID, "type": "prompt", "title": title,
				"rect": map[string]any{"x": args.X, "y": args.Y, "w": 320, "h": 220},
				"spec": map[string]any{"text": args.Text},
			},
		})
		doc, err := s.canvas.Get(ctx, canvasID)
		if err != nil {
			return ToolCallResult{CallID: call.ID, Status: "error", Error: &ToolError{Code: platform.CodeNotFound, Message: err.Error()}}, nil
		}
		res, _, err := s.canvas.AppendOps(ctx, canvasID, doc.Version, []json.RawMessage{op}, actor)
		if err != nil {
			de := platform.AsDomainError(err)
			return ToolCallResult{CallID: call.ID, Status: "error", Error: &ToolError{Code: de.Code, Message: de.Message}}, nil
		}
		inverse, _ := json.Marshal(res.Inverse)
		result, _ := json.Marshal(map[string]any{"nodeId": nodeID})
		return ToolCallResult{CallID: call.ID, Status: "ok",
			Applied: &AppliedInfo{Ops: res.Applied, Version: res.Version}, Inverse: inverse, Result: result}, nil

	case "canvas.create_generation_flow":
		var args struct {
			Prompt     string  `json:"prompt"`
			Capability string  `json:"capability"`
			Model      string  `json:"model"`
			X          float64 `json:"x"`
			Y          float64 `json:"y"`
		}
		if err := strictDecode(call.Arguments, &args); err != nil || args.Prompt == "" {
			return ToolCallResult{CallID: call.ID, Status: "error",
				Error: &ToolError{Code: platform.CodeInvalidRequest, Message: "prompt is required"}}, nil
		}
		capability := args.Capability
		if capability == "" {
			capability = "image.generate"
		}
		promptID := s.ids.NewID("n")
		genID := s.ids.NewID("n")
		edgeID := s.ids.NewID("e")
		ops := []json.RawMessage{}
		for _, spec := range []map[string]any{
			{"kind": "add_node", "node": map[string]any{
				"id": promptID, "type": "prompt", "title": "提示词",
				"rect": map[string]any{"x": args.X, "y": args.Y, "w": 320, "h": 220},
				"spec": map[string]any{"text": args.Prompt}}},
			{"kind": "add_node", "node": map[string]any{
				"id": genID, "type": "generation", "title": "生成",
				"rect": map[string]any{"x": args.X + 380, "y": args.Y, "w": 340, "h": 260},
				"spec": map[string]any{"capability": capability, "model": args.Model, "outputCount": 1}}},
			{"kind": "add_edge", "edge": map[string]any{
				"id":   edgeID,
				"from": map[string]any{"nodeId": promptID, "portId": "out"},
				"to":   map[string]any{"nodeId": genID, "portId": "prompt"},
				"kind": "text"}},
		} {
			b, _ := json.Marshal(spec)
			ops = append(ops, b)
		}
		doc, err := s.canvas.Get(ctx, canvasID)
		if err != nil {
			return ToolCallResult{CallID: call.ID, Status: "error", Error: &ToolError{Code: platform.CodeNotFound, Message: err.Error()}}, nil
		}
		res, _, err := s.canvas.AppendOps(ctx, canvasID, doc.Version, ops, actor)
		if err != nil {
			de := platform.AsDomainError(err)
			return ToolCallResult{CallID: call.ID, Status: "error", Error: &ToolError{Code: de.Code, Message: de.Message}}, nil
		}
		inverse, _ := json.Marshal(res.Inverse)
		result, _ := json.Marshal(map[string]any{"promptNodeId": promptID, "generationNodeId": genID})
		return ToolCallResult{CallID: call.ID, Status: "ok",
			Applied: &AppliedInfo{Ops: res.Applied, Version: res.Version}, Inverse: inverse, Result: result}, nil

	case "canvas.run_generation":
		// 上一轮这里返回 ok 但什么都没做——用户看到「Agent 说已触发」而画布毫无变化，
		// 这是最难排查的一类问题（谎报成功）。现在改成：
		//   - 未注入 RunTrigger → 明确的 not_implemented（不是静默成功）；
		//   - 已注入 → 真正触发并回传 runId，前端可据此订阅进度。
		if s.runs == nil {
			return notWired(call, "canvas.run_generation"), nil
		}
		var args struct {
			NodeIDs []string `json:"nodeIds"`
		}
		if err := strictDecode(call.Arguments, &args); err != nil || len(args.NodeIDs) == 0 {
			return invalidArgs(call, errors.New("nodeIds is required")), nil
		}
		// 校验目标节点存在：否则运行会以「编译失败」告终，而错误发生在异步阶段，
		// 用户看到的是「点了没反应」。
		doc, err := s.canvas.Get(ctx, canvasID)
		if err != nil {
			return toolError(call, err), nil
		}
		for _, id := range args.NodeIDs {
			if _, ok := doc.Nodes[id]; !ok {
				return ToolCallResult{CallID: call.ID, Status: "error", Error: &ToolError{
					Code: platform.CodeNotFound, Message: "节点不存在: " + id}}, nil
			}
		}
		runID, err := s.runs.TriggerRun(ctx, canvasID, args.NodeIDs, actor)
		if err != nil {
			return toolError(call, err), nil
		}
		return toolOK(call, map[string]any{"runId": runID, "nodeIds": args.NodeIDs}), nil
	}
	return ToolCallResult{CallID: call.ID, Status: "error",
		Error: &ToolError{Code: "unknown_tool", Message: call.Name}}, nil
}

func strictDecode(raw json.RawMessage, out any) error {
	if len(raw) == 0 {
		return errors.New("arguments is required")
	}
	dec := json.NewDecoder(bytesReader(raw))
	dec.DisallowUnknownFields()
	return dec.Decode(out)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	// 按 rune 截断，避免切出半个中文
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…"
}

func parseTime(s string) time.Time {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05.999999999-07:00"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}
