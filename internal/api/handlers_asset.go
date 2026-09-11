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

// updateAsset 更新资产的显示元数据（标题/标签/备注）。
//
// 不允许改 hash/size/mime/kind：它们是内容寻址的事实，改了会破坏
// 「同一 hash 只存一份」这条不变量，导致缩略图与下载内容错乱。
func (h *handlers) updateAsset(w http.ResponseWriter, r *http.Request) {
	updater, ok := h.deps.Assets.(AssetMetaUpdater)
	if !ok {
		writeError(w, r, platform.NewError(501, platform.CodeNotImplemented, "asset meta update not supported"))
		return
	}
	p, err := h.requireWorkspace(r, r.URL.Query().Get("workspaceId"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	if err := h.requireAction(r, p, ActWriteContent); err != nil {
		writeError(w, r, err)
		return
	}
	var in struct {
		Name       *string        `json:"name,omitempty"`
		Meta       map[string]any `json:"meta,omitempty"`
		MetaDelete []string       `json:"metaDelete,omitempty"`
	}
	if err := decodeBody(r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	// 显式区分「不修改」与「改成空」：name 为 null/缺省时不改，
	// 传空串才是「清空标题」（会被 sanitize 成占位名）。
	dto, err := updater.UpdateMeta(r.Context(), p.WorkspaceID, r.PathValue("aid"), AssetMetaPatch{
		Name: in.Name, Meta: in.Meta, MetaDelete: in.MetaDelete,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, dto)
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
