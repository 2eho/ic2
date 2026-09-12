import { useRef } from "react";
import { api } from "@/shared/api";
import type { CanvasKernel } from "../kernel";
import { NODE_SCHEMAS } from "../kernel/schema";
import { useKernelSelection } from "../hooks/useKernel";
import { useWorkspace } from "@/shared/session/workspace";
import type { TFn } from "@/app/App";

interface Props {
  t: TFn;
  kernel: CanvasKernel;
  onCommit: () => void;
  onCreateNode: (type: string) => void;
}

/** 底部 Dock：撤销重做、节点创建、上传、外观、删除/清空（对齐 docs/design/10 §3.12）。 */
export function CanvasToolbar({ t, kernel, onCommit, onCreateNode }: Props) {
  const fileRef = useRef<HTMLInputElement>(null);
  const selection = useKernelSelection(kernel);
  const { workspaceId } = useWorkspace();

  const upload = async (files: FileList | null) => {
    if (!files || files.length === 0) return;
    // 多文件 40px 错位排布（对齐原项目 handleDrop 语义）
    let offset = 0;
    for (const file of Array.from(files)) {
      const asset = await api.uploadAsset(workspaceId, file, file.name);
      const kind =
        asset.kind === "image"
          ? "image"
          : asset.kind === "video"
            ? "video"
            : asset.kind === "audio"
              ? "audio"
              : "image";
      const host = document.querySelector(
        "[data-canvas-host]",
      ) as HTMLElement | null;
      const rect = host?.getBoundingClientRect();
      const center = kernel.viewport.toWorld({
        x: (rect?.width ?? 800) / 2 + offset,
        y: (rect?.height ?? 600) / 2 + offset,
      });
      const node = kernel.createNode(kind, center);
      if (node) {
        kernel.dispatch({
          type: "set-spec",
          id: node.id,
          patch: {
            assetId: asset.id,
            mime: asset.mime,
            bytes: asset.size,
            naturalW: asset.meta?.width ?? 0,
            naturalH: asset.meta?.height ?? 0,
          },
        });
      }
      offset += 40;
    }
    onCommit();
  };

  // 上传替换：选中单个媒体节点时，第一个文件替换它，其余新建（对齐原项目 §3.23）
  const uploadReplacing = async (files: FileList | null) => {
    if (!files || files.length === 0) return;
    const targetId = selection.nodes[0];
    const target = targetId ? kernel.scene.getNode(targetId) : null;
    if (target && ["image", "video", "audio"].includes(target.type)) {
      const asset = await api.uploadAsset(workspaceId, files[0], files[0].name);
      kernel.dispatch({
        type: "set-spec",
        id: target.id,
        patch: {
          assetId: asset.id,
          mime: asset.mime,
          bytes: asset.size,
          naturalW: asset.meta?.width ?? 0,
          naturalH: asset.meta?.height ?? 0,
        },
      });
      const rest = Array.from(files).slice(1);
      if (rest.length) {
        const dt = new DataTransfer();
        rest.forEach((f) => dt.items.add(f));
        await upload(dt.files);
      }
      onCommit();
      return;
    }
    await upload(files);
  };

  return (
    <div
      style={{
        position: "absolute",
        left: "50%",
        bottom: 16,
        transform: "translateX(-50%)",
        display: "flex",
        gap: 4,
        padding: 6,
        borderRadius: 12,
        background: "var(--ic-surface)",
        border: "1px solid var(--ic-border)",
        boxShadow: "var(--ic-shadow)",
        alignItems: "center",
        flexWrap: "nowrap",
      }}
      onPointerDown={(e) => e.stopPropagation()}
    >
      <button
        className="ic-btn ic-btn--ghost"
        title={t("canvas.undo")}
        onClick={() => {
          if (kernel.undoOnce()) onCommit();
        }}
      >
        ↶
      </button>
      <button
        className="ic-btn ic-btn--ghost"
        title={t("canvas.redo")}
        onClick={() => {
          if (kernel.redoOnce()) onCommit();
        }}
      >
        ↷
      </button>
      <span style={{ width: 1, height: 20, background: "var(--ic-border)" }} />

      {["prompt", "image", "video", "audio", "generation", "group"].map(
        (type) => (
          <button
            key={type}
            className="ic-btn ic-btn--ghost"
            title={NODE_SCHEMAS[type]?.title}
            onClick={() => onCreateNode(type)}
          >
            {t(`canvas.node.${type}`)}
          </button>
        ),
      )}

      <span style={{ width: 1, height: 20, background: "var(--ic-border)" }} />
      <button
        className="ic-btn ic-btn--ghost"
        onClick={() => fileRef.current?.click()}
      >
        {t("canvas.uploadToCanvas")}
      </button>
      <input
        ref={fileRef}
        type="file"
        multiple
        hidden
        accept="image/*,video/*,audio/*"
        onChange={(e) => {
          void uploadReplacing(e.target.files);
          e.target.value = "";
        }}
      />

      <button
        className="ic-btn ic-btn--ghost"
        title={t("canvas.background")}
        onClick={() => {
          const order = ["dots", "lines", "blank"] as const;
          const next =
            order[
              (order.indexOf(kernel.settings.background as "dots") + 1) %
                order.length
            ];
          kernel.dispatch({
            type: "set-settings",
            settings: { background: next },
          });
          onCommit();
        }}
      >
        {t("canvas.background")}
      </button>
      <button
        className="ic-btn ic-btn--ghost"
        title={t("canvas.imageInfo")}
        onClick={() => {
          kernel.dispatch({
            type: "set-settings",
            settings: { imageInfo: !kernel.settings.imageInfo },
          });
          onCommit();
        }}
      >
        {kernel.settings.imageInfo ? "ℹ" : "ⓘ"}
      </button>

      <span style={{ width: 1, height: 20, background: "var(--ic-border)" }} />
      <button
        className="ic-btn ic-btn--ghost"
        disabled={selection.nodes.length < 1}
        title={t("canvas.groupSelected")}
        onClick={() => {
          const group = kernel.createNode("group", {
            x:
              Math.min(
                ...selection.nodes.map(
                  (id) => kernel.scene.getNode(id)?.rect.x ?? 0,
                ),
              ) - 24,
            y:
              Math.min(
                ...selection.nodes.map(
                  (id) => kernel.scene.getNode(id)?.rect.y ?? 0,
                ),
              ) - 52,
          });
          if (group) {
            kernel.dispatch({
              type: "group",
              nodeIds: selection.nodes,
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
        disabled={
          selection.nodes.length === 1 &&
          kernel.scene.getNode(selection.nodes[0])?.type !== "group"
        }
        onClick={() => {
          const g = kernel.scene.getNode(selection.nodes[0]);
          if (g?.type === "group") {
            kernel.dispatch({ type: "ungroup", groupId: g.id });
            onCommit();
          }
        }}
      >
        {t("canvas.ungroupSelected")}
      </button>
      <button
        className="ic-btn ic-btn--ghost ic-btn--danger"
        disabled={selection.nodes.length === 0}
        onClick={() => {
          kernel.dispatch({ type: "delete-nodes", ids: selection.nodes });
          onCommit();
        }}
      >
        {t("canvas.deleteSelection")}
      </button>
    </div>
  );
}
