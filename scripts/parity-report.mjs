#!/usr/bin/env node
// 对等矩阵覆盖率报告：解析 docs/design/10-parity-matrix.md 中的状态列。
// 覆盖率 = done / (总数 - dropped)，与 docs/design/13 §2.1 的门槛对齐。
import { readFileSync } from 'node:fs';

const FILE = 'docs/design/10-parity-matrix.md';
const THRESHOLDS = [
  { name: 'M0 骨架', min: 0 },
  { name: 'M1 画布内核与文档', min: 0.20 },
  { name: 'M2 资产与生成闭环', min: 0.45 },
  { name: 'M3 统一执行与多渠道', min: 0.65 },
  { name: 'M5 插件体系', min: 0.85 },
  { name: 'M6 Agent', min: 0.95 },
  { name: 'M7 发版', min: 1.0 },
];

const text = readFileSync(FILE, 'utf8');
const counts = { done: 0, wip: 0, todo: 0, dropped: 0, total: 0 };
const perSection = new Map();
let section = '未分组';

for (const rawLine of text.split('\n')) {
  const line = rawLine.trim();
  if (line.startsWith('## ')) {
    section = line.replace(/^##\s+/, '');
    continue;
  }
  // 只统计表格行，且形如 | ... | <status> |
  if (!line.startsWith('|')) continue;
  if (/^\|\s*-+/.test(line)) continue;
  const cells = line.split('|').map((c) => c.trim());
  if (cells.length < 3) continue;
  const status = cells[cells.length - 2] || '';
  if (!['todo', 'wip', 'done', 'dropped'].includes(status)) continue;
  counts[status]++;
  counts.total++;
  if (!perSection.has(section)) perSection.set(section, { done: 0, wip: 0, todo: 0, dropped: 0, total: 0 });
  const s = perSection.get(section);
  s[status]++;
  s.total++;
}

const denom = counts.total - counts.dropped;
const rate = denom === 0 ? 0 : counts.done / denom;

console.log('对等矩阵覆盖率报告');
console.log('─'.repeat(52));
console.log(`条目总数   : ${counts.total}`);
console.log(`done       : ${counts.done}`);
console.log(`wip        : ${counts.wip}`);
console.log(`todo       : ${counts.todo}`);
console.log(`dropped    : ${counts.dropped}（不计入分母）`);
console.log(`覆盖率     : ${(rate * 100).toFixed(2)}%  (done / (total - dropped))`);
console.log('─'.repeat(52));
for (const [name, s] of perSection) {
  const d = s.total - s.dropped;
  const r = d === 0 ? 0 : s.done / d;
  console.log(`${name.padEnd(28)} ${(r * 100).toFixed(1).padStart(6)}%  (${s.done}/${d})`);
}
console.log('─'.repeat(52));

// 门槛校验：默认只报告，CI 可通过 --enforce=<milestone> 强制
const enforceArg = process.argv.find((a) => a.startsWith('--enforce='));
const minArg = process.argv.find((a) => a.startsWith('--min='));
if (minArg) {
  const min = Number(minArg.split('=')[1]);
  if (rate + 1e-9 < min) {
    console.error(`覆盖率 ${(rate * 100).toFixed(2)}% 低于门槛 ${(min * 100).toFixed(2)}%`);
    process.exit(1);
  }
  console.log(`覆盖率达标（>= ${(min * 100).toFixed(0)}%）`);
}
if (enforceArg) {
  const name = enforceArg.split('=')[1];
  const t = THRESHOLDS.find((x) => x.name.startsWith(name));
  if (!t) {
    console.error(`未知里程碑: ${name}`);
    process.exit(2);
  }
  if (rate + 1e-9 < t.min) {
    console.error(`${t.name} 覆盖率 ${(rate * 100).toFixed(2)}% 低于门槛 ${(t.min * 100).toFixed(0)}%`);
    process.exit(1);
  }
  console.log(`${t.name} 覆盖率达标`);
}

// 报告矩阵里出现但状态非法/未知的行数，用于发现文档笔误
const unknown = [];
for (const rawLine of text.split('\n')) {
  const line = rawLine.trim();
  if (!line.startsWith('|')) continue;
  if (/^\|\s*-+/.test(line)) continue;
  const cells = line.split('|').map((c) => c.trim());
  if (cells.length < 4) continue;
  const last = cells[cells.length - 2] || '';
  if (/^(todo|wip|done|dropped)$/.test(last)) continue;
  // 跳过表头与说明行
  if (/^#/.test(cells[1] || '')) continue;
  if (!/^\d/.test(cells[1] || '')) continue;
  unknown.push(cells[1]);
}
if (unknown.length) {
  console.warn(`警告：${unknown.length} 行缺少合法状态标记：${unknown.slice(0, 5).join(', ')}...`);
}
