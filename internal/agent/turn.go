package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/context-flow/ic/internal/platform"
)

// nextItemSeq 直接用 SQL 取会话内的最大序号，避免在单连接池下自锁
// （sqlite 的 max_open_conns=1 时，函数内另起查询会与调用方的语句互等）。
func (s *Service) nextItemSeq(ctx context.Context, turnID string) int {
	var max sql.NullInt64
	if err := s.db.QueryRowContext(ctx,
		`SELECT MAX(seq) FROM agent_items WHERE turn_id = ?`, turnID).Scan(&max); err != nil {
		return 1
	}
	if !max.Valid {
		return 1
	}
	return int(max.Int64) + 1
}

// SubmitTurn 提交一次用户输入，产出模型回复并（如需）挂起等待审批。
//
// 流程：
//  1. 建 Turn（pending → running）；
//  2. 生成画布快照作为上下文；
//  3. 调模型（形态 A）；模型无工具调用则直接完成；
//  4. 有工具调用时：只读工具立即执行；写工具按权限策略决定是否挂起审批。
func (s *Service) SubmitTurn(ctx context.Context, sessionID, input, actor string) (*Turn, error) {
	sess, err := s.GetSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(input) == "" {
		return nil, platform.ErrInvalid("input is required")
	}

	seq, err := s.nextTurnSeq(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	turn := &Turn{
		ID:        s.ids.NewID("tn"),
		SessionID: sessionID,
		Seq:       seq,
		Status:    TurnRunning,
		Input:     input,
		CreatedAt: s.clock.Now().UTC(),
		UpdatedAt: s.clock.Now().UTC(),
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO agent_turns (id, session_id, seq, status, usage, input, created_at, updated_at)
		 VALUES (?, ?, ?, ?, '{}', ?, ?, ?)`,
		turn.ID, sessionID, seq, string(turn.Status), input, turn.CreatedAt, turn.UpdatedAt); err != nil {
		return nil, platform.AsError(err)
	}

	// 用户输入作为第一个条目
	userPayload, _ := json.Marshal(map[string]string{"text": input})
	if err := s.UpsertItem(ctx, Item{ID: s.ids.NewID("it"), TurnID: turn.ID, Seq: 1,
		Kind: ItemAgentMessage, Payload: userPayload, Source: SourceLive, CreatedAt: turn.CreatedAt}); err != nil {
		return nil, err
	}

	if s.model == nil {
		return s.failTurn(ctx, turn, platform.NewError(501, platform.CodeNotImplemented,
			"agent backend is not configured; install a local agent or configure a server model"))
	}

	snap, err := s.Snapshot(ctx, sess.CanvasID)
	if err != nil {
		return s.failTurn(ctx, turn, err)
	}

	res, err := s.model.Complete(ctx, CompletionRequest{
		System:   systemPrompt(),
		Messages: []Message{{Role: "user", Content: input}},
		Tools:    ToolSet(),
		Snapshot: snap,
	})
	if err != nil {
		return s.failTurn(ctx, turn, err)
	}

	turn.Usage = turn.Usage.Add(res.Usage)
	itemSeq := 2
	if res.Text != "" {
		payload, _ := json.Marshal(map[string]string{"text": res.Text})
		_ = s.UpsertItem(ctx, Item{ID: s.ids.NewID("it"), TurnID: turn.ID, Seq: itemSeq,
			Kind: ItemAgentMessage, Payload: payload, Source: SourceLive, CreatedAt: s.clock.Now().UTC()})
		itemSeq++
	}
	for _, call := range res.ToolCalls {
		payload, _ := json.Marshal(map[string]any{"id": call.ID, "name": call.Name, "arguments": call.Arguments})
		_ = s.UpsertItem(ctx, Item{ID: call.ID, TurnID: turn.ID, Seq: itemSeq,
			Kind: ItemToolCall, Payload: payload, Source: SourceLive, CreatedAt: s.clock.Now().UTC()})
		itemSeq++
	}
	turn.Usage.ToolCalls += int64(len(res.ToolCalls))

	// 工具执行与审批
	var pending *PendingTool
	for _, call := range res.ToolCalls {
		def, ok := ToolByName(call.Name)
		if !ok {
			continue
		}
		mode := ApprovalFor(def, sess.Permission)
		if mode == ApprovalForbidden {
			_ = s.recordToolResult(ctx, turn, call.ID, ToolCallResult{CallID: call.ID, Status: "denied",
				Error: &ToolError{Code: platform.CodeForbidden, Message: "tool is forbidden in this session"}})
			continue
		}
		if mode == ApprovalConfirm {
			// 只保留第一个待审批工具，其余在审批后继续（避免一次弹多个卡片）
			pending = &PendingTool{
				CallID: call.ID, Tool: call.Name, Arguments: call.Arguments,
				OpCount: estimateOps(call), NodeIDs: estimateNodes(call),
				EstCostMicros: estimateCost(def),
			}
			break
		}
		result, execErr := s.ExecuteTool(ctx, sess.CanvasID, actor, call)
		if execErr != nil {
			result = ToolCallResult{CallID: call.ID, Status: "error",
				Error: &ToolError{Code: platform.CodeInternal, Message: execErr.Error()}}
		}
		_ = s.recordToolResult(ctx, turn, call.ID, result)
	}

	if pending != nil {
		turn.Status = TurnAwaiting
		turn.Pending = pending
	} else {
		turn.Status = TurnDone
	}
	turn.UpdatedAt = s.clock.Now().UTC()
	if err := s.saveTurn(ctx, turn); err != nil {
		return nil, err
	}
	return s.readTurnAfterWrite(ctx, turn)
}

// Approve 批准或拒绝一个挂起的工具调用。
func (s *Service) Approve(ctx context.Context, sessionID, turnID, callID, actor string, approve bool) (*Turn, error) {
	turn, err := s.loadTurn(ctx, turnID)
	if err != nil {
		return nil, err
	}
	if turn.SessionID != sessionID {
		return nil, platform.ErrNotFound("turn")
	}
	if turn.Status != TurnAwaiting || turn.Pending == nil || turn.Pending.CallID != callID {
		return nil, platform.NewError(409, platform.CodeConflict, "turn is not awaiting this tool call")
	}
	sess, err := s.GetSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	call := ToolCall{ID: turn.Pending.CallID, Name: turn.Pending.Tool, Arguments: turn.Pending.Arguments}
	if !approve {
		_ = s.recordToolResult(ctx, turn, call.ID, ToolCallResult{CallID: call.ID, Status: "denied",
			Error: &ToolError{Code: platform.CodeForbidden, Message: "user denied the tool call"}})
		turn.Status = TurnDone
		turn.Pending = nil
		turn.UpdatedAt = s.clock.Now().UTC()
		if err := s.saveTurn(ctx, turn); err != nil {
			return nil, err
		}
		return s.loadTurn(ctx, turn.ID)
	}

	result, execErr := s.ExecuteTool(ctx, sess.CanvasID, actor, call)
	if execErr != nil {
		result = ToolCallResult{CallID: call.ID, Status: "error",
			Error: &ToolError{Code: platform.CodeInternal, Message: execErr.Error()}}
	}
	_ = s.recordToolResult(ctx, turn, call.ID, result)
	turn.Status = TurnDone
	turn.Pending = nil
	turn.UpdatedAt = s.clock.Now().UTC()
	if err := s.saveTurn(ctx, turn); err != nil {
		return nil, err
	}
	return s.loadTurn(ctx, turn.ID)
}

// UndoAgentOps 用工具结果里的 inverse 撤销一次写操作。
func (s *Service) UndoAgentOps(ctx context.Context, canvasID string, inverse json.RawMessage, actor string) (graphApply, error) {
	var ops []json.RawMessage
	if err := json.Unmarshal(inverse, &ops); err != nil {
		return graphApply{}, platform.ErrInvalid("inverse is not an op list")
	}
	if len(ops) == 0 {
		return graphApply{}, platform.ErrInvalid("inverse is empty")
	}
	doc, err := s.canvas.Get(ctx, canvasID)
	if err != nil {
		return graphApply{}, err
	}
	res, _, err := s.canvas.AppendOps(ctx, canvasID, doc.Version, ops, actor)
	if err != nil {
		return graphApply{}, err
	}
	return graphApply{Ops: res.Applied, Version: res.Version}, nil
}

// graphApply 是撤销结果（避免 agent 包对外暴露 graph.ApplyResult 细节）。
type graphApply struct {
	Ops     int   `json:"ops"`
	Version int64 `json:"version"`
}

func (s *Service) recordToolResult(ctx context.Context, turn *Turn, callID string, result ToolCallResult) error {
	payload, _ := json.Marshal(result)
	return s.UpsertItem(ctx, Item{
		ID: s.ids.NewID("it"), TurnID: turn.ID, Seq: s.nextItemSeq(ctx, turn.ID),
		Kind: ItemToolResult, Payload: payload, Source: SourceLive, CreatedAt: s.clock.Now().UTC(),
	})
}

func (s *Service) nextTurnSeq(ctx context.Context, sessionID string) (int, error) {
	var max int
	err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(seq), 0) FROM agent_turns WHERE session_id = ?`, sessionID).Scan(&max)
	if err != nil {
		return 0, platform.AsError(err)
	}
	return max + 1, nil
}

func (s *Service) saveTurn(ctx context.Context, turn *Turn) error {
	usage, _ := json.Marshal(turn.Usage)
	pendingJSON := ""
	if turn.Pending != nil {
		b, err := json.Marshal(turn.Pending)
		if err != nil {
			return platform.AsError(err)
		}
		pendingJSON = string(b)
	}
	var pendingVal any
	if pendingJSON != "" {
		pendingVal = pendingJSON
	}
	errorJSON := ""
	if turn.Error != nil {
		b, _ := json.Marshal(turn.Error)
		errorJSON = string(b)
	}
	var errorVal any
	if errorJSON != "" {
		errorVal = errorJSON
	}
	now := s.clock.Now().UTC()
	_, err := s.db.ExecContext(ctx,
		`UPDATE agent_turns SET status = ?, usage = ?, pending = ?, error = ?, updated_at = ? WHERE id = ?`,
		string(turn.Status), string(usage), pendingVal, errorVal, now, turn.ID)
	return platform.AsError(err)
}

func (s *Service) loadTurn(ctx context.Context, turnID string) (*Turn, error) {
	var turn Turn
	var created string
	var usageRaw, input string
	var pendingRaw, errorRaw sql.NullString
	if err := s.db.QueryRowContext(ctx,
		`SELECT id, session_id, seq, status, usage, input, pending, error, created_at
		 FROM agent_turns WHERE id = ?`, turnID).
		Scan(&turn.ID, &turn.SessionID, &turn.Seq, &turn.Status, &usageRaw, &input, &pendingRaw, &errorRaw, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, platform.ErrNotFound("turn")
		}
		return nil, platform.AsError(err)
	}
	_ = json.Unmarshal([]byte(usageRaw), &turn.Usage)
	turn.Input = input
	if pendingRaw.Valid && pendingRaw.String != "" {
		var p PendingTool
		if err := json.Unmarshal([]byte(pendingRaw.String), &p); err == nil {
			turn.Pending = &p
		}
	}
	if errorRaw.Valid && errorRaw.String != "" {
		var de platform.DomainError
		if err := json.Unmarshal([]byte(errorRaw.String), &de); err == nil {
			turn.Error = &de
		}
	}
	turn.CreatedAt = parseTime(created)
	items, err := s.items(ctx, turn.ID)
	if err != nil {
		return nil, err
	}
	turn.Items = items
	return &turn, nil
}

func (s *Service) failTurn(ctx context.Context, turn *Turn, err error) (*Turn, error) {
	de := platform.AsDomainError(err)
	turn.Status = TurnFailed
	turn.Error = de
	turn.UpdatedAt = s.clock.Now().UTC()
	payload, _ := json.Marshal(map[string]string{"code": de.Code, "message": de.Message})
	_ = s.UpsertItem(ctx, Item{ID: s.ids.NewID("it"), TurnID: turn.ID, Seq: s.nextItemSeq(ctx, turn.ID),
		Kind: ItemError, Payload: payload, Source: SourceLive, CreatedAt: s.clock.Now().UTC()})
	if serr := s.saveTurn(ctx, turn); serr != nil {
		return nil, serr
	}
	return s.readTurnAfterWrite(ctx, turn)
}

// readTurnAfterWrite 回读刚写入的 turn，并补齐未落库的运行时字段。
// 拆成独立函数是为了让「写」与「读」的职责分开，避免在同一函数里
// 同时构造返回值与回读造成的可读性问题。
func (s *Service) readTurnAfterWrite(ctx context.Context, written *Turn) (*Turn, error) {
	stored, err := s.loadTurn(ctx, written.ID)
	if err != nil {
		return nil, err
	}
	// Error / Pending 属于运行时字段，不落库；这里从入参带回。
	if written.Error != nil {
		stored.Error = written.Error
	}
	if written.Pending != nil {
		stored.Pending = written.Pending
	}
	stored.Input = written.Input
	return stored, nil
}

// estimateOps 估算 op 数量（审批卡片必须给出影响范围）。
func estimateOps(call ToolCall) int {
	switch call.Name {
	case "canvas.create_generation_flow":
		return 3 // 两个节点 + 一条连线
	case "canvas.create_text_node":
		return 1
	case "canvas.apply_ops":
		var args struct {
			Ops []json.RawMessage `json:"ops"`
		}
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			return 0
		}
		return len(args.Ops)
	}
	return 0
}

func estimateNodes(call ToolCall) []string {
	var args struct {
		NodeIDs []string `json:"nodeIds"`
	}
	_ = json.Unmarshal(call.Arguments, &args)
	return args.NodeIDs
}

// estimateCost 给出费用预估（无价格表时返回 0，UI 会显示「未知」而不是 0）。
func estimateCost(def ToolDef) int64 {
	if !def.CostsMoney {
		return 0
	}
	return 0
}

func systemPrompt() string {
	return strings.TrimSpace(`
你是无限画布（IC）的助手。你可以读取画布状态并通过工具修改画布。

规则：
1. 写操作（创建节点、连线、触发生成）会经过用户审批；请把操作理由说清楚。
2. 生成会消耗真实费用，务必在调用前说明预估影响。
3. 画布结构：节点有输入/输出端口，端口有资源类型（text/image/video/audio）。
   连线必须类型匹配；分组的子节点共享输入环境。
4. 不要臆造节点 ID；如需引用已有节点，先读取画布状态。
`)
}
