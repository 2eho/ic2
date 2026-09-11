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
