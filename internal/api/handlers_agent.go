package api

import (
	"encoding/json"
	"net/http"

	"github.com/context-flow/ic/internal/platform"
)

func (h *handlers) createAgentSession(w http.ResponseWriter, r *http.Request) {
	if h.deps.Agent == nil {
		writeError(w, r, platform.NewError(501, platform.CodeNotImplemented, "agent not configured"))
		return
	}
	p, err := h.principal(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var in struct {
		WorkspaceID string `json:"workspaceId"`
		CanvasID    string `json:"canvasId"`
		Backend     string `json:"backend"`
		Title       string `json:"title"`
	}
	if err := decodeBody(r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	wsID := firstNonEmpty(in.WorkspaceID, p.WorkspaceID)
	if wsID == "" {
		writeError(w, r, platform.ErrInvalid("workspaceId is required"))
		return
	}
	if _, err := h.requireWorkspace(r, wsID); err != nil {
		writeError(w, r, err)
		return
	}
	sess, err := h.deps.Agent.CreateSession(r.Context(), wsID, in.CanvasID, in.Backend, in.Title)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, sess)
}

func (h *handlers) getAgentSession(w http.ResponseWriter, r *http.Request) {
	if h.deps.Agent == nil {
		writeError(w, r, platform.NewError(501, platform.CodeNotImplemented, "agent not configured"))
		return
	}
	if _, err := h.principal(r); err != nil {
		writeError(w, r, err)
		return
	}
	sess, err := h.deps.Agent.GetSession(r.Context(), r.PathValue("sid"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, sess)
}

func (h *handlers) createAgentTurn(w http.ResponseWriter, r *http.Request) {
	if h.deps.Agent == nil {
		writeError(w, r, platform.NewError(501, platform.CodeNotImplemented, "agent not configured"))
		return
	}
	p, err := h.principal(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var in struct {
		Input string `json:"input"`
	}
	if err := decodeBody(r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	turn, err := h.deps.Agent.SubmitTurn(r.Context(), r.PathValue("sid"), in.Input, p.UserID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, turn)
}

func (h *handlers) approveAgentTurn(w http.ResponseWriter, r *http.Request) {
	if h.deps.Agent == nil {
		writeError(w, r, platform.NewError(501, platform.CodeNotImplemented, "agent not configured"))
		return
	}
	p, err := h.principal(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var in struct {
		Approve bool   `json:"approve"`
		CallID  string `json:"callId"`
	}
	if err := decodeBody(r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	turn, err := h.deps.Agent.Approve(r.Context(), r.PathValue("sid"), r.PathValue("tid"), in.CallID, p.UserID, in.Approve)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, turn)
}

func (h *handlers) agentHistory(w http.ResponseWriter, r *http.Request) {
	h.getAgentSession(w, r)
}

func (h *handlers) agentToolResult(w http.ResponseWriter, r *http.Request) {
	writeError(w, r, platform.NewError(501, platform.CodeNotImplemented,
		"tool results are produced by the browser executor and recorded through /turns/{tid}/approve"))
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
