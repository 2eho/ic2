/**
 * 画布内图像工具的纯函数实现。
 *
 * 设计依据 docs/design/10 §5：原项目这一大块逻辑散在 438/299 行对话框里，
 * 重写后统一放这里，与 UI 解耦，全部可单测。
 *
 * 约束：只使用 Canvas API 的确定性能力，不做隐式重采样之外的魔法。
 */

export interface CropSpec {
  x: number;
  y: number;
  w: number;
  h: number;
}

export interface SplitSpec {
  rows: number;
  cols: number;
  /** 自定义切线（相对 0–1 的比例），为空则等分 */
  rowCuts?: number[];
  colCuts?: number[];
}

/** 边界：切图行列上限（与 internal/graph/limits.go 及 docs 保持一致）。 */
export const SPLIT_ROWS_MAX = 50;
export const SPLIT_CELLS_MAX = 200;
export const UPSCALE_EDGE_MAX = 4096;

/** 生成裁剪区域（把 UI 的缩放/平移换算回原图像素）。 */
export function cropRect(
  spec: CropSpec,
  natural: { w: number; h: number },
): CropSpec {
  const x = clamp(Math.round(spec.x), 0, Math.max(0, natural.w - 1));
  const y = clamp(Math.round(spec.y), 0, Math.max(0, natural.h - 1));
  const w = clamp(Math.round(spec.w), 1, natural.w - x);
  const h = clamp(Math.round(spec.h), 1, natural.h - y);
  return { x, y, w, h };
}

/**
 * 计算切图网格。
 * 返回每个单元格的像素矩形，顺序为从左到右、从上到下（对齐原项目排布）。
 */
export function splitGrid(
  spec: SplitSpec,
  natural: { w: number; h: number },
): CropSpec[] {
  const rows = clampInt(spec.rows, 1, SPLIT_ROWS_MAX);
  const cols = clampInt(spec.cols, 1, SPLIT_ROWS_MAX);
  if (rows * cols > SPLIT_CELLS_MAX) {
    throw new Error(
      `split would create ${rows * cols} cells, limit is ${SPLIT_CELLS_MAX}`,
    );
  }
  const rowEdges = edgesFrom(rows, spec.rowCuts, natural.h);
  const colEdges = edgesFrom(cols, spec.colCuts, natural.w);
  const out: CropSpec[] = [];
  for (let r = 0; r < rows; r++) {
    for (let c = 0; c < cols; c++) {
      const y = rowEdges[r];
      const x = colEdges[c];
      const h = rowEdges[r + 1] - y;
      const w = colEdges[c + 1] - x;
      if (w <= 0 || h <= 0) continue;
      out.push({ x, y, w, h });
    }
  }
  return out;
}

function edgesFrom(
  count: number,
  cuts: number[] | undefined,
  total: number,
): number[] {
  const edges: number[] = [0];
  if (cuts && cuts.length > 0) {
    const sorted = [...cuts].map((c) => clamp(c, 0, 1)).sort((a, b) => a - b);
    for (const c of sorted) {
      const v = Math.round(c * total);
      if (v > edges[edges.length - 1] && v < total) edges.push(v);
    }
  } else {
    for (let i = 1; i < count; i++) edges.push(Math.round((total * i) / count));
  }
  if (edges[edges.length - 1] !== total) edges.push(total);
  return edges;
}

/**
 * 放大目标尺寸计算（原项目上限 4096，见 docs/design/10 §5.4）。
 * 返回 null 表示已达到上限（UI 应提示而不是静默截断）。
 */
export function upscaleTarget(
  natural: { w: number; h: number },
  targetEdge: number,
  maxEdge = UPSCALE_EDGE_MAX,
): { w: number; h: number; capped: boolean } | null {
  const longest = Math.max(natural.w, natural.h);
  const desired = clampInt(targetEdge, 1, maxEdge);
  if (desired <= longest) return null;
  const ratio = desired / longest;
  const w = Math.round(natural.w * ratio);
  const h = Math.round(natural.h * ratio);
  return { w, h, capped: desired >= maxEdge };
}

export type UpscaleAlgorithm = "nearest" | "bilinear" | "highQuality";

/**
 * 单像素采样（纯函数，便于单测三种算法的差异）。
 * src 为按行优先存放的 RGBA 数组。
 */
export function samplePixel(
  src: Uint8ClampedArray,
  srcW: number,
  srcH: number,
  x: number,
  y: number,
  algorithm: UpscaleAlgorithm,
): [number, number, number, number] {
  if (algorithm === "nearest") {
    const sx = clamp(Math.floor(x), 0, srcW - 1);
    const sy = clamp(Math.floor(y), 0, srcH - 1);
    return readPixel(src, srcW, sx, sy);
  }
  // bilinear / highQuality 共用双线性核（highQuality 由调用方叠加锐化）
  const x0 = clamp(Math.floor(x), 0, srcW - 1);
  const y0 = clamp(Math.floor(y), 0, srcH - 1);
  const x1 = clamp(x0 + 1, 0, srcW - 1);
  const y1 = clamp(y0 + 1, 0, srcH - 1);
  const fx = x - x0;
  const fy = y - y0;
  const p00 = readPixel(src, srcW, x0, y0);
  const p10 = readPixel(src, srcW, x1, y0);
  const p01 = readPixel(src, srcW, x0, y1);
  const p11 = readPixel(src, srcW, x1, y1);
  const out: [number, number, number, number] = [0, 0, 0, 0];
  for (let i = 0; i < 4; i++) {
    const top = p00[i] * (1 - fx) + p10[i] * fx;
    const bottom = p01[i] * (1 - fx) + p11[i] * fx;
    out[i] = Math.round(top * (1 - fy) + bottom * fy);
  }
  return out;
}

function readPixel(
  src: Uint8ClampedArray,
  srcW: number,
  x: number,
  y: number,
): [number, number, number, number] {
  const i = (y * srcW + x) * 4;
  return [src[i], src[i + 1], src[i + 2], src[i + 3]];
}

function clamp(v: number, min: number, max: number): number {
  if (!Number.isFinite(v)) return min;
  return Math.min(max, Math.max(min, v));
}

function clampInt(v: number, min: number, max: number): number {
  return Math.round(clamp(v, min, max));
}

/**
 * 反推提示词节点的文案（对齐原项目 createImageReversePromptNodes 的三节点布局）。
 * 返回「文本节点」「生成配置节点」的内容与相对位置。
 */
export function reversePromptPlan(nodeRect: {
  x: number;
  y: number;
  w: number;
  h: number;
}) {
  return {
    textNode: {
      title: "反推提示词",
      spec: {
        text: "请描述这张图片的画面内容、风格、构图与光线，输出一段可直接用于生图的提示词。",
      },
      rect: { x: nodeRect.x + nodeRect.w + 80, y: nodeRect.y, w: 320, h: 220 },
    },
    configNode: {
      title: "生成",
      spec: { capability: "image.edit", outputCount: 1 },
      rect: {
        x: nodeRect.x + nodeRect.w + 80,
        y: nodeRect.y + 260,
        w: 340,
        h: 260,
      },
    },
  };
}

export interface AngleSpec {
  horizontal: number; // -90..90 度
  pitch: number; // -90..90 度
  distance: number; // 0..2 倍
  wideAngle: boolean;
}

/**
 * 生成 AI 多角度编辑提示词（对齐原项目 buildAnglePrompt 的文案结构）。
 * 角度标签必须与数值一致，否则用户看到的与请求的不符。
 */
export function buildAnglePrompt(spec: AngleSpec): string {
  const h = clamp(Math.round(spec.horizontal), -90, 90);
  const p = clamp(Math.round(spec.pitch), -90, 90);
  const d = clamp(spec.distance, 0, 2);
  const parts: string[] = ["保持主体不变，改变观察视角："];
  if (h !== 0)
    parts.push(`水平旋转 ${h > 0 ? "向右" : "向左"} ${Math.abs(h)} 度`);
  if (p !== 0) parts.push(`俯仰 ${p > 0 ? "向上" : "向下"} ${Math.abs(p)} 度`);
  if (d !== 1)
    parts.push(
      d > 1
        ? `镜头拉远至 ${d.toFixed(1)} 倍距离`
        : `镜头拉近至 ${d.toFixed(1)} 倍距离`,
    );
  if (spec.wideAngle) parts.push("使用广角镜头效果");
  if (parts.length === 1) parts.push("保持原视角");
  return parts.join("；") + "。";
}

/** 蒙版导出：把画布上的遮罩转成黑白标注图（白=需要重绘区域）。 */
export function maskToImageData(
  mask: Uint8ClampedArray,
  w: number,
  h: number,
): Uint8ClampedArray {
  const out = new Uint8ClampedArray(w * h * 4);
  for (let i = 0; i < w * h; i++) {
    const a = mask[i * 4 + 3];
    const v = a > 0 ? 255 : 0;
    out[i * 4] = v;
    out[i * 4 + 1] = v;
    out[i * 4 + 2] = v;
    out[i * 4 + 3] = 255;
  }
  return out;
}

/** 视频截帧时间点（首帧/尾帧/当前帧），避免 0 与 duration 越界。 */
export function frameTime(
  kind: "first" | "last" | "current",
  durationSec: number,
  currentSec: number,
): number {
  const d = Math.max(0, durationSec);
  switch (kind) {
    case "first":
      return 0;
    case "last":
      return Math.max(0, d - 0.04); // 尾帧留一点余量，避免取不到
    default:
      return clamp(currentSec, 0, Math.max(0, d - 0.04));
  }
}
