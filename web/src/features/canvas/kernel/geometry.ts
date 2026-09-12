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
