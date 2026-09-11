package agent

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/context-flow/ic/internal/graph"
	"github.com/context-flow/ic/internal/platform"
)

// 工具实现的两个原则（上一个实现里各违反了一次）：
//
//  1. **不谎报成功**：能力未注入时返回 not_implemented，而不是 ok。
//     上一轮 canvas.run_generation 返回 `{"note":"delegated to api layer"}` 并带 ok 状态，
//     用户看到「Agent 已触发生成」但画布毫无变化。
//  2. **校验前置**：目标节点不存在这类错误必须在调用上游/异步阶段之前报出，
//     否则用户看到的是「点了没反应」，而不是一条可行动的提示。

func toolOK(call ToolCall, result any) ToolCallResult {
	raw, _ := json.Marshal(result)
	return ToolCallResult{CallID: call.ID, Status: "ok", Result: raw}
}

func toolError(call ToolCall, err error) ToolCallResult {
	de := platform.AsDomainError(err)
	code := platform.CodeInternal
	msg := "内部错误"
	if de != nil {
		code = de.Code
		msg = platform.Redact(de.Message)
	}
	return ToolCallResult{CallID: call.ID, Status: "error", Error: &ToolError{Code: code, Message: msg}}
}

func invalidArgs(call ToolCall, err error) ToolCallResult {
	return ToolCallResult{CallID: call.ID, Status: "error", Error: &ToolError{
		Code: platform.CodeInvalidRequest, Message: platform.Redact(err.Error())}}
}

func notWired(call ToolCall, tool string) ToolCallResult {
	return ToolCallResult{CallID: call.ID, Status: "error", Error: &ToolError{
		Code:    platform.CodeNotImplemented,
		Message: "该能力尚未在服务端装配: " + tool,
	}}
}

func clampLimit(v, def int) int {
	if v <= 0 {
		return def
	}
	if v > 100 {
		return 100
	}
	return v
}

// workspaceOf 从画布反查工作区（素材/提示词检索需要工作区边界，INV-10）。
// 解析不出时返回空串，由下游给出「未找到」而不是跨工作区检索。
func (s *Service) workspaceOf(ctx context.Context, canvasID string) string {
	type wsResolver interface {
		WorkspaceOf(ctx context.Context, canvasID string) (string, error)
	}
	if r, ok := s.canvas.(wsResolver); ok {
		if ws, err := r.WorkspaceOf(ctx, canvasID); err == nil {
			return ws
		}
	}
	return ""
}

// createAttachmentNodes 见 9.7：把附件（URL 或 data URI）落成真实图片节点。
//
// 与原项目 `canvas_create_attachment_nodes` 语义对等，但有两处刻意差异：
//   - 附件先入库成资产再建节点（原项目直接把 dataURL 塞进节点 metadata，
//     导致画布 JSON 随图片膨胀，且导出/分享时不可用）；
//   - 落位排布避开已占区域（原项目固定 40px 错位，节点多了会叠在一起）。
func (s *Service) createAttachmentNodes(ctx context.Context, canvasID, actor string, call ToolCall) (ToolCallResult, error) {
	if s.files == nil {
		return notWired(call, "canvas.create_attachment_nodes"), nil
	}
	var args struct {
		Attachments []struct {
			Name string `json:"name"`
			Mime string `json:"mime"`
			URL  string `json:"url"`
			Data string `json:"data"`
		} `json:"attachments"`
		OriginX float64 `json:"originX"`
		OriginY float64 `json:"originY"`
	}
	if err := strictDecode(call.Arguments, &args); err != nil {
		return invalidArgs(call, err), nil
	}
	if len(args.Attachments) == 0 {
		return invalidArgs(call, errors.New("attachments is required")), nil
	}
	if len(args.Attachments) > 20 {
		return invalidArgs(call, errors.New("一次最多创建 20 个附件节点")), nil
	}
	wsID := s.workspaceOf(ctx, canvasID)
	if wsID == "" {
		return toolError(call, platform.ErrNotFound("workspace")), nil
	}

	doc, err := s.canvas.Get(ctx, canvasID)
	if err != nil {
		return toolError(call, err), nil
	}
	// 落位：从 origin 开始，按已占区域向右下推，避免与现有节点重叠。
	place := graph.PlaceNewNodes(doc, len(args.Attachments), graph.Point{X: args.OriginX, Y: args.OriginY})

	ops := make([]json.RawMessage, 0, len(args.Attachments))
	nodeIDs := make([]string, 0, len(args.Attachments))
	for i, att := range args.Attachments {
		if att.URL == "" && att.Data == "" {
			return invalidArgs(call, errors.New("附件必须提供 url 或 data")), nil
		}
		assetID, err := s.files.StoreAttachment(ctx, wsID, att.Name, att.Mime, att.URL, att.Data)
		if err != nil {
			return toolError(call, err), nil
		}
		nodeID := s.ids.NewID("n")
		nodeIDs = append(nodeIDs, nodeID)
		title := att.Name
		if title == "" {
			title = "附件"
		}
		raw, err := json.Marshal(map[string]any{
			"kind": "add_node",
			"node": map[string]any{
				"id": nodeID, "type": "image", "title": title,
				"rect": map[string]any{"x": place[i].X, "y": place[i].Y, "w": 320, "h": 320},
				"spec": map[string]any{"assetId": assetID, "fit": "cover"},
			},
		})
		if err != nil {
			return toolError(call, err), nil
		}
		ops = append(ops, raw)
	}

	res, _, err := s.canvas.AppendOps(ctx, canvasID, doc.Version, ops, actor)
	if err != nil {
		return toolError(call, err), nil
	}
	inverse, _ := json.Marshal(res.Inverse)
	return ToolCallResult{
		CallID: call.ID, Status: "ok", Inverse: inverse,
		Applied: &AppliedInfo{Ops: res.Applied, Version: res.Version},
		Result:  mustJSON(map[string]any{"nodeIds": nodeIDs}),
	}, nil
}

func mustJSON(v any) json.RawMessage {
	raw, _ := json.Marshal(v)
	return raw
}
