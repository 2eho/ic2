import { test as base, expect, type Page } from '@playwright/test';

/**
 * E2E 装置（fixtures）。
 *
 * 设计取舍：这些用例需要「一个可信的、可重复的」起点，因此：
 *   - 用独立的后端实例 + 临时数据目录（不用生产库）；
 *   - 通过真实 HTTP 建账号/项目/画布，不直接写库——那样会绕过被测的接口层；
 *   - 每个用例拿到全新的工作区，避免相互污染。
 *
 * 后端地址由 E2E_BASE_URL 提供；未提供时用例自动 skip 而不是失败：
 * 这是为了让「没起后端」变成明确的跳过，而不是一个看起来像代码 bug 的红。
 */
export const API = process.env.E2E_BASE_URL ?? 'http://127.0.0.1:8080';

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
  const project = await apiCall<{ id: string }>('/api/v1/workspaces/' + session.workspaceId + '/projects', {
    method: 'POST',
    body: { name: name + '-project' },
    token: session.token,
  });
  const canvas = await apiCall<{ canvas: { id: string } }>(
    '/api/v1/projects/' + project.data.id + '/canvases',
    { method: 'POST', body: { name }, token: session.token },
  );
  return canvas.data.canvas.id;
}

/** 登录状态注入：E2E 不应在每个用例里重放一遍登录 UI（那是登录测试的事）。 */
export async function loginAs(page: Page, session: Session): Promise<void> {
  await page.addInitScript((token) => {
    try {
      sessionStorage.setItem('ic.session.token', token);
    } catch {
      /* 隐私模式下不可用，忽略 */
    }
  }, session.token);
}

export const test = base.extend<{ session: Session }>({
  session: async ({}, use) => {
    const session = await registerUser('e2e');
    await use(session);
  },
});

export { expect };
