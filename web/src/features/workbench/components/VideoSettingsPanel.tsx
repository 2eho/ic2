import {
  VIDEO_SECONDS_MAX,
  VIDEO_SECONDS_MIN,
  clampVideoSeconds,
  computeVideoSize,
  inferVideoRatio,
  parseVideoResolution,
  readVideoDimensions,
  videoRatioOptions,
  videoResolutionOptions,
} from "../media-size";
import type { TFn } from "@/app/App";

export interface VideoParams {
  model: string;
  resolution: string;
  ratio: string;
  size: string;
  seconds: string;
  mode: "frames" | "reference";
  generateAudio: boolean;
  watermark: boolean;
}

export const defaultVideoParams: VideoParams = {
  model: "",
  resolution: "720",
  ratio: "16:9",
  size: "1280x720",
  seconds: "6",
  mode: "frames",
  generateAudio: false,
  watermark: false,
};

interface Props {
  t: TFn;
  params: VideoParams;
  onChange: (patch: Partial<VideoParams>) => void;
  models: Array<{ id: string; providerId: string }>;
  referenceCount: number;
}

/**
 * 视频参数面板（对齐 docs/design/10 §6.9）。
 *
 * 参数落到请求体的字段名必须与上游一致，因此这里的键名（resolution / ratio /
 * seconds / mode / generateAudio / watermark）刻意保持直白，
 * 由 exec 的编译器映射到各家协议——映射集中在服务端一处，前端不做协议适配。
 */
export function VideoSettingsPanel({
  t,
  params,
  onChange,
  models,
  referenceCount,
}: Props) {
  const dimensions = readVideoDimensions(
    params.size,
    params.resolution,
    params.ratio,
  );

  const applySize = (resolution: string, ratio: string) => {
    onChange({ resolution, ratio, size: computeVideoSize(resolution, ratio) });
  };

  /** 参考图超过 2 张时上游只支持「全能参考」模式，这里自动切换并说明原因。 */
  const effectiveMode = referenceCount > 2 ? "reference" : params.mode;

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
          {t("workbench.vquality")}
        </span>
        <div style={{ display: "flex", gap: 4 }}>
          {videoResolutionOptions.map((res) => (
            <button
              key={res}
              className={
                parseVideoResolution(params.resolution) === res
                  ? "ic-btn ic-btn--primary"
                  : "ic-btn"
              }
              style={{ fontSize: 11, padding: "3px 8px" }}
              onClick={() => applySize(res, params.ratio)}
            >
              {res}p
            </button>
          ))}
        </div>
      </div>

      <div>
        <span className="ic-dim" style={{ fontSize: 12 }}>
          {t("workbench.ratio")}
        </span>
        <div style={{ display: "flex", gap: 4, flexWrap: "wrap" }}>
          {videoRatioOptions.map((option) => (
            <button
              key={option.value}
              className={
                params.ratio === option.value
                  ? "ic-btn ic-btn--primary"
                  : "ic-btn"
              }
              style={{ fontSize: 11, padding: "3px 8px" }}
              onClick={() => applySize(params.resolution, option.value)}
            >
              {option.value}
            </button>
          ))}
        </div>
      </div>

      <label>
        <span className="ic-dim" style={{ fontSize: 12 }}>
          {t("workbench.seconds")}: {clampVideoSeconds(params.seconds)}s（
          {VIDEO_SECONDS_MIN}–{VIDEO_SECONDS_MAX}）
        </span>
        <input
          type="range"
          min={VIDEO_SECONDS_MIN}
          max={VIDEO_SECONDS_MAX}
          value={Number(clampVideoSeconds(params.seconds))}
          style={{ width: "100%" }}
          onChange={(e) =>
            onChange({ seconds: clampVideoSeconds(e.target.value) })
          }
        />
      </label>

      <div>
        <span className="ic-dim" style={{ fontSize: 12 }}>
          {t("workbench.videoMode")}
        </span>
        <div style={{ display: "flex", gap: 4 }}>
          {(["frames", "reference"] as const).map((mode) => (
            <button
              key={mode}
              className={
                effectiveMode === mode ? "ic-btn ic-btn--primary" : "ic-btn"
              }
              style={{ fontSize: 11, padding: "3px 8px" }}
              disabled={referenceCount > 2 && mode === "frames"}
              onClick={() => onChange({ mode })}
            >
              {mode === "frames"
                ? t("workbench.modeFrames")
                : t("workbench.modeReference")}
            </button>
          ))}
        </div>
        {referenceCount > 2 && (
          <p className="ic-dim" style={{ fontSize: 11, margin: "4px 0 0" }}>
            {t("workbench.modeAutoSwitched")}
          </p>
        )}
      </div>

      <div style={{ display: "flex", gap: 12 }}>
        <label
          style={{
            display: "flex",
            alignItems: "center",
            gap: 4,
            fontSize: 12,
          }}
        >
          <input
            type="checkbox"
            checked={params.generateAudio}
            onChange={(e) => onChange({ generateAudio: e.target.checked })}
          />
          {t("workbench.generateAudio")}
        </label>
        <label
          style={{
            display: "flex",
            alignItems: "center",
            gap: 4,
            fontSize: 12,
          }}
        >
          <input
            type="checkbox"
            checked={params.watermark}
            onChange={(e) => onChange({ watermark: e.target.checked })}
          />
          {t("workbench.watermark")}
        </label>
      </div>

      <p className="ic-dim ic-mono" style={{ fontSize: 11, margin: 0 }}>
        → {params.size} ({dimensions.width}×{dimensions.height}) ·{" "}
        {clampVideoSeconds(params.seconds)}s · {effectiveMode} · audio=
        {String(params.generateAudio)} · wm={String(params.watermark)}
      </p>
    </div>
  );
}

export { inferVideoRatio };
