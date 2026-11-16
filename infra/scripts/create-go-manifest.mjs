import fs from 'node:fs/promises';
import { createHash } from 'node:crypto';
import { validateManifest, repository } from '../runtime/go-release-contract.mjs';

const args = process.argv.slice(2);
const get = key => { const i = args.indexOf(key); return i < 0 ? undefined : args[i + 1]; };
const sha = process.env.GITHUB_SHA;
const imageTag = `${sha}-${process.env.GITHUB_RUN_ID}-${process.env.GITHUB_RUN_ATTEMPT}`;
const archive = await fs.readFile(get('--archive'));
const manifest = { generation: 'go-stack-v1', repository, branch: 'develop', commit_sha: sha,
  image_tag: imageTag, frontend_entry: 'original-go-product', generated_at: new Date().toISOString(),
  platform: 'linux/amd64', frontend_checksum_sha256: get('--frontend-checksum'),
  archive: { file: 'go-stack.tar.gz', image_id: get('--image-id'),
    sha256: createHash('sha256').update(archive).digest('hex') }, status: 'build_ready_pending_explicit_deployment' };
validateManifest(manifest, sha);
await fs.writeFile(get('--output'), JSON.stringify(manifest, null, 2) + '\n');
console.log('Sealed Go release manifest generated for ' + sha);
