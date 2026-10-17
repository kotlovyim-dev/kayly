import tailwindcss from '@tailwindcss/vite';
import react from '@vitejs/plugin-react';
import { defineConfig } from 'vite';

// `npm run dev` proxies the API to a local daemon started with --base-domain localhost.
export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    proxy: { '/api': { target: 'https://kayly.localhost', secure: false, changeOrigin: true } },
  },
});
