/**
 * E2E 专用挂载入口（仅 E2E_MOUNT=1 时构建为 `dist/e2e-mount.js`）。
 *
 * 为什么需要它：e2e 必须验证**仓库里的真实实现**，而不是在用例里重抄一份逻辑
 * （拷贝一份的话，实现改了用例不会红——测的是拷贝）。
 * dev server 下可以直接 `import('/src/...')`，但 e2e 打的是服务端同源托管的
 * **构建产物**，产物里没有源码路径。于是把这些实现以具名导出挂到 `window.__IC__`，
 * 并在 rollup 的 input 里把它作为一个固定命名的入口产物。
 *
 * 生产构建不含本文件（见 vite.config.ts 的 E2E_MOUNT 开关）。
 */
import {
  parsePluginMessage,
  requiredPermission,
  sandboxAttrs,
  pluginCSP,
} from '@/features/plugins/sandbox/protocol';
import { CanvasKernel } from '@/features/canvas/kernel';
import * as budget from '@/features/canvas/kernel/budget';
import type { CanvasDoc, RawNode, Rect } from '@/features/canvas/kernel';

/** 构造一个只含给定节点的空文档（性能用例需要它生成 5000 节点）。 */
function makeDoc(nodes: Array<{ id: string; rect: Rect }>): CanvasDoc {
  const raw: RawNode[] = nodes.map((n) => ({
    id: n.id,
    type: 'prompt',
    title: n.id,
    rect: n.rect,
    z: 0,
    ports: {
      inputs: [],
      outputs: [
        { id: 'out', name: '文本', kind: 'text', multiple: false, required: false, order: 0 },
      ],
    },
    spec: { text: n.id },
    state: 'idle',
  }));
  return {
    id: 'cv_e2e',
    projectId: 'pj_e2e',
    version: 0,
    viewport: { x: 0, y: 0, k: 1 },
    settings: {
      background: 'dots',
      imageInfo: true,
      gridSnap: false,
      readOnly: false,
      freeResize: false,
    },
    nodes: Object.fromEntries(raw.map((n) => [n.id, n])),
    edges: {},
    updatedAt: new Date().toISOString(),
  };
}

/**
 * 性能用例的统一构造入口。
 * 之所以由这里提供而不是让用例直接 `new CanvasKernel`：用例不该知道内核的构造
 * 签名，否则签名一变，用例会因为「构造方式不对」而红，掩盖真正的性能回归。
 */
function createKernel(opts: { nodes?: Array<{ id: string; rect: Rect }> }) {
  return new CanvasKernel(makeDoc(opts.nodes ?? []));
}

export const __IC__ = {
  parsePluginMessage,
  requiredPermission,
  sandboxAttrs,
  pluginCSP,
  CanvasKernel,
  createKernel,
  makeDoc,
  budget,
};

// 同时挂到 globalThis：e2e 里 `window.__IC__` 直接可用，无需依赖模块标识符
(globalThis as unknown as { __IC__: typeof __IC__ }).__IC__ = __IC__;
