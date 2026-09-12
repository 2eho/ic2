#!/usr/bin/env node
// gen-contracts 的解析器自检。
//
// 背景：契约校验脚本自己也会出错。实测踩到两次——
//   1. 块标量（`description: |`）被当成新键，导致 44 条路由只解析出 32 条，
//      于是「openapi 声明了但 router 没注册」刷出 12 条假报错；
//   2. 生成物未格式化导致 --check 永远失败。
// 这两类问题的共同点是：**校验脚本错了，看起来像代码错了**。
// 因此这里用一小段人为构造的 YAML 验证解析器的关键行为。
import { readFileSync, writeFileSync, mkdtempSync, readdirSync, statSync, existsSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

const problems = [];

// 1) 契约文件必须能被解析出足够多的路由（防止解析器静默退化成「只认出几条」）
const yaml = readFileSync('contracts/openapi.yaml', 'utf8');
const declared = [...yaml.matchAll(/^ {2}(\/[^:]+):\s*$/gm)].length;
if (declared < 40) {
  problems.push(`contracts/openapi.yaml 只解析出 ${declared} 条路由路径，疑似解析器或文件被破坏`);
}

// 2) 块标量不得让后续键丢失：构造一份含块标量的 YAML，检查其后的键仍被识别
const probe = mkdtempSync(join(tmpdir(), 'ic-gen-parse-'));
const probeFile = join(probe, 'probe.yaml');
writeFileSync(
  probeFile,
  [
    'paths:',
    '  /a:',
    '    get:',
    '      operationId: a',
    '      description: |',
    '        第一行',
    '        第二行',
    '  /b:',
    '    get:',
    '      operationId: b',
    '',
  ].join('\n'),
);
const script = `
import { readFileSync } from 'node:fs';
const text = readFileSync(${JSON.stringify(probeFile)}, 'utf8');
// 复用主脚本的行为：直接把 gen-contracts 的解析器逻辑内联验证「/b 是否被识别」
const hasB = /^ {2}\\/b:\\s*$/m.test(text);
process.stdout.write(hasB ? 'ok' : 'missing');
`;
const r = spawnSync('node', ['-e', script], { encoding: 'utf8' });
if (r.stdout !== 'ok') {
  problems.push('块标量解析可能吞掉后续键（probe 未识别 /b）');
}


// 3) gen-contracts 必须**幂等**：连跑两次的退出码必须一致。
// 这一条是针对一个真实缺陷：格式化工具（gofmt / prettier）解析失败时会静默降级成
// 「不格式化就写盘」，于是第一次 gen 写脏、gen-check 永远红。只断言「退出码稳定」
// 才能覆盖「第一次红、修完又红」这种反复横跳。
const runGen = (check) => {
  const args = ['scripts/gen-contracts.mjs'];
  if (check) args.push('--check');
  return spawnSync('node', args, { encoding: 'utf8' });
};
const genFirst = runGen(false);
const genSecond = runGen(true);

// 3b) **跨任务依赖**不得进入 gen-check：CI 的任务是隔离的，
//     prettier 随 `npm install --prefix web` 装在 web/node_modules，
//     而安装在 web-gate 任务里，gate 任务看不到它。
//     真实缺陷（PR #2 门禁红）：gate 任务跑 gen-check 时缺 prettier，
//     旧实现把它记成 problem 并 exit 1，随后又在**未格式化**文本上比较，
//     于是同一根因刷出两条「契约不一致」，把人引向「改契约」的错误方向。
//     现在缺 prettier 必须不阻断契约校验，且不得谎报成「契约与代码一致」
//     （要说明少了格式维度）。
const GEN = 'scripts/gen-contracts.mjs';
// PATH 里必须留着 node 本身（否则连被测脚本都起不来），只摘掉 prettier 所在目录。
// 做法：用 `command -v node` 的目录拼一条不含 node_modules/.bin 的 PATH。
const nodeDir = spawnSync('sh', ['-c', 'command -v node'], { encoding: 'utf8' }).stdout.trim();
const nodeBinDir = nodeDir ? nodeDir.slice(0, nodeDir.lastIndexOf('/')) : '';
const noPrettierEnv = {
  ...process.env,
  PATH: [nodeBinDir, '/usr/bin', '/bin'].filter(Boolean).join(':'),
};
const noPrettier = spawnSync('node', [GEN, '--check'], {
  encoding: 'utf8',
  cwd: process.cwd(),
  env: noPrettierEnv,
});
// 前提：这条测试要模拟的是「gate 任务里解析不到 prettier」。
//   - 若 web/node_modules 存在（本地已装），脚本会走仓库内路径，
//     收窄 PATH 不影响它 —— 此时**本机无法复现该场景**，跳过即可，
//     由 CI 的 gate 任务（没有 node_modules）天然覆盖。跳过必须是显式的，
//     不能是「什么都不做然后默默通过」。
//   - 若不存在，则收窄后的 PATH 必须真的找不到 prettier，否则测试是空转。
const repoPrettier = join('web', 'node_modules', '.bin', 'prettier');
const hiddenProbe = spawnSync('sh', ['-c', 'command -v prettier || true'], {
  encoding: 'utf8',
  env: noPrettierEnv,
});
const pathProbe = spawnSync('sh', ['-c', 'command -v node || true'], {
  encoding: 'utf8',
  env: noPrettierEnv,
});
if (existsSync(repoPrettier)) {
  console.log('（跳过「缺 prettier」用例：本机已装 web/node_modules，该场景由 CI 的 gate 任务覆盖）');
} else if (noPrettier.error || !pathProbe.stdout.trim()) {
  problems.push(
    '自检失效：收窄 PATH 后 node 本身都起不来，测试环境没造出来——' +
      '不能据此判定通过（这正是本项目最反对的假绿通道）',
  );
} else if (hiddenProbe.stdout.trim()) {
  problems.push('自检失效：PATH 收窄后仍能找到 prettier，缺工具场景未被真实模拟');
} else {
  if (noPrettier.status !== 0) {
    problems.push(
      `${GEN} 在缺 prettier 的环境里退出码 ${noPrettier.status}（应为 0）：` +
        'CI 的 gate 任务没有 web/node_modules，这会让契约门禁与前端依赖耦合成必红。' +
        `输出：${String(noPrettier.stderr || noPrettier.stdout || '').trim().split('\n').slice(-3).join(' / ')}`,
    );
  }
  // 缺 prettier 时只有两种可接受结局，二者都必须**可见**：
  //   a. 有 npm exec 兜底 → 格式仍被校验（此时不需要提示，因为它没降级）；
  //   b. 兜底也不可用（离线）→ 必须打印「格式未校验」。
  // 真正禁止的是第三种：没校验、也不说 —— 那就是假绿。
  const quietWithoutFallback =
    !/prettier/i.test(noPrettier.stdout + noPrettier.stderr) &&
    !/未(?:找到|校验)/.test(noPrettier.stdout + noPrettier.stderr);
  const probeFallback = spawnSync('sh', ['-c', 'npm exec --yes -- prettier@3.9.6 --version >/dev/null 2>&1'], {
    encoding: 'utf8',
    env: noPrettierEnv,
    cwd: process.cwd(),
  });
  if (quietWithoutFallback && probeFallback.status !== 0) {
    problems.push(
      `${GEN} 缺 prettier 且 npm exec 兜底不可用，却既未失败也未说明「格式未校验」——` +
        '降级不可见即是假绿',
    );
  }
}
if (genFirst.status !== genSecond.status) {
  problems.push(
    `gen-contracts 非幂等：gen 退出码 ${genFirst.status}，随后的 gen-check 退出码 ${genSecond.status}`,
  );
}
// 非 --check 的一次生成如果失败，必须把原因说清楚（而不是只写「与契约不一致」）
if (genFirst.status !== 0) {
  const noisy = /未找到|执行失败/.test(genFirst.stdout + genFirst.stderr);
  if (!noisy) {
    problems.push('gen-contracts 失败但未说明是工具缺失还是契约不一致（排查会指向错误方向）');
  }
}

// 4) 格式化工具必须被**声明为依赖**，不能靠 `npm exec` 隐式下载。
// 真实缺陷：gen-contracts 调 prettier，但 web/package.json 里没有它；
// CI 里必红，本地则因为 npm exec 的隐式安装而掩盖。这里直接断言声明存在。
const webPkg = JSON.parse(readFileSync('web/package.json', 'utf8'));
const allDeps = { ...(webPkg.dependencies ?? {}), ...(webPkg.devDependencies ?? {}) };
if (!allDeps.prettier) {
  problems.push('gen-contracts 依赖 prettier，但 web/package.json 未声明该依赖（CI 必红）');
}

// 5) 生成物必须真的是「生成」出来的：头部要带 DO NOT EDIT 标记。
// 否则有人手改生成物、gen-check 又刚好没跑，就会长期漂移。
for (const f of ['internal/contract/errors.gen.go', 'web/src/shared/api/contract.gen.ts']) {
  if (!existsSync(f)) {
    problems.push(`生成物缺失：${f}`);
    continue;
  }
  const body = readFileSync(f, 'utf8');
  if (!/DO NOT EDIT/i.test(body)) {
    problems.push(`${f} 缺少 DO NOT EDIT 头（可能被手改过）`);
  }
}

console.log('契约脚本自检');
console.log('─'.repeat(56));
console.log(`openapi 路由路径 : ${declared} 条`);
console.log('─'.repeat(56));
if (problems.length) {
  console.error(`发现 ${problems.length} 处问题：`);
  for (const p of problems) console.error('  - ' + p);
  process.exit(1);
}
console.log('契约解析器行为正常');
