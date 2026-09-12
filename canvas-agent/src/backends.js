/**
 * 后端进程管理：启动 Codex app-server / Claude Code CLI 并解析其输出流。
 *
 * 关键设计：**桥接器不执行模型任务，只转接**。
 * 它自己不读文件、不跑命令——所有实际能力由用户本机的 Codex/Claude 提供，
 * 用户对自己本机的行为有完全的控制权（可审计、可随时停）。
 *
 * 进程管理要点：
 *   - 子进程随桥接器退出而终止（避免孤儿进程占用端口/文件句柄）；
 *   - stdout 按行解析（两个后端都是 JSON Lines）；
 *   - 解析失败的行不丢弃，作为 error item 上报（否则用户看到「卡住」查不出原因）。
 */
import { spawn, spawnSync } from 'node:child_process';
import { normalizeClaude, normalizeCodex } from './normalize.js';

/** 检测本机有哪些可用后端。 */
export function detectBackends(cfg) {
  const found = [];
  // 用 which/where 探测而不是直接执行：直接执行可能触发登录流程。
  for (const [name, cmd] of [['codex', cfg.codexCommand], ['claude', cfg.claudeCommand]]) {
    if (commandExists(cmd)) found.push(name);
  }
  return found;
}

function commandExists(cmd) {
  // 用 `--version` 探测而不是 which：Windows 与容器里 which 不一定存在，
  // 而 `--version` 对两个 CLI 都是安全且快速的。
  try {
    const r = spawnSync(cmd, ['--version'], { stdio: 'ignore', timeout: 3000 });
    return r.status === 0 || r.status === 1;
  } catch {
    return false;
  }
}

/** 解析一行 JSON；失败时返回 error item（不静默丢弃）。 */
export function parseLine(line, backend, ctx = {}) {
  const trimmed = line.trim();
  if (!trimmed) return [];
  let parsed;
  try {
    parsed = JSON.parse(trimmed);
  } catch {
    return [
      {
        kind: 'error',
        itemId: `${ctx.turnId ?? 'turn'}:parse`,
        payload: {
          code: 'unparsable_upstream_line',
          // 行内容截断：可能很长，但必须保留一点上下文才能定位
          message: trimmed.length > 200 ? trimmed.slice(0, 200) + '…' : trimmed,
        },
      },
    ];
  }
  const raw = backend === 'claude' ? normalizeClaude(parsed, ctx) : normalizeCodex(parsed, ctx);
  if (raw == null) return [];
  return Array.isArray(raw) ? raw : [raw];
}

/** 启动一个后端进程并逐行回调。返回 { child, stop }。 */
export function startBackend(kind, cfg, onLine, onExit) {
  const command = kind === 'claude' ? cfg.claudeCommand : cfg.codexCommand;
  const args = kind === 'claude' ? ['--print', '--output-format', 'stream-json'] : ['app-server'];
  const child = spawn(command, args, {
    stdio: ['pipe', 'pipe', 'pipe'],
    env: { ...process.env },
  });

  let buffer = '';
  child.stdout.on('data', (chunk) => {
    buffer += chunk.toString('utf8');
    let idx;
    while ((idx = buffer.indexOf('\n')) >= 0) {
      const line = buffer.slice(0, idx);
      buffer = buffer.slice(idx + 1);
      onLine(line);
    }
  });
  child.stderr.on('data', (chunk) => {
    onLine(JSON.stringify({ type: 'error', error: { message: chunk.toString('utf8').slice(0, 500) } }));
  });
  child.on('exit', (code, signal) => onExit?.(code, signal));
  child.on('error', (err) => {
    onLine(JSON.stringify({ type: 'error', error: { code: 'backend_spawn_failed', message: err.message } }));
    onExit?.(-1, null);
  });

  return {
    child,
    send(line) {
      child.stdin.write(line.endsWith('\n') ? line : line + '\n');
    },
    stop() {
      try {
        child.kill('SIGTERM');
      } catch {
        /* 已退出 */
      }
    },
  };
}
