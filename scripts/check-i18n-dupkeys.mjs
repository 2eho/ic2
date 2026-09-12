#!/usr/bin/env node
// i18n 字典重复键检查。
//
// 为什么需要独立脚本：TS 会报 TS1117，但只在**跑 tsc 时**才报；
// 而 i18n 字典是纯数据，「多了一个键」很容易被当成无害。
// 实测本项目已连续三次踩到（agent / assets / settings 三处），
// 每次都表现为「文案没生效」——因为后者覆盖前者，或反之，取决于位置。
// 这类问题不报错、只静默用错值，必须由脚本拦住。
import { readFileSync } from 'node:fs';

const FILES = ['web/src/shared/i18n/zh-CN.ts', 'web/src/shared/i18n/en-US.ts'];
const problems = [];

for (const file of FILES) {
  const lines = readFileSync(file, 'utf8').split('\n');
  // 逐层记录作用域（按缩进）里的键，检测同一作用域内的重复
  const scopes = new Map(); // depth -> Set<key>
  let depth = 0;
  let inString = false;

  for (const [index, raw] of lines.entries()) {
    const line = raw.trimEnd();
    if (!line.trim() || line.trim().startsWith('//')) continue;

    // 粗略跳过模板字符串/多行字符串里的内容（本文件里不存在，但保守处理）
    if ((line.match(/`/g) ?? []).length % 2 === 1) inString = !inString;
    if (inString) continue;

    // 作用域结束
    let closeCount = 0;
    for (const ch of line) {
      if (ch === '}') closeCount += 1;
    }
    if (closeCount > 0) {
      for (let i = 0; i < closeCount; i += 1) {
        scopes.delete(depth);
        depth = Math.max(0, depth - 1);
      }
    }

    const m = line.match(/^(\s*)([A-Za-z_$][\w$]*)\s*:\s*(.*)$/);
    if (m) {
      const indent = m[1].length;
      const key = m[2];
      const value = m[3];
      const scopeDepth = indent;
      if (!scopes.has(scopeDepth)) scopes.set(scopeDepth, new Set());
      const set = scopes.get(scopeDepth);
      if (set.has(key)) {
        problems.push(`${file}:${index + 1} 同一层级重复定义键 "${key}"（后者会覆盖前者，文案可能静默用错）`);
      }
      set.add(key);

      // 值是对象字面量 → 进入更深一层
      if (value.trim().startsWith('{')) {
        depth = scopeDepth + 2;
        scopes.delete(depth);
        scopes.set(depth, new Set());
      }
    }
  }
}

console.log('i18n 重复键检查');
console.log('─'.repeat(56));
console.log(`扫描文件 : ${FILES.length}`);
console.log('─'.repeat(56));
if (problems.length) {
  console.error(`发现 ${problems.length} 处重复键：`);
  for (const p of problems) console.error('  - ' + p);
  process.exit(1);
}
console.log('无重复键');
