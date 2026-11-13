import { ServerResponse } from 'node:http'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import { configDefaults, defineConfig } from 'vitest/config'

export default defineConfig({
  plugins: [react(), tailwindcss()],
  build: { outDir: '.dev/dist' },
  server: {
    allowedHosts: ['unsoulful-observantly-ahmed.ngrok-free.dev'],
    proxy: {
      '/api': {
        target: 'http://127.0.0.1:8000',
        changeOrigin: true,
        configure(proxy) {
          proxy.on('proxyReq', (outgoing, request) => {
            // Same-origin browser GETs may omit Origin. Forward their verified local
            // page origin so the existing gateway's exact-origin check still works.
            if (!request.headers.origin && request.headers['sec-fetch-site'] === 'same-origin' && request.headers.referer) {
              try {
                const origin = new URL(request.headers.referer).origin;
                if (origin === `http://${request.headers.host}`) outgoing.setHeader('origin', origin);
              } catch { /* Invalid referrer is never trusted. */ }
            }
          });
          proxy.prependListener('error', (error, request, response) => {
            // ECONNREFUSED means the gateway never accepted the request: retry is safe.
            // Do not make this claim for a timeout/disconnect after a write.
            if ('code' in error && error.code === 'ECONNREFUSED' &&
                /^\/api\/(companies|onboarding-applications|onboarding-reference)(?:\/|$)/.test(request.url ?? '') &&
                response instanceof ServerResponse && !response.headersSent) {
              response.writeHead(503, { 'content-type': 'application/json', 'cache-control': 'no-store' });
              response.end(JSON.stringify({ code: 'ONBOARDING_GATEWAY_UNREACHABLE' }));
            }
          });
        },
      },
    },
  },
  test: {
    globals: true,
    exclude: [...configDefaults.exclude, '**/worktrees/**', '**/.worktrees/**'],
  },
})
