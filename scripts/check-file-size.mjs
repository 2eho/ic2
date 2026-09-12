#!/usr/bin/env node
// 代码规模纪律（docs/design/08-infra.md §6）：
//   单文件 > 500 行必须在 PR 说明里解释。
// 上一轮的原项目有 3384 行的 project.tsx 与 1605 行的 canvas-node.tsx，
// 重写的目的之一就是消除巨型文件，所以这条约束必须自动执行而不是靠自觉。
//
// 规则：
//   - > 500 行：列为「需解释」，脚本默认只报告；
//   - > 800 行：硬失败（重写产物不该出现这种规模）；
//   - 生成物与测试豁免。
import { readdirSync, readFileSync, statSync } from 'node:fs';
import { join } from 'node:path';

const LIMIT_WARN = 500;
const LIMIT_FAIL = 800;
const EXEMPT = [/\.gen\.(go|ts)$/, /_test\.go$/, /\.test\.tsx?$/];

const walk = (dir, out = []) => {
  for (const name of readdirSync(dir)) {
    if (['node_modules', '.git', 'upstream', 'dist', 'bin'].includes(name)) continue;
    const p = join(dir, name);
    const st = statSync(p);
    if (st.isDirectory()) walk(p, out);
    else if (/\.(go|ts|tsx|mjs|sh|sql)$/.test(name)) out.push(p);
  }
  return out;
};

const roots = ['internal', 'cmd', 'web/src', 'web/e2e', 'canvas-agent/src', 'scripts', 'migrations', 'contracts'];
const files = [];
for (const r of roots) {
  try {
    walk(r, files);
  } catch {
    // 目录不存在时跳过（例如 contracts 尚未引入）
  }
}

const warn = [];
const fail = [];
for (const file of files) {
  if (EXEMPT.some((re) => re.test(file))) continue;
  const lines = readFileSync(file, 'utf8').split('\n').length;
  if (lines > LIMIT_FAIL) fail.push([file, lines]);
  else if (lines > LIMIT_WARN) warn.push([file, lines]);
}

console.log('代码规模检查');
console.log('─'.repeat(56));
console.log(`扫描文件 : ${files.length}（硬上限 ${LIMIT_FAIL} 行，需解释 ${LIMIT_WARN} 行）`);
for (const [f, n] of warn.sort((a, b) => b[1] - a[1])) console.log(`  需解释  ${String(n).padStart(5)}  ${f}`);
console.log('─'.repeat(56));
if (fail.length) {
  console.error(`以下文件超过 ${LIMIT_FAIL} 行，必须拆分：`);
  for (const [f, n] of fail) console.error(`  ${n} 行  ${f}`);
  process.exit(1);
}
console.log(warn.length ? `${warn.length} 个文件超过 ${LIMIT_WARN} 行（需在 PR 说明中解释，不阻断）` : '无超大文件');
