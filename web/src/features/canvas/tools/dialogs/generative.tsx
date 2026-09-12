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
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
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

  /**
   * 生成编辑节点：以「原图 + 蒙版标注图」两张参考图走 image.edit（5.1）。
   *
   * 关键点是蒙版必须**落成真实资产**再连线，而不是只在 params 里放一个
   * `masked: true` 标记。上一版就是这样：界面上画笔、擦除、撤销全都能用，
   * 但服务端收到的是一个布尔值——用户的涂改从未离开浏览器。
   * 这类「界面完整、链路断开」的问题最难发现，因为每一步看起来都成功了。
   *
   * 所以这里的纪律是：**只要产物会影响结果，它就必须是可追溯的输入**。
   * 蒙版图作为图片2 连到 edit 节点，执行时被 CompilePrompt 编成「图片2」，
   * 与 UI 的角标同源。
   */
  const generate = async () => {
    const c = canvasRef.current;
    if (!c) {
      return;
    }
    setError(null);
    setBusy(true);
    try {
      const blob = await new Promise<Blob | null>((resolve) =>
        c.toBlob((b) => resolve(b), "image/png"),
      );
      if (!blob) {
        throw new Error("mask_export_failed");
      }
      // 空白蒙版（用户一笔没画）会让模型收到一张全透明的图，
      // 结果是「原图被轻微重绘」——不是用户要的，也不是错误。
      // 显式拒绝并说明原因，比让他花一次费用去发现更好。
      if (!blob.size || isBlankMask(c)) {
        setError("empty_mask");
        return;
      }
      const mask = await api.uploadAsset(
        workspaceId,
        blob,
        `${node.title || "node"}-mask.png`,
      );

      const child = kernel.createNode("generation", {
        x: node.rect.x + node.rect.w + 60,
        y: node.rect.y,
      });
      if (!child) {
        throw new Error("create_node_failed");
      }
      kernel.dispatch({
        type: "set-spec",
        id: child.id,
        patch: {
          capability: "image.edit",
          editMode: "mask",
          outputCount: 1,
        },
      });
      // 图片1 = 原图（ref 端口 order 0），图片2 = 蒙版（order 1）。
      // 顺序即语义：port 的 order 决定 CompilePrompt 里的编号。
      kernel.createEdge(node.id, "out", child.id, "ref");
      const maskNode = kernel.createNode("image", {
        x: node.rect.x,
        y: node.rect.y + node.rect.h + 40,
      });
      if (!maskNode) {
        throw new Error("create_node_failed");
      }
      kernel.dispatch({
        type: "set-spec",
        id: maskNode.id,
        patch: { assetId: mask.id, fit: "contain" },
      });
      kernel.dispatch({ type: "rename", id: maskNode.id, title: "蒙版" });
      kernel.createEdge(maskNode.id, "out", child.id, "ref");
      onCommit();
      onClose();
    } catch (e) {
      setError((e as Error).message || "mask_upload_failed");
    } finally {
      setBusy(false);
    }
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
        <br />
        确认后会创建「原图 + 蒙版」双参考的编辑节点（蒙版会作为真实素材入库）。
      </p>
      {error && (
        <p className="ic-error" style={{ fontSize: 12 }}>
          {error === "empty_mask"
            ? "蒙版是空的：请先涂抹需要重绘的区域"
            : `失败：${error}`}
        </p>
      )}
      {busy && (
        <p className="ic-dim" style={{ fontSize: 12 }}>
          正在上传蒙版…
        </p>
      )}
    </Modal>
  );
}

/**
 * 判断蒙版是否为空。
 *
 * 采样而不是逐像素：512×N 的画布逐像素读在低端设备上会卡住主线程，
 * 而「有没有画过」只需要知道有没有出现有 alpha 的像素。
 * 采样步长 8 意味着最小可检测涂改约 8×8 像素，远小于任何有意义的笔刷。
 */
function isBlankMask(canvas: HTMLCanvasElement): boolean {
  const ctx = canvas.getContext("2d");
  if (!ctx) return true;
  const { width, height } = canvas;
  const data = ctx.getImageData(0, 0, width, height).data;
  for (let y = 0; y < height; y += 8) {
    for (let x = 0; x < width; x += 8) {
      if (data[(y * width + x) * 4 + 3] > 8) return false;
    }
  }
  return true;
}
