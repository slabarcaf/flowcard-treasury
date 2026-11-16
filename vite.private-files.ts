import { resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { normalizePath } from 'vite';

const root = fileURLToPath(new URL('.', import.meta.url));

// Preserve Vite's default sensitive-file exclusions and add this checkout's
// private directories. Absolute checkout-relative globs keep a worktree's own
// public source readable even when its parent directory is named .worktrees.
export function privateRuntimeFileAccess() {
  return {
    strict: true,
    deny: [
      '.env',
      '.env.*',
      '*.{crt,pem,key,p12,pfx,cer,der}',
      '.npmrc',
      '.yarnrc.yml',
      '**/.git/**',
      normalizePath(resolve(root, '.dev', '**')),
      normalizePath(resolve(root, '.worktrees', '**')),
    ],
  };
}
