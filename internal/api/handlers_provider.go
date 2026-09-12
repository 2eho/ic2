package api

import (
	"net/http"

	"github.com/context-flow/ic/internal/platform"
)

func (h *handlers) listProviders(w http.ResponseWriter, r *http.Request) {
	if h.deps.Providers == nil {
		writeError(w, r, platform.NewError(501, platform.CodeNotImplemented, "providers not configured"))
		return
	}
	if _, err := h.requireWorkspace(r, r.PathValue("wid")); err != nil {
		writeError(w, r, err)
		return
	}
	items, err := h.deps.Providers.ListProviders(r.Context(), r.PathValue("wid"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *handlers) createProvider(w http.ResponseWriter, r *http.Request) {
	if h.deps.Providers == nil {
		writeError(w, r, platform.NewError(501, platform.CodeNotImplemented, "providers not configured"))
		return
	}
	if _, err := h.requireWorkspace(r, r.PathValue("wid")); err != nil {
		writeError(w, r, err)
		return
	}
	var in ProviderInput
	if err := decodeBody(r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	dto, err := h.deps.Providers.CreateProvider(r.Context(), r.PathValue("wid"), in)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, dto)
}

func (h *handlers) createCredential(w http.ResponseWriter, r *http.Request) {
	if h.deps.Providers == nil {
		writeError(w, r, platform.NewError(501, platform.CodeNotImplemented, "providers not configured"))
		return
	}
	if _, err := h.requireWorkspace(r, r.PathValue("wid")); err != nil {
		writeError(w, r, err)
		return
	}
	var in CredentialInput
	if err := decodeBody(r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	dto, err := h.deps.Providers.CreateCredential(r.Context(), r.PathValue("wid"), r.PathValue("pid"), in)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, dto)
}

func (h *handlers) testProvider(w http.ResponseWriter, r *http.Request) {
	if h.deps.Providers == nil {
		writeError(w, r, platform.NewError(501, platform.CodeNotImplemented, "providers not configured"))
		return
	}
	if _, err := h.requireWorkspace(r, r.PathValue("wid")); err != nil {
		writeError(w, r, err)
		return
	}
	res, err := h.deps.Providers.TestProvider(r.Context(), r.PathValue("wid"), r.PathValue("pid"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *handlers) listModels(w http.ResponseWriter, r *http.Request) {
	if h.deps.Providers == nil {
		writeError(w, r, platform.NewError(501, platform.CodeNotImplemented, "providers not configured"))
		return
	}
	if _, err := h.requireWorkspace(r, r.PathValue("wid")); err != nil {
		writeError(w, r, err)
		return
	}
	items, err := h.deps.Providers.ListModels(r.Context(), r.PathValue("wid"), r.URL.Query().Get("capability"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// saveModels 保存渠道下勾选的模型与能力。
//
// 为什么要把能力**显式存下来**而不是每次按关键词重猜：
// 关键词表会演进（新模型命名、新能力），重猜会让已保存的配置漂移——
// 用户昨天配好的生图模型，今天可能因为关键词表改动而变成文本模型。
func (h *handlers) saveModels(w http.ResponseWriter, r *http.Request) {
	saver, ok := h.deps.Providers.(ModelSaver)
	if !ok {
		writeError(w, r, platform.NewError(501, platform.CodeNotImplemented, "model save not supported"))
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
	var in struct {
		Models []struct {
			ID           string   `json:"id"`
			DisplayName  string   `json:"displayName,omitempty"`
			Capabilities []string `json:"capabilities"`
		} `json:"models"`
	}
	if err := decodeBody(r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	if len(in.Models) > 2000 {
		writeError(w, r, platform.NewError(422, platform.CodeInvalidRequest, "模型数量超出上限").
			WithDetail("limit", 2000).WithDetail("got", len(in.Models)))
		return
	}
	dtos := make([]ModelDTO, 0, len(in.Models))
	for _, m := range in.Models {
		dtos = append(dtos, ModelDTO{
			ID: m.ID, ProviderID: r.PathValue("pid"),
			DisplayName: m.DisplayName, Capabilities: m.Capabilities,
		})
	}
	items, err := saver.SaveModels(r.Context(), p.WorkspaceID, r.PathValue("pid"), dtos)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
