/**
 * 事件归一化：Codex app-server / Claude Code CLI → IC Item 协议。
 *
 * 设计原则（这三条是踩过的坑，写在最前面）：
 *
 *  1. **未知事件必须保留而不是丢弃**。上游加一个新 item 类型时，
 *     静默丢掉会让用户看到「Agent 卡住了」，而日志里什么都没有。
 *     这里对未知类型保留为 error item 并带上原始 type，至少可诊断。
 *
 *  2. **itemId 必须稳定**。IC 侧的合并策略依赖 (turnId, itemId) 唯一，
 *     若同一逻辑事件两次上报产生不同 id，就会出现重复条目。
 *     因此 id 优先取上游 id，没有则用「turnId:seq:kind」的确定性组合。
 *
 *  3. **文本增量与最终文本不能同时上报**。同时上报会让 UI 出现重复文本，
 *     这里把 delta 合并成最终文本后只上报一次 completed（见 foldDeltas）。
 */

/** IC 的 ItemKind 枚举（与 internal/agent/model.go 保持一致）。 */
export const ItemKind = {
  AgentMessage: 'agent_message',
  Reasoning: 'reasoning',
  ToolCall: 'tool_call',
  ToolResult: 'tool_result',
  FileChange: 'file_change',
  Error: 'error',
};

/** 一次归一化所需的上下文。 */
function makeId(ctx, kind, upstreamId, seq) {
  if (upstreamId) return String(upstreamId);
  return `${ctx.turnId ?? 'turn'}:${seq}:${kind}`;
}

function pick(obj, ...keys) {
  for (const k of keys) {
    if (obj && obj[k] !== undefined && obj[k] !== null) return obj[k];
  }
  return undefined;
}

/**
 * 归一化 Codex app-server 的一条通知。
 * @param {object} ev 上游事件（{ method, params } 或已解包的对象）
 * @param {{turnId?: string}} ctx
 * @returns {{kind: string, itemId: string, payload: object} | null}
 */
export function normalizeCodex(ev, ctx = {}) {
  if (!ev || typeof ev !== 'object') return null;
  const method = ev.method ?? ev.type ?? '';
  const params = ev.params ?? ev;

  // 会话/轮次级事件：不产生 item，交由调用方处理
  if (/turn\.(started|completed)|thread\./.test(method)) return null;

  if (/error/i.test(method) || params.error) {
    const err = params.error ?? params;
    return {
      kind: ItemKind.Error,
      itemId: makeId(ctx, 'error', pick(err, 'id'), params.seq ?? 0),
      payload: { code: pick(err, 'code') ?? 'agent_error', message: String(pick(err, 'message') ?? err) },
    };
  }

  const item = params.item ?? params;
  const type = pick(item, 'type', 'itemType', 'kind') ?? '';
  const seq = params.seq ?? item.seq ?? 0;
  const upstreamId = pick(item, 'id', 'itemId');

  switch (type) {
    case 'message':
    case 'agent_message':
    case 'assistant_message':
      return {
        kind: ItemKind.AgentMessage,
        itemId: makeId(ctx, 'msg', upstreamId, seq),
        payload: { text: extractText(item), role: pick(item, 'role') ?? 'assistant' },
      };
    case 'reasoning':
    case 'thinking':
    case 'reasoning_summary':
      return {
        kind: ItemKind.Reasoning,
        itemId: makeId(ctx, 'reason', upstreamId, seq),
        payload: { text: extractText(item) },
      };
    case 'command_execution':
    case 'command':
    case 'exec':
    case 'shell':
      // 同一个 item 的 started/completed 归一化为两次上报：
      // started → tool_call（前端展示「正在执行」），completed → tool_result。
      if (/started|begin/i.test(method)) {
        return {
          kind: ItemKind.ToolCall,
          itemId: makeId(ctx, 'call', upstreamId, seq),
          payload: {
            callId: upstreamId ?? `${ctx.turnId ?? 'turn'}:${seq}`,
            // name 是「工具名」，command 是「参数」——两者不能混：
            // 把命令本身当工具名会让 UI 显示成一长串 shell 命令，无法按工具聚合。
            name: pick(item, 'name', 'tool', 'toolName') ?? 'shell',
            args: { command: pick(item, 'command', 'cmd') ?? '' },
          },
        };
      }
      return {
        kind: ItemKind.ToolResult,
        itemId: makeId(ctx, 'result', upstreamId, seq),
        payload: {
          callId: upstreamId ?? `${ctx.turnId ?? 'turn'}:${seq}`,
          status: pick(item, 'status') === 'failed' ? 'error' : 'ok',
          output: extractText(item) || String(pick(item, 'output', 'stdout') ?? ''),
          exitCode: pick(item, 'exitCode', 'exit_code'),
        },
      };
    case 'file_change':
    case 'patch':
    case 'edit':
      return {
        kind: ItemKind.FileChange,
        itemId: makeId(ctx, 'file', upstreamId, seq),
        payload: {
          path: pick(item, 'path', 'file') ?? '',
          // 只上报摘要而非整段 diff：diff 可能很大，且会进模型上下文
          summary: summarizeChanges(item),
        },
      };
    default:
      // 原则 1：未知类型保留（携带原始 type，便于定位上游变更）
      return {
        kind: ItemKind.Error,
        itemId: makeId(ctx, 'unknown', upstreamId, seq),
        payload: {
          code: 'unknown_upstream_event',
          message: `未识别的上游事件类型: ${type || method || '(empty)'}`,
          raw: safeRaw(item),
        },
      };
  }
}

/**
 * 归一化 Claude Code CLI 的 stream-json 一行。
 * @param {object} line
 * @param {{turnId?: string}} ctx
 */
export function normalizeClaude(line, ctx = {}) {
  if (!line || typeof line !== 'object') return null;
  const type = line.type ?? '';
  if (type === 'system' || type === 'result') {
    if (type === 'result' && line.is_error) {
      return {
        kind: ItemKind.Error,
        itemId: makeId(ctx, 'error', undefined, 0),
        payload: { code: 'agent_error', message: String(line.result ?? '执行失败') },
      };
    }
    return null;
  }
  const message = line.message ?? {};
  const blocks = Array.isArray(message.content) ? message.content : [];
  // 一个事件可能携带多个 content block：这里返回数组，由调用方逐条上报。
  const out = [];
  for (const [i, block] of blocks.entries()) {
    const id = block.id ?? `${ctx.turnId ?? 'turn'}:${type}:${i}`;
    switch (block.type) {
      case 'text':
        out.push({
          kind: ItemKind.AgentMessage,
          itemId: String(id),
          payload: { text: String(block.text ?? ''), role: message.role ?? 'assistant' },
        });
        break;
      case 'thinking':
        out.push({ kind: ItemKind.Reasoning, itemId: String(id), payload: { text: String(block.thinking ?? '') } });
        break;
      case 'tool_use':
        out.push({
          kind: ItemKind.ToolCall,
          itemId: String(id),
          payload: { callId: String(block.id ?? id), name: String(block.name ?? 'tool'), args: block.input ?? {} },
        });
        break;
      case 'tool_result':
        out.push({
          kind: ItemKind.ToolResult,
          itemId: String(id),
          payload: {
            callId: String(block.tool_use_id ?? id),
            status: block.is_error ? 'error' : 'ok',
            output: extractText(block),
          },
        });
        break;
      default:
        out.push({
          kind: ItemKind.Error,
          itemId: String(id),
          payload: {
            code: 'unknown_upstream_event',
            message: `未识别的 content block: ${block.type ?? '(empty)'}`,
            raw: safeRaw(block),
          },
        });
    }
  }
  return out;
}

/**
 * 把「增量 → 最终文本」折叠成一条 completed 上报。
 *
 * 为什么需要：Codex 会同时发 delta 与 completed。若两者都上报，
 * IC 侧会显示两份相同文本（一份流式 + 一份最终），用户以为模型重复回答了。
 * 折叠规则：以 itemId 为键累积 delta，收到 completed 时用最终文本覆盖并只发出一次。
 */
export function foldDeltas(events) {
  const acc = new Map();
  const out = [];
  for (const ev of events) {
    if (!ev) continue;
    if (ev.kind === 'delta') {
      const key = ev.itemId;
      const prev = acc.get(key) ?? { text: '', meta: ev };
      prev.text += ev.payload?.delta ?? '';
      acc.set(key, prev);
      continue;
    }
    if (ev.kind === ItemKind.AgentMessage) {
      const key = ev.itemId;
      if (acc.has(key)) {
        const merged = acc.get(key);
        acc.delete(key);
        out.push({ ...ev, payload: { ...ev.payload, text: ev.payload?.text || merged.text } });
        continue;
      }
    }
    out.push(ev);
  }
  // 剩余只有 delta 没有 completed 的：补一条最终消息（否则文本会丢）
  for (const [key, v] of acc) {
    out.push({
      kind: ItemKind.AgentMessage,
      itemId: key,
      payload: { text: v.text, role: 'assistant', incomplete: true },
    });
  }
  return out;
}

function extractText(item) {
  if (typeof item === 'string') return item;
  const raw = pick(item, 'text', 'content', 'output', 'message');
  if (typeof raw === 'string') return raw;
  if (Array.isArray(raw)) {
    return raw.map((b) => (typeof b === 'string' ? b : b?.text ?? '')).join('');
  }
  if (raw && typeof raw === 'object' && typeof raw.text === 'string') return raw.text;
  return '';
}

function summarizeChanges(item) {
  const changes = pick(item, 'changes', 'diff', 'patch');
  if (Array.isArray(changes)) return `${changes.length} 处改动`;
  if (typeof changes === 'string') {
    const added = (changes.match(/^\+/gm) ?? []).length;
    const removed = (changes.match(/^-/gm) ?? []).length;
    return `+${added} / -${removed} 行`;
  }
  return '';
}

/** 截断原始事件，避免把大 payload 带进日志与上下文。 */
function safeRaw(item) {
  try {
    const s = JSON.stringify(item);
    return s.length > 1000 ? s.slice(0, 1000) + '…' : s;
  } catch {
    return '(unserializable)';
  }
}
