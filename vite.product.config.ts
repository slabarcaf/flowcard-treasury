import { defineConfig } from 'vite';
import gateway from './vite.browser-gateway.config.js';
export default defineConfig({
  ...gateway,
  build: { ...gateway.build, outDir: '.dev/dist-product' },
  server: { ...gateway.server, port: 5176, strictPort: true },
});
