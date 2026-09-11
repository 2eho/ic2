import type { Ports } from './types';

/**
 * 前端侧的内置节点 schema。
 *
 * 真源在服务端 `internal/graph/spec.go`；这里保留一份用于「离线创建节点」与
 * 渲染默认尺寸。两者一致性由 `scripts/check-node-schema.mjs` 在 CI 校验，
 * 避免出现「前端能建、后端拒绝」的错位。
 */
export interface NodeSchema {
  title: string;
  w: number;
  h: number;
  ports: Ports;
  spec: Record<string, unknown>;
  minimapColor: string;
}

const P = (
  id: string,
  name: string,
  kind: Ports['inputs'][number]['kind'],
  multiple: boolean,
  required: boolean,
  order: number,
) => ({ id, name, kind, multiple, required, order });

export const NODE_SCHEMAS: Record<string, NodeSchema> = {
  prompt: {
    title: '提示词',
    w: 320,
    h: 220,
    minimapColor: '#7dd3fc',
    ports: { inputs: [], outputs: [P('out', '文本', 'text', false, false, 0)] },
    spec: { text: '' },
  },
  image: {
    title: '图片',
    w: 320,
    h: 320,
    minimapColor: '#a5b4fc',
    ports: {
      inputs: [P('in', '输入', 'image', true, false, 0)],
      outputs: [P('out', '图片', 'image', false, false, 0)],
    },
    spec: { fit: 'cover' },
  },
  video: {
    title: '视频',
    w: 400,
    h: 260,
    minimapColor: '#fca5a5',
    ports: {
      inputs: [P('in', '输入', 'video', true, false, 0)],
      outputs: [P('out', '视频', 'video', false, false, 0)],
    },
    spec: { loop: true, controls: true },
  },
  audio: {
    title: '音频',
    w: 360,
    h: 140,
    minimapColor: '#fcd34d',
    ports: {
      inputs: [P('in', '输入', 'audio', true, false, 0)],
      outputs: [P('out', '音频', 'audio', false, false, 0)],
    },
    spec: {},
  },
  group: {
    title: '分组',
    w: 480,
    h: 320,
    minimapColor: '#cbd5e1',
    ports: { inputs: [], outputs: [] },
    spec: { collapsed: false },
  },
  generation: {
    title: '生成',
    w: 340,
    h: 260,
    minimapColor: '#86efac',
    ports: {
      inputs: [P('prompt', '提示词', 'text', true, false, 0), P('ref', '参考图', 'image', true, false, 1)],
      outputs: [P('out', '结果', 'image', false, false, 0), P('text', '文本', 'text', false, false, 1)],
    },
    spec: { capability: 'image.generate', outputCount: 1 },
  },
  run: {
    title: '运行',
    w: 280,
    h: 160,
    minimapColor: '#f0abfc',
    ports: { inputs: [], outputs: [P('out', '结果', 'json', false, false, 0)] },
    spec: {},
  },
};

export function defaultSchemaFor(type: string): NodeSchema | null {
  return NODE_SCHEMAS[type] ?? null;
}

const ALPHABET = 'abcdefghijklmnopqrstuvwxyz0123456789';

/** 生成客户端 ID。服务端会做格式校验（^[A-Za-z0-9_.:-]{1,64}$）。 */
export function newLocalID(prefix: string): string {
  let rand = '';
  if (typeof crypto !== 'undefined' && 'getRandomValues' in crypto) {
    const buf = new Uint8Array(8);
    crypto.getRandomValues(buf);
    rand = Array.from(buf, (b) => ALPHABET[b % ALPHABET.length]).join('');
  } else {
    for (let i = 0; i < 8; i++) rand += ALPHABET[Math.floor(Math.random() * ALPHABET.length)];
  }
  return `${prefix}_${Date.now().toString(36)}${rand}`;
}
