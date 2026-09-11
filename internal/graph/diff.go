package graph

import (
	"fmt"
	"strings"
)

// DiffDocuments 比较两份文档，返回人类可读差异（空串表示完全一致）。
// 用于 INV-1 校验与 golden 测试。
func DiffDocuments(a, b *CanvasDocument) string {
	var sb []string
	if a.Version != b.Version {
		sb = append(sb, fmt.Sprintf("version: %d != %d", a.Version, b.Version))
	}
	if a.Viewport != b.Viewport {
		sb = append(sb, fmt.Sprintf("viewport: %+v != %+v", a.Viewport, b.Viewport))
	}
	if a.Settings != b.Settings {
		sb = append(sb, fmt.Sprintf("settings: %+v != %+v", a.Settings, b.Settings))
	}
	if len(a.Nodes) != len(b.Nodes) {
		sb = append(sb, fmt.Sprintf("node count: %d != %d", len(a.Nodes), len(b.Nodes)))
	}
	for _, id := range a.NodeIDs() {
		na := a.Nodes[id]
		nb, ok2 := b.Nodes[id]
		if !ok2 {
			sb = append(sb, "missing node in replay: "+id)
			continue
		}
		if na.Type != nb.Type || na.Rect != nb.Rect || na.Title != nb.Title || na.ParentID != nb.ParentID || na.State != nb.State {
			sb = append(sb, fmt.Sprintf("node %s: %+v != %+v", id, na, nb))
		}
		if specFingerprint(na.Spec) != specFingerprint(nb.Spec) {
			sb = append(sb, fmt.Sprintf("node %s spec differs: %s != %s", id, specFingerprint(na.Spec), specFingerprint(nb.Spec)))
		}
	}
	for _, id := range b.NodeIDs() {
		if _, ok := a.Nodes[id]; !ok {
			sb = append(sb, "extra node in replay: "+id)
		}
	}
	if len(a.Edges) != len(b.Edges) {
		sb = append(sb, fmt.Sprintf("edge count: %d != %d", len(a.Edges), len(b.Edges)))
	}
	for _, id := range a.EdgeIDs() {
		ea := a.Edges[id]
		eb, ok2 := b.Edges[id]
		if !ok2 {
			sb = append(sb, "missing edge in replay: "+id)
			continue
		}
		if ea.From != eb.From || ea.To != eb.To || ea.Kind != eb.Kind {
			sb = append(sb, fmt.Sprintf("edge %s: %+v != %+v", id, ea, eb))
		}
	}
	if len(sb) == 0 {
		return ""
	}
	return strings.Join(sb, "; ")
}

func specFingerprint(s NodeSpec) string {
	keys := make([]string, 0, len(s))
	for k := range s {
		keys = append(keys, k)
	}
	sortStrings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", k, s[k]))
	}
	return strings.Join(parts, ",")
}
