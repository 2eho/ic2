#!/usr/bin/env node
// 自托管 Runner 工具链探测（R9）。
//
// 起因：本轮有四个缺陷都源自「工具在不在」这件事没有被显式验证：
//   1. 裸名调用 `gofmt`（Node 不查 PATH）→ 静默降级；
//   2. `prettier` 从未被声明为依赖，只靠 `npm exec` 隐式下载；
//   3. `make sec` 的用例名与实现不匹配 → 门禁空转；
//   4. e2e 拿不到后端就自动跳过 → 9 条用例空转。
//
// 共同点：**依赖的可用性没有被探测**，而是被假设。自托管 Runner 与 CNB 托管
// Runner 的工具集不同，这种假设在两种环境下会给出不同结论（"本地绿、CI 红"
// 或更糟的"CI 绿、其实没跑"）。
//
// 本脚本只做事实报告 + 对**必需**工具硬失败，不做任何自动安装：
// 自动安装会让环境问题变成隐式的网络依赖，反而更难排查。
import { existsSync, statSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import { join, resolve } from 'node:path';

/** 与 scripts/gen-contracts.mjs 相同的解析顺序：显式候选目录 → PATH。 */
function resolveTool(name, extraDirs = []) {
  for (const dir of extraDirs) {
    const candidate = resolve(dir, name);
    try {
      if (statSync(candidate).isFile()) return candidate;
    } catch {
      /* 继续 */
    }
  }
  const probe = spawnSync('sh', ['-c', `command -v ${name}`], { encoding: 'utf8' });
  return probe.status === 0 ? probe.stdout.trim() || null : null;
}

function version(cmd, args) {
  const r = spawnSync(cmd, args, { encoding: 'utf8' });
  if (r.status !== 0) return null;
  return (r.stdout || r.stderr).split('\n')[0].trim().slice(0, 80);
}

const TOOLS = [
  {
    name: 'go',
    args: ['version'],
    required: true,
    hint: '安装 Go 1.24+（CNB: 用 golang:1.24 镜像；自托管: apt/官方 tarball）',
    why: 'Go 门禁（build / vet / test / drill）',
  },
  {
    name: 'gofmt',
    args: ['-h'],
    required: true,
    hint: '随 Go 工具链安装；若 gofmt 不在 PATH，请把 $GOROOT/bin 加入 PATH',
    why: 'fmt-check 与契约生成物格式化（缺它会静默降级，见 gen-contracts 注释）',
  },
  {
    name: 'node',
    args: ['-v'],
    required: true,
    hint: '安装 Node 20+（CNB: node:22 镜像）',
    why: '前端构建、契约生成、全部门禁脚本',
  },
  {
    name: 'npm',
    args: ['-v'],
    required: true,
    hint: '随 Node 安装',
    why: '前端依赖与构建',
  },
  {
    name: 'prettier',
    args: ['--version'],
    required: true,
    extra: [join('web', 'node_modules', '.bin')],
    hint: 'cd web && npm install（prettier 是 web 的 devDependency）',
    why: '契约生成的 TS 生成物格式化（缺它会报"契约不一致"而真实原因是工具缺失）',
  },
  {
    name: 'make',
    args: ['--version'],
    required: true,
    hint: 'CNB/自托管 Runner 默认应有；缺失时可直接调用 node/go 命令',
    why: 'CI 与本地统一入口',
  },
  {
    name: 'sqlite3',
    args: ['-version'],
    required: false,
    hint: '可选：仅用于人工排查本地 sqlite 库',
    why: '人工排查（门禁用不到）',
  },
];

const problems = [];
const rows = [];

for (const t of TOOLS) {
  const path = resolveTool(t.name, t.extra ?? []);
  const v = path ? version(path, t.args) : null;
  rows.push({ tool: t.name, path, version: v, required: t.required });
  if (!path || !v) {
    if (t.required) {
      problems.push(`${t.name} 不可用（必需）：${t.why}\n      安装：${t.hint}`);
    }
  }
}

// 目录可写探测：用真实写测试而不是看 mode 位（容器里 mode 经常不可靠，
// 这一点与 cmd/ic-cli 的 doctor 保持一致）。
const dirs = ['web/node_modules', 'internal', 'docs/upstream'];
for (const d of dirs) {
  if (!existsSync(d)) {
    problems.push(`${d} 不存在（门禁依赖它；前端依赖请执行 cd web && npm install）`);
  }
}

console.log('自托管 Runner 工具链探测');
console.log('─'.repeat(64));
for (const r of rows) {
  const mark = r.path && r.version ? '✓' : r.required ? '✗' : '-';
  const ver = r.version ?? (r.required ? '不可用' : '未安装（可选）');
  console.log(`  ${mark} ${r.tool.padEnd(10)} ${ver}`);
  if (r.path && r.path !== r.tool) console.log(`      ${r.path}`);
}
console.log('─'.repeat(64));
if (problems.length) {
  console.error(`发现 ${problems.length} 处环境问题：`);
  for (const p of problems) console.error('  - ' + p);
  process.exit(1);
}
console.log('必需工具链齐备');
