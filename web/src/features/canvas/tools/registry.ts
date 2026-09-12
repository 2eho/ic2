import type { RawNode } from "../kernel/types";

export interface ToolDef {
  id: string;
  labelKey: string;
  /** 适用条件：由节点类型与能力推导，避免巨型 switch */
  applies: (node: RawNode) => boolean;
}

const isImageLike = (n: RawNode) =>
  n.type === "image" ||
  (n.type === "generation" && hasVariant(n)) ||
  n.type.startsWith("plugin:");

const hasVariant = (n: RawNode) => Boolean(n.result?.variants?.length);

/**
 * 画布内图像工具清单（对齐 docs/design/10-parity-matrix.md §5 的 12 项）。
 * 工具是否显示由 applies 决定，配置（显示/隐藏）存 localStorage。
 */
export const IMAGE_TOOLS: ToolDef[] = [
  { id: "view", labelKey: "canvas.tool.viewImage", applies: isImageLike },
  { id: "mask", labelKey: "canvas.tool.mask", applies: isImageLike },
  { id: "crop", labelKey: "canvas.tool.crop", applies: isImageLike },
  { id: "split", labelKey: "canvas.tool.split", applies: isImageLike },
  { id: "upscale", labelKey: "canvas.tool.upscale", applies: isImageLike },
  { id: "superRes", labelKey: "canvas.tool.superRes", applies: isImageLike },
  {
    id: "multiAngle",
    labelKey: "canvas.tool.multiAngle",
    applies: isImageLike,
  },
  {
    id: "reversePrompt",
    labelKey: "canvas.tool.reversePrompt",
    applies: isImageLike,
  },
  {
    id: "videoFrame",
    labelKey: "canvas.tool.videoFrame",
    applies: (n) => n.type === "video" || hasVideoVariant(n),
  },
];

function hasVideoVariant(n: RawNode): boolean {
  return Boolean(n.result?.variants?.some((v) => v.kind === "video"));
}

const HIDDEN_KEY = "ic.canvas.hiddenTools";

/** 用户可自定义显示哪些工具（持久化到 localStorage，对齐原项目行为）。 */
export function hiddenTools(): Set<string> {
  try {
    const raw = localStorage.getItem(HIDDEN_KEY);
    if (!raw) return new Set();
    return new Set(JSON.parse(raw) as string[]);
  } catch {
    return new Set();
  }
}

export function setToolHidden(toolId: string, hidden: boolean): void {
  const set = hiddenTools();
  if (hidden) set.add(toolId);
  else set.delete(toolId);
  try {
    localStorage.setItem(HIDDEN_KEY, JSON.stringify([...set]));
  } catch {
    // 隐私模式下忽略
  }
}

export function imageToolsFor(node: RawNode): ToolDef[] {
  const hidden = hiddenTools();
  return IMAGE_TOOLS.filter((t) => t.applies(node) && !hidden.has(t.id));
}

/** 悬浮工具条的通用能力判定（info/retry/saveAsset 等）。 */
export function canUseTool(tool: string, node: RawNode): boolean {
  switch (tool) {
    case "retry":
      return node.state === "failed" || Boolean(node.result);
    case "saveAsset":
      return Boolean(node.spec.assetId || node.result?.variants?.length);
    case "editText":
      return node.type === "prompt" || node.type === "text";
    default:
      return true;
  }
}
