package graph

import "math"

// Point 是画布平面上的一个点。
type Point struct{ X, Y float64 }

// 自动落位参数。
//
// 为什么需要它：Agent 与插件会批量建节点，如果都从原点开始放，
// 节点会完全重叠——用户看到的是「只多了一个节点」，其余藏在下面。
// 原项目用「40px 错位」缓解，但错位叠加后仍然重叠，且与已有节点无关。
const (
	// PlaceNodeW / PlaceNodeH 是默认占位尺寸（与内置节点默认尺寸一致）。
	PlaceNodeW = 320.0
	PlaceNodeH = 260.0
	// PlaceGap 是节点之间的间隙。
	PlaceGap = 40.0
	// PlaceMaxCols 是单行最多放几个，超过换行。
	PlaceMaxCols = 5
	// PlaceSearchRadius 是以给定原点为中心的搜索半径（格数），防止无限循环。
	PlaceSearchRadius = 200
)

// PlaceNewNodes 在文档中为 n 个新节点找不重叠的位置。
//
// 算法：从 origin 开始按网格向右扩展，若某格与已有节点矩形相交则跳到下一格。
// 复杂度 O(n * 已有节点数)，在「一次最多 20 个新节点」的场景下完全够用，
// 且比空间索引更可预测（不会因为索引实现差异产生不同布局，便于测试断言）。
func PlaceNewNodes(doc *CanvasDocument, n int, origin Point) []Point {
	if n <= 0 {
		return nil
	}
	occupied := make([]Rect, 0, len(doc.Nodes))
	for _, id := range doc.NodeIDs() {
		occupied = append(occupied, doc.Nodes[id].Rect)
	}
	if origin.X == 0 && origin.Y == 0 {
		// 未指定原点时放在已有内容的右侧，避免覆盖用户正在看的内容。
		origin = Point{X: boundingRight(occupied) + PlaceGap, Y: boundingTop(occupied)}
	}

	out := make([]Point, 0, n)
	col, row := 0, 0
	for len(out) < n {
		candidate := Rect{
			X: origin.X + float64(col)*(PlaceNodeW+PlaceGap),
			Y: origin.Y + float64(row)*(PlaceNodeH+PlaceGap),
			W: PlaceNodeW, H: PlaceNodeH,
		}
		if !intersectsAny(candidate, occupied) {
			out = append(out, Point{X: candidate.X, Y: candidate.Y})
			occupied = append(occupied, candidate)
		}
		col++
		if col >= PlaceMaxCols {
			col = 0
			row++
		}
		if row > PlaceSearchRadius {
			// 极端情况（画布已密不透风）：退化为按行继续排，
			// 宁可重叠也不要死循环——返回可用的坐标比「卡住」有价值。
			for len(out) < n {
				out = append(out, Point{
					X: origin.X + float64(len(out)%PlaceMaxCols)*(PlaceNodeW+PlaceGap),
					Y: origin.Y + (float64(row)+float64(len(out)/PlaceMaxCols))*(PlaceNodeH+PlaceGap),
				})
			}
		}
	}
	return out
}

func intersectsAny(r Rect, list []Rect) bool {
	for _, o := range list {
		if rectsIntersect(r, o) {
			return true
		}
	}
	return false
}

func rectsIntersect(a, b Rect) bool {
	// 相邻不算相交（留 1px 容差，避免浮点误差把「恰好贴边」判成重叠）。
	const eps = 1.0
	return a.X < b.X+b.W-eps && b.X < a.X+a.W-eps && a.Y < b.Y+b.H-eps && b.Y < a.Y+a.H-eps
}

func boundingRight(rects []Rect) float64 {
	if len(rects) == 0 {
		return 0
	}
	maxRight := math.Inf(-1)
	for _, r := range rects {
		if v := r.X + r.W; v > maxRight {
			maxRight = v
		}
	}
	if math.IsInf(maxRight, -1) {
		return 0
	}
	return maxRight
}

func boundingTop(rects []Rect) float64 {
	if len(rects) == 0 {
		return 0
	}
	minTop := math.Inf(1)
	for _, r := range rects {
		if r.Y < minTop {
			minTop = r.Y
		}
	}
	if math.IsInf(minTop, 1) {
		return 0
	}
	return minTop
}
