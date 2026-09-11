import { useMemo } from "react";
import { api } from "@/shared/api";
import type { CanvasKernel } from "../kernel";
import type { RawNode } from "../kernel/types";
import { useWorkspace } from "@/shared/session/workspace";
import { Modal } from "./dialogs/shared";
import { InfoDialog } from "./dialogs/info";
import {
  CropDialog,
  SplitDialog,
  UpscaleDialog,
} from "./dialogs/transform";
import { AngleDialog, MaskDialog, ReversePromptDialog } from "./dialogs/generative";
import { VideoFrameDialog } from "./dialogs/media";
import type { TFn } from "@/app/App";

interface Props {
  t: TFn;
  tool: string;
  kernel: CanvasKernel;
  node: RawNode;
  onClose: () => void;
  onCommit: () => void;
}

/**
 * 图像工具对话框集合。
 * 纯几何/文案计算全部委托给 tools/pure.ts（已单测），这里只做交互与提交。
 *
 * 已知回归点：蒙版编辑在**缩放期间**打开会崩（原项目 v0.16.0 修复过）。
 * 这里用 `isNodeResizing` 门控——由内核的交互状态机提供，而不是靠 setTimeout。
 */
export function ImageToolDialog({
  t,
  tool,
  kernel,
  node,
  onClose,
  onCommit,
}: Props) {
  const { workspaceId } = useWorkspace();
  const assetId =
    String(node.spec.assetId ?? "") ||
    (node.result?.variants?.[node.result?.primary ?? 0]?.assetId ?? "");
  const resizing = kernel.interaction.current === "resizing-node";

  const natural = useMemo(
    () => ({
      w: Number(node.spec.naturalW ?? 0) || 1024,
      h: Number(node.spec.naturalH ?? 0) || 1024,
    }),
    [node.spec.naturalW, node.spec.naturalH],
  );

  if (resizing) {
    return (
      <Modal title={t("common.loading")} onClose={onClose}>
        <p className="ic-dim">请先结束缩放操作</p>
      </Modal>
    );
  }

  const common = {
    t,
    kernel,
    node,
    assetId,
    workspaceId,
    natural,
    onClose,
    onCommit,
  };

  switch (tool) {
    case "info":
      return <InfoDialog {...common} />;
    case "crop":
      return <CropDialog {...common} />;
    case "split":
      return <SplitDialog {...common} />;
    case "upscale":
      return <UpscaleDialog {...common} />;
    case "multiAngle":
      return <AngleDialog {...common} />;
    case "reversePrompt":
      return <ReversePromptDialog {...common} />;
    case "videoFrame":
      return <VideoFrameDialog {...common} />;
    case "superRes":
      return (
        <Modal title={t("canvas.tool.superRes")} onClose={onClose}>
          <p className="ic-dim">
            AI 超分需要服务端上采样模型，当前版本尚未接入。
          </p>
        </Modal>
      );
    case "mask":
      return <MaskDialog {...common} />;
    case "view":
      return (
        <Modal title={t("canvas.tool.viewImage")} onClose={onClose} wide>
          {assetId ? (
            <img
              src={api.assetRawUrl(assetId, workspaceId)}
              alt=""
              style={{ maxWidth: "100%" }}
            />
          ) : null}
        </Modal>
      );
    default:
      return null;
  }
}
