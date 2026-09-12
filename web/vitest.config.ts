import { defineConfig } from 'vitest/config';
import { fileURLToPath, URL } from 'node:url';

/**
 * 前端单测配置。
 *
 * 环境划分（不是随便选的，每一类都有理由）：
 *   - kernel/ 与 tools/pure：纯计算，用 node 环境（更快，且能反证「内核不碰 DOM」）；
 *   - shared/client、shared/session、features/**：涉及 sessionStorage/localStorage/fetch，
 *     必须用 jsdom，否则测的是一堆 `if (typeof window !== 'undefined')` 的分支而不是真实行为。
 *
 * 上一轮的写法用 environmentMatchGlobs 按目录猜环境，结果是「新建一个目录忘了加规则」
 * 就会用错环境，报出 `sessionStorage is not defined` 这种与代码无关的失败。
 * 现在改成**默认 jsdom**、显式列出纯 node 的例外——默认值应该是「能测真实行为」的那个。
 */
export default defineConfig({
  resolve: {
    alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) },
  },
  test: {
    globals: true,
    environment: 'jsdom',
    include: ['src/**/*.test.ts', 'src/**/*.test.tsx'],
    environmentMatchGlobs: [
      ['src/features/canvas/kernel/**', 'node'],
      ['src/features/canvas/tools/**', 'node'],
    ],
    setupFiles: ['./src/test-setup.ts'],
  },
});
