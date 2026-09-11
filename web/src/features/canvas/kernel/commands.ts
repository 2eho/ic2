import type { Op, Rect, Selection, Viewport } from './types';

/**
 * 命令 → op 的转换层（docs/design/04 §3.1）。
 * UI 只发命令，内核负责批量合并与提交；undo 栈记录反向 op。
 */
export type Command =
  | { type: 'move-nodes'; ids: string[]; dx: number; dy: number }
  | { type: 'resize-node'; id: string; rect: Rect; keepAspect: boolean }
  | { type: 'add-node'; node: import('./types').RawNode }
  | { type: 'delete-nodes'; ids: string[] }
  | { type: 'duplicate-nodes'; ids: string[]; offsetX: number; offsetY: number; newIds: Record<string, string> }
  | { type: 'connect'; edge: import('./types').RawEdge }
  | { type: 'disconnect'; edgeIds: string[] }
  | { type: 'rename'; id: string; title: string }
  | { type: 'set-spec'; id: string; patch?: Record<string, unknown>; unset?: string[] }
  | { type: 'group'; nodeIds: string[]; groupId: string }
  | { type: 'ungroup'; groupId: string }
  | { type: 'set-parent'; id: string; parentId?: string }
  | { type: 'set-viewport'; viewport: Viewport }
  | { type: 'set-settings'; settings: Partial<import('./types').CanvasSettings> }
  | { type: 'set-state'; id: string; state: import('./types').NodeState; result?: import('./types').NodeResult; error?: { code: string; message: string } };

/** 命令 → op 列表。纯函数，便于单测与批量合并。 */
export function commandToOps(cmd: Command): Op[] {
  switch (cmd.type) {
    case 'move-nodes':
      return cmd.ids.map((id) => ({ kind: 'move_node' as const, id, x: cmd.dx, y: cmd.dy, delta: true }));
    case 'resize-node':
      return [{ kind: 'resize_node', id: cmd.id, w: cmd.rect.w, h: cmd.rect.h, keepAspect: cmd.keepAspect }];
    case 'add-node':
      return [{ kind: 'add_node', node: cmd.node }];
    case 'delete-nodes':
      return cmd.ids.map((id) => ({ kind: 'remove_node' as const, id, cascade: true }));
    case 'duplicate-nodes':
      return []; // 由调用方展开为 add_node + add_edge（需要重映射边端点）
    case 'connect':
      return [{ kind: 'add_edge', edge: cmd.edge }];
    case 'disconnect':
      return cmd.edgeIds.map((id) => ({ kind: 'remove_edge' as const, id }));
    case 'rename':
      return [{ kind: 'set_title', id: cmd.id, title: cmd.title }];
    case 'set-spec':
      return [{ kind: 'set_spec', id: cmd.id, patch: cmd.patch, unset: cmd.unset }];
    case 'group':
      return [{ kind: 'group', nodeIds: cmd.nodeIds, groupId: cmd.groupId }];
    case 'ungroup':
      return [{ kind: 'ungroup', groupId: cmd.groupId, keepChildren: true }];
    case 'set-parent':
      return [{ kind: 'set_parent', id: cmd.id, parentId: cmd.parentId }];
    case 'set-viewport':
      return [{ kind: 'set_viewport', viewport: cmd.viewport }];
    case 'set-settings':
      return [{ kind: 'set_settings', settings: cmd.settings }];
    case 'set-state':
      return [{ kind: 'set_state', id: cmd.id, state: cmd.state, result: cmd.result, error: cmd.error }];
    default:
      return [];
  }
}

/**
 * op 批量合并：把同一帧内对同一节点的同类 op 合并，减少请求体积（见 11 §2.3）。
 * 语义安全的前提：移动是 delta 累积、其余同类 op 以后者覆盖前者。
 */
export function coalesceOps(ops: Op[]): Op[] {
  const out: Op[] = [];
  const moveAcc = new Map<string, { x: number; y: number }>();
  const resizeLast = new Map<string, Op>();
  const viewportIdx = new Map<number, number>();

  for (const op of ops) {
    switch (op.kind) {
      case 'move_node': {
        const prev = moveAcc.get(op.id) ?? { x: 0, y: 0 };
        moveAcc.set(op.id, { x: prev.x + (op.delta ? op.x : 0), y: prev.y + (op.delta ? op.y : 0) });
        break;
      }
      case 'resize_node':
        resizeLast.set(op.id, op);
        break;
      case 'set_viewport': {
        // 视口 op 无法合并为多个，只保留最后一个
        const idx = out.findIndex((o) => o.kind === 'set_viewport');
        if (idx >= 0) out[idx] = op;
        else out.push(op);
        viewportIdx.set(0, 0);
        break;
      }
      default:
        out.push(op);
    }
  }
  for (const [id, d] of moveAcc) {
    if (d.x !== 0 || d.y !== 0) out.push({ kind: 'move_node', id, x: d.x, y: d.y, delta: true });
  }
  for (const op of resizeLast.values()) out.push(op);
  return out;
}

/** 从一批命令推导撤销所需的命令（内核在本地已应用的前提下使用）。 */
export function invertOpsForUndo(
  ops: Op[],
  snapshot: { rectOf: (id: string) => Rect | undefined; nodeOf: (id: string) => import('./types').RawNode | undefined; viewport: Viewport },
): Op[] {
  const inverse: Op[] = [];
  // 逆序生成，保证语义正确
  for (let i = ops.length - 1; i >= 0; i--) {
    const op = ops[i];
    switch (op.kind) {
      case 'move_node': {
        const rect = snapshot.rectOf(op.id);
        if (rect) inverse.push({ kind: 'move_node', id: op.id, x: rect.x, y: rect.y });
        break;
      }
      case 'resize_node': {
        const rect = snapshot.rectOf(op.id);
        if (rect) inverse.push({ kind: 'resize_node', id: op.id, w: rect.w, h: rect.h });
        break;
      }
      case 'set_title': {
        const n = snapshot.nodeOf(op.id);
        if (n) inverse.push({ kind: 'set_title', id: op.id, title: n.title });
        break;
      }
      case 'add_node':
        inverse.push({ kind: 'remove_node', id: op.node.id, cascade: true });
        break;
      case 'remove_node':
        break; // 删除的逆需要完整快照，由 UndoStack 负责
      case 'add_edge':
        inverse.push({ kind: 'remove_edge', id: op.edge.id });
        break;
      case 'remove_edge':
        break;
      case 'set_viewport':
        inverse.push({ kind: 'set_viewport', viewport: snapshot.viewport });
        break;
      default:
        break;
    }
  }
  return inverse;
}

/** 选择集工具：additive 表示为「追加」而不是替换（对齐原项目 Shift 语义）。 */
export function applySelection(
  current: Selection,
  hit: { nodes?: string[]; edges?: string[] },
  additive: boolean,
): Selection {
  if (!additive) {
    return { nodes: hit.nodes ?? [], edges: hit.edges ?? [] };
  }
  const nodes = new Set(current.nodes);
  const edges = new Set(current.edges);
  for (const id of hit.nodes ?? []) {
    if (nodes.has(id)) nodes.delete(id);
    else nodes.add(id);
  }
  for (const id of hit.edges ?? []) {
    if (edges.has(id)) edges.delete(id);
    else edges.add(id);
  }
  return { nodes: [...nodes], edges: [...edges] };
}
