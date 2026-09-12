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
  // 键允许引号与通配符（历史坑正是把 "**" 当事件名，旧正则 ^[\w"-]+ 匹配不到它，
  // 于是这条检查自己在最该报警的场景下静默通过）。取「行首到冒号」之间任意非空白字符。
  if (indent === branchIndent + 2 && /^\S.*:\s*$/.test(line.trim())) {
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
// 锚点名允许连字符（*.gate-stages* 这类写法很常见）；引用要连字符一起吃掉，
// 否则 \*gate-stages-typo\ 会被截成 gate，恰好命中已定义锚点而**静默漏报**。
const ANCHOR_NAME = '[\\w-]+';
const defs = new Set([...yaml.matchAll(new RegExp('^\\.(' + ANCHOR_NAME + '):\\s*&(' + ANCHOR_NAME + ')', 'gm'))].map((m) => m[2]));
for (const m of yaml.matchAll(new RegExp('(?<![&*])\\*(' + ANCHOR_NAME + ')', 'g'))) {
  if (!defs.has(m[1])) problems.push(`${CNB} 引用了未定义的锚点 *${m[1]}`);
}

// 4) 只调用 make 目标，且目标真实存在。
//    门禁命令的形式是 `node scripts/ci-exec.mjs run -- make <target>`，
//    因此要剥离外层包装后再判定——否则「目标改名」这类漂移又会在 CI 里才暴露。
const makeTargets = new Set([...makefile.matchAll(/^([a-zA-Z][\w-]*):/gm)].map((m) => m[1]));
const CI_EXEC_PREFIX = 'node scripts/ci-exec.mjs run -- ';
for (const [i, line] of lines.entries()) {
  const m = line.match(/^\s+(\S.*)$/);
  if (!m) continue;
  let cmd = m[1].trim();
  if (!cmd || cmd.startsWith('#') || cmd === '|' || cmd === '>') continue;
  if (cmd.includes(CI_EXEC_PREFIX)) cmd = cmd.slice(cmd.indexOf(CI_EXEC_PREFIX) + CI_EXEC_PREFIX.length).trim();
  // `npx --yes playwright install …` / `npm run build` 等由 ci-exec 直接转发，不是 make 目标
  const first = cmd.split(/\s+/)[0];
  if (first !== 'make') continue;
  const target = cmd.split(/\s+/)[1];
  if (target && !target.startsWith('-') && !makeTargets.has(target)) {
    problems.push(`${CNB}:${i + 1} 调用了不存在的 make 目标 "${target}"（Makefile 里没有）`);
  }
}

// 5) || true 只允许出现在上游巡检（它的产物是报告，不是门禁结论）。
//    与 check-no-silent-skip.mjs 保持一致：用**精确白名单**，不做模糊匹配——
//    宽匹配会让 `make parity || true` 因为附近有「上游巡检」字样而被放行。
const ALLOWED_TRUE = new Set(['bash scripts/upstream-sync.sh || true']);
for (const [i, line] of lines.entries()) {
  if (!/\|\|\s*true\b/.test(line)) continue;
  if (/^\s*#/.test(line)) continue; // 注释里的纪律说明
  const bare = line.trim().replace(/^script:\s*/, '');
  if (ALLOWED_TRUE.has(bare)) continue;
  problems.push(`${CNB}:${i + 1} 出现 "|| true" 但不在白名单（只有上游巡检可失败）——那等于把门禁失败变绿`);
}

// 5b) 契约/静态检查依赖前端依赖（prettier、tsc 都在 web/node_modules 里）。
//     本轮 CI 实红：gate 只装 Go 工具链，`make gen-check` 报
//     「未找到 prettier / contract.gen.ts 与契约不一致」——
//     前者是真实缺依赖，后者是**格式化比对失败的假象**（生成物格式化前后不同）。
//     这类失败最费时间：报的是「代码与契约不一致」，真实原因是环境缺依赖。
//     因此把「跑 gen-check 前必须装过前端依赖」变成硬检查，而不是靠人记得。
const NEEDS_WEB_DEPS = new Set(['gen-check', 'lint', 'tsc', 'fmt', 'perf', 'e2e', 'e2e-build']);
let sawWebInstall = false;
let gateJob = '';
for (const [i, line] of lines.entries()) {
  const jobMatch = /^\s*- name: ([\w-]+)\s*$/.exec(line);
  if (jobMatch) {
    gateJob = jobMatch[1];
    sawWebInstall = false;
  }
  if (!/^\s+script:\s*\S/.test(line)) continue;
  const cmd = line.replace(/^\s+script:\s*/, '').trim();
  if (cmd.includes('npm install') && cmd.includes('--prefix web')) sawWebInstall = true;
  const target = /make\s+([\w-]+)/.exec(cmd)?.[1];
  if (target && NEEDS_WEB_DEPS.has(target) && !sawWebInstall) {
    problems.push(
      `${CNB}:${i + 1}（任务 ${gateJob || '未知'}）在 ${target} 之前没有安装前端依赖：\n      ${cmd}\n` +
        '      prettier / tsc / vitest 都来自 web/node_modules；缺依赖时 gen-check 会报' +
        '「生成物与契约不一致」，把环境问题伪装成代码问题，排查成本极高。',
    );
  }
}

// 6) 门禁脚本不得依赖 bash-only 选项，且必须经 scripts/ci-exec.mjs 派发。
//    起因（CI 上真实红过三轮）：
//      a. 平台 runner 用 /bin/sh，脚本首行 `set -euo pipefail` 在 dash 上直接
//         `Illegal option -o pipefail` 返回 2 —— web-gate 第一步就挂了；
//      b. gate 只声明 golang 镜像，`node -v` 返回 127，后续 stage 全部 skip；
//      c. **平台解析 YAML 时会把块标量按 C 风格转义**：合法换行被压成空格、
//         `\n` 被换成真实换行。于是「一个 stage 多条命令」会合成一条，
//         argv 被污染（日志里表现为 `version: node: no such file`）。
//         只改 YAML 写法（改名 / 换单引号 / 换块标量）都只是换一种被解析的方式。
//    因此：严格模式 + 工具链自检 + 命令行拆分统一由 ci-exec.mjs 承载；
//    每个 stage 的 script 必须是**单行、无反斜杠**的一条 ci-exec 调用。
//
//    检查范围＝「门禁」stage 锚点（.gate-stages / .web-stages），
//    以及 gate / web-gate / frontend-check 这三个 job 自己的 script 块。
//    上游巡检（upstream-watch）本就是「允许失败、只产出报告」，不受此约束。
const CI_EXEC = 'node scripts/ci-exec.mjs';
const GATE_ANCHORS = ['.gate-stages', '.web-stages'];
const GATE_JOBS = new Set(['gate', 'web-gate', 'frontend-check']);
let currentJob = '';
let currentAnchor = '';
let scriptIndent = -1;
for (const [i, line] of lines.entries()) {
  const anchorMatch = line.match(/^\.([\w-]+):\s*&([\w-]+)\s*$/);
  if (anchorMatch) {
    currentAnchor = '.' + anchorMatch[1];
    currentJob = '';
    scriptIndent = -1;
    continue;
  }
  const jobMatch = line.match(/^\s*- name: ([\w-]+)\s*$/);
  if (jobMatch) {
    currentJob = jobMatch[1];
    currentAnchor = '';
    scriptIndent = -1;
    continue;
  }
  // 块标量形式（`script: |`）在门禁里**一律禁止**：平台解析会把块标量按 C 风格
  // 转义，多行命令被压成一条、argv 串位（CI 上已真实红过）。单行 `script: <cmd>`
  // 没有换行也没有反斜杠，平台怎么转义都伤不到。
  if (/^\s+script:\s*\|\s*$/.test(line) || /^\s+script:\s*$/.test(line)) {
    const inGateScopeBlock = GATE_ANCHORS.includes(currentAnchor) || GATE_JOBS.has(currentJob);
    if (inGateScopeBlock) {
      const where = currentAnchor || `任务 ${currentJob}`;
      problems.push(
        `${CNB}:${i + 1}（${where}）门禁 script 用了块标量（\`script: |\`）：\n` +
          '      平台解析块标量时按 C 风格转义，多行命令会被压成一条、argv 串位' +
          '（CI 上表现为命令收到多余参数）。' +
          '请改成单行 \`script: <一行命令>\`；需要串行就拆成多个 stage。',
      );
    }
    scriptIndent = line.match(/^( *)/)[1].length + 2;
    continue;
  }
  // 单行形式：`script: <cmd>`（本轮改成单行，见文件头纪律 2）
  const inlineScript = line.match(/^(\s+)script:\s*(\S.*)$/);
  if (inlineScript) {
    scriptIndent = -1;
    const inGateScopeInline = GATE_ANCHORS.includes(currentAnchor) || GATE_JOBS.has(currentJob);
    if (inGateScopeInline) {
      const raw = inlineScript[2].trim();
      const where = currentAnchor || `任务 ${currentJob}`;
      if (/^set\s+-[a-z]*o\s+pipefail/.test(raw)) {
        problems.push(
          `${CNB}:${i + 1} 脚本直接用 "set -o pipefail"：CI 的 shell 不保证是 bash（dash 会直接失败），` +
            `改用 ${CI_EXEC}（严格模式由它承载）`,
        );
      } else if (/\\/.test(raw)) {
        problems.push(
          `${CNB}:${i + 1}（${where}）门禁命令含反斜杠：\n      ${raw}\n` +
            '      平台解析 YAML 时按 C 风格转义处理块标量，反斜杠会被吃掉或变成换行，' +
            '导致命令被压平/污染（CI 上表现为 argv 串位）。' +
            '请把多步写进一个 node 脚本，或在 Makefile 里组合。',
        );
      } else if (/['"]/.test(raw)) {
        problems.push(
          `${CNB}:${i + 1}（${where}）门禁命令含引号：\n      ${raw}\n` +
            '      引号是平台 YAML 标量解析最容易改写的东西（C 风格转义 + 与 YAML 引号冲突），' +
            '一旦被改写，CI 里表现为命令收到错误参数。' +
            '请让 YAML 里只出现「纯命令 + 空格分隔的参数」，需要引号时写进 node 脚本或 Makefile。',
        );
      } else if (/[;&|]|\$\$/.test(raw)) {
        problems.push(
          `${CNB}:${i + 1}（${where}）门禁命令里出现 shell 连接符（; / && / || / 管道）：\n      ${raw}\n` +
            '      串行请拆成多个 stage，或写进 Makefile 目标——在 YAML 里拼命令等于让门禁重新依赖 shell 方言。',
        );
      } else if (!raw.startsWith(CI_EXEC) && !/^[A-Z_]+=/.test(raw)) {
        problems.push(
          `${CNB}:${i + 1}（${where}）门禁命令未经过 ${CI_EXEC}：\n      ${raw}\n` +
            '      绕过它会让严格模式与工具链自检同时失效，方言/缺工具的失败会变成假绿',
        );
      }
    }
    continue;
  }
  if (scriptIndent < 0) continue;
  const indent = line.match(/^( *)/)[1].length;
  if (line.trim() && indent < scriptIndent) {
    scriptIndent = -1;
    continue;
  }
  const inGateScope = GATE_ANCHORS.includes(currentAnchor) || GATE_JOBS.has(currentJob);
  if (!inGateScope) continue;
  const raw = line.trim();
  if (!raw || raw.startsWith('#')) continue;
  if (/^set\s+-[a-z]*o\s+pipefail\s*$/.test(raw)) {
    problems.push(
      `${CNB}:${i + 1} 脚本直接用 "set -o pipefail"：CI 的 shell 不保证是 bash（dash 会直接失败），` +
        `改用 ${CI_EXEC}（严格模式由它承载）`,
    );
    continue;
  }
  if (/\\/.test(raw)) {
    problems.push(
      `${CNB}:${i + 1} 门禁命令含反斜杠：\n      ${raw}\n` +
        '      平台解析 YAML 时按 C 风格转义处理块标量，反斜杠会被吃掉或变成换行，' +
        '导致命令被压平/污染（CI 上表现为 argv 串位）。' +
        '请把多步写进一个 node 脚本，或在 Makefile 里组合，YAML 只留一条无转义命令。',
    );
    continue;
  }
  if (/['"]/.test(raw)) {
    problems.push(
      `${CNB}:${i + 1} 门禁命令含引号：\n      ${raw}\n` +
        '      引号是平台 YAML 标量解析最容易改写的东西（C 风格转义 + 与 YAML 引号冲突），' +
        '一旦被改写，CI 里表现为命令收到错误参数。' +
        '请让 YAML 里只出现「纯命令 + 空格分隔的参数」，需要引号时写进 node 脚本或 Makefile。',
    );
    continue;
  }
  if (/[;&|]|\$\$/.test(raw)) {
    problems.push(
      `${CNB}:${i + 1} 门禁命令里出现 shell 连接符（; / && / || / 管道）：\n      ${raw}\n` +
        '      串行请拆成多个 stage，或写进 Makefile 目标——' +
        '在 YAML 里拼命令等于让门禁重新依赖 shell 方言。',
    );
    continue;
  }
  if (raw.startsWith(CI_EXEC) || /^[A-Z_]+=/.test(raw)) continue;
  const where = currentAnchor || `任务 ${currentJob}`;
  problems.push(
    `${CNB}:${i + 1}（${where}）门禁命令未经过 ${CI_EXEC}：\n      ${raw}\n` +
      '      绕过它会让严格模式与工具链自检同时失效，方言/缺工具的失败会变成假绿',
  );
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
