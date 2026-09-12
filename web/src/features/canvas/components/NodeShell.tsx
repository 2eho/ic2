import { memo, useEffect, useRef, useState } from "react";
import type { CanvasKernel } from "../kernel";
import { useKernelNode, useKernelSelection } from "../hooks/useKernel";
import type { ResourceKind, RawNode } from "../kernel/types";
import { NodeContent } from "./NodeContent";
import { NodeHoverToolbar } from "./NodeHoverToolbar";
import type { TFn } from "@/app/App";

interface Props {
  t: TFn;
  kernel: CanvasKernel;
  nodeId: string;
  onCommit: () => void;
  onRun: (nodeIds: string[]) => void;
}

/**
 * 节点外壳：只订阅自己这个节点与选择集，父级重渲染不必然带动它（见 04 §5.1）。
 * 端口锚点由内核计算位置，这里只渲染。
 */
export const NodeShell = memo(function NodeShell({
  t,
  kernel,
  nodeId,
  onCommit,
  onRun,
}: Props) {
  const node = useKernelNode(kernel, nodeId);
  const selection = useKernelSelection(kernel);
  const [editing, setEditing] = useState(false);
  const [hover, setHover] = useState(false);
  const [draftTitle, setDraftTitle] = useState("");
  const inputRef = useRef<HTMLInputElement>(null);

  const selected = selection.nodes.includes(nodeId);

  useEffect(() => {
    if (editing) inputRef.current?.focus();
  }, [editing]);

  if (!node) return null;

  const statusClass =
    node.state === "failed"
      ? "ic-badge--danger"
      : node.state === "running" || node.state === "pending"
        ? "ic-badge--warn"
        : node.state === "succeeded"
          ? "ic-badge--ok"
          : "";

  return (
    <div
      data-node-id={nodeId}
      style={{
        position: "absolute",
        transform: `translate3d(${node.rect.x}px, ${node.rect.y}px, 0)`,
        width: node.rect.w,
        height: node.rect.h,
        zIndex: node.z,
        borderRadius: 10,
        border: `1px solid ${selected ? "var(--ic-accent)" : "var(--ic-border)"}`,
        background: node.type === "group" ? "transparent" : "var(--ic-surface)",
        boxShadow: node.type === "group" ? "none" : "var(--ic-shadow)",
        display: "flex",
        flexDirection: "column",
        overflow: "hidden",
      }}
      onMouseEnter={() => setHover(true)}
      onMouseLeave={() => setHover(false)}
    >
      {/* 顶栏：标题 + 状态 */}
      <div
        style={{
          display: "flex",
          alignItems: "center",
          gap: 6,
          padding: "6px 8px",
          borderBottom:
            node.type === "group" ? "none" : "1px solid var(--ic-border)",
          background:
            node.type === "group"
              ? "color-mix(in srgb, var(--ic-border) 30%, transparent)"
              : "var(--ic-surface-2)",
          cursor: "move",
        }}
        onDoubleClick={() => {
          setDraftTitle(node.title);
          setEditing(true);
        }}
      >
        <span style={{ fontSize: 11, color: "var(--ic-text-dim)" }}>
          {t(`canvas.node.${kindKey(node)}`)}
        </span>
        {editing ? (
          <input
            ref={inputRef}
            className="ic-input"
            style={{ height: 22, padding: "0 6px", fontSize: 12 }}
            value={draftTitle}
            onChange={(e) => setDraftTitle(e.target.value)}
            onBlur={() => {
              kernel.dispatch({
                type: "rename",
                id: nodeId,
                title: draftTitle,
              });
              setEditing(false);
              onCommit();
            }}
            onKeyDown={(e) => {
              if (e.key === "Enter") (e.target as HTMLInputElement).blur();
              if (e.key === "Escape") setEditing(false);
            }}
          />
        ) : (
          <strong
            style={{
              fontSize: 12,
              flex: 1,
              overflow: "hidden",
              textOverflow: "ellipsis",
              whiteSpace: "nowrap",
            }}
          >
            {node.title}
          </strong>
        )}
        {node.state !== "idle" && (
          <span className={`ic-badge ${statusClass}`} style={{ fontSize: 10 }}>
            {node.state}
          </span>
        )}
      </div>

      {/* 内容区：按类型分发 */}
      <div style={{ flex: 1, minHeight: 0, position: "relative" }}>
        <NodeContent
          t={t}
          kernel={kernel}
          node={node}
          onCommit={onCommit}
          onRun={onRun}
        />
        {node.state === "running" && (
          <div
            style={{
              position: "absolute",
              inset: 0,
              display: "grid",
              placeItems: "center",
              background:
                "color-mix(in srgb, var(--ic-surface) 70%, transparent)",
            }}
          >
            <span className="ic-dim">{t("common.loading")}</span>
          </div>
        )}
        {node.error && (
          <div
            className="ic-error"
            style={{
              position: "absolute",
              bottom: 0,
              left: 0,
              right: 0,
              padding: 6,
              fontSize: 11,
              background: "var(--ic-surface)",
            }}
          >
            {t(`errors.${node.error.code}`)}
          </div>
        )}
      </div>

      {/* 端口锚点 */}
      <PortAnchors node={node} />

      {/* 悬浮工具条：按节点类型条件渲染 */}
      {hover && !kernel.settings.readOnly && (
        <NodeHoverToolbar
          t={t}
          kernel={kernel}
          node={node}
          onCommit={onCommit}
          onRun={onRun}
        />
      )}
    </div>
  );
});

function kindKey(node: RawNode): string {
  if (node.type.includes(":")) return "generation";
  return node.type;
}

/** 端口锚点：输入在左、输出在右，垂直均分。 */
function PortAnchors({ node }: { node: RawNode }) {
  const inputs = node.ports.inputs;
  const outputs = node.ports.outputs;
  const dot: React.CSSProperties = {
    position: "absolute",
    width: 10,
    height: 10,
    borderRadius: "50%",
    background: "var(--ic-surface)",
    border: "1.5px solid var(--ic-accent)",
  };
  return (
    <>
      {inputs.map((p, i) => (
        <span
          key={"in-" + p.id}
          data-port={p.id}
          data-port-kind={p.kind}
          title={p.name}
          style={{
            ...dot,
            left: -5,
            top: `${((i + 1) * 100) / (inputs.length + 1)}%`,
          }}
        />
      ))}
      {outputs.map((p, i) => (
        <span
          key={"out-" + p.id}
          data-port={p.id}
          data-port-kind={p.kind}
          title={p.name}
          style={{
            ...dot,
            right: -5,
            top: `${((i + 1) * 100) / (outputs.length + 1)}%`,
          }}
        />
      ))}
    </>
  );
}

export function kindLabel(kind: ResourceKind): string {
  switch (kind) {
    case "image":
      return "图片";
    case "video":
      return "视频";
    case "audio":
      return "音频";
    case "text":
      return "文本";
    default:
      return "文件";
  }
}
