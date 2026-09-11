package api

import "github.com/context-flow/ic/internal/graph"

// PublicLimits 把边界常量暴露给前端展示（见 05 §3.2：不做静默的经验值）。
// 这些数值的真源是 internal/graph/limits.go 与 docs/design/*，由 make boundaries 校验一致。
func PublicLimits() map[string]any {
	return map[string]any{
		"coord":             map[string]any{"min": graph.CoordMin, "max": graph.CoordMax},
		"nodeSize":          map[string]any{"min": graph.SizeMin, "max": graph.SizeMax},
		"zoom":              map[string]any{"min": graph.ZoomMin, "max": graph.ZoomMax},
		"opsPerBatch":       graph.MaxOpBatch,
		"nodesPerCanvas":    graph.MaxNodesPerCanvas,
		"edgesPerCanvas":    graph.MaxEdgesPerCanvas,
		"promptBytes":       graph.MaxPromptBytes,
		"titleLen":          graph.MaxTitleLen,
		"groupDepth":        graph.MaxGroupDepth,
		"imageCount":        map[string]any{"min": 1, "max": 15, "workbenchDefault": 1, "workbenchMax": 10},
		"videoSeconds":      map[string]any{"min": 4, "max": 30},
		"imageEdgeMax":      4096,
		"imagePixels":       map[string]any{"min": 655360, "max": 8294400},
		"sizeStep":          16,
		"aspectRatioMax":    3,
		"splitRowsMax":      50,
		"splitCellsMax":     200,
		"pageLimit":         map[string]any{"min": 1, "max": 200, "default": 20},
		"uploadChunkBytes":  32 << 20,
		"uploadMaxBytes":    2 << 30,
		"sseMaxLifetimeMin": 30,
		"idleRetryMax":      3,
	}
}
