import { useEffect, useState } from "react";
import type { CanvasKernel } from "../kernel";
import type {
  AlignAnchor,
  AlignMode,
  DistributeAxis,
} from "../kernel/geometry";
import { useKernelSelection } from "../hooks/useKernel";
import type { TFn } from "@/app/App";

interface Props {
  t: TFn;
  kernel: CanvasKernel;
  onCommit: () => void;
}

/**
 * 多选工具栏：一键对齐 / 等间距分布 / 打组 / 解散。
 *
 * 设计取舍：
 * - 只在内核层面算位移（`kernel.alignSelection`），UI 不做几何计算；
 * - 按钮的可用性来自「对齐后是否真的会动」，而不是「选了几个」——
 *   已经对齐时按钮置灰，避免用户点了没反应却以为坏了；
 * - 默认按选区包围盒对齐（多数画布的习惯），按住 Alt 改为以首个选中的
 *   节点为基准（等价于 Figma 的「对齐到关键对象」）。
 */
export function SelectionToolbar({ t, kernel, onCommit }: Props) {
  const selection = useKernelSelection(kernel);
  const [anchor, setAnchor] = useState<AlignAnchor>("union");
  const [altHeld, setAltHeld] = useState(false);

  // Alt 按住时切基准：用 keydown/keyup 而不是读取按钮事件，
  // 这样「先按住 Alt 再点按钮」和「点了按钮再想改基准」都一致。
  useEffect(() => {
    const down = (e: KeyboardEvent) => {
      if (e.key === "Alt") setAltHeld(true);
    };
    const up = (e: KeyboardEvent) => {
      if (e.key === "Alt") setAltHeld(false);
    };
    const blur = () => setAltHeld(false);
    window.addEventListener("keydown", down);
    window.addEventListener("keyup", up);
    window.addEventListener("blur", blur);
    return () => {
      window.removeEventListener("keydown", down);
      window.removeEventListener("keyup", up);
      window.removeEventListener("blur", blur);
    };
  }, []);

  const activeAnchor: AlignAnchor = altHeld ? "first" : anchor;

  const ids = selection.nodes;
  const rects = kernel.rectsOf(ids);

  /** 会不会真的动？动不了就置灰，而不是让用户点了没反应。 */
  const canAlign = (mode: AlignMode): boolean =>
    rects.length >= 2 && !isAlignedAlready(rects, mode, activeAnchor);

  /** 分层成列：同样与「点了会不会真的动」同源，已经成列就置灰。 */
  const canLayoutLayers = ids.length >= 2 && !kernel.isLayeredAlready(ids);

  if (ids.length < 2) return null;

  const run = (mode: AlignMode) => {
    const ops = kernel.alignSelection(ids, mode, activeAnchor);
    if (ops.length) onCommit();
  };

  const runDistribute = (axis: DistributeAxis) => {
    const ops = kernel.distributeSelection(ids, axis);
    if (ops.length) onCommit();
  };

  const runLayoutLayers = () => {
    const ops = kernel.layoutByLayers(ids);
    if (ops.length) onCommit();
  };

  const alignButtons: { mode: AlignMode; label: string; icon: string }[] = [
    { mode: "left", label: t("canvas.alignLeft"), icon: "⇤" },
    { mode: "hcenter", label: t("canvas.alignHCenter"), icon: "↔" },
    { mode: "right", label: t("canvas.alignRight"), icon: "⇥" },
    { mode: "top", label: t("canvas.alignTop"), icon: "⤒" },
    { mode: "vcenter", label: t("canvas.alignVCenter"), icon: "↕" },
    { mode: "bottom", label: t("canvas.alignBottom"), icon: "⤓" },
  ];

  return (
    <div
      className="ic-card"
      data-selection-toolbar
      onPointerDown={(e) => e.stopPropagation()}
      style={{
        position: "absolute",
        left: "50%",
        bottom: 72,
        transform: "translateX(-50%)",
        display: "flex",
        gap: 2,
        padding: 6,
        alignItems: "center",
        zIndex: 400,
      }}
    >
      <span
        className="ic-badge"
        title={t("canvas.alignHint")}
        style={{ marginRight: 4 }}
      >
        {t("canvas.align")}
      </span>
      {alignButtons.map((b) => (
        <button
          key={b.mode}
          className="ic-btn ic-btn--ghost"
          data-align={b.mode}
          title={b.label}
          disabled={!canAlign(b.mode)}
          onClick={() => run(b.mode)}
        >
          {b.icon}
        </button>
      ))}
      <span style={{ width: 1, height: 20, background: "var(--ic-border)" }} />
      <button
        className="ic-btn ic-btn--ghost"
        data-align="distribute-h"
        title={t("canvas.distributeH")}
        disabled={!canDistribute(rects, "horizontal")}
        onClick={() => runDistribute("horizontal")}
      >
        ⇹
      </button>
      <button
        className="ic-btn ic-btn--ghost"
        data-align="distribute-v"
        title={t("canvas.distributeV")}
        disabled={!canDistribute(rects, "vertical")}
        onClick={() => runDistribute("vertical")}
      >
        ⤢
      </button>
      <button
        className="ic-btn ic-btn--ghost"
        data-align="layout-layers"
        title={`${t("canvas.layoutLayers")} · ${t("canvas.layoutLayersHint")}`}
        disabled={!canLayoutLayers}
        onClick={runLayoutLayers}
      >
        {t("canvas.layoutLayers")}
      </button>
      <span style={{ width: 1, height: 20, background: "var(--ic-border)" }} />
      <button
        className="ic-btn ic-btn--ghost"
        onClick={() => setAnchor(anchor === "union" ? "first" : "union")}
        title={t("canvas.alignAnchor")}
        data-align="anchor"
      >
        {activeAnchor === "union"
          ? t("canvas.alignAnchorUnion")
          : t("canvas.alignAnchorFirst")}
      </button>
      <button
        className="ic-btn ic-btn--ghost"
        onClick={() => {
          const group = kernel.createNode("group", {
            x: Math.min(...rects.map((r) => r.x)) - 24,
            y: Math.min(...rects.map((r) => r.y)) - 52,
          });
          if (group) {
            kernel.dispatch({
              type: "group",
              nodeIds: ids,
              groupId: group.id,
            });
            onCommit();
          }
        }}
      >
        {t("canvas.groupSelected")}
      </button>
      <button
        className="ic-btn ic-btn--ghost"
        data-align="delete"
        onClick={() => {
          kernel.dispatch({ type: "delete-nodes", ids });
          onCommit();
        }}
      >
        {t("canvas.deleteSelection")}
      </button>
    </div>
  );
}

/** 已经对齐 → 置灰（对用户来说「再点也不会变」的按钮不应可点）。 */
function isAlignedAlready(
  rects: { id: string; x: number; y: number; w: number; h: number }[],
  mode: AlignMode,
  anchor: AlignAnchor,
): boolean {
  const EPS = 0.5;
  const first = rects[0];
  const ref = (pick: (r: typeof first) => number): number => {
    if (anchor === "first") return pick(first);
    const minX = Math.min(...rects.map((r) => r.x));
    const maxX = Math.max(...rects.map((r) => r.x + r.w));
    const minY = Math.min(...rects.map((r) => r.y));
    const maxY = Math.max(...rects.map((r) => r.y + r.h));
    switch (mode) {
      case "left":
        return minX;
      case "right":
        return maxX;
      case "hcenter":
        return (minX + maxX) / 2;
      case "top":
        return minY;
      case "bottom":
        return maxY;
      default:
        return (minY + maxY) / 2;
    }
  };
  return rects.every((r) => {
    switch (mode) {
      case "left":
        return Math.abs(r.x - ref((f) => f.x)) < EPS;
      case "right":
        return Math.abs(r.x + r.w - ref((f) => f.x + f.w)) < EPS;
      case "hcenter":
        return Math.abs(r.x + r.w / 2 - ref((f) => f.x + f.w / 2)) < EPS;
      case "top":
        return Math.abs(r.y - ref((f) => f.y)) < EPS;
      case "bottom":
        return Math.abs(r.y + r.h - ref((f) => f.y + f.h)) < EPS;
      default:
        return Math.abs(r.y + r.h / 2 - ref((f) => f.y + f.h / 2)) < EPS;
    }
  });
}

function canDistribute(
  rects: { id: string; x: number; y: number; w: number; h: number }[],
  axis: DistributeAxis,
): boolean {
  if (rects.length < 3) return false;
  const key = axis === "horizontal" ? "x" : "y";
  const sorted = [...rects].sort((a, b) => a[key] - b[key]);
  return sorted.length >= 3;
}
