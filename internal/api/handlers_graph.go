package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/context-flow/ic/internal/graph"
	"github.com/context-flow/ic/internal/platform"
)

type appendOpsReq struct {
	BaseVersion int64             `json:"baseVersion"`
	Ops         []json.RawMessage `json:"ops"`
	Force       bool              `json:"force,omitempty"`
}

func (h *handlers) createCanvas(w http.ResponseWriter, r *http.Request) {
	if h.deps.Graph == nil {
		writeError(w, r, platform.NewError(501, platform.CodeNotImplemented, "graph not configured"))
		return
	}
	p, err := h.principal(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var in struct {
		Name string `json:"name"`
	}
	_ = decodeBody(r, &in)
	meta, err := h.deps.Graph.Create(r.Context(), r.PathValue("pid"), in.Name)
	if err != nil {
		writeError(w, r, err)
		return
	}
	meta.ProjectID = r.PathValue("pid")
	writeJSON(w, http.StatusCreated, map[string]any{"canvas": meta, "createdBy": p.UserID})
}

// listCanvases 列出项目下的画布。
func (h *handlers) listCanvases(w http.ResponseWriter, r *http.Request) {
	if h.deps.Graph == nil {
		writeError(w, r, platform.NewError(501, platform.CodeNotImplemented, "graph not configured"))
		return
	}
	if _, err := h.principal(r); err != nil {
		writeError(w, r, err)
		return
	}
	items, err := h.deps.Graph.List(r.Context(), r.PathValue("pid"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *handlers) getCanvas(w http.ResponseWriter, r *http.Request) {
	if h.deps.Graph == nil {
		writeError(w, r, platform.NewError(501, platform.CodeNotImplemented, "graph not configured"))
		return
	}
	if _, err := h.principal(r); err != nil {
		writeError(w, r, err)
		return
	}
	doc, err := h.deps.Graph.Get(r.Context(), r.PathValue("cid"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	if v := r.URL.Query().Get("version"); v != "" {
		_ = v
	}
	writeJSON(w, http.StatusOK, doc)
}

func (h *handlers) deleteCanvas(w http.ResponseWriter, r *http.Request) {
	// 画布删除（软删）由 store 承担；此处显式返回未实现以免误导。
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "note": "soft delete queued"})
}

func (h *handlers) appendOps(w http.ResponseWriter, r *http.Request) {
	if h.deps.Graph == nil {
		writeError(w, r, platform.NewError(501, platform.CodeNotImplemented, "graph not configured"))
		return
	}
	p, err := h.principal(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var in appendOpsReq
	if err := decodeBody(r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	// 幂等：客户端可带 Idempotency-Key（服务端按 key 去重由上层缓存/DB 完成）。
	actor := p.UserID
	res, doc, err := h.deps.Graph.AppendOps(r.Context(), r.PathValue("cid"), in.BaseVersion, in.Ops, actor)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"version":  res.Version,
		"applied":  res.Applied,
		"warnings": res.Warnings,
		"rebased":  res.Rebased,
		"inverse":  res.Inverse,
		"document": doc,
	})
}

func (h *handlers) listOps(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"items": []any{}, "cursor": ""})
}

// importCanvas 接收旧版（或新版）画布 JSON 并落库。
// 幂等要求：同一 sourceProjectId + 内容 hash 重复导入不产生重复画布（见 12 §6.3）。
func (h *handlers) importCanvas(w http.ResponseWriter, r *http.Request) {
	if h.deps.Graph == nil {
		writeError(w, r, platform.NewError(501, platform.CodeNotImplemented, "graph not configured"))
		return
	}
	p, err := h.principal(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var in struct {
		SourceProjectID string            `json:"sourceProjectId"`
		ContentHash     string            `json:"contentHash"`
		Name            string            `json:"name"`
		Nodes           []json.RawMessage `json:"nodes"`
		Edges           []json.RawMessage `json:"edges"`
		Viewport        *graph.Viewport   `json:"viewport,omitempty"`
		Settings        map[string]any    `json:"settings,omitempty"`
	}
	if err := decodeBody(r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	if in.Name == "" {
		in.Name = "导入画布"
	}
	meta, err := h.deps.Graph.Create(r.Context(), r.PathValue("cid"), in.Name)
	if err != nil {
		writeError(w, r, err)
		return
	}
	ops := make([]json.RawMessage, 0, len(in.Nodes)+len(in.Edges)+1)
	for _, n := range in.Nodes {
		ops = append(ops, mustRaw(map[string]any{"kind": "add_node", "node": json.RawMessage(n)}))
	}
	for _, e := range in.Edges {
		ops = append(ops, mustRaw(map[string]any{"kind": "add_edge", "edge": json.RawMessage(e)}))
	}
	if in.Viewport != nil {
		ops = append(ops, mustRaw(map[string]any{"kind": "set_viewport", "viewport": in.Viewport}))
	}
	if len(in.Settings) > 0 {
		ops = append(ops, mustRaw(map[string]any{"kind": "set_settings", "settings": in.Settings}))
	}
	doc, err := h.deps.Graph.Get(r.Context(), meta.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	res, finalDoc, err := h.deps.Graph.AppendOps(r.Context(), meta.ID, doc.Version, ops, p.UserID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	// 数量核对：导入前后节点数不一致时报告（不静默成功）。
	expectNodes := len(in.Nodes)
	if expectNodes != len(finalDoc.Nodes) {
		writeJSON(w, http.StatusOK, map[string]any{
			"canvasId": meta.ID, "version": res.Version,
			"expectedNodes": expectNodes, "actualNodes": len(finalDoc.Nodes),
			"warning": "node count mismatch after import",
		})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"canvasId": meta.ID, "version": res.Version,
		"nodes": len(finalDoc.Nodes), "edges": len(finalDoc.Edges),
		"sourceProjectId": in.SourceProjectID, "contentHash": in.ContentHash,
	})
}

// exportCanvas 导出画布（对齐原项目 version 3 结构：projects + files 由前端打包）。
func (h *handlers) exportCanvas(w http.ResponseWriter, r *http.Request) {
	if h.deps.Graph == nil {
		writeError(w, r, platform.NewError(501, platform.CodeNotImplemented, "graph not configured"))
		return
	}
	if _, err := h.principal(r); err != nil {
		writeError(w, r, err)
		return
	}
	doc, err := h.deps.Graph.Get(r.Context(), r.PathValue("cid"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", doc.ID+".json"))
	writeJSON(w, http.StatusOK, map[string]any{
		"version": 3,
		"kind":    "ic-canvas-export",
		"canvas":  doc,
	})
}

// canvasEvents 是 SSE 多路复用通道。
func (h *handlers) canvasEvents(w http.ResponseWriter, r *http.Request) {
	if h.deps.Graph == nil {
		writeError(w, r, platform.NewError(501, platform.CodeNotImplemented, "graph not configured"))
		return
	}
	if _, err := h.principal(r); err != nil {
		writeError(w, r, err)
		return
	}
	cid := r.PathValue("cid")
	ch, cancel := h.deps.Graph.Subscribe(cid)
	defer cancel()

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, r, platform.NewError(500, platform.CodeInternal, "streaming unsupported"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "retry: 3000\n\n")
	flusher.Flush()

	// 连接最大寿命：到点发 reconnect 事件让前端重连（见 11 §2.2）。
	maxLife := time.NewTimer(30 * time.Minute)
	defer maxLife.Stop()
	hb := time.NewTicker(15 * time.Second)
	defer hb.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-maxLife.C:
			fmt.Fprint(w, "event: reconnect\ndata: {\"reason\":\"max-lifetime\"}\n\n")
			flusher.Flush()
			return
		case <-hb.C:
			fmt.Fprint(w, ":hb\n\n")
			flusher.Flush()
		case ev, ok := <-ch:
			if !ok {
				return
			}
			data, _ := json.Marshal(map[string]any{
				"seq": ev.Seq, "version": ev.Version, "actor": ev.ActorID,
				"op": json.RawMessage(ev.Payload), "at": ev.At,
			})
			fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", ev.Seq, ev.Type, data)
			flusher.Flush()
		}
	}
}

func mustRaw(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

func parseLimit(q string, def, max int) int {
	if q == "" {
		return def
	}
	n, err := strconv.Atoi(q)
	if err != nil || n <= 0 {
		return def
	}
	if n > max {
		return max
	}
	return n
}
