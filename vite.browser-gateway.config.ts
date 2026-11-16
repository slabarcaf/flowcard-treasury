import { ServerResponse } from 'node:http';
import react from '@vitejs/plugin-react';
import tailwindcss from '@tailwindcss/vite';
import { defineConfig } from 'vite';
import { privateRuntimeFileAccess } from './vite.private-files.js';

// Dedicated opt-in preview. Never modifies the existing Vite/Node target.
const target = process.env.BROWSER_GATEWAY_DEV_TARGET ?? 'http://127.0.0.1:8094';
const parsed = new URL(target);
if (parsed.protocol !== 'http:' || !['127.0.0.1', '[::1]', 'localhost'].includes(parsed.hostname) ||
    parsed.username || parsed.password || parsed.pathname !== '/' || parsed.search || parsed.hash) {
  throw new Error('Browser gateway development target must be a loopback HTTP origin');
}
if (process.env.VITE_BROWSER_GATEWAY_ENABLED !== 'true') throw new Error('Dedicated gateway preview requires VITE_BROWSER_GATEWAY_ENABLED=true');

export default defineConfig({
  plugins: [react(), tailwindcss()],
  build: { outDir: '.dev/dist-browser-gateway' },
  server: {
    fs: privateRuntimeFileAccess(),
    host: '127.0.0.1', port: 5175, strictPort: true,
    proxy: {
      '/api': {
        target, changeOrigin: false, proxyTimeout: 35_000, timeout: 35_000,
        configure(proxy) {
          proxy.prependListener('error', (_error, _request, response) => {
            if (response instanceof ServerResponse && !response.headersSent) {
              response.writeHead(503, { 'content-type': 'application/json', 'cache-control': 'no-store' });
              response.end(JSON.stringify({ error: 'gateway unavailable' }));
            }
          });
        },
      },
    },
  },
});
