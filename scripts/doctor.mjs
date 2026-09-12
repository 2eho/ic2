#!/usr/bin/env node
// 环境体检：一次性回答「这台机器现在能不能跑门禁 / 起服务」。
//
// 与 cmd/ic-cli doctor 的分工：
//   - ic-cli doctor：服务端**运行时**体检（数据目录、密钥、DB、Blob）；
//   - 本脚本：**开发/CI 环境**体检（工具链、依赖、可构建性）。
// 两者都不自动修复：自动修复会把环境问题变成隐式副作用，更难排查。
import { existsSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

const problems = [];
const notes = [];

function run(cmd, args, opts = {}) {
  return spawnSync(cmd, args, { encoding: 'utf8', ...opts });
}

function check(name, fn) {
  const r = fn();
  if (r.ok) {
    console.log(`  ✓ ${name}${r.detail ? ` — ${r.detail}` : ''}`);
  } else {
    console.log(`  ✗ ${name} — ${r.detail}`);
    problems.push(`${name}: ${r.detail}${r.fix ? `\n      修复：${r.fix}` : ''}`);
  }
  return r;
}

console.log('IC 环境体检');
console.log('─'.repeat(64));

// ---------------------------------------------------------------- 工具链
const probe = run('node', ['scripts/toolchain-probe.mjs']);
check('工具链', () => ({
  ok: probe.status === 0,
  detail:
    probe.status === 0
      ? '必需工具齐备'
      : '有必需工具缺失（详见上方 toolchain-probe 输出）',
  fix: 'node scripts/toolchain-probe.mjs 查看逐项缺失与安装方式',
}));

// ---------------------------------------------------------------- 契约生成物
check('契约生成物', () => {
  const r = run('node', ['scripts/gen-contracts.mjs', '--check']);
  return {
    ok: r.status === 0,
    detail: r.status === 0 ? '与 contracts/ 一致' : '不一致或工具缺失',
    fix: 'make gen（并提交生成物）；若提示工具缺失先按上方修复',
  };
});

// ---------------------------------------------------------------- Go 可构建
check('Go 构建', () => {
  const out = join(tmpdir(), `ic-doctor-server-${Date.now()}`);
  const r = run('go', ['build', '-o', out, './cmd/ic-server']);
  if (r.status === 0) rmSync(out, { force: true });
  return {
    ok: r.status === 0,
    detail: r.status === 0 ? 'ic-server 可构建' : (r.stderr ?? '').split('\n')[0],
    fix: 'go mod download',
  };
});

// ---------------------------------------------------------------- 前端依赖
check('前端依赖', () => ({
  ok: existsSync('web/node_modules'),
  detail: existsSync('web/node_modules') ? 'web/node_modules 存在' : 'web/node_modules 缺失',
  fix: 'make deps（或 cd web && npm install）',
}));

// ---------------------------------------------------------------- 数据目录可写
// 用真实写探测，而不是看 mode 位：容器里 mode 经常不可靠（与 ic-cli doctor 一致）。
check('临时目录可写', () => {
  const dir = mkdtempSync(join(tmpdir(), 'ic-doctor-'));
  try {
    const f = join(dir, 'probe');
    writeFileSync(f, 'ok');
    return { ok: true, detail: dir };
  } catch (e) {
    return { ok: false, detail: String(e), fix: '检查 TMPDIR 权限' };
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

// ---------------------------------------------------------------- e2e 前置
check('e2e 前置（可跳过而不算失败）', () => {
  const hasBrowser = existsSync(
    join(process.env.HOME ?? '/root', '.cache', 'ms-playwright'),
  );
  if (hasBrowser) return { ok: true, detail: 'Playwright 浏览器已安装' };
  // e2e 需要浏览器，但开发环境没装它不该阻塞其他工作 → 记为提示而非问题
  notes.push('Playwright 浏览器未安装：make e2e 前执行 npx playwright install --with-deps chromium');
  return { ok: true, detail: '浏览器未安装（仅影响 make e2e）' };
});

console.log('─'.repeat(64));
for (const n of notes) console.log(`  提示：${n}`);
if (problems.length) {
  console.error(`\n发现 ${problems.length} 处问题：`);
  for (const p of problems) console.error('  - ' + p);
  process.exit(1);
}
console.log('环境就绪');
