package api

import (
	"net/http"

	"github.com/context-flow/ic/internal/platform"
)

func (h *handlers) listPlugins(w http.ResponseWriter, r *http.Request) {
	if h.deps.Plugins == nil {
		writeError(w, r, platform.NewError(501, platform.CodeNotImplemented, "plugins not configured"))
		return
	}
	if _, err := h.requireWorkspace(r, r.PathValue("wid")); err != nil {
		writeError(w, r, err)
		return
	}
	items, err := h.deps.Plugins.List(r.Context(), r.PathValue("wid"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *handlers) installPlugin(w http.ResponseWriter, r *http.Request) {
	if h.deps.Plugins == nil {
		writeError(w, r, platform.NewError(501, platform.CodeNotImplemented, "plugins not configured"))
		return
	}
	if _, err := h.requireWorkspace(r, r.PathValue("wid")); err != nil {
		writeError(w, r, err)
		return
	}
	var in PluginInstallInput
	if err := decodeBody(r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	dto, err := h.deps.Plugins.Install(r.Context(), r.PathValue("wid"), in)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, dto)
}

func (h *handlers) enablePlugin(w http.ResponseWriter, r *http.Request) {
	if h.deps.Plugins == nil {
		writeError(w, r, platform.NewError(501, platform.CodeNotImplemented, "plugins not configured"))
		return
	}
	if _, err := h.requireWorkspace(r, r.PathValue("wid")); err != nil {
		writeError(w, r, err)
		return
	}
	var in struct {
		Enabled bool `json:"enabled"`
		Trusted bool `json:"trusted,omitempty"`
	}
	if err := decodeBody(r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	dto, err := h.deps.Plugins.Enable(r.Context(), r.PathValue("wid"), r.PathValue("key"), in.Enabled)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, dto)
}

func (h *handlers) pluginBundle(w http.ResponseWriter, r *http.Request) {
	if h.deps.Plugins == nil {
		writeError(w, r, platform.NewError(501, platform.CodeNotImplemented, "plugins not configured"))
		return
	}
	data, etag, err := h.deps.Plugins.Bundle(r.Context(), r.PathValue("key"), r.PathValue("version"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "public, max-age=300, must-revalidate")
	_, _ = w.Write(data)
}

func (h *handlers) pluginRegistry(w http.ResponseWriter, r *http.Request) {
	if h.deps.Plugins == nil {
		writeError(w, r, platform.NewError(501, platform.CodeNotImplemented, "plugins not configured"))
		return
	}
	reg, err := h.deps.Plugins.Registry(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, reg)
}
