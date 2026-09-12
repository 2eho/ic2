#!/usr/bin/env node
// scripts/ci-exec.mjs 的自测。
//
// 为什么需要（PR #2 第三轮 CI 实红）：平台的 YAML 解析会把 stage 的块标量
// 按 C 风格转义处理，多行命令被压成一条，argv 随之串位。修法是把命令行拆分
// 下沉到 ci-exec，但**拆分逻辑本身**必须被验证——否则下一次它静默错了，
// 表现出来又是「CI 里某条命令莫名收到多余参数」，很难定位。
//
// 用 node:test 跑：`node --test scripts/ci-exec.test.mjs`
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { execFileSync, execSync } from 'node:child_process';
import { mkdtempSync, rmSync, symlinkSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { splitArgs } from './ci-exec.mjs';

/** 在 PATH 里定位一个可执行文件的绝对路径（测试自建「精简 PATH」用）。 */
function whichSync(bin) {
  try {
    return execSync(`command -v ${bin}`, { encoding: 'utf8', shell: '/bin/sh' }).trim() || null;
  } catch {
    return null;
  }
}

const CI_EXEC = 'scripts/ci-exec.mjs';

/** 直接调用 CLI，返回 { code, stdout, stderr }，不抛异常。 */
function cli(args) {
  return cliEnv(args);
}

/** 同上，但可注入环境变量（用于把 PATH 收窄，模拟「镜像里没这个工具」）。 */
function cliEnv(args, env) {
  try {
    const stdout = execFileSync(process.execPath, [CI_EXEC, ...args], {
      encoding: 'utf8',
      stdio: ['ignore', 'pipe', 'pipe'],
      env,
    });
    return { code: 0, stdout, stderr: '' };
  } catch (err) {
    return { code: err.status ?? 1, stdout: err.stdout?.toString() ?? '', stderr: err.stderr?.toString() ?? '' };
  }
}

test('splitArgs：按空白拆词', () => {
  assert.deepEqual(splitArgs('go version'), ['go', 'version']);
  assert.deepEqual(splitArgs('  make   lint  '), ['make', 'lint']);
});

test('splitArgs：一串按空格拼接的命令保持成一个 argv（平台压行的场景）', () => {
  // 平台把两行合成一行后，argv 会变成 ['go','version','node','-v']；
  // go 因此报 "version: node: no such file"。这里断言不会把词再拆错。
  assert.deepEqual(splitArgs('go version node -v'), ['go', 'version', 'node', '-v']);
});

test('splitArgs：引号内整体成词', () => {
  assert.deepEqual(splitArgs('echo "a b"'), ['echo', 'a b']);
  assert.deepEqual(splitArgs("echo 'a b'"), ['echo', 'a b']);
});

test('splitArgs：引号未闭合要报错，不能静默吞掉', () => {
  assert.throws(() => splitArgs('echo "a b'), /引号未闭合/);
});

test('run：单字符串形式解析出正确命令', () => {
  const r = cli(['run', 'node -v']);
  assert.equal(r.code, 0);
  assert.match(r.stdout, /^v\d+\./m);
});

test('run：`run -- <cmd>` 传统形式仍然可用', () => {
  const r = cli(['run', '--', 'node', '-v']);
  assert.equal(r.code, 0);
  assert.match(r.stdout, /^v\d+\./m);
});

test('run：拒绝 shell 连接符，避免开出新的假绿通道', () => {
  const r = cli(['run', 'make lint && make sec']);
  assert.equal(r.code, 2);
  assert.match(r.stderr, /不支持/);
});

test('probe：已知工具输出绝对路径且成功', () => {
  const r = cli(['probe', 'node']);
  assert.equal(r.code, 0);
  assert.match(r.stdout, /^node=\//m);
});

test('probe：缺工具必须以失败收场（不做跳过）', () => {
  const r = cli(['probe', 'definitely-not-a-real-tool-xyz']);
  assert.notEqual(r.code, 0);
  assert.match(r.stderr, /镜像里不存在/);
});

test('make 目标按实际依赖校验工具，不统一要求 go', () => {
  // `make parity` 只跑 node 脚本。若它把 go 也算成必需，门禁会被误判成环境故障，
  // 而误报会让人开始怀疑并绕过门禁。这里断言输出里**不出现 go 缺失**。
  // （本沙箱没有 make，所以只看缺失清单，不看退出码。）
  const r = cli(['run', '--', 'make', 'parity']);
  assert.doesNotMatch(r.stderr, /- go 缺失/, 'parity 不应要求 go');
  assert.doesNotMatch(r.stderr, /- gofmt 缺失/, 'parity 不应要求 gofmt');
});

test('需要 Go 的目标仍会要求 go（避免漏查）', () => {
  // 断言「该目标被推导为需要 go」，而不是「这台机器上缺 go」。
  // CI 的 gate 现在是 node 镜像 + scripts/install-go.sh 自装 Go，
  // 环境里**有** go，因此按「stderr 里出现 go 缺失」去断言会在 CI 上反向失败——
  // 它测的其实是沙箱环境，不是被检对象。探针输出 go=<绝对路径> 才是在问
  // 「这个目标要不要 go」，与环境无关。
  const r = cli(['probe', 'go']);
  assert.equal(r.code, 0, 'gate 任务的镜像必须能提供 go（由 scripts/install-go.sh 保证）');
  assert.match(r.stdout, /^go=\//m, 'go 必须以绝对路径可定位，否则 ci-exec 的工具链自检会误判为缺失');

  // 反向（与环境无关）：make 目标的工具需求来自 MAKE_TOOLSETS 这张表，
  // 表里没有的目标保守要求 go + gofmt + node + npm。
  //
  // 不能用「stderr 里出现 go 缺失」来断言：本机装了 go 时这句话根本不会出现，
  // 测的就变成沙箱环境了。做法是造一个「只有基础 shell 工具」的 PATH——
  // 注意**只收窄 PATH 不行**：(a) ci-exec 自身、make 都要能找到；
  // (b) 更要紧的是，于是根本进不到自检那一步，报错是「make 没有这个目标」；
  // (c) /usr/local/bin 里的 go 会跟着 node 一起被放行，「少了 go」这件事测不出来。
  // 因此显式建一个只软链 node/npm/npx/make/sh 的目录当 PATH。
  const bare = mkdtempSync(join(tmpdir(), 'ic-bare-'));
  try {
    for (const bin of ['node', 'npm', 'npx', 'make', 'sh', 'bash']) {
      const found = whichSync(bin);
      if (found) symlinkSync(found, join(bare, bin));
    }
    const strict = cliEnv(['run', '--', 'make', 'definitely-not-a-target-xyz'], {
      ...process.env,
      PATH: bare,
    });
    for (const tool of ['go', 'gofmt']) {
      assert.match(
        strict.stderr,
        new RegExp(`- ${tool} 缺失`),
        `未知 make 目标必须按最严格工具集校验（缺少 ${tool} 的检查＝漏查）`,
      );
    }
    assert.equal(strict.code, 127, '缺工具必须以 127 失败，不能降级成「跑起来再说」');
  } finally {
    rmSync(bare, { recursive: true, force: true });
  }
});
