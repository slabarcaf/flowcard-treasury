import fs from 'node:fs/promises';
import path from 'node:path';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { validateFoundation, validateManifest, verifyArchive } from '../runtime/go-release-contract.mjs';

const execute = promisify(execFile);
const contract = JSON.parse(await fs.readFile('infra/runtime/go-stack.json', 'utf8'));
if (contract.generation !== 'go-stack-v1' || contract.modules.length !== 6 || contract.services.length !== 9 || contract.database_bindings.length !== 7) throw Error('Incomplete Go deployment contract');
const names = new Set();
for (const service of contract.services) {
  if (names.has(service.name) || !contract.modules.includes(service.module) ||
      !['api', 'worker'].includes(service.command) ||
      Object.keys(service.secrets).some(k => /ADMIN|MIGRATION/.test(k)) ||
      Object.values(service.secrets).some(v => v.startsWith('migrate:'))) throw Error('Unsafe runtime service');
  names.add(service.name);
  await fs.access(`services/${service.module}/cmd/${service.command}/main.go`);
  if (Object.entries(service.environment).some(([k, v]) => /LIVE_DISPATCH|SANDBOX_FUNDING_ENABLED|SANDBOX_COLLECTION_ENABLED/.test(k) && v !== 'false')) throw Error('Financial execution must default off');
}
for (const module of contract.modules) await fs.access(`services/${module}/cmd/migrate/main.go`);
const docker = await fs.readFile('infra/runtime/Dockerfile.go-stack', 'utf8');
if (docker.includes('FROM node:') || !docker.includes('USER 10001:10001') || !docker.includes('rds-global-bundle.pem')) throw Error('Go runtime isolation incomplete');
const ignores = await fs.readFile('.dockerignore', 'utf8');
if (!ignores.split('\n').includes('.worktrees') || !ignores.split('\n').includes('.dev')) throw Error('Private image context exclusion missing');
for (const file of ['.github/workflows/aws-demo.yml', '.github/workflows/go-ci.yml']) {
  const text = await fs.readFile(file, 'utf8');
  for (const m of text.matchAll(/uses:\s+([^\s#]+)/g)) {
    if (!m[1].startsWith('./') && !/^[^@]+@[a-f0-9]{40}$/.test(m[1])) throw Error('Third-party action must be SHA pinned');
  }
}
if (process.argv.includes('--deployment-inputs')) {
  if (process.env.CALLBACK_APPROVED !== 'true') throw Error('Explicit callback migration/disable review required before Go cutover');
  const raw = process.env.GO_FOUNDATION_JSON ?? '';
  if (raw.length > 65536) throw Error('Foundation input too large');
  const foundation = validateFoundation(JSON.parse(raw));
  if (foundation.account_id !== process.env.RELEASE_AWS_ACCOUNT_ID || foundation.aws_region !== process.env.RELEASE_AWS_REGION) throw Error('Repository variables and foundation differ');
  const manifest = validateManifest(JSON.parse(await fs.readFile('.dev/go-release/go-release-manifest.json', 'utf8')), process.env.GITHUB_SHA);
  await verifyArchive(path.join('.image-archives', manifest.archive.file), manifest.archive.sha256);
  const digest = await execute('bash', ['infra/scripts/compute-frontend-hash.sh', '.dev/dist-product']);
  if (digest.stdout.trim() !== manifest.frontend_checksum_sha256) throw Error('Frontend artifact checksum mismatch');
  await fs.writeFile('.dev/go-release/go-foundation.json', JSON.stringify(foundation, null, 2) + '\n', { mode: 0o600 });
}
console.log('Go deployment contract verified: six modules, isolated secrets, SHA-pinned actions, no default financial dispatch');
