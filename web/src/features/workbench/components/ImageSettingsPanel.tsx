import {
  imageSizePresets,
  alignDimension,
  mediaRatioOptions,
  mediaScaleOptions,
  readMediaDimensions,
  type RatioOption,
} from "../media-size";
import type { TFn } from "@/app/App";

export interface ImageParams {
  model: string;
  scale: string;
  ratio: string;
  size: string;
  quality: string;
  count: number;
  background: string;
}

export const defaultImageParams: ImageParams = {
  model: "",
  scale: "1k",
  ratio: "1:1",
  size: imageSizePresets["1k"]["1:1"],
  quality: "auto",
  count: 1,
  background: "auto",
};

interface Props {
  t: TFn;
  params: ImageParams;
  onChange: (patch: Partial<ImageParams>) => void;
  models: Array<{ id: string; providerId: string }>;
  maxCount?: number;
}

/**
 * 生图参数面板（对齐 docs/design/10 §6.2）。
 *
 * 交互约束（这些不是审美选择，每一条都为了避免一种错误）：
 *   - 尺寸由「档位 + 比例」组合决定，用户不能随意输入宽高，
 *     否则极易组合出上游不接受的尺寸（见 media-size.ts 的说明）；
 *   - 但允许手工微调，且微调后自动对齐到 16 的倍数并**立刻显示结果值**，
 *     让用户看到实际会发出去的尺寸，而不是他输入的那个数；
 *   - 张数上限 10（与 limits.imageCount.workbenchMax 一致）。
 */
export function ImageSettingsPanel({
  t,
  params,
  onChange,
  models,
  maxCount = 10,
}: Props) {
  const dimensions = readMediaDimensions(
    params.size,
    params.scale,
    params.ratio,
  );

  const applySize = (scale: string, ratio: string) => {
    const size =
      ratio === "auto" || scale === "auto"
        ? "auto"
        : (imageSizePresets[scale]?.[ratio] ?? params.size);
    onChange({ scale, ratio, size });
  };

  const updateDimension = (key: "width" | "height", raw: number) => {
    const value = alignDimension(raw, true);
    const next = { ...dimensions, [key]: value };
    onChange({ size: `${next.width}x${next.height}` });
  };

  return (
    <div style={{ display: "grid", gap: 10 }}>
      <label>
        <span className="ic-dim" style={{ fontSize: 12 }}>
          {t("workbench.model")}
        </span>
        <select
          className="ic-select"
          value={params.model}
          onChange={(e) => onChange({ model: e.target.value })}
        >
          <option value="">{t("workbench.defaultModel")}</option>
          {models.map((m) => (
            <option key={`${m.providerId}/${m.id}`} value={m.id}>
              {m.id}
            </option>
          ))}
        </select>
      </label>

      <div>
        <span className="ic-dim" style={{ fontSize: 12 }}>
          {t("workbench.quality")}
        </span>
        <div style={{ display: "flex", gap: 4 }}>
          {["auto", "high", "medium", "low"].map((q) => (
            <button
              key={q}
              className={
                params.quality === q ? "ic-btn ic-btn--primary" : "ic-btn"
              }
              style={{ fontSize: 11, padding: "3px 8px" }}
              onClick={() => onChange({ quality: q })}
            >
              {q}
            </button>
          ))}
        </div>
      </div>

      <div>
        <span className="ic-dim" style={{ fontSize: 12 }}>
          {t("workbench.size")}
        </span>
        <div style={{ display: "flex", gap: 4, marginBottom: 4 }}>
          {mediaScaleOptions.map((scale) => (
            <button
              key={scale}
              className={
                params.scale === scale ? "ic-btn ic-btn--primary" : "ic-btn"
              }
              style={{ fontSize: 11, padding: "3px 8px" }}
              onClick={() => applySize(scale, params.ratio)}
            >
              {scale}
            </button>
          ))}
        </div>
        <div style={{ display: "flex", gap: 4, flexWrap: "wrap" }}>
          {mediaRatioOptions.map((option: RatioOption) => (
            <button
              key={option.value}
              className={
                params.ratio === option.value
                  ? "ic-btn ic-btn--primary"
                  : "ic-btn"
              }
              style={{ fontSize: 11, padding: "3px 8px" }}
              onClick={() => applySize(params.scale, option.value)}
            >
              {option.value}
            </button>
          ))}
        </div>
      </div>

      <div style={{ display: "flex", gap: 6, alignItems: "flex-end" }}>
        <label style={{ flex: 1 }}>
          <span className="ic-dim" style={{ fontSize: 12 }}>
            W
          </span>
          <input
            className="ic-input"
            type="number"
            value={dimensions.width}
            disabled={params.ratio === "auto"}
            onChange={(e) => updateDimension("width", Number(e.target.value))}
          />
        </label>
        <label style={{ flex: 1 }}>
          <span className="ic-dim" style={{ fontSize: 12 }}>
            H
          </span>
          <input
            className="ic-input"
            type="number"
            value={dimensions.height}
            disabled={params.ratio === "auto"}
            onChange={(e) => updateDimension("height", Number(e.target.value))}
          />
        </label>
        <label style={{ width: 110 }}>
          <span className="ic-dim" style={{ fontSize: 12 }}>
            {t("workbench.background")}
          </span>
          <select
            className="ic-select"
            value={params.background}
            onChange={(e) => onChange({ background: e.target.value })}
          >
            <option value="auto">auto</option>
            <option value="transparent">{t("workbench.transparent")}</option>
            <option value="opaque">{t("workbench.opaque")}</option>
          </select>
        </label>
      </div>

      <label>
        <span className="ic-dim" style={{ fontSize: 12 }}>
          {t("workbench.count")} (1–{maxCount})
        </span>
        <div style={{ display: "flex", gap: 4, flexWrap: "wrap" }}>
          {Array.from({ length: maxCount }, (_, i) => i + 1).map((n) => (
            <button
              key={n}
              className={
                params.count === n ? "ic-btn ic-btn--primary" : "ic-btn"
              }
              style={{ fontSize: 11, padding: "3px 8px", minWidth: 30 }}
              onClick={() => onChange({ count: n })}
            >
              {n}
            </button>
          ))}
        </div>
      </label>

      {/* 实际提交值必须可见：用户看到的是「会发出去的那个数」，不是他输入的 */}
      <p className="ic-dim ic-mono" style={{ fontSize: 11, margin: 0 }}>
        → {params.size} · quality={params.quality} · n={params.count} · bg=
        {params.background}
      </p>
    </div>
  );
}
