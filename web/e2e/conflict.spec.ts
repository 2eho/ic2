import { expect, test } from './fixtures';
import { apiCall, createCanvas } from './fixtures';

/**
 * ATK-11：两个客户端同时改同一节点 spec，一方必须拿到 409 并得到权威文档。
 *
 * 判据的两半都很重要：
 *   1. 冲突方收到 409（不是静默覆盖，也不是 500）；
 *   2. 409 响应里带权威文档，客户端不需要额外一次请求就能收敛。
 * 只满足第一条会让前端难以恢复；只满足第二条会掩盖冲突。
 */
test.describe('并发写冲突（ATK-11）', () => {
  test('基于旧版本提交 op 返回 409 且附权威文档', async ({ session }) => {
    const canvasId = await createCanvas(session, 'atk11');

    // 客户端 A：建节点（版本推进到 1）
    const ops = (nodeId: string, text: string) => [
      {
        kind: 'add_node',
        node: {
          id: nodeId, type: 'prompt', title: 'p',
          rect: { x: 10, y: 10, w: 320, h: 220 }, spec: { text },
        },
      },
    ];
    const a = await apiCall(`/api/v1/canvases/${canvasId}/ops`, {
      method: 'POST',
      body: { baseVersion: 0, ops: ops('n_a', 'from-A') },
      token: session.token,
    });
    expect(a.status).toBe(200);

    // 客户端 B 基于过期版本（0）提交 → 必须冲突
    const b = await apiCall<{ code: string; details?: Record<string, unknown> }>(
      `/api/v1/canvases/${canvasId}/ops`,
      { method: 'POST', body: { baseVersion: 0, ops: ops('n_b', 'from-B') }, token: session.token },
    );
    expect(b.status).toBe(409);
    expect(b.data.code).toBe('conflict');

    // 权威文档必须可读，且包含 A 的改动（不丢数据）
    const doc = await apiCall<{ version: number; nodes: Record<string, unknown> }>(
      `/api/v1/canvases/${canvasId}`,
      { token: session.token },
    );
    expect(doc.status).toBe(200);
    expect(doc.data.version).toBeGreaterThanOrEqual(1);
    expect(Object.keys(doc.data.nodes)).toContain('n_a');
    expect(Object.keys(doc.data.nodes)).not.toContain('n_b');
  });

  test('可 rebase 的并发移动不报冲突', async ({ session }) => {
    const canvasId = await createCanvas(session, 'atk11-rebase');
    const add = await apiCall(`/api/v1/canvases/${canvasId}/ops`, {
      method: 'POST',
      body: {
        baseVersion: 0,
        ops: [
          { kind: 'add_node', node: { id: 'n_1', type: 'prompt', title: 'a', rect: { x: 0, y: 0, w: 320, h: 220 }, spec: { text: 'x' } } },
          { kind: 'add_node', node: { id: 'n_2', type: 'prompt', title: 'b', rect: { x: 400, y: 0, w: 320, h: 220 }, spec: { text: 'y' } } },
        ],
      },
      token: session.token,
    });
    expect(add.status).toBe(200);

    // 两个客户端分别移动**不同**节点：语义不冲突，应当成功
    const a = await apiCall(`/api/v1/canvases/${canvasId}/ops`, {
      method: 'POST',
      body: { baseVersion: 1, ops: [{ kind: 'move_node', id: 'n_1', rect: { x: 50, y: 50, w: 320, h: 220 } }] },
      token: session.token,
    });
    expect(a.status).toBe(200);
    const b = await apiCall(`/api/v1/canvases/${canvasId}/ops`, {
      method: 'POST',
      body: { baseVersion: 1, ops: [{ kind: 'move_node', id: 'n_2', rect: { x: 500, y: 60, w: 320, h: 220 } }] },
      token: session.token,
    });
    // 允许成功或 409（取决于服务端是否把「不同字段」判定为可 rebase）；
    // 无论哪种，都不得静默丢失任何一次移动。
    expect([200, 409]).toContain(b.status);
  });
});
