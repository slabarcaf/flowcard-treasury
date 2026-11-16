import { describe, expect, it, vi } from 'vitest';
import { ProductReadClient } from '../../src/services/financial/product-reads.js';
const source = {
  source_mode: 'DEMO',
  source_binding_id: 'owned-installation',
  facility_id: 'owned-facility',
  as_of: '2026-11-16T03:00:00Z',
};
const portfolio = {
  ...source,
  facility_version: 1,
  facility_total_cents: 100000000,
  reserve_bps: 2000,
  reserve_floor_cents: 20000000,
  funded_principal_cents: 700000,
  reserved_principal_cents: 0,
  other_commitments_cents: 0,
  available_capital_cents: 79300000,
  current_regime: 'ABUNDANT',
  principal_owed_cents: 700000,
  unpaid_fee_cents: 19000,
  fees_collected_cents: null,
  history_complete: false,
  lifecycle_counts: { ACTIVE: 1 },
  held_bridge_count: 0,
  policy_id: 'owned-policy',
  policy_hash: 'a'.repeat(64),
};
const row = {
  customer_id: 'owned-customer',
  display_name: null,
  mapping_active: true,
  accounting_available: false,
  principal_owed_cents: null,
  unpaid_fee_cents: null,
  reserved_principal_cents: null,
  reserved_fee_cents: null,
  history_complete: null,
  active_hold: null,
  version: null,
  captured_at: null,
  active_bridge_id: null,
};
function client(body: unknown, status = 200) {
  const fetcher = vi
    .fn<typeof fetch>()
    .mockResolvedValue(
      new Response(JSON.stringify(body), {
        status,
        headers: { 'content-type': 'application/json' },
      })
    );
  return { api: new ProductReadClient(fetcher), fetcher };
}
describe('connected product read contracts', () => {
  it('preserves integer cents and unknown fee coverage without inventing zero', async () => {
    const { api, fetcher } = client(portfolio);
    const value = await api.portfolio();
    expect(value.available_capital_cents).toBe(79300000);
    expect(value.fees_collected_cents).toBeNull();
    expect(fetcher.mock.calls[0][0]).toBe('/api/operator/portfolio');
    expect(fetcher.mock.calls[0][1]).toMatchObject({ credentials: 'include', cache: 'no-store' });
  });
  it('rejects unsafe money and unsupported source modes', async () => {
    await expect(
      client({ ...portfolio, facility_total_cents: 9007199254740992 }).api.portfolio()
    ).rejects.toMatchObject({ status: 502 });
    await expect(
      client({ ...portfolio, source_mode: 'LIVE' }).api.portfolio()
    ).rejects.toMatchObject({ status: 502 });
  });
  it('preserves missing customer accounting and opaque cursors', async () => {
    const { api, fetcher } = client({ ...source, items: [row], next_cursor: 'next+/=' });
    const result = await api.customers('opaque+/=');
    expect(result.items[0].principal_owed_cents).toBeNull();
    expect(fetcher.mock.calls[0][0]).toBe('/api/operator/customers?cursor=opaque%2B%2F%3D');
  });
  it('rejects fabricated zero for unavailable accounting and customer substitution', async () => {
    await expect(
      client({
        ...source,
        items: [{ ...row, principal_owed_cents: 0 }],
        next_cursor: null,
      }).api.customers()
    ).rejects.toMatchObject({ status: 502 });
    await expect(
      client({ ...source, customer: { ...row, customer_id: 'foreign' } }).api.customer(
        'owned-customer'
      )
    ).rejects.toMatchObject({ status: 502 });
  });
  it('customer discovery never supplies a browser customer selector', async () => {
    const { api, fetcher } = client({ ...source, items: [], next_cursor: null });
    expect((await api.bridges()).items).toEqual([]);
    expect(fetcher.mock.calls[0][0]).toBe('/api/customer/bridges');
  });
  it('operator bridge history scopes and encodes the exact customer path', async () => {
    const { api, fetcher } = client({ ...source, items: [], next_cursor: null });
    await api.bridges('owned customer', 'cursor');
    expect(fetcher.mock.calls[0][0]).toBe(
      '/api/operator/customers/owned%20customer/bridges?cursor=cursor'
    );
  });
  it('surfaces unavailable and unauthorized sources instead of returning fixtures', async () => {
    await expect(client({}, 503).api.portfolio()).rejects.toMatchObject({ status: 503 });
    await expect(client({}, 401).api.bridges()).rejects.toMatchObject({ status: 401 });
  });
  it('rejects oversized pages and missing collection arrays', async () => {
    await expect(
      client({
        ...source,
        items: Array.from({ length: 26 }, () => row),
        next_cursor: null,
      }).api.customers()
    ).rejects.toMatchObject({ status: 502 });
    await expect(
      client({ ...source, items: null, next_cursor: null }).api.bridges()
    ).rejects.toMatchObject({ status: 502 });
  });
});
