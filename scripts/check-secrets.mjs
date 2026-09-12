#!/usr/bin/env node
// 仓库卫生检查（INV-5 / 12 §7 落地检查单）：
// 1) 业务代码不得出现上游项目源码或商业内容；
// 2) 不得提交真实密钥形态的字面量；
// 3) 不得在源码中留下调试残留（DBG-xxx）。
import { readdirSync, readFileSync, statSync } from 'node:fs';
import { join, extname, relative } from 'node:path';

const ROOT = '.';
// research/ 与 upstream/ 都是「不入库的上游分析镜像」（见 .gitignore）：
// 把它们纳入检查会产生大量假阳性（上游代码本身就含上游标识），
// 而它们的存在意义恰恰是「读原实现，不引用原代码」。
const SKIP_DIRS = new Set(['.git', 'node_modules', 'dist', 'build', 'upstream', 'research', 'bin', 'data', 'coverage', 'playwright-report', 'test-results']);
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
  { re: /\bsk-proj-[A-Za-z0-9_-]{20,}\b/, label: 'OpenAI 项目密钥' },
  { re: /\bAIza[0-9A-Za-z_\-]{30,}\b/, label: 'Google API Key' },
  { re: /-----BEGIN [A-Z ]*PRIVATE KEY-----/, label: '私钥' },
  { re: /gh[pousr]_[A-Za-z0-9]{30,}/, label: 'GitHub Token' },
  { re: /github_pat_[A-Za-z0-9_]{30,}/, label: 'GitHub Fine-grained PAT' },
  { re: /\bxox[baprs]-[A-Za-z0-9-]{10,}/, label: 'Slack Token' },
  { re: /\bAKIA[0-9A-Z]{16}\b/, label: 'AWS Access Key ID' },
  { re: /\bASIA[0-9A-Z]{16}\b/, label: 'AWS 临时访问密钥' },
  { re: /\bglpat-[A-Za-z0-9_-]{20,}/, label: 'GitLab PAT' },
  { re: /\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}/, label: 'JWT（可能含会话令牌）' },
  { re: /\bBearer\s+[A-Za-z0-9_-]{30,}/, label: '硬编码 Bearer 令牌' },
  { re: /\b(?:api[_-]?key|secret|passwd|password|token)\s*[:=]\s*["'][A-Za-z0-9_\-+/]{24,}["']/i, label: '硬编码凭据字面量' },
];

/**
 * 高熵字符串探测：抓「没有已知前缀但仍然像密钥」的字面量。
 *
 * 为什么需要它：前缀型规则只能抓已知厂商。本项目的凭据是**用户自带**的
 * （任意中转站、任意网关），形态不受任何规则约束，因此必须用熵兜底。
 * 判据刻意保守（长度 ≥ 32、字符集 ≥ 3 类、熵 > 4.0 bits/char）：
 * 假阳性会让人习惯性忽略门禁，那比漏报更危险。
 */
const ENTROPY_MIN_LEN = 32;
const ENTROPY_MIN_BITS = 4.0;

function shannonBits(s) {
  const freq = new Map();
  for (const ch of s) freq.set(ch, (freq.get(ch) ?? 0) + 1);
  let bits = 0;
  for (const n of freq.values()) {
    const p = n / s.length;
    bits -= p * Math.log2(p);
  }
  return bits;
}

function looksHighEntropy(token) {
  if (token.length < ENTROPY_MIN_LEN) return false;
  // 必须混有大小写+数字（纯十六进制 sha256 也会命中，因此额外要求 3 类字符）
  const classes = [/[a-z]/, /[A-Z]/, /[0-9]/, /[+/_=-]/].filter((re) => re.test(token)).length;
  if (classes < 3) return false;
  // 排除明显的非密钥：
  if (/^[A-Za-z]+$/.test(token)) return false;
  if (/^\d+$/.test(token)) return false;
  if (/^(?:sha256|sha512|md5)[:_-]/i.test(token)) return false;
  // 文件路径/模块名/标识符：含 `/`、`.`、`@` 分隔的命名空间
  // （实测假阳性：`features/workbench/someModule` 这类字符串熵也超过阈值）
  if (/[/@]/.test(token) && !/=/.test(token)) return false;
  if (/^[a-z-]+(?:\.[a-z-]+)+$/i.test(token)) return false;
  // 形如 xx-yyyy-zzzz-... 的 CSS 类名/工具标识（多段短横线且无数字混排）
  if (/^[a-z]+(?:-[a-z]+){2,}$/.test(token)) return false;
  // 仅由可打印英文单词与短横线组成的长串（如 `agent-skills` 命名空间）
  if (/^[a-z0-9]+(?:-[a-z0-9]+){2,}$/i.test(token) && !/[A-Z]/.test(token)) return false;
  return shannonBits(token) > ENTROPY_MIN_BITS;
}
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
    // 「上游产品标识」与「上游产品标识作为**兼容性常量**」是两件事。
    // 前者是搬运（必须拒绝），后者是我们主动写下的互操作契约——
    // 例如资产包导入器要识别 `app: "infinite-canvas"` 才能读老用户的包。
    // 这类出现必须带明确说明，因此要求同一行/邻近行出现 legacy/兼容/import 等词。
    const isCompatReference =
      /(legacy|compat|兼容|上游|import|immigrat)/i.test(
        body.split('\n').filter((l) => /basketikun|infinite-canvas/.test(l)).join(' '),
      );
    if (!isCompatReference) {
      problems.push(`${rel}: 业务代码中出现上游项目引用（应移除或移入文档/许可）`);
    }
  }
  const isTestOrSelf = /_test\.go$/.test(rel) || /check-secrets/.test(rel) || /\.test\.tsx?$/.test(rel);
  for (const s of SECRET_PATTERNS) {
    const m = body.match(s.re);
    if (m && !isTestOrSelf) {
      problems.push(`${rel}: 疑似提交了${s.label} -> ${m[0].slice(0, 12)}...`);
    }
  }

  // 高熵兜底：只扫「看起来是赋值/字面量」的片段，避免把散文/注释里的长单词误判。
  if (!isTestOrSelf) {
    for (const m of body.matchAll(/["'`]([A-Za-z0-9+/_=-]{32,})["'`]/g)) {
      const tok = m[1];
      if (!looksHighEntropy(tok)) continue;
      // 常见假阳性白名单：哈希示例、测试夹具、SVG/PNG 的 base64 头、伪 UUID
      if (/^(?:iVBORw0KGgo|data:image|PHN2ZyB|JVBERi0)/.test(tok)) continue;
      if (/^[0-9a-f]{40,}$/i.test(tok)) continue; // 纯 hex（内容寻址 hash）
      problems.push(
        `${rel}: 疑似高熵字面量（可能是密钥）-> ${tok.slice(0, 10)}...（熵 ${shannonBits(tok).toFixed(2)} bits/char）`,
      );
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

// ---------------------------------------------------------------- 5) 运行期泄密面（INV-5）
//
// 静态字符串扫描抓不到「凭据在运行时被写进日志/响应」这类问题。
// 这里对**代码形态**做检查：显式把凭据值拼进日志或 JSON 响应的地方。
// 判据用「同一表达式里同时出现凭据读取与日志/序列化」，避免把正常代码误判。
const LEAK_SURFACES = [
  {
    // Go: log/slog/print 系列里直接带上 secret 变量的值
    re: /(?:slog\.|logger\.|log\.|fmt\.Print)[A-Za-z]*\([^)]*\b(?:secret|apiKey|api_key|credential|token|password|privateKey)\b[^)]*\)/gi,
    label: 'Go 日志中直接输出凭据变量',
  },
  {
    // Go/TS: 响应体里直接塞凭据字段（未脱敏）
    re: /(?:writeJSON|json\.NewEncoder|res\.json|Response\.json)[^\n]{0,80}["']?(?:secret|apiKey|rawSecret)["']?\s*:/gi,
    label: '响应体中直接返回凭据字段',
  },
];
const LEAK_ALLOW = [/database\/sql/, /\/\/\s*INV-5/];
for (const rel of ['internal', 'web/src', 'cmd']) {
  const walkLeak = (dir) => {
    let names = [];
    try {
      names = readdirSync(dir);
    } catch {
      return;
    }
    for (const n of names) {
      const p = join(dir, n);
      const st = statSync(p);
      if (st.isDirectory()) {
        if (SKIP_DIRS.has(n)) continue;
        walkLeak(p);
        continue;
      }
      if (!/\.(go|ts|tsx)$/.test(p)) continue;
      if (/_test\.go$|\.test\.tsx?$/.test(p)) continue;
      const body = readFileSync(p, 'utf8');
      for (const surface of LEAK_SURFACES) {
        const m = body.match(surface.re);
        if (m) {
          problems.push(`${p}: ${surface.label} -> ${m[0].slice(0, 60)}...`);
        }
      }
      for (const allow of LEAK_ALLOW) {
        if (allow.test(body)) {
          /* 显式允许的形态由注释/包名表达，这里只做记录不处理 */
        }
      }
    }
  };
  walkLeak(rel);
}

console.log('仓库卫生与凭据检查');
console.log('─'.repeat(52));
if (problems.length) {
  console.error(`发现 ${problems.length} 个问题：`);
  for (const p of problems) console.error('  - ' + p);
  process.exit(1);
}
console.log('通过：无上游源码、无明文密钥、无调试残留');
