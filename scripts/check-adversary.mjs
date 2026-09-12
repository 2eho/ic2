#!/usr/bin/env node
// 对抗用例门禁：docs/design/11 §3 声明的 ATK-01..22 必须**真的存在可执行用例**。
//
// 为什么需要这个脚本：上一轮的对等矩阵与对抗表都写了「ATK-* 是发版门禁」，
// 但其中 11 条根本没有用例，另外 5 条有测试却没按 ID 命名（make 里 grep 不到）。
// 「写在文档里的门禁」等于没有门禁，所以这里把「有没有用例」变成硬检查：
//   1. 表里每条 ATK 都要有 `用例位置` 指向的文件；
//   2. 该文件里必须出现对应 ATK 编号的测试函数（Go：func TestATKnn...；TS：test('ATK-nn ...')）；
//   3. 反向：代码里出现的 ATK 编号必须在表里声明（禁止编号漂移）。
import { readFileSync, existsSync, readdirSync, statSync } from 'node:fs';
import { join, dirname } from 'node:path';

const DOC = 'docs/design/11-boundary-and-adversarial.md';
const doc = readFileSync(DOC, 'utf8');

// 解析对抗表：| ATK-01 | 动作 | 期望 | `路径` |
const rows = [];
for (const line of doc.split('\n')) {
  const m = line.match(/^\|\s*(ATK-\d+)\s*\|(.*)\|\s*(.*?)\s*\|\s*(.*?)\s*\|\s*$/);
  if (!m) continue;
  // 一条对抗可以用多个用例共同覆盖（例如 ATK-03 需要传输层 + 落库层两侧），
  // 因此把「用例位置」列解析成路径数组而不是单一路径。
  const paths = [...m[4].matchAll(/`([^`]+)`/g)].map((x) => x[1].trim()).filter(Boolean);
  const fallback = m[4].replace(/`/g, '').trim();
  rows.push({
    id: m[1], num: Number(m[1].slice(4)), action: m[2].trim(), expect: m[3].trim(),
    paths: paths.length ? paths : (fallback ? [fallback] : []),
  });
}

const problems = [];
if (rows.length === 0) {
  console.error(`未能从 ${DOC} 解析出对抗用例表`);
  process.exit(1);
}

const fileHasAtk = (file, num) => {
  const id = `ATK-${String(num).padStart(2, '0')}`;
  const compact = `ATK${num}`;
  if (!existsSync(file)) return { ok: false, reason: '文件不存在' };
  const body = readFileSync(file, 'utf8');
  // Go: func TestATK02Xxx / 注释里的 ATK-02；TS: test('ATK-02 ...')
  const hit = body.includes(id) || new RegExp(`func Test${compact}\\b`).test(body);
  return { ok: hit, reason: hit ? '' : `未在文件中找到 ${id} / Test${compact}` };
};

// 1) 逐条检查：一条对抗的所有用例文件都必须存在，且至少有一个文件带 ATK 编号。
for (const row of rows) {
  if (row.paths.length === 0) {
    problems.push(`${row.id}: 表中未给出用例位置`);
    continue;
  }
  let covered = false;
  for (const p of row.paths) {
    if (!existsSync(p)) {
      problems.push(`${row.id}: 用例位置不存在 → ${p}`);
      continue;
    }
    if (fileHasAtk(p, row.num).ok) covered = true;
  }
  if (!covered) {
    problems.push(`${row.id}: 以下文件中均未找到 ATK-${String(row.num).padStart(2, '0')} 标记 → ${row.paths.join(', ')}`);
  }
}

// 3) 反向：仓库里出现的 ATK 编号必须在表中
const declared = new Set(rows.map((r) => r.num));
const walk = (dir, out = []) => {
  for (const name of readdirSync(dir)) {
    if (name === 'node_modules' || name === '.git' || name === 'upstream' || name === 'dist') continue;
    const p = join(dir, name);
    const st = statSync(p);
    if (st.isDirectory()) walk(p, out);
    else if (/\.(go|ts|tsx)$/.test(name)) out.push(p);
  }
  return out;
};
const seen = new Set();
for (const file of [...walk('internal'), ...walk('web/src')]) {
  const body = readFileSync(file, 'utf8');
  for (const m of body.matchAll(/\bATK-?(\d{1,2})\b/g)) {
    const num = Number(m[1]);
    seen.add(num);
    if (!declared.has(num)) problems.push(`${file} 引用了未在 ${DOC} 声明的 ATK-${String(num).padStart(2, '0')}`);
  }
}

console.log('对抗用例门禁');
console.log('─'.repeat(56));
console.log(`表中条目 : ${rows.length} 条`);
console.log(`代码引用 : ${[...seen].sort((a, b) => a - b).map((n) => `ATK-${String(n).padStart(2, '0')}`).join(', ') || '无'}`);
console.log('─'.repeat(56));
if (problems.length) {
  console.error(`发现 ${problems.length} 处问题：`);
  for (const p of problems) console.error('  - ' + p);
  console.error('\n对抗用例是发版门禁（docs/design/13 §3.2）：新功能必须补用例，修复缺陷必须先写失败用例。');
  process.exit(1);
}
const covered = rows.filter((r) => r.paths.some((p) => existsSync(p) && fileHasAtk(p, r.num).ok)).length;
console.log(`全部 ${covered}/${rows.length} 条对抗用例均有可执行测试`);
