package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/context-flow/ic/internal/platform"
)

func (h *handlers) uploadAsset(w http.ResponseWriter, r *http.Request) {
	if h.deps.Assets == nil {
		writeError(w, r, platform.NewError(501, platform.CodeNotImplemented, "assets not configured"))
		return
	}
	if _, err := h.requireWorkspace(r, r.PathValue("wid")); err != nil {
		writeError(w, r, err)
		return
	}
	// 简单上传：multipart 单文件。分片上传走同一 endpoint 的 ?uploadId 续传语义（M2 完善）。
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeError(w, r, platform.NewError(400, platform.CodeInvalidRequest, "parse multipart failed"))
		return
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		writeError(w, r, platform.NewError(400, platform.CodeInvalidRequest, "file field is required"))
		return
	}
	defer file.Close()
	mime := hdr.Header.Get("Content-Type")
	if mime == "" {
		mime = "application/octet-stream"
	}
	dto, err := h.deps.Assets.Upload(r.Context(), r.PathValue("wid"), hdr.Filename, mime, file, hdr.Size)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, dto)
}

func (h *handlers) getAsset(w http.ResponseWriter, r *http.Request) {
	if h.deps.Assets == nil {
		writeError(w, r, platform.NewError(501, platform.CodeNotImplemented, "assets not configured"))
		return
	}
	ws := r.URL.Query().Get("workspaceId")
	dto, err := h.deps.Assets.Get(r.Context(), ws, r.PathValue("aid"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, dto)
}

func (h *handlers) getAssetRaw(w http.ResponseWriter, r *http.Request) {
	if h.deps.Assets == nil {
		writeError(w, r, platform.NewError(501, platform.CodeNotImplemented, "assets not configured"))
		return
	}
	ws := r.URL.Query().Get("workspaceId")
	rc, size, mime, err := h.deps.Assets.Open(r.Context(), ws, r.PathValue("aid"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	defer rc.Close()
	w.Header().Set("Content-Type", mime)
	w.Header().Set("ETag", `"`+r.PathValue("aid")+`"`)
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("Accept-Ranges", "bytes")
	// Range 支持由 http.ServeContent 处理（资产不可变，可长缓存，见 03 §2.4）。
	http.ServeContent(w, r, r.PathValue("aid"), time.Time{}, rc)
	_ = size
}

func (h *handlers) getAssetThumb(w http.ResponseWriter, r *http.Request) {
	if h.deps.Assets == nil {
		writeError(w, r, platform.NewError(501, platform.CodeNotImplemented, "assets not configured"))
		return
	}
	ws := r.URL.Query().Get("workspaceId")
	width := 0
	if v := r.URL.Query().Get("w"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			width = n
		}
	}
	data, mime, err := h.deps.Assets.Thumb(r.Context(), ws, r.PathValue("aid"), width)
	if err != nil {
		writeError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Cache-Control", "public, max-age=604800")
	_, _ = w.Write(data)
}

func (h *handlers) deleteAsset(w http.ResponseWriter, r *http.Request) {
	if h.deps.Assets == nil {
		writeError(w, r, platform.NewError(501, platform.CodeNotImplemented, "assets not configured"))
		return
	}
	ws := r.URL.Query().Get("workspaceId")
	if err := h.deps.Assets.Delete(r.Context(), ws, r.PathValue("aid")); err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *handlers) listAssets(w http.ResponseWriter, r *http.Request) {
	if h.deps.Assets == nil {
		writeError(w, r, platform.NewError(501, platform.CodeNotImplemented, "assets not configured"))
		return
	}
	if _, err := h.requireWorkspace(r, r.PathValue("wid")); err != nil {
		writeError(w, r, err)
		return
	}
	limit := parseLimit(r.URL.Query().Get("limit"), 20, 200)
	items, cursor, err := h.deps.Assets.List(r.Context(), r.PathValue("wid"),
		strings.TrimSpace(r.URL.Query().Get("kind")), limit, r.URL.Query().Get("cursor"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "cursor": cursor})
}
