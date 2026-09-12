import type { RawEdge, RawNode, Rect, Vec2 } from "./types";
import { rectsIntersect } from "./geometry";

/**
 * 场景图：维护节点/边索引与空间分桶。
 *
 * 性能目标（docs/design/13 §3.3）：5000 节点视口操作用时 ≥55FPS。
 * 手段：网格分桶做视口裁剪 + O(1) 索引查找，避免每帧全量遍历。
 */
export class SceneGraph {
  private nodes = new Map<string, RawNode>();
  private edges = new Map<string, RawEdge>();
  /** 邻接索引：nodeId -> 关联边 id */
  private adjacency = new Map<string, Set<string>>();
  /** 空间分桶：cellKey -> node ids */
  private buckets = new Map<string, Set<string>>();
  private static readonly CELL = 1024;

  get size(): number {
    return this.nodes.size;
  }

  get edgeCount(): number {
    return this.edges.size;
  }

  clear(): void {
    this.nodes.clear();
    this.edges.clear();
    this.adjacency.clear();
    this.buckets.clear();
  }

  /** 用权威文档整体替换（服务端快照 / 重连后全量同步）。 */
  load(nodes: Record<string, RawNode>, edges: Record<string, RawEdge>): void {
    this.clear();
    for (const n of Object.values(nodes)) this.addNode(n);
    for (const e of Object.values(edges)) this.addEdge(e);
  }

  addNode(n: RawNode): void {
    this.nodes.set(n.id, n);
    this.bucketInsert(n);
  }

  updateNode(n: RawNode): void {
    const prev = this.nodes.get(n.id);
    if (prev) this.bucketRemove(prev);
    this.nodes.set(n.id, n);
    this.bucketInsert(n);
  }

  removeNode(id: string): RawNode | undefined {
    const n = this.nodes.get(id);
    if (!n) return undefined;
    this.bucketRemove(n);
    this.nodes.delete(id);
    const links = this.adjacency.get(id);
    if (links) {
      for (const eid of links) this.removeEdge(eid);
      this.adjacency.delete(id);
    }
    return n;
  }

  addEdge(e: RawEdge): void {
    this.edges.set(e.id, e);
    this.link(e.from.nodeId, e.id);
    this.link(e.to.nodeId, e.id);
  }

  removeEdge(id: string): RawEdge | undefined {
    const e = this.edges.get(id);
    if (!e) return undefined;
    this.unlink(e.from.nodeId, id);
    this.unlink(e.to.nodeId, id);
    this.edges.delete(id);
    return e;
  }

  getNode(id: string): RawNode | undefined {
    return this.nodes.get(id);
  }

  getEdge(id: string): RawEdge | undefined {
    return this.edges.get(id);
  }

  allNodes(): RawNode[] {
    return [...this.nodes.values()];
  }

  allEdges(): RawEdge[] {
    return [...this.edges.values()];
  }

  edgesOf(nodeId: string): RawEdge[] {
    const ids = this.adjacency.get(nodeId);
    if (!ids) return [];
    const out: RawEdge[] = [];
    for (const id of ids) {
      const e = this.edges.get(id);
      if (e) out.push(e);
    }
    return out;
  }

  upstreamOf(nodeId: string): RawEdge[] {
    return this.edgesOf(nodeId).filter((e) => e.to.nodeId === nodeId);
  }

  downstreamOf(nodeId: string): RawEdge[] {
    return this.edgesOf(nodeId).filter((e) => e.from.nodeId === nodeId);
  }

  childrenOf(groupId: string): RawNode[] {
    const out: RawNode[] = [];
    for (const n of this.nodes.values()) {
      if (n.parentId === groupId) out.push(n);
    }
    return out;
  }

  /**
   * 视口裁剪：只返回与世界矩形相交的节点。
   * 用分桶粗筛后再精确判断，5000 节点场景下只触碰可见桶。
   */
  queryRect(worldRect: Rect): RawNode[] {
    const out: RawNode[] = [];
    const seen = new Set<string>();
    const c = SceneGraph.CELL;
    const x0 = Math.floor(worldRect.x / c);
    const y0 = Math.floor(worldRect.y / c);
    const x1 = Math.floor((worldRect.x + worldRect.w) / c);
    const y1 = Math.floor((worldRect.y + worldRect.h) / c);
    for (let cx = x0; cx <= x1; cx++) {
      for (let cy = y0; cy <= y1; cy++) {
        const ids = this.buckets.get(`${cx},${cy}`);
        if (!ids) continue;
        for (const id of ids) {
          if (seen.has(id)) continue;
          seen.add(id);
          const n = this.nodes.get(id);
          if (n && rectsIntersect(n.rect, worldRect)) out.push(n);
        }
      }
    }
    return out;
  }

  /** 点命中：从最高 z 开始（视觉最上层优先） */
  hitTest(point: Vec2, opts: { skipGroups?: boolean } = {}): RawNode | null {
    const candidates = this.queryRect({ x: point.x, y: point.y, w: 1, h: 1 });
    candidates.sort((a, b) => b.z - a.z);
    for (const n of candidates) {
      if (opts.skipGroups && n.type === "group") continue;
      if (
        point.x >= n.rect.x &&
        point.x <= n.rect.x + n.rect.w &&
        point.y >= n.rect.y &&
        point.y <= n.rect.y + n.rect.h
      ) {
        return n;
      }
    }
    return null;
  }

  /** 框选：返回与框相交的节点（原项目语义为交集判定） */
  hitRect(rect: Rect, opts: { skipGroups?: boolean } = {}): RawNode[] {
    return this.queryRect(rect).filter(
      (n) => !(opts.skipGroups && n.type === "group"),
    );
  }

  /** 邻接高亮：返回与给定节点直接相连的节点与边 */
  related(nodeIds: string[]): { nodes: Set<string>; edges: Set<string> } {
    const nodes = new Set<string>();
    const edges = new Set<string>();
    for (const id of nodeIds) {
      for (const e of this.edgesOf(id)) {
        edges.add(e.id);
        nodes.add(e.from.nodeId === id ? e.to.nodeId : e.from.nodeId);
      }
    }
    return { nodes, edges };
  }

  bounds(): Rect | null {
    let minX = Infinity;
    let minY = Infinity;
    let maxX = -Infinity;
    let maxY = -Infinity;
    for (const n of this.nodes.values()) {
      minX = Math.min(minX, n.rect.x);
      minY = Math.min(minY, n.rect.y);
      maxX = Math.max(maxX, n.rect.x + n.rect.w);
      maxY = Math.max(maxY, n.rect.y + n.rect.h);
    }
    if (!Number.isFinite(minX)) return null;
    return { x: minX, y: minY, w: maxX - minX, h: maxY - minY };
  }

  private link(nodeId: string, edgeId: string): void {
    let set = this.adjacency.get(nodeId);
    if (!set) {
      set = new Set();
      this.adjacency.set(nodeId, set);
    }
    set.add(edgeId);
  }

  private unlink(nodeId: string, edgeId: string): void {
    this.adjacency.get(nodeId)?.delete(edgeId);
  }

  private bucketInsert(n: RawNode): void {
    for (const key of this.bucketKeys(n.rect)) {
      let set = this.buckets.get(key);
      if (!set) {
        set = new Set();
        this.buckets.set(key, set);
      }
      set.add(n.id);
    }
  }

  private bucketRemove(n: RawNode): void {
    for (const key of this.bucketKeys(n.rect)) {
      const set = this.buckets.get(key);
      if (set) {
        set.delete(n.id);
        if (set.size === 0) this.buckets.delete(key);
      }
    }
  }

  private bucketKeys(r: Rect): string[] {
    const c = SceneGraph.CELL;
    const keys: string[] = [];
    const x0 = Math.floor(r.x / c);
    const y0 = Math.floor(r.y / c);
    const x1 = Math.floor((r.x + r.w) / c);
    const y1 = Math.floor((r.y + r.h) / c);
    for (let cx = x0; cx <= x1; cx++) {
      for (let cy = y0; cy <= y1; cy++) {
        keys.push(`${cx},${cy}`);
      }
    }
    return keys;
  }
}
