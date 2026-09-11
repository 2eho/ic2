package api

import (
	"encoding/json"
	"net/http"

	"github.com/context-flow/ic/internal/graph"
	"github.com/context-flow/ic/internal/legacy"
	"github.com/context-flow/ic/internal/platform"
)

// importLegacy 处理旧版（IndexedDB 导出）画布导入。
//
// 幂等与核对要求（docs/design/12 §6.3）：
//   - 用 sourceProjectId + 内容 hash 去重；
//   - 单个 Blob 缺失不阻断整体；
//   - 导入前后节点数必须一致，否则报告而不是静默成功。
func (h *handlers) importLegacy(w http.ResponseWriter, r *http.Request, raw json.RawMessage, name, actor string) {
	if h.deps.Graph == nil {
		writeError(w, r, platform.NewError(501, platform.CodeNotImplemented, "graph not configured"))
		return
	}
	var project legacy.LegacyProject
	if err := json.Unmarshal(raw, &project); err != nil {
		writeError(w, r, platform.ErrInvalid("legacy project is not valid json"))
		return
	}
	projectID := r.PathValue("cid")
	if projectID == "" {
		projectID = "legacy"
	}

	// 资产解析：客户端在迁移前已把 Blob 上传，这里按 storageKey → assetId 的映射解析。
	assets := h.assetsForRequest(r)
	resolve := func(storageKey, inline string) (string, bool) {
		if inline == "" {
			return "", false
		}
		if id, ok := assets[storageKey]; ok {
			return id, true
		}
		if id, ok := assets[inline]; ok {
			return id, true
		}
		return "", false
	}

	// 先探测是否已导入（幂等）
	contentHash := hashLegacyProject(project)
	if existing, err := h.findImportedCanvas(r, project.ID, contentHash); err == nil && existing != "" {
		writeJSON(w, http.StatusOK, map[string]any{
			"canvasId": existing, "deduplicated": true,
			"sourceProjectId": project.ID, "contentHash": contentHash,
		})
		return
	}

	displayName := firstNonEmpty(name, project.Name)
	if displayName == "" {
		displayName = "导入画布"
	}
	// 命名带 sourceId + hash 前缀：既便于用户辨识，也作为幂等去重键（不引入新表）。
	dedupName := "legacy:" + project.ID + ":" + firstN(contentHash, 12)
	meta, err := h.deps.Graph.Create(r.Context(), projectID, dedupName)
	if err != nil {
		writeError(w, r, err)
		return
	}
	doc, migration, err := legacy.Migrate(project, resolve, meta.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}

	ops := documentToOps(doc)
	base, err := h.deps.Graph.Get(r.Context(), meta.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	res, finalDoc, err := h.deps.Graph.AppendOps(r.Context(), meta.ID, base.Version, ops, actor)
	if err != nil {
		writeError(w, r, err)
		return
	}

	// 数量核对：不一致必须报告（不能静默成功）
	body := map[string]any{
		"canvasId":        meta.ID,
		"version":         res.Version,
		"nodes":           len(finalDoc.Nodes),
		"edges":           len(finalDoc.Edges),
		"expectedNodes":   migration.NodeCount,
		"expectedEdges":   migration.EdgeCount,
		"skippedNodes":    migration.SkippedNodes,
		"missingAssets":   migration.MissingAssets,
		"warnings":        migration.Warnings,
		"sourceProjectId": project.ID,
		"contentHash":     contentHash,
		"name":            displayName,
	}
	if len(finalDoc.Nodes) != migration.NodeCount || len(finalDoc.Edges) != migration.EdgeCount {
		body["warning"] = "imported counts differ from expected"
	}
	writeJSON(w, http.StatusCreated, body)
}

// assetsForRequest 从请求头读取 storageKey → assetId 的映射。
// 用请求头而不是 body，是为了让 body 保持与旧导出格式一致，便于用户直接丢文件。
func (h *handlers) assetsForRequest(r *http.Request) map[string]string {
	out := map[string]string{}
	raw := r.Header.Get("X-IC-Asset-Map")
	if raw == "" {
		return out
	}
	_ = json.Unmarshal([]byte(raw), &out)
	return out
}

func hashLegacyProject(p legacy.LegacyProject) string {
	// 复用 legacy 包的内容 hash（保持与迁移结果一致）
	_, res, err := legacy.Migrate(p, nil, "probe")
	if err != nil {
		return ""
	}
	return res.ContentHash
}

// findImportedCanvas 查是否已有同源同内容的画布（幂等去重）。
func (h *handlers) findImportedCanvas(r *http.Request, sourceID, contentHash string) (string, error) {
	items, err := h.deps.Graph.List(r.Context(), r.PathValue("cid"))
	if err != nil {
		return "", err
	}
	for _, item := range items {
		// 命名约定：导入画布名带上内容 hash 前缀，便于去重且不引入新表
		if item.Name == "legacy:"+sourceID+":"+contentHash[:12] {
			return item.ID, nil
		}
	}
	return "", nil
}

// documentToOps 把文档展开为 op 列表（导入统一走 op 校验路径）。
func documentToOps(doc *graph.CanvasDocument) []json.RawMessage {
	ops := make([]json.RawMessage, 0, len(doc.Nodes)+len(doc.Edges)+1)
	for _, id := range doc.NodeIDs() {
		n := doc.Nodes[id]
		ops = append(ops, mustRaw(map[string]any{
			"kind": "add_node",
			"node": map[string]any{
				"id": n.ID, "type": n.Type, "title": n.Title,
				"rect":     map[string]any{"x": n.Rect.X, "y": n.Rect.Y, "w": n.Rect.W, "h": n.Rect.H},
				"parentId": n.ParentID,
				"spec":     n.Spec,
			},
		}))
	}
	for _, id := range doc.EdgeIDs() {
		e := doc.Edges[id]
		ops = append(ops, mustRaw(map[string]any{
			"kind": "add_edge",
			"edge": map[string]any{
				"id":   e.ID,
				"from": map[string]any{"nodeId": e.From.NodeID, "portId": e.From.PortID},
				"to":   map[string]any{"nodeId": e.To.NodeID, "portId": e.To.PortID},
				"kind": string(e.Kind),
			},
		}))
	}
	if doc.Settings != graph.DefaultSettings() {
		ops = append(ops, mustRaw(map[string]any{
			"kind": "set_settings",
			"settings": map[string]any{
				"background": doc.Settings.Background,
				"imageInfo":  doc.Settings.ImageInfo,
			},
		}))
	}
	return ops
}

func firstN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
