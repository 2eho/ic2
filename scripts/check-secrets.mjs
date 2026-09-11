#!/usr/bin/env node
// 仓库卫生检查（INV-5 / 12 §7 落地检查单）：
// 1) 业务代码不得出现上游项目源码或商业内容；
// 2) 不得提交真实密钥形态的字面量；
// 3) 不得在源码中留下调试残留（DBG-xxx）。
import { readdirSync, readFileSync, statSync } from 'node:fs';
import { join, extname, relative } from 'node:path';

const ROOT = '.';
const SKIP_DIRS = new Set(['.git', 'node_modules', 'dist', 'build', 'upstream', 'bin', 'data', 'coverage']);
const SRC_EXT = new Set(['.go', '.ts', '.tsx', '.js', '.jsx', '.mjs', '.cjs', '.css', '.html', '.yaml', '.yml', '.json', '.sh', '.sql', '.md']);
// 允许出现上游引用的位置（许可与致谢、设计文档）
const ALLOW_UPSTREAM = [/^LICENSE$/, /^NOTICE$/, /^README\.md$/, /^docs\//, /^scripts\//, /^\.cnb\.yml$/, /^Makefile$/];

const problems = [];

function walk(dir) {
  for (const name of readdirSync(dir)) {
    const p = join(dir, name);
    const st = statSync(p);
    if (st.isDirectory()) {
      if (SKIP_DIRS.has(name)) continue;
      walk(p);
      continue;
    }
    check(p);
  }
}

const SECRET_PATTERNS = [
  { re: /\bsk-[A-Za-z0-9]{20,}\b/, label: 'OpenAI 风格密钥' },
  { re: /\bAIza[0-9A-Za-z_\-]{30,}\b/, label: 'Google API Key' },
  { re: /-----BEGIN [A-Z ]*PRIVATE KEY-----/, label: '私钥' },
  { re: /gh[pousr]_[A-Za-z0-9]{30,}/, label: 'GitHub Token' },
];
const DEBUG_PATTERNS = [
  { re: /DBG-[A-Z0-9-]{3,}/, label: '调试残留标记' },
  { re: /SENTINEL/, label: '调试残留标记' },
  { re: /import_panic/, label: '调试辅助函数' },
];

function check(p) {
  const ext = extname(p);
  if (!SRC_EXT.has(ext) && !/^Makefile$/.test(p) && !/^NOTICE$/.test(p)) return;
  const rel = relative(ROOT, p);
  let body;
  try {
    body = readFileSync(p, 'utf8');
  } catch {
    return;
  }
  const isDocOrMeta = ALLOW_UPSTREAM.some((re) => re.test(rel));
  if (!isDocOrMeta && /basketikun|infinite-canvas/.test(body)) {
    problems.push(`${rel}: 业务代码中出现上游项目引用（应移除或移入文档/许可）`);
  }
  for (const s of SECRET_PATTERNS) {
    const m = body.match(s.re);
    if (m && !/_test\.go$/.test(rel) && !/check-secrets/.test(rel)) {
      problems.push(`${rel}: 疑似提交了${s.label} -> ${m[0].slice(0, 12)}...`);
    }
  }
  for (const s of DEBUG_PATTERNS) {
    // 测试文件中的对抗用例命名允许
    if (/_test\.go$/.test(rel) || /check-secrets/.test(rel)) continue;
    if (s.re.test(body)) {
      problems.push(`${rel}: 存在${s.label}`);
    }
  }
}

walk(ROOT);

console.log('仓库卫生与凭据检查');
console.log('─'.repeat(52));
if (problems.length) {
  console.error(`发现 ${problems.length} 个问题：`);
  for (const p of problems) console.error('  - ' + p);
  process.exit(1);
}
console.log('通过：无上游源码、无明文密钥、无调试残留');
