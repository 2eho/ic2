import {
  alignOffsets,
  distributeOffsets,
  layerRanks,
  layeredColumnOffsets,
  type AlignAnchor,
  type AlignMode,
  type AlignRect,
  type DistributeAxis,
  type LayeredLayoutOptions,
} from "./geometry";

/**
 * 布局意图 → 位移的纯计算层。
 *
 * 内核（`CanvasKernel`）只负责「拿场景数据 → 调这里算位移 → dispatch」，
 * 把「分级/分层怎么算」的规则集中在本文件，便于单测与复用。
 * 不 import react、不碰 DOM、不持有状态。
 */

/** 连线的最小形态：只关心两端节点 id，避免与本模块耦合 RawEdge。 */
export interface EdgeLike {
  from: string;
  to: string;
}

/** 对齐位移；少于 2 个目标返回空（没有「对齐」可言）。 */
export function alignRects(
  rects: AlignRect[],
  mode: AlignMode,
  anchor: AlignAnchor = "union",
): { id: string; dx: number; dy: number }[] {
  if (rects.length < 2) return [];
  return alignOffsets(rects, mode, anchor).filter(
    (o) => o.dx !== 0 || o.dy !== 0,
  );
}

/** 等距分布位移；少于 3 个目标返回空（没有「间距」可言）。 */
export function distributeRects(
  rects: AlignRect[],
  axis: DistributeAxis,
): { id: string; dx: number; dy: number }[] {
  if (rects.length < 3) return [];
  return distributeOffsets(rects, axis).filter((o) => o.dx !== 0 || o.dy !== 0);
}

/**
 * 只保留两端都在给定集合内的边。
 *
 * 分区内的分层不应该被区外节点改写层号：例如选区里两个节点看似独立，
 * 但它们之间有一条经区外节点绕行的边时，不应据此把它们排到不同列。
 */
export function edgesWithin(
  edges: EdgeLike[],
  ids: Iterable<string>,
): EdgeLike[] {
  const set = new Set(ids);
  return edges.filter((e) => set.has(e.from) && set.has(e.to));
}

/** 分层成列位移；少于 2 个目标返回空。 */
export function layerLayoutRects(
  rects: AlignRect[],
  edges: EdgeLike[],
  opts: LayeredLayoutOptions = {},
): { id: string; dx: number; dy: number }[] {
  if (rects.length < 2) return [];
  const ranks = layerRanks(
    rects.map((r) => r.id),
    edgesWithin(
      edges,
      rects.map((r) => r.id),
    ),
  );
  return layeredColumnOffsets(rects, ranks, opts).filter(
    (o) => o.dx !== 0 || o.dy !== 0,
  );
}

/** 是否已经按层成列（已排好 → UI 置灰、内核不产生 op）。 */
export function isLayerLayoutSettled(
  rects: AlignRect[],
  edges: EdgeLike[],
  opts: LayeredLayoutOptions = {},
): boolean {
  if (rects.length < 2) return true;
  return layerLayoutRects(rects, edges, opts).length === 0;
}
