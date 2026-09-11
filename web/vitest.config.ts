import { defineConfig } from 'vitest/config';
import { fileURLToPath, URL } from 'node:url';

export default defineConfig({
  resolve: {
    alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) },
  },
  test: {
    globals: true,
    environment: 'node',
    include: ['src/**/*.test.ts', 'src/**/*.test.tsx'],
    // 内核单测不需要 DOM；组件测试单独指定 jsdom
    environmentMatchGlobs: [['src/**/components/**', 'jsdom']],
  },
});
