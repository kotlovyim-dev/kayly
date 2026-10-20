/// <reference types="vitest/config" />
import { fileURLToPath } from 'node:url';
import tailwindcss from '@tailwindcss/vite';
import react from '@vitejs/plugin-react';
import { defineConfig } from 'vite';
import { mockApi } from './mock/api.ts';

// `npm run dev` proxies the API to a local daemon started with --base-domain localhost;
// `npm run dev:mock` answers it with sample data instead (mock/api.ts).
export default defineConfig(({ mode }) => ({
  plugins: [react(), tailwindcss(), mode === 'mock' && mockApi()],
  resolve: { alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) } },
  server:
    mode === 'mock'
      ? {}
      : { proxy: { '/api': { target: 'https://kayly.localhost', secure: false, changeOrigin: true } } },
  test: { environment: 'node' },
}));
