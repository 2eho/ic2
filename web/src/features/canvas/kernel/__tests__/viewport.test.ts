import { describe, expect, it } from "vitest";
import {
  ViewportController,
  ZOOM_MAX,
  ZOOM_MIN,
  isValidRect,
  isValidViewport,
} from "../viewport";
import type { Rect } from "../types";

describe("ViewportController", () => {
  it("屏幕与世界坐标互转可逆", () => {
    const vp = new ViewportController({ x: 120, y: -40, k: 1.75 });
    for (const p of [
      { x: 0, y: 0 },
      { x: 640, y: 480 },
      { x: -320, y: 200 },
    ]) {
      const world = vp.toWorld(p);
      const back = vp.toScreen(world);
      expect(back.x).toBeCloseTo(p.x, 6);
      expect(back.y).toBeCloseTo(p.y, 6);
    }
  });

  it("缩放时锚点下的世界坐标保持不动（指针为锚点）", () => {
    const vp = new ViewportController({ x: 33, y: 77, k: 1 });
    const anchor = { x: 400, y: 300 };
    const before = vp.toWorld(anchor);
    vp.zoomAt(anchor, 0.25);
    const after = vp.toWorld(anchor);
    expect(after.x).toBeCloseTo(before.x, 6);
    expect(after.y).toBeCloseTo(before.y, 6);
  });

  it("缩放被限制在 [0.05, 5]", () => {
    const vp = new ViewportController();
    vp.setZoom(1000, { x: 0, y: 0 });
    expect(vp.current.k).toBe(ZOOM_MAX);
    vp.setZoom(0.0001, { x: 0, y: 0 });
    expect(vp.current.k).toBe(ZOOM_MIN);
    vp.setZoom(Number.NaN, { x: 0, y: 0 });
    expect(vp.current.k).toBe(ZOOM_MIN);
  });

  it("拒绝非有限数与越界视口", () => {
    const vp = new ViewportController();
    vp.set({ x: Number.NaN, y: 0, k: 1 });
    expect(vp.current).toEqual({ x: 0, y: 0, k: 1 });
    vp.set({ x: 0, y: 0, k: 1e15 });
    expect(vp.current.k).toBe(1);
    expect(isValidViewport({ x: 1e9, y: 0, k: 1 })).toBe(false);
  });

  it("平移累积并受坐标上限约束", () => {
    const vp = new ViewportController();
    vp.panBy(100, 50);
    expect(vp.current.x).toBe(100);
    expect(vp.current.y).toBe(50);
    vp.panBy(Number.POSITIVE_INFINITY, 0);
    expect(Number.isFinite(vp.current.x)).toBe(true);
  });

  it("视口裁剪返回正确的世界包围盒", () => {
    const vp = new ViewportController({ x: 0, y: 0, k: 2 });
    const r = vp.visibleWorldRect({ x: 800, y: 600 });
    expect(r.w).toBeCloseTo(400);
    expect(r.h).toBeCloseTo(300);
  });

  it("fit 包裹全部矩形并留出内边距", () => {
    const vp = new ViewportController();
    const rects: Rect[] = [
      { x: 0, y: 0, w: 100, h: 100 },
      { x: 900, y: 700, w: 100, h: 100 },
    ];
    vp.fit(rects, { x: 800, y: 600 }, 40);
    for (const r of rects.map((x) => vp.rectToScreen(x))) {
      expect(r.x).toBeGreaterThan(-1);
      expect(r.y).toBeGreaterThan(-1);
      expect(r.x + r.w).toBeLessThan(801);
      expect(r.y + r.h).toBeLessThan(601);
    }
  });

  it("空集合 fit 回到默认视角", () => {
    const vp = new ViewportController({ x: 500, y: 500, k: 3 });
    vp.fit([], { x: 800, y: 600 });
    expect(vp.current).toEqual({ x: 0, y: 0, k: 1 });
  });
});

describe("isValidRect", () => {
  it("拒绝 NaN / Infinity / 越界尺寸", () => {
    expect(isValidRect({ x: Number.NaN, y: 0, w: 100, h: 100 })).toBe(false);
    expect(isValidRect({ x: 0, y: 0, w: 0, h: 100 })).toBe(false);
    expect(isValidRect({ x: 0, y: 0, w: 1e9, h: 100 })).toBe(false);
    expect(isValidRect({ x: 1e8, y: 0, w: 100, h: 100 })).toBe(false);
    expect(isValidRect({ x: 0, y: 0, w: 320, h: 220 })).toBe(true);
  });
});
