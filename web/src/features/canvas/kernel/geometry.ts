import type { Rect, Vec2 } from "./types";

export function rectsIntersect(a: Rect, b: Rect): boolean {
  return !(
    a.x + a.w < b.x ||
    b.x + b.w < a.x ||
    a.y + a.h < b.y ||
    b.y + b.h < a.y
  );
}

export function rectContains(outer: Rect, inner: Rect): boolean {
  return (
    inner.x >= outer.x &&
    inner.y >= outer.y &&
    inner.x + inner.w <= outer.x + outer.w &&
    inner.y + inner.h <= outer.y + outer.h
  );
}

export function pointInRect(p: Vec2, r: Rect): boolean {
  return p.x >= r.x && p.x <= r.x + r.w && p.y >= r.y && p.y <= r.y + r.h;
}

/** 归一化矩形（拖拽中可能出现负宽高） */
export function normalizeRect(a: Vec2, b: Vec2): Rect {
  const x = Math.min(a.x, b.x);
  const y = Math.min(a.y, b.y);
  return { x, y, w: Math.abs(b.x - a.x), h: Math.abs(b.y - a.y) };
}

/** 合并多个矩形 */
export function unionRects(rects: Rect[]): Rect | null {
  if (rects.length === 0) return null;
  let minX = Infinity;
  let minY = Infinity;
  let maxX = -Infinity;
  let maxY = -Infinity;
  for (const r of rects) {
    minX = Math.min(minX, r.x);
    minY = Math.min(minY, r.y);
    maxX = Math.max(maxX, r.x + r.w);
    maxY = Math.max(maxY, r.y + r.h);
  }
  return { x: minX, y: minY, w: maxX - minX, h: maxY - minY };
}

/** 网格吸附（对齐原项目 16px 步长） */
export function snapToGrid(v: number, step = 16): number {
  if (!Number.isFinite(v) || step <= 0) return v;
  return Math.round(v / step) * step;
}

/** 等比缩放：给定新宽度，按原始宽高比求高度 */
/**
 * 手柄方向解析。
 * 注意：不能用 `handle.includes('n')` 这种写法——'n' 同时出现在 'n'、'nw'、'ne' 里是对的，
 * 但 `includes('e')` 会误匹配 'ne'/'se'（正确）却与 'w' 组合时语义混淆。
 * 这里按字面量显式映射，杜绝歧义。
 */
export function handleDirections(handle: ResizeHandle): {
  n: boolean;
  s: boolean;
  e: boolean;
  w: boolean;
} {
  switch (handle) {
    case "n":
      return { n: true, s: false, e: false, w: false };
    case "s":
      return { n: false, s: true, e: false, w: false };
    case "e":
      return { n: false, s: false, e: true, w: false };
    case "w":
      return { n: false, s: false, e: false, w: true };
    case "nw":
      return { n: true, s: false, e: false, w: true };
    case "ne":
      return { n: true, s: false, e: true, w: false };
    case "sw":
      return { n: false, s: true, e: false, w: true };
    case "se":
      return { n: false, s: true, e: true, w: false };
    default:
      return { n: false, s: false, e: false, w: false };
  }
}

function clamp(v: number, min: number, max: number): number {
  if (!Number.isFinite(v)) return min;
  return Math.min(max, Math.max(min, v));
}

export function keepAspectHeight(rect: Rect, newW: number): number {
  if (rect.w <= 0 || rect.h <= 0) return rect.h;
  return (rect.h * newW) / rect.w;
}

/**
 * 八向缩放手柄命中测试与目标矩形计算。
 * 手柄顺序：nw, n, ne, e, se, s, sw, w（与原项目一致）。
 */
export type ResizeHandle = "nw" | "n" | "ne" | "e" | "se" | "s" | "sw" | "w";

export const RESIZE_HANDLES: ResizeHandle[] = [
  "nw",
  "n",
  "ne",
  "e",
  "se",
  "s",
  "sw",
  "w",
];

const HANDLE_SIZE = 10;

export function hitResizeHandle(
  point: Vec2,
  screenRect: Rect,
  handleSize = HANDLE_SIZE,
): ResizeHandle | null {
  const hs = handleSize;
  const centers: Record<ResizeHandle, Vec2> = {
    nw: { x: screenRect.x, y: screenRect.y },
    n: { x: screenRect.x + screenRect.w / 2, y: screenRect.y },
    ne: { x: screenRect.x + screenRect.w, y: screenRect.y },
    e: { x: screenRect.x + screenRect.w, y: screenRect.y + screenRect.h / 2 },
    se: { x: screenRect.x + screenRect.w, y: screenRect.y + screenRect.h },
    s: { x: screenRect.x + screenRect.w / 2, y: screenRect.y + screenRect.h },
    sw: { x: screenRect.x, y: screenRect.y + screenRect.h },
    w: { x: screenRect.x, y: screenRect.y + screenRect.h / 2 },
  };
  for (const h of RESIZE_HANDLES) {
    const c = centers[h];
    const half = hs / 2;
    if (Math.abs(point.x - c.x) <= half && Math.abs(point.y - c.y) <= half)
      return h;
  }
  return null;
}

/**
 * 根据手柄与位移计算新矩形（世界坐标）。
 * 保持最小尺寸约束，避免出现 0 或负数尺寸（服务端也会拒绝，这里先挡住）。
 */
export function applyResize(
  origin: Rect,
  handle: ResizeHandle,
  worldDelta: { dx: number; dy: number },
  opts: { keepAspect?: boolean; minSize?: number; maxSize?: number } = {},
): Rect {
  const minSize = opts.minSize ?? 16;
  const maxSize = opts.maxSize ?? 20000;
  let { x, y, w, h } = origin;
  const dx = Number.isFinite(worldDelta.dx) ? worldDelta.dx : 0;
  const dy = Number.isFinite(worldDelta.dy) ? worldDelta.dy : 0;

  const dirs = handleDirections(handle);
  if (dirs.w) {
    const nx = Math.min(x + dx, x + w - minSize);
    w = w + (x - nx);
    x = nx;
  }
  if (dirs.e) {
    w = Math.max(minSize, w + dx);
  }
  if (dirs.n) {
    const ny = Math.min(y + dy, y + h - minSize);
    h = h + (y - ny);
    y = ny;
  }
  if (dirs.s) {
    h = Math.max(minSize, h + dy);
  }

  if (opts.keepAspect && origin.w > 0 && origin.h > 0) {
    // 先约束宽度到合法区间，再按原始宽高比反推高度，避免把非法宽度带入比例计算。
    w = clamp(w, minSize, maxSize);
    const ratio = origin.w / origin.h;
    const nh = clamp(w / ratio, minSize, maxSize);
    if (dirs.n) {
      y += h - nh;
    }
    h = nh;
    return { x, y, w, h };
  }

  return { x, y, w: clamp(w, minSize, maxSize), h: clamp(h, minSize, maxSize) };
}

/* ------------------------------------------------------------------------- *
 * 多选对齐与等间距分布（对齐原项目之外的通用画布能力）
 *
 * 全部为纯函数：输入矩形集合，输出「每个 id 需要移动多少」，
 * 由内核转换成 move_node op。不依赖 DOM 测量，便于单测与重放。
 * ------------------------------------------------------------------------- */

export type AlignMode =
  "left" | "hcenter" | "right" | "top" | "vcenter" | "bottom";

export type DistributeAxis = "horizontal" | "vertical";

/** 带 id 的矩形，用于对齐输入（保持顺序，输出一一对应）。 */
export interface AlignRect extends Rect {
  id: string;
}

/**
 * 对齐的参考基准：
 * - `union`（默认）：按选中集合的包围盒对齐（选区对齐）；
 * - `first`：按首元素对齐（常用于「都对齐到某个节点」）。
 */
export type AlignAnchor = "union" | "first";

/**
 * 计算对齐所需的位移。
 *
 * 至少 2 个矩形才有意义；`first` 模式下首元素位移恒为 0（它自己是基准）。
 * 结果保留浮点值，由内核/schema 决定是否取整，这里不做四舍五入，
 * 以免与「16 倍数网格吸附」策略耦合。
 */
export function alignOffsets(
  rects: AlignRect[],
  mode: AlignMode,
  anchor: AlignAnchor = "union",
): { id: string; dx: number; dy: number }[] {
  if (rects.length < 2) return [];
  const first = rects[0];
  let target: number;
  switch (mode) {
    case "left":
    case "right":
    case "hcenter": {
      if (anchor === "first") {
        target =
          mode === "left"
            ? first.x
            : mode === "right"
              ? first.x + first.w
              : first.x + first.w / 2;
      } else {
        const minX = Math.min(...rects.map((r) => r.x));
        const maxX = Math.max(...rects.map((r) => r.x + r.w));
        target =
          mode === "left" ? minX : mode === "right" ? maxX : (minX + maxX) / 2;
      }
      return rects.map((r) => ({
        id: r.id,
        dx:
          mode === "left"
            ? target - r.x
            : mode === "right"
              ? target - (r.x + r.w)
              : target - (r.x + r.w / 2),
        dy: 0,
      }));
    }
    case "top":
    case "bottom":
    case "vcenter": {
      if (anchor === "first") {
        target =
          mode === "top"
            ? first.y
            : mode === "bottom"
              ? first.y + first.h
              : first.y + first.h / 2;
      } else {
        const minY = Math.min(...rects.map((r) => r.y));
        const maxY = Math.max(...rects.map((r) => r.y + r.h));
        target =
          mode === "top" ? minY : mode === "bottom" ? maxY : (minY + maxY) / 2;
      }
      return rects.map((r) => ({
        id: r.id,
        dx: 0,
        dy:
          mode === "top"
            ? target - r.y
            : mode === "bottom"
              ? target - (r.y + r.h)
              : target - (r.y + r.h / 2),
      }));
    }
    default:
      return [];
  }
}

/**
 * 等间距分布：保持两端不动，中间元素按「中心等距」重排。
 *
 * 用中心等距而不是间隙等距：节点尺寸不一时，前者视觉上更整齐，
 * 也是主流画布（Figma / tldraw / libtv）的默认行为。
 * 元素不足 3 个时无可分布，直接返回空。
 */
export function distributeOffsets(
  rects: AlignRect[],
  axis: DistributeAxis,
): { id: string; dx: number; dy: number }[] {
  if (rects.length < 3) return [];
  const horizontal = axis === "horizontal";
  const sorted = [...rects].sort((a, b) =>
    horizontal ? a.x - b.x : a.y - b.y,
  );
  const first = sorted[0];
  const last = sorted[sorted.length - 1];
  const startCenter = horizontal
    ? first.x + first.w / 2
    : first.y + first.h / 2;
  const endCenter = horizontal ? last.x + last.w / 2 : last.y + last.h / 2;
  const step = (endCenter - startCenter) / (sorted.length - 1);

  const moves = new Map<string, number>();
  sorted.forEach((r, i) => {
    if (i === 0 || i === sorted.length - 1) return;
    const targetCenter = startCenter + step * i;
    const currentCenter = horizontal ? r.x + r.w / 2 : r.y + r.h / 2;
    moves.set(r.id, targetCenter - currentCenter);
  });
  return rects.map((r) => {
    const d = moves.get(r.id) ?? 0;
    return { id: r.id, dx: horizontal ? d : 0, dy: horizontal ? 0 : d };
  });
}

/** 是否所有元素在给定轴上已经对齐（用于置灰按钮 / 幂等判断）。 */
export function isAligned(rects: AlignRect[], mode: AlignMode): boolean {
  if (rects.length < 2) return true;
  return alignOffsets(rects, mode).every(
    (o) => Math.abs(o.dx) < 0.5 && Math.abs(o.dy) < 0.5,
  );
}

/* ------------------------------------------------------------------------- *
 * 分层成列布局（「每一层在同一列」）
 *
 * 场景：连线画布铺开后，同一拓扑层级的节点散落在不同 x 上，视觉上很乱。
 * 这里把「按连线算出的层级」映射成「列」：同层节点共用同一个 x（列坐标），
 * 列与列之间按最宽节点 + 间距排布；每列内部保持原有上下顺序、按固定间距铺开。
 *
 * 全部为纯函数：输入矩形 + 层级，输出「每个 id 需要移动多少」，
 * 由内核转成 move_node op。不依赖 DOM 测量，便于单测与重放。
 * ------------------------------------------------------------------------- */

/** 分层布局的输入矩形（与 AlignRect 同构，单独命名以便语义清晰）。 */
export type LayoutRect = AlignRect;

export interface LayeredLayoutOptions {
  /** 列间距（世界坐标，节点右边缘 → 下一列左边缘） */
  columnGap?: number;
  /** 同列内节点的垂直间距 */
  rowGap?: number;
  /** 层内节点的对齐方式：列内左对齐还是水平居中 */
  columnAlign?: "left" | "center";
}

export { layerRanks } from "./layers";

/**
 * 分层成列：同层节点共用同一个 x，列内按原 y 顺序铺开。
 *
 * 返回位移（与 `alignOffsets` 同构），由 `offsetsToMoves` 转成绝对坐标 op。
 * 少于 2 个矩形时没有「整理」的意义，直接返回空。
 * 列坐标从选区包围盒左侧开始，整体不改变选区的左上锚点（避免整理完整个画布跳走）。
 */
export function layeredColumnOffsets(
  rects: LayoutRect[],
  ranks: Map<string, number>,
  opts: LayeredLayoutOptions = {},
): { id: string; dx: number; dy: number }[] {
  if (rects.length < 2) return [];
  const columnGap = Number.isFinite(opts.columnGap) ? opts.columnGap! : 120;
  const rowGap = Number.isFinite(opts.rowGap) ? opts.rowGap! : 40;
  const align = opts.columnAlign ?? "left";

  // 按层分组，层内保持输入顺序（= 视觉上的稳定顺序，避免每次整理乱跳）
  const byRank = new Map<number, LayoutRect[]>();
  for (const r of rects) {
    const rank = ranks.get(r.id) ?? 0;
    const list = byRank.get(rank);
    if (list) list.push(r);
    else byRank.set(rank, [r]);
  }
  const rankKeys = [...byRank.keys()].sort((a, b) => a - b);

  const originX = Math.min(...rects.map((r) => r.x));
  const originY = Math.min(...rects.map((r) => r.y));

  const moves = new Map<string, { dx: number; dy: number }>();
  let colX = originX;
  for (const key of rankKeys) {
    const nodes = byRank.get(key)!;
    // 列宽取该层最宽节点：保证列与列之间不会横向重叠
    const colW = Math.max(...nodes.map((r) => r.w));
    // 列内顺序：沿用原 y（同层后再按原 x 兜底），避免同 y 时顺序不稳定
    const ordered = [...nodes].sort((a, b) => a.y - b.y || a.x - b.x);
    let rowY = originY;
    for (const r of ordered) {
      const targetX = align === "center" ? colX + (colW - r.w) / 2 : colX;
      moves.set(r.id, { dx: targetX - r.x, dy: rowY - r.y });
      rowY += r.h + rowGap;
    }
    colX += colW + columnGap;
  }

  return rects.map((r) => {
    const m = moves.get(r.id) ?? { dx: 0, dy: 0 };
    return { id: r.id, dx: m.dx, dy: m.dy };
  });
}

/** 是否已经按层成列（用于置灰按钮：再点也不会动）。 */
export function isLayerLayoutSettled(
  rects: LayoutRect[],
  ranks: Map<string, number>,
  opts: LayeredLayoutOptions = {},
): boolean {
  if (rects.length < 2) return true;
  return layeredColumnOffsets(rects, ranks, opts).every(
    (o) => Math.abs(o.dx) < 0.5 && Math.abs(o.dy) < 0.5,
  );
}
