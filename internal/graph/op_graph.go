package graph

// 本文件是 op 应用过程中用到的**图算法与结构校验**：
// 端口查找、成环检测、分组归属与嵌套深度、坐标/矩形/视口范围。
// 从 op.go 拆出，理由同 op_apply.go：职责独立且被多处复用（Agent 工具、导入器）。

import (
	"math"
)

func findPort(ports []Port, id string) (Port, bool) {
	for _, p := range ports {
		if p.ID == id {
			return p, true
		}
	}
	return Port{}, false
}

// wouldCycle 判断新增 from→to 是否成环。
func wouldCycle(doc *CanvasDocument, from, to string) bool {
	// 从 from 出发沿入边向上走，若可达 to 则新增 to→...→from 会成环。
	seen := map[string]bool{}
	var walk func(string) bool
	walk = func(cur string) bool {
		if cur == to {
			return true
		}
		if seen[cur] {
			return false
		}
		seen[cur] = true
		for _, e := range doc.UpstreamOf(cur) {
			if walk(e.From.NodeID) {
				return true
			}
		}
		// 分组归属也算依赖方向，避免「分组内引用上游分组」的死循环。
		if n, ok := doc.Nodes[cur]; ok && n.ParentID != "" {
			if walk(n.ParentID) {
				return true
			}
		}
		return false
	}
	return walk(from)
}

// checkParent 校验分组归属：父必须存在且是分组，且嵌套深度不超限。
func checkParent(doc *CanvasDocument, nodeID, parentID string) error {
	if parentID == "" {
		return nil
	}
	if nodeID == parentID {
		return NewError(422, CodeInvalidSpec, "node cannot be its own parent")
	}
	p, ok := doc.Nodes[parentID]
	if !ok {
		return NewError(404, CodeNotFound, "parent group not found").WithDetail("id", parentID)
	}
	if p.Type != NodeTypeGroup {
		return NewError(422, CodeInvalidSpec, "parent must be a group node").WithDetail("id", parentID)
	}
	// parentID 是第 1 层分组；其祖先链长度不得使总层数超过 MaxGroupDepth。
	// 已用层数 = 1（parentID）+ ancestors，故 ancestors >= MaxGroupDepth 时拒绝。
	depth := 0
	cur := parentID
	for cur != "" {
		if depth >= MaxGroupDepth {
			return NewError(422, CodeInvalidSpec, "group nesting too deep").WithDetail("limit", MaxGroupDepth)
		}
		if cur == nodeID {
			return NewError(422, CodeInvalidSpec, "group cycle detected")
		}
		parent, ok := doc.Nodes[cur]
		if !ok {
			break
		}
		depth++
		cur = parent.ParentID
	}
	return nil
}

func checkCoord(v float64) error {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		// ATK-01：NaN/Inf 一律 422 invalid_geometry。
		return NewError(422, CodeInvalidGeometry, "coordinate must be a finite number")
	}
	if v < CoordMin || v > CoordMax {
		return NewError(422, CodeInvalidGeometry, "coordinate out of range").
			WithDetail("min", CoordMin).WithDetail("max", CoordMax).WithDetail("got", v)
	}
	return nil
}

func validateRect(r Rect) error {
	for _, v := range []float64{r.X, r.Y, r.W, r.H} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return NewError(422, CodeInvalidGeometry, "rect has non-finite value")
		}
	}
	if r.X < CoordMin || r.X > CoordMax || r.Y < CoordMin || r.Y > CoordMax {
		return NewError(422, CodeInvalidGeometry, "rect origin out of range").
			WithDetail("min", CoordMin).WithDetail("max", CoordMax)
	}
	if r.W < SizeMin || r.W > SizeMax || r.H < SizeMin || r.H > SizeMax {
		return NewError(422, CodeInvalidGeometry, "rect size out of range").
			WithDetail("min", SizeMin).WithDetail("max", SizeMax).
			WithDetail("w", r.W).WithDetail("h", r.H)
	}
	return nil
}

// ValidateViewport 校验视口（缩放范围与原项目一致 0.05–5）。
func ValidateViewport(v Viewport) error {
	for _, x := range []float64{v.X, v.Y, v.K} {
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return NewError(422, CodeInvalidGeometry, "viewport contains non-finite value")
		}
	}
	if v.X < CoordMin || v.X > CoordMax || v.Y < CoordMin || v.Y > CoordMax {
		return NewError(422, CodeInvalidGeometry, "viewport origin out of range").
			WithDetail("min", CoordMin).WithDetail("max", CoordMax)
	}
	if v.K < ZoomMin || v.K > ZoomMax {
		return NewError(422, CodeInvalidGeometry, "zoom out of range").
			WithDetail("min", ZoomMin).WithDetail("max", ZoomMax).WithDetail("got", v.K)
	}
	return nil
}
