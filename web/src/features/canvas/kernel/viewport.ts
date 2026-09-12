import type { Rect, Vec2, Viewport } from "./types";

/** 与 internal/graph/limits.go 保持一致（边界唯一真源在服务端，前端做前置校验）。 */
export const ZOOM_MIN = 0.05;
export const ZOOM_MAX = 5;
export const COORD_LIMIT = 1e7;
export const SIZE_MIN = 16;
export const SIZE_MAX = 20000;

export function isValidViewport(v: Viewport): boolean {
  for (const n of [v.x, v.y, v.k]) {
    if (!Number.isFinite(n)) return false;
  }
  if (Math.abs(v.x) > COORD_LIMIT || Math.abs(v.y) > COORD_LIMIT) return false;
  return v.k >= ZOOM_MIN && v.k <= ZOOM_MAX;
}

export function isValidRect(r: Rect): boolean {
  for (const n of [r.x, r.y, r.w, r.h]) {
    if (!Number.isFinite(n)) return false;
  }
  if (Math.abs(r.x) > COORD_LIMIT || Math.abs(r.y) > COORD_LIMIT) return false;
  return (
    r.w >= SIZE_MIN && r.w <= SIZE_MAX && r.h >= SIZE_MIN && r.h <= SIZE_MAX
  );
}

/** 把缩放约束到合法区间；非有限值返回 1（默认视角）。 */
export function clampZoom(k: number): number {
  if (!Number.isFinite(k)) return 1;
  return Math.min(ZOOM_MAX, Math.max(ZOOM_MIN, k));
}

/**
 * 视口控制器：世界坐标 ↔ 屏幕坐标。
 * 变换公式：screen = world * k + offset。
 * 纯函数式：所有方法返回新 viewport，不原地修改（便于 undo 与测试）。
 */
export class ViewportController {
  private vp: Viewport;

  constructor(initial: Viewport = { x: 0, y: 0, k: 1 }) {
    this.vp = isValidViewport(initial) ? { ...initial } : { x: 0, y: 0, k: 1 };
  }

  get current(): Viewport {
    return { ...this.vp };
  }

  set(v: Viewport): Viewport {
    if (!isValidViewport(v)) return this.current;
    this.vp = { ...v };
    return this.current;
  }

  /** 屏幕坐标 → 世界坐标 */
  toWorld(p: Vec2): Vec2 {
    return {
      x: (p.x - this.vp.x) / this.vp.k,
      y: (p.y - this.vp.y) / this.vp.k,
    };
  }

  /** 世界坐标 → 屏幕坐标 */
  toScreen(p: Vec2): Vec2 {
    return { x: p.x * this.vp.k + this.vp.x, y: p.y * this.vp.k + this.vp.y };
  }

  /** 按屏幕位移平移 */
  panBy(dx: number, dy: number): Viewport {
    if (!Number.isFinite(dx) || !Number.isFinite(dy)) return this.current;
    this.vp = {
      ...this.vp,
      x: clampCoord(this.vp.x + dx),
      y: clampCoord(this.vp.y + dy),
    };
    return this.current;
  }

  /**
   * 以屏幕点为锚点缩放（滚轮/捏合）。
   * 保持 anchor 下方的世界点不动，这是「指针为锚点」的核心。
   */
  zoomAt(anchor: Vec2, delta: number): Viewport {
    if (!Number.isFinite(delta) || delta === 0) return this.current;
    const worldBefore = this.toWorld(anchor);
    const nextK = clampZoom(this.vp.k * (1 - delta));
    if (nextK === this.vp.k) return this.current;
    this.vp = {
      k: nextK,
      x: anchor.x - worldBefore.x * nextK,
      y: anchor.y - worldBefore.y * nextK,
    };
    return this.current;
  }

  /** 直接设定缩放倍数（滑杆/百分比输入），保持画布中心不动。 */
  setZoom(k: number, center: Vec2): Viewport {
    if (!Number.isFinite(k)) return this.current;
    const worldCenter = this.toWorld(center);
    const nextK = clampZoom(k);
    this.vp = {
      k: nextK,
      x: center.x - worldCenter.x * nextK,
      y: center.y - worldCenter.y * nextK,
    };
    return this.current;
  }

  reset(): Viewport {
    this.vp = { x: 0, y: 0, k: 1 };
    return this.current;
  }

  /** 计算包裹全部矩形的视口（带内边距），bounds 为空时回到默认视角。 */
  fit(bounds: Rect[], viewportSize: Vec2, padding = 80): Viewport {
    if (bounds.length === 0) return this.reset();
    let minX = Infinity;
    let minY = Infinity;
    let maxX = -Infinity;
    let maxY = -Infinity;
    for (const r of bounds) {
      minX = Math.min(minX, r.x);
      minY = Math.min(minY, r.y);
      maxX = Math.max(maxX, r.x + r.w);
      maxY = Math.max(maxY, r.y + r.h);
    }
    const w = Math.max(1, maxX - minX);
    const h = Math.max(1, maxY - minY);
    const k = clampZoom(
      Math.min(
        (viewportSize.x - padding * 2) / w,
        (viewportSize.y - padding * 2) / h,
      ),
    );
    this.vp = {
      k,
      x: viewportSize.x / 2 - (minX + w / 2) * k,
      y: viewportSize.y / 2 - (minY + h / 2) * k,
    };
    return this.current;
  }

  /** 动画聚焦到某节点（对齐原项目 450ms easeOutCubic）。 */
  focusRect(
    target: Rect,
    viewportSize: Vec2,
    scale = Math.min(1.6, ZOOM_MAX),
  ): Viewport {
    const k = clampZoom(scale);
    this.vp = {
      k,
      x: viewportSize.x / 2 - (target.x + target.w / 2) * k,
      y: viewportSize.y / 2 - (target.y + target.h / 2) * k,
    };
    return this.current;
  }

  /** 世界坐标矩形 → 屏幕坐标矩形（命中测试与渲染裁剪都用它） */
  rectToScreen(r: Rect): Rect {
    const a = this.toScreen({ x: r.x, y: r.y });
    return { x: a.x, y: a.y, w: r.w * this.vp.k, h: r.h * this.vp.k };
  }

  /** 可视区域的世界坐标包围盒（用于视口裁剪） */
  visibleWorldRect(viewportSize: Vec2, padding = 0): Rect {
    const tl = this.toWorld({ x: -padding, y: -padding });
    const br = this.toWorld({
      x: viewportSize.x + padding,
      y: viewportSize.y + padding,
    });
    return { x: tl.x, y: tl.y, w: br.x - tl.x, h: br.y - tl.y };
  }
}

function clampCoord(n: number): number {
  return Math.min(COORD_LIMIT, Math.max(-COORD_LIMIT, n));
}
