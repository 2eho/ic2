package api

import (
	"net/http"
	"strings"

	"github.com/context-flow/ic/internal/platform"
)

func (h *handlers) listPromptSources(w http.ResponseWriter, r *http.Request) {
	if h.deps.Prompts == nil {
		writeError(w, r, platform.NewError(501, platform.CodeNotImplemented, "prompts not configured"))
		return
	}
	if _, err := h.requireWorkspace(r, r.PathValue("wid")); err != nil {
		writeError(w, r, err)
		return
	}
	items, err := h.deps.Prompts.ListSources(r.Context(), r.PathValue("wid"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *handlers) createPromptSource(w http.ResponseWriter, r *http.Request) {
	if h.deps.Prompts == nil {
		writeError(w, r, platform.NewError(501, platform.CodeNotImplemented, "prompts not configured"))
		return
	}
	if _, err := h.requireWorkspace(r, r.PathValue("wid")); err != nil {
		writeError(w, r, err)
		return
	}
	var in PromptSourceInput
	if err := decodeBody(r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	dto, err := h.deps.Prompts.CreateSource(r.Context(), r.PathValue("wid"), in)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, dto)
}

func (h *handlers) syncPromptSource(w http.ResponseWriter, r *http.Request) {
	if h.deps.Prompts == nil {
		writeError(w, r, platform.NewError(501, platform.CodeNotImplemented, "prompts not configured"))
		return
	}
	if _, err := h.requireWorkspace(r, r.PathValue("wid")); err != nil {
		writeError(w, r, err)
		return
	}
	res, err := h.deps.Prompts.SyncSource(r.Context(), r.PathValue("wid"), r.PathValue("sid"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *handlers) searchPrompts(w http.ResponseWriter, r *http.Request) {
	if h.deps.Prompts == nil {
		writeError(w, r, platform.NewError(501, platform.CodeNotImplemented, "prompts not configured"))
		return
	}
	ws := r.URL.Query().Get("workspaceId")
	if _, err := h.requireWorkspace(r, ws); err != nil {
		// 未提供 workspaceId 时允许以「仅搜索」语义继续（提示词库可全局检索）。
		if ws != "" {
			writeError(w, r, err)
			return
		}
	}
	tags := []string{}
	if v := r.URL.Query().Get("tags"); v != "" {
		for _, t := range strings.Split(v, ",") {
			if t = strings.TrimSpace(t); t != "" {
				tags = append(tags, t)
			}
		}
	}
	limit := parseLimit(r.URL.Query().Get("limit"), 20, 200)
	items, cursor, err := h.deps.Prompts.Search(r.Context(), ws, r.URL.Query().Get("q"), tags, limit, r.URL.Query().Get("cursor"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "cursor": cursor})
}
