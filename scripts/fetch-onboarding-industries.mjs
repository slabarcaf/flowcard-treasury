import { writeFile } from 'node:fs/promises';

// Read-only sandbox reference export. Never print credentials, tokens or raw errors.
const base = 'https://api.sandbox.airwallex.com';
const clientId = process.env.AIRWALLEX_CLIENT_ID;
const apiKey = process.env.AIRWALLEX_API_KEY;
if (!clientId || !apiKey) throw new Error('Sandbox credentials are not configured.');
async function json(response, label) {
  if (!response.ok) {
    let code = '';
    try {
      const body = await response.json();
      if (typeof body.code === 'string' && /^[A-Za-z0-9_-]{1,80}$/.test(body.code)) code = ` (${body.code})`;
      if (typeof body.message === 'string') {
        const safe = body.message.replaceAll(clientId, '[redacted]').replaceAll(apiKey, '[redacted]').replace(/[A-Za-z0-9._+/=-]{24,}/g, '[redacted]').slice(0, 300);
        console.error('Service message:', safe);
      }
    } catch { /* No raw response or credential logging. */ }
    throw new Error(`${label} returned HTTP ${response.status}${code}`);
  }
  return response.json();
}
try {
  const auth = await json(await fetch(`${base}/api/v1/authentication/login`, {
    method: 'POST', headers: { 'content-type': 'application/json', 'x-client-id': clientId, 'x-api-key': apiKey },
    body: '{}', signal: AbortSignal.timeout(20_000),
  }), 'Sandbox authentication');
  if (typeof auth.token !== 'string' || !auth.token) throw new Error('Authentication returned no token.');
  console.log('Authentication succeeded.');
  const tree = await json(await fetch(`${base}/api/v1/reference/industry_categories`, {
    headers: { Authorization: `Bearer ${auth.token}`, 'content-type': 'application/json', accept: 'application/json' }, signal: AbortSignal.timeout(20_000),
  }), 'Industry reference');
  if (!Array.isArray(tree)) throw new Error('Industry reference is not an array.');
  const codes = new Set();
  function visit(nodes) {
    for (const item of nodes) {
      if (String(item.type).toUpperCase() === 'CODE' && typeof item.value === 'string' && /^ICCV3_[A-Z0-9]+$/.test(item.value)) codes.add(item.value);
      if (Array.isArray(item.values)) visit(item.values);
    }
  }
  visit(tree);
  if (!codes.size) throw new Error('Reference response has no recognized industry codes; export was not written.');
  const target = new URL('../src/services/onboarding/industry-categories.json', import.meta.url);
  await writeFile(target, `${JSON.stringify(tree, null, 2)}\n`);
  await writeFile(new URL('../src/services/onboarding/industry-categories.source.json', import.meta.url), `${JSON.stringify({
    source: `${base}/api/v1/reference/industry_categories`, fetched_at: new Date().toISOString(), category_count: tree.length, unique_code_count: codes.size,
  }, null, 2)}\n`);
  console.log(JSON.stringify({ exported: true, categories: tree.length, uniqueCodes: codes.size }));
} catch (error) {
  console.error(error instanceof Error ? error.message : 'Industry export failed.');
  process.exitCode = 1;
}
