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
      output: {
        manualChunks: {
          // 画布内核单独分包：主包不被内核体积拖累
          kernel: ['./src/features/canvas/kernel/index.ts'],
        },
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
