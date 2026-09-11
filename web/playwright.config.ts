import { defineConfig, devices } from '@playwright/test';

/**
 * Playwright 配置（对齐 docs/design/13 §3.1 的 e2e 层）。
 *
 * 关键取舍：
 *   - 只用 chromium：本项目的主链路（画布内核、SSE、iframe 沙箱）在 Chromium 上
 *     可完整验证；跨浏览器回归放在发版前的人工清单，不进 CI（避免 3 倍时长换 0 收益）。
 *   - 失败时保留 trace：e2e 失败最难排查，trace 是唯一能复盘「当时页面上发生了什么」的手段。
 *   - 后端地址与启动方式由外部提供（webServer 只在本地未起后端时启动前端 dev server）。
 */
export default defineConfig({
  testDir: './e2e',
  timeout: 60_000,
  expect: { timeout: 10_000 },
  fullyParallel: false, // 用例共享后端实例，串行可获得稳定的性能数字
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  workers: 1,
  reporter: process.env.CI ? [['list'], ['html', { open: 'never' }]] : 'list',
  use: {
    baseURL: process.env.E2E_WEB_URL ?? 'http://127.0.0.1:5173',
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    video: 'off',
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
});
