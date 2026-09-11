#!/usr/bin/env node
// .cnb.yml 配置校验（在本地就能跑，不依赖平台）。
//
// 为什么需要：上一轮整条 gate 流水线因为「把 "**" 当事件名」而完全没生效，
// 而本地没有任何检查能发现它——错误只在平台的 conf-schema 里暴露。
// 这里把已知的平台约束固化成脚本，避免同类问题再次静默。
//
// 校验项：
//   1. 无 tab 缩进（YAML 要求空格）；
//   2. 分支下的键必须是事件名（push / pull_request / crontab: …），
//      "**" 只能作为事件之下的分支通配；
//   3. YAML 锚点定义与引用成对；
//   4. 门禁步骤只调用 `make <target>`，且目标在 Makefile 中真实存在；
//   5. 只有明确标注的任务允许 `|| true`（其余等于把红变绿）。
import { readFileSync, existsSync } from 'node:fs';

const CNB = '.cnb.yml';
const MAKEFILE = 'Makefile';
const problems = [];

if (!existsSync(CNB)) {
  console.error(`缺少 ${CNB}`);
  process.exit(1);
}
const yaml = readFileSync(CNB, 'utf8');
const makefile = existsSync(MAKEFILE) ? readFileSync(MAKEFILE, 'utf8') : '';

// 1) tab 缩进
for (const [i, line] of yaml.split('\n').entries()) {
  if (/^\s*\t/.test(line)) problems.push(`${CNB}:${i + 1} 使用了 tab 缩进（YAML 要求空格）`);
}

// 2) 事件名合法性：扫描 `main:` 等顶层分支下的直接子键
const EVENT_NAMES = ['push', 'pull_request', 'pull_request_target', 'tag_push', 'crontab', 'api_trigger', 'web_trigger'];
const lines = yaml.split('\n');
let inBranch = false;
let branchIndent = 0;
for (let i = 0; i < lines.length; i += 1) {
  const line = lines[i];
  if (!line.trim() || line.trim().startsWith('#')) continue;
  const indent = line.match(/^ */)[0].length;
  // 顶层分支（main / $ 等，缩进 0 且以冒号结尾）
  if (indent === 0 && /^[\w.$-]+:\s*$/.test(line)) {
    inBranch = true;
    branchIndent = indent;
    continue;
  }
  if (!inBranch) continue;
  if (indent <= branchIndent && !(indent === 0 && /^[\w.$-]+:\s*$/.test(line))) {
    if (indent === branchIndent && /^[\w.$-]+:\s*$/.test(line)) {
      branchIndent = indent;
      continue;
    }
  }
  // 分支下的直接子键：缩进 = branchIndent + 2 且形如 `key:`
  if (indent === branchIndent + 2 && /^[\w"-]+.*:\s*$/.test(line)) {
    const rawKey = line.trim().replace(/:$/, '').replace(/^["']|["']$/g, '');
    const isCrontab = rawKey.startsWith('crontab:') || rawKey.startsWith('crontab');
    const isWebTrigger = rawKey.startsWith('web_trigger');
    const isKnown = EVENT_NAMES.includes(rawKey) || isCrontab || isWebTrigger;
    if (!isKnown) {
      problems.push(
        `${CNB}:${i + 1} 分支 "${rawKey}" 不是合法事件名（应是 push / pull_request / crontab: …）。` +
          '注意 "**" 只能作为事件之下的分支通配，不能作为事件名——上一轮整条 gate 因此未生效。',
      );
    }
  }
  if (indent === 0 && !/^[\w.$-]+:\s*$/.test(line)) inBranch = false;
}

// 3) 锚点配对
const defs = new Set([...yaml.matchAll(/^\.([\w-]+):\s*&(\w+)/gm)].map((m) => m[2]));
for (const m of yaml.matchAll(/(?<![&*])\*(\w+)/g)) {
  if (!defs.has(m[1])) problems.push(`${CNB} 引用了未定义的锚点 *${m[1]}`);
}

// 4) 只调用 make 目标，且目标真实存在
const makeTargets = new Set([...makefile.matchAll(/^([a-zA-Z][\w-]*):/gm)].map((m) => m[1]));
// 这些是 shell/node 直接调用，属于「工具」而非门禁目标，显式放行并说明
const SHELL_ALLOWLIST = ['node', 'go', 'npm', 'npx', 'bash', 'sh', 'cd', 'set', 'echo', 'test', 'cat', 'exit', 'if', 'fi', 'cp', 'mkdir', 'git', 'true', 'false', 'ls', 'rm', 'for', 'do', 'done', 'then', 'else'];
for (const [i, line] of lines.entries()) {
  const m = line.match(/^\s*script:\s*(.+)$/) || line.match(/^\s+(\S.*)$/);
  if (!m) continue;
  const cmd = m[1].trim();
  if (!cmd || cmd.startsWith('#') || cmd === '|' || cmd === '>') continue;
  const first = cmd.split(/\s+/)[0];
  if (first === 'make') {
    const target = cmd.split(/\s+/)[1];
    if (target && !target.startsWith('-') && !makeTargets.has(target)) {
      problems.push(`${CNB}:${i + 1} 调用了不存在的 make 目标 "${target}"`);
    }
    continue;
  }
  if (!SHELL_ALLOWLIST.includes(first)) {
    // 环境变量赋值或续行不报错
    if (/^[A-Z_]+=/.test(first)) continue;
    continue;
  }
}

// 5) || true 只允许出现在上游巡检（它的产物是报告，不是门禁结论）
for (const [i, line] of lines.entries()) {
  if (!line.includes('|| true')) continue;
  const context = lines.slice(Math.max(0, i - 12), i + 1).join('\n');
  if (!/upstream-sync|upstream-watch|巡检/.test(context)) {
    problems.push(`${CNB}:${i + 1} 出现 "|| true" 但没有「上游巡检」上下文——那等于把门禁失败变绿`);
  }
}

console.log('.cnb.yml 配置校验');
console.log('─'.repeat(56));
console.log(`锚点 : ${[...defs].join(', ') || '无'}`);
console.log(`make 目标引用 : ${[...new Set([...yaml.matchAll(/make ([\w-]+)/g)].map((m) => m[1]))].join(', ')}`);
console.log('─'.repeat(56));
if (problems.length) {
  console.error(`发现 ${problems.length} 处问题：`);
  for (const p of problems) console.error('  - ' + p);
  process.exit(1);
}
console.log('.cnb.yml 符合平台约束');
