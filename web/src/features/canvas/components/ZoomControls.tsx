import { useKernelViewport } from "../hooks/useKernel";
import type { CanvasKernel } from "../kernel";
import { ZOOM_MAX, ZOOM_MIN } from "../kernel/viewport";
import type { TFn } from "@/app/App";

/** 缩放控件：滑杆 / 百分比 / 重置（对齐 docs/design/10 §3.15）。 */
export function ZoomControls({
  kernel,
  size,
  t,
}: {
  kernel: CanvasKernel;
  size: { x: number; y: number };
  t: TFn;
}) {
  const vp = useKernelViewport(kernel);
  const center = { x: size.x / 2, y: size.y / 2 };

  return (
    <div
      style={{
        position: "absolute",
        right: 12,
        bottom: 148,
        display: "flex",
        alignItems: "center",
        gap: 6,
        padding: "4px 8px",
        borderRadius: 8,
        background: "var(--ic-surface)",
        border: "1px solid var(--ic-border)",
        boxShadow: "var(--ic-shadow)",
      }}
      onPointerDown={(e) => e.stopPropagation()}
    >
      <button
        className="ic-btn ic-btn--ghost"
        style={{ padding: "0 6px" }}
        onClick={() => kernel.viewport.setZoom(vp.k * 0.8, center)}
      >
        −
      </button>
      <input
        type="range"
        min={ZOOM_MIN}
        max={ZOOM_MAX}
        step={0.01}
        value={vp.k}
        style={{ width: 90 }}
        onChange={(e) =>
          kernel.viewport.setZoom(Number(e.target.value), center)
        }
      />
      <button
        className="ic-btn ic-btn--ghost"
        style={{ padding: "0 6px" }}
        onClick={() => kernel.viewport.setZoom(vp.k * 1.25, center)}
      >
        +
      </button>
      <span
        className="ic-dim"
        style={{ fontSize: 12, width: 44, textAlign: "right" }}
      >
        {Math.round(vp.k * 100)}%
      </span>
      <button
        className="ic-btn ic-btn--ghost"
        style={{ padding: "0 6px" }}
        title={t("canvas.zoomReset")}
        onClick={() => kernel.viewport.reset()}
      >
        ⟲
      </button>
    </div>
  );
}
