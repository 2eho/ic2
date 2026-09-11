import { describe, expect, it } from "vitest";
import {
  VIDEO_SECONDS_MAX,
  VIDEO_SECONDS_MIN,
  alignDimension,
  clampVideoSeconds,
  computeMediaSize,
  computeVideoSize,
  imageSizePresets,
  inferMediaRatio,
  inferMediaScale,
  inferVideoRatio,
  parseVideoResolution,
  readMediaDimensions,
  readVideoDimensions,
} from "../media-size";

/**
 * 尺寸计算的单测。
 *
 * 为什么值得测：这些函数的输出会**直接作为参数发给上游**。
 * 算错一个像素就是一次 400，而用户看到的是英文错误码，根本不知道该改什么。
 * 因此这里穷举的是「上游的硬约束」而不是我们的实现细节：
 *   - 尺寸必须能整除 16（除视频的偶数约束）；
 *   - 比例表与像素表必须自洽（每个比例在每个档位都有预设）；
 *   - 反推（尺寸 → 档位/比例）必须能还原 UI 选项，否则「回填参数」会失准。
 */
describe("图像尺寸", () => {
  it("每个档位 × 每个比例都有预设，且能整除 16", () => {
    for (const [scale, table] of Object.entries(imageSizePresets)) {
      for (const [ratio, size] of Object.entries(table)) {
        expect(size, `${scale} ${ratio} 缺失`).toMatch(/^\d+x\d+$/);
        const [w, h] = size.split("x").map(Number);
        expect(w % 16, `${scale} ${ratio} 宽 ${w} 不能被 16 整除`).toBe(0);
        expect(h % 16, `${scale} ${ratio} 高 ${h} 不能被 16 整除`).toBe(0);
      }
    }
  });

  it("档位越高像素越多（4k > 2k > 1k）", () => {
    for (const ratio of Object.keys(imageSizePresets["1k"])) {
      const area = (scale: string) => {
        const [w, h] = imageSizePresets[scale][ratio].split("x").map(Number);
        return w * h;
      };
      expect(area("4k"), `${ratio} 4k 应大于 2k`).toBeGreaterThan(area("2k"));
      expect(area("2k"), `${ratio} 2k 应大于 1k`).toBeGreaterThan(area("1k"));
    }
  });

  it("比例像素值与比例声明一致（容差 2%，因为取整到 16 的倍数）", () => {
    for (const [scale, table] of Object.entries(imageSizePresets)) {
      for (const [ratio, size] of Object.entries(table)) {
        const [rw, rh] = ratio.split(":").map(Number);
        const [w, h] = size.split("x").map(Number);
        const declared = rw / rh;
        const actual = w / h;
        expect(
          Math.abs(actual - declared) / declared,
          `${scale} ${ratio} 偏差过大`,
        ).toBeLessThan(0.02);
      }
    }
  });

  it("computeMediaSize：auto 比例返回 auto", () => {
    expect(computeMediaSize("1k", "auto")).toBe("auto");
    expect(computeMediaSize("auto", "16:9")).toBe("16:9");
  });

  it("反推：能还原标准尺寸对应的档位与比例", () => {
    for (const scale of ["1k", "2k", "4k"]) {
      for (const [ratio, size] of Object.entries(imageSizePresets[scale])) {
        expect(inferMediaScale(size), `${size} 档位反推失败`).toBe(scale);
        expect(inferMediaRatio(size), `${size} 比例反推失败`).toBe(ratio);
      }
    }
  });

  it("反推：非标准尺寸取最接近的比例而不是精确匹配", () => {
    // 1920x1080 不在预设表里，但它是 16:9
    expect(inferMediaRatio("1920x1080")).toBe("16:9");
    // 1000x1000 接近 1:1
    expect(inferMediaRatio("1000x1000")).toBe("1:1");
  });

  it("反推：无法解析时回落到默认值，不抛错", () => {
    expect(inferMediaScale("bogus")).toBe("auto");
    expect(inferMediaRatio("bogus")).toBe("1:1");
    expect(inferMediaRatio("bogus", "16:9")).toBe("16:9");
  });

  it("alignDimension 对齐到 16 的倍数且不小于 16", () => {
    expect(alignDimension(100, true)).toBe(96);
    expect(alignDimension(8, true)).toBe(16);
    expect(alignDimension(0, true)).toBe(16);
    expect(alignDimension(-5, true)).toBe(16);
    expect(alignDimension(100, false)).toBe(100);
  });

  it("readMediaDimensions 优先用显式尺寸，否则用档位×比例算", () => {
    expect(readMediaDimensions("800x600", "1k", "1:1")).toEqual({
      width: 800,
      height: 600,
    });
    expect(readMediaDimensions("auto", "1k", "1:1")).toEqual({
      width: 1024,
      height: 1024,
    });
  });
});

describe("视频尺寸", () => {
  it("清晰度作用于短边，另一条边按比例推", () => {
    // 720p 横屏 16:9 → 短边 720，长边 1280
    expect(computeVideoSize("720", "16:9")).toBe("1280x720");
    // 720p 竖屏 9:16 → 短边 720，长边 1280
    expect(computeVideoSize("720", "9:16")).toBe("720x1280");
  });

  it("所有边都是偶数（编码器兼容性）", () => {
    for (const res of ["480", "720", "1080"]) {
      for (const ratio of ["1:1", "3:4", "4:3", "16:9", "9:16", "21:9"]) {
        const size = computeVideoSize(res, ratio);
        const [w, h] = size.split("x").map(Number);
        expect(w % 2, `${res} ${ratio} → ${size} 宽不是偶数`).toBe(0);
        expect(h % 2, `${res} ${ratio} → ${size} 高不是偶数`).toBe(0);
      }
    }
  });

  it("短边恒等于清晰度（除了奇数修正）", () => {
    for (const res of ["480", "720", "1080"]) {
      for (const ratio of ["16:9", "9:16"]) {
        const [w, h] = computeVideoSize(res, ratio).split("x").map(Number);
        expect(Math.min(w, h)).toBe(Number(res));
      }
    }
  });

  it("auto 比例返回 auto（让上游自己决定）", () => {
    expect(computeVideoSize("720", "auto")).toBe("auto");
  });

  it("清晰度解析：兼容 low/high/medium/auto 与带 p 后缀", () => {
    expect(parseVideoResolution("1080p")).toBe("1080");
    expect(parseVideoResolution("low")).toBe("480");
    expect(parseVideoResolution("high")).toBe("720");
    expect(parseVideoResolution("auto")).toBe("720");
    expect(parseVideoResolution("")).toBe("720");
    expect(parseVideoResolution(undefined)).toBe("720");
  });

  it("视频比例反推默认 16:9（而不是 1:1）", () => {
    expect(inferVideoRatio("bogus")).toBe("16:9");
    expect(inferVideoRatio("1920x1080")).toBe("16:9");
    expect(inferVideoRatio("1080x1920")).toBe("9:16");
  });

  it("readVideoDimensions 优先显式尺寸", () => {
    expect(readVideoDimensions("640x480", "720", "16:9")).toEqual({
      width: 640,
      height: 480,
    });
    expect(readVideoDimensions("auto", "1080", "16:9")).toEqual({
      width: 1920,
      height: 1080,
    });
  });
});

describe("时长边界", () => {
  it("越界值收敛到边界（用户选错，不静默改成默认值）", () => {
    expect(clampVideoSeconds("0")).toBe(String(VIDEO_SECONDS_MIN));
    expect(clampVideoSeconds("1")).toBe("4");
    expect(clampVideoSeconds("999")).toBe(String(VIDEO_SECONDS_MAX));
    expect(clampVideoSeconds(-10)).toBe("4");
  });

  it("无法解析时用默认值（用户还没选，而不是选错了）", () => {
    expect(clampVideoSeconds("abc")).toBe("6");
    expect(clampVideoSeconds("")).toBe("6");
    expect(clampVideoSeconds("   ")).toBe("6");
    expect(clampVideoSeconds(Number.NaN)).toBe("6");
    // 与「越界」区分：这是本用例的核心断言
    expect(clampVideoSeconds("0")).not.toBe("6");
  });

  it("合法值原样返回", () => {
    expect(clampVideoSeconds("6")).toBe("6");
    expect(clampVideoSeconds("30")).toBe("30");
    expect(clampVideoSeconds(12)).toBe("12");
  });

  it("小数向下取整", () => {
    expect(clampVideoSeconds("7.9")).toBe("7");
  });
});
