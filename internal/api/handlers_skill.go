package api

import (
	"net/http"

	"github.com/context-flow/ic/internal/platform"
)

// Agent Skills 的 HTTP 面（对齐 docs/design/10 §9.14）。
//
// 权限：Skill 会进系统提示词、影响 Agent 行为，因此归入 ActManageConfig
// （与渠道/凭据同级）而不是 ActWriteContent——editor 不该改别人看到的模型指令。

func (h *handlers) listSkills(w http.ResponseWriter, r *http.Request) {
	if h.deps.Skills == nil {
		writeError(w, r, platform.NewError(501, platform.CodeNotImplemented, "skills not configured"))
		return
	}
	p, err := h.requireWorkspace(r, r.PathValue("wid"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	items, err := h.deps.Skills.ListSkills(r.Context(), p.WorkspaceID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *handlers) saveSkill(w http.ResponseWriter, r *http.Request) {
	if h.deps.Skills == nil {
		writeError(w, r, platform.NewError(501, platform.CodeNotImplemented, "skills not configured"))
		return
	}
	p, err := h.requireWorkspace(r, r.PathValue("wid"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	if err := h.requireAction(r, p, ActManageConfig); err != nil {
		writeError(w, r, err)
		return
	}
	var in map[string]any
	if err := decodeBody(r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	saved, err := h.deps.Skills.UpsertSkill(r.Context(), p.WorkspaceID, in)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, saved)
}

func (h *handlers) deleteSkill(w http.ResponseWriter, r *http.Request) {
	if h.deps.Skills == nil {
		writeError(w, r, platform.NewError(501, platform.CodeNotImplemented, "skills not configured"))
		return
	}
	p, err := h.requireWorkspace(r, r.PathValue("wid"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	if err := h.requireAction(r, p, ActManageConfig); err != nil {
		writeError(w, r, err)
		return
	}
	if err := h.deps.Skills.DeleteSkill(r.Context(), p.WorkspaceID, r.PathValue("name")); err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
