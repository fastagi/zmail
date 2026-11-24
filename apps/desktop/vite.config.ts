import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

const rootDir = path.dirname(fileURLToPath(import.meta.url));

export default defineConfig({
  root: path.resolve(rootDir, 'src/renderer'),
  base: './',
  plugins: [react()],
  resolve: {
    alias: {
      '@shared': path.resolve(rootDir, 'src/shared'),
      '@zmail/mail-ui': path.resolve(rootDir, '../../packages/mail-ui/src/index.ts'),
    },
  },
  server: {
    host: '127.0.0.1',
    port: 5174,
    strictPort: true,
    fs: {
      allow: [rootDir, path.resolve(rootDir, '../../packages/mail-ui')],
    },
  },
  build: {
    outDir: path.resolve(rootDir, 'dist/renderer'),
    emptyOutDir: true,
    sourcemap: true,
    target: 'es2022',
  },
});
