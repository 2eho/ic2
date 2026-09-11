#!/usr/bin/env node
// 画布内核纯度检查：kernel/ 目录不得 import react（docs/design/01 §3 硬约束）。
// 这条约束是「视口操作零 React 重渲染」的前提，一旦破坏性能目标就不复存在。
import { readdirSync, readFileSync, statSync } from 'node:fs';
import { join } from 'node:path';

const KERNEL_DIR = 'web/src/features/canvas/kernel';
const FORBIDDEN = [
  { re: /from\s+['"]react['"]/, label: "import react" },
  { re: /from\s+['"]react-dom/, label: 'import react-dom' },
  { re: /from\s+['"]@tanstack\/react-query/, label: 'import react-query' },
  { re: /from\s+['"]react-router/, label: 'import react-router' },
  { re: /from\s+['"]zustand/, label: 'import zustand' },
  { re: /from\s+['"]@\//, label: 'import 应用层别名（内核必须自包含）' },
];

const problems = [];

function walk(dir) {
  for (const name of readdirSync(dir)) {
    const p = join(dir, name);
    const st = statSync(p);
    if (st.isDirectory()) {
      if (name === '__tests__') continue; // 测试可用任何依赖
      walk(p);
      continue;
    }
    if (!/\.(ts|tsx)$/.test(name)) continue;
    const body = readFileSync(p, 'utf8');
    for (const f of FORBIDDEN) {
      if (f.re.test(body)) {
        problems.push(`${p}: ${f.label}`);
      }
    }
    // .tsx 不应出现在内核里（内核不含 JSX）
    if (name.endsWith('.tsx')) {
      problems.push(`${p}: 内核不应包含 .tsx（内核与渲染层解耦）`);
    }
  }
}

walk(KERNEL_DIR);

console.log('画布内核纯度检查');
console.log('─'.repeat(52));
if (problems.length) {
  console.error(`发现 ${problems.length} 处违规：`);
  for (const p of problems) console.error('  - ' + p);
  process.exit(1);
}
console.log('通过：kernel/ 不依赖 React 与应用层');
