/**
 * 桥接器配置。
 *
 * 安全约束（docs/design/07 §3，逐条来自原项目已验证的结论）：
 *   - 只监听 127.0.0.1：否则同局域网任何人都能连上你的 Agent；
 *   - 令牌一次性生成并落盘 0600：固定令牌容易被从示例/文档里扫到；
 *   - Origin 白名单：**这条最容易被忽略**。只校验令牌是不够的——
 *     任意网页都能让浏览器带着「用户已授权的 cookie/token」来访问 127.0.0.1，
 *     也就是 DNS rebinding / CSRF 的经典形态。必须校验 Origin。
 */
import { chmodSync, existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { homedir } from 'node:os';
import { join } from 'node:path';
import { randomBytes } from 'node:crypto';

export const CONFIG_DIR = process.env.IC_AGENT_HOME ?? join(homedir(), '.ic');
export const CONFIG_PATH = join(CONFIG_DIR, 'agent.json');

/** 读取或创建配置（含一次性令牌）。 */
export function loadConfig() {
  mkdirSync(CONFIG_DIR, { recursive: true, mode: 0o700 });
  let cfg = {
    token: randomBytes(32).toString('hex'),
    port: Number(process.env.IC_AGENT_PORT ?? 17371),
    host: '127.0.0.1',
    // IC 站点 Origin 白名单：默认覆盖常见本地开发端口。
    allowedOrigins: (process.env.IC_AGENT_ORIGINS ??
      'http://localhost:5173,http://127.0.0.1:5173,http://localhost:8080,http://127.0.0.1:8080')
      .split(',')
      .map((s) => s.trim())
      .filter(Boolean),
    backend: process.env.IC_AGENT_BACKEND ?? 'auto', // auto | codex | claude
    codexCommand: process.env.IC_AGENT_CODEX ?? 'codex',
    claudeCommand: process.env.IC_AGENT_CLAUDE ?? 'claude',
    createdAt: new Date().toISOString(),
  };

  if (existsSync(CONFIG_PATH)) {
    try {
      const existing = JSON.parse(readFileSync(CONFIG_PATH, 'utf8'));
      // 只接受已知字段：配置文件被手工编辑过时，未知字段不应该进入运行时
      cfg = {
        ...cfg,
        token: existing.token || cfg.token,
        port: Number(existing.port) || cfg.port,
        host: existing.host || cfg.host,
        allowedOrigins: Array.isArray(existing.allowedOrigins) && existing.allowedOrigins.length
          ? existing.allowedOrigins
          : cfg.allowedOrigins,
        backend: existing.backend || cfg.backend,
      };
    } catch {
      // 配置损坏时重建，而不是崩溃——桥接器是辅助工具，
      // 它挂了不该让用户无法使用画布本身。
    }
  }

  writeFileSync(CONFIG_PATH, JSON.stringify(cfg, null, 2), { mode: 0o600 });
  try {
    chmodSync(CONFIG_PATH, 0o600);
  } catch {
    /* Windows 等平台可能不支持，忽略 */
  }
  return cfg;
}

/** 校验 Origin 是否在白名单内。 */
export function originAllowed(cfg, origin) {
  if (!origin) return false;
  return cfg.allowedOrigins.includes(origin);
}

/** 从 Authorization 头提取令牌（token 绝不走 URL：URL 会进日志与 referer）。 */
export function bearerToken(req) {
  const raw = req.headers.authorization ?? '';
  if (!raw.toLowerCase().startsWith('bearer ')) return '';
  return raw.slice(7).trim();
}
