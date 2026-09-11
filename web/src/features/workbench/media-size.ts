/**
 * 媒体尺寸计算（复刻原项目 `lib/media-size.ts` 的**行为**，不是代码）。
 *
 * 为什么要复刻这张表而不是「让用户随便填」：
 *   上游对宽高有硬约束——尺寸必须是 16 的倍数、长边上限、比例集合有限。
 *   如果不按这张表算，用户会拿到「参数不合法」的上游错误，
 *   而错误信息通常是英文的 400，用户根本不知道该改成多少。
 *   这里把约束内化成选项，让用户**无法**组合出非法参数。
 *
 * 两张表的分工：
 *   - imageSizePresets：预设档位（1k/2k/4k）× 比例 → 像素。这是上游推荐的组合。
 *   - video：给定清晰度（480/720/1080）与比例，按短边算偶数像素。
 *
 * 有意保留的边界（与 internal/graph/limits.go 的 SizeMax 一致）：
 *   4k 21:9 = 3840x1648，长边 3840，仍在 SizeMax=20000 之内。
 */

export const mediaScaleOptions = ["1k", "2k", "4k", "auto"] as const;

export interface RatioOption {
  value: string;
  width: number;
  height: number;
}

export const mediaRatioOptions: readonly RatioOption[] = [
  { value: "1:1", width: 1, height: 1 },
  { value: "2:3", width: 2, height: 3 },
  { value: "3:2", width: 3, height: 2 },
  { value: "4:3", width: 4, height: 3 },
  { value: "3:4", width: 3, height: 4 },
  { value: "16:9", width: 16, height: 9 },
  { value: "9:16", width: 9, height: 16 },
  { value: "21:9", width: 21, height: 9 },
  { value: "9:21", width: 9, height: 21 },
  { value: "auto", width: 0, height: 0 },
] as const;

/** 预设像素表（来自原项目的实测可用组合）。 */
export const imageSizePresets: Record<string, Record<string, string>> = {
  "1k": {
    "1:1": "1024x1024",
    "2:3": "1024x1536",
    "3:2": "1536x1024",
    "4:3": "1024x768",
    "3:4": "768x1024",
    "16:9": "1536x864",
    "9:16": "864x1536",
    "21:9": "2016x864",
    "9:21": "864x2016",
  },
  "2k": {
    "1:1": "2048x2048",
    "2:3": "1360x2048",
    "3:2": "2048x1360",
    "4:3": "2048x1536",
    "3:4": "1536x2048",
    "16:9": "2048x1152",
    "9:16": "1152x2048",
    "21:9": "2688x1152",
    "9:21": "1152x2688",
  },
  "4k": {
    "1:1": "2880x2880",
    "2:3": "2336x3520",
    "3:2": "3520x2336",
    "4:3": "3312x2480",
    "3:4": "2480x3312",
    "16:9": "3840x2160",
    "9:16": "2160x3840",
    "21:9": "3840x1648",
    "9:21": "1648x3840",
  },
};

export const VIDEO_SECONDS_MIN = 4;
export const VIDEO_SECONDS_MAX = 30;

export const videoRatioOptions: readonly RatioOption[] = [
  { value: "1:1", width: 1, height: 1 },
  { value: "3:4", width: 3, height: 4 },
  { value: "4:3", width: 4, height: 3 },
  { value: "16:9", width: 16, height: 9 },
  { value: "9:16", width: 9, height: 16 },
  { value: "21:9", width: 21, height: 9 },
  { value: "auto", width: 0, height: 0 },
] as const;

export const videoResolutionOptions = ["480", "720", "1080"] as const;

export function parsePixelSize(
  value: string,
): { width: number; height: number } | null {
  const match = String(value ?? "").match(/^(\d+)x(\d+)$/i);
  if (!match) return null;
  return { width: Number(match[1]), height: Number(match[2]) };
}

export function parseAspectRatio(
  value: string,
): { width: number; height: number } | null {
  const match = String(value ?? "").match(/^(\d+(?:\.\d+)?):(\d+(?:\.\d+)?)$/);
  if (!match) return null;
  const width = Number(match[1]);
  const height = Number(match[2]);
  if (!width || !height) return null;
  return { width, height };
}

export function normalizeMediaScale(value: string | undefined): string {
  const scale = String(value ?? "")
    .trim()
    .toLowerCase();
  if (scale === "2k" || scale === "2048") return "2k";
  if (scale === "4k" || scale === "3840") return "4k";
  if (scale === "auto") return "auto";
  return "1k";
}

/** 从像素反推档位（用于「回填参数」时把历史尺寸还原成 UI 选项）。 */
export function inferMediaScale(size: string, storedScale?: string): string {
  if (storedScale) return normalizeMediaScale(storedScale);
  if (!size || size === "auto") return "auto";
  if (parseAspectRatio(size)) return "auto";
  const pixels = parsePixelSize(size);
  if (!pixels) return "auto";
  const preset = Object.keys(imageSizePresets).find((scale) =>
    Object.values(imageSizePresets[scale]).includes(
      `${pixels.width}x${pixels.height}`,
    ),
  );
  if (preset) return preset;
  const longSide = Math.max(pixels.width, pixels.height);
  if (longSide >= 3072) return "4k";
  if (longSide >= 1536) return "2k";
  return "1k";
}

/**
 * 从像素反推最接近的比例。
 *
 * 判据用「宽高比差的绝对值」而不是「精确匹配」：用户可能手工输入过
 * 1024x1000 这种非标准尺寸，精确匹配会退化成 1:1（把意图丢掉）。
 */
export function inferMediaRatio(size: string, fallback = "1:1"): string {
  if (!size || size === "auto") return "auto";
  if (mediaRatioOptions.some((item) => item.value === size)) return size;
  const pixels = parsePixelSize(size) ?? parseAspectRatio(size);
  if (!pixels) return fallback;
  const target = pixels.width / pixels.height;
  let best = fallback;
  let bestDelta = Number.POSITIVE_INFINITY;
  for (const option of mediaRatioOptions) {
    if (option.value === "auto") continue;
    const delta = Math.abs(option.width / option.height - target);
    if (delta < bestDelta) {
      bestDelta = delta;
      best = option.value;
    }
  }
  return best;
}

export function computeMediaSize(scale: string, ratio: string): string {
  if (!ratio || ratio === "auto") return "auto";
  const normalized = normalizeMediaScale(scale);
  if (normalized === "auto") return ratio;
  return imageSizePresets[normalized]?.[ratio] ?? "auto";
}

export function readMediaDimensions(
  size: string,
  scale: string,
  ratio: string,
): { width: number; height: number } {
  const pixels = parsePixelSize(size);
  if (pixels) return pixels;
  const computed = computeMediaSize(
    scale === "auto" ? "1k" : scale,
    ratio === "auto" ? "1:1" : ratio,
  );
  return parsePixelSize(computed) ?? { width: 0, height: 0 };
}

/** 把尺寸对齐到 16 的倍数（上游硬要求）。 */
export function alignDimension(value: number, toStep: boolean): number {
  const n = Math.max(1, Math.floor(value || 0));
  if (!toStep) return n;
  return Math.max(16, Math.round(n / 16) * 16);
}

/**
 * 把时长收敛到 [4, 30]。
 *
 * 边界语义刻意区分两种情况（原项目把两者混为一谈，导致 0 被静默改成 6）：
 *   - **无法解析**（空串 / "abc" / undefined）→ 用默认 6 秒，这是「用户还没选」；
 *   - **能解析但越界**（0、1、999）→ 收敛到边界，这是「用户选了但选错」，
 *     悄悄改成 6 会让用户以为自己选的值生效了。
 */
export function clampVideoSeconds(
  value: string | number,
  fallback = 6,
): string {
  // 注意 `Number('') === 0` 这个 JS 陷阱：空串会被当成「用户输入了 0」，
  // 从而收敛到 4 而不是用默认值。必须先判空再转数字。
  const text =
    typeof value === "number" ? String(value) : String(value ?? "").trim();
  if (text === "") return String(fallback);
  const raw = Number(text);
  const seconds = Number.isFinite(raw) ? Math.floor(raw) : fallback;
  return String(
    Math.max(VIDEO_SECONDS_MIN, Math.min(VIDEO_SECONDS_MAX, seconds)),
  );
}

export function parseVideoResolution(value: string | undefined): string {
  const raw = String(value ?? "")
    .trim()
    .toLowerCase();
  if (raw === "low") return "480";
  if (raw === "auto" || raw === "high" || raw === "medium" || raw === "")
    return "720";
  const number = raw.replace(/p$/i, "");
  return /^\d+$/.test(number) && Number(number) > 0 ? number : "720";
}

export function inferVideoRatio(size: string): string {
  if (!size || size === "auto") return "auto";
  if (videoRatioOptions.some((item) => item.value === size)) return size;
  const pixels = parsePixelSize(size) ?? parseAspectRatio(size);
  if (!pixels) return "16:9";
  const target = pixels.width / pixels.height;
  let best = "16:9";
  let bestDelta = Number.POSITIVE_INFINITY;
  for (const option of videoRatioOptions) {
    if (option.value === "auto") continue;
    const delta = Math.abs(option.width / option.height - target);
    if (delta < bestDelta) {
      bestDelta = delta;
      best = option.value;
    }
  }
  return best;
}

/**
 * 按清晰度与比例算视频尺寸。
 *
 * 规则：清晰度是**短边**的像素数，另一条边按比例推并取偶数
 * （编码器对奇数边的支持不一致，取偶数是最省事的兼容策略）。
 */
export function computeVideoSize(resolution: string, ratio: string): string {
  if (!ratio || ratio === "auto") return "auto";
  const parsed = parseAspectRatio(ratio);
  if (!parsed) return "auto";
  const shortSide = Math.max(
    1,
    Math.floor(Number(parseVideoResolution(resolution)) || 720),
  );
  const landscape = parsed.width >= parsed.height;
  const width = evenRound(
    landscape ? (shortSide * parsed.width) / parsed.height : shortSide,
  );
  const height = evenRound(
    landscape ? shortSide : (shortSide * parsed.height) / parsed.width,
  );
  return `${width}x${height}`;
}

export function readVideoDimensions(
  size: string,
  resolution: string,
  ratio: string,
): { width: number; height: number } {
  const pixels = parsePixelSize(size);
  if (pixels) return pixels;
  const computed = computeVideoSize(
    resolution,
    ratio === "auto" ? "16:9" : ratio,
  );
  return parsePixelSize(computed) ?? { width: 0, height: 0 };
}

function evenRound(value: number): number {
  return Math.max(2, Math.round(value / 2) * 2);
}
