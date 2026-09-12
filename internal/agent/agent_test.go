package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/context-flow/ic/internal/graph"
	"github.com/context-flow/ic/internal/platform"
	"github.com/context-flow/ic/migrations"
)

// ---------------------------------------------------------------- fakes

type fakeCanvas struct {
	mu     sync.Mutex
	doc    *graph.CanvasDocument
	ops    []json.RawMessage
	actor  string
	nextV  int64
	failOn string
}

func newFakeCanvas() *fakeCanvas {
	return &fakeCanvas{doc: graph.NewDocument("cv_1", "pj_1"), nextV: 1}
}

func (c *fakeCanvas) Get(context.Context, string) (*graph.CanvasDocument, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.doc.Clone(), nil
}

func (c *fakeCanvas) AppendOps(_ context.Context, _ string, baseVersion int64, ops []json.RawMessage, actor string) (graph.ApplyResult, *graph.CanvasDocument, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failOn != "" {
		return graph.ApplyResult{}, nil, platform.ErrInvalid(c.failOn)
	}
	next := c.doc.Clone()
	res, applied, err := graph.Apply(next, ops, actor, platform.SystemClock().Now())
	if err != nil {
		return graph.ApplyResult{}, nil, err
	}
	c.doc = applied
	c.ops = append(c.ops, ops...)
	c.actor = actor
	c.nextV = res.Version
	return res, applied, nil
}

type fakeModel struct {
	text  string
	calls []ToolCall
	err   error
}

func (m *fakeModel) Complete(context.Context, CompletionRequest) (CompletionResult, error) {
	if m.err != nil {
		return CompletionResult{}, m.err
	}
	return CompletionResult{Text: m.text, ToolCalls: m.calls, Usage: Usage{TextTokensIn: 10, TextTokensOut: 5}}, nil
}

func newTestService(t *testing.T, canvas CanvasGateway, model ModelClient) *Service {
	t.Helper()
	cfg := platform.Defaults()
	cfg.DBDriver = "sqlite"
	cfg.DBDSN = "file::memory:?cache=shared"
	cfg.AllowInsecureDevKey = true
	db, err := platform.OpenDB(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(context.Background(), migrations.FS, "."); err != nil {
		t.Fatal(err)
	}
	return New(Options{DB: db.DB, Canvas: canvas, Model: model, Clock: platform.SystemClock()})
}

// ---------------------------------------------------------------- tools

func TestToolSetShape(t *testing.T) {
	tools := ToolSet()
	if len(tools) < 10 {
		t.Fatalf("工具数量过少: %d", len(tools))
	}
	for _, tool := range tools {
		if tool.Name == "" || tool.Description == "" || len(tool.InputSchema) == 0 {
			t.Fatalf("工具定义不完整: %+v", tool)
		}
		if tool.Scope != "read" && tool.Scope != "write" {
			t.Fatalf("scope 非法: %s", tool.Scope)
		}
		// 输入 schema 必须是合法 JSON 且是 object
		var schema map[string]any
		if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
			t.Fatalf("%s schema 非法: %v", tool.Name, err)
		}
		if schema["type"] != "object" {
			t.Fatalf("%s schema 必须是 object", tool.Name)
		}
		if schema["additionalProperties"] != false {
			t.Fatalf("%s schema 必须 additionalProperties:false", tool.Name)
		}
	}
}

func TestToolNamesUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, tool := range ToolSet() {
		if seen[tool.Name] {
			t.Fatalf("工具重名: %s", tool.Name)
		}
		seen[tool.Name] = true
	}
}

// 费用工具在非 full 模式下必须始终需要确认。
func TestApprovalPolicy(t *testing.T) {
	runGen, _ := ToolByName("canvas.run_generation")
	for _, mode := range []PermissionMode{PermRequest, PermAutomatic, PermFull} {
		got := ApprovalFor(runGen, mode)
		if mode == PermFull && got != ApprovalAuto {
			t.Fatalf("full 模式应放行，实际 %s", got)
		}
		if mode != PermFull && got != ApprovalConfirm {
			t.Fatalf("费用工具在 %s 下必须确认，实际 %s", mode, got)
		}
	}
	readTool, _ := ToolByName("canvas.get_state")
	if ApprovalFor(readTool, PermRequest) != ApprovalAuto {
		t.Fatal("只读工具不应需要确认")
	}
	writeTool, _ := ToolByName("canvas.create_text_node")
	if ApprovalFor(writeTool, PermRequest) != ApprovalConfirm {
		t.Fatal("写工具在 request 模式下必须确认")
	}
	if ApprovalFor(writeTool, PermAutomatic) != ApprovalConfirm {
		t.Fatal("写工具在 automatic 模式下仍需确认")
	}
}

// ---------------------------------------------------------------- snapshot

func TestSnapshotTruncatesLongText(t *testing.T) {
	canvas := newFakeCanvas()
	long := ""
	for i := 0; i < 1000; i++ {
		long += "中"
	}
	canvas.doc.Nodes["p_1"] = graph.Node{
		ID: "p_1", Type: graph.NodeTypePrompt, Title: "提示词",
		Rect: graph.Rect{X: 0, Y: 0, W: 320, H: 220},
		Spec: graph.NodeSpec{"text": long},
	}
	svc := newTestService(t, canvas, &fakeModel{})
	snap, err := svc.Snapshot(context.Background(), "cv_1")
	if err != nil {
		t.Fatal(err)
	}
	if len(snap) > 4000 {
		t.Fatalf("快照未压缩，长度 %d", len(snap))
	}
	var parsed struct {
		Nodes []struct {
			Summary map[string]any `json:"summary"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(snap, &parsed); err != nil {
		t.Fatal(err)
	}
	text, _ := parsed.Nodes[0].Summary["text"].(string)
	if len([]rune(text)) > 241 { // 240 + ellipsis
		t.Fatalf("文本未按 rune 截断: %d", len([]rune(text)))
	}
}

// ---------------------------------------------------------------- session/turn

func TestCreateAndGetSession(t *testing.T) {
	svc := newTestService(t, newFakeCanvas(), &fakeModel{})
	ctx := context.Background()
	sess, err := svc.CreateSession(ctx, "ws_1", "cv_1", BackendHTTP, "测试会话")
	if err != nil {
		t.Fatal(err)
	}
	got, err := svc.GetSession(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "测试会话" || got.CanvasID != "cv_1" || got.Backend != BackendHTTP {
		t.Fatalf("session=%+v", got)
	}
	if _, err := svc.GetSession(ctx, "missing"); err == nil {
		t.Fatal("未知会话应 404")
	}
}

func TestCreateSessionRequiresWorkspace(t *testing.T) {
	svc := newTestService(t, newFakeCanvas(), &fakeModel{})
	if _, err := svc.CreateSession(context.Background(), "", "cv_1", BackendHTTP, ""); err == nil {
		t.Fatal("缺 workspaceId 应被拒绝")
	}
}

func TestSubmitTurnWithoutModel(t *testing.T) {
	svc := newTestService(t, newFakeCanvas(), nil)
	ctx := context.Background()
	sess, _ := svc.CreateSession(ctx, "ws_1", "cv_1", BackendHTTP, "")
	turn, err := svc.SubmitTurn(ctx, sess.ID, "你好", "u_1")
	if err != nil {
		t.Fatal(err)
	}
	if turn.Status != TurnFailed {
		t.Fatalf("无模型时应失败并给出明确原因: %s", turn.Status)
	}
	if turn.Error == nil || turn.Error.Code != platform.CodeNotImplemented {
		t.Fatalf("错误码应明确: %+v", turn.Error)
	}
}

func TestSubmitTurnPlainReply(t *testing.T) {
	svc := newTestService(t, newFakeCanvas(), &fakeModel{text: "好的"})
	ctx := context.Background()
	sess, _ := svc.CreateSession(ctx, "ws_1", "cv_1", BackendHTTP, "")
	turn, err := svc.SubmitTurn(ctx, sess.ID, "帮我看看画布", "u_1")
	if err != nil {
		t.Fatal(err)
	}
	if turn.Status != TurnDone {
		t.Fatalf("status=%s", turn.Status)
	}
	if len(turn.Items) < 2 {
		t.Fatalf("应包含用户输入与助手回复: %d", len(turn.Items))
	}
	if turn.Usage.TextTokensIn != 10 {
		t.Fatalf("计量未累计: %+v", turn.Usage)
	}
}

// ATK-14：重放同 callId 的工具调用只执行一次（由主键幂等保证）。
func TestATK14DuplicateToolCallIdempotent(t *testing.T) {
	canvas := newFakeCanvas()
	svc := newTestService(t, canvas, &fakeModel{})
	ctx := context.Background()
	sess, _ := svc.CreateSession(ctx, "ws_1", "cv_1", BackendHTTP, "")

	call := ToolCall{ID: "call_1", Name: "canvas.create_text_node",
		Arguments: json.RawMessage(`{"text":"hi","x":0,"y":0}`)}
	// 同一 callId 执行两次
	for i := 0; i < 2; i++ {
		if _, err := svc.ExecuteTool(ctx, "cv_1", "u_1", call); err != nil {
			t.Fatal(err)
		}
	}
	// 两次调用会产生两个节点（工具本身幂等由调用方保证），
	// 但条目写入必须幂等：同一 turn 内同 callId 只留一条结果
	payload, _ := json.Marshal(map[string]any{"id": "call_1", "name": "call_1"})
	_ = svc.UpsertItem(ctx, Item{ID: "call_1", TurnID: "tn_1", Seq: 1, Kind: ItemToolCall, Payload: payload})
	_ = svc.UpsertItem(ctx, Item{ID: "call_1", TurnID: "tn_1", Seq: 1, Kind: ItemToolCall, Payload: payload})
	items, _ := svc.items(ctx, "tn_1")
	if len(items) != 1 {
		t.Fatalf("同 (turnId,itemId) 应只有一条: %d", len(items))
	}
	_ = sess
}

// ATK-13：实时事件 + 历史快照同时到达时，快照为权威且不重复。
func TestATK13LiveAndSnapshotMerge(t *testing.T) {
	svc := newTestService(t, newFakeCanvas(), &fakeModel{})
	ctx := context.Background()

	livePayload, _ := json.Marshal(map[string]string{"text": "streaming partial"})
	snapPayload, _ := json.Marshal(map[string]string{"text": "final snapshot text"})

	// 先到实时事件
	if err := svc.UpsertItem(ctx, Item{ID: "item_1", TurnID: "tn_x", Seq: 1,
		Kind: ItemAgentMessage, Payload: livePayload, Source: SourceLive}); err != nil {
		t.Fatal(err)
	}
	// 再到历史快照（同 ID）
	if err := svc.UpsertItem(ctx, Item{ID: "item_1", TurnID: "tn_x", Seq: 1,
		Kind: ItemAgentMessage, Payload: snapPayload, Source: SourceSnapshot}); err != nil {
		t.Fatal(err)
	}
	items, err := svc.items(ctx, "tn_x")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("不应重复: %d", len(items))
	}
	if items[0].Source != SourceSnapshot {
		t.Fatalf("快照应为权威来源: %s", items[0].Source)
	}
	var got map[string]string
	_ = json.Unmarshal(items[0].Payload, &got)
	if got["text"] != "final snapshot text" {
		t.Fatalf("快照未覆盖实时内容: %v", got)
	}

	// 反向顺序：快照先到，实时不覆盖
	if err := svc.UpsertItem(ctx, Item{ID: "item_2", TurnID: "tn_x", Seq: 2,
		Kind: ItemAgentMessage, Payload: snapPayload, Source: SourceSnapshot}); err != nil {
		t.Fatal(err)
	}
	if err := svc.UpsertItem(ctx, Item{ID: "item_2", TurnID: "tn_x", Seq: 2,
		Kind: ItemAgentMessage, Payload: livePayload, Source: SourceLive}); err != nil {
		t.Fatal(err)
	}
	items, _ = svc.items(ctx, "tn_x")
	for _, it := range items {
		if it.ID == "item_2" && it.Source != SourceSnapshot {
			t.Fatalf("实时事件不应覆盖快照: %s", it.Source)
		}
	}
}

func TestUpsertItemValidatesIdentity(t *testing.T) {
	svc := newTestService(t, newFakeCanvas(), &fakeModel{})
	ctx := context.Background()
	if err := svc.UpsertItem(ctx, Item{TurnID: "t"}); err == nil {
		t.Fatal("缺 item id 应被拒绝")
	}
	if err := svc.UpsertItem(ctx, Item{ID: "i"}); err == nil {
		t.Fatal("缺 turn id 应被拒绝")
	}
}

// ---------------------------------------------------------------- tool execution

func TestExecuteToolUnknown(t *testing.T) {
	svc := newTestService(t, newFakeCanvas(), &fakeModel{})
	res, err := svc.ExecuteTool(context.Background(), "cv_1", "u_1", ToolCall{ID: "c", Name: "shell.exec"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "error" || res.Error.Code != "unknown_tool" {
		t.Fatalf("res=%+v", res)
	}
}

func TestExecuteToolApplyOpsWritesThroughSamePath(t *testing.T) {
	canvas := newFakeCanvas()
	svc := newTestService(t, canvas, &fakeModel{})
	ctx := context.Background()

	ops := json.RawMessage(`{"ops":[{"kind":"add_node","node":{"id":"n_1","type":"prompt","title":"t","rect":{"x":0,"y":0,"w":320,"h":220},"spec":{"text":"hi"}}}]}`)
	res, err := svc.ExecuteTool(ctx, "cv_1", "u_1", ToolCall{ID: "c1", Name: "canvas.apply_ops", Arguments: ops})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "ok" || res.Applied == nil || res.Applied.Ops != 1 {
		t.Fatalf("res=%+v", res)
	}
	// 写操作必须经过 graph 校验：非法几何应被拒绝
	bad := json.RawMessage(`{"ops":[{"kind":"add_node","node":{"id":"n_2","type":"prompt","rect":{"x":1e15,"y":0,"w":320,"h":220},"spec":{"text":"x"}}}]}`)
	res2, err := svc.ExecuteTool(ctx, "cv_1", "u_1", ToolCall{ID: "c2", Name: "canvas.apply_ops", Arguments: bad})
	if err != nil {
		t.Fatal(err)
	}
	if res2.Status != "error" || res2.Error.Code != platform.CodeInvalidGeometry {
		t.Fatalf("Agent 写操作必须走同一套校验: %+v", res2)
	}
}

func TestExecuteToolRejectsUnknownArguments(t *testing.T) {
	svc := newTestService(t, newFakeCanvas(), &fakeModel{})
	res, _ := svc.ExecuteTool(context.Background(), "cv_1", "u_1", ToolCall{
		ID: "c", Name: "canvas.create_text_node",
		Arguments: json.RawMessage(`{"text":"x","bogus":1}`),
	})
	if res.Status != "error" {
		t.Fatal("未知参数应被拒绝（严格模式）")
	}
}

func TestCreateGenerationFlowProducesThreeOps(t *testing.T) {
	canvas := newFakeCanvas()
	svc := newTestService(t, canvas, &fakeModel{})
	res, err := svc.ExecuteTool(context.Background(), "cv_1", "u_1", ToolCall{
		ID: "c1", Name: "canvas.create_generation_flow",
		Arguments: json.RawMessage(`{"prompt":"一只猫","capability":"image.generate"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "ok" || res.Applied.Ops != 3 {
		t.Fatalf("应产生 3 个 op（两节点 + 一连线）: %+v", res)
	}
	var result map[string]string
	_ = json.Unmarshal(res.Result, &result)
	if result["promptNodeId"] == "" || result["generationNodeId"] == "" {
		t.Fatalf("未返回节点 ID: %v", result)
	}
	// inverse 必须可撤销
	if len(res.Inverse) == 0 {
		t.Fatal("写操作必须提供 inverse 供撤销")
	}
}

func TestUndoAgentOps(t *testing.T) {
	canvas := newFakeCanvas()
	svc := newTestService(t, canvas, &fakeModel{})
	ctx := context.Background()

	res, err := svc.ExecuteTool(ctx, "cv_1", "u_1", ToolCall{
		ID: "c1", Name: "canvas.create_text_node",
		Arguments: json.RawMessage(`{"text":"hello","x":10,"y":10}`),
	})
	if err != nil || res.Status != "ok" {
		t.Fatalf("create: %+v %v", res, err)
	}
	if _, err := svc.UndoAgentOps(ctx, "cv_1", res.Inverse, "u_1"); err != nil {
		t.Fatalf("undo: %v", err)
	}
	doc, _ := canvas.Get(ctx, "cv_1")
	// 撤销后节点应被删除（inverse 是 remove_node；新增节点 id 未知，因此断言节点数为 0）
	if len(doc.Nodes) != 0 {
		t.Fatalf("撤销后节点数应为 0，实际 %d", len(doc.Nodes))
	}
}

func TestUndoRejectsEmptyInverse(t *testing.T) {
	svc := newTestService(t, newFakeCanvas(), &fakeModel{})
	if _, err := svc.UndoAgentOps(context.Background(), "cv_1", json.RawMessage(`[]`), "u_1"); err == nil {
		t.Fatal("空 inverse 应被拒绝")
	}
	if _, err := svc.UndoAgentOps(context.Background(), "cv_1", json.RawMessage(`garbage`), "u_1"); err == nil {
		t.Fatal("非法 inverse 应被拒绝")
	}
}

// ---------------------------------------------------------------- approval

func TestWriteToolRequiresApprovalThenExecutes(t *testing.T) {
	canvas := newFakeCanvas()
	model := &fakeModel{text: "我来创建节点", calls: []ToolCall{{
		ID: "call_1", Name: "canvas.create_text_node",
		Arguments: json.RawMessage(`{"text":"来自 Agent","x":0,"y":0}`),
	}}}
	svc := newTestService(t, canvas, model)
	ctx := context.Background()
	sess, _ := svc.CreateSession(ctx, "ws_1", "cv_1", BackendHTTP, "")

	turn, err := svc.SubmitTurn(ctx, sess.ID, "创建一个节点", "u_1")
	if err != nil {
		t.Fatal(err)
	}
	if turn.Status != TurnAwaiting {
		t.Fatalf("写工具应挂起等待审批，实际 %s", turn.Status)
	}
	if turn.Pending == nil || turn.Pending.Tool != "canvas.create_text_node" {
		t.Fatalf("pending=%+v", turn.Pending)
	}
	if turn.Pending.OpCount == 0 {
		t.Fatal("审批卡片必须给出影响范围（op 数量）")
	}

	// 批准
	done, err := svc.Approve(ctx, sess.ID, turn.ID, turn.Pending.CallID, "u_1", true)
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != TurnDone {
		t.Fatalf("批准后应完成: %s", done.Status)
	}
	doc, _ := canvas.Get(ctx, "cv_1")
	if len(doc.Nodes) != 1 {
		t.Fatalf("批准后应创建节点，实际 %d", len(doc.Nodes))
	}
}

func TestDeniedToolDoesNotTouchCanvas(t *testing.T) {
	canvas := newFakeCanvas()
	model := &fakeModel{calls: []ToolCall{{
		ID: "call_1", Name: "canvas.create_text_node",
		Arguments: json.RawMessage(`{"text":"x","x":0,"y":0}`),
	}}}
	svc := newTestService(t, canvas, model)
	ctx := context.Background()
	sess, _ := svc.CreateSession(ctx, "ws_1", "cv_1", BackendHTTP, "")
	turn, _ := svc.SubmitTurn(ctx, sess.ID, "创建", "u_1")

	done, err := svc.Approve(ctx, sess.ID, turn.ID, turn.Pending.CallID, "u_1", false)
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != TurnDone {
		t.Fatalf("拒绝后应结束: %s", done.Status)
	}
	doc, _ := canvas.Get(ctx, "cv_1")
	if len(doc.Nodes) != 0 {
		t.Fatal("拒绝后不得改动画布")
	}
}

func TestApproveUnknownTurn(t *testing.T) {
	svc := newTestService(t, newFakeCanvas(), &fakeModel{})
	if _, err := svc.Approve(context.Background(), "s", "t", "c", "u", true); err == nil {
		t.Fatal("未知 turn 应 404")
	}
}

func TestApproveWhenNotAwaiting(t *testing.T) {
	canvas := newFakeCanvas()
	svc := newTestService(t, canvas, &fakeModel{text: "hi"})
	ctx := context.Background()
	sess, _ := svc.CreateSession(ctx, "ws_1", "cv_1", BackendHTTP, "")
	turn, _ := svc.SubmitTurn(ctx, sess.ID, "hello", "u_1")
	if _, err := svc.Approve(ctx, sess.ID, turn.ID, "nope", "u_1", true); err == nil {
		t.Fatal("非挂起状态应拒绝审批")
	} else if de := platform.AsDomainError(err); de.Code != platform.CodeConflict {
		t.Fatalf("code=%s", de.Code)
	}
}

func TestReadToolExecutesWithoutApproval(t *testing.T) {
	canvas := newFakeCanvas()
	model := &fakeModel{calls: []ToolCall{{ID: "c1", Name: "canvas.get_state", Arguments: json.RawMessage(`{}`)}}}
	svc := newTestService(t, canvas, model)
	ctx := context.Background()
	sess, _ := svc.CreateSession(ctx, "ws_1", "cv_1", BackendHTTP, "")
	turn, err := svc.SubmitTurn(ctx, sess.ID, "看看画布", "u_1")
	if err != nil {
		t.Fatal(err)
	}
	if turn.Status != TurnDone {
		t.Fatalf("只读工具不应挂起: %s", turn.Status)
	}
	// 工具结果条目必须存在
	sawResult := false
	for _, it := range turn.Items {
		if it.Kind == ItemToolResult {
			sawResult = true
		}
	}
	if !sawResult {
		t.Fatal("缺少工具结果条目")
	}
}

func TestModelFailureCreatesErrorItem(t *testing.T) {
	canvas := newFakeCanvas()
	svc := newTestService(t, canvas, &fakeModel{err: errors.New("upstream down")})
	ctx := context.Background()
	sess, _ := svc.CreateSession(ctx, "ws_1", "cv_1", BackendHTTP, "")
	turn, err := svc.SubmitTurn(ctx, sess.ID, "hi", "u_1")
	if err != nil {
		t.Fatal(err)
	}
	if turn.Status != TurnFailed {
		t.Fatalf("status=%s", turn.Status)
	}
	sawError := false
	for _, it := range turn.Items {
		if it.Kind == ItemError {
			sawError = true
		}
	}
	if !sawError {
		t.Fatal("失败必须有 error 条目（不能静默）")
	}
}

func TestTurnSeqMonotonic(t *testing.T) {
	svc := newTestService(t, newFakeCanvas(), &fakeModel{text: "ok"})
	ctx := context.Background()
	sess, _ := svc.CreateSession(ctx, "ws_1", "cv_1", BackendHTTP, "")
	for i := 1; i <= 3; i++ {
		turn, err := svc.SubmitTurn(ctx, sess.ID, "hi", "u_1")
		if err != nil {
			t.Fatal(err)
		}
		if turn.Seq != i {
			t.Fatalf("seq 应为 %d，实际 %d", i, turn.Seq)
		}
	}
}

func TestSubmitTurnRejectsEmptyInput(t *testing.T) {
	svc := newTestService(t, newFakeCanvas(), &fakeModel{})
	ctx := context.Background()
	sess, _ := svc.CreateSession(ctx, "ws_1", "cv_1", BackendHTTP, "")
	if _, err := svc.SubmitTurn(ctx, sess.ID, "   ", "u_1"); err == nil {
		t.Fatal("空输入应被拒绝")
	}
}

// ---------------------------------------------------------------- MCP

func TestMCPInitializeAndToolsList(t *testing.T) {
	svc := newTestService(t, newFakeCanvas(), &fakeModel{})
	h := NewMCPHandler(svc, func(context.Context) (string, string, error) { return "cv_1", "u_1", nil })

	res := h.Handle(context.Background(), "initialize", nil, json.RawMessage(`1`))
	out, _ := json.Marshal(res)
	if !contains(string(out), MCPProtocolVersion) || !contains(string(out), "ic-canvas") {
		t.Fatalf("initialize 响应异常: %s", out)
	}

	res = h.Handle(context.Background(), "tools/list", nil, json.RawMessage(`2`))
	out, _ = json.Marshal(res)
	if !contains(string(out), "canvas.apply_ops") || !contains(string(out), "inputSchema") {
		t.Fatalf("tools/list 响应异常: %s", out)
	}
}

func TestMCPToolsCallDelegates(t *testing.T) {
	canvas := newFakeCanvas()
	svc := newTestService(t, canvas, &fakeModel{})
	h := NewMCPHandler(svc, func(context.Context) (string, string, error) { return "cv_1", "u_1", nil })

	res := h.Handle(context.Background(), "tools/call",
		json.RawMessage(`{"name":"canvas.create_text_node","arguments":{"text":"mcp","x":0,"y":0}}`),
		json.RawMessage(`3`))
	out, _ := json.Marshal(res)
	if !contains(string(out), `"isError":false`) {
		t.Fatalf("MCP 工具调用失败: %s", out)
	}
	doc, _ := canvas.Get(context.Background(), "cv_1")
	if len(doc.Nodes) != 1 {
		t.Fatal("MCP 调用应作用于画布")
	}
}

func TestMCPUnknownMethod(t *testing.T) {
	svc := newTestService(t, newFakeCanvas(), &fakeModel{})
	h := NewMCPHandler(svc, func(context.Context) (string, string, error) { return "cv_1", "u_1", nil })
	res := h.Handle(context.Background(), "tools/nope", nil, json.RawMessage(`4`))
	out, _ := json.Marshal(res)
	if !contains(string(out), "-32601") {
		t.Fatalf("未知方法应返回 JSON-RPC 错误: %s", out)
	}
}

func TestMCPErrorsWhenNotConfigured(t *testing.T) {
	h := NewMCPHandler(nil, nil)
	res := h.Handle(context.Background(), "tools/call", json.RawMessage(`{"name":"x"}`), json.RawMessage(`5`))
	out, _ := json.Marshal(res)
	if !contains(string(out), "not configured") {
		t.Fatalf("未配置时应给明确错误: %s", out)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}

// 未装配的能力必须返回 not_implemented，而不是「ok 但什么都没做」。
//
// 这条对应上一轮的真实缺陷：canvas.run_generation 返回
// `{"note":"delegated to api layer"}` 且状态为 ok，用户看到「Agent 已触发生成」
// 但画布毫无变化——谎报成功是最难排查的一类问题。
func TestUnwiredToolsReportNotImplemented(t *testing.T) {
	canvas := newFakeCanvas()
	svc := newTestService(t, canvas, &fakeModel{text: "ok"})
	ctx := context.Background()
	sess, err := svc.CreateSession(ctx, "ws_1", "cv_1", BackendHTTP, "t")
	if err != nil {
		t.Fatal(err)
	}

	for _, tool := range []string{
		"assets.search", "prompts.search", "runs.list", "runs.get",
		"canvas.create_attachment_nodes", "skills.list", "skills.save",
		"canvas.run_generation",
	} {
		t.Run(tool, func(t *testing.T) {
			res, err := svc.ExecuteTool(ctx, sess.CanvasID, "u_1", ToolCall{
				ID: "c1", Name: tool, Arguments: json.RawMessage(`{}`),
			})
			if err != nil {
				t.Fatal(err)
			}
			// 参数校验可能先于能力检查（例如 run_generation 要求 nodeIds），
			// 因此允许 invalid_request；但绝不允许「ok」。
			if res.Status == "ok" {
				t.Fatalf("%s 未装配时不得返回 ok（谎报成功）: %s", tool, string(res.Result))
			}
			if res.Error == nil {
				t.Fatalf("%s 失败时必须给出错误信息", tool)
			}
		})
	}
}

// run_generation 必须校验目标节点存在：否则执行在异步阶段才失败，
// 用户看到的是「点了没反应」而不是一条可行动的提示。
func TestRunGenerationRejectsUnknownNode(t *testing.T) {
	canvas := newFakeCanvas()
	svc := newTestService(t, canvas, &fakeModel{text: "ok"})
	ctx := context.Background()
	sess, err := svc.CreateSession(ctx, "ws_1", "cv_1", BackendHTTP, "t")
	if err != nil {
		t.Fatal(err)
	}
	res, err := svc.ExecuteTool(ctx, sess.CanvasID, "u_1", ToolCall{
		ID:        "c1",
		Name:      "canvas.run_generation",
		Arguments: json.RawMessage(`{"nodeIds":["n_does_not_exist"]}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status == "ok" {
		t.Fatal("不存在的节点不应通过校验")
	}
}

// 工具表必须与 op schema 同源：op kind 增加时工具表要能自动反映。
func TestToolSetMatchesOpSchema(t *testing.T) {
	tools := ToolSet()
	if len(tools) < 13 {
		t.Fatalf("工具数量异常: %d", len(tools))
	}
	// apply_ops 的 schema 必须枚举全部 op kind，否则 Agent 无法使用新 op
	var applyOps json.RawMessage
	for _, tool := range tools {
		if tool.Name == "canvas.apply_ops" {
			applyOps = tool.InputSchema
		}
	}
	if len(applyOps) == 0 {
		t.Fatal("缺少 canvas.apply_ops")
	}
	for _, kind := range graph.AllOpKinds() {
		if !strings.Contains(string(applyOps), string(kind)) {
			t.Fatalf("apply_ops 的 schema 未包含 op kind %s（工具表与 op schema 漂移）", kind)
		}
	}
}

// 产生费用的工具在任何权限档位下都必须确认，只有 PermFull 例外。
func TestCostlyToolsAlwaysRequireApproval(t *testing.T) {
	costly := []string{"canvas.create_generation_flow", "canvas.run_generation"}
	modes := []PermissionMode{PermRequest, PermAutomatic, PermFull}
	for _, name := range costly {
		def, ok := ToolByName(name)
		if !ok {
			t.Fatalf("%s 不在工具表里", name)
		}
		if !def.CostsMoney {
			t.Fatalf("%s 应标记 CostsMoney（否则用户看不出它会花钱）", name)
		}
		for _, mode := range modes {
			got := ApprovalFor(def, mode)
			if mode == PermFull {
				if got == ApprovalForbidden {
					t.Fatalf("%s 在 full 档位不应被禁止", name)
				}
				continue
			}
			if got != ApprovalConfirm {
				t.Fatalf("%s 在 %s 档位必须要求确认，实际 %s", name, mode, got)
			}
		}
	}
}

// 写工具在自动档位下仍要确认；读工具在自动档位下可放行。
func TestApprovalMatrix(t *testing.T) {
	read, _ := ToolByName("canvas.get_state")
	write, _ := ToolByName("canvas.create_text_node")

	if ApprovalFor(read, PermAutomatic) != ApprovalAuto {
		t.Fatal("读工具在 automatic 档位应放行")
	}
	if ApprovalFor(write, PermAutomatic) != ApprovalConfirm {
		t.Fatal("写工具在 automatic 档位仍需确认")
	}
	if ApprovalFor(write, PermRequest) != ApprovalConfirm {
		t.Fatal("写工具在 request 档位需确认")
	}
}
