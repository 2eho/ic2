import type { Op, RawEdge, RawNode, Viewport } from './types';

/** 一条可撤销记录：正向 op 与其反向 op，以及恢复所需的节点/边快照。 */
export interface UndoEntry {
  label: string;
  ops: Op[];
  inverse: Op[];
  /** 删除操作的还原载荷（op 逆无法表达完整重建时用） */
  removedNodes?: RawNode[];
  removedEdges?: RawEdge[];
  prevViewport?: Viewport;
  at: number;
}

/**
 * 撤销栈：默认 50 步，180ms 内的同类操作合并（对齐原项目 historyRef 防抖）。
 * 见 docs/design/10-parity-matrix.md 3.19。
 */
export class UndoStack {
  private stack: UndoEntry[] = [];
  private index = -1;
  /** 手势标识：同一手势（一次拖拽）内的连续同类 op 合并为一条记录。 */
  private gesture: string | null = null;
  private static readonly MERGE_WINDOW_MS = 180;

  constructor(private readonly limit = 50) {}

  get canUndo(): boolean {
    return this.index >= 0;
  }

  get canRedo(): boolean {
    return this.index < this.stack.length - 1;
  }

  get depth(): number {
    return this.stack.length;
  }

  /**
   * 开始一个新手势（如 pointerdown 拖拽开始）。
   * 手势内 push 的同类 op 会合并为一条撤销记录；手势结束必须调用 endGesture。
   */
  beginGesture(name: string): void {
    this.gesture = name;
  }

  endGesture(): void {
    this.gesture = null;
  }

  push(entry: UndoEntry): void {
    // 丢弃 redo 分支
    this.stack = this.stack.slice(0, this.index + 1);
    const last = this.stack[this.stack.length - 1];
    // 合并条件：同一手势 + 同类 op + 在时间窗内。刻意要求 gesture 非空，
    // 否则两次独立点击也会被合并成一步（不可接受的撤销粒度）。
    if (this.gesture && last && entry.at - last.at < UndoStack.MERGE_WINDOW_MS && canMerge(last, entry)) {
      last.ops = mergeOps(last.ops, entry.ops);
      last.inverse = entry.inverse;
      last.at = entry.at;
      this.index = this.stack.length - 1;
      return;
    }
    this.stack.push(entry);
    if (this.stack.length > this.limit) {
      this.stack.shift();
    }
    this.index = this.stack.length - 1;
  }

  undo(): UndoEntry | null {
    if (!this.canUndo) return null;
    const entry = this.stack[this.index];
    this.index -= 1;
    return entry;
  }

  redo(): UndoEntry | null {
    if (!this.canRedo) return null;
    this.index += 1;
    return this.stack[this.index];
  }

  clear(): void {
    this.stack = [];
    this.index = -1;
    this.gesture = null;
  }
}

function canMerge(a: UndoEntry, b: UndoEntry): boolean {
  if (a.label !== b.label) return false;
  const kindOf = (ops: Op[]) => ops.map((o) => o.kind).join(',');
  const mergeable = /^(move_node|resize_node|set_viewport)(,)?$/;
  if (!mergeable.test(kindOf(a.ops))) return false;
  return kindOf(a.ops) === kindOf(b.ops);
}

function mergeOps(a: Op[], b: Op[]): Op[] {
  const out = new Map<string, Op>();
  for (const op of a) out.set(op.kind + ':' + ('id' in op ? op.id : ''), op);
  for (const op of b) out.set(op.kind + ':' + ('id' in op ? op.id : ''), op);
  return [...out.values()];
}
