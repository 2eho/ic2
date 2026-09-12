import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import { fileURLToPath, URL } from 'node:url';

// 开发期把 /api 代理到本地 Go 服务，生产由 Go 服务同源托管静态产物（无 CORS）。
export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  build: {
    outDir: 'dist',
    sourcemap: true,
    target: 'es2022',
    rollupOptions: {
      // E2E 需要能 `import('/e2e-mount.js')` 拿到**仓库里的真实实现**
      // （协议校验、内核、预算常量），而不是在用例里重抄一份。
      // 只在 E2E_MOUNT=1 时加入，生产产物不含它（不会让用户多下一个字节）。
      input:
        process.env.E2E_MOUNT === '1'
          ? { index: 'index.html', 'e2e-mount': 'e2e/mount.ts' }
          : undefined,
      output: {
        manualChunks: {
          // 画布内核单独分包：主包不被内核体积拖累
          kernel: ['./src/features/canvas/kernel/index.ts'],
        },
        // 固定文件名：e2e 用例要按稳定路径 import，哈希名会让用例与产物耦合
        entryFileNames: (chunk) =>
          chunk.name === 'e2e-mount' ? 'e2e-mount.js' : 'assets/[name]-[hash].js',
      },
    },
  },
  server: {
    port: 5173,
    proxy: {
      '/api': { target: 'http://localhost:8080', changeOrigin: true },
      '/healthz': { target: 'http://localhost:8080', changeOrigin: true },
    },
  },
});
