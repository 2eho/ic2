import { useState } from "react";
import { api } from "@/shared/api";
import type { CanvasKernel } from "../kernel";
import type { RawNode } from "../kernel/types";
import { useWorkspace } from "@/shared/session/workspace";
import { canUseTool, imageToolsFor } from "../tools/registry";
import { ImageToolDialog } from "../tools/ImageToolDialog";
import type { TFn } from "@/app/App";

interface Props {
  t: TFn;
  kernel: CanvasKernel;
  node: RawNode;
  onCommit: () => void;
  onRun: (nodeIds: string[]) => void;
}

/**
 * 节点悬浮工具条。
 * 工具集由「节点类型 + 能力声明」生成，不再是巨型 switch（见 04 §5.1）。
 */
export function NodeHoverToolbar({ t, kernel, node, onCommit, onRun }: Props) {
  const { workspaceId } = useWorkspace();
  const [openTool, setOpenTool] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);

  const tools = imageToolsFor(node);
  const assetId = String(node.spec.assetId ?? "");
  const variantAsset =
    node.result?.variants?.[node.result?.primary ?? 0]?.assetId ?? "";
  const effectiveAsset = assetId || variantAsset;

  const remove = () => {
    kernel.dispatch({ type: "delete-nodes", ids: [node.id] });
    onCommit();
  };

  const copyPrompt = async () => {
    const upstream = kernel.scene.upstreamOf(node.id);
    const texts: string[] = [];
    for (const e of upstream) {
      const src = kernel.scene.getNode(e.from.nodeId);
      if (src?.type === "prompt") texts.push(String(src.spec.text ?? ""));
    }
    const payload = texts.filter(Boolean).join("\n\n");
    if (!payload) return;
    try {
      await navigator.clipboard.writeText(payload);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // 剪贴板不可用时静默失败（用户可见的失败没有替代动作）
    }
  };

  return (
    <div
      style={{
        position: "absolute",
        top: -34,
        left: 0,
        display: "flex",
        gap: 2,
        padding: 3,
        borderRadius: 8,
        background: "var(--ic-surface)",
        border: "1px solid var(--ic-border)",
        boxShadow: "var(--ic-shadow)",
        whiteSpace: "nowrap",
        zIndex: 100,
      }}
      onPointerDown={(e) => e.stopPropagation()}
    >
      <ToolBtn
        label={t("canvas.tool.info")}
        onClick={() => setOpenTool("info")}
      />
      <ToolBtn
        label={t("canvas.tool.copyPrompt")}
        onClick={copyPrompt}
        done={copied}
      />
      {canUseTool("retry", node) && (
        <ToolBtn
          label={t("canvas.tool.retry")}
          onClick={() => onRun([node.id])}
        />
      )}
      {effectiveAsset && canUseTool("saveAsset", node) && (
        <ToolBtn
          label={t("canvas.tool.saveAsset")}
          onClick={async () => {
            const res = await fetch(
              api.assetRawUrl(effectiveAsset, workspaceId),
            );
            const blob = await res.blob();
            await api.uploadAsset(
              workspaceId,
              blob,
              `${node.title || "asset"}.png`,
            );
          }}
        />
      )}
      {effectiveAsset && (
        <a
          className="ic-btn ic-btn--ghost"
          style={{ padding: "2px 6px", fontSize: 11, textDecoration: "none" }}
          href={api.assetRawUrl(effectiveAsset, workspaceId)}
          download
        >
          {t("canvas.tool.download")}
        </a>
      )}
      {tools.map((tool) => (
        <ToolBtn
          key={tool.id}
          label={t(tool.labelKey)}
          onClick={() => setOpenTool(tool.id)}
        />
      ))}
      <ToolBtn label={t("canvas.tool.delete")} danger onClick={remove} />

      {openTool === "info" && (
        <ImageToolDialog
          t={t}
          tool="info"
          kernel={kernel}
          node={node}
          onClose={() => setOpenTool(null)}
          onCommit={onCommit}
        />
      )}
      {openTool && openTool !== "info" && (
        <ImageToolDialog
          t={t}
          tool={openTool}
          kernel={kernel}
          node={node}
          onClose={() => setOpenTool(null)}
          onCommit={onCommit}
        />
      )}
    </div>
  );
}

function ToolBtn({
  label,
  onClick,
  danger,
  done,
}: {
  label: string;
  onClick: () => void;
  danger?: boolean;
  done?: boolean;
}) {
  return (
    <button
      className={`ic-btn ic-btn--ghost ${danger ? "ic-btn--danger" : ""}`}
      style={{ padding: "2px 6px", fontSize: 11 }}
      onClick={onClick}
      title={label}
    >
      {done ? "✓" : label}
    </button>
  );
}
