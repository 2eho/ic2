package api

import (
	"encoding/json"
	"net/http"

	"github.com/context-flow/ic/internal/platform"
)

// AgentService 由 internal/agent 提供。
type AgentService interface {
	CreateSession(ctx interface{ Done() <-chan struct{} }, wsID, canvasID, backend, title string) (any, error)
}

func (h *handlers) createAgentSession(w http.ResponseWriter, r *http.Request) {
	if h.deps.Agent == nil {
		writeError(w, r, platform.NewError(501, platform.CodeNotImplemented, "agent not configured"))
		return
	}
	writeError(w, r, platform.NewError(501, platform.CodeNotImplemented, "agent session API is provided by internal/agent"))
}

func (h *handlers) getAgentSession(w http.ResponseWriter, r *http.Request) {
	writeError(w, r, platform.NewError(501, platform.CodeNotImplemented, "agent not configured"))
}

func (h *handlers) createAgentTurn(w http.ResponseWriter, r *http.Request) {
	writeError(w, r, platform.NewError(501, platform.CodeNotImplemented, "agent not configured"))
}

func (h *handlers) approveAgentTurn(w http.ResponseWriter, r *http.Request) {
	writeError(w, r, platform.NewError(501, platform.CodeNotImplemented, "agent not configured"))
}

func (h *handlers) agentHistory(w http.ResponseWriter, r *http.Request) {
	writeError(w, r, platform.NewError(501, platform.CodeNotImplemented, "agent not configured"))
}

func (h *handlers) agentToolResult(w http.ResponseWriter, r *http.Request) {
	writeError(w, r, platform.NewError(501, platform.CodeNotImplemented, "agent not configured"))
}

// mcpHTTP 是 MCP Streamable HTTP 入口（见 07 §5）。
func (h *handlers) mcpHTTP(w http.ResponseWriter, r *http.Request) {
	if h.deps.MCP == nil {
		writeError(w, r, platform.NewError(501, platform.CodeNotImplemented, "mcp not configured"))
		return
	}
	var req struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id,omitempty"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params,omitempty"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"jsonrpc": "2.0", "id": nil,
			"error": map[string]any{"code": -32700, "message": "parse error"},
		})
		return
	}
	resp := h.deps.MCP.Handle(r.Context(), req.Method, req.Params, req.ID)
	writeJSON(w, http.StatusOK, resp)
}
