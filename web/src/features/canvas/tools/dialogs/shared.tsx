import type { ReactNode } from "react";
import type { TFn } from "@/app/App";
import type { CanvasKernel } from "../../kernel";
import type { RawNode } from "../../kernel/types";

/**
 * 图像工具对话框的公共契约。
 *
 * 从 ImageToolDialog.tsx 抽出（原文件 888 行，超过代码规模硬上限）：
 * 对话框按用途分组到 dialogs/ 下，共享这一份 props 定义。
 * 分组的依据是**行为特征**而不是行数：
 *   - info      只读展示
 *   - transform 纯前端像素运算（裁剪/切图/放大）
 *   - generative 需要调用模型（多角度/反推/蒙版）
 *   - media     媒体抽取（视频截帧）
 */
export interface CommonProps {
  t: TFn;
  kernel: CanvasKernel;
  node: RawNode;
  assetId: string;
  workspaceId: string;
  natural: { w: number; h: number };
  onClose: () => void;
  onCommit: () => void;
}

/** 对话框外壳：遮罩 + 卡片 + 关闭按钮。所有图像工具共用。 */
export function Modal({
  title,
  onClose,
  children,
  wide,
}: {
  title: string;
  onClose: () => void;
  children: ReactNode;
  wide?: boolean;
}) {
  return (
    <div
      style={{
        position: "fixed",
        inset: 0,
        background: "rgba(0,0,0,.35)",
        display: "grid",
        placeItems: "center",
        zIndex: 1000,
      }}
      onPointerDown={(e) => e.stopPropagation()}
      onClick={onClose}
    >
      <div
        className="ic-card"
        style={{
          width: wide ? "80vw" : 520,
          maxWidth: "92vw",
          maxHeight: "88vh",
          overflow: "auto",
          padding: 16,
        }}
        onClick={(e) => e.stopPropagation()}
      >
        <div
          style={{
            display: "flex",
            justifyContent: "space-between",
            alignItems: "center",
            marginBottom: 12,
          }}
        >
          <strong>{title}</strong>
          <button className="ic-btn ic-btn--ghost" onClick={onClose}>
            ✕
          </button>
        </div>
        {children}
      </div>
    </div>
  );
}

/** 折叠 data URI，避免在信息面板里把整段 base64 铺满屏幕。 */
export function collapseDataURIs(
  spec: Record<string, unknown>,
): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  for (const [k, v] of Object.entries(spec)) {
    out[k] =
      typeof v === "string" && v.startsWith("data:")
        ? `<data-uri ${v.length} bytes>`
        : v;
  }
  return out;
}
