import type { Op } from "./types";
import type {
  AlignAnchor,
  AlignMode,
  AlignRect,
  DistributeAxis,
} from "./geometry";
import type { LayoutRect } from "./geometry";
import {
  alignRects,
  distributeRects,
  isLayerLayoutSettled,
  layerLayoutRects,
  type EdgeLike,
} from "./layout";
import type { LayeredLayoutOptions } from "./geometry";
import type { Command } from "./commands";

/**
 * 「布局命令」的门面：把「取数据 → 算位移 → 产出命令」集中一处，
 * 内核只负责 `dispatch`。抽出来的动机有两个：
 *  1. `kernel.ts` 有 800 行硬上限（文件规模门禁），这四个门面是纯搬运；
 *  2. 对齐 / 分布 / 分层的**返回约定**必须一致（无位移就返回空数组，
 *     调用方据此判断按钮置灰与是否 onCommit），集中写一条规则比散落四处可靠。
 *
 * 本模块不 import react、不碰 DOM、不持有状态。
 */

export interface LayoutInputs {
  rects: AlignRect[];
  edges: EdgeLike[];
}

/** 命令构造器由调用方注入（内核的 `dispatch`）。 */
export type Dispatcher = (cmd: Command) => Op[];

/** 一键对齐；无位移（不足 2 个 / 已对齐）返回空，不产生无意义 op。 */
export function runAlign(
  dispatch: Dispatcher,
  rects: AlignRect[],
  mode: AlignMode,
  anchor: AlignAnchor,
): Op[] {
  const offsets = alignRects(rects, mode, anchor);
  return offsets.length === 0 ? [] : dispatch({ type: "align-nodes", offsets });
}

/** 等间距分布；不足 3 个或已等距返回空。 */
export function runDistribute(
  dispatch: Dispatcher,
  rects: AlignRect[],
  axis: DistributeAxis,
): Op[] {
  const offsets = distributeRects(rects, axis);
  return offsets.length === 0
    ? []
    : dispatch({ type: "distribute-nodes", offsets });
}

/** 分层成列；不足 2 个或已成列返回空。 */
export function runLayeredLayout(
  dispatch: Dispatcher,
  inputs: LayoutInputs,
  opts: LayeredLayoutOptions,
): Op[] {
  const offsets = layerLayoutRects(inputs.rects, inputs.edges, opts);
  return offsets.length === 0
    ? []
    : dispatch({ type: "layout-layers", offsets });
}

/** 是否已按层成列（UI 置灰判断，与「点了会不会真动」同源）。 */
export function isLayeredSettled(
  inputs: LayoutInputs,
  opts: LayeredLayoutOptions,
): boolean {
  return isLayerLayoutSettled(inputs.rects, inputs.edges, opts);
}

/** 便于类型复用（内核的 `rectsOf` 返回值）。 */
export type { LayoutRect };
