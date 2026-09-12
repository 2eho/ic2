#!/usr/bin/env node
// CI 入口包装器：门禁脚本一律经它执行。
//
// 为什么需要（上一轮 CI 两条流水线全红，就死在这两处）：
//   1. 平台脚本 runner 用的是 **/bin/sh**，不保证是 bash。脚本首行
//      `set -euo pipefail` 在 dash 上直接报 `Illegal option -o pipefail` 并返回 2 ——
//      web-gate 的「安装依赖」一步都没跑起来就红了。
//   2. gate 只声明了 golang:1.24 镜像，里面没有 node，`node -v` 返回 127 ——
//      「工具链就绪」直接把后面 7 个 stage 全部 skip。
//
// 本脚本因此做三件事，且都不引入「新的静默失败」：
//   a. 严格模式与 shell 方言解耦：`node` 是各任务都有的（CI 的 shell 不是 bash），
//      由 node 自己实现 -e / pipefail 语义（见 run()），不再依赖 `set -o`。
//   b. 工具链自检：用 command -v 定位绝对路径，缺了就在「工具链探针」阶段显式失败
//      并给出「该任务该用哪个镜像」的可执行指令。
//   c. 失败传播：子进程非 0 / 被信号杀死，一律原样返回退出码，绝不吞。
//
// 明确不做的事：不猜镜像的包管理器去现场装工具链。
//   实测 C 端镜像里的发行版版本普遍不达标（debian bookworm 只有 golang 1.19、
//   nodejs 18），装上去会让门禁在「版本不对」的环境里跑，比直接红更危险。
//   正确做法是 .cnb.yml 里给每个 job 指定带工具链的 image。
import { spawnSync } from 'node:child_process';
import { existsSync } from 'node:fs';
import { join, resolve } from 'node:path';

const STRICT_ENV = {
  ...process.env,
  // `sh -c` 启动的子脚本（npm 生命周期脚本等）也是严格模式。
  // 注意这里只能用 sh 也认识的 `-e`；pipefail 是 bash 专有选项，
  // 而 CI 的 /bin/sh 可能是 dash —— 让它失败正是上一轮的红。
  PIPEFLAGS: 'set -e',
  // Makefile 里 `SHELL := /bin/bash`：只要镜像里有 bash 就走严格模式。
  SHELL: '/bin/bash',
};

const IMAGE_HINT = {
  go: 'golang:1.24',
  gofmt: 'golang:1.24',
  node: 'node:22',
  npm: 'node:22',
  npx: 'node:22',
  prettier: 'node:22',
  bash: 'bash（Debian 系镜像自带 /bin/bash；否则请把 image 换成 golang:1.24 / node:22）',
  git: 'git（改用自带 git 的镜像）',
  make: 'make（golang:1.24 / node:22 都自带；缺则换用带 make 的镜像）',
};

// 工具查找顺序：PATH → 仓库内的固定位置。
//
// 为什么需要后者：prettier 装在 web/node_modules/.bin，而 CI 的 `npm install`
// 不会把这个目录加进 PATH。只查 PATH 会让 `probe ... prettier` 在 CI 上恒失败，
// 而 gen-contracts.mjs 自己已经会去 web/node_modules/.bin 找它——
// 两处判断不一致会让人以为「工具没装」，实际只是查找方式不同。
// 因此这里列出**确定的候选路径**，不做通配搜索、不猜包管理器。
const LOCAL_TOOL_DIRS = ['web/node_modules/.bin', 'canvas-agent/node_modules/.bin'];

function which(bin) {
  const r = spawnSync('/bin/sh', ['-c', `command -v ${bin} 2>/dev/null || true`], { encoding: 'utf8' });
  const fromPath = (r.stdout || '').trim();
  if (fromPath) return fromPath;
  for (const dir of LOCAL_TOOL_DIRS) {
    if (existsSync(join(dir, bin))) return resolve(dir, bin);
  }
  return null;
}

// 必备工具集按「这个任务需要什么」推导，不能一刀切：
//   - 前端任务（web-gate）跑的是 npm/vite/tsc，镜像里本来就没有 go；
//     若在这里要求 go，会把 web 任务误判成环境故障（第一版就踩了这个坑）。
//   - 因此：**要执行的命令本身**一律必查；门禁工具链只查「该命令跑起来真正会用到的那几项」。
const TOOLSETS = {
  go: ['go', 'gofmt'],
  node: ['node', 'npm'],
};
// make 目标 → 真实需要的工具链（依据 Makefile 里的实际命令，不靠猜）。
const MAKE_TOOLSETS = {
  // 契约生成需要 prettier（web/node_modules/.bin）与 gofmt：
  // 缺它们时 gen-contracts 会报「生成物与契约不一致」，把「环境缺依赖」
  // 伪装成「代码与契约不一致」——本轮 CI 上就是这么红的。
  'gen-check': ['node', 'npm', 'gofmt', 'prettier'],
  'adversary': ['node'],
  'parity': ['node'],
  'parity-enforce': ['node'],
  'agent-check': ['node'],
  'perf': ['node', 'npm'],
  'boundaries': ['node'],
  'upstream-radar': ['node'],
  'e2e-build': ['node', 'npm'],
  // 只需要 Go 工具链里的 gofmt（不编译）
  'fmt-check': ['gofmt'],
  // 需要完整 Go 工具链
  'preflight': ['go', 'gofmt', 'node'],
  'vet': ['go'],
  'lint': ['go', 'node'],
  'test': ['go', 'node', 'npm'],
  'test-go': ['go'],
  'test-agent': ['node'],
  'sec': ['go', 'node'],
  'drill': ['go'],
  'e2e': ['go', 'node', 'npm'],
};

function requiredTools(argv) {
  const bin = argv[0];
  const wanted = new Set();
  // 命令本身必须存在
  if (IMAGE_HINT[bin]) wanted.add(bin);
  if (bin === 'make') {
    // 工具需求按「该目标真正跑什么」推导，不能统一假定 go + node：
    // 把不需要 Go 的目标（perf / adversary / parity / sec / agent-check）判成环境故障，
    // 会让人开始怀疑门禁并绕过它——误报失败与漏报失败同样有害。
    // 映射里没有的目标保守要求 go + gofmt + node + npm（宁可多查不可漏查）。
    const tools = MAKE_TOOLSETS[argv[1]] || ['go', 'gofmt', 'node', 'npm'];
    for (const t of tools) wanted.add(t);
    wanted.add('make');
  }
  if (bin === 'go') for (const t of TOOLSETS.go) wanted.add(t);
  if (['node', 'npm', 'npx'].includes(bin)) for (const t of TOOLSETS.node) wanted.add(t);
  // `npx playwright install --with-deps` 会在容器里装系统依赖，只要求 npx 本身
  return [...wanted].filter((b) => { const local = b === bin; return local || IMAGE_HINT[b]; });
}
function missingTools(argv) {
  return requiredTools(argv).filter((b) => !which(b));
}

function reportMissing(missing) {
  console.error('[ci-exec] 工具链缺失，门禁无法保证可信，直接失败（不做跳过/兜底）：');
  for (const b of missing) console.error(`  - ${b} 缺失 → 请给该 CI 任务指定 image: ${IMAGE_HINT[b]}`);
  console.error('[ci-exec] 这条规则来自 docs/design/13 §3：宁可红，也不要假绿。');
}

function run(argv) {
  console.log(`[ci-exec] $ ${argv.join(' ')}`);
  // 关键：不用 shell 解析，argv 直接 exec；严格模式由 node 自己承载。
  // - spawnSync 失败（ENOENT）等价于 127，显式返回，不吞。
  const r = spawnSync(argv[0], argv.slice(1), { stdio: 'inherit', env: STRICT_ENV, shell: false });
  if (r.error) {
    console.error(`[ci-exec] 无法执行 ${argv[0]}：${r.error.code || r.error.message}`);
    return 127;
  }
  if (r.signal) console.error(`[ci-exec] ${argv[0]} 被信号终止：${r.signal}（OOM/超时也算失败）`);
  return r.status === null ? 1 : r.status;
}
// 兼容入口：`run "<完整命令行>"`（单字符串形式）。
//
// 为什么需要（PR #2 第三轮 CI 实红）：
//   CNB 平台在**运行 job 之前**会先解析一次 `.cnb.yml`。实测该解析把块标量按
//   C 风格转义处理：shell 里合法的 `\n` 会被换成真实换行、`\s` 会得到字面量 "s"。
//   而 CI 的 shell 是 **dash**，dash 的 echo **不解释反斜杠转义**，于是
//
//     stages:
//       - script: |
//           node scripts/ci-exec.mjs run -- go version
//           node scripts/ci-exec.mjs run -- node -v
//
//   在平台侧被压成**一条命令**，argv 变成 ['go','version','node','-v']，
//   go 于是报 'version: node: no such file'。只改 .cnb.yml 的写法没用：改名、
//   换单引号、换块标量都只是换一种被解析的方式（三种均已实测）。
//
// 因此把「一段命令行」的拆分**下沉到 node 里**：YAML 层只写一行、不含反斜杠，
// 平台怎么转义都伤不到壳；引号与转义语义由 splitArgs() 用确定规则实现。

/** 按 shell 规则拆分一段命令行：引号成对则整体成词，反斜杠转义下一字符。 */
export function splitArgs(line) {
  const argv = [];
  let cur = '';
  let quote = null; // 单引号或双引号
  let started = false;
  // 双引号内反斜杠只对这四类字符生效（与 shell 一致）
  const escapableInDouble = new Set(['"', '\\', '$', '`']);
  for (let i = 0; i < line.length; i += 1) {
    const ch = line[i];
    const next = line[i + 1];
    if (quote === "'") {
      if (ch === "'") quote = null;
      else cur += ch;
      continue;
    }
    if (ch === '\\') {
      const takes = quote !== '"' || (next !== undefined && escapableInDouble.has(next));
      if (takes && next !== undefined) {
        cur += next;
        i += 1;
      } else {
        cur += ch;
      }
      started = true;
      continue;
    }
    if (quote) {
      if (ch === quote) quote = null;
      else cur += ch;
      started = true;
      continue;
    }
    if (ch === '"' || ch === "'") {
      quote = ch;
      started = true;
      continue;
    }
    if (/\s/.test(ch)) {
      if (started) argv.push(cur);
      cur = '';
      started = false;
      continue;
    }
    cur += ch;
    started = true;
  }
  if (quote) throw new Error('命令行的引号未闭合：' + line);
  if (started) argv.push(cur);
  return argv;
}
// 只有直接执行时才跑 CLI：这样测试可以直接 import splitArgs 而不触发参数解析。
// 判据用 realpath：CI 里脚本可能经符号链接被调用。
import { realpathSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

const invokedDirectly = (() => {
  const entry = process.argv[1];
  if (!entry) return false;
  try {
    return realpathSync(entry) === realpathSync(fileURLToPath(import.meta.url));
  } catch {
    return false;
  }
})();

if (invokedDirectly) {
  const [, , cmd, ...rest] = process.argv;

  // which / probe：列出工具绝对路径。作用是在**还没跑门禁**时就把「镜像里没有这个工具」
  // 暴露出来——上一轮 gate 只声明 golang 镜像，`node -v` 直接 127，后面 7 个 stage 全被 skip，
  // 那种红来得太晚、也看不出是环境问题。缺任何一个都以 127 失败，不做跳过。
  if (cmd === 'which' || cmd === 'probe') {
    let failed = false;
    for (const b of rest) {
      const p = which(b);
      console.log(`${b}=${p || ''}`);
      if (!p) failed = true;
    }
    if (failed) console.error('[ci-exec] 上列为空的工具在该任务镜像里不存在（见 IMAGE_HINT）');
    process.exit(failed ? 1 : 0);
  }

  let argv = rest;
  // 三种写法都收敛到同一个 argv 数组，再走下方同一条工具链自检路径：
  //   run -- make lint        · 显式分隔符，YAML 层一行一条（历史写法）
  //   run "go version"        · 整段命令行，见 splitArgs 注释（平台会压行时的写法）
  //   run -- "make lint"      · 分隔符 + 整段
  if (cmd === 'run') {
    const cut = argv.indexOf('--');
    // 没有 `--` 时，整个参数表就是「命令行/命令+参数」，不能当未知选项拒掉
    const head = cut >= 0 ? argv.slice(0, cut) : [];
    if (head.length) {
      console.error('用法：node scripts/ci-exec.mjs run -- <command> [args...]');
      console.error('      node scripts/ci-exec.mjs run "<command> [args...]"');
      console.error('      node scripts/ci-exec.mjs probe|which <bin>...');
      process.exit(2);
    }
    let tail = cut >= 0 ? argv.slice(cut + 1) : argv;
    if (tail.length === 1 && /[\s'"\\]/.test(tail[0])) tail = splitArgs(tail[0]);
    argv = tail;
    // 不在这里重新实现 shell：`;` / `&&` / `||` 需要串行时直接拆成多个 stage。
    // 把一段命令行当 shell 重跑会开出新的假绿通道（中间一步失败会被吞掉）。
    if (/[;&|]/.test(argv.join(' '))) {
      console.error('[ci-exec] 一段命令行里不支持 ; / && / ||，请拆成多个 stage：' + argv.join(' '));
      process.exit(2);
    }
  }
  if (cmd !== 'run') {
    console.error('用法：node scripts/ci-exec.mjs run -- <command> [args...]');
    console.error('      node scripts/ci-exec.mjs probe|which <bin>...');
    process.exit(2);
  }
  if (!argv.length) {
    console.error('[ci-exec] run 需要命令，例如：node scripts/ci-exec.mjs run -- make lint');
    process.exit(2);
  }

  // 自检：缺失即失败并给出怎么修；这是「工具链探针」阶段的全部作用
  const missing = missingTools(argv);
  if (missing.length) {
    reportMissing(missing);
    process.exit(127);
  }

  // `npx --yes playwright install --with-deps` 要联网并调 apt，工具自检只覆盖 npx 是否存在
  process.exit(run(argv));

}
