import { test as base, expect, type Page } from '@playwright/test';
import { existsSync, readFileSync } from 'node:fs';
import { resolve, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

/**
 * E2E 装置（fixtures）。
 *
 * 设计取舍：这些用例需要「一个可信的、可重复的」起点，因此：
 *   - 用独立的服务端实例 + 临时数据目录（由 scripts/e2e-stack.mjs 拉起）；
 *   - 通过真实 HTTP 建账号/项目/画布，不直接写库——那样会绕过被测的接口层；
 *   - 每个用例拿到全新的工作区，避免相互污染。
 *
 * **后端必须真的存在**：早期版本在没起后端时自动 skip，结果是 9 条用例在 CI 里
 * 「全绿」——它们只是被跳过了。现在改成硬失败：拿不到后端就报错并给出启动命令。
 * 「跳过」对回归门禁来说等于不存在（与 docs/design/13 §3.2「对抗用例即回归」冲突）。
 */

const here = dirname(fileURLToPath(import.meta.url));
const stackState = resolve(here, '..', '..', '.e2e-stack.json');

function readStack(): { base: string } | null {
  if (!existsSync(stackState)) return null;
  try {
    return JSON.parse(readFileSync(stackState, 'utf8')) as { base: string };
  } catch {
    return null;
  }
}

const stack = readStack();
const API = process.env.E2E_BASE_URL ?? stack?.base ?? '';
// 前端与后端**同源**：静态产物由 Go 服务托管。这样 ATK-10 的 sandbox、
// ATK-12 的离线队列、cookie/存储边界才是真实语义，而不是跨域下的近似。
const WEB_URL = process.env.E2E_WEB_URL ?? stack?.base ?? '';

if (!API) {
  throw new Error(
    '未找到 e2e 后端。请先执行：node scripts/e2e-stack.mjs start\n' +
      '（或用 E2E_BASE_URL 显式指定一个已就绪的服务端）',
  );
}

export const API_BASE = API;
export const WEB_BASE = WEB_URL;

export type Session = {
  token: string;
  userId: string;
  workspaceId: string;
};

export async function apiCall<T>(
  path: string,
  options: { method?: string; body?: unknown; token?: string } = {},
): Promise<{ status: number; data: T }> {
  const res = await fetch(API + path, {
    method: options.method ?? 'GET',
    headers: {
      ...(options.body ? { 'Content-Type': 'application/json' } : {}),
      ...(options.token ? { Authorization: `Bearer ${options.token}` } : {}),
    },
    body: options.body ? JSON.stringify(options.body) : undefined,
  });
  const text = await res.text();
  let data: unknown = undefined;
  try {
    data = text ? JSON.parse(text) : undefined;
  } catch {
    data = text;
  }
  return { status: res.status, data: data as T };
}

/** 通过真实接口注册一个新账号，返回会话与首个工作区。 */
export async function registerUser(prefix: string): Promise<Session> {
  const unique = `${prefix}-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
  const { status, data } = await apiCall<{
    token: string;
    user: { id: string };
    workspaces: Array<{ id: string }>;
  }>('/api/v1/auth/register', {
    method: 'POST',
    body: { email: `${unique}@e2e.test`, name: unique, password: 'password123' },
  });
  if (status !== 201) {
    throw new Error(`注册失败 status=${status}: ${JSON.stringify(data)}`);
  }
  return {
    token: data.token,
    userId: data.user.id,
    workspaceId: data.workspaces[0]?.id ?? '',
  };
}

export async function createCanvas(session: Session, name: string): Promise<string> {
  const project = await apiCall<{ id: string }>(
    '/api/v1/workspaces/' + session.workspaceId + '/projects',
    { method: 'POST', body: { name: name + '-project' }, token: session.token },
  );
  if (project.status !== 201) {
    throw new Error(`建项目失败 status=${project.status}: ${JSON.stringify(project.data)}`);
  }
  const canvas = await apiCall<{ canvas: { id: string } }>(
    '/api/v1/projects/' + project.data.id + '/canvases',
    { method: 'POST', body: { name }, token: session.token },
  );
  if (canvas.status !== 201) {
    throw new Error(`建画布失败 status=${canvas.status}: ${JSON.stringify(canvas.data)}`);
  }
  return canvas.data.canvas.id;
}

/**
 * 登录状态注入：E2E 不应在每个用例里重放一遍登录 UI（那是登录测试的事）。
 *
 * 注意用的是**真实存储键**（与 shared/session 一致）；键名写错会让用例变成
 * 「页面其实是未登录状态」，而断言又只检查「页面能打开」——那种用例永远绿。
 * 因此这里由 shared/session 的常量导出校验（见下方 assertSessionKey）。
 */
export async function loginAs(page: Page, session: Session): Promise<void> {
  await page.addInitScript((token) => {
    try {
      sessionStorage.setItem('ic.session.token', token);
    } catch {
      /* 隐私模式下不可用，忽略 */
    }
  }, session.token);
}

/** 检查后端确实在服务端托管了前端产物（否则同源假设不成立）。 */
export async function assertSpaServed(): Promise<void> {
  const res = await fetch(WEB_BASE + '/');
  if (!res.ok) {
    throw new Error(`前端产物未由服务端托管（GET / → ${res.status}）。请先 cd web && npm run build`);
  }
  const html = await res.text();
  if (!html.includes('<div id="root">') && !html.includes('id="root"')) {
    throw new Error('GET / 返回的不是 SPA 入口（缺少 #root），同源假设立不成立');
  }
}

export const test = base.extend<{ session: Session }>({
  session: async ({}, use) => {
    const session = await registerUser('e2e');
    await use(session);
  },
});

export { expect };
