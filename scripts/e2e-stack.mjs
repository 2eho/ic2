#!/usr/bin/env node
// e2e 栈引导：起一个**真实的服务端进程**（不是 httptest），供 Playwright 打。
//
// 为什么必须是真的进程而不是 in-process handler：
//   - ATK-10（插件沙箱）要看真实浏览器与真实页面的 origin/sandbox 语义；
//   - ATK-12（离线恢复）要能真的切断网络再恢复；
//   - 同源托管（静态产物由 Go 服务提供）只有真进程才有。
//   用 httptest 顶替会让这些用例变成「自证」——它们永远绿，因为被测的边界根本没被经过。
//
// 用法：
//   node scripts/e2e-stack.mjs start   # 构建并启动，写 .e2e-stack.json
//   node scripts/e2e-stack.mjs stop    # 停止并清理
//   node scripts/e2e-stack.mjs status  # 健康检查
import { spawn, spawnSync } from 'node:child_process';
import { existsSync, mkdtempSync, rmSync, readFileSync, writeFileSync, openSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';

const STATE = resolve('.e2e-stack.json');
const PORT = Number(process.env.E2E_PORT ?? 18080);
const ROOT = mkdtempSync(join(tmpdir(), 'ic-e2e-'));

const goBin = process.env.GO ?? 'go';

function resolveTool(name, extraDirs = []) {
  for (const dir of extraDirs) {
    const candidate = join(dir, name);
    if (existsSync(candidate)) return resolve(candidate);
  }
  const probe = spawnSync('sh', ['-c', `command -v ${name}`], { encoding: 'utf8' });
  return probe.status === 0 ? probe.stdout.trim() || null : null;
}

const GO = resolveTool(goBin) ?? goBin;

function log(msg) {
  process.stdout.write(`[e2e-stack] ${msg}\n`);
}

async function waitReady(url, timeoutMs = 60_000) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    try {
      const res = await fetch(url);
      if (res.ok) return true;
    } catch {
      /* 还没起来 */
    }
    await new Promise((r) => setTimeout(r, 250));
  }
  return false;
}

async function start() {
  if (existsSync(STATE)) {
    const prev = JSON.parse(readFileSync(STATE, 'utf8'));
    try {
      process.kill(prev.pid, 0);
      log(`已有实例在跑（pid=${prev.pid}），先停掉`);
      await stop();
    } catch {
      rmSync(STATE, { force: true });
    }
  }

  log('构建 ic-server…');
  const binDir = join(ROOT, 'bin');
  const build = spawnSync(GO, ['build', '-o', join(binDir, 'ic-server'), './cmd/ic-server'], {
    encoding: 'utf8',
  });
  if (build.status !== 0) {
    console.error(build.stdout, build.stderr);
    throw new Error('ic-server 构建失败（e2e 无法在真实服务上运行）');
  }

  const dataDir = join(ROOT, 'data');
  const blobDir = join(ROOT, 'blobs');
  const env = {
    ...process.env,
    IC_MODE: 'standalone',
    IC_ROLE: 'all',
    IC_LISTEN: `127.0.0.1:${PORT}`,
    IC_DB_DRIVER: 'sqlite',
    IC_DB_DSN: `file:${join(dataDir, 'ic.db')}`,
    IC_BLOB_DRIVER: 'fs',
    IC_BLOB_FS_ROOT: blobDir,
    // e2e 用**固定**密钥：每次随机会让「重启后旧密文仍可读」这条无法验证，
    // 而它正是备份/恢复演练要证明的东西。
    IC_SECRET_KEY: 'BwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwc=',
    IC_ALLOW_REGISTRATION: 'true',
    IC_LOG_LEVEL: 'warn',
    IC_LOG_FORMAT: 'text',
    // 静态产物由服务端同源托管：e2e 必须走「同一个 origin」，
    // 否则跨域会掩盖 cookie/sandbox/CSP 的真实行为。
    IC_STATIC_DIR: resolve(process.env.E2E_STATIC_DIR ?? 'web/dist'),
    IC_SSRF_ALLOW_PRIVATE: 'true',
  };

  log(`启动服务端 :${PORT}（数据目录 ${ROOT}）`);
  // 日志写文件而不是管到父进程：父进程退出后管道会关掉，
  // 子进程下一次写日志就会收到 EPIPE 而**直接死掉**（实测表现是
  // 「start 报告就绪，随后 status 永远不健康」——非常难查）。
  const logFile = join(ROOT, 'server.log');
  const out = openSync(logFile, 'a');
  const child = spawn(join(binDir, 'ic-server'), [], {
    env,
    // detached + 忽略 stdio：让服务端脱离本次 CLI 的生命周期，
    // 否则 CLI 一退出就被一起收走（e2e 会拿到「连接被拒」而不是真实失败）。
    detached: true,
    stdio: ['ignore', out, out],
  });

  const base = `http://127.0.0.1:${PORT}`;
  const ready = await waitReady(`${base}/healthz`);
  if (!ready) {
    try {
      process.kill(child.pid, 'SIGKILL');
    } catch {
      /* 已退出 */
    }
    console.error(readFileSync(logFile, 'utf8'));
    throw new Error('服务端未在期限内就绪（/healthz 未返回 200）：真实原因见上方服务端日志');
  }

  // 前端产物必须先构建：服务端只托管产物，不会替我们构建。
  writeFileSync(
    STATE,
    JSON.stringify({ pid: child.pid, port: PORT, base, root: ROOT, startedAt: Date.now() }, null, 2),
  );
  log(`就绪：${base}`);
  child.unref();
}

async function stop() {
  if (!existsSync(STATE)) {
    log('没有记录在跑的实例');
    return;
  }
  const st = JSON.parse(readFileSync(STATE, 'utf8'));
  try {
    process.kill(st.pid, 'SIGTERM');
    // 给它 5s 优雅退出（服务端有 graceful shutdown）
    await new Promise((r) => setTimeout(r, 500));
    process.kill(st.pid, 0);
    await new Promise((r) => setTimeout(r, 3000));
    process.kill(st.pid, 'SIGKILL');
  } catch {
    /* 已经退出 */
  }
  try {
    rmSync(st.root, { recursive: true, force: true });
  } catch {
    /* 清理失败不影响结论 */
  }
  rmSync(STATE, { force: true });
  log('已停止并清理');
}

async function status() {
  if (!existsSync(STATE)) {
    console.log('未启动');
    process.exit(1);
  }
  const st = JSON.parse(readFileSync(STATE, 'utf8'));
  const ready = await waitReady(`${st.base}/healthz`, 2000);
  console.log(JSON.stringify({ ...st, healthy: ready }, null, 2));
  process.exit(ready ? 0 : 1);
}

const cmd = process.argv[2] ?? 'status';
try {
  if (cmd === 'start') await start();
  else if (cmd === 'stop') await stop();
  else await status();
} catch (e) {
  console.error(`[e2e-stack] 失败：${e.message}`);
  process.exit(1);
}
