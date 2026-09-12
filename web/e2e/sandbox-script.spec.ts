import { expect, test, apiCall, createCanvas } from './fixtures';

/**
 * 自定义调用脚本的沙箱边界（4.19 / 4.12）。
 *
 * 这一条必须打**真实服务端**：脚本沙箱的安全性来自「解释器本身不提供
 * 那些能力」，而任何在用例里重造的替身都会把这个结论变成自证。
 *
 * 覆盖的是三类判据，缺一不可：
 *   1. 逃逸手法被拒（eval / Function / 原型链）——「拒绝生效」；
 *   2. 合法脚本能跑通并产出正确结果——「拒绝没有过度」；
 *   3. 错误码是 invalid_request / forbidden 而不是 500 —— 用户输入问题
 *      不能被报成平台故障（否则运维会被无关告警淹没）。
 */
test.describe('自定义脚本沙箱（4.19 / ATK-23 / ATK-24）', () => {
  /** 建一个「脚本协议」渠道，把脚本放进凭据的 limits（与 ScriptEditor 一致）。 */
  async function createScriptProvider(
    session: { token: string; workspaceId: string },
    script: string,
  ) {
    const pid = `script-e2e-${Date.now()}-${Math.floor(Math.random() * 1e6)}`;
    await apiCall(`/api/v1/workspaces/${session.workspaceId}/providers`, {
      method: 'POST',
      token: session.token,
      body: {
        id: pid,
        name: '脚本协议',
        kind: 'script',
        baseUrl: 'http://127.0.0.1:9',
        authKind: 'bearer',
        capabilities: ['image.generate'],
      },
    });
    await apiCall(`/api/v1/workspaces/${session.workspaceId}/providers/${pid}/credentials`, {
      method: 'POST',
      token: session.token,
      body: { name: '脚本', secret: 'sk-e2e', priority: 0, limits: { script } },
    });
    return pid;
  }

  const escapingScripts: Array<[string, string]> = [
    ['eval', `return { x: eval("1+1") };`],
    ['Function 构造器', `var f = Function("return 1"); return { x: f() };`],
    ['原型链字面量', `return { x: params.__proto__ };`],
    ['原型链拼接', `return { x: params["constr" + "uctor"] };`],
    ['循环', `for (var i = 0; i < 3; i = i + 1) { } return { path: "/x" };`],
    ['宿主全局', `return { x: process.env.HOME };`],
    ['动态导入', `return { x: require("fs") };`],
    ['模板插值', "return { x: `a${params.b}` };"],
  ];

  for (const [label, script] of escapingScripts) {
    test(`拒绝 ${label}`, async ({ session }) => {
      const pid = await createScriptProvider(session, script);
      const res = await apiCall<{ code?: string }>(
        `/api/v1/workspaces/${session.workspaceId}/generate`,
        {
          method: 'POST',
          token: session.token,
          // providerId 是**顶层**字段，不是 params 里的：
        // 它决定用哪个渠道，属于「这次调用用什么」，不是生成参数。
        body: {
            capability: 'image.generate',
            prompt: 'x',
            providerId: pid,
            params: { size: '1k' },
          },
        },
      );
      // 关键：不能是 500。脚本写错是用户输入问题，报 500 会让用户
      // 以为平台坏了，也会把真正的平台故障淹没在噪音里。
      expect(res.status, `${label} 应当以 4xx 被拒，实际 ${res.status}`).toBeLessThan(500);
      expect(res.status).toBeGreaterThanOrEqual(400);
    });
  }

  test('合法脚本能产出请求（拒绝没有过度）', async ({ session }) => {
    // 反向证明：如果沙箱把一切都拒了，上面的用例会全绿而功能完全不可用。
    const pid = await createScriptProvider(
      session,
      [
        'var body = { text: prompt, n: count };',
        'if (params.size != null) { body.size = params.size; }',
        'return { method: "POST", path: "/v1/gen", body: body, responsePath: "data[0].url" };',
      ].join('\n'),
    );
    const res = await apiCall<{ id?: string; error?: { code: string } }>(
      `/api/v1/workspaces/${session.workspaceId}/generate`,
      {
        method: 'POST',
        token: session.token,
        body: {
          capability: 'image.generate',
          prompt: '一只猫',
          count: 1,
          providerId: pid,
          params: { size: '1k' },
        },
      },
    );
    // 脚本本身通过了沙箱；失败发生在「连不上 127.0.0.1:9」这一步，
    // 那是上游/网络问题，与沙箱无关。判据是「不是 script_* 错误」。
    const body = JSON.stringify(res.data ?? {});
    expect(body).not.toContain('script_syntax');
    expect(body).not.toContain('script_forbidden');
  });

  test('同一份脚本 + 同一份输入 = 同一份输出（重放前提）', async ({ session }) => {
    // 脚本看不到自己的源码（params.script 被剔除）：否则脚本能按「自己被怎么写的」
    // 改变行为，重放就不再成立。
    const pid = await createScriptProvider(
      session,
      `return { method: "POST", path: "/v1/gen", body: { leak: params.script }, responsePath: "url" };`,
    );
    const res = await apiCall<{ code?: string }>(
      `/api/v1/workspaces/${session.workspaceId}/generate`,
      {
        method: 'POST',
        token: session.token,
        body: { capability: 'image.generate', prompt: 'x', providerId: pid },
      },
    );
    expect(JSON.stringify(res.data ?? {})).not.toContain('return { method');
  });

  test('脚本不能设置 Authorization（凭据注入点不可被绕过）', async ({ session }) => {
    const pid = await createScriptProvider(
      session,
      `return { method: "POST", path: "/x", headers: { Authorization: "Bearer stolen" }, responsePath: "url" };`,
    );
    const res = await apiCall<{ code?: string; message?: string }>(
      `/api/v1/workspaces/${session.workspaceId}/generate`,
      {
        method: 'POST',
        token: session.token,
        body: { capability: 'image.generate', prompt: 'x', providerId: pid },
      },
    );
    expect(res.status).toBeGreaterThanOrEqual(400);
    expect(JSON.stringify(res.data ?? {})).toMatch(/Authorization|平台管理/);
  });

  test('脚本不能指定其他主机（否则它就是一条绕过 SSRF 守卫的通道）', async ({ session }) => {
    const pid = await createScriptProvider(
      session,
      `return { method: "GET", path: "http://169.254.169.254/latest/meta-data/", responsePath: "x" };`,
    );
    const res = await apiCall(
      `/api/v1/workspaces/${session.workspaceId}/generate`,
      {
        method: 'POST',
        token: session.token,
        body: { capability: 'image.generate', prompt: 'x', providerId: pid },
      },
    );
    expect(res.status).toBeGreaterThanOrEqual(400);
    expect(JSON.stringify(res.data ?? {})).not.toContain('meta-data');
  });
});

/**
 * 本地直连模式（4.21）。
 *
 * 三条约束都必须在**服务端**成立，否则直接调 API 就能绕过 UI：
 *   1. 默认关闭；
 *   2. 开启必须确认风险（acknowledgedAt）；
 *   3. 地址必须是回环（否则这个开关就是「服务端代任意地址发请求」）。
 */
test.describe('本地直连模式（4.21 / ATK-26）', () => {
  test('默认关闭', async ({ session }) => {
    const res = await apiCall<{ prefs: { direct?: { enabled?: boolean } } }>(
      `/api/v1/workspaces/${session.workspaceId}/prefs`,
      { token: session.token },
    );
    expect(res.status).toBe(200);
    expect(res.data.prefs.direct?.enabled ?? false).toBe(false);
  });

  test('只开开关不确认风险 → 服务端拒绝', async ({ session }) => {
    const res = await apiCall(`/api/v1/workspaces/${session.workspaceId}/prefs`, {
      method: 'PATCH',
      token: session.token,
      body: { prefs: { direct: { enabled: true, baseUrl: 'http://127.0.0.1:8317' } } },
    });
    expect(res.status).toBeGreaterThanOrEqual(400);
    expect(JSON.stringify(res.data ?? {})).toContain('确认风险');
  });

  test('非回环地址被拒绝', async ({ session }) => {
    for (const url of [
      'http://10.0.0.5:8317',
      'http://169.254.169.254/',
      'http://evil.example:80',
      'file:///etc/passwd',
    ]) {
      const res = await apiCall(`/api/v1/workspaces/${session.workspaceId}/prefs`, {
        method: 'PATCH',
        token: session.token,
        body: {
          prefs: {
            direct: {
              enabled: true,
              baseUrl: url,
              acknowledgedAt: new Date().toISOString(),
            },
          },
        },
      });
      expect(res.status, `${url} 应当被拒绝`).toBeGreaterThanOrEqual(400);
    }
  });

  test('回环地址 + 确认风险 → 接受，且范围默认只有 image.generate', async ({ session }) => {
    const res = await apiCall<{
      prefs: { direct?: { enabled: boolean; scope?: string[]; baseUrl?: string } };
    }>(`/api/v1/workspaces/${session.workspaceId}/prefs`, {
      method: 'PATCH',
      token: session.token,
      body: {
        prefs: {
          direct: {
            enabled: true,
            baseUrl: 'http://127.0.0.1:8317',
            acknowledgedAt: new Date().toISOString(),
          },
        },
      },
    });
    expect(res.status).toBe(200);
    expect(res.data.prefs.direct?.enabled).toBe(true);
    // 默认范围由服务端补齐；把 video.generate 放进默认集合会让
    // 昂贵任务静默走到未计量的路径。
    const scope = res.data.prefs.direct?.scope ?? [];
    expect(scope.length === 0 || scope.includes('image.generate')).toBe(true);
    expect(scope).not.toContain('video.generate');
  });

  test('未知能力名被拒绝（typo 不该变成「该能力不走直连」）', async ({ session }) => {
    const res = await apiCall(`/api/v1/workspaces/${session.workspaceId}/prefs`, {
      method: 'PATCH',
      token: session.token,
      body: {
        prefs: {
          direct: {
            enabled: true,
            baseUrl: 'http://127.0.0.1:8317',
            acknowledgedAt: new Date().toISOString(),
            scope: ['image.generate', 'admin.everything'],
          },
        },
      },
    });
    expect(res.status).toBeGreaterThanOrEqual(400);
  });
});

/**
 * 画布导出包含会话只读快照（2.11）。
 *
 * 判据是「有会话时带快照、且非文本条目被裁剪」——导出文件经常被贴进
 * 聊天群，工具调用参数里可能带凭据与内网地址。
 */
test.describe('画布导出（2.11 / 11.3）', () => {
  test('导出结构含 canvas 与版本号，且不带明文凭据', async ({ session }) => {
    const canvasId = await createCanvas(session, 'export-e2e');
    await apiCall(`/api/v1/canvases/${canvasId}/ops`, {
      method: 'POST',
      token: session.token,
      body: {
        baseVersion: 0,
        ops: [
          {
            kind: 'add_node',
            node: {
              id: 'n_export_1',
              type: 'prompt',
              title: 'p',
              rect: { x: 0, y: 0, w: 320, h: 220 },
              spec: { text: 'hello' },
            },
          },
        ],
      },
    });
    const res = await apiCall<{ version: number; kind: string; canvas: { nodes: unknown } }>(
      `/api/v1/canvases/${canvasId}/export`,
      { token: session.token },
    );
    expect(res.status).toBe(200);
    expect(res.data.kind).toBe('ic-canvas-export');
    expect(res.data.canvas.nodes).toBeTruthy();
    // 导出里绝不能出现凭据明文（INV-5）
    const raw = JSON.stringify(res.data);
    expect(raw).not.toContain('sk-e2e');
    // 若带会话快照，必须同时带说明（说明「非文本条目已裁剪」）
    if ('agentSessions' in (res.data as Record<string, unknown>)) {
      expect(raw).toContain('只读快照');
    }
  });
});
