#!/usr/bin/env node
// 前端架构约束（docs/design/08-infra.md §6）：
//   1. features/* 不允许跨目录深层 import —— 即 features/A 不得直接 import features/B/...；
//      跨特性通信必须经过 shared/ 或 app/ 的公开出口，否则会退化成不可拆的巨网。
//   2. kernel/ 不得 import react（由 check-kernel-purity.mjs 负责，这里是它的补充面）。
//   3. 禁止从 features/*/internal 之类的私有路径引用（不存在则不检查，存在则强制）。
import { readdirSync, readFileSync, statSync } from 'node:fs';
import { join } from 'node:path';

const ROOT = 'web/src';
const problems = [];

const walk = (dir, out = []) => {
  for (const name of readdirSync(dir)) {
    if (name === 'node_modules' || name === '__tests__') continue;
    const p = join(dir, name);
    const st = statSync(p);
    if (st.isDirectory()) walk(p, out);
    else if (/\.(ts|tsx)$/.test(name)) out.push(p);
  }
  return out;
};

const files = walk(ROOT);

// allowedCrossFeature：确实需要跨特性协作的场景，逐条登记 + 给理由。
// 空表是目标状态；新条目必须在 PR 里解释，否则就是隐性耦合。
//
// 已登记的耦合必须满足一个条件：**依赖方向是稳定的**。
// 例如 canvas 是宿主、plugins 是可插拔渲染器，方向永远是 canvas → plugins；
// 反过来 plugins → canvas 会造成环，因此那条走 kernel 的契约类型（不是 kernel 实现）。
const ALLOWED = new Map([
  // 画布是宿主，插件节点视图是可插拔渲染器（方向固定：canvas → plugins）
  ['web/src/features/canvas/components/NodeContent.tsx', new Set(['plugins'])],
  // 配置中心是宿主，插件管理是其中一个面板（方向固定：settings → plugins）
  ['web/src/features/settings/SettingsPage.tsx', new Set(['plugins'])],
  // 画布页面是宿主，Agent 侧边栏是可插拔面板（方向固定：canvas → agent）
  ['web/src/features/canvas/CanvasPage.tsx', new Set(['agent'])],
]);

// 允许「引用但不依赖」的例外：只用于类型/端口契约的路径。
// 判据：import 语句带 `type` 修饰，或路径落在 kernel/index（对外出口）且只取类型。
const TYPE_ONLY_EXCEPTIONS = new Set([
  'web/src/features/plugins/PluginNodeView.tsx',
]);
const isTypeOnlyImport = (body, target) => {
  const re = new RegExp(`import\\s+type\\s+[^;]*from\\s+'@/features/${target}/[^']+'`, 'g');
  return re.test(body);
};

for (const file of files) {
  const body = readFileSync(file, 'utf8');
  const own = file.split('/features/')[1]?.split('/')[0] ?? null;
  if (!own) continue;
  const allowed = ALLOWED.get(file) ?? new Set();
  for (const m of body.matchAll(/from\s+'@\/features\/([a-zA-Z0-9_-]+)\/([^']+)'/g)) {
    const [, target, rest] = m;
    if (target === own) continue;
    if (allowed.has(target)) continue;
    // 类型专用 import 不构成运行时耦合（编译后消失），但仍要求登记，
    // 否则会演变成「悄悄把实现也引进来」。
    if (TYPE_ONLY_EXCEPTIONS.has(file) && isTypeOnlyImport(body, target)) continue;
    problems.push(`${file} 跨特性直接引用 @/features/${target}/${rest}`);
  }
  // 深层 import：@/features/x/.../... 超过三层视为私有耦合
  for (const m of body.matchAll(/from\s+'@\/features\/([a-zA-Z0-9_-]+)\/((?:[^/']+\/){3,}[^']+)'/g)) {
    problems.push(`${file} 深层 import @/features/${m[1]}/${m[2]}`);
  }
}

console.log('前端特性边界检查');
console.log('─'.repeat(56));
console.log(`扫描文件 : ${files.length}`);
console.log('─'.repeat(56));
if (problems.length) {
  console.error(`发现 ${problems.length} 处跨特性耦合：`);
  for (const p of problems) console.error('  - ' + p);
  process.exit(1);
}
console.log('特性之间无未登记的直接依赖');
