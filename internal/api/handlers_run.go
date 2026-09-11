package api

import (
	"encoding/json"
	"net/http"

	"github.com/context-flow/ic/internal/platform"
)

type createRunReq struct {
	TargetNodes []string       `json:"targetNodes"`
	Trigger     string         `json:"trigger"`
	Params      map[string]any `json:"params,omitempty"`
	// IdempotencyKey 也可以走请求头 Idempotency-Key。
	IdempotencyKey string `json:"idempotencyKey,omitempty"`
}

func (h *handlers) createRun(w http.ResponseWriter, r *http.Request) {
	if h.deps.Runs == nil {
		writeError(w, r, platform.NewError(501, platform.CodeNotImplemented, "exec not configured"))
		return
	}
	p, err := h.principal(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var in createRunReq
	if err := decodeBody(r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	key := in.IdempotencyKey
	if key == "" {
		key = r.Header.Get("Idempotency-Key")
	}
	run, err := h.deps.Runs.Submit(r.Context(), RunRequest{
		WorkspaceID:    p.WorkspaceID,
		CanvasID:       r.PathValue("cid"),
		TargetNodes:    in.TargetNodes,
		Trigger:        firstNonEmpty(in.Trigger, "manual"),
		ActorID:        p.UserID,
		Params:         in.Params,
		IdempotencyKey: key,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, run)
}

func (h *handlers) getRun(w http.ResponseWriter, r *http.Request) {
	if h.deps.Runs == nil {
		writeError(w, r, platform.NewError(501, platform.CodeNotImplemented, "exec not configured"))
		return
	}
	p, err := h.principal(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	run, err := h.deps.Runs.Get(r.Context(), p.WorkspaceID, r.PathValue("rid"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (h *handlers) cancelRun(w http.ResponseWriter, r *http.Request) {
	if h.deps.Runs == nil {
		writeError(w, r, platform.NewError(501, platform.CodeNotImplemented, "exec not configured"))
		return
	}
	p, err := h.principal(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if err := h.deps.Runs.Cancel(r.Context(), p.WorkspaceID, r.PathValue("rid")); err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "status": "canceling"})
}

func (h *handlers) replayRun(w http.ResponseWriter, r *http.Request) {
	if h.deps.Runs == nil {
		writeError(w, r, platform.NewError(501, platform.CodeNotImplemented, "exec not configured"))
		return
	}
	p, err := h.principal(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	run, err := h.deps.Runs.Replay(r.Context(), p.WorkspaceID, r.PathValue("rid"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, run)
}

func (h *handlers) listRuns(w http.ResponseWriter, r *http.Request) {
	if h.deps.Runs == nil {
		writeError(w, r, platform.NewError(501, platform.CodeNotImplemented, "exec not configured"))
		return
	}
	p, err := h.principal(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	limit := parseLimit(r.URL.Query().Get("limit"), 20, 200)
	items, cursor, err := h.deps.Runs.List(r.Context(), p.WorkspaceID, r.PathValue("cid"), limit, r.URL.Query().Get("cursor"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "cursor": cursor})
}

// generate 是工作台/调试用的直通生成入口：不落画布，直接返回结果。
func (h *handlers) generate(w http.ResponseWriter, r *http.Request) {
	if h.deps.Runs == nil {
		writeError(w, r, platform.NewError(501, platform.CodeNotImplemented, "exec not configured"))
		return
	}
	p, err := h.requireWorkspace(r, r.PathValue("wid"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	var in struct {
		Capability     string         `json:"capability"`
		ProviderID     string         `json:"providerId,omitempty"`
		CredentialID   string         `json:"credentialId,omitempty"`
		Model          string         `json:"model,omitempty"`
		Prompt         string         `json:"prompt"`
		Params         map[string]any `json:"params,omitempty"`
		OutputCount    int            `json:"outputCount,omitempty"`
		IdempotencyKey string         `json:"idempotencyKey,omitempty"`
	}
	if err := decodeBody(r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	raw, _ := json.Marshal(in.Params)
	_ = raw
	run, err := h.deps.Runs.Submit(r.Context(), RunRequest{
		WorkspaceID: p.WorkspaceID,
		Trigger:     "manual",
		ActorID:     p.UserID,
		AdHoc:       true,
		Params: map[string]any{
			"capability":   in.Capability,
			"providerId":   in.ProviderID,
			"credentialId": in.CredentialID,
			"model":        in.Model,
			"prompt":       in.Prompt,
			"params":       in.Params,
			"outputCount":  in.OutputCount,
		},
		IdempotencyKey: in.IdempotencyKey,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, run)
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}
