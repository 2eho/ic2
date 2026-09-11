import { useEffect, useRef } from "react";
import { api } from "@/shared/api";
import type { CanvasKernel } from "../kernel";
import { NODE_SCHEMAS } from "../kernel/schema";
import { useWorkspace } from "@/shared/session/workspace";
import type { TFn } from "@/app/App";

interface Props {
  t: TFn;
  x: number;
  y: number;
  nodeId?: string;
  kernel: CanvasKernel;
  onClose: () => void;
  onCommit: () => void;
  onRun: (nodeIds: string[]) => void;
}

/**
 * 右键菜单：空白处为节点创建菜单，节点上为节点操作菜单
 * （对齐 docs/design/10 §3.8/§3.9）。
 */
export function ContextMenu({
  t,
  x,
  y,
  nodeId,
  kernel,
  onClose,
  onCommit,
  onRun,
}: Props) {
  const ref = useRef<HTMLDivElement>(null);
  const { workspaceId } = useWorkspace();

  useEffect(() => {
    const onDown = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) onClose();
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    document.addEventListener("mousedown", onDown);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onDown);
      document.removeEventListener("keydown", onKey);
    };
  }, [onClose]);

  const node = nodeId ? kernel.scene.getNode(nodeId) : null;

  return (
    <div
      ref={ref}
      className="ic-card"
      style={{
        position: "fixed",
        left: x,
        top: y,
        zIndex: 500,
        padding: 4,
        minWidth: 180,
      }}
      onPointerDown={(e) => e.stopPropagation()}
    >
      {node ? (
        <>
          <MenuItem
            label={t("common.copy")}
            onClick={() => {
              kernel.duplicateNodes([node.id]);
              onCommit();
              onClose();
            }}
          />
          {node.type !== "group" && (
            <MenuItem
              label={t("canvas.groupSelected")}
              onClick={() => {
                const g = kernel.createNode("group", {
                  x: node.rect.x - 24,
                  y: node.rect.y - 52,
                });
                if (g) {
                  kernel.dispatch({
                    type: "group",
                    nodeIds: [node.id],
                    groupId: g.id,
                  });
                  onCommit();
                }
                onClose();
              }}
            />
          )}
          {node.type === "group" && (
            <MenuItem
              label={t("canvas.ungroupSelected")}
              onClick={() => {
                kernel.dispatch({ type: "ungroup", groupId: node.id });
                onCommit();
                onClose();
              }}
            />
          )}
          {node.type === "generation" && (
            <MenuItem
              label={t("canvas.tool.generate")}
              onClick={() => {
                onRun([node.id]);
                onClose();
              }}
            />
          )}
          {node.type === "video" && (
            <MenuItem
              label={t("canvas.tool.videoFrame")}
              onClick={() => {
                const child = kernel.createNode("image", {
                  x: node.rect.x + node.rect.w + 60,
                  y: node.rect.y,
                });
                if (child) {
                  kernel.dispatch({
                    type: "set-spec",
                    id: child.id,
                    patch: { frameAt: 0, sourceAssetId: node.spec.assetId },
                  });
                  kernel.createEdge(node.id, "out", child.id, "in");
                  onCommit();
                }
                onClose();
              }}
            />
          )}
          <MenuItem
            label={t("canvas.tool.saveAsset")}
            onClick={async () => {
              const assetId =
                String(node.spec.assetId ?? "") ||
                node.result?.variants?.[0]?.assetId;
              if (assetId) {
                const res = await fetch(api.assetRawUrl(assetId, workspaceId));
                const blob = await res.blob();
                await api.uploadAsset(
                  workspaceId,
                  blob,
                  `${node.title || "asset"}`,
                );
              }
              onClose();
            }}
          />
          <MenuItem
            label={t("common.delete")}
            danger
            onClick={() => {
              kernel.dispatch({ type: "delete-nodes", ids: [node.id] });
              onCommit();
              onClose();
            }}
          />
        </>
      ) : (
        <>
          {Object.entries(NODE_SCHEMAS).map(([type, schema]) => (
            <MenuItem
              key={type}
              label={schema.title}
              onClick={() => {
                const host = document.querySelector(
                  "[data-canvas-host]",
                ) as HTMLElement | null;
                const rect = host?.getBoundingClientRect();
                const world = kernel.viewport.toWorld({
                  x: x - (rect?.left ?? 0),
                  y: y - (rect?.top ?? 0),
                });
                kernel.createNode(type, world);
                onCommit();
                onClose();
              }}
            />
          ))}
        </>
      )}
    </div>
  );
}

function MenuItem({
  label,
  onClick,
  danger,
}: {
  label: string;
  onClick: () => void;
  danger?: boolean;
}) {
  return (
    <button
      className={`ic-btn ic-btn--ghost ${danger ? "ic-btn--danger" : ""}`}
      style={{
        display: "block",
        width: "100%",
        textAlign: "left",
        padding: "6px 10px",
        fontSize: 13,
      }}
      onClick={onClick}
    >
      {label}
    </button>
  );
}
