import { mkdtemp, mkdir, rm, writeFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { join, resolve } from 'node:path';
import { createServer } from 'vite';
import { expect, it, vi } from 'vitest';

const root = fileURLToPath(new URL('../../', import.meta.url));

it.each(['vite.config.ts', 'vite.browser-gateway.config.ts', 'vite.product.config.ts'])(
  '%s denies private runtime files without blocking worktree app source',
  async (config) => {
    vi.stubEnv('VITE_BROWSER_GATEWAY_ENABLED', 'true');
    const dirs: string[] = [];
    let server: Awaited<ReturnType<typeof createServer>> | undefined;
    try {
      for (const parent of ['.dev', '.worktrees']) {
        await mkdir(join(root, parent), { recursive: true });
        dirs.push(await mkdtemp(join(root, parent, 'vite-boundary-')));
      }
      for (const dir of dirs) {
        await writeFile(join(dir, 'runtime.json'), '{"private_marker":"fixture-private-runtime-value"}');
      }
      server = await createServer({
        root,
        configFile: resolve(root, config),
        logLevel: 'silent',
        cacheDir: join(dirs[0], 'cache'),
        server: { host: '127.0.0.1', port: 0, strictPort: false, hmr: false, watch: null },
        optimizeDeps: { noDiscovery: true, include: [] },
      });
      await server.listen();
      const address = server.httpServer?.address();
      if (!address || typeof address === 'string') throw new Error('Expected isolated HTTP listener');
      const base = `http://127.0.0.1:${address.port}`;
      for (const dir of dirs) {
        const relative = dir.slice(root.length).replaceAll('\\', '/');
        for (const route of [
          `/${relative}/runtime.json`,
          `/@fs/${join(dir, 'runtime.json')}`,
          `/${relative}/runtime.json?raw`,
          `/${relative}/runtime.json?import`,
        ]) {
          const response = await fetch(base + route);
          const body = await response.text();
          expect(response.status, route).toBe(403);
          expect(body, route).not.toContain('fixture-private-runtime-value');
        }
      }
      expect((await fetch(base + '/')).status).toBe(200);
      expect((await fetch(base + '/src/main.tsx')).status).toBe(200);
      expect(server.config.server.fs.deny).toEqual(expect.arrayContaining(['.env', '.env.*', '**/.git/**']));
    } finally {
      await server?.close();
      for (const dir of dirs) await rm(dir, { recursive: true, force: true });
      vi.unstubAllEnvs();
    }
  },
  30_000,
);
