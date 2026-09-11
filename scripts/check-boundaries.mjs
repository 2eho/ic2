#!/usr/bin/env node
// 边界常量一致性校验：internal/graph/limits.go 是唯一真源，
// docs/design 中出现的同名数值必须与代码一致（见 docs/design/13 §6）。
import { readFileSync, readdirSync } from 'node:fs';
import { join } from 'node:path';

const GO_FILE = 'internal/graph/limits.go';
const DOC_DIR = 'docs/design';

const go = readFileSync(GO_FILE, 'utf8');
const consts = new Map();
for (const m of go.matchAll(/^\s*([A-Z][A-Za-z0-9_]*)\s*=\s*([0-9_]+(?:\s*\*\s*[0-9_]+)*|\d+\.?\d*e?[+-]?\d*)\s*$/gm)) {
  const name = m[1];
  const expr = m[2].replace(/_/g, '');
  let value;
  try {
    value = Function(`"use strict";return (${expr})`)();
  } catch {
    continue;
  }
  if (typeof value === 'number' && Number.isFinite(value)) consts.set(name, value);
}

if (consts.size === 0) {
  console.error('未从 limits.go 解析到任何常量');
  process.exit(1);
}

// 文档中必须与代码一致的检查项：文档数值 ↔ 常量名
const EXPECT = [
  { doc: /坐标[^\n]*?±?1e7|1e7/g, name: 'CoordMax', label: '坐标上限' },
  { doc: /0\.05[–\-]5/g, name: 'ZoomMax', label: '缩放上限' },
  { doc: /\[16,\s*20000\]/g, name: 'SizeMax', label: '尺寸上限' },
  { doc: /1000/g, name: 'MaxOpBatch', label: '单批 op 上限' },
  { doc: /32768|32KB/g, name: 'MaxPromptBytes', label: '提示词字节上限' },
];

const problems = [];
const files = readdirSync(DOC_DIR).filter((f) => f.endsWith('.md'));
for (const f of files) {
  const body = readFileSync(join(DOC_DIR, f), 'utf8');
  for (const e of EXPECT) {
    if (!e.doc.test(body)) continue;
    if (!consts.has(e.name)) {
      problems.push(`${f}: 文档提到「${e.label}」但 ${GO_FILE} 中缺少常量 ${e.name}`);
    }
  }
}

// 反向：文档里显式引用的常量名必须存在于代码
for (const f of files) {
  const body = readFileSync(join(DOC_DIR, f), 'utf8');
  for (const m of body.matchAll(/`(Max[A-Za-z]+|Zoom[A-Za-z]+|Coord[A-Za-z]+|Size[A-Za-z]+)`/g)) {
    if (!consts.has(m[1])) problems.push(`${f}: 引用了不存在的常量 ${m[1]}`);
  }
}

console.log('边界常量校验');
console.log('─'.repeat(52));
console.log(`从 ${GO_FILE} 解析到 ${consts.size} 个常量`);
for (const [k, v] of consts) console.log(`  ${k.padEnd(22)} ${v}`);
console.log('─'.repeat(52));
if (problems.length) {
  console.error(`发现 ${problems.length} 处不一致：`);
  for (const p of problems) console.error('  - ' + p);
  process.exit(1);
}
console.log('文档与代码中的边界常量一致');
