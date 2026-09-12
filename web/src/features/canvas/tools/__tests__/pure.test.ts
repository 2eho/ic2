import { describe, expect, it } from "vitest";
import {
  buildAnglePrompt,
  cropRect,
  frameTime,
  maskToImageData,
  reversePromptPlan,
  samplePixel,
  SPLIT_CELLS_MAX,
  splitGrid,
  upscaleTarget,
  UPSCALE_EDGE_MAX,
} from "../pure";

describe("cropRect", () => {
  it("把越界区域裁到图像范围内", () => {
    expect(
      cropRect({ x: -10, y: -10, w: 500, h: 500 }, { w: 200, h: 100 }),
    ).toEqual({ x: 0, y: 0, w: 200, h: 100 });
  });
  it("宽高至少为 1", () => {
    const r = cropRect({ x: 0, y: 0, w: 0, h: 0 }, { w: 100, h: 100 });
    expect(r.w).toBe(1);
    expect(r.h).toBe(1);
  });
  it("NaN 输入回退到安全值", () => {
    const r = cropRect(
      { x: Number.NaN, y: Number.NaN, w: Number.NaN, h: Number.NaN },
      { w: 100, h: 100 },
    );
    expect(r.x).toBe(0);
    expect(r.w).toBe(1);
  });
});

describe("splitGrid", () => {
  it("等分网格且覆盖完整", () => {
    const cells = splitGrid({ rows: 2, cols: 2 }, { w: 100, h: 100 });
    expect(cells).toHaveLength(4);
    expect(cells[0]).toEqual({ x: 0, y: 0, w: 50, h: 50 });
    expect(cells[3]).toEqual({ x: 50, y: 50, w: 50, h: 50 });
    const area = cells.reduce((s, c) => s + c.w * c.h, 0);
    expect(area).toBe(10000);
  });

  it("支持自定义切线", () => {
    const cells = splitGrid(
      { rows: 2, cols: 1, rowCuts: [0.3] },
      { w: 100, h: 100 },
    );
    expect(cells[0].h).toBe(30);
    expect(cells[1].h).toBe(70);
  });

  it("拒绝超过单元格上限的组合", () => {
    expect(() => splitGrid({ rows: 50, cols: 50 }, { w: 100, h: 100 })).toThrow(
      /limit/,
    );
  });

  it("行列被限制在合法范围", () => {
    expect(() =>
      splitGrid({ rows: 0, cols: 0 }, { w: 100, h: 100 }),
    ).not.toThrow();
    const cells = splitGrid({ rows: 1, cols: 1 }, { w: 100, h: 100 });
    expect(cells).toHaveLength(1);
  });

  it("单张面积恒等于图像面积（无缝隙无重叠）", () => {
    const cells = splitGrid({ rows: 3, cols: 4 }, { w: 101, h: 97 });
    expect(cells.reduce((s, c) => s + c.w * c.h, 0)).toBe(101 * 97);
    expect(cells.length).toBeLessThanOrEqual(SPLIT_CELLS_MAX);
  });
});

describe("upscaleTarget", () => {
  it("按最长边等比放大", () => {
    expect(upscaleTarget({ w: 100, h: 50 }, 200)).toEqual({
      w: 200,
      h: 100,
      capped: false,
    });
  });
  it("目标不大于原图时返回 null（UI 需提示已达上限）", () => {
    expect(upscaleTarget({ w: 500, h: 500 }, 400)).toBeNull();
  });
  it("受 4096 上限约束并标记 capped", () => {
    const r = upscaleTarget({ w: 100, h: 100 }, 99999);
    expect(r?.w).toBe(UPSCALE_EDGE_MAX);
    expect(r?.capped).toBe(true);
  });
});

describe("samplePixel", () => {
  // 2x2 棋盘：左上红、右上绿、左下蓝、右下白
  const src = new Uint8ClampedArray([
    255, 0, 0, 255, 0, 255, 0, 255, 0, 0, 255, 255, 255, 255, 255, 255,
  ]);

  it("最近邻取整点颜色", () => {
    expect(samplePixel(src, 2, 2, 0.1, 0.1, "nearest")).toEqual([
      255, 0, 0, 255,
    ]);
    expect(samplePixel(src, 2, 2, 1.1, 1.1, "nearest")).toEqual([
      255, 255, 255, 255,
    ]);
  });

  it("双线性插值产生中间色（与最近邻可区分）", () => {
    const nearest = samplePixel(src, 2, 2, 0.5, 0.5, "nearest");
    const bilinear = samplePixel(src, 2, 2, 0.5, 0.5, "bilinear");
    expect(bilinear).not.toEqual(nearest);
    // 四色平均应为灰
    expect(bilinear[0]).toBeCloseTo(127, -1);
    expect(bilinear[1]).toBeCloseTo(127, -1);
    expect(bilinear[2]).toBeCloseTo(127, -1);
  });

  it("越界坐标被夹取，不抛异常", () => {
    expect(() => samplePixel(src, 2, 2, -5, 99, "bilinear")).not.toThrow();
  });
});

describe("reversePromptPlan", () => {
  it("生成三个节点的布局与文案", () => {
    const plan = reversePromptPlan({ x: 0, y: 0, w: 320, h: 320 });
    expect(plan.textNode.rect.x).toBeGreaterThan(320);
    expect(plan.configNode.rect.y).toBeGreaterThan(plan.textNode.rect.y);
    expect(plan.textNode.spec.text).toContain("提示词");
    expect(plan.configNode.spec.capability).toBe("image.edit");
  });
});

describe("buildAnglePrompt", () => {
  it("角度标签与数值一致", () => {
    const p = buildAnglePrompt({
      horizontal: 30,
      pitch: 0,
      distance: 1,
      wideAngle: false,
    });
    expect(p).toContain("向右 30 度");
    expect(p).not.toContain("向左");
  });
  it("超出范围的角度被夹取（不会输出 200 度这种请求）", () => {
    const p = buildAnglePrompt({
      horizontal: 999,
      pitch: -999,
      distance: 5,
      wideAngle: true,
    });
    expect(p).toContain("向右 90 度");
    expect(p).toContain("向下 90 度");
    expect(p).toContain("2.0 倍");
    expect(p).toContain("广角");
  });
  it("默认视角有稳定文案", () => {
    expect(
      buildAnglePrompt({
        horizontal: 0,
        pitch: 0,
        distance: 1,
        wideAngle: false,
      }),
    ).toContain("保持原视角");
  });
});

describe("maskToImageData", () => {
  it("有遮罩处为白，无遮罩处为黑，alpha 全不透明", () => {
    const mask = new Uint8ClampedArray(2 * 4);
    mask[3] = 255; // 第一个像素有遮罩
    const out = maskToImageData(mask, 2, 1);
    expect(out[0]).toBe(255);
    expect(out[4]).toBe(0);
    expect(out[3]).toBe(255);
    expect(out[7]).toBe(255);
  });
});

describe("frameTime", () => {
  it("首帧为 0", () => {
    expect(frameTime("first", 10, 5)).toBe(0);
  });
  it("尾帧留余量，避免取不到", () => {
    expect(frameTime("last", 10, 0)).toBeCloseTo(9.96, 2);
  });
  it("当前帧被夹取在时长内", () => {
    expect(frameTime("current", 10, 999)).toBeCloseTo(9.96, 2);
    expect(frameTime("current", 10, -5)).toBe(0);
  });
  it("时长为 0 时不产生负值", () => {
    expect(frameTime("last", 0, 0)).toBe(0);
  });
});
