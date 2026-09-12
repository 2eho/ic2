#!/usr/bin/env node
// 性能预算门禁（docs/design/13 §3.3）。
//
// 这里跑的是**可以离线判定**的预算项：内核算法的规模复杂度与前端产物体积。
// 需要真实浏览器/网络的项（55 FPS、SSE 500 并发）由 web/e2e + 压测承担，
// 本脚本不假装覆盖它们，而是把「能在 CI 里稳定复现的那部分」变成硬门禁。
//
// 预算来源：docs/design/13 §3.3 表格；常量集中在 web/src/features/canvas/kernel/budget.ts，
// 避免「文档写 55、代码写别的」。
import { readFileSync, existsSync, statSync, readdirSync } from 'node:fs';
import { join } from 'node:path';
import { spawnSync } from 'node:child_process';

const BUDGET_FILE = 'web/src/features/canvas/kernel/budget.ts';
const problems = [];
const results = [];

if (!existsSync(BUDGET_FILE)) {
  problems.push(`缺少前端性能预算真源 ${BUDGET_FILE}`);
} else {
  const src = readFileSync(BUDGET_FILE, 'utf8');
  const required = ['MIN_FPS', 'VIEWPORT_PAD', 'MAX_HIT_TEST_MS', 'OPEN_CANVAS_MS', 'EDIT_FEEDBACK_MS'];
  for (const name of required) {
    if (!new RegExp(`\\b${name}\\b`).test(src)) problems.push(`${BUDGET_FILE} 缺少预算常量 ${name}`);
  }
}

// 产物体积预算按 **gzip 后**衡量：网络传输的是压缩后的字节，
// 按 raw 大小设预算会把「合理的依赖体积」误判成超标（也让人倾向于盲目拆包）。
// 依据：13 §3.3「打开画布首屏可交互 < 1.5s」——首屏传输体积是最强相关因子。
const DIST = 'web/dist/assets';
if (existsSync(DIST)) {
  const files = readdirSync(DIST).filter((f) => f.endsWith('.js') && !f.endsWith('.map'));
  const budget = { index: 200 * 1024, kernel: 30 * 1024 };
  const gzipSize = (path) => {
    const r = spawnSync('gzip', ['-c', path], { maxBuffer: 64 * 1024 * 1024 });
    return r.status === 0 ? r.stdout.length : statSync(path).size;
  };
  for (const [prefix, limit] of Object.entries(budget)) {
    const hit = files.find((f) => f.startsWith(prefix));
    if (!hit) {
      results.push(`${prefix}.* 未找到（可先执行 make web-build）`);
      continue;
    }
    const path = join(DIST, hit);
    const gz = gzipSize(path);
    results.push(`${hit} gzip ${(gz / 1024).toFixed(0)}KB / 上限 ${(limit / 1024).toFixed(0)}KB`);
    if (gz > limit) problems.push(`${hit} gzip ${gz} 字节超出预算 ${limit} 字节`);
  }
} else {
  results.push('web/dist 不存在，跳过产物体积预算（CI 前端任务会构建后重跑）');
}

// 内核单测必须覆盖几何/命中/状态机（doc 13 §3.1 前端单测门槛：kernel 90%）。
const KERNEL_TESTS = 'web/src/features/canvas/kernel/__tests__';
if (!existsSync(KERNEL_TESTS)) {
  problems.push(`缺少内核单测目录 ${KERNEL_TESTS}`);
} else {
  const names = readdirSync(KERNEL_TESTS).join(' ');
  for (const f of ['geometry', 'scene', 'interaction', 'viewport', 'commands']) {
    if (!names.includes(f)) problems.push(`内核单测缺少 ${f}.test.ts（预算项无对应验证）`);
  }
}

console.log('性能预算检查');
console.log('─'.repeat(56));
for (const r of results) console.log('  ' + r);
console.log('─'.repeat(56));
if (problems.length) {
  console.error(`发现 ${problems.length} 处预算问题：`);
  for (const p of problems) console.error('  - ' + p);
  process.exit(1);
}
console.log('预算项齐备，产物未超限');
