/**
 * 画布宿主端口（shared 层契约）。
 *
 * 为什么把它抽到 shared：插件在 sandbox iframe 里渲染，需要「读画布 / 改画布」，
 * 但它不应该依赖画布特性的实现（那会让 plugins → canvas → plugins 成环）。
 * 这里只声明**宿主必须提供什么**，具体实现由 canvas 特性注入。
 *
 * 这是解耦清单（docs/design/11 §5）要求的「抽象必须有 ≥2 个实现」的落点：
 * 生产实现是画布内核，测试实现是 `createFakeCanvasPort()`（见 __tests__）。
 */
export interface Rect {
  x: number;
  y: number;
  w: number;
  h: number;
}

/** 节点快照：插件只能看到这些字段，看不到宿主内部状态。 */
export interface NodeSnapshot {
  id: string;
  type: string;
  title: string;
  rect: Rect;
  spec: Record<string, unknown>;
  state: { status: string; [k: string]: unknown };
}

/** 边快照（插件查询上下游时使用）。 */
export interface EdgeSnapshot {
  id: string;
  from: { nodeId: string; portId: string };
  to: { nodeId: string; portId: string };
  kind: string;
}

/**
 * 画布操作端口。全部是「意图」，不是「直接改内存」：
 * 实现方负责把它们翻译成 op 提交（从而天然带上校验、版本与审计）。
 */
export interface CanvasPort {
  getNode(id: string): NodeSnapshot | null;
  getNodes(): NodeSnapshot[];
  getSelection(): string[];
  getEdges(): EdgeSnapshot[];
  getUpstream(nodeId: string): NodeSnapshot[];
  getDownstream(nodeId: string): NodeSnapshot[];
  /** 合并写入节点配置（局部更新，不覆盖未提供字段）。 */
  patchSpec(nodeId: string, patch: Record<string, unknown>): void;
  /** 调整节点尺寸（keepAspect 时按原始比例锁定）。 */
  resizeNode(nodeId: string, rect: Rect, keepAspect: boolean): void;
  /** 批量提交 op（插件 applyOps 的入口）。 */
  applyOps(ops: unknown[]): void;
  /** 打开/关闭节点面板（内置面板由宿主渲染）。 */
  openPanel(nodeId: string): void;
  closePanel(nodeId: string): void;
}

/**
 * 创建测试用替身（解耦清单要求 ≥2 个实现；也让插件 SDK 可以脱离画布单测）。
 * 刻意保持「内存 + 同步」，不引入 async，避免测试里到处 await。
 */
export function createFakeCanvasPort(
  seed: NodeSnapshot[] = [],
): CanvasPort & { ops: unknown[] } {
  const nodes = new Map<string, NodeSnapshot>(
    seed.map((n) => [n.id, { ...n }]),
  );
  const ops: unknown[] = [];
  return {
    ops,
    getNode: (id) => nodes.get(id) ?? null,
    getNodes: () => [...nodes.values()],
    getSelection: () => [],
    getEdges: () => [],
    getUpstream: () => [],
    getDownstream: () => [],
    patchSpec: (id, patch) => {
      const n = nodes.get(id);
      if (n) n.spec = { ...n.spec, ...patch };
      ops.push({ kind: "set_spec", id, patch });
    },
    resizeNode: (id, rect) => {
      const n = nodes.get(id);
      if (n) n.rect = rect;
      ops.push({ kind: "resize_node", id, rect });
    },
    applyOps: (list) => ops.push(...list),
    openPanel: () => undefined,
    closePanel: () => undefined,
  };
}
