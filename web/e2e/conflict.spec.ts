import { expect, test, apiCall, createCanvas } from './fixtures';

/**
 * ATK-11：两个客户端同时改同一节点 spec，一方必须拿到 409 并得到权威文档。
 *
 * 判据的两半都很重要：
 *   1. 冲突方收到 409（不是静默覆盖，也不是 500）；
 *   2. 409 响应里带权威文档，客户端不需要额外一次请求就能收敛。
 * 只满足第一条会让前端难以恢复；只满足第二条会掩盖冲突。
 *
 * 早期版本的两个问题（都是「用例本身不成立」，导致它从来没真正验证过冲突）：
 *   a. 冲突场景用的是 `add_node` 新建**另一个**节点。按设计
 *      （docs/design/03 §2.1 策略 1）这属于「可交换的 op 集合」，服务端会
 *      自动 rebase 并返回 200——即这条用例声称的期望行为与设计相反；
 *   b. 版本号被写死（baseVersion: 0/1）。而版本语义（新建文档是不是 0）会变，
 *      写死之后「版本语义错了」这件事只能表现为莫名其妙的失败，看不出原因。
 * 现在改为：用同一节点同一字段构造真冲突，并且**从服务端读版本**。
 */
test.describe('并发写冲突（ATK-11）', () => {
  const makeNode = (nodeId: string, text: string) => ({
    kind: 'add_node',
    node: {
      id: nodeId,
      type: 'prompt',
      title: 'p',
      rect: { x: 10, y: 10, w: 320, h: 220 },
      spec: { text },
    },
  });

  test('同一节点同一字段的过期提交必须 409 且附权威文档', async ({ session }) => {
    const canvasId = await createCanvas(session, 'atk11');

    // 先读画布拿到服务端权威版本，而不是假设它是 0
    const initial = await apiCall<{ version: number }>(`/api/v1/canvases/${canvasId}`, {
      token: session.token,
    });
    expect(initial.status).toBe(200);
    const base = initial.data.version;

    // 客户端 A：建节点（版本推进）
    const a = await apiCall<{ version: number }>(`/api/v1/canvases/${canvasId}/ops`, {
      method: 'POST',
      body: { baseVersion: base, ops: [makeNode('n_a', 'from-A')] },
      token: session.token,
    });
    expect(a.status).toBe(200);

    // A 再改一次该节点的 spec —— 这成为「在途 op」
    const a2 = await apiCall<{ version: number }>(`/api/v1/canvases/${canvasId}/ops`, {
      method: 'POST',
      body: {
        baseVersion: a.data.version,
        ops: [{ kind: 'set_spec', id: 'n_a', patch: { text: 'A-changed' } }],
      },
      token: session.token,
    });
    expect(a2.status).toBe(200);

    // 客户端 B 基于**过期版本**提交同一节点的同一字段 → 不可 rebase → 409
    const b = await apiCall<{ code: string; details?: Record<string, unknown> }>(
      `/api/v1/canvases/${canvasId}/ops`,
      {
        method: 'POST',
        body: {
          baseVersion: base,
          ops: [{ kind: 'set_spec', id: 'n_a', patch: { text: 'from-B' } }],
        },
        token: session.token,
      },
    );
    expect(b.status, `期望 409，实际 ${b.status}: ${JSON.stringify(b.data)}`).toBe(409);
    expect(b.data.code).toBe('conflict');

    // 权威文档必须随 409 一起返回（客户端不该再发一次请求才能恢复）
    expect(b.data.details?.authoritative, '409 未携带权威文档，客户端无法收敛').toBeTruthy();

    // 且 A 的改动必须原样保留：冲突提交不得静默落库
    const doc = await apiCall<{ version: number; nodes: Record<string, { spec: { text: string } }> }>(
      `/api/v1/canvases/${canvasId}`,
      { token: session.token },
    );
    expect(doc.data.version).toBe(a2.data.version);
    expect(doc.data.nodes.n_a?.spec.text).toBe('A-changed');

    // 另一侧：持正确版本的写入必须成功（不能把冲突检查做成恒拒）
    const c = await apiCall<{ version: number }>(`/api/v1/canvases/${canvasId}/ops`, {
      method: 'POST',
      body: {
        baseVersion: doc.data.version,
        ops: [{ kind: 'set_spec', id: 'n_a', patch: { text: 'legit' } }],
      },
      token: session.token,
    });
    expect(c.status, '持有正确版本的写入被误拒').toBe(200);
  });

  test('可 rebase 的并发修改被自动合并并返回 warning', async ({ session }) => {
    const canvasId = await createCanvas(session, 'atk11-rebase');
    const initial = await apiCall<{ version: number }>(`/api/v1/canvases/${canvasId}`, {
      token: session.token,
    });
    const base = initial.data.version;

    const add = await apiCall<{ version: number }>(`/api/v1/canvases/${canvasId}/ops`, {
      method: 'POST',
      body: {
        baseVersion: base,
        ops: [makeNode('n_1', 'a'), makeNode('n_2', 'b')],
      },
      token: session.token,
    });
    expect(add.status).toBe(200);

    // 在途：移动 n_1
    const a = await apiCall(`/api/v1/canvases/${canvasId}/ops`, {
      method: 'POST',
      body: {
        baseVersion: add.data.version,
        ops: [{ kind: 'move_node', id: 'n_1', x: 50, y: 50 }],
      },
      token: session.token,
    });
    expect(a.status).toBe(200);

    // 过期客户端移动 n_2（与在途目标不相交）→ 应自动 rebase，不是 409
    const b = await apiCall<{ version: number; rebased?: boolean; warnings?: string[] }>(
      `/api/v1/canvases/${canvasId}/ops`,
      {
        method: 'POST',
        body: {
          baseVersion: add.data.version,
          ops: [{ kind: 'move_node', id: 'n_2', x: 500, y: 60 }],
        },
        token: session.token,
      },
    );
    expect(b.status, '不相交的并发移动应当自动合并').toBe(200);
    expect(b.data.rebased, '自动合并必须标记 rebased').toBe(true);
    // 静默合并与静默覆盖同样危险：必须给出 warning 让 UI 能提示
    expect(b.data.warnings?.length ?? 0, '自动合并必须给出 warning').toBeGreaterThan(0);

    // 两个移动都必须生效（合并不能丢任何一次改动）
    const doc = await apiCall<{
      nodes: Record<string, { rect: { x: number; y: number } }>;
    }>(`/api/v1/canvases/${canvasId}`, { token: session.token });
    expect(doc.data.nodes.n_1?.rect.x).toBe(50);
    expect(doc.data.nodes.n_2?.rect.x).toBe(500);
  });

  test('baseVersion 超前于服务端必须 409 而不是被接受', async ({ session }) => {
    const canvasId = await createCanvas(session, 'atk11-future');
    const res = await apiCall<{ code: string }>(`/api/v1/canvases/${canvasId}/ops`, {
      method: 'POST',
      body: { baseVersion: 99, ops: [makeNode('n_future', 'x')] },
      token: session.token,
    });
    expect(res.status, '超前版本会被当作「无冲突」而静默应用').toBe(409);
    expect(res.data.code).toBe('conflict');
  });
});
