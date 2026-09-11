import { useEffect, useRef, useState } from "react";
import { api } from "@/shared/api";
import { reversePromptPlan, buildAnglePrompt } from "../pure";
import type { AngleSpec } from "../pure";
import { Modal } from "./shared";
import type { CommonProps } from "./shared";

export function AngleDialog({
  t,
  kernel,
  node,
  onClose,
  onCommit,
}: CommonProps) {
  const [spec, setSpec] = useState<AngleSpec>({
    horizontal: 0,
    pitch: 0,
    distance: 1,
    wideAngle: false,
  });
  const prompt = buildAnglePrompt(spec);

  const apply = () => {
    // 生成「文本节点 + 生成节点」并按原项目语义连线
    const text = kernel.createNode("prompt", {
      x: node.rect.x + node.rect.w + 60,
      y: node.rect.y,
    });
    const gen = kernel.createNode("generation", {
      x: node.rect.x + node.rect.w + 60,
      y: node.rect.y + 260,
    });
    if (text && gen) {
      kernel.dispatch({
        type: "set-spec",
        id: text.id,
        patch: { text: prompt },
      });
      kernel.dispatch({
        type: "set-spec",
        id: gen.id,
        patch: { capability: "image.edit", params: { angle: spec } },
      });
      kernel.createEdge(text.id, "out", gen.id, "prompt");
      kernel.createEdge(node.id, "out", gen.id, "ref");
      onCommit();
    }
    onClose();
  };

  return (
    <Modal title={t("canvas.tool.multiAngle")} onClose={onClose}>
      {(
        [
          ["horizontal", -90, 90, "水平角度"],
          ["pitch", -90, 90, "俯仰角度"],
          ["distance", 0, 2, "镜头距离"],
        ] as const
      ).map(([key, min, max, label]) => (
        <label key={key} style={{ display: "block", marginBottom: 8 }}>
          <span className="ic-dim">
            {label}: {spec[key]}
          </span>
          <input
            className="ic-input"
            type="range"
            min={min}
            max={max}
            step={key === "distance" ? 0.1 : 1}
            value={spec[key]}
            onChange={(e) =>
              setSpec({ ...spec, [key]: Number(e.target.value) })
            }
          />
        </label>
      ))}
      <label style={{ display: "flex", gap: 6, alignItems: "center" }}>
        <input
          type="checkbox"
          checked={spec.wideAngle}
          onChange={(e) => setSpec({ ...spec, wideAngle: e.target.checked })}
        />
        <span>广角</span>
      </label>
      <pre
        className="ic-mono"
        style={{
          background: "var(--ic-surface-2)",
          padding: 10,
          borderRadius: 8,
          whiteSpace: "pre-wrap",
        }}
      >
        {prompt}
      </pre>
      <button className="ic-btn ic-btn--primary" onClick={apply}>
        {t("common.confirm")}
      </button>
    </Modal>
  );
}

export function ReversePromptDialog({
  t,
  kernel,
  node,
  onClose,
  onCommit,
}: CommonProps) {
  const plan = reversePromptPlan(node.rect);
  const apply = () => {
    const text = kernel.createNode("prompt", {
      x: plan.textNode.rect.x,
      y: plan.textNode.rect.y,
    });
    const gen = kernel.createNode("generation", {
      x: plan.configNode.rect.x,
      y: plan.configNode.rect.y,
    });
    if (text && gen) {
      kernel.dispatch({
        type: "set-spec",
        id: text.id,
        patch: { text: plan.textNode.spec.text },
      });
      kernel.dispatch({
        type: "set-spec",
        id: gen.id,
        patch: { capability: "text.generate" },
      });
      kernel.createEdge(text.id, "out", gen.id, "prompt");
      kernel.createEdge(node.id, "out", gen.id, "ref");
      onCommit();
    }
    onClose();
  };
  return (
    <Modal title={t("canvas.tool.reversePrompt")} onClose={onClose}>
      <p className="ic-dim" style={{ fontSize: 13 }}>
        将创建「文本」与「生成」两个节点并自动连线。
      </p>
      <button className="ic-btn ic-btn--primary" onClick={apply}>
        {t("common.confirm")}
      </button>
    </Modal>
  );
}

export function MaskDialog({
  t,
  kernel,
  node,
  assetId,
  workspaceId,
  natural,
  onClose,
  onCommit,
}: CommonProps) {
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const [brush, setBrush] = useState(40);
  const [history, setHistory] = useState<ImageData[]>([]);
  const drawing = useRef(false);
  const last = useRef<{ x: number; y: number } | null>(null);

  useEffect(() => {
    const c = canvasRef.current;
    if (!c) return;
    c.width = 512;
    c.height = Math.round((512 * natural.h) / natural.w);
    const ctx = c.getContext("2d");
    if (!ctx) return;
    ctx.clearRect(0, 0, c.width, c.height);
    const img = new Image();
    img.crossOrigin = "anonymous";
    img.src = api.assetThumbUrl(assetId, workspaceId, 512);
    img.onload = () => {
      ctx.globalAlpha = 0.9;
      ctx.drawImage(img, 0, 0, c.width, c.height);
      ctx.globalAlpha = 1;
    };
  }, [assetId, workspaceId, natural.w, natural.h]);

  const toCanvas = (e: React.PointerEvent) => {
    const c = canvasRef.current!;
    const r = c.getBoundingClientRect();
    return {
      x: ((e.clientX - r.left) / r.width) * c.width,
      y: ((e.clientY - r.top) / r.height) * c.height,
    };
  };

  const stroke = (
    from: { x: number; y: number },
    to: { x: number; y: number },
    erase: boolean,
  ) => {
    const ctx = canvasRef.current?.getContext("2d");
    if (!ctx) return;
    ctx.globalCompositeOperation = erase ? "destination-out" : "source-over";
    ctx.strokeStyle = "rgba(79,70,229,.55)";
    ctx.lineWidth = brush;
    ctx.lineCap = "round";
    ctx.beginPath();
    ctx.moveTo(from.x, from.y);
    ctx.lineTo(to.x, to.y);
    ctx.stroke();
    ctx.globalCompositeOperation = "source-over";
  };

  const snapshot = () => {
    const ctx = canvasRef.current?.getContext("2d");
    if (!ctx || !canvasRef.current) return;
    setHistory((h) => [
      ...h.slice(-19),
      ctx.getImageData(
        0,
        0,
        canvasRef.current!.width,
        canvasRef.current!.height,
      ),
    ]);
  };

  const undo = () => {
    const prev = history[history.length - 1];
    const ctx = canvasRef.current?.getContext("2d");
    if (!prev || !ctx) return;
    ctx.putImageData(prev, 0, 0);
    setHistory((h) => h.slice(0, -1));
  };

  const exportMaskOnly = () => {
    const c = canvasRef.current;
    if (!c) return;
    const link = document.createElement("a");
    link.download = `${node.title || "mask"}.png`;
    link.href = c.toDataURL("image/png");
    link.click();
  };

  const generate = () => {
    // 生成编辑节点：以「原图 + 蒙版标注图」两张参考图走 image.edit
    const child = kernel.createNode("generation", {
      x: node.rect.x + node.rect.w + 60,
      y: node.rect.y,
    });
    if (child) {
      kernel.dispatch({
        type: "set-spec",
        id: child.id,
        patch: {
          capability: "image.edit",
          params: { masked: true },
          outputCount: 1,
        },
      });
      kernel.createEdge(node.id, "out", child.id, "ref");
      onCommit();
    }
    onClose();
  };

  return (
    <Modal title={t("canvas.tool.mask")} onClose={onClose} wide>
      <div
        style={{
          display: "flex",
          gap: 8,
          marginBottom: 8,
          alignItems: "center",
          flexWrap: "wrap",
        }}
      >
        <label>
          <span className="ic-dim">笔刷 {brush}</span>
          <input
            type="range"
            min={4}
            max={120}
            value={brush}
            onChange={(e) => setBrush(Number(e.target.value))}
          />
        </label>
        <button
          className="ic-btn"
          onClick={undo}
          disabled={history.length === 0}
        >
          撤销
        </button>
        <button className="ic-btn" onClick={exportMaskOnly}>
          仅导出
        </button>
        <button className="ic-btn ic-btn--primary" onClick={generate}>
          立刻生成
        </button>
      </div>
      <canvas
        ref={canvasRef}
        style={{
          width: "100%",
          borderRadius: 8,
          cursor: "crosshair",
          touchAction: "none",
        }}
        onPointerDown={(e) => {
          snapshot();
          drawing.current = true;
          last.current = toCanvas(e);
          (e.target as HTMLElement).setPointerCapture(e.pointerId);
        }}
        onPointerMove={(e) => {
          if (!drawing.current) return;
          const p = toCanvas(e);
          if (last.current) stroke(last.current, p, e.shiftKey);
          last.current = p;
        }}
        onPointerUp={() => {
          drawing.current = false;
          last.current = null;
        }}
      />
      <p className="ic-dim" style={{ fontSize: 12 }}>
        按住 Shift 为擦除；遮罩区域会被标记为需要重绘。
      </p>
    </Modal>
  );
}
