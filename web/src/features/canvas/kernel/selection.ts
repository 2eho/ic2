import type { Selection } from "./types";

/**
 * 选区合并的纯函数层。
 *
 * 为什么单独一个文件：选区的**语义**（追加 / 反选 / 去重）值得有唯一实现，
 * 且它是纯函数、可单测；内核只负责把结果写进自己的状态并通知订阅者。
 *
 * 背景（曾出现的假绿）：状态机 `InteractionMachine` 产出了 `additive`，
 * 但 `CanvasKernel.handleIntent` 的 `select` 分支直接 `this.selection = intent.selection`，
 * 把它丢掉了 —— 于是 Shift 点击节点会静默覆盖选区，用户根本堆不出多选，
 * 多选工具栏（一键对齐 / 等间距 / 分层成列）也就永远只对 1 个节点可见。
 * 功能「存在」但用户「到不了」，这正是本仓最忌讳的假绿。
 */

/** 并集（保持既有顺序在前，追加项去重）。 */
export function unionSelection(base: Selection, added: Selection): Selection {
  const nodes = [...base.nodes];
  for (const id of added.nodes) if (!nodes.includes(id)) nodes.push(id);
  const edges = [...base.edges];
  for (const id of added.edges) if (!edges.includes(id)) edges.push(id);
  return { nodes, edges };
}

/**
 * 反选：`toggled` 中的节点如果在选区内则移除，否则追加。
 * 只对节点生效（连线的追加/取消仍走 `unionSelection`）。
 */
export function toggleSelectionNodes(
  base: Selection,
  toggled: Selection,
): Selection {
  let nodes = base.nodes.filter((id) => !toggled.nodes.includes(id));
  for (const id of toggled.nodes)
    if (!base.nodes.includes(id) && !nodes.includes(id)) nodes.push(id);
  return { nodes, edges: [...base.edges] };
}

/**
 * Shift 点击的最终语义：命中的节点**全部**已在选区内 → 反选，否则并入。
 * 把这条判断收敛到纯函数里，避免内核分支里散落 if。
 */
export function applyAdditive(base: Selection, incoming: Selection): Selection {
  const allSelected =
    incoming.nodes.length > 0 &&
    incoming.nodes.every((id) => base.nodes.includes(id));
  return allSelected
    ? toggleSelectionNodes(base, incoming)
    : unionSelection(base, incoming);
}
