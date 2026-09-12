#!/usr/bin/env node
// 非锚定的 .gitignore 规则会**静默吞掉发布物**。
//
// 这个脚本存在的理由是一个**重复发生**的缺陷：
//
//   第 1 次：`upstream/`（非锚定）匹配了 `docs/upstream/`，
//            于是上游同步记录写完永远不会被提交；
//   第 2 次：`bin/`（非锚定）匹配了 `packages/plugin-sdk/bin/`，
//            于是插件 SDK 少了构建脚本——用户装了包却无法构建。
//
// 两次的共同点：**没有任何报错**。文件在本地存在、本地门禁绿、
// `git status` 干净（被忽略的文件不算未跟踪），而发布物里没有它。
//
// 判据：目录形态的忽略规则（`xxx/`）若非以 `/` 开头，就会匹配任意层级的
// 同名目录。只有「确实想忽略任意层级」时才允许，且必须显式写在白名单里
//（白名单要求写理由，否则下次又变成「随手一行」）。
import { readFileSync, existsSync } from 'node:fs';
import { spawnSync } from 'node:child_process';

const PATH_SPEC = '.gitignore';

/** 允许「有意忽略任意层级」的规则（必须写理由）。 */
const ALLOWED_UNANCHORED = new Map([
  ['node_modules/', '依赖目录：任何层级的 node_modules 都不入库（含 packages/*/）'],
  ['.DS_Store', 'macOS 元数据：任何层级都可能出现'],
  ['*.log', '日志：任何层级都可能出现，且都有害'],
]);

if (!existsSync(PATH_SPEC)) {
  console.error(`缺少 ${PATH_SPEC}`);
  process.exit(1);
}

const problems = [];
const lines = readFileSync(PATH_SPEC, 'utf8').split('\n');
for (const [i, raw] of lines.entries()) {
  const line = raw.trim();
  if (!line || line.startsWith('#')) continue;
  // 取反规则（!）不吞东西，跳过
  if (line.startsWith('!')) continue;
  // 锚定规则安全
  if (line.startsWith('/')) continue;
  // 只关心「目录形态」与「裸名字」：它们最容易匹配到子目录里的同名目录。
  // `*.ext` 这类扩展名规则影响面窄（且通常是有意的），不在此列。
  const isDirRule = line.endsWith('/');
  const isPlainName = /^[A-Za-z0-9._-]+$/.test(line);
  if (!isDirRule && !isPlainName) continue;
  if (ALLOWED_UNANCHORED.has(line)) continue;

  // 真正验证「有没有被误伤的东西」，而不是只看规则形状——
  // 形状不对但恰好没有同名目录时不该告警（噪音告警会被忽略）。
  //
  // 判据必须问 git 的**忽略判定**，而不是列已跟踪文件：
  // 被忽略的文件既不在 `git ls-files --cached` 里，也不在
  // `--others --exclude-standard` 里（后者会应用忽略规则把它们滤掉）。
  // 早期版本用后者探测，于是「文件确实被吞了」这一情形反而测不出来
  //（实测：把规则改回 `bin/` 仍然报绿）。
  const hits = [];
  const dir = isDirRule ? line.slice(0, -1) : line;
  // 仓库里所有同名目录（任何层级）都去问一次 git 是否被忽略。
  const walk = spawnSync('git', ['ls-files', '--cached', '-z'], { encoding: 'utf8' });
  const known = (walk.stdout || '').split('\0').filter(Boolean);
  const candidates = new Set();
  for (const f of known) {
    const parts = f.split('/');
    for (let k = 0; k < parts.length - 1; k++) {
      if (parts[k] === dir) candidates.add(parts.slice(0, k).join('/'));
    }
  }
  // 还要覆盖「尚未入库但存在于工作区」的候选：这正是一次新缺陷的形态
  //（SDK 的 bin/ 目录刚建出来、还没被 add，就被规则静默吞掉）。
  try {
    const { readdirSync, statSync } = await import('node:fs');
    const scan = (base, depth) => {
      if (depth > 4) return;
      let entries = [];
      try {
        entries = readdirSync(base, { withFileTypes: true });
      } catch {
        return;
      }
      for (const e of entries) {
        if (!e.isDirectory()) continue;
        if (['.git', 'node_modules', 'upstream', 'dist', 'bin', 'build'].includes(e.name) && e.name !== dir) {
          continue;
        }
        const rel = base === '.' ? e.name : `${base}/${e.name}`;
        if (e.name === dir) candidates.add(base === '.' ? '' : base);
        scan(rel, depth + 1);
      }
    };
    scan('.', 0);
  } catch {
    /* 目录不可读时跳过 */
  }
  for (const cand of candidates) {
    const probePath = (cand ? cand + '/' : '') + dir + '/.gitignore-probe';
    const r = spawnSync('git', ['check-ignore', '-q', probePath], { encoding: 'utf8' });
    if (r.status === 0) {
      // 已经被跟踪的文件不算「被吞」——规则改回来之后它们仍在库里。
      const trackedInDir = known.some((f) => f.startsWith((cand ? cand + '/' : '') + dir + '/'));
      hits.push(`${probePath}${trackedInDir ? '（该目录已有文件入库，风险更高）' : ''}`);
    }
  }
  if (hits.length > 0) {
    problems.push(
      `.gitignore:${i + 1} 的 \`${line}\` 是非锚定规则，正在忽略仓库内的同名目录：` +
        `${hits.slice(0, 3).join('; ')}${hits.length > 3 ? ` 等 ${hits.length} 处` : ''}。` +
        `请改为 \`/${line}\`（锚定到仓库根），或加入 ALLOWED_UNANCHORED 并写明理由。`,
    );
  }
}

console.log('.gitignore 锚定检查');
console.log('─'.repeat(56));
if (problems.length) {
  console.error(`发现 ${problems.length} 处问题：`);
  for (const p of problems) console.error('  - ' + p);
  process.exit(1);
}
console.log('无「非锚定规则吞掉发布物」的风险');
