package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/context-flow/ic/internal/graph"
	"github.com/context-flow/ic/internal/platform"
)

// 上游工具的执行分发（9.5）。
//
// 与规范工具的关系：规范工具有各自的实现（语义清晰、命名一致）；
// 上游工具**全部**落到同一批底层能力上：
//
//	画布改动 → CanvasGateway.AppendOps（唯一写入口，op 校验只有一条路径）
//	触发运行 → 注入的 RunTrigger（与画布手动点「生成」完全同一条链路）
//	素材新增 → 注入的 AttachmentFetcher（与附件转节点共用）
//
// 这样「34 个工具名」不会变成 34 份实现，只会变成 34 个**入参形状**。
// 代价是翻译层要写全（见 translate.go），收益是任何一条路径都不可能
// 绕过 op 校验、幂等键、审计。

// executeUpstream 执行一个上游工具名。返回 (结果, 是否已处理)。
//
// 「是否已处理」是必要的：不返回它就要在这里给出「unknown tool」，
// 而那会让规范工具的分发逻辑散落两处。
func (s *Service) executeUpstream(ctx context.Context, canvasID, actor string, call ToolCall) (ToolCallResult, bool) {
	switch call.Name {
	case "site_navigate":
		return s.toolSiteNavigate(call), true
	case "canvas_list_projects":
		return s.toolListProjects(ctx, call), true
	case "canvas_create_node":
		return s.applyTranslated(ctx, canvasID, actor, call, func(newID idGen) (translateResult, error) {
			return translateCreateNode(call.Arguments, newID)
		})
	case "canvas_create_text_nodes":
		return s.applyTranslated(ctx, canvasID, actor, call, func(newID idGen) (translateResult, error) {
			return translateCreateTextNodes(call.Arguments, newID)
		})
	case "canvas_create_config_node", "canvas_create_image_prompt_flow",
		"canvas_generate_text", "canvas_generate_image",
		"canvas_generate_video", "canvas_generate_audio":
		return s.toolCreateGenerationFlow(ctx, canvasID, actor, call)
	case "canvas_update_node":
		return s.applyTranslated(ctx, canvasID, actor, call, func(idGen) (translateResult, error) {
			return translateUpdateNode(call.Arguments)
		})
	case "canvas_update_node_text":
		return s.applyTranslated(ctx, canvasID, actor, call, func(idGen) (translateResult, error) {
			return translateUpdateNodeText(call.Arguments)
		})
	case "canvas_move_nodes":
		return s.applyTranslated(ctx, canvasID, actor, call, func(idGen) (translateResult, error) {
			return translateMoveNodes(call.Arguments)
		})
	case "canvas_resize_node":
		return s.applyTranslated(ctx, canvasID, actor, call, func(idGen) (translateResult, error) {
			return translateResizeNode(call.Arguments)
		})
	case "canvas_delete_nodes":
		return s.applyTranslated(ctx, canvasID, actor, call, func(idGen) (translateResult, error) {
			return translateDeleteNodes(call.Arguments)
		})
	case "canvas_connect_nodes":
		return s.toolConnectNodes(ctx, canvasID, actor, call)
	case "canvas_select_nodes":
		// 选中态是**每个客户端各自**的视图状态。放服务端会导致
		// 「A 的选中把 B 的选中改掉」——这是一个真实的多端 bug，
		// 而不是「实现不完整」。
		return ToolCallResult{CallID: call.ID, Status: "error", Error: &ToolError{
			Code: platform.CodeNotImplemented,
			Message: "canvas_select_nodes 由前端的画布客户端执行：选中态是每个客户端各自的视图状态，" +
				"放服务端会让多端互相覆盖。请改用前端交互或 canvas.get_selection 读取。",
		}}, true
	case "canvas_set_viewport":
		return s.toolSetViewport(ctx, canvasID, actor, call)
	case "generation_get_status":
		return s.toolGenerationStatus(ctx, canvasID, call), true
	case "workbench_image_get_config":
		return toolOK(call, imageWorkbenchConfig()), true
	case "workbench_video_get_config":
		return toolOK(call, videoWorkbenchConfig()), true
	case "workbench_image_generate":
		return s.toolWorkbenchGenerate(ctx, canvasID, actor, call, "image.generate", "image")
	case "workbench_video_generate":
		return s.toolWorkbenchGenerate(ctx, canvasID, actor, call, "video.generate", "video")
	case "assets_add":
		return s.toolAssetsAdd(ctx, canvasID, call)
	}
	// 别名工具：转发到规范实现（入参形状已经完全一致）
	if canon, ok := upstreamAliases[call.Name]; ok {
		forwarded := call
		forwarded.Name = canon
		res, err := s.ExecuteTool(ctx, canvasID, actor, forwarded)
		if err != nil {
			return res, true
		}
		return res, true
	}
	return ToolCallResult{}, false
}

// applyTranslated 是「翻译 + 提交」的共用骨架。
//
// 抽出来的原因：10 个画布工具都是「翻译成 op → 以当前版本为 base 提交 →
// 返回 inverse 以便撤销」。每处各写一遍会让「有一个忘了带 inverse」
// 变成「有一个工具不能撤销」，而那种不一致极难发现。
func (s *Service) applyTranslated(
	ctx context.Context, canvasID, actor string, call ToolCall,
	translate func(idGen) (translateResult, error),
) (ToolCallResult, bool) {
	res, err := translate(func(prefix string) string { return s.ids.NewID(prefix) })
	if err != nil {
		// 翻译失败 = 用户入参问题 → invalid_request（不是 500，也不是重试）
		return ToolCallResult{CallID: call.ID, Status: "error", Error: &ToolError{
			Code: platform.CodeInvalidRequest, Message: platform.Redact(err.Error()),
		}}, true
	}
	if len(res.ops) == 0 {
		// 没有 op 但有 result 的情况（例如 canvas_connect_nodes 需要读文档）
		return toolOK(call, res.result), true
	}
	doc, err := s.canvas.Get(ctx, canvasID)
	if err != nil {
		return toolError(call, err), true
	}
	applied, _, err := s.canvas.AppendOps(ctx, canvasID, doc.Version, res.ops, actor)
	if err != nil {
		return toolError(call, err), true
	}
	inverse, _ := json.Marshal(applied.Inverse)
	return ToolCallResult{
		CallID: call.ID, Status: "ok",
		Applied: &AppliedInfo{Ops: applied.Applied, Version: applied.Version},
		Inverse: inverse,
		Result:  marshalOrNil(res.result),
	}, true
}

// toolCreateGenerationFlow 覆盖「创建配置节点 / 提示词生图流程 / 四种 generate_*」。
//
// 它们共用同一段实现是刻意的：四个工具的唯一差别是**能力**与
// 「是否自动运行」。各写一份的话，就会有人漏加 autoRun 的写回，
// 表现是「生成成功了但画布上的节点没有结果」。
func (s *Service) toolCreateGenerationFlow(
	ctx context.Context, canvasID, actor string, call ToolCall,
) (ToolCallResult, bool) {
	var in struct {
		Prompt           string          `json:"prompt"`
		Mode             string          `json:"mode"`
		Title            string          `json:"title"`
		X                *float64        `json:"x"`
		Y                *float64        `json:"y"`
		AutoRun          bool            `json:"autoRun"`
		ReferenceNodeIDs []string        `json:"referenceNodeIds"`
		Model            string          `json:"model"`
		Count            *int            `json:"count"`
		Size             string          `json:"size"`
		Quality          string          `json:"quality"`
		Extra            json.RawMessage `json:"-"`
	}
	if err := strictDecode(call.Arguments, &in); err != nil {
		return invalidArgs(call, err), true
	}
	capability := capabilityForTool(call.Name, in.Mode)
	if capability == "" {
		return invalidArgs(call, errors.New("mode 只能是 text/image/video/audio")), true
	}
	prompt := in.Prompt
	// 四个 generate_* 工具的上游语义是「创建流程并立即生成」，
	// 因此 prompt 为空时不静默创建一个空流程（那会生成出一张随机图，
	// 还产生费用）。
	if prompt == "" && strings.HasPrefix(call.Name, "canvas_generate_") {
		return invalidArgs(call, errors.New("prompt 必填：generate_* 会立即触发生成并产生费用")), true
	}

	doc, err := s.canvas.Get(ctx, canvasID)
	if err != nil {
		return toolError(call, err), true
	}
	x, y := 0.0, 0.0
	if in.X != nil {
		x = *in.X
	}
	if in.Y != nil {
		y = *in.Y
	}
	ops := []json.RawMessage{}
	var promptID, genID string
	if prompt != "" {
		promptID = s.ids.NewID("n")
		ops = append(ops, mustJSONOp(map[string]any{"kind": "add_node", "node": map[string]any{
			"id": promptID, "type": string(graph.NodeTypePrompt), "title": "提示词",
			"rect": map[string]any{"x": x, "y": y, "w": 320, "h": 220},
			"spec": map[string]any{"text": prompt},
		}}))
	}
	genID = s.ids.NewID("gen")
	genSpec := map[string]any{"capability": capability, "outputCount": 1}
	if in.Model != "" {
		genSpec["model"] = in.Model
	}
	if in.Count != nil && *in.Count > 0 {
		genSpec["outputCount"] = *in.Count
	}
	params := map[string]any{}
	if in.Size != "" {
		params["size"] = in.Size
	}
	if in.Quality != "" {
		params["quality"] = in.Quality
	}
	if len(params) > 0 {
		genSpec["params"] = params
	}
	ops = append(ops, mustJSONOp(map[string]any{"kind": "add_node", "node": map[string]any{
		"id": genID, "type": string(graph.NodeTypeGeneration), "title": "生成",
		"rect": map[string]any{"x": x + 380, "y": y, "w": 340, "h": 260},
		"spec": genSpec,
	}}))
	if promptID != "" {
		ops = append(ops, mustJSONOp(map[string]any{"kind": "add_edge", "edge": map[string]any{
			"id":   s.ids.NewID("e"),
			"from": map[string]any{"nodeId": promptID, "portId": "out"},
			"to":   map[string]any{"nodeId": genID, "portId": "prompt"},
			"kind": "text",
		}}))
	}
	// 参考图连线：**必须校验目标节点存在**，否则连线会以
	// 「edge source missing」在 op 应用阶段失败，错误信息与
	// 用户填错的字段（referenceNodeIds）对不上。
	for i, refID := range in.ReferenceNodeIDs {
		if _, ok := doc.Nodes[refID]; !ok {
			return ToolCallResult{CallID: call.ID, Status: "error", Error: &ToolError{
				Code:    platform.CodeNotFound,
				Message: fmt.Sprintf("referenceNodeIds[%d] 指向的节点不存在: %s", i, refID),
			}}, true
		}
		ops = append(ops, mustJSONOp(map[string]any{"kind": "add_edge", "edge": map[string]any{
			"id":   s.ids.NewID("e"),
			"from": map[string]any{"nodeId": refID, "portId": "out"},
			"to":   map[string]any{"nodeId": genID, "portId": "ref"},
			"kind": "image",
		}}))
	}

	applied, _, err := s.canvas.AppendOps(ctx, canvasID, doc.Version, ops, actor)
	if err != nil {
		return toolError(call, err), true
	}
	inverse, _ := json.Marshal(applied.Inverse)
	result := map[string]any{"generationNodeId": genID, "promptNodeId": promptID}

	// autoRun 或 generate_* 时真正触发运行。
	// 「创建了但没有运行」而返回 ok 属于谎报成功 —— 用户会一直等结果。
	if in.AutoRun || strings.HasPrefix(call.Name, "canvas_generate_") {
		if s.runs == nil {
			return ToolCallResult{CallID: call.ID, Status: "error", Error: &ToolError{
				Code:    platform.CodeNotImplemented,
				Message: "节点已创建，但生成能力未装配（RunTrigger 未注入），未触发运行",
			}}, true
		}
		runID, rerr := s.runs.TriggerRun(ctx, canvasID, []string{genID}, actor)
		if rerr != nil {
			return toolError(call, rerr), true
		}
		result["runId"] = runID
	}
	return ToolCallResult{
		CallID: call.ID, Status: "ok",
		Applied: &AppliedInfo{Ops: applied.Applied, Version: applied.Version},
		Inverse: inverse,
		Result:  marshalOrNil(result),
	}, true
}

// capabilityForTool 按工具名或 mode 决定能力。
func capabilityForTool(toolName, mode string) string {
	switch toolName {
	case "canvas_generate_text":
		return "text.generate"
	case "canvas_generate_image":
		return "image.generate"
	case "canvas_generate_video":
		return "video.generate"
	case "canvas_generate_audio":
		return "audio.generate"
	case "canvas_create_image_prompt_flow":
		return "image.generate"
	}
	switch mode {
	case "text":
		return "text.generate"
	case "image", "":
		return "image.generate"
	case "video":
		return "video.generate"
	case "audio":
		return "audio.generate"
	}
	return ""
}

// toolConnectNodes 解析端口并批量连线。
//
// 端口解析放在这里（而不是翻译层）是因为它需要读文档，
// 而翻译层是纯函数。解析规则与画布内的连线校验同源：
// **输出端口 kind 必须等于输入端口 kind**，否则拒绝（与 createEdge 一致）。
func (s *Service) toolConnectNodes(
	ctx context.Context, canvasID, actor string, call ToolCall,
) (ToolCallResult, bool) {
	tr, err := translateConnectNodes(call.Arguments)
	if err != nil {
		return invalidArgs(call, err), true
	}
	var conns []struct {
		FromNodeID string `json:"fromNodeId"`
		ToNodeID   string `json:"toNodeId"`
	}
	raw, _ := json.Marshal(tr.result["connections"])
	if err := json.Unmarshal(raw, &conns); err != nil {
		return invalidArgs(call, err), true
	}
	doc, err := s.canvas.Get(ctx, canvasID)
	if err != nil {
		return toolError(call, err), true
	}
	ops := []json.RawMessage{}
	created := make([]map[string]any, 0, len(conns))
	for i, c := range conns {
		from, ok := doc.Nodes[c.FromNodeID]
		if !ok {
			return ToolCallResult{CallID: call.ID, Status: "error", Error: &ToolError{
				Code:    platform.CodeNotFound,
				Message: fmt.Sprintf("connections[%d].fromNodeId 不存在: %s", i, c.FromNodeID),
			}}, true
		}
		to, ok := doc.Nodes[c.ToNodeID]
		if !ok {
			return ToolCallResult{CallID: call.ID, Status: "error", Error: &ToolError{
				Code:    platform.CodeNotFound,
				Message: fmt.Sprintf("connections[%d].toNodeId 不存在: %s", i, c.ToNodeID),
			}}, true
		}
		out, in := matchPorts(from, to)
		if out == nil || in == nil {
			// 指出两边各自有什么类型：只说「类型不匹配」时用户得自己
			// 去数端口，而端口的名字在界面上根本不显示。
			return ToolCallResult{CallID: call.ID, Status: "error", Error: &ToolError{
				Code: platform.CodeInvalidSpec,
				Message: fmt.Sprintf("connections[%d]: %s 的输出（%s）与 %s 的输入（%s）没有共同的资源类型",
					i, from.ID, portKinds(from.Ports.Outputs), to.ID, portKinds(to.Ports.Inputs)),
			}}, true
		}
		ops = append(ops, mustJSONOp(map[string]any{"kind": "add_edge", "edge": map[string]any{
			"id":   s.ids.NewID("e"),
			"from": map[string]any{"nodeId": from.ID, "portId": out.ID},
			"to":   map[string]any{"nodeId": to.ID, "portId": in.ID},
			"kind": out.Kind,
		}}))
		created = append(created, map[string]any{
			"from": from.ID, "fromPort": out.ID, "to": to.ID, "toPort": in.ID, "kind": out.Kind,
		})
	}
	applied, _, err := s.canvas.AppendOps(ctx, canvasID, doc.Version, ops, actor)
	if err != nil {
		return toolError(call, err), true
	}
	inverse, _ := json.Marshal(applied.Inverse)
	return ToolCallResult{
		CallID: call.ID, Status: "ok",
		Applied: &AppliedInfo{Ops: applied.Applied, Version: applied.Version},
		Inverse: inverse,
		Result:  marshalOrNil(map[string]any{"edges": created}),
	}, true
}

// matchPorts 按类型匹配一对端口（同 kind 才可连）。
func matchPorts(from, to graph.Node) (*graph.Port, *graph.Port) {
	for _, in := range to.Ports.Inputs {
		for i := range from.Ports.Outputs {
			out := &from.Ports.Outputs[i]
			if out.Kind == in.Kind {
				inCopy := in
				return out, &inCopy
			}
		}
	}
	return nil, nil
}

func portKinds(ports []graph.Port) string {
	if len(ports) == 0 {
		return "无"
	}
	seen := []string{}
	for _, p := range ports {
		seen = append(seen, string(p.Kind))
	}
	return strings.Join(seen, "/")
}

// toolSetViewport 写视口（它是画布文档的一部分，因此真的落库）。
func (s *Service) toolSetViewport(
	ctx context.Context, canvasID, actor string, call ToolCall,
) (ToolCallResult, bool) {
	var in struct {
		Viewport struct {
			X *float64 `json:"x"`
			Y *float64 `json:"y"`
			K *float64 `json:"k"`
		} `json:"viewport"`
	}
	if err := strictDecode(call.Arguments, &in); err != nil {
		return invalidArgs(call, err), true
	}
	if in.Viewport.X == nil || in.Viewport.Y == nil || in.Viewport.K == nil {
		return invalidArgs(call, errors.New("viewport 必须包含 x / y / k")), true
	}
	doc, err := s.canvas.Get(ctx, canvasID)
	if err != nil {
		return toolError(call, err), true
	}
	op := mustJSONOp(map[string]any{"kind": "set_viewport", "viewport": map[string]any{
		"x": *in.Viewport.X, "y": *in.Viewport.Y, "k": *in.Viewport.K,
	}})
	applied, _, err := s.canvas.AppendOps(ctx, canvasID, doc.Version, []json.RawMessage{op}, actor)
	if err != nil {
		return toolError(call, err), true
	}
	return ToolCallResult{CallID: call.ID, Status: "ok",
		Applied: &AppliedInfo{Ops: applied.Applied, Version: applied.Version}}, true
}

// toolSiteNavigate 返回受限的导航提示。
//
// 服务端**不代用户跳转**：一个「Agent 可以触发的页面跳转」就是一个
// 开放重定向 + 钓鱼入口（Agent 被提示词注入后可以把用户导去任意页面）。
// 正确做法是把导航意图交给前端，由前端在用户可见的上下文里执行。
func (s *Service) toolSiteNavigate(call ToolCall) ToolCallResult {
	var in struct {
		Path string `json:"path"`
	}
	if err := strictDecode(call.Arguments, &in); err != nil {
		return invalidArgs(call, err)
	}
	allowed := map[string]bool{
		"/": true, "/canvas": true, "/projects": true, "/image": true,
		"/video": true, "/prompts": true, "/assets": true, "/config": true,
		"/workbench/image": true, "/workbench/video": true,
	}
	path := strings.TrimSpace(in.Path)
	ok := allowed[path] ||
		(strings.HasPrefix(path, "/canvas/") && !strings.Contains(path, "..")) ||
		(strings.HasPrefix(path, "/") && !strings.Contains(path, "//") &&
			!strings.Contains(path, "..") && !strings.Contains(path, ":"))
	if !ok {
		return ToolCallResult{CallID: call.ID, Status: "error", Error: &ToolError{
			Code:    platform.CodeInvalidRequest,
			Message: "path 不是应用内部路由: " + platform.Redact(path),
		}}
	}
	return ToolCallResult{CallID: call.ID, Status: "error", Error: &ToolError{
		Code: platform.CodeNotImplemented,
		Message: "site_navigate 需由画布客户端执行（服务端不代用户跳转）：" +
			"已校验 path 合法（" + path + "），请在前端会话中触发导航。",
	}}
}

// toolListProjects 列出工作区内的画布。
func (s *Service) toolListProjects(ctx context.Context, call ToolCall) ToolCallResult {
	var in struct {
		Keyword  string `json:"keyword"`
		Page     int    `json:"page"`
		PageSize int    `json:"pageSize"`
	}
	if err := strictDecode(call.Arguments, &in); err != nil {
		return invalidArgs(call, err)
	}
	// 这个工具不需要 canvas 上下文（它的用途正是「找到那个画布」），
	// 因此走注入的 ProjectLister 而不是 canvas 网关。
	if s.projects == nil {
		return notWired(call, "canvas_list_projects")
	}
	limit := clampLimit(in.PageSize, 20)
	page := in.Page
	if page < 1 {
		page = 1
	}
	items, err := s.projects.ListProjectsBrief(ctx, in.Keyword, page, limit)
	if err != nil {
		return toolError(call, err)
	}
	return toolOK(call, map[string]any{"items": items, "page": page})
}

// toolGenerationStatus 查询生成状态。
func (s *Service) toolGenerationStatus(ctx context.Context, canvasID string, call ToolCall) ToolCallResult {
	var in struct {
		Scope   string   `json:"scope"`
		TaskID  string   `json:"taskId"`
		NodeIDs []string `json:"nodeIds"`
		Limit   int      `json:"limit"`
	}
	if err := strictDecode(call.Arguments, &in); err != nil {
		return invalidArgs(call, err)
	}
	if s.runList == nil {
		return notWired(call, "generation_get_status")
	}
	scope := in.Scope
	if scope == "" {
		scope = "canvas"
	}
	switch scope {
	case "all", "canvas", "image", "video":
	default:
		return invalidArgs(call, errors.New("scope 只能是 all/canvas/image/video"))
	}
	wsID := s.workspaceOf(ctx, canvasID)
	// scope=canvas 或 all 时查画布运行；工作台的直通运行没有 canvasId，
	// 因此 image/video 用同一接口但只按工作区过滤（避免「查不到」的错觉）。
	items, err := s.runList.ListRunsBrief(ctx, wsID, canvasID, clampLimit(in.Limit, 20))
	if err != nil {
		return toolError(call, err)
	}
	if len(in.NodeIDs) > 0 {
		want := map[string]bool{}
		for _, id := range in.NodeIDs {
			want[id] = true
		}
		filtered := items[:0]
		for _, it := range items {
			targets, _ := it["targets"].([]string)
			for _, t := range targets {
				if want[t] {
					filtered = append(filtered, it)
					break
				}
			}
		}
		items = filtered
	}
	return toolOK(call, map[string]any{"scope": scope, "items": items})
}

// toolWorkbenchGenerate 在工作台发起直通生成（不落画布，复用同一执行引擎）。
func (s *Service) toolWorkbenchGenerate(
	ctx context.Context, canvasID, actor string, call ToolCall, capability, kind string,
) (ToolCallResult, bool) {
	var in struct {
		Prompt        string   `json:"prompt"`
		Model         string   `json:"model"`
		Size          string   `json:"size"`
		Quality       string   `json:"quality"`
		Count         *int     `json:"count"`
		Seconds       string   `json:"seconds"`
		Resolution    string   `json:"resolution"`
		GenerateAudio *bool    `json:"generateAudio"`
		Watermark     *bool    `json:"watermark"`
		Mode          string   `json:"mode"`
		Run           *bool    `json:"run"`
		References    []string `json:"references"`
	}
	if err := strictDecode(call.Arguments, &in); err != nil {
		return invalidArgs(call, err), true
	}
	if strings.TrimSpace(in.Prompt) == "" {
		return invalidArgs(call, errors.New("prompt 必填")), true
	}
	if in.Run != nil && !*in.Run {
		// run=false 的上游语义是「只填参数不提交」。服务端没有「填写中的表单」
		// 这个状态，因此显式说明，而不是返回一个空的成功。
		return ToolCallResult{CallID: call.ID, Status: "error", Error: &ToolError{
			Code: platform.CodeNotImplemented,
			Message: "run=false（只填参数不提交）由前端工作台执行：服务端没有「填写中的表单」这一状态。" +
				"请省略 run 或传 true 直接生成。",
		}}, true
	}
	if s.adhoc == nil {
		return notWired(call, call.Name), true
	}
	params := map[string]any{
		"capability": capability,
		"prompt":     in.Prompt,
		"kind":       kind,
	}
	if in.Model != "" {
		params["model"] = in.Model
	}
	if in.Size != "" {
		params["size"] = in.Size
	}
	if in.Quality != "" {
		params["quality"] = in.Quality
	}
	if in.Count != nil && *in.Count > 0 {
		params["outputCount"] = *in.Count
	}
	if in.Seconds != "" {
		params["seconds"] = in.Seconds
	}
	if in.Resolution != "" {
		params["vquality"] = in.Resolution
	}
	if in.GenerateAudio != nil {
		params["generateAudio"] = *in.GenerateAudio
	}
	if in.Watermark != nil {
		params["watermark"] = *in.Watermark
	}
	if in.Mode != "" {
		params["videoMode"] = in.Mode
	}
	if len(in.References) > 0 {
		params["references"] = in.References
	}
	runID, err := s.adhoc.SubmitAdHoc(ctx, canvasID, actor, params)
	if err != nil {
		return toolError(call, err), true
	}
	return toolOK(call, map[string]any{"runId": runID, "capability": capability}), true
}

// toolAssetsAdd 向素材库新增素材（text 直接入库，image 走附件抓取）。
func (s *Service) toolAssetsAdd(ctx context.Context, canvasID string, call ToolCall) (ToolCallResult, bool) {
	var in struct {
		Kind     string         `json:"kind"`
		Title    string         `json:"title"`
		Content  string         `json:"content"`
		ImageURL string         `json:"imageUrl"`
		Tags     []string       `json:"tags"`
		Source   string         `json:"source"`
		Note     string         `json:"note"`
		Meta     map[string]any `json:"-"`
	}
	if err := strictDecode(call.Arguments, &in); err != nil {
		return invalidArgs(call, err), true
	}
	if in.Title == "" {
		return invalidArgs(call, errors.New("title 必填")), true
	}
	if s.files == nil {
		return notWired(call, "assets_add"), true
	}
	wsID := s.workspaceOf(ctx, canvasID)
	if wsID == "" {
		return toolError(call, platform.ErrNotFound("workspace")), true
	}
	switch in.Kind {
	case "text":
		if in.Content == "" {
			return invalidArgs(call, errors.New("kind=text 时 content 必填")), true
		}
		// 文本素材入库走文本资产的写路径（由 AttachmentFetcher 的
		// dataURI 分支承担，避免为纯文本再引入一条上传接口）。
		assetID, err := s.files.StoreAttachment(ctx, wsID, in.Title+".txt",
			"text/plain", "", "data:text/plain;base64,"+base64Text(in.Content))
		if err != nil {
			return toolError(call, err), true
		}
		return toolOK(call, map[string]any{"assetId": assetID, "kind": "text"}), true
	case "image":
		if in.ImageURL == "" {
			return invalidArgs(call, errors.New("kind=image 时 imageUrl 必填")), true
		}
		assetID, err := s.files.StoreAttachment(ctx, wsID, in.Title+".png",
			"image/png", in.ImageURL, "")
		if err != nil {
			return toolError(call, err), true
		}
		return toolOK(call, map[string]any{"assetId": assetID, "kind": "image"}), true
	}
	return invalidArgs(call, errors.New("kind 只能是 text 或 image")), true
}

// imageWorkbenchConfig 是生图工作台的可选项（与前端 imagesize 表同源）。
func imageWorkbenchConfig() map[string]any {
	return map[string]any{
		"quality": []string{"auto", "low", "medium", "high"},
		"tiers":   []string{"1k", "2k", "4k", "auto"},
		"ratios":  []string{"1:1", "16:9", "9:16", "4:3", "3:4", "3:2", "2:3", "21:9", "9:21"},
		"count":   map[string]any{"min": 1, "max": 15},
		"note": "size 可传档位（如 2k）或显式尺寸（如 2048x1152）；" +
			"尺寸必须能被 16 整除，比例集合有限。",
	}
}

// videoWorkbenchConfig 是视频创作台的可选项。
func videoWorkbenchConfig() map[string]any {
	return map[string]any{
		"resolution": []string{"480", "720", "1080"},
		"ratios":     []string{"1:1", "3:4", "4:3", "16:9", "9:16", "21:9", "auto"},
		"seconds":    map[string]any{"min": 4, "max": 30, "default": 6},
		"modes":      []string{"frames", "reference"},
		"flags":      []string{"generateAudio", "watermark"},
	}
}

// marshalOrNil 序列化结果（nil 时返回空，避免产出 "null" 字面量）。
func marshalOrNil(v map[string]any) json.RawMessage {
	if len(v) == 0 {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}

func base64Text(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}
