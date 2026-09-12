#!/usr/bin/env node
// 前后端节点 schema 一致性检查。
//
// 真源：internal/graph/spec.go（服务端）。前端 web/src/features/canvas/kernel/schema.ts
// 必须与之一致，否则会出现「前端能建、后端拒绝」的错位。
import { readFileSync } from 'node:fs';

const GO_FILE = 'internal/graph/spec.go';
const TS_FILE = 'web/src/features/canvas/kernel/schema.ts';

const go = readFileSync(GO_FILE, 'utf8');
const ts = readFileSync(TS_FILE, 'utf8');

const problems = [];

// 1) 内置节点类型必须两边都有
const goTypes = new Set();
for (const m of go.matchAll(/NodeType(\w+):\s*\{/g)) {
  if (m[1] !== 'ID') goTypes.add(m[1].toLowerCase());
}
// 只取 NODE_SCHEMAS 对象内部的键，避免把其他同名层级对象误认为节点类型。
const tsTypes = new Set();
const schemasStart = ts.indexOf('NODE_SCHEMAS');
if (schemasStart < 0) {
  problems.push('前端 schema.ts 中未找到 NODE_SCHEMAS');
} else {
  const body = ts.slice(schemasStart);
  for (const m of body.matchAll(/^ {2}(\w+):\s*\{/gm)) {
    tsTypes.add(m[1]);
  }
}

for (const t of goTypes) {
  if (!tsTypes.has(t)) problems.push(`前端 schema.ts 缺少节点类型 ${t}`);
}
for (const t of tsTypes) {
  if (!goTypes.has(t)) problems.push(`前端 schema.ts 多出节点类型 ${t}（服务端没有）`);
}

// 2) 端口 id 必须一致（逐类型对比）
const goNodes = parseGoSchemas(go);
const tsNodes = parseTsSchemas(ts);
for (const [type, goNode] of Object.entries(goNodes)) {
  const tsNode = tsNodes[type];
  if (!tsNode) continue;
  const goPorts = new Set([...goNode.inputs, ...goNode.outputs]);
  const tsPorts = new Set([...tsNode.inputs, ...tsNode.outputs]);
  for (const p of goPorts) {
    if (!tsPorts.has(p)) problems.push(`${type}: 前端缺少端口 ${p}`);
  }
  for (const p of tsPorts) {
    if (!goPorts.has(p)) problems.push(`${type}: 前端多出端口 ${p}`);
  }
}

function parseGoSchemas(src) {
  const out = {};
  const parts = src.split(/\n\tNodeType/);
  for (const part of parts) {
    const head = part.match(/^(\w+):\s*\{/);
    if (!head) continue;
    const type = head[1].toLowerCase();

    // 取 Ports{ ... } 的内容：从 "Ports: Ports{" 起，按花括号配平截取。
    const startIdx = part.indexOf('Ports: Ports{');
    let body = '';
    if (startIdx >= 0) {
      let depth = 0;
      let began = false;
      for (let i = startIdx + 'Ports: Ports'.length; i < part.length; i++) {
        const ch = part[i];
        if (ch === '{') {
          depth++;
          if (began) body += ch;
          began = true;
          continue;
        }
        if (ch === '}') {
          depth--;
          if (began && depth === 0) break;
          continue;
        }
        if (began) body += ch;
      }
    }

    const outIdx = body.indexOf('Outputs:');
    const inSection = outIdx >= 0 ? body.slice(0, outIdx) : body;
    const outSection = outIdx >= 0 ? body.slice(outIdx) : '';
    out[type] = {
      inputs: [...inSection.matchAll(/port\("([\w-]+)"/g)].map((m) => m[1]),
      outputs: [...outSection.matchAll(/port\("([\w-]+)"/g)].map((m) => m[1]),
    };
  }
  return out;
}

function parseTsSchemas(src) {
  const out = {};
  const start = src.indexOf('NODE_SCHEMAS');
  const scoped = start >= 0 ? src.slice(start) : src;
  const blocks = scoped.split(/\n  (\w+):\s*\{/);
  for (let i = 1; i < blocks.length; i += 2) {
    const type = blocks[i];
    const body = blocks[i + 1] ?? '';

    // 取 ports: { ... } 的内容（按花括号配平，兼容单行与多行）
    let portsBody = '';
    const startIdx = body.indexOf('ports: {');
    if (startIdx >= 0) {
      let depth = 0;
      let began = false;
      for (let j = startIdx + 'ports: '.length; j < body.length; j++) {
        const ch = body[j];
        if (ch === '{') {
          depth++;
          if (began) portsBody += ch;
          began = true;
          continue;
        }
        if (ch === '}') {
          depth--;
          if (began && depth === 0) break;
          continue;
        }
        if (began) portsBody += ch;
      }
    }

    const outIdx = portsBody.indexOf('outputs:');
    const inSection = outIdx >= 0 ? portsBody.slice(0, outIdx) : portsBody;
    const outSection = outIdx >= 0 ? portsBody.slice(outIdx) : '';
    // 兼容单引号与双引号（prettier 的 quote 配置会改变这一处，
    // 而解析器不应该因为格式化风格变化就误报「端口缺失」——那是假阳性，
    // 会让真正的 schema 漂移被淹没在噪音里）。
    const portPattern = /P\(\s*['"]([\w-]+)['"]/g;
    out[type] = {
      inputs: [...inSection.matchAll(portPattern)].map((m) => m[1]),
      outputs: [...outSection.matchAll(portPattern)].map((m) => m[1]),
    };
  }
  return out;
}

console.log('节点 schema 前后端一致性检查');
console.log('─'.repeat(52));
console.log(`服务端类型: ${[...goTypes].sort().join(', ')}`);
console.log(`前端类型  : ${[...tsTypes].sort().join(', ')}`);
if (problems.length) {
  console.error(`发现 ${problems.length} 处不一致：`);
  for (const p of problems) console.error('  - ' + p);
  process.exit(1);
}
console.log('一致');
