package agent

import (
	"context"

	"github.com/context-flow/ic/internal/api"
)

// APIService 把 agent.Service 适配为 api.AgentService。
// 单独一层是为了让 internal/agent 不依赖 api 的 DTO 细节之外的东西（依赖单向）。
type APIService struct {
	svc *Service
}

// NewAPIService 构造适配器。
func NewAPIService(svc *Service) *APIService { return &APIService{svc: svc} }

// CreateSession 见 api.AgentService。
func (a *APIService) CreateSession(ctx context.Context, wsID, canvasID, backend, title string) (*api.AgentSessionDTO, error) {
	sess, err := a.svc.CreateSession(ctx, wsID, canvasID, Backend(backend), title)
	if err != nil {
		return nil, err
	}
	return toSessionDTO(sess), nil
}

// SessionsForCanvas 见 api.CanvasSessionSnapshotter（2.11：随画布导出会话快照）。
//
// 实现放在 APIService 而不是再包一层适配器：`deps.Agent` 已经是这个对象，
// 而「导出画布时会话快照」正是它的能力 —— 再引一个类型只会让装配多一个
// 需要同步的注入点，而注入点漏接的表现是「导出里没有会话，也没人知道为什么」。
func (a *APIService) SessionsForCanvas(ctx context.Context, canvasID string, limit int) ([]api.AgentSnapshot, error) {
	snaps, err := a.svc.SessionsForCanvas(ctx, canvasID, limit)
	if err != nil {
		return nil, err
	}
	out := make([]api.AgentSnapshot, 0, len(snaps))
	for _, s := range snaps {
		turns := make([]map[string]any, 0, len(s.Turns))
		for _, t := range s.Turns {
			items := make([]map[string]any, 0, len(t.Items))
			for _, it := range t.Items {
				items = append(items, map[string]any{
					"kind": it.Kind, "text": it.Text, "at": it.At, "redacted": it.Redacted,
				})
			}
			turns = append(turns, map[string]any{
				"seq": t.Seq, "status": t.Status, "input": t.Input, "items": items,
			})
		}
		out = append(out, api.AgentSnapshot{
			"id": s.ID, "canvasId": s.CanvasID, "threadId": s.ThreadID,
			"title": s.Title, "createdAt": s.CreatedAt, "turns": turns,
		})
	}
	return out, nil
}

var _ api.CanvasSessionSnapshotter = (*APIService)(nil)

// GetSession 见 api.AgentService。
func (a *APIService) GetSession(ctx context.Context, sessionID string) (*api.AgentSessionDTO, error) {
	sess, err := a.svc.GetSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return toSessionDTO(sess), nil
}

// SubmitTurn 见 api.AgentService。
func (a *APIService) SubmitTurn(ctx context.Context, sessionID, input, actor string) (*api.AgentTurnDTO, error) {
	turn, err := a.svc.SubmitTurn(ctx, sessionID, input, actor)
	if err != nil {
		return nil, err
	}
	return toTurnDTO(turn), nil
}

// Approve 见 api.AgentService。
func (a *APIService) Approve(ctx context.Context, sessionID, turnID, callID, actor string, approve bool) (*api.AgentTurnDTO, error) {
	turn, err := a.svc.Approve(ctx, sessionID, turnID, callID, actor, approve)
	if err != nil {
		return nil, err
	}
	return toTurnDTO(turn), nil
}

func toSessionDTO(s *Session) *api.AgentSessionDTO {
	turns := make([]api.AgentTurnDTO, 0, len(s.Turns))
	for _, t := range s.Turns {
		turns = append(turns, *toTurnDTO(t))
	}
	return &api.AgentSessionDTO{
		ID: s.ID, WorkspaceID: s.WorkspaceID, CanvasID: s.CanvasID,
		Backend: string(s.Backend), ThreadID: s.ThreadID, Title: s.Title,
		Permission: string(s.Permission), Turns: turns, CreatedAt: s.CreatedAt,
	}
}

func toTurnDTO(t *Turn) *api.AgentTurnDTO {
	items := make([]api.AgentItemDTO, 0, len(t.Items))
	for _, it := range t.Items {
		items = append(items, api.AgentItemDTO{
			ID: it.ID, TurnID: it.TurnID, Seq: it.Seq, Kind: string(it.Kind),
			Payload: it.Payload, Source: string(it.Source), CreatedAt: it.CreatedAt,
		})
	}
	dto := &api.AgentTurnDTO{
		ID: t.ID, Seq: t.Seq, Status: string(t.Status), Input: t.Input,
		Items: items, CreatedAt: t.CreatedAt,
		Usage: map[string]any{
			"textTokensIn":  t.Usage.TextTokensIn,
			"textTokensOut": t.Usage.TextTokensOut,
			"toolCalls":     t.Usage.ToolCalls,
			"costMicros":    t.Usage.CostMicros,
		},
	}
	if t.Pending != nil {
		dto.Pending = &api.AgentPendingDTO{
			CallID: t.Pending.CallID, Tool: t.Pending.Tool, Arguments: t.Pending.Arguments,
			OpCount: t.Pending.OpCount, NodeIDs: t.Pending.NodeIDs, EstCostMicros: t.Pending.EstCostMicros,
		}
	}
	if t.Error != nil {
		dto.Error = &api.ErrDTO{Code: t.Error.Code, Message: t.Error.Message, Details: t.Error.Details}
	}
	return dto
}
