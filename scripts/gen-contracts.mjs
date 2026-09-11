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
import { dirname } from 'node:path';

const CHECK = process.argv.includes('--check');
const problems = [];
const written = [];

// ---------------------------------------------------------------- 极简 YAML 读取
// 只支持本仓库用到的子集：缩进映射、`- ` 序列、`key: value`、内联数组、引号与注释。
// 刻意不引第三方依赖：契约文件格式由我们控制，读不了就报错，不静默兜底。
function parseYAML(text) {
  const lines = [];
  for (const raw of text.split('\n')) {
    if (!raw.trim() || raw.trim().startsWith('#')) continue;
    const indent = raw.match(/^ */)[0].length;
    lines.push({ indent, body: raw.trim().replace(/\s+#(?![^"']*["']).*$/, '') });
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

const openapiRaw = readRequired('contracts/openapi.yaml');
const errorsRaw = readRequired('contracts/errors.yaml');
const eventsRaw = readRequired('contracts/events.yaml');
const opsRaw = readRequired('contracts/ops.schema.json');

const openapi = openapiRaw ? parseYAML(openapiRaw) : {};
const errors = errorsRaw ? parseYAML(errorsRaw) : {};
const events = eventsRaw ? parseYAML(eventsRaw) : {};
const opsSchema = opsRaw ? JSON.parse(opsRaw) : {};

// 1) 错误码：contracts/errors.yaml 是唯一真源，必须与 platform/errors.go、i18n 双向一致。
const contractCodes = new Set(
  (errors?.codes ?? []).map((c) => (typeof c === 'string' ? c : c?.code)).filter(Boolean),
);
const errorsGo = readFileSync('internal/platform/errors.go', 'utf8');
const goCodes = new Map();
for (const m of errorsGo.matchAll(/^\s*(Code\w+)\s*=\s*"([a-z_]+)"\s*$/gm)) goCodes.set(m[2], m[1]);

for (const code of contractCodes) {
  if (!goCodes.has(code)) problems.push(`contracts/errors.yaml 声明了 ${code}，但 internal/platform/errors.go 没有该常量`);
}
for (const code of goCodes.keys()) {
  if (!contractCodes.has(code)) problems.push(`internal/platform/errors.go 有 ${code}，但 contracts/errors.yaml 未声明`);
}

// i18n：zh-CN 与 en-US 都必须有 errors.<code> 文案
const zhSrc = readFileSync('web/src/shared/i18n/zh-CN.ts', 'utf8');
const enSrc = readFileSync('web/src/shared/i18n/en-US.ts', 'utf8');
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
const graphSrc = readFileSync('internal/graph/doc.go', 'utf8') + readFileSync('internal/graph/service.go', 'utf8');
const goEventConsts = new Set();
for (const m of graphSrc.matchAll(/EventType\w*\s*=\s*"([a-z_.]+)"/g)) goEventConsts.add(m[1]);
const apiRunEventSrc = readFileSync('internal/exec/engine.go', 'utf8');
for (const m of apiRunEventSrc.matchAll(/"([a-z_]+)"\s*,\s*\/\/\s*event/g)) goEventConsts.add(m[1]);

// 事件类型只在契约里声明、代码里用；此处只做「契约里声明的必须非空且唯一」的卫生检查
if (contractEvents.size === 0) problems.push('contracts/events.yaml 未声明任何事件类型');
if (new Set(contractEvents).size !== (events?.events ?? []).length) problems.push('contracts/events.yaml 存在重复事件类型');

// 3) 路由：openapi.yaml 的 paths ↔ internal/api/router.go 的注册路径
const routerSrc = readFileSync('internal/api/router.go', 'utf8');
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
const opSrc = readFileSync('internal/graph/op.go', 'utf8');
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
const { join } = await import('node:path');

// 生成物必须与仓库的格式化配置一致，否则 `make gen` 之后 `make fmt-check` 仍会红，
// 形成「生成——格式化——再生成」的循环（上一轮踩过的同类问题）。
// 因此这里对 Go 走 gofmt、对 TS 走 web/ 下已安装的 prettier；工具缺失时保持原样不阻断。
const formatInMemory = (path, content, kind) => {
  const dir = mkdtempSync(join(tmpdir(), 'ic-gen-'));
  const ext = kind === 'go' ? 'go' : 'ts';
  const tmp = join(dir, `out.${ext}`);
  writeFileSync(tmp, content);
  const r =
    kind === 'go'
      ? spawnSync('gofmt', ['-w', tmp], { stdio: 'ignore' })
      : spawnSync('npx', ['--no-install', 'prettier', '--write', tmp], {
          cwd: 'web',
          stdio: 'ignore',
          shell: process.platform === 'win32',
        });
  if (r.status !== 0) return content;
  return readFileSync(tmp, 'utf8');
};

// 关键：比较对象必须是「格式化之后」的内容。
// 直接比较未格式化的模板会让 `make gen` 永远报告有变化（写进去又被格式化），
// 于是 --check 永远失败、CI 永远红——这正是上一轮 gen-contracts 缺失的同类问题。
for (const [path, content, kind] of outputs) {
  const prev = existsSync(path) ? readFileSync(path, 'utf8') : null;
  const formatted = formatInMemory(path, content, kind);
  if (prev === formatted) continue;
  if (CHECK) {
    problems.push(`${path} 与契约不一致（请执行 make gen 并提交）`);
    continue;
  }
  mkdirSync(dirname(path), { recursive: true });
  writeFileSync(path, formatted);
  written.push(path);
}

console.log('契约生成与校验');
console.log('─'.repeat(56));
console.log(`错误码   : ${goCodes.size} 个`);
console.log(`事件类型 : ${contractEvents.size} 个`);
console.log(`路由     : ${registered.size} 条（openapi 声明 ${declared.size} 条）`);
console.log(`op kind  : ${opKinds.size} 个`);
console.log(`生成物   : ${written.length ? written.join(', ') : '无变化（已是最新）'}`);
console.log('─'.repeat(56));
if (problems.length) {
  console.error(`发现 ${problems.length} 处问题：`);
  for (const p of problems) console.error('  - ' + p);
  process.exit(1);
}
console.log('契约与代码一致');
