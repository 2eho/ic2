import { test } from 'node:test';
import assert from 'node:assert/strict';
import { ItemKind, foldDeltas, normalizeClaude, normalizeCodex } from './normalize.js';

test('Codex：消息事件归一化为 agent_message', () => {
  const ev = normalizeCodex({ method: 'item.completed', params: { seq: 3, item: { id: 'i1', type: 'message', text: '你好' } } }, { turnId: 't1' });
  assert.equal(ev.kind, ItemKind.AgentMessage);
  assert.equal(ev.itemId, 'i1');
  assert.equal(ev.payload.text, '你好');
});

test('Codex：思考事件归一化为 reasoning', () => {
  const ev = normalizeCodex({ method: 'item.completed', params: { item: { id: 'i2', type: 'reasoning', text: '想一下' } } });
  assert.equal(ev.kind, ItemKind.Reasoning);
});

test('Codex：命令 started/completed 拆成 tool_call + tool_result', () => {
  const started = normalizeCodex({ method: 'item.started', params: { seq: 1, item: { id: 'c1', type: 'command_execution', command: 'ls' } } });
  assert.equal(started.kind, ItemKind.ToolCall);
  assert.equal(started.payload.name, 'shell');

  const done = normalizeCodex({ method: 'item.completed', params: { seq: 2, item: { id: 'c1', type: 'command_execution', status: 'completed', output: 'a.txt' } } });
  assert.equal(done.kind, ItemKind.ToolResult);
  assert.equal(done.payload.status, 'ok');
  assert.equal(done.payload.output, 'a.txt');
});

test('Codex：失败的命令 status=error', () => {
  const done = normalizeCodex({ method: 'item.completed', params: { item: { id: 'c2', type: 'command_execution', status: 'failed', exit_code: 1 } } });
  assert.equal(done.payload.status, 'error');
  assert.equal(done.payload.exitCode, 1);
});

test('Codex：未知类型必须保留而不是丢弃', () => {
  const ev = normalizeCodex({ method: 'item.completed', params: { item: { id: 'x1', type: 'brand_new_type', foo: 'bar' } } });
  assert.equal(ev.kind, ItemKind.Error);
  assert.equal(ev.payload.code, 'unknown_upstream_event');
  assert.match(ev.payload.message, /brand_new_type/);
  // 原始事件要保留，便于定位上游变更
  assert.match(ev.payload.raw, /brand_new_type/);
});

test('Codex：会话级事件不产生 item', () => {
  assert.equal(normalizeCodex({ method: 'turn.started', params: {} }), null);
  assert.equal(normalizeCodex({ method: 'thread.updated', params: {} }), null);
});

test('Codex：itemId 在缺少上游 id 时必须是确定性的', () => {
  const a = normalizeCodex({ method: 'item.completed', params: { seq: 7, item: { type: 'message', text: 'x' } } }, { turnId: 't9' });
  const b = normalizeCodex({ method: 'item.completed', params: { seq: 7, item: { type: 'message', text: 'x' } } }, { turnId: 't9' });
  assert.equal(a.itemId, b.itemId, '同一事件两次归一化必须得到同一 id（否则会重复上报）');
  assert.match(a.itemId, /t9:7:/);
});

test('Codex：错误事件归一化为 error', () => {
  const ev = normalizeCodex({ method: 'error', params: { error: { code: 'rate_limit', message: 'too many' } } });
  assert.equal(ev.kind, ItemKind.Error);
  assert.equal(ev.payload.code, 'rate_limit');
});

test('Claude：text/thinking/tool_use/tool_result 四类 block', () => {
  const events = normalizeClaude({
    type: 'assistant',
    message: {
      role: 'assistant',
      content: [
        { type: 'text', text: '好的' },
        { type: 'thinking', thinking: '思考中' },
        { type: 'tool_use', id: 'tu1', name: 'Read', input: { path: '/a' } },
        { type: 'tool_result', tool_use_id: 'tu1', content: 'file content' },
      ],
    },
  }, { turnId: 't2' });
  assert.equal(events.length, 4);
  assert.deepEqual(events.map((e) => e.kind), [
    ItemKind.AgentMessage, ItemKind.Reasoning, ItemKind.ToolCall, ItemKind.ToolResult,
  ]);
  assert.equal(events[2].payload.name, 'Read');
  assert.equal(events[3].payload.callId, 'tu1');
});

test('Claude：未知 block 保留', () => {
  const events = normalizeClaude({ type: 'assistant', message: { content: [{ type: 'mystery' }] } });
  assert.equal(events[0].kind, ItemKind.Error);
  assert.match(events[0].payload.message, /mystery/);
});

test('Claude：is_error 的 result 变成 error', () => {
  const ev = normalizeClaude({ type: 'result', is_error: true, result: '炸了' });
  assert.equal(ev.kind, ItemKind.Error);
  assert.equal(ev.payload.message, '炸了');
});

test('foldDeltas：delta + completed 只产出一条消息（不重复）', () => {
  const folded = foldDeltas([
    { kind: 'delta', itemId: 'm1', payload: { delta: '你' } },
    { kind: 'delta', itemId: 'm1', payload: { delta: '好' } },
    { kind: ItemKind.AgentMessage, itemId: 'm1', payload: { text: '你好' } },
  ]);
  assert.equal(folded.length, 1);
  assert.equal(folded[0].payload.text, '你好');
});

test('foldDeltas：只有 delta 时要补一条最终消息（不能丢文本）', () => {
  const folded = foldDeltas([
    { kind: 'delta', itemId: 'm2', payload: { delta: '半' } },
    { kind: 'delta', itemId: 'm2', payload: { delta: '句' } },
  ]);
  assert.equal(folded.length, 1);
  assert.equal(folded[0].payload.text, '半句');
  assert.equal(folded[0].payload.incomplete, true, '必须标记不完整，让 UI 能提示');
});

test('foldDeltas：不同 itemId 的 delta 互不干扰', () => {
  const folded = foldDeltas([
    { kind: 'delta', itemId: 'a', payload: { delta: 'A' } },
    { kind: 'delta', itemId: 'b', payload: { delta: 'B' } },
  ]);
  assert.equal(folded.length, 2);
  assert.deepEqual(folded.map((f) => f.payload.text).sort(), ['A', 'B']);
});
