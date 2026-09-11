import { describe, expect, it } from 'vitest';
import { SceneGraph } from '../scene';
import type { RawNode } from '../types';

function node(id: string, x: number, y: number, w = 100, h = 100, z = 0): RawNode {
  return {
    id, type: 'prompt', title: id, rect: { x, y, w, h }, z,
    ports: { inputs: [], outputs: [] }, spec: { text: '' }, state: 'idle',
  };
}

describe('SceneGraph', () => {
  it('增删查与邻接索引', () => {
    const g = new SceneGraph();
    g.addNode(node('a', 0, 0));
    g.addNode(node('b', 500, 0));
    g.addEdge({ id: 'e1', from: { nodeId: 'a', portId: 'out' }, to: { nodeId: 'b', portId: 'in' }, kind: 'text', createdAt: '' });
    expect(g.size).toBe(2);
    expect(g.edgesOf('a')).toHaveLength(1);
    expect(g.downstreamOf('a')).toHaveLength(1);
    expect(g.upstreamOf('b')).toHaveLength(1);
    g.removeNode('a');
    expect(g.edgeCount).toBe(0);
  });

  it('视口裁剪只返回相交节点', () => {
    const g = new SceneGraph();
    for (let i = 0; i < 5000; i++) g.addNode(node('n' + i, (i % 100) * 500, Math.floor(i / 100) * 500));
    const visible = g.queryRect({ x: 0, y: 0, w: 800, h: 800 });
    expect(visible.length).toBeGreaterThan(0);
    expect(visible.length).toBeLessThan(50);
  });

  it('5000 节点下视口查询每次 < 2ms', () => {
    const g = new SceneGraph();
    for (let i = 0; i < 5000; i++) g.addNode(node('n' + i, (i % 100) * 400, Math.floor(i / 100) * 400));
    const t0 = performance.now();
    const rounds = 200;
    for (let i = 0; i < rounds; i++) g.queryRect({ x: i * 10, y: i * 5, w: 1200, h: 900 });
    expect((performance.now() - t0) / rounds).toBeLessThan(2);
  });

  it('命中测试取 z 最大的节点', () => {
    const g = new SceneGraph();
    g.addNode(node('low', 0, 0, 200, 200, 1));
    g.addNode(node('high', 50, 50, 200, 200, 5));
    expect(g.hitTest({ x: 100, y: 100 })?.id).toBe('high');
    expect(g.hitTest({ x: 4000, y: 4000 })).toBeNull();
  });

  it('框选为交集判定', () => {
    const g = new SceneGraph();
    g.addNode(node('a', 0, 0, 100, 100));
    g.addNode(node('b', 500, 500, 100, 100));
    expect(g.hitRect({ x: 50, y: 50, w: 100, h: 100 }).map((n) => n.id)).toEqual(['a']);
  });

  it('关联高亮返回上下游', () => {
    const g = new SceneGraph();
    g.addNode(node('a', 0, 0));
    g.addNode(node('b', 300, 0));
    g.addNode(node('c', 600, 0));
    g.addEdge({ id: 'e1', from: { nodeId: 'a', portId: 'out' }, to: { nodeId: 'b', portId: 'in' }, kind: 'text', createdAt: '' });
    g.addEdge({ id: 'e2', from: { nodeId: 'b', portId: 'out' }, to: { nodeId: 'c', portId: 'in' }, kind: 'text', createdAt: '' });
    const rel = g.related(['b']);
    expect([...rel.nodes].sort()).toEqual(['a', 'c']);
    expect([...rel.edges].sort()).toEqual(['e1', 'e2']);
  });

  it('分组子节点与边界计算', () => {
    const g = new SceneGraph();
    g.addNode({ ...node('g', 0, 0, 500, 500), type: 'group' });
    g.addNode({ ...node('c', 50, 50), parentId: 'g' });
    expect(g.childrenOf('g').map((n) => n.id)).toEqual(['c']);
    expect(g.bounds()).toEqual({ x: 0, y: 0, w: 500, h: 500 });
  });

  it('移动节点后分桶更新', () => {
    const g = new SceneGraph();
    g.addNode(node('a', 0, 0));
    g.updateNode(node('a', 5000, 5000));
    expect(g.hitTest({ x: 10, y: 10 })).toBeNull();
    expect(g.hitTest({ x: 5050, y: 5050 })?.id).toBe('a');
  });

  it('空场景 bounds 为 null', () => {
    expect(new SceneGraph().bounds()).toBeNull();
  });
});
