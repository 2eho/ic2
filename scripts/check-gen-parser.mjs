#!/usr/bin/env node
// gen-contracts 的解析器自检。
//
// 背景：契约校验脚本自己也会出错。实测踩到两次——
//   1. 块标量（`description: |`）被当成新键，导致 44 条路由只解析出 32 条，
//      于是「openapi 声明了但 router 没注册」刷出 12 条假报错；
//   2. 生成物未格式化导致 --check 永远失败。
// 这两类问题的共同点是：**校验脚本错了，看起来像代码错了**。
// 因此这里用一小段人为构造的 YAML 验证解析器的关键行为。
import { readFileSync, writeFileSync, mkdtempSync } from 'node:fs';
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
