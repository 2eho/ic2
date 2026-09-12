#!/usr/bin/env node
// scripts/check-cnb-config.mjs 的**反向验证**。
//
// 为什么需要：一个只会「对当前文件说 OK」的校验脚本没有价值——必须证明它在
// 遇到坏写法时**真的会红**。CI 上已经先后被「块标量换行被压平」「引号被改写」
// 「漏查工具链」坑过三次，所以这里把每种坏写法固化成用例。
//
// 做法：把 .cnb.yml 临时改成坏写法，跑校验，断言退出码非 0，然后还原。
import { test, before, after } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, writeFileSync } from 'node:fs';
import { execFileSync } from 'node:child_process';

const CNB = '.cnb.yml';
let original;

before(() => {
  original = readFileSync(CNB, 'utf8');
});
after(() => {
  writeFileSync(CNB, original);
});

/** 跑校验，返回退出码。 */
function runChecker() {
  try {
    execFileSync(process.execPath, ['scripts/check-cnb-config.mjs'], { encoding: 'utf8', stdio: 'pipe' });
    return 0;
  } catch (err) {
    return err.status ?? 1;
  }
}

/** 用 mutator 改坏配置，断言校验失败。 */
function expectRejected(name, mutate) {
  test(`反向验证：${name}`, () => {
    const broken = mutate(original);
    // mutate 必须**真的改动了文件**。否则当替换目标在 .cnb.yml 里被改名/删除时，
    // mutate 原样返回，校验当然通过，而我们看到的失败信息会是「坏写法未被拦截」——
    // 把「测试自己失效」误报成「校验脚本漏报」。
    assert.notEqual(broken, original, `坏写法注入失败（替换目标在 ${CNB} 里找不到）：${name}`);
    writeFileSync(CNB, broken);
    const code = runChecker();
    writeFileSync(CNB, original);
    assert.notEqual(code, 0, `坏写法未被拦截：${name}`);
  });
}

test('基线：当前 .cnb.yml 必须通过', () => {
  assert.equal(runChecker(), 0);
});

expectRejected('门禁 stage 用块标量（多行会被平台压平）', (y) =>
  y.replace('script: node scripts/ci-exec.mjs run -- make fmt-check', 'script: |\n      node scripts/ci-exec.mjs run -- make fmt-check'),
);

expectRejected('门禁命令含引号（会被平台标量解析改写）', (y) =>
  y.replace('script: node scripts/ci-exec.mjs run -- make parity', "script: node scripts/ci-exec.mjs run 'make parity'"),
);

expectRejected('门禁命令含反斜杠（会被 C 风格转义吃掉）', (y) =>
  y.replace('script: node scripts/ci-exec.mjs probe go gofmt node npm', 'script: node scripts/ci-exec.mjs probe go\\ gofmt'),
);

expectRejected('门禁用 `|| true` 兜底（把红变绿）', (y) =>
  y.replace('script: node scripts/ci-exec.mjs run -- make sec', 'script: node scripts/ci-exec.mjs run -- make sec || true'),
);

expectRejected('绕过 ci-exec 直接调 make（严格模式与工具链自检同时失效）', (y) =>
  y.replace('script: node scripts/ci-exec.mjs run -- make drill', 'script: make drill'),
);

expectRejected('引用不存在的 make 目标', (y) =>
  y.replace('script: node scripts/ci-exec.mjs run -- make adversary', 'script: node scripts/ci-exec.mjs run -- make adversari'),
);

expectRejected('分支下出现非事件名键（曾是整条 gate 不生效的原因）', (y) =>
  y.replace('  push:', '  "**":'),
);

expectRejected('引用未定义的锚点（锚点名含连字符时曾被截断而漏报）', (y) =>
  y.replace('*web-stages', '*web-stages-typo'),
);

expectRejected('契约/静态检查之前没装前端依赖（会被伪装成「代码与契约不一致」）', (y) =>
  y.replace(
    '        - name: 前端依赖（契约与静态检查前置）\n' +
      '          script: node scripts/ci-exec.mjs run -- npm install --no-audit --no-fund --prefix web\n',
    '',
    1,
  ),
);
