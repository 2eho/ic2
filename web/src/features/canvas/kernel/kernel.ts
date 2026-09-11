import { SceneGraph } from './scene';
import { ViewportController } from './viewport';
import { UndoStack, type UndoEntry } from './undo';
import { InteractionMachine, type Intent, type Modifiers } from './interaction';
import { coalesceOps, commandToOps, type Command } from './commands';
import type { CanvasDoc, Op, RawEdge, RawNode, Rect, Selection, Vec2, Viewport } from './types';
import { applyResize } from './geometry';

/**
 * 内核门面：把视口、场景图、交互状态机、命令总线、undo 组合成单一 API。
 *
 * 设计要点：
 * - 视口变更只更新 ViewportController 与一个 CSS transform，不触发 React 重渲染；
 * - 所有写入都走 dispatch(command) → op 队列 → flush(提交服务端)；
 * - 乐观应用先落本地，服务端返回权威版本后对账。
 */
export class CanvasKernel {
  readonly scene = new SceneGraph();
  readonly viewport = new ViewportController();
  readonly undo = new UndoStack(50);
  readonly interaction = new InteractionMachine();

  private doc: CanvasDoc;
  private selection: Selection = { nodes: [], edges: [] };
  private pending: Op[] = [];
  private listeners = new Set<() => void>();
  private viewportListeners = new Set<(v: Viewport) => void>();
  private version: number;

  constructor(doc: CanvasDoc) {
    this.doc = doc;
    this.version = doc.version;
    this.viewport.set(doc.viewport);
    this.scene.load(doc.nodes, doc.edges);
  }

  get currentVersion(): number {
    return this.version;
  }

  get currentSelection(): Selection {
    return { nodes: [...this.selection.nodes], edges: [...this.selection.edges] };
  }

  get settings() {
    return this.doc.settings;
  }

  get documentId(): string {
    return this.doc.id;
  }

  get projectId(): string {
    return this.doc.projectId;
  }

  /** 文档变更订阅（粗粒度：节点/边/版本变化时通知 React）。 */
  subscribe(fn: () => void): () => void {
    this.listeners.add(fn);
    return () => this.listeners.delete(fn);
  }

  /** 视口变更订阅（细粒度：只重写 transform，不触发节点树重渲染）。 */
  subscribeViewport(fn: (v: Viewport) => void): () => void {
    this.viewportListeners.add(fn);
    return () => this.viewportListeners.delete(fn);
  }

  private notify(): void {
    for (const fn of this.listeners) fn();
  }

  private notifyViewport(): void {
    const v = this.viewport.current;
    for (const fn of this.viewportListeners) fn(v);
  }

  /** 用服务端权威文档整体替换（重连/冲突后 re-sync）。 */
  applyAuthoritative(doc: CanvasDoc): void {
    this.doc = doc;
    this.version = doc.version;
    this.scene.load(doc.nodes, doc.edges);
    this.viewport.set(doc.viewport);
    this.pending = [];
    this.notifyViewport();
    this.notify();
  }

  /**
   * 派发命令：本地乐观应用 + 入队 + 记录 undo。
   * 返回本次产生的 op（调用方可据此做本地渲染优化）。
   */
  dispatch(cmd: Command): Op[] {
    if (this.doc.settings.readOnly) return [];
    const ops = commandToOps(cmd);
    if (ops.length === 0 && cmd.type !== 'duplicate-nodes') return [];

    const before = this.snapshotFor(cmd);
    this.applyLocal(ops);
    this.pending.push(...ops);

    const inverse = this.invertOf(cmd, before);
    this.undo.push({
      label: cmd.type,
      ops,
      inverse,
      removedNodes: before.removedNodes,
      removedEdges: before.removedEdges,
      prevViewport: cmd.type === 'set-viewport' ? before.prevViewport : undefined,
      at: Date.now(),
    });

    this.notify();
    return ops;
  }

  /** 取出并清空待提交队列（已合并同帧同类 op）。 */
  takePendingOps(): Op[] {
    const ops = coalesceOps(this.pending);
    this.pending = [];
    return ops;
  }

  get pendingCount(): number {
    return this.pending.length;
  }

  /** 撤销：应用反向 op。 */
  undoOnce(): boolean {
    const entry = this.undo.undo();
    if (!entry) return false;
    this.applyLocal(entry.inverse);
    this.restoreRemoved(entry);
    this.pending.push(...entry.inverse);
    this.notify();
    return true;
  }

  /** 重做：重新应用正向 op。 */
  redoOnce(): boolean {
    const entry = this.undo.redo();
    if (!entry) return false;
    this.applyLocal(entry.ops);
    this.pending.push(...entry.ops);
    this.notify();
    return true;
  }

  setSelection(sel: Selection): void {
    this.selection = sel;
    this.notify();
  }

  /** 内核层处理交互事件，产出并执行意图。 */
  handleIntent(intent: Intent): Op[] {
    switch (intent.type) {
      case 'pan': {
        this.viewport.panBy(intent.dx, intent.dy);
        this.interaction.commitOrigin({ x: 0, y: 0 });
        this.notifyViewport();
        return [];
      }
      case 'zoom': {
        this.viewport.zoomAt(intent.point, intent.delta);
        this.notifyViewport();
        return [];
      }
      case 'select': {
        this.selection = intent.selection;
        this.notify();
        return [];
      }
      case 'clear-selection': {
        this.selection = { nodes: [], edges: [] };
        this.notify();
        return [];
      }
      case 'drag': {
        const ids = this.selection.nodes.length ? this.selection.nodes : [];
        if (ids.length === 0) return [];
        const ops = this.dispatch({ type: 'move-nodes', ids, dx: intent.dx, dy: intent.dy });
        this.interaction.commitOrigin(this.lastPoint ?? { x: 0, y: 0 });
        return ops;
      }
      case 'resize': {
        const id = this.selection.nodes[0];
        if (!id) return [];
        const node = this.scene.getNode(id);
        if (!node) return [];
        const rect = applyResize(node.rect, intent.handle, { dx: intent.dx, dy: intent.dy });
        return this.dispatch({ type: 'resize-node', id, rect, keepAspect: !node.spec.freeResize });
      }
      default:
        return [];
    }
  }

  private lastPoint?: Vec2;

  /** 由 React 层在每次 pointermove 时告知当前指针位置（用于拖拽 origin 复位）。 */
  notePointer(point: Vec2): void {
    this.lastPoint = point;
  }

  /** 本地应用 op（与 internal/graph/op.go 语义保持一致的子集）。 */
  private applyLocal(ops: Op[]): void {
    for (const op of ops) {
      switch (op.kind) {
        case 'add_node':
          this.scene.addNode(op.node);
          break;
        case 'remove_node': {
          const n = this.scene.removeNode(op.id);
          if (n) {
            for (const e of this.scene.edgesOf(op.id)) {
              this.scene.removeEdge(e.id);
            }
          }
          break;
        }
        case 'move_node': {
          const n = this.scene.getNode(op.id);
          if (!n) break;
          const next: RawNode = {
            ...n,
            rect: {
              ...n.rect,
              x: op.delta ? n.rect.x + op.x : op.x,
              y: op.delta ? n.rect.y + op.y : op.y,
            },
          };
          this.scene.updateNode(next);
          break;
        }
        case 'resize_node': {
          const n = this.scene.getNode(op.id);
          if (!n) break;
          this.scene.updateNode({ ...n, rect: { ...n.rect, w: op.w, h: op.h } });
          break;
        }
        case 'set_title': {
          const n = this.scene.getNode(op.id);
          if (!n) break;
          this.scene.updateNode({ ...n, title: op.title || n.title });
          break;
        }
        case 'set_spec': {
          const n = this.scene.getNode(op.id);
          if (!n) break;
          const spec = { ...n.spec };
          for (const k of op.unset ?? []) delete spec[k];
          for (const [k, v] of Object.entries(op.patch ?? {})) spec[k] = v;
          this.scene.updateNode({ ...n, spec });
          break;
        }
        case 'set_state': {
          const n = this.scene.getNode(op.id);
          if (!n) break;
          this.scene.updateNode({
            ...n,
            state: op.state,
            result: op.result ?? (op.state === 'idle' ? undefined : n.result),
            error: op.error ?? (op.state === 'idle' ? null : n.error),
          });
          break;
        }
        case 'add_edge': {
          const from = this.scene.getNode(op.edge.from.nodeId);
          const to = this.scene.getNode(op.edge.to.nodeId);
          if (!from || !to) break;
          // 单入端口替换语义（与服务端一致）
          const targetPort = to.ports.inputs.find((p) => p.id === op.edge.to.portId);
          if (targetPort && !targetPort.multiple) {
            for (const e of this.scene.upstreamOf(to.id)) {
              if (e.to.portId === op.edge.to.portId) this.scene.removeEdge(e.id);
            }
          }
          this.scene.addEdge(op.edge);
          break;
        }
        case 'remove_edge':
          this.scene.removeEdge(op.id);
          break;
        case 'group': {
          for (const id of op.nodeIds) {
            const n = this.scene.getNode(id);
            if (n) this.scene.updateNode({ ...n, parentId: op.groupId });
          }
          break;
        }
        case 'ungroup': {
          for (const n of this.scene.childrenOf(op.groupId)) {
            this.scene.updateNode({ ...n, parentId: undefined });
          }
          break;
        }
        case 'set_parent': {
          const n = this.scene.getNode(op.id);
          if (n) this.scene.updateNode({ ...n, parentId: op.parentId });
          break;
        }
        case 'set_viewport':
          this.viewport.set(op.viewport);
          this.notifyViewport();
          break;
        case 'set_settings':
          this.doc = { ...this.doc, settings: { ...this.doc.settings, ...op.settings } };
          break;
        default:
          break;
      }
    }
    this.version += 1;
  }

  private snapshotFor(cmd: Command): {
    rectOf: (id: string) => Rect | undefined;
    nodeOf: (id: string) => RawNode | undefined;
    viewport: Viewport;
    removedNodes?: RawNode[];
    removedEdges?: RawEdge[];
    prevViewport?: Viewport;
  } {
    const rects = new Map<string, Rect>();
    const nodes = new Map<string, RawNode>();
    const removedNodes: RawNode[] = [];
    const removedEdges: RawEdge[] = [];

    const capture = (id: string) => {
      const n = this.scene.getNode(id);
      if (n) {
        rects.set(id, { ...n.rect });
        nodes.set(id, { ...n, spec: { ...n.spec } });
      }
    };
    if (cmd.type === 'move-nodes') for (const id of cmd.ids) capture(id);
    if (cmd.type === 'resize-node') capture(cmd.id);
    if (cmd.type === 'rename') capture(cmd.id);
    if (cmd.type === 'set-spec') capture(cmd.id);
    if (cmd.type === 'delete-nodes') {
      for (const id of cmd.ids) {
        const n = this.scene.getNode(id);
        if (n) removedNodes.push({ ...n, spec: { ...n.spec } });
        for (const e of this.scene.edgesOf(id)) removedEdges.push({ ...e });
      }
    }
    return {
      rectOf: (id) => rects.get(id),
      nodeOf: (id) => nodes.get(id),
      viewport: this.viewport.current,
      removedNodes: removedNodes.length ? removedNodes : undefined,
      removedEdges: removedEdges.length ? removedEdges : undefined,
      prevViewport: cmd.type === 'set-viewport' ? this.viewport.current : undefined,
    };
  }

  private invertOf(
    cmd: Command,
    before: ReturnType<CanvasKernel['snapshotFor']>,
  ): Op[] {
    switch (cmd.type) {
      case 'move-nodes':
        return cmd.ids
          .map((id) => ({ kind: 'move_node' as const, id, x: before.rectOf(id)?.x ?? 0, y: before.rectOf(id)?.y ?? 0 }))
          .filter((op) => op.x !== undefined);
      case 'resize-node': {
        const r = before.rectOf(cmd.id);
        return r ? [{ kind: 'resize_node', id: cmd.id, w: r.w, h: r.h }] : [];
      }
      case 'rename': {
        const n = before.nodeOf(cmd.id);
        return n ? [{ kind: 'set_title', id: cmd.id, title: n.title }] : [];
      }
      case 'set-spec': {
        const n = before.nodeOf(cmd.id);
        if (!n) return [];
        const patch: Record<string, unknown> = {};
        const unset: string[] = [];
        for (const k of cmd.unset ?? []) patch[k] = n.spec[k];
        for (const k of Object.keys(cmd.patch ?? {})) unset.push(k);
        return [{ kind: 'set_spec', id: cmd.id, patch, unset }];
      }
      case 'add-node':
        return [{ kind: 'remove_node', id: cmd.node.id, cascade: true }];
      case 'connect':
        return [{ kind: 'remove_edge', id: cmd.edge.id }];
      case 'disconnect':
        return [];
      case 'group':
        return cmd.nodeIds.map((id) => ({ kind: 'set_parent' as const, id, parentId: undefined }));
      case 'ungroup':
        return this.scene.childrenOf(cmd.groupId).map((n) => ({ kind: 'set_parent' as const, id: n.id, parentId: cmd.groupId }));
      case 'set-viewport':
        return [{ kind: 'set_viewport', viewport: before.viewport }];
      default:
        return [];
    }
  }

  private restoreRemoved(entry: UndoEntry): void {
    for (const n of entry.removedNodes ?? []) this.scene.addNode(n);
    for (const e of entry.removedEdges ?? []) this.scene.addEdge(e);
  }

  /** 组合键处理：Ctrl/Cmd+Z / Ctrl+Shift+Z / Ctrl+Y 等（对齐原项目快捷键）。 */
  handleShortcut(e: {
    key: string;
    ctrlKey: boolean;
    metaKey: boolean;
    shiftKey: boolean;
    altKey: boolean;
    target?: { tagName?: string; isContentEditable?: boolean };
  }): 'undo' | 'redo' | 'select-all' | 'delete' | 'escape' | 'copy' | 'paste' | null {
    // 中文输入 / 编辑态不触发快捷键（11 §2.9）
    const tag = e.target?.tagName?.toLowerCase();
    if (e.target?.isContentEditable || tag === 'input' || tag === 'textarea') return null;
    const mod = e.ctrlKey || e.metaKey;
    if (!mod && e.key === 'Escape') return 'escape';
    if (!mod && (e.key === 'Delete' || e.key === 'Backspace')) return 'delete';
    if (!mod) return null;
    const k = e.key.toLowerCase();
    if (k === 'z' && !e.shiftKey) return 'undo';
    if ((k === 'z' && e.shiftKey) || k === 'y') return 'redo';
    if (k === 'a') return 'select-all';
    if (k === 'c') return 'copy';
    if (k === 'v') return 'paste';
    return null;
  }

  /** 当前待提交的 op 与本地版本号，交由 API 层发送。 */
  submitPayload(): { baseVersion: number; ops: Op[] } {
    const ops = this.takePendingOps();
    return { baseVersion: this.doc.version, ops };
  }

  /** 服务端确认后推进本地版本号。 */
  commitVersion(version: number): void {
    this.doc = { ...this.doc, version };
    this.version = version;
    this.notify();
  }
}
