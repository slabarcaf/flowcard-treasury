import { describe, expect, it, vi } from 'vitest';
import { loadScenarios } from '../../src/kit5/scenarios.js';
import { createSalesforceSnapshotClient } from '../../src/services/salesforceSnapshotClient.js';
import type { SalesforceSnapshotReadModel } from '../../src/services/types.js';

describe('Salesforce snapshot service source selection', () => {
  it('returns deterministic synthetic snapshots in demo mode without network calls', async () => {
    const fetchImpl = vi.fn(() => Promise.reject(new Error('unexpected network request')));
    const client = createSalesforceSnapshotClient('demo', { fetchImpl });

    const first = await client.getSnapshot('acct_bloom001');
    const second = await client.getSnapshot('acct_bloom001');

    expect(first).toEqual(second);
    expect(first).toMatchObject({
      customerId: 'acct_bloom001',
      active: true,
      freshness: 'fresh',
      snapshot: {
        snapshotId: 'demo-acct_bloom001',
        sourceMode: 'demo',
        data: { account: { recordId: 'demo-sf-bloom001' } },
      },
    });
    expect(fetchImpl).not.toHaveBeenCalled();
  });

  it('uses only the production read API and sends no browser credentials', async () => {
    const result: SalesforceSnapshotReadModel = {
      customerId: 'acct_customer / 1',
      active: true,
      freshness: 'fresh',
      ageSeconds: 20,
      snapshot: null,
      latestAttempt: null,
      validationErrors: [],
    };
    const fetchImpl = vi.fn(async () => new Response(JSON.stringify(result), { status: 200 }));
    const client = createSalesforceSnapshotClient('production', {
      baseUrl: 'https://ledger.example.test',
      fetchImpl,
    });

    await expect(client.getSnapshot('acct_customer / 1')).resolves.toEqual(result);
    expect(fetchImpl).toHaveBeenCalledWith(
      'https://ledger.example.test/api/customers/acct_customer%20%2F%201/snapshot',
      {
        method: 'GET',
        headers: { accept: 'application/json' },
        credentials: 'omit',
      },
    );
  });

  it('surfaces production authorization errors and never falls back to fixtures', async () => {
    const fetchImpl = vi.fn(async () => new Response(JSON.stringify({ error: 'snapshot_read_unauthorized' }), { status: 401 }));
    const client = createSalesforceSnapshotClient('production', { fetchImpl });

    await expect(client.getSnapshot('acct_bloom001')).rejects.toThrow('Salesforce snapshot request failed (401)');
    expect(fetchImpl).toHaveBeenCalledTimes(1);
  });

  it('keeps scenario snapshot lookups on stable customer IDs without changing policy inputs', () => {
    const scenario = loadScenarios().find(({ id }) => id === 'original');
    expect(scenario).toBeDefined();
    expect(scenario!.inputs.customers.map(({ salesforceSnapshotCustomerId }) => salesforceSnapshotCustomerId)).toEqual(
      scenario!.inputs.customers.map(({ id }) => id),
    );
    expect(scenario!.inputs.customers.map(({ key }) => key)).toEqual(['bloom', 'atlas', 'nova', 'pixel']);
  });
});
