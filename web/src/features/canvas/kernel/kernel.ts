import { SceneGraph } from "./scene";
import { ViewportController } from "./viewport";
import { UndoStack, type UndoEntry } from "./undo";
import { InteractionMachine, type Intent } from "./interaction";
import {
  coalesceOps,
  commandToOps,
  offsetsToMoves,
  type Command,
} from "./commands";
import type {
  CanvasDoc,
  Op,
  RawEdge,
  RawNode,
  Rect,
  Selection,
  Vec2,
  Viewport,
} from "./types";
import {
  applyResize,
  type AlignAnchor,
  type AlignMode,
  type AlignRect,
  type DistributeAxis,
  type LayeredLayoutOptions,
} from "./geometry";
import { defaultSchemaFor, newLocalID } from "./schema";
import {
  applyAdditive,
  toggleSelectionNodes,
  unionSelection,
} from "./selection";
import { applySceneOp } from "./applyOp";
import {
  isLayeredSettled,
  runAlign,
  runDistribute,
  runLayeredLayout,
} from "./layoutOps";
import type { EdgeLike } from "./layout";

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

  /** 本端 actorId：用于忽略自己提交的 SSE 回声。 */
  readonly localActor: string;

  private doc: CanvasDoc;
  private selection: Selection = { nodes: [], edges: [] };
  private pending: Op[] = [];
  private listeners = new Set<() => void>();
  private viewportListeners = new Set<(v: Viewport) => void>();
  private version: number;

  constructor(doc: CanvasDoc, localActor = "") {
    this.doc = doc;
    this.localActor = localActor;
    this.version = doc.version;
    this.viewport.set(doc.viewport);
    this.scene.load(doc.nodes, doc.edges);
  }

  get currentVersion(): number {
    return this.version;
  }

  get currentSelection(): Selection {
    return {
      nodes: [...this.selection.nodes],
      edges: [...this.selection.edges],
    };
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
    const ops =
      cmd.type === "align-nodes" ||
      cmd.type === "distribute-nodes" ||
      cmd.type === "layout-layers"
        ? offsetsToMoves(cmd.offsets, (id) => this.scene.getNode(id)?.rect)
        : commandToOps(cmd);
    if (ops.length === 0 && cmd.type !== "duplicate-nodes") return [];

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
      prevViewport:
        cmd.type === "set-viewport" ? before.prevViewport : undefined,
      at: Date.now(),
    });

    this.notify();
    return ops;
  }

  /**
   * 应用远端 op（来自 SSE）。与本地路径完全同源，因此不存在「某条路径绕过校验」。
   * 注意：远端 op 不进 undo 栈（撤销只针对自己的操作）。
   */
  applyRemote(op: Op): void {
    this.applyLocal([op]);
    // 版本由 SSE 载荷推进；此处只保证本地视图一致。
    this.notify();
  }

  /**
   * 按类型创建节点（默认位置由调用方给出世界坐标）。
   * 端口与默认 spec 从内置 schema 表推导，保证与服务端一致。
   */
  createNode(type: string, worldPos: Vec2): RawNode | null {
    const schema = defaultSchemaFor(type);
    if (!schema) return null;
    const id = newLocalID(type);
    const node: RawNode = {
      id,
      type,
      title: schema.title,
      rect: {
        x: Math.round(worldPos.x),
        y: Math.round(worldPos.y),
        w: schema.w,
        h: schema.h,
      },
      z: this.nextZ(),
      ports: schema.ports,
      spec: { ...schema.spec },
      state: "idle",
    };
    this.dispatch({ type: "add-node", node });
    this.selection = { nodes: [id], edges: [] };
    return node;
  }

  /** 创建连线（含端口类型校验与单入端口替换，语义与服务端一致）。 */
  createEdge(
    fromNode: string,
    fromPort: string,
    toNode: string,
    toPort: string,
  ): RawEdge | null {
    const a = this.scene.getNode(fromNode);
    const b = this.scene.getNode(toNode);
    if (!a || !b || fromNode === toNode) return null;
    const out = a.ports.outputs.find((p) => p.id === fromPort);
    const inp = b.ports.inputs.find((p) => p.id === toPort);
    if (!out || !inp || out.kind !== inp.kind) return null;
    const edge: RawEdge = {
      id: newLocalID("e"),
      from: { nodeId: fromNode, portId: fromPort },
      to: { nodeId: toNode, portId: toPort },
      kind: out.kind,
      createdAt: new Date().toISOString(),
    };
    this.dispatch({ type: "connect", edge });
    return edge;
  }

  private nextZ(): number {
    let max = 0;
    for (const n of this.scene.allNodes()) {
      if (n.z > max) max = n.z;
    }
    return max + 1;
  }

  /** 复制选中节点（含内部连线），返回新节点 ID 映射（对齐原项目复制粘贴语义）。 */
  duplicateNodes(ids: string[], offset = 24): Record<string, string> {
    const idMap: Record<string, string> = {};
    const nodes = ids
      .map((id) => this.scene.getNode(id))
      .filter((n): n is RawNode => Boolean(n));
    for (const n of nodes) {
      const newId = newLocalID(n.type === "group" ? "grp" : "n");
      idMap[n.id] = newId;
      this.dispatch({
        type: "add-node",
        node: {
          ...n,
          id: newId,
          rect: { ...n.rect, x: n.rect.x + offset, y: n.rect.y + offset },
          z: this.nextZ(),
          parentId: n.parentId ? (idMap[n.parentId] ?? n.parentId) : undefined,
          state: "idle",
          result: undefined,
          error: null,
        },
      });
    }
    // 复制两端都在选区内的连线
    for (const e of this.scene.allEdges()) {
      if (idMap[e.from.nodeId] && idMap[e.to.nodeId]) {
        this.dispatch({
          type: "connect",
          edge: {
            id: newLocalID("e"),
            from: { nodeId: idMap[e.from.nodeId], portId: e.from.portId },
            to: { nodeId: idMap[e.to.nodeId], portId: e.to.portId },
            kind: e.kind,
            createdAt: new Date().toISOString(),
          },
        });
      }
    }
    this.selection = { nodes: Object.values(idMap), edges: [] };
    return idMap;
  }

  /**
   * 选中节点的对齐（一键对齐）。`anchor` 默认 `union`（选区包围盒），
   * 可选 `first`（首元素）。少于 2 个或本来就已经对齐时不产生 op ——
   * 调用方据此判断「这次点击是不是空操作」，不把无意义 op 发给服务端。
   * 位移怎么算在 `layout.ts` 纯函数里，这里只负责取数据 + 派发（见 `layoutOps.ts`）。
   */
  alignSelection(
    ids: string[] = this.selection.nodes,
    mode: AlignMode,
    anchor: AlignAnchor = "union",
  ): Op[] {
    return runAlign(
      (cmd) => this.dispatch(cmd),
      this.rectsOf(ids),
      mode,
      anchor,
    );
  }

  /** 选中节点的等间距分布（需 ≥3 个，按中心等距）。 */
  distributeSelection(
    ids: string[] = this.selection.nodes,
    axis: DistributeAxis,
  ): Op[] {
    return runDistribute((cmd) => this.dispatch(cmd), this.rectsOf(ids), axis);
  }

  /**
   * 按拓扑层级成列（「每一层在同一列」）：用选区内部的连线算层号，
   * 同层节点对齐到同一个 x、列内按原顺序铺开（算法见 `layout.ts`）。
   * 只产出一条 `layout-layers` 命令，一次 Ctrl+Z 可整体还原。
   */
  layoutByLayers(
    ids: string[] = this.selection.nodes,
    opts: LayeredLayoutOptions = {},
  ): Op[] {
    return runLayeredLayout(
      (cmd) => this.dispatch(cmd),
      { rects: this.rectsOf(ids), edges: this.edgesLike() },
      opts,
    );
  }

  /** 是否已经按层成列（UI 用于置灰，避免「点了没反应」）。 */
  isLayeredAlready(
    ids: string[] = this.selection.nodes,
    opts: LayeredLayoutOptions = {},
  ): boolean {
    return isLayeredSettled(
      { rects: this.rectsOf(ids), edges: this.edgesLike() },
      opts,
    );
  }

  /** 场景内全部连线（只取两端节点 id），供分层计算使用。 */
  private edgesLike(): EdgeLike[] {
    return this.scene
      .allEdges()
      .map((e) => ({ from: e.from.nodeId, to: e.to.nodeId }));
  }

  /** 选中节点矩形（世界坐标），过滤掉已删除 id；用于对齐可用性判断。 */
  rectsOf(ids: string[] = this.selection.nodes): AlignRect[] {
    const out: AlignRect[] = [];
    for (const id of ids) {
      const n = this.scene.getNode(id);
      if (n) out.push({ id: n.id, ...n.rect });
    }
    return out;
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

  /**
   * 设置选区。`additive` 为真时**并入**当前选区（Shift 多选），否则整体替换。
   * 合并/反选规则在 `selection.ts` 纯函数里，这里只负责写入状态与通知。
   *
   * 这条路径曾出现假绿：`handleIntent` 把 `intent.additive` 丢掉，
   * Shift 点击变成静默覆盖选区（多选堆不出来 → 多选工具栏到不了）。
   */
  setSelection(sel: Selection, opts: { additive?: boolean } = {}): void {
    this.selection = opts.additive ? unionSelection(this.selection, sel) : sel;
    this.notify();
  }

  /** Shift 点击已选中的节点 → 取消选中（与主流画布一致的反选语义）。 */
  toggleSelection(sel: Selection): void {
    this.selection = toggleSelectionNodes(this.selection, sel);
    this.notify();
  }

  /** 内核层处理交互事件，产出并执行意图。 */
  handleIntent(intent: Intent): Op[] {
    switch (intent.type) {
      case "pan": {
        this.viewport.panBy(intent.dx, intent.dy);
        this.interaction.commitOrigin({ x: 0, y: 0 });
        this.notifyViewport();
        return [];
      }
      case "zoom": {
        this.viewport.zoomAt(intent.point, intent.delta);
        this.notifyViewport();
        return [];
      }
      case "select": {
        // additive 必须真的生效：状态机产出了 `additive`，内核却曾把它丢掉。
        this.selection = intent.additive
          ? applyAdditive(this.selection, intent.selection)
          : intent.selection;
        this.notify();
        return [];
      }
      case "clear-selection": {
        this.selection = { nodes: [], edges: [] };
        this.notify();
        return [];
      }
      case "drag": {
        const ids = this.selection.nodes.length ? this.selection.nodes : [];
        if (ids.length === 0) return [];
        const ops = this.dispatch({
          type: "move-nodes",
          ids,
          dx: intent.dx,
          dy: intent.dy,
        });
        this.interaction.commitOrigin(this.lastPoint ?? { x: 0, y: 0 });
        return ops;
      }
      case "start-connect":
      case "connect-drag":
      case "cancel-connect": {
        // 预览线段由 UI 层绘制（它需要屏幕坐标），内核不保留瞬时交互态：
        // 内核状态一旦包含「正在拖的线」，重放/快照就必须解释它，
        // 而它不是文档的一部分。
        this.notify();
        return [];
      }
      case "commit-connect": {
        const edge = this.createEdge(
          intent.fromNodeId,
          intent.fromPort,
          intent.toNodeId,
          intent.toPort,
        );
        if (!edge) {
          // 端口类型不匹配或自连：给出可观测信号而不是静默无反应。
          this.notify();
          return [];
        }
        return this.takePendingOps();
      }
      case "commit-connect-blank": {
        // 3.7：落点是空白。内核只负责「暴露这个事实」，
        // 具体弹什么菜单由 UI 决定（内核不 import react，也不该知道菜单）。
        this.lastConnectBlank = {
          x: intent.point.x,
          y: intent.point.y,
          fromNodeId: intent.fromNodeId,
          fromPort: intent.fromPort,
        };
        this.notify();
        return [];
      }
      case "resize": {
        const id = this.selection.nodes[0];
        if (!id) return [];
        const node = this.scene.getNode(id);
        if (!node) return [];
        const rect = applyResize(node.rect, intent.handle, {
          dx: intent.dx,
          dy: intent.dy,
        });
        return this.dispatch({
          type: "resize-node",
          id,
          rect,
          keepAspect: !node.spec.freeResize,
        });
      }
      default:
        return [];
    }
  }

  /**
   * 最近一次「连线落在空白处」的位置（3.7）。
   *
   * 单独存成字段而不是塞进事件回调：CanvasSurface 是重渲染驱动的，
   * 用回调会引入「React 状态与内核状态谁先更新」的竞态。
   */
  lastConnectBlank: {
    x: number;
    y: number;
    fromNodeId: string;
    fromPort: string;
  } | null = null;

  /** 取出并清空「连线落在空白处」事件。 */
  takeConnectBlank() {
    const v = this.lastConnectBlank;
    this.lastConnectBlank = null;
    return v;
  }

  /** 最近一次指针位置（拖拽 origin 复位与框选用）。 */
  private lastPoint?: Vec2;
  /** 最近一次 pointerdown 位置（框选起点）。 */
  lastDownPoint?: Vec2;

  /** 由 React 层在每次 pointermove 时告知当前指针位置。 */
  notePointer(point: Vec2): void {
    if (
      !this.lastPoint ||
      this.interaction.current === "idle" ||
      this.interaction.current === "marquee"
    ) {
      if (this.interaction.current === "marquee" && !this.lastDownPoint) {
        this.lastDownPoint = point;
      }
    }
    this.lastPoint = point;
  }

  /** 记录 pointerdown 位置（框选起点）。 */
  noteDown(point: Vec2): void {
    this.lastDownPoint = point;
  }

  /** 本地应用 op（场景级逻辑见 `applyOp.ts`，这里只处理视口/设置与版本号）。 */
  private applyLocal(ops: Op[]): void {
    for (const op of ops) {
      if (applySceneOp(this.scene, op)) continue;
      switch (op.kind) {
        case "set_viewport":
          this.viewport.set(op.viewport);
          this.notifyViewport();
          break;
        case "set_settings":
          this.doc = {
            ...this.doc,
            settings: { ...this.doc.settings, ...op.settings },
          };
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
    if (cmd.type === "move-nodes") for (const id of cmd.ids) capture(id);
    if (
      cmd.type === "align-nodes" ||
      cmd.type === "distribute-nodes" ||
      cmd.type === "layout-layers"
    )
      for (const o of cmd.offsets) capture(o.id);
    if (cmd.type === "resize-node") capture(cmd.id);
    if (cmd.type === "rename") capture(cmd.id);
    if (cmd.type === "set-spec") capture(cmd.id);
    if (cmd.type === "delete-nodes") {
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
      prevViewport:
        cmd.type === "set-viewport" ? this.viewport.current : undefined,
    };
  }

  private invertOf(
    cmd: Command,
    before: ReturnType<CanvasKernel["snapshotFor"]>,
  ): Op[] {
    switch (cmd.type) {
      case "move-nodes":
        return cmd.ids
          .map((id) => ({
            kind: "move_node" as const,
            id,
            x: before.rectOf(id)?.x ?? 0,
            y: before.rectOf(id)?.y ?? 0,
          }))
          .filter((op) => op.x !== undefined);
      case "resize-node": {
        const r = before.rectOf(cmd.id);
        return r ? [{ kind: "resize_node", id: cmd.id, w: r.w, h: r.h }] : [];
      }
      case "rename": {
        const n = before.nodeOf(cmd.id);
        return n ? [{ kind: "set_title", id: cmd.id, title: n.title }] : [];
      }
      case "set-spec": {
        const n = before.nodeOf(cmd.id);
        if (!n) return [];
        const patch: Record<string, unknown> = {};
        const unset: string[] = [];
        for (const k of cmd.unset ?? []) patch[k] = n.spec[k];
        for (const k of Object.keys(cmd.patch ?? {})) unset.push(k);
        return [{ kind: "set_spec", id: cmd.id, patch, unset }];
      }
      case "align-nodes":
      case "distribute-nodes":
      case "layout-layers": {
        // 撤销：按执行前的矩形回到原位（选后立刻再对齐/撤销都不漂移）
        const inverse: Op[] = [];
        for (const o of cmd.offsets) {
          const r = before.rectOf(o.id);
          if (r && (o.dx !== 0 || o.dy !== 0))
            inverse.push({ kind: "move_node", id: o.id, x: r.x, y: r.y });
        }
        return inverse;
      }
      case "add-node":
        return [{ kind: "remove_node", id: cmd.node.id, cascade: true }];
      case "connect":
        return [{ kind: "remove_edge", id: cmd.edge.id }];
      case "disconnect":
        return [];
      case "group":
        return cmd.nodeIds.map((id) => ({
          kind: "set_parent" as const,
          id,
          parentId: undefined,
        }));
      case "ungroup":
        return this.scene.childrenOf(cmd.groupId).map((n) => ({
          kind: "set_parent" as const,
          id: n.id,
          parentId: cmd.groupId,
        }));
      case "set-viewport":
        return [{ kind: "set_viewport", viewport: before.viewport }];
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
  }):
    | "undo"
    | "redo"
    | "select-all"
    | "delete"
    | "escape"
    | "copy"
    | "paste"
    | null {
    // 中文输入 / 编辑态不触发快捷键（11 §2.9）
    const tag = e.target?.tagName?.toLowerCase();
    if (e.target?.isContentEditable || tag === "input" || tag === "textarea")
      return null;
    const mod = e.ctrlKey || e.metaKey;
    if (!mod && e.key === "Escape") return "escape";
    if (!mod && (e.key === "Delete" || e.key === "Backspace")) return "delete";
    if (!mod) return null;
    const k = e.key.toLowerCase();
    if (k === "z" && !e.shiftKey) return "undo";
    if ((k === "z" && e.shiftKey) || k === "y") return "redo";
    if (k === "a") return "select-all";
    if (k === "c") return "copy";
    if (k === "v") return "paste";
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
