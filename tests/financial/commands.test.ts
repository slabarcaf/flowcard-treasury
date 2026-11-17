import { describe, expect, it, vi } from 'vitest';
import { FinancialCommandClient } from '../../src/services/financial/commands.js';
import type { SavedCommand } from '../../src/services/financial/commands.js';
import type { Offer } from '../../src/services/financial/client.js';

const offer: Offer = { id: '22222222-2222-4222-8222-222222222222', source_mode: 'DEMO', customer_id: 'customer-a', version: 7, offer_revision: 1, terms_hash: 'a'.repeat(64), state: 'AWAITING_ACCEPTANCE', principal_cents: 700000, contract_fee_cents: 19000, total_due_cents: 719000, term_days: 30, offered_on: '2026-11-15', due_on: '2026-12-15', expires_at: '2026-11-15T22:00:00Z', disclosures_version: 'v1', late_fee_convention: 'saved-convention', consent_label: 'SYNTHETIC_DEMO_DECISION', originating_case_id: null, evaluation_id: 'saved-evaluation' };
const id = '55555555-5555-4555-8555-555555555555';
const response = (value: unknown, status = 200) => new Response(JSON.stringify(value), { status, headers: { 'content-type': 'application/json', 'X-FlowCard-Command-ID': id } });

describe('explicit browser financial commands', () => {
  it('submits exact saved terms and aggregate version with an opaque key and no actor/customer identity', async () => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(response({ status: 'ACCEPTED' }, 201));
    const result = await new FinancialCommandClient(fetcher).accept(offer);
    expect(result).toMatchObject({ status: 201, commandID: id });
    const [path, init] = fetcher.mock.calls[0];
    expect(path).toBe(`/api/offers/${offer.id}/acceptances`);
    expect(JSON.parse(String(init?.body))).toEqual({ terms_hash: offer.terms_hash, expected_version: 7 });
    expect(init?.credentials).toBe('include'); expect(init?.method).toBe('POST'); expect(init?.headers).not.toHaveProperty('Authorization');
    expect((init!.headers as Record<string, string>)['Idempotency-Key']).toMatch(/^[a-z0-9-]{36}$/);
  });
  it('does not retry an unknown outcome and replays only the same durable payload/path/key on explicit recovery', async () => {
    const fetcher = vi.fn<typeof fetch>().mockRejectedValueOnce(new Error('private transport detail')).mockResolvedValueOnce(response({ status: 'RESERVED' }));
    const api = new FinancialCommandClient(fetcher);
    await expect(api.fund(offer)).rejects.toMatchObject({ status: 503 });
    expect(fetcher).toHaveBeenCalledTimes(1);
    const original = fetcher.mock.calls[0][1]!;
    const key = (original.headers as Record<string, string>)['Idempotency-Key'];
    const saved: SavedCommand = { id, path: `/api/offers/${offer.id}/fund`, idempotency_key: key, payload: JSON.parse(String(original.body)), state: 'UNKNOWN', status: null, receipt: null, created_at: '2026-11-15T20:00:00Z' };
    const receipt = await api.retry(saved);
    expect(receipt.receipt.status).toBe('RESERVED');
    expect(fetcher).toHaveBeenCalledTimes(2);
    expect(fetcher.mock.calls[1][1]?.body).toBe(original.body);
    expect((fetcher.mock.calls[1][1]!.headers as Record<string, string>)['Idempotency-Key']).toBe(key);
  });
  it('refuses unsafe versions and arbitrary recovery destinations before network calls', async () => {
    const fetcher = vi.fn<typeof fetch>(); const api = new FinancialCommandClient(fetcher);
    await expect(api.accept({ ...offer, version: Number.MAX_SAFE_INTEGER + 1 })).rejects.toMatchObject({ status: 400 });
    await expect(api.retry({ id, path: 'https://evil.example/api/offers', idempotency_key: 'saved', payload: {}, state: 'UNKNOWN', status: null, receipt: null, created_at: '' })).rejects.toMatchObject({ status: 400 });
    expect(fetcher).not.toHaveBeenCalled();
  });
  it('keeps rejected, forbidden and expired requests distinct from unknown results without sending another command', async () => {
    for (const status of [401, 403, 409, 422, 502, 503]) {
      const fetcher = vi.fn<typeof fetch>().mockResolvedValue(response({ error: 'safe error' }, status));
      await expect(new FinancialCommandClient(fetcher).approve(offer, 'REVIEWED')).rejects.toMatchObject({ status, commandID: id });
      expect(fetcher).toHaveBeenCalledTimes(1);
    }
  });
});

it('uses explicit sandbox session metadata and keeps unsupported modes unavailable', async () => {
  const fetcher = vi.fn<typeof fetch>()
    .mockResolvedValueOnce(response({ enabled: true, linked: true, mode: 'SANDBOX', role: 'CUSTOMER', customer_id: 'customer-a' }))
    .mockResolvedValueOnce(response({ enabled: true, linked: true, mode: 'PRODUCTION', role: 'CUSTOMER', customer_id: 'customer-a' }))
    .mockResolvedValueOnce(response({ status: 'ACCEPTED' }, 201));
  const api = new FinancialCommandClient(fetcher);
  await expect(api.session()).resolves.toMatchObject({ mode: 'SANDBOX', role: 'CUSTOMER' });
  await expect(api.session()).rejects.toMatchObject({ status: 502 });
  await expect(api.accept({ ...offer, source_mode: 'SANDBOX' })).resolves.toMatchObject({ status: 201 });
  const [path, init] = fetcher.mock.calls[2];
  expect(path).toBe(`/api/offers/${offer.id}/acceptances`);
  expect(JSON.parse(String(init?.body))).toEqual({ terms_hash: offer.terms_hash, expected_version: offer.version });
});
