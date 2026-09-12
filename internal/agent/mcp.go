package agent

import (
	"context"
	"encoding/json"

	"github.com/context-flow/ic/internal/platform"
)

// MCP 协议版本（Streamable HTTP）。
const MCPProtocolVersion = "2025-06-18"

// MCPHandler 实现 api.MCPService：把工具表以 MCP 形式暴露。
//
// 设计要点：**工具表与 REST 工具同源**（都来自 ToolSet），避免两套定义漂移。
type MCPHandler struct {
	svc *Service
	// resolveCanvas 由调用方根据 API Key 的 workspace 绑定给出默认画布。
	resolveCanvas func(ctx context.Context) (canvasID, actor string, err error)
}

// NewMCPHandler 构造 MCP 处理器。
func NewMCPHandler(svc *Service, resolve func(context.Context) (string, string, error)) *MCPHandler {
	return &MCPHandler{svc: svc, resolveCanvas: resolve}
}

type mcpRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Handle 分发 MCP 方法。
func (h *MCPHandler) Handle(ctx context.Context, method string, params json.RawMessage, id json.RawMessage) any {
	switch method {
	case "initialize":
		return mcpResult(id, map[string]any{
			"protocolVersion": MCPProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "ic-canvas", "version": platform.Version},
		})
	case "notifications/initialized":
		return map[string]any{"jsonrpc": "2.0"}
	case "tools/list":
		tools := []map[string]any{}
		for _, t := range ToolSet() {
			tools = append(tools, map[string]any{
				"name":        t.Name,
				"description": t.Description,
				"inputSchema": json.RawMessage(t.InputSchema),
				"annotations": map[string]any{
					"readOnlyHint":    t.Scope == "read",
					"destructiveHint": t.Scope == "write",
				},
			})
		}
		return mcpResult(id, map[string]any{"tools": tools})
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return mcpError(id, -32602, "invalid params")
		}
		if h.svc == nil || h.resolveCanvas == nil {
			return mcpError(id, -32603, "agent is not configured")
		}
		canvasID, actor, err := h.resolveCanvas(ctx)
		if err != nil {
			de := platform.AsDomainError(err)
			return mcpError(id, -32603, de.Message)
		}
		res, err := h.svc.ExecuteTool(ctx, canvasID, actor, ToolCall{ID: "mcp", Name: p.Name, Arguments: p.Arguments})
		if err != nil {
			de := platform.AsDomainError(err)
			return mcpError(id, -32603, de.Message)
		}
		content := json.RawMessage(res.Result)
		if len(content) == 0 {
			content, _ = json.Marshal(res)
		}
		return mcpResult(id, map[string]any{
			"content": []map[string]any{{"type": "text", "text": string(content)}},
			"isError": res.Status != "ok",
		})
	case "ping":
		return mcpResult(id, map[string]any{})
	default:
		return mcpError(id, -32601, "method not found: "+method)
	}
}

func mcpResult(id json.RawMessage, result any) any {
	return map[string]any{"jsonrpc": "2.0", "id": rawOrNull(id), "result": result}
}

func mcpError(id json.RawMessage, code int, message string) any {
	return map[string]any{
		"jsonrpc": "2.0", "id": rawOrNull(id),
		"error": map[string]any{"code": code, "message": message},
	}
}

func rawOrNull(id json.RawMessage) any {
	if len(id) == 0 {
		return nil
	}
	return json.RawMessage(id)
}
