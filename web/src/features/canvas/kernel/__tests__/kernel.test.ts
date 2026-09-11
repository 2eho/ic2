import { describe, expect, it } from 'vitest';
import { CanvasKernel } from '../kernel';
import type { CanvasDoc, RawEdge, RawNode } from '../types';

function makeDoc(nodes: RawNode[] = [], edges: RawEdge[] = []): CanvasDoc {
  return {
    id: 'cv_1',
    projectId: 'pj_1',
    version: 1,
    viewport: { x: 0, y: 0, k: 1 },
    settings: { background: 'dots', imageInfo: true, gridSnap: false, readOnly: false, freeResize: false },
    nodes: Object.fromEntries(nodes.map((n) => [n.id, n])),
    edges: Object.fromEntries(edges.map((e) => [e.id, e])),
    updatedAt: new Date().toISOString(),
  };
}

function prompt(id: string, x: number, y: number): RawNode {
  return {
    id, type: 'prompt', title: id, rect: { x, y, w: 320, h: 220 }, z: 0,
    ports: { inputs: [], outputs: [{ id: 'out', name: '文本', kind: 'text', multiple: false, required: false, order: 0 }] },
    spec: { text: 'hello' }, state: 'idle',
  };
}

function group(id: string): RawNode {
  return {
    id, type: 'group', title: id, rect: { x: 0, y: 0, w: 500, h: 500 }, z: 0,
    ports: { inputs: [], outputs: [] }, spec: {}, state: 'idle',
  };
}

function imageNode(id: string, x: number, y: number): RawNode {
  return {
    id, type: 'image', title: id, rect: { x, y, w: 320, h: 320 }, z: 0,
    ports: {
      inputs: [{ id: 'in', name: '输入', kind: 'image', multiple: false, required: false, order: 0 }],
      outputs: [{ id: 'out', name: '图片', kind: 'image', multiple: false, required: false, order: 0 }],
    },
    spec: { assetId: 'a_1' }, state: 'idle',
  };
}

describe('CanvasKernel', () => {
  it('初始化加载文档与视口', () => {
    const k = new CanvasKernel(makeDoc([prompt('a', 0, 0)]));
    expect(k.scene.size).toBe(1);
    expect(k.currentVersion).toBe(1);
    expect(k.viewport.current).toEqual({ x: 0, y: 0, k: 1 });
  });

  it('派发移动命令产生 op 并更新本地场景', () => {
    const k = new CanvasKernel(makeDoc([prompt('a', 0, 0)]));
    expect(k.dispatch({ type: 'move-nodes', ids: ['a'], dx: 40, dy: 20 }))
      .toEqual([{ kind: 'move_node', id: 'a', x: 40, y: 20, delta: true }]);
    expect(k.scene.getNode('a')?.rect.x).toBe(40);
    expect(k.scene.getNode('a')?.rect.y).toBe(20);
    expect(k.pendingCount).toBe(1);
  });

  it('撤销与重做恢复位置', () => {
    const k = new CanvasKernel(makeDoc([prompt('a', 10, 10)]));
    k.dispatch({ type: 'move-nodes', ids: ['a'], dx: 100, dy: 100 });
    expect(k.scene.getNode('a')?.rect.x).toBe(110);
    expect(k.undoOnce()).toBe(true);
    expect(k.scene.getNode('a')?.rect.x).toBe(10);
    expect(k.redoOnce()).toBe(true);
    expect(k.scene.getNode('a')?.rect.x).toBe(110);
  });

  it('撤销栈上限 50 步并丢弃 redo 分支', () => {
    const k = new CanvasKernel(makeDoc([prompt('a', 0, 0)]));
    for (let i = 0; i < 80; i++) k.dispatch({ type: 'move-nodes', ids: ['a'], dx: 1, dy: 0 });
    expect(k.undo.depth).toBe(50);
    k.undoOnce();
    k.dispatch({ type: 'move-nodes', ids: ['a'], dx: 5, dy: 0 });
    expect(k.undo.canRedo).toBe(false);
  });

  it('删除节点级联删边且可撤销恢复', () => {
    const k = new CanvasKernel(makeDoc([prompt('a', 0, 0), imageNode('img', 500, 0)], [
      { id: 'e1', from: { nodeId: 'a', portId: 'out' }, to: { nodeId: 'img', portId: 'in' }, kind: 'text', createdAt: '' },
    ]));
    k.dispatch({ type: 'delete-nodes', ids: ['a'] });
    expect(k.scene.getNode('a')).toBeUndefined();
    expect(k.scene.edgeCount).toBe(0);
    k.undoOnce();
    expect(k.scene.getNode('a')).toBeDefined();
    expect(k.scene.edgeCount).toBe(1);
  });

  it('单入端口连线按替换语义处理', () => {
    const k = new CanvasKernel(makeDoc([prompt('a', 0, 0), prompt('b', 400, 0), imageNode('img', 800, 0)]));
    k.dispatch({ type: 'connect', edge: { id: 'e1', from: { nodeId: 'a', portId: 'out' }, to: { nodeId: 'img', portId: 'in' }, kind: 'image', createdAt: '' } });
    k.dispatch({ type: 'connect', edge: { id: 'e2', from: { nodeId: 'b', portId: 'out' }, to: { nodeId: 'img', portId: 'in' }, kind: 'image', createdAt: '' } });
    expect(k.scene.upstreamOf('img').map((e) => e.id)).toEqual(['e2']);
  });

  it('只读画布拒绝写入', () => {
    const doc = makeDoc([prompt('a', 0, 0)]);
    doc.settings.readOnly = true;
    const k = new CanvasKernel(doc);
    expect(k.dispatch({ type: 'move-nodes', ids: ['a'], dx: 10, dy: 10 })).toEqual([]);
    expect(k.scene.getNode('a')?.rect.x).toBe(0);
  });

  it('takePendingOps 合并同帧 op', () => {
    const k = new CanvasKernel(makeDoc([prompt('a', 0, 0)]));
    k.dispatch({ type: 'move-nodes', ids: ['a'], dx: 10, dy: 10 });
    k.dispatch({ type: 'move-nodes', ids: ['a'], dx: 5, dy: 5 });
    expect(k.takePendingOps()).toEqual([{ kind: 'move_node', id: 'a', x: 15, y: 15, delta: true }]);
  });

  it('应用权威文档后重置待提交队列', () => {
    const k = new CanvasKernel(makeDoc([prompt('a', 0, 0)]));
    k.dispatch({ type: 'move-nodes', ids: ['a'], dx: 10, dy: 10 });
    const authority = makeDoc([prompt('a', 77, 77)]);
    authority.version = 9;
    k.applyAuthoritative(authority);
    expect(k.currentVersion).toBe(9);
    expect(k.pendingCount).toBe(0);
    expect(k.scene.getNode('a')?.rect.x).toBe(77);
  });

  it('快捷键识别 Ctrl+Z / Ctrl+Shift+Z / Ctrl+A / Esc', () => {
    const k = new CanvasKernel(makeDoc());
    const base = { ctrlKey: false, metaKey: false, shiftKey: false, altKey: false };
    expect(k.handleShortcut({ key: 'z', ...base, ctrlKey: true })).toBe('undo');
    expect(k.handleShortcut({ key: 'z', ...base, ctrlKey: true, shiftKey: true })).toBe('redo');
    expect(k.handleShortcut({ key: 'y', ...base, ctrlKey: true })).toBe('redo');
    expect(k.handleShortcut({ key: 'a', ...base, ctrlKey: true })).toBe('select-all');
    expect(k.handleShortcut({ key: 'Escape', ...base })).toBe('escape');
    expect(k.handleShortcut({ key: 'Delete', ...base })).toBe('delete');
  });

  it('输入态下不触发快捷键（中文输入法）', () => {
    const k = new CanvasKernel(makeDoc());
    const base = { ctrlKey: true, metaKey: false, shiftKey: false, altKey: false };
    expect(k.handleShortcut({ key: 'z', ...base, target: { tagName: 'TEXTAREA' } })).toBeNull();
    expect(k.handleShortcut({ key: 'z', ...base, target: { tagName: 'DIV', isContentEditable: true } })).toBeNull();
  });

  it('视口变更不触发文档订阅者（内核与 React 解耦的关键）', () => {
    const k = new CanvasKernel(makeDoc());
    let docCalls = 0;
    let vpCalls = 0;
    k.subscribe(() => { docCalls++; });
    k.subscribeViewport(() => { vpCalls++; });
    k.handleIntent({ type: 'pan', dx: 10, dy: 10 });
    k.handleIntent({ type: 'zoom', point: { x: 0, y: 0 }, delta: 0.1 });
    expect(docCalls).toBe(0);
    expect(vpCalls).toBe(2);
  });

  it('设置变更写入文档并产生 op', () => {
    const k = new CanvasKernel(makeDoc());
    expect(k.dispatch({ type: 'set-settings', settings: { background: 'lines' } }))
      .toEqual([{ kind: 'set_settings', settings: { background: 'lines' } }]);
    expect(k.settings.background).toBe('lines');
  });

  it('分组与解散保持父子关系', () => {
    const k = new CanvasKernel(makeDoc([group('g'), prompt('b', 50, 50)]));
    k.dispatch({ type: 'group', nodeIds: ['b'], groupId: 'g' });
    expect(k.scene.getNode('b')?.parentId).toBe('g');
    k.dispatch({ type: 'ungroup', groupId: 'g' });
    expect(k.scene.getNode('b')?.parentId).toBeUndefined();
  });

  it('提交载荷带当前版本号', () => {
    const k = new CanvasKernel(makeDoc([prompt('a', 0, 0)]));
    k.dispatch({ type: 'move-nodes', ids: ['a'], dx: 1, dy: 1 });
    const p = k.submitPayload();
    expect(p.baseVersion).toBe(1);
    expect(p.ops).toHaveLength(1);
    k.commitVersion(2);
    expect(k.currentVersion).toBe(2);
  });
});
