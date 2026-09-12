import { useState } from "react";
import { api } from "@/shared/api";
import { cropRect, splitGrid, upscaleTarget } from "../pure";
import { Modal } from "./shared";
import type { CommonProps } from "./shared";

export function CropDialog({
  t,
  kernel,
  node,
  assetId,
  workspaceId,
  natural,
  onClose,
  onCommit,
}: CommonProps) {
  const [ratio, setRatio] = useState<
    "free" | "original" | "1:1" | "16:9" | "9:16"
  >("free");
  const [rect, setRect] = useState({ x: 0, y: 0, w: natural.w, h: natural.h });

  const apply = () => {
    const r = cropRect(rect, natural);
    // 生成新节点并连线（原项目语义：裁剪结果为新图片节点）
    const child = kernel.createNode("image", {
      x: node.rect.x + node.rect.w + 60,
      y: node.rect.y,
    });
    if (child) {
      kernel.dispatch({
        type: "set-spec",
        id: child.id,
        patch: {
          crop: r,
          sourceAssetId: assetId,
          freeResize: ratio === "free",
        },
      });
      kernel.createEdge(node.id, "out", child.id, "in");
      onCommit();
    }
    onClose();
  };

  return (
    <Modal title={t("canvas.tool.crop")} onClose={onClose}>
      <div style={{ marginBottom: 10 }}>
        {(["free", "original", "1:1", "16:9", "9:16"] as const).map((r) => (
          <button
            key={r}
            className={`ic-btn ${ratio === r ? "ic-btn--primary" : ""}`}
            style={{ marginRight: 6, padding: "2px 8px", fontSize: 12 }}
            onClick={() => {
              setRatio(r);
              if (r === "original")
                setRect({ x: 0, y: 0, w: natural.w, h: natural.h });
              if (r === "1:1") setRect(square(natural, 1));
              if (r === "16:9") setRect(square(natural, 16 / 9));
              if (r === "9:16") setRect(square(natural, 9 / 16));
            }}
          >
            {r}
          </button>
        ))}
      </div>
      <img
        src={api.assetThumbUrl(assetId, workspaceId, 480)}
        alt=""
        style={{ width: "100%", borderRadius: 8, marginBottom: 10 }}
      />
      <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 8 }}>
        {(["x", "y", "w", "h"] as const).map((k) => (
          <label key={k}>
            <span className="ic-dim">{k}</span>
            <input
              className="ic-input"
              type="number"
              value={rect[k]}
              onChange={(e) =>
                setRect({ ...rect, [k]: Number(e.target.value) })
              }
            />
          </label>
        ))}
      </div>
      <button
        className="ic-btn ic-btn--primary"
        style={{ marginTop: 12 }}
        onClick={apply}
      >
        {t("common.confirm")}
      </button>
    </Modal>
  );
}

function square(natural: { w: number; h: number }, ratio: number) {
  const w = Math.min(natural.w, Math.round(natural.h * ratio));
  const h = Math.round(w / ratio);
  return {
    x: Math.round((natural.w - w) / 2),
    y: Math.round((natural.h - h) / 2),
    w,
    h,
  };
}

export function SplitDialog({
  t,
  kernel,
  node,
  onClose,
  onCommit,
}: CommonProps) {
  const [rows, setRows] = useState(2);
  const [cols, setCols] = useState(2);
  const [error, setError] = useState<string | null>(null);

  const apply = () => {
    try {
      const cells = splitGrid(
        { rows, cols },
        {
          w: Number(node.spec.naturalW) || 1024,
          h: Number(node.spec.naturalH) || 1024,
        },
      );
      // 按原网格排列到右侧
      const cellW = node.rect.w / cols;
      const cellH = node.rect.h / rows;
      cells.forEach((c, i) => {
        const r = Math.floor(i / cols);
        const col = i % cols;
        const child = kernel.createNode("image", {
          x: node.rect.x + node.rect.w + 60 + col * (cellW + 12),
          y: node.rect.y + r * (cellH + 12),
        });
        if (child) {
          kernel.dispatch({
            type: "set-spec",
            id: child.id,
            patch: { crop: c, sourceAssetId: node.spec.assetId },
          });
          kernel.createEdge(node.id, "out", child.id, "in");
        }
      });
      onCommit();
      onClose();
    } catch (e) {
      setError((e as Error).message);
    }
  };

  return (
    <Modal title={t("canvas.tool.split")} onClose={onClose}>
      <div style={{ display: "flex", gap: 12 }}>
        <label style={{ flex: 1 }}>
          <span className="ic-dim">rows (1–50)</span>
          <input
            className="ic-input"
            type="number"
            min={1}
            max={50}
            value={rows}
            onChange={(e) => setRows(Number(e.target.value))}
          />
        </label>
        <label style={{ flex: 1 }}>
          <span className="ic-dim">cols (1–50)</span>
          <input
            className="ic-input"
            type="number"
            min={1}
            max={50}
            value={cols}
            onChange={(e) => setCols(Number(e.target.value))}
          />
        </label>
      </div>
      <p className="ic-dim" style={{ fontSize: 12 }}>
        将生成 {rows * cols} 个节点（上限 200）
      </p>
      {error && <p className="ic-error">{error}</p>}
      <button className="ic-btn ic-btn--primary" onClick={apply}>
        {t("common.confirm")}
      </button>
    </Modal>
  );
}

export function UpscaleDialog({
  t,
  kernel,
  node,
  onClose,
  onCommit,
}: CommonProps) {
  const natural = {
    w: Number(node.spec.naturalW) || 1024,
    h: Number(node.spec.naturalH) || 1024,
  };
  const [edge, setEdge] = useState(
    Math.min(2048, Math.max(natural.w, natural.h) * 2),
  );
  const [algorithm, setAlgorithm] = useState<
    "nearest" | "bilinear" | "highQuality"
  >("highQuality");
  const target = upscaleTarget(natural, edge);

  const apply = () => {
    if (!target) return;
    const child = kernel.createNode("image", {
      x: node.rect.x + node.rect.w + 60,
      y: node.rect.y,
    });
    if (child) {
      kernel.dispatch({
        type: "set-spec",
        id: child.id,
        patch: {
          upscale: { ...target, algorithm },
          sourceAssetId: node.spec.assetId,
          freeResize: true,
        },
      });
      kernel.createEdge(node.id, "out", child.id, "in");
      onCommit();
    }
    onClose();
  };

  return (
    <Modal title={t("canvas.tool.upscale")} onClose={onClose}>
      <label>
        <span className="ic-dim">目标最长边（≤4096）</span>
        <input
          className="ic-input"
          type="number"
          min={1}
          max={4096}
          value={edge}
          onChange={(e) => setEdge(Number(e.target.value))}
        />
      </label>
      <label style={{ display: "block", marginTop: 8 }}>
        <span className="ic-dim">算法</span>
        <select
          className="ic-select"
          value={algorithm}
          onChange={(e) => setAlgorithm(e.target.value as typeof algorithm)}
        >
          <option value="nearest">最近邻（像素风）</option>
          <option value="bilinear">双线性</option>
          <option value="highQuality">高清插值</option>
        </select>
      </label>
      <p className="ic-dim" style={{ fontSize: 12 }}>
        {target
          ? `${natural.w}×${natural.h} → ${target.w}×${target.h}`
          : "已达上限：目标边长不大于原图"}
        {target?.capped && "（已触及 4096 上限）"}
      </p>
      <button
        className="ic-btn ic-btn--primary"
        disabled={!target}
        onClick={apply}
      >
        {t("common.confirm")}
      </button>
    </Modal>
  );
}
