/**
 * 画布内核类型（framework-agnostic，不 import react）。
 * 设计依据：docs/design/04-frontend.md §2。
 */

export interface Vec2 {
  x: number;
  y: number;
}

/** 世界坐标矩形 */
export interface Rect {
  x: number;
  y: number;
  w: number;
  h: number;
}

/** 视口：x/y 平移（屏幕像素），k 缩放 */
export interface Viewport {
  x: number;
  y: number;
  k: number;
}

export type NodeState = "idle" | "pending" | "running" | "succeeded" | "failed";
export type ResourceKind =
  "text" | "image" | "video" | "audio" | "file" | "json";

export interface Port {
  id: string;
  name: string;
  kind: ResourceKind;
  multiple: boolean;
  required: boolean;
  order: number;
}

export interface Ports {
  inputs: Port[];
  outputs: Port[];
}

export interface NodeResult {
  variants?: {
    assetId?: string;
    text?: string;
    kind: ResourceKind;
    status: NodeState;
    error?: string;
  }[];
  primary: number;
  runId?: string;
  stepId?: string;
}

export interface RawNode {
  id: string;
  type: string;
  title: string;
  rect: Rect;
  z: number;
  parentId?: string;
  ports: Ports;
  spec: Record<string, unknown>;
  state: NodeState;
  result?: NodeResult;
  error?: { code: string; message: string } | null;
  locked?: boolean;
}

export interface RawEdge {
  id: string;
  from: { nodeId: string; portId: string };
  to: { nodeId: string; portId: string };
  kind: ResourceKind;
  createdBy?: string;
  createdAt: string;
}

export interface CanvasSettings {
  background: "lines" | "dots" | "blank";
  imageInfo: boolean;
  gridSnap: boolean;
  readOnly: boolean;
  freeResize: boolean;
}

export interface CanvasDoc {
  id: string;
  projectId: string;
  version: number;
  viewport: Viewport;
  settings: CanvasSettings;
  nodes: Record<string, RawNode>;
  edges: Record<string, RawEdge>;
  updatedAt: string;
}

/** 内核 op：与服务端 internal/graph/op.go 一一对应（同源语义）。 */
export type Op =
  | { kind: "add_node"; node: RawNode }
  | { kind: "remove_node"; id: string; cascade?: boolean }
  | { kind: "move_node"; id: string; x: number; y: number; delta?: boolean }
  | {
      kind: "resize_node";
      id: string;
      w: number;
      h: number;
      keepAspect?: boolean;
    }
  | { kind: "set_title"; id: string; title: string }
  | {
      kind: "set_spec";
      id: string;
      patch?: Record<string, unknown>;
      unset?: string[];
    }
  | {
      kind: "set_state";
      id: string;
      state: NodeState;
      result?: NodeResult;
      error?: { code: string; message: string };
    }
  | { kind: "add_edge"; edge: RawEdge }
  | { kind: "remove_edge"; id: string }
  | {
      kind: "group";
      nodeIds: string[];
      groupId: string;
      title?: string;
      rect?: Rect;
    }
  | { kind: "ungroup"; groupId: string; keepChildren?: boolean }
  | { kind: "set_viewport"; viewport: Viewport }
  | { kind: "set_settings"; settings: Partial<CanvasSettings> }
  | { kind: "set_parent"; id: string; parentId?: string };

/** 交互状态机的状态（显式枚举，替代散落的 if (dragging && ...)）。 */
export type InteractionState =
  | "idle"
  | "panning"
  | "marquee"
  | "dragging-node"
  | "resizing-node"
  | "connecting"
  | "editing-text"
  | "plugin-interacting";

export interface Selection {
  nodes: string[];
  edges: string[];
}
