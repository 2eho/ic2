import { describe, expect, it } from 'vitest';
import {
  applyResize,
  hitResizeHandle,
  keepAspectHeight,
  normalizeRect,
  rectContains,
  rectsIntersect,
  snapToGrid,
  unionRects,
} from '../geometry';

const R = { x: 0, y: 0, w: 100, h: 100 };

describe('几何计算', () => {
  it('矩形相交判定', () => {
    expect(rectsIntersect(R, { x: 50, y: 50, w: 100, h: 100 })).toBe(true);
    expect(rectsIntersect(R, { x: 101, y: 0, w: 10, h: 10 })).toBe(false);
  });

  it('包含判定', () => {
    expect(rectContains(R, { x: 10, y: 10, w: 10, h: 10 })).toBe(true);
    expect(rectContains(R, { x: -1, y: 10, w: 10, h: 10 })).toBe(false);
  });

  it('归一化矩形支持反向拖拽', () => {
    expect(normalizeRect({ x: 100, y: 100 }, { x: 0, y: 0 })).toEqual({ x: 0, y: 0, w: 100, h: 100 });
  });

  it('合并矩形', () => {
    expect(unionRects([{ x: 0, y: 0, w: 10, h: 10 }, { x: 20, y: 30, w: 10, h: 10 }])).toEqual({
      x: 0, y: 0, w: 30, h: 40,
    });
    expect(unionRects([])).toBeNull();
  });

  it('网格吸附步长为 16', () => {
    expect(snapToGrid(20)).toBe(16);
    expect(snapToGrid(24)).toBe(32);
    expect(snapToGrid(Number.NaN)).toBeNaN();
  });

  it('等比高度换算', () => {
    expect(keepAspectHeight({ x: 0, y: 0, w: 100, h: 50 }, 200)).toBe(100);
    expect(keepAspectHeight({ x: 0, y: 0, w: 0, h: 0 }, 200)).toBe(0);
  });

  it('八向手柄命中', () => {
    const screen = { x: 100, y: 100, w: 200, h: 200 };
    expect(hitResizeHandle({ x: 100, y: 100 }, screen)).toBe('nw');
    expect(hitResizeHandle({ x: 300, y: 200 }, screen)).toBe('e');
    expect(hitResizeHandle({ x: 200, y: 300 }, screen)).toBe('s');
    expect(hitResizeHandle({ x: 200, y: 200 }, screen)).toBeNull();
  });

  it('缩放手柄计算保持最小尺寸', () => {
    const r = { x: 0, y: 0, w: 100, h: 100 };
    expect(applyResize(r, 'e', { dx: -500, dy: 0 }, { minSize: 16 }).w).toBe(16);
    const grow = applyResize(r, 'se', { dx: 50, dy: 30 });
    expect(grow.w).toBe(150);
    expect(grow.h).toBe(130);
  });

  it('保持比例时高度跟随宽度', () => {
    const out = applyResize({ x: 0, y: 0, w: 100, h: 50 }, 'e', { dx: 100, dy: 0 }, { keepAspect: true });
    expect(out.w).toBeCloseTo(200);
    expect(out.h).toBeCloseTo(100);
  });

  it('缩放受尺寸上限约束', () => {
    expect(applyResize({ x: 0, y: 0, w: 100, h: 100 }, 'e', { dx: 1e9, dy: 0 }).w).toBe(20000);
  });
});
