/**
 * e2e 专用类型声明。
 *
 * `/e2e-mount.js` 是构建产物（由 web/e2e/mount.ts 在 E2E_MOUNT=1 时生成），
 * 磁盘上不存在对应源码路径，因此 tsc 无法解析它。声明在这里而不是用
 * `@ts-expect-error`：后者会在「模块真的不存在」时也保持沉默，
 * 把「产物没构建」这类基础设施问题变成运行时的 `undefined` 访问。
 */
declare module '/e2e-mount.js' {
  import type { parsePluginMessage } from '@/features/plugins/sandbox/protocol';
  import type { CanvasKernel } from '@/features/canvas/kernel';

  export const __IC__: {
    parsePluginMessage: typeof parsePluginMessage;
    CanvasKernel: typeof CanvasKernel;
    createKernel: (opts: {
      nodes?: Array<{ id: string; rect: { x: number; y: number; w: number; h: number } }>;
    }) => CanvasKernel;
    budget: Record<string, number>;
  };
}
