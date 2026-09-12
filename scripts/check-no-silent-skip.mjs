#!/usr/bin/env node
// 「假绿通道」门禁：不允许让「没跑」伪装成「通过」。
//
// 起因（本轮实测到的四个真实缺陷，全部属于同一类）：
//   1. e2e 在拿不到后端时 `test.skip` → CI 里显示「通过」，实测 9 条用例全部空转；
//   2. e2e 的 ATK-18 因为 import 一个不存在的模块而永远 skip；
//   3. e2e 的 ATK-19 import 一个仓库里不存在的文件（svg-sanitize.ts）而永远 skip；
//   4. Makefile 里 `cmd || echo 跳过` 把工具缺失当成检查通过。
//
// 共同点：**失败被降级成了跳过**。对回归门禁而言，跳过 == 不存在。
// 这个脚本把「静默跳过」变成硬检查：
//   a. e2e（Playwright）里禁止无条件 skip —— 除非在同一行/上一行显式标注
//      `ALLOW-CONDITIONAL-SKIP:` 并给出理由（必须能说清"什么条件下才该跳"）；
//   b. Makefile 里禁止 `|| true` / `|| echo` 出现在门禁目标中（preflight 除外）；
//   c. 禁止 `command -v X >/dev/null || true` 这类「查了但不管结果」的写法。
import { readFileSync, existsSync, readdirSync, statSync } from 'node:fs';
import { join, relative } from 'node:path';

const problems = [];
const ROOT = process.cwd();

function walk(dir, filter, out = []) {
  if (!existsSync(dir)) return out;
  for (const name of readdirSync(dir)) {
    if (name === 'node_modules' || name === 'dist' || name.startsWith('.')) continue;
    const p = join(dir, name);
    const st = statSync(p);
    if (st.isDirectory()) walk(p, filter, out);
    else if (filter(p)) out.push(p);
  }
  return out;
}

// ---------------------------------------------------------------- 1) e2e 静默跳过
const ALLOW_MARK = 'ALLOW-CONDITIONAL-SKIP:';
const specs = walk('web/e2e', (p) => p.endsWith('.spec.ts'));
let skipCount = 0;
for (const file of specs) {
  const lines = readFileSync(file, 'utf8').split('\n');
  lines.forEach((line, i) => {
    if (!/\.skip\s*\(/.test(line)) return;
    skipCount += 1;
    // 允许：注释里带标记（本行或往上 4 行内），且理由不是空话
    const context = lines.slice(Math.max(0, i - 4), i + 1).join('\n');
    if (context.includes(ALLOW_MARK)) {
      const reason = context.split(ALLOW_MARK)[1]?.split('\n')[0]?.trim() ?? '';
      if (reason.length < 12) {
        problems.push(
          `${relative(ROOT, file)}:${i + 1} 有条件跳过但理由过短（必须说明何时可以跳）`,
        );
      }
      return;
    }
    problems.push(
      `${relative(ROOT, file)}:${i + 1} 无条件 skip：会让「没跑」伪装成「通过」` +
        `（如确需条件跳过，加注释 \`${ALLOW_MARK} <理由>\`）`,
    );
  });
}

// ---------------------------------------------------------------- 2) Makefile 静默兜底
const mk = readFileSync('Makefile', 'utf8');
const mkLines = mk.split('\n');
// 这些目标本身就是「允许失败」的（诊断/可选工具），显式放行
const ALLOW_FAIL_TARGETS = new Set(['vet-extra', 'web-trigger']);
let currentTarget = '';
mkLines.forEach((line, i) => {
  const m = line.match(/^([a-zA-Z][\w-]*):/);
  if (m) currentTarget = m[1];
  if (!/^\t/.test(line)) return;
  if (ALLOW_FAIL_TARGETS.has(currentTarget)) return;
  const trimmed = line.trim();
  // 例外：`trap ... || true` 是**清理**（EXIT 钩子里不能让清理失败覆盖真实退出码），
  // 它不参与门禁判定，因此不算兜底。除此之外一律禁止。
  const isCleanupTrap = /^trap\b/.test(trimmed) || /trap\s+'.*\|\|\s*true'/.test(trimmed);
  if (isCleanupTrap) return;
  if (/\|\|\s*true\b/.test(trimmed)) {
    problems.push(`Makefile:${i + 1}（目标 ${currentTarget}）用 \`|| true\` 兜底：把失败变绿`);
  }
  if (/\|\|\s*echo\s/.test(trimmed) && !/exit\s+1/.test(trimmed) && !/>&2/.test(trimmed)) {
    problems.push(
      `Makefile:${i + 1}（目标 ${currentTarget}）用 \`|| echo\` 吞掉失败：` +
        `工具缺失会被当成检查通过`,
    );
  }
});

// ---------------------------------------------------------------- 3) CI 配置不得兜底真门禁
if (existsSync('.cnb.yml')) {
  const ci = readFileSync('.cnb.yml', 'utf8');
  const ciLines = ci.split('\n');
  ciLines.forEach((line, i) => {
    if (!/\|\|\s*true\b/.test(line)) return;
    // 注释行本身不算「兜底」（文件顶部就写着「不允许 || true 兜住真门禁」这句纪律）。
    if (/^\s*#/.test(line)) return;
    // 允许的例外必须**逐条列白名单**，不能用「注释里出现了上游二字」这种模糊匹配：
    // 实测过，宽松匹配会让 `make parity || true` 因为附近恰好有「上游巡检」的注释
    // 而被放行——门禁自己被绕过，比没有门禁更危险。
    const ALLOWED_EXACT = [
      'bash scripts/upstream-sync.sh || true', // 上游巡检只产出报告，不阻断
    ];
    // 单行 script 形式：`script: bash ...`（本轮起 YAML 里门禁/巡检都写成单行）
    const code = line.trim().replace(/^script:\s*/, '');
    if (ALLOWED_EXACT.includes(code)) return;
    problems.push(
      `.cnb.yml:${i + 1} 门禁步骤用 \`|| true\` 兜底：把红变绿（如确需允许失败，` +
        `加入本文脚本的 ALLOWED_EXACT 白名单并写清理由）`,
    );
  });
}

// ---------------------------------------------------------------- 4) 测试文件不得自我跳过核心断言
// 允许的条件跳过必须在测试体内可检索到「为什么跳过」的可观测依据
const vitestFiles = walk('web/src', (p) => p.endsWith('.test.ts') || p.endsWith('.test.tsx'));
for (const file of vitestFiles) {
  const body = readFileSync(file, 'utf8');
  if (/\.skip\s*\(|\.todo\s*\(/.test(body) && !body.includes(ALLOW_MARK)) {
    problems.push(
      `${relative(ROOT, file)} 含 skip/todo 且未标注 \`${ALLOW_MARK}\`（单测被跳过等于没有覆盖）`,
    );
  }
}

console.log('假绿通道检查');
console.log('─'.repeat(56));
console.log(`e2e 用例文件 : ${specs.length} 个（skip 出现 ${skipCount} 处）`);
console.log(`前端单测     : ${vitestFiles.length} 个`);
console.log('─'.repeat(56));
if (problems.length) {
  console.error(`发现 ${problems.length} 处问题：`);
  for (const p of problems) console.error('  - ' + p);
  process.exit(1);
}
console.log('未发现静默跳过或静默兜底');
