import type { InteractionState, Selection, Vec2 } from "./types";
import type { ResizeHandle } from "./geometry";

/**
 * 交互状态机（docs/design/04 §2.2）。
 * 用显式状态替代散落的 if (dragging && ...)，每次转移产出意图而不是直接改文档。
 */
export type InteractionEvent =
  | {
      type: "pointerdown";
      point: Vec2;
      hitNode?: string;
      hitEdge?: string;
      hitHandle?: ResizeHandle;
      /** 命中的端口（含所属节点与方向），用于从端口拖出连线。 */
      hitPort?: { nodeId: string; portId: string; side: "in" | "out"; kind: string };
      modifiers: Modifiers;
    }
  | { type: "pointermove"; point: Vec2; modifiers: Modifiers }
  | { type: "pointerup"; point: Vec2 }
  | { type: "keydown"; key: string; modifiers: Modifiers }
  | { type: "keyup"; key: string }
  | { type: "wheel"; point: Vec2; deltaY: number; modifiers: Modifiers }
  | { type: "dblclick"; point: Vec2; hitNode?: string }
  | { type: "blur" };

/** PortHit 是命中的端口信息。 */
export interface PortHit {
  nodeId: string;
  portId: string;
  side: "in" | "out";
  kind: string;
}

export interface Modifiers {
  shift: boolean;
  ctrl: boolean;
  meta: boolean;
  alt: boolean;
  space: boolean;
}

/** 状态机产出的意图，由上层转成命令。 */
export type Intent =
  | { type: "none" }
  | { type: "pan"; dx: number; dy: number }
  | { type: "zoom"; point: Vec2; delta: number }
  | { type: "start-marquee"; point: Vec2; additive: boolean }
  | { type: "update-marquee"; point: Vec2 }
  | { type: "commit-marquee"; selection: Selection }
  | { type: "start-drag"; ids: string[]; point: Vec2 }
  | { type: "drag"; dx: number; dy: number }
  | { type: "commit-drag" }
  | { type: "start-resize"; id: string; handle: ResizeHandle; point: Vec2 }
  | { type: "resize"; dx: number; dy: number; handle: ResizeHandle }
  | { type: "commit-resize" }
  | { type: "select"; selection: Selection; additive: boolean }
  | { type: "clear-selection" }
  | { type: "enter-text-edit"; id: string }
  | { type: "exit-text-edit" }
  // 连线拖拽（3.x）：从端口拖出、实时预览、落点提交。
  | {
      type: "start-connect";
      nodeId: string;
      portId: string;
      side: "in" | "out";
      kind: string;
    }
  | { type: "connect-drag"; point: Vec2 }
  // 落点为节点：建立连线
  | {
      type: "commit-connect";
      fromNodeId: string;
      fromPort: string;
      toNodeId: string;
      toPort: string;
    }
  // 落点为空白：弹出创建菜单（3.7）
  | { type: "commit-connect-blank"; point: Vec2; fromNodeId: string; fromPort: string }
  | { type: "cancel-connect" };

export class InteractionMachine {
  private state: InteractionState = "idle";
  private origin: Vec2 = { x: 0, y: 0 };
  private draggingIds: string[] = [];
  private resizeTarget?: { id: string; handle: ResizeHandle };
  private extraModifier = false;
  private connectFrom: { nodeId: string; portId: string; side: "in" | "out"; kind: string } | null = null;

  get current(): InteractionState {
    return this.state;
  }

  /** 当前是否处于「会修改文档」的状态（用于暂停动画与订阅） */
  get isMutating(): boolean {
    return this.state === "dragging-node" || this.state === "resizing-node";
  }

  get isPanning(): boolean {
    return this.state === "panning";
  }

  handle(ev: InteractionEvent): Intent {
    switch (ev.type) {
      case "pointerdown":
        return this.onPointerDown(ev);
      case "pointermove":
        return this.onPointerMove(ev);
      case "pointerup":
        return this.onPointerUp();
      case "keydown":
        return this.onKeyDown(ev);
      case "keyup":
        return this.onKeyUp(ev);
      case "wheel":
        return { type: "zoom", point: ev.point, delta: ev.deltaY };
      case "dblclick":
        if (ev.hitNode) {
          this.state = "editing-text";
          return { type: "enter-text-edit", id: ev.hitNode };
        }
        return { type: "none" };
      case "blur":
        this.reset();
        return { type: "clear-selection" };
      default:
        return { type: "none" };
    }
  }

  private onPointerDown(
    ev: Extract<InteractionEvent, { type: "pointerdown" }>,
  ): Intent {
    const additive = ev.modifiers.shift;
    this.origin = ev.point;

    // 1) 空格或中键或 Ctrl 拖拽 → 平移
    if (ev.modifiers.space || ev.modifiers.ctrl) {
      this.state = "panning";
      return { type: "none" };
    }
    // 2) 端口命中优先于一切：端口是「连线」的入口，而它通常落在节点边缘上。
    //    如果让节点拖拽先接管，用户永远拖不出连线（实测过的行为：
    //    从端口按下会移动整个节点，而用户以为自己在拉线）。
    if (ev.hitPort) {
      this.state = "connecting";
      this.connectFrom = {
        nodeId: ev.hitPort.nodeId,
        portId: ev.hitPort.portId,
        side: ev.hitPort.side,
        kind: ev.hitPort.kind,
      };
      return {
        type: "start-connect",
        nodeId: ev.hitPort.nodeId,
        portId: ev.hitPort.portId,
        side: ev.hitPort.side,
        kind: ev.hitPort.kind,
      };
    }
    // 3) 缩放手柄优先于节点拖拽
    if (ev.hitHandle && ev.hitNode) {
      this.state = "resizing-node";
      this.resizeTarget = { id: ev.hitNode, handle: ev.hitHandle };
      return {
        type: "start-resize",
        id: ev.hitNode,
        handle: ev.hitHandle,
        point: ev.point,
      };
    }
    // 4) 命中节点 → 拖拽（首个节点先选中）
    if (ev.hitNode) {
      this.state = "dragging-node";
      this.draggingIds = [ev.hitNode];
      return { type: "start-drag", ids: [ev.hitNode], point: ev.point };
    }
    // 5) 命中连线 → 选择连线
    if (ev.hitEdge) {
      this.state = "idle";
      return {
        type: "select",
        selection: { nodes: [], edges: [ev.hitEdge] },
        additive,
      };
    }
    // 6) 空白 → 框选
    this.state = "marquee";
    return { type: "start-marquee", point: ev.point, additive };
  }

  private onPointerMove(
    ev: Extract<InteractionEvent, { type: "pointermove" }>,
  ): Intent {
    const dx = ev.point.x - this.origin.x;
    const dy = ev.point.y - this.origin.y;
    switch (this.state) {
      case "panning":
        // 平移使用屏幕位移，不更新 origin（累积由视口控制器负责）
        return {
          type: "pan",
          dx: ev.point.x - this.origin.x,
          dy: ev.point.y - this.origin.y,
        };
      case "marquee":
        return { type: "update-marquee", point: ev.point };
      case "dragging-node":
        return { type: "drag", dx, dy };
      case "resizing-node":
        if (this.resizeTarget) {
          return { type: "resize", dx, dy, handle: this.resizeTarget.handle };
        }
        return { type: "none" };
      case "connecting":
        return { type: "connect-drag", point: ev.point };
      default:
        return { type: "none" };
    }
  }

  private onPointerUp(): Intent {
    const state = this.state;
    const from = this.connectFrom;
    this.reset();
    switch (state) {
      case "dragging-node":
        return { type: "commit-drag" };
      case "resizing-node":
        return { type: "commit-resize" };
      case "marquee":
        return { type: "commit-marquee", selection: { nodes: [], edges: [] } };
      case "connecting":
        // 连线的落点在**上层**决定（只有那里知道命中测试的结果）。
        // 状态机自己只能表达「拖拽结束了，起点是什么」。
        return from
          ? { type: "cancel-connect" }
          : { type: "none" };
      default:
        return { type: "none" };
    }
  }

  /**
   * 由上层调用：指针在连线的落点上松开。
   *
   * 之所以不在 onPointerUp 里做命中测试：命中测试需要场景图与视口，
   * 而状态机的契约是「不依赖渲染与文档」（见 kernel-purity 门禁）。
   * 上层拿到结果后回传，状态机只负责产出正确语义的意图。
   */
  commitConnect(
    hit: { nodeId: string; portId: string } | null,
    point: Vec2,
  ): Intent {
    const from = this.connectFrom;
    if (!from) return { type: "none" };
    this.reset();
    if (hit && hit.nodeId !== from.nodeId) {
      // 方向归一：从输入端口拖出时，语义是「把上游连到我的输入」，
      // 因此起点与终点需要互换。不归一的话用户从输入端拉线会得到一条反向边，
      // 而画布上看起来是对的（只有执行顺序不对）。
      if (from.side === "in") {
        return {
          type: "commit-connect",
          fromNodeId: hit.nodeId,
          fromPort: hit.portId,
          toNodeId: from.nodeId,
          toPort: from.portId,
        };
      }
      return {
        type: "commit-connect",
        fromNodeId: from.nodeId,
        fromPort: from.portId,
        toNodeId: hit.nodeId,
        toPort: hit.portId,
      };
    }
    if (hit && hit.nodeId === from.nodeId) {
      // 自连：直接取消，不弹菜单（弹出创建菜单会让人以为「必须再建一个节点」）
      return { type: "cancel-connect" };
    }
    // 落点为空白 → 创建菜单（3.7）
    return {
      type: "commit-connect-blank",
      point,
      fromNodeId: from.nodeId,
      fromPort: from.portId,
    };
  }

  private onKeyDown(
    ev: Extract<InteractionEvent, { type: "keydown" }>,
  ): Intent {
    if (ev.key === "Escape") {
      this.reset();
      return { type: "clear-selection" };
    }
    if (ev.key === " " && !ev.modifiers.ctrl && !ev.modifiers.meta) {
      // 空格：临时切换为平移模式（松开还原）
      this.extraModifier = true;
      return { type: "none" };
    }
    return { type: "none" };
  }

  private onKeyUp(ev: Extract<InteractionEvent, { type: "keyup" }>): Intent {
    if (ev.key === " ") this.extraModifier = false;
    return { type: "none" };
  }

  /** origin 在拖拽提交后需要复位，否则第二次拖拽的位移会叠加。 */
  commitOrigin(point: Vec2): void {
    this.origin = point;
  }

  reset(): void {
    this.state = "idle";
    this.draggingIds = [];
    this.resizeTarget = undefined;
    this.connectFrom = null;
  }

  get isEditingText(): boolean {
    return this.state === "editing-text";
  }

  get spaceHeld(): boolean {
    return this.extraModifier;
  }

  /** 当前正在拖拽的节点（多选联动时由上层使用）。 */
  get dragging(): string[] {
    return [...this.draggingIds];
  }

  /** 当前正在拖出的连线起点（无则为 null）。 */
  get connecting(): { nodeId: string; portId: string; side: "in" | "out"; kind: string } | null {
    return this.connectFrom;
  }
}
