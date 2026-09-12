#!/usr/bin/env node
// 契约生成与一致性校验（见 docs/design/08-infra.md §6「契约先行」）。
//
// contracts/ 是 API 契约唯一真源：
//   - openapi.yaml  路由与 DTO 形状
//   - errors.yaml   稳定错误码 ↔ i18n key
//   - events.yaml   SSE 事件类型
//   - ops.schema.json 画布 op 的 JSON Schema
//
// 生成物：
//   - internal/contract/errors.gen.go     错误码常量（Go）
//   - web/src/shared/api/contract.gen.ts  错误码与事件类型（TS）
//
// 生成后必须已提交；CI 重跑本脚本后 git diff 必须为空（避免「代码上了契约没改」）。
// 用法：node scripts/gen-contracts.mjs [--check]
import { readFileSync, writeFileSync, existsSync, mkdirSync } from 'node:fs';
import { dirname, resolve as resolvePath } from 'node:path';
import { fileURLToPath } from 'node:url';

// 工具与契约一律相对**脚本所在目录**解析，而不是 process.cwd()。
// 起因（PR #2 门禁飘红）：`make gen-check` 里 `cd web` 与 `node scripts/…`
// 一旦写成把两者接起来的 form（例如 `cd web && node ../scripts/gen-contracts.mjs`），
// 相对 cwd 的 contracts/ 、internal/ 全部 ENOENT，报错却指向「文件缺失」。
// 锚定脚本目录后，无论从哪个 cwd 调用，读到的都是同一份契约、跑的都是同一套规则。
const ROOT = resolvePath(dirname(fileURLToPath(import.meta.url)), '..');
const at = (p) => resolvePath(ROOT, p);

const CHECK = process.argv.includes('--check');
const problems = [];
const written = [];

// ---------------------------------------------------------------- 极简 YAML 读取
// 只支持本仓库用到的子集：缩进映射、`- ` 序列、`key: value`、内联数组、引号与注释。
// 刻意不引第三方依赖：契约文件格式由我们控制，读不了就报错，不静默兜底。
function parseYAML(text) {
  const lines = [];
  const rawLines = text.split('\n');
  for (let i = 0; i < rawLines.length; i++) {
    const raw = rawLines[i];
    if (!raw.trim() || raw.trim().startsWith('#')) continue;
    const indent = raw.match(/^ */)[0].length;
    let body = raw.trim().replace(/\s+#(?![^"']*["']).*$/, '');
    // 块标量（`key: |` 或 `key: >`）：其后的缩进内容全部属于该值。
    // 不支持块标量会让「多行 description」被当成新键，
    // 进而把 paths 下的结构解析乱（实测会把 44 条路由解析成 32 条）。
    if (/^[\w"'-]+:\s*[|>][-+]?\s*$/.test(body)) {
      const blockIndent = (rawLines[i + 1]?.match(/^ */) ?? [''])[0].length;
      const chunk = [];
      while (i + 1 < rawLines.length) {
        const next = rawLines[i + 1];
        if (!next.trim()) {
          chunk.push('');
          i++;
          continue;
        }
        const nextIndent = next.match(/^ */)[0].length;
        if (nextIndent <= indent) break;
        chunk.push(next.slice(Math.min(blockIndent, nextIndent)));
        i++;
      }
      body = body.replace(/[|>][-+]?\s*$/, JSON.stringify(chunk.join('\n')));
    }
    lines.push({ indent, body });
  }
  let i = 0;
  const parseBlock = (indent) => {
    if (i >= lines.length) return null;
    if (lines[i].body.startsWith('- ')) {
      const out = [];
      while (i < lines.length && lines[i].indent === indent && lines[i].body.startsWith('- ')) {
        const inline = lines[i].body.slice(2).trim();
        i++;
        if (inline.includes(':')) {
          const obj = {};
          const [k, ...rest] = splitKeyValue(inline);
          const v = rest.join(':').trim();
          if (v) obj[k] = scalar(v);
          else {
            const child = parseBlock(nextIndent(indent));
            if (child !== null) obj[k] = child;
          }
          // 同一 list item 的后续行
          while (i < lines.length && lines[i].indent > indent && !lines[i].body.startsWith('- ')) {
            const ci = lines[i].indent;
            if (lines[i].body.includes(':')) {
              const [ck, ...cr] = splitKeyValue(lines[i].body);
              const cv = cr.join(':').trim();
              i++;
              if (cv) obj[ck] = scalar(cv);
              else {
                const child = parseBlock(nextIndent(ci));
                if (child !== null) obj[ck] = child;
              }
            } else i++;
          }
          out.push(obj);
        } else {
          out.push(scalar(inline));
        }
      }
      return out;
    }
    const obj = {};
    while (i < lines.length && lines[i].indent === indent && !lines[i].body.startsWith('- ')) {
      const [k, ...rest] = splitKeyValue(lines[i].body);
      const v = rest.join(':').trim();
      i++;
      if (v) obj[k] = scalar(v);
      else {
        const child = parseBlock(nextIndent(indent));
        obj[k] = child === null ? {} : child;
      }
    }
    return obj;
  };
  const nextIndent = (cur) => {
    for (let j = i; j < lines.length; j++) if (lines[j].indent > cur) return lines[j].indent;
    return cur + 2;
  };
  return parseBlock(0);
}
function splitKeyValue(s) {
  const idx = s.indexOf(':');
  if (idx < 0) return [s.trim(), ''];
  return [s.slice(0, idx).trim(), s.slice(idx + 1)];
}
function scalar(v) {
  v = v.trim();
  if (v.startsWith('[') && v.endsWith(']')) {
    const inner = v.slice(1, -1).trim();
    return inner ? inner.split(',').map((x) => scalar(x)) : [];
  }
  if ((v.startsWith('"') && v.endsWith('"')) || (v.startsWith("'") && v.endsWith("'"))) return v.slice(1, -1);
  if (v === 'true') return true;
  if (v === 'false') return false;
  if (/^-?\d+$/.test(v)) return Number(v);
  return v;
}

// ---------------------------------------------------------------- 读取契约
const readRequired = (path) => {
  if (!existsSync(path)) {
    problems.push(`缺少契约文件 ${path}`);
    return null;
  }
  return readFileSync(path, 'utf8');
};

const openapiRaw = readRequired(at('contracts/openapi.yaml'));
const errorsRaw = readRequired(at('contracts/errors.yaml'));
const eventsRaw = readRequired(at('contracts/events.yaml'));
const opsRaw = readRequired(at('contracts/ops.schema.json'));

const openapi = openapiRaw ? parseYAML(openapiRaw) : {};
const errors = errorsRaw ? parseYAML(errorsRaw) : {};
const events = eventsRaw ? parseYAML(eventsRaw) : {};
const opsSchema = opsRaw ? JSON.parse(opsRaw) : {};

// 1) 错误码：contracts/errors.yaml 是唯一真源，必须与 platform/errors.go、i18n 双向一致。
const contractCodes = new Set(
  (errors?.codes ?? []).map((c) => (typeof c === 'string' ? c : c?.code)).filter(Boolean),
);
const errorsGo = readFileSync(at('internal/platform/errors.go'), 'utf8');
const goCodes = new Map();
for (const m of errorsGo.matchAll(/^\s*(Code\w+)\s*=\s*"([a-z_]+)"\s*$/gm)) goCodes.set(m[2], m[1]);

for (const code of contractCodes) {
  if (!goCodes.has(code)) problems.push(`contracts/errors.yaml 声明了 ${code}，但 internal/platform/errors.go 没有该常量`);
}
for (const code of goCodes.keys()) {
  if (!contractCodes.has(code)) problems.push(`internal/platform/errors.go 有 ${code}，但 contracts/errors.yaml 未声明`);
}

// i18n：zh-CN 与 en-US 都必须有 errors.<code> 文案
const zhSrc = readFileSync(at('web/src/shared/i18n/zh-CN.ts'), 'utf8');
const enSrc = readFileSync(at('web/src/shared/i18n/en-US.ts'), 'utf8');
const errorsBlock = (src) => {
  const start = src.indexOf('errors: {');
  if (start < 0) return '';
  const end = src.indexOf('\n  },', start);
  return src.slice(start, end < 0 ? src.length : end);
};
const zhErrors = errorsBlock(zhSrc);
const enErrors = errorsBlock(enSrc);
for (const code of goCodes.keys()) {
  if (!new RegExp(`^\\s{4}${code}:`, 'm').test(zhErrors)) problems.push(`zh-CN 缺少文案 errors.${code}`);
  if (!new RegExp(`^\\s{4}${code}:`, 'm').test(enErrors)) problems.push(`en-US 缺少文案 errors.${code}`);
}

// 2) 事件类型：contracts/events.yaml ↔ internal/api 的 RunEvent.Type / graph Event.Type
const contractEvents = new Set((events?.events ?? []).map((e) => (typeof e === 'string' ? e : e?.type)).filter(Boolean));
const graphSrc = readFileSync(at('internal/graph/doc.go'), 'utf8') + readFileSync(at('internal/graph/service.go'), 'utf8');
const goEventConsts = new Set();
for (const m of graphSrc.matchAll(/EventType\w*\s*=\s*"([a-z_.]+)"/g)) goEventConsts.add(m[1]);
const apiRunEventSrc = readFileSync(at('internal/exec/engine.go'), 'utf8');
for (const m of apiRunEventSrc.matchAll(/"([a-z_]+)"\s*,\s*\/\/\s*event/g)) goEventConsts.add(m[1]);

// 事件类型只在契约里声明、代码里用；此处只做「契约里声明的必须非空且唯一」的卫生检查
if (contractEvents.size === 0) problems.push('contracts/events.yaml 未声明任何事件类型');
if (new Set(contractEvents).size !== (events?.events ?? []).length) problems.push('contracts/events.yaml 存在重复事件类型');

// 3) 路由：openapi.yaml 的 paths ↔ internal/api/router.go 的注册路径
const routerSrc = readFileSync(at('internal/api/router.go'), 'utf8');
const registered = new Set();
for (const m of routerSrc.matchAll(/mux\.HandleFunc\("(GET|POST|PUT|PATCH|DELETE) (\/[^"]+)"/g)) {
  const path = m[2].startsWith('/api/v1/') ? m[2].slice('/api/v1'.length) : m[2];
  registered.add(`${m[1]} ${path}`);
}
const declared = new Set();
for (const p of Object.keys(openapi?.paths ?? {})) {
  for (const method of Object.keys(openapi.paths[p] ?? {})) {
    declared.add(`${method.toUpperCase()} ${p}`);
  }
}
for (const r of registered) {
  if (!declared.has(r)) problems.push(`router.go 注册了 ${r}，但 contracts/openapi.yaml 未声明`);
}
for (const d of declared) {
  if (!registered.has(d)) problems.push(`contracts/openapi.yaml 声明了 ${d}，但 router.go 未注册`);
}

// 4) op schema：contracts/ops.schema.json ↔ internal/graph 的 op kind 常量
const opKinds = new Set();
const opSrc = readFileSync(at('internal/graph/op.go'), 'utf8');
for (const m of opSrc.matchAll(/^\s*Op\w+\s+OpKind\s*=\s*"([a-z_.]+)"/gm)) opKinds.add(m[1]);
const schemaKinds = new Set(opsSchema?.properties?.kind?.enum ?? []);
for (const k of opKinds) if (!schemaKinds.has(k)) problems.push(`op kind ${k} 未出现在 contracts/ops.schema.json`);
for (const k of schemaKinds) if (!opKinds.has(k)) problems.push(`ops.schema.json 声明了未知 op kind ${k}`);

// ---------------------------------------------------------------- 产出代码
const genGo = `// Code generated by scripts/gen-contracts.mjs from contracts/errors.yaml. DO NOT EDIT.
// 契约改动请改 contracts/，然后执行 make gen。

package contract

// ErrorCode 是稳定的对外错误码。
type ErrorCode = string

// 全部稳定错误码（与 internal/platform/errors.go 双向校验）。
const (
${[...goCodes.entries()]
  .sort()
  .map(([code, name]) => `\t${name} ErrorCode = "${code}"`)
  .join('\n')}
)

// AllErrorCodes 返回全部错误码，供测试穷举。
var AllErrorCodes = []ErrorCode{
${[...goCodes.keys()].sort().map((c) => `\t"${c}",`).join('\n')}
}

// I18nKeys 返回错误码对应的 i18n key（前端 errors.<code>）。
func I18nKey(code ErrorCode) string { return "errors." + code }
`;

const genTS = `// Code generated by scripts/gen-contracts.mjs from contracts/errors.yaml + events.yaml. DO NOT EDIT.
// 契约改动请改 contracts/，然后执行 make gen。

/** 稳定的对外错误码（与 Go 侧 internal/platform/errors.go 双向校验）。 */
export type ErrorCode =
${[...goCodes.keys()].sort().map((c) => `  | '${c}'`).join('\n')};

/** 全部错误码，供测试穷举。 */
export const ERROR_CODES = [
${[...goCodes.keys()].sort().map((c) => `  '${c}',`).join('\n')}
] as const;

/** 错误码 → i18n key。 */
export function errorI18nKey(code: string): string {
  return ERROR_CODES.includes(code as ErrorCode) ? \`errors.\${code}\` : 'errors.internal';
}

/** 画布实时事件类型（contracts/events.yaml）。 */
export type CanvasEventType =
${[...contractEvents].sort().map((c) => `  | '${c}'`).join('\n')};

/** 全部画布事件类型。 */
export const CANVAS_EVENT_TYPES = [
${[...contractEvents].sort().map((c) => `  '${c}',`).join('\n')}
] as const;
`;

// 生成物立即用 gofmt / prettier 规整：否则 `make gen` 之后 `gofmt -l` 仍然报红，
// 「生成即可提交」这条纪律就断了。
const outputs = [
  ['internal/contract/errors.gen.go', genGo, 'go'],
  ['web/src/shared/api/contract.gen.ts', genTS, 'ts'],
];

const { spawnSync } = await import('node:child_process');
const { mkdtempSync } = await import('node:fs');
const { tmpdir } = await import('node:os');
const { join, resolve } = await import('node:path');

// 关键：格式化工具必须用**绝对路径**解析。
// 只写 `gofmt` 时 Node 不会查 PATH：它先按无扩展名文件查找，找不到再补 .exe，
// 全是相对当前目录的路径，因此 `gofmt` 会直接 ENOENT（Linux/macOS 同理）。
// 后果不是「报错」，而是**静默降级**：格式化失败 → 返回未格式化内容 → 与仓库中
// 已格式化的生成物不等 → gen-check 永远红；而写回模式下会把未格式化内容覆盖上去，
// 把工作区越改越脏（下一节正是这个问题的实测复现）。
// 解析顺序刻意是「显式候选路径 → PATH」而不是反过来：
// prettier 装在 web/node_modules/.bin，CI 的 gen-check 步骤不会把该目录加进 PATH，
// 只查 PATH 会让这条门禁在 CI 上永远报「找不到 prettier」而本地正常——
// 也就是「本地绿、CI 红」的经典形态。显式候选路径让两条环境一致。
const resolveTool = (name, extraDirs = []) => {
  for (const dir of extraDirs) {
    // 必须转成绝对路径：spawnSync 会先按 `cwd` 切换目录再执行，
    // 相对路径的候选（web/node_modules/.bin/prettier）在 cwd=web 下会变成
    // web/web/node_modules/... 而 ENOENT——报错信息是「执行失败（退出码 null）」，
    // 完全看不出真实原因。这类「路径被二次解析」的坑只有实测才会暴露。
    const candidate = resolvePath(dir, name);
    if (existsSync(candidate)) return candidate;
  }
  const probe = spawnSync('sh', ['-c', `command -v ${name}`], { encoding: 'utf8' });
  if (probe.status === 0) return probe.stdout.trim() || null;
  return null;
};

// 候选目录一律相对脚本所在仓库根，不相对 cwd（理由同 ROOT）。
const GOFMT = resolveTool('gofmt');
const PRETTIER = resolveTool('prettier', [join(ROOT, 'web', 'node_modules', '.bin')]);

// prettier 缺失时的**最后兜底**：`npm exec` 拉取与 web/package.json 同版本的
// prettier 到 ~/.npm/_npx（npm 的缓存目录，不写工作区、不生成 node_modules）。
//
// 为什么要这层兜底，而不是直接把 «缺 prettier» 降级成「不校验格式」：
//   gate 任务里没有 web/node_modules（安装在另一个任务），而生成物是 TS。
//   若只做内容级比较，就等于让「契约漂移」这条门禁在 CI 上永久失去格式维度——
//   而本地有 node_modules 时它又在检查，于是又回到「本地红、CI 绿」的漂移。
//   兜底后：CI 与本地用**同一个版本的 prettier**，比较基准完全一致。
//
// 为什么不是无条件依赖它：它需要联网。所以解析顺序是
//   ① 仓库内已安装（npm install --prefix web 的产物，离线可用）
//   ② PATH
//   ③ npm exec 兜底
// ① 命中时行为与网络无关；③ 只在确实没装且能联网时生效。
// 版本号取自 web/package.json，避免兜底拉到的版本与仓库约定不一致
// （prettier 大版本之间格式化结果会变，那就是新的假红来源）。
const webPkgPath = at('web/package.json');
const PRETTIER_PIN = (() => {
  try {
    const pkg = JSON.parse(readFileSync(webPkgPath, 'utf8'));
    const v = { ...(pkg.dependencies ?? {}), ...(pkg.devDependencies ?? {}) }.prettier;
    return typeof v === 'string' ? v.replace(/^[^\d]*/, '') : null;
  } catch {
    return null;
  }
})();

/** 用 npm exec 拉取 pinned prettier 试跑一次；成功则返回可复用的 argv 前缀。 */
const resolvePrettierViaNpmExec = () => {
  if (!PRETTIER_PIN) return null;
  const spec = `prettier@${PRETTIER_PIN}`;
  const probe = spawnSync('npm', ['exec', '--yes', '--', spec, '--version'], {
    encoding: 'utf8',
    cwd: ROOT,
    env: { ...process.env, PIPEFLAGS: 'set -e' },
  });
  if (probe.status !== 0) return null;
  return ['npm', ['exec', '--yes', '--', spec]];
};

const PRETTIER_EXEC = PRETTIER ? null : resolvePrettierViaNpmExec();

// ---------------------------------------------------------------- 格式化工具缺失时的语义
//
// 纪律（docs/design/13 §3.4）：生成物必须与 contracts 一致，「一致」包含格式。
// 但「保证格式」与「校验契约」是**两件事**，必须分开对待——把前者做成后者的
// 前置条件，就会造出一个跨任务的隐式依赖，而 CI 的任务是按 job 隔离的。
//
// 起因（PR #2 门禁实红，两处问题同源）：
//   gate 任务里没有 prettier（它随 web 的 npm install 装在 web/node_modules，
//   而安装发生在**另一个任务 web-gate** 里）。旧实现把「找不到 prettier」
//   直接记成 problem 并让脚本退出 1，紧接着在**未格式化**的文本上做比较，
//   于是又刷出第二条「contract.gen.ts 与契约不一致」。两条都指向「契约漂移」，
//   真实原因却是「这个任务里没有前端依赖」——正是脚本自己注释里反对的方向误导。
//
// 因此：
//   - 工具缺失  → 记入 notes（stderr 可见、不影响退出码），比较时退回内容级比较；
//   - 工具报错  → 仍是硬失败。这**不是**放宽门禁：工具在却执行失败，说明比较
//                 基准不可信，绿了才是假绿。
//   - 强校验入口：`make gen-check` 只声明契约需要的工具（见 ci-exec 的
//     MAKE_TOOLSETS），不隐含 npm 依赖；`make gen`（写盘）则要求工具齐备，
//     因为写盘必须落已格式化的内容，否则会污染工作区。
const notes = [];

// 生成物必须与仓库的格式化配置一致，否则 `make gen` 之后 `make fmt-check` 仍会红，
// 形成「生成——格式化——再生成」的循环（上一轮踩过的同类问题）。
// 因此这里对 Go 走 gofmt、对 TS 走 web/ 下已安装的 prettier；
// 工具缺失时退回内容级比较，并在 notes 里写明「格式未校验」。
const formatInMemory = (path, content, kind) => {
  const dir = mkdtempSync(join(tmpdir(), 'ic-gen-'));
  const ext = kind === 'go' ? 'go' : 'ts';
  const tmp = join(dir, `out.${ext}`);
  writeFileSync(tmp, content);

  let r;
  if (kind === 'go') {
    if (!GOFMT) {
      // Go 工具链缺失：gate 的第一步就是 `bash scripts/install-go.sh`，走到这里
      // 说明环境本身不对，属硬失败（也是 ci-exec 的 MAKE_TOOLSETS.gen-check 已覆盖的项）。
      problems.push('未找到 gofmt（Go 工具链）：请先执行 bash scripts/install-go.sh');
      return content;
    }
    r = spawnSync(GOFMT, ['-w', tmp], { stdio: 'ignore' });
  } else {
    if (!PRETTIER && !PRETTIER_EXEC) {
      // 本地无依赖且拉不到 pinned 版本（离线）时的**明确降级**：
      // 退回内容级比较，并说明少了哪个维度。不静默、也不误报成「契约漂移」。
      notes.push(
        '未找到 prettier，且 npm exec 兜底不可用（离线？）。本次只做内容级比较，' +
          '未校验生成物格式。请 `npm install --prefix web` 后重跑。',
      );
      return content;
    }
    const args = ['--write', tmp];
    if (PRETTIER) {
      r = spawnSync(PRETTIER, args, { cwd: join(ROOT, 'web'), stdio: 'ignore' });
    } else {
      r = spawnSync(PRETTIER_EXEC[0], [...PRETTIER_EXEC[1], ...args], { cwd: ROOT, stdio: 'ignore' });
    }
  }
  // 工具存在但执行失败同样是硬错误：静默返回未格式化内容会让这个问题以
  // 「生成物与契约不一致」的面目出现，而真实原因是格式化失败。
  // 这条与上面的「工具缺失只记 notes」不矛盾：缺失时我们知道比较基准退回内容级，
  // 是**明确降级**；而在场却执行失败，说明基准不可信，绿了才是假绿。
  if (r.status !== 0) {
    problems.push(`${kind === 'go' ? 'gofmt' : 'prettier'} 执行失败（退出码 ${r.status}）：${path}`);
    return content;
  }
  return readFileSync(tmp, 'utf8');
};

// 关键：比较对象必须是「格式化之后」的内容。
// 直接比较未格式化的模板会让 `make gen` 永远报告有变化（写进去又被格式化），
// 于是 --check 永远失败、CI 永远红——这正是上一轮 gen-contracts 缺失的同类问题。
for (const [rel, content, kind] of outputs) {
  const path = at(rel);
  const prev = existsSync(path) ? readFileSync(path, 'utf8') : null;
  const formatted = formatInMemory(rel, content, kind);
  if (prev === formatted) continue;
  if (CHECK) {
    problems.push(`${rel} 与契约不一致（请执行 make gen 并提交）`);
    continue;
  }
  // 写盘模式下格式化工具必须齐备：缺工具时写下去的是未格式化内容，
  // 会把工作区越改越脏（实测会把 fmt-check 一起带红）。
  // 注意 gen-check 不写盘，因此这条不影响它——两者的严格程度**故意不同**：
  // 校验可以退回内容级比较，写盘不行。
  if (problems.length || notes.length) {
    if (notes.length) console.error('[gen] 有工具未就绪，已跳过写盘（不落未格式化内容）：' + notes.join(' / '));
    continue;
  }
  mkdirSync(dirname(path), { recursive: true });
  writeFileSync(path, formatted);
  written.push(rel);
}

console.log('契约生成与校验');
console.log('─'.repeat(56));
console.log(`错误码   : ${goCodes.size} 个`);
console.log(`事件类型 : ${contractEvents.size} 个`);
console.log(`路由     : ${registered.size} 条（openapi 声明 ${declared.size} 条）`);
console.log(`op kind  : ${opKinds.size} 个`);
console.log(`生成物   : ${written.length ? written.join(', ') : '无变化（已是最新）'}`);
console.log('─'.repeat(56));
// notes 走 stderr 但不影响退出码：它是「本次没校验到什么」，不是「校验没通过」。
// 与 problems 打印在同一段，避免出现「输出很干净、实际有降级」的假绿观感。
for (const n of notes) console.error('  · ' + n);
if (problems.length) {
  console.error(`发现 ${problems.length} 处问题：`);
  for (const p of problems) console.error('  - ' + p);
  process.exit(1);
}
console.log('契约与代码一致');
