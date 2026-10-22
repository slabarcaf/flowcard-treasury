import { describe, expect, it, vi } from 'vitest';
import { BridgeApiClient } from '../../src/services/http/BridgeApiClient.js';
import type { BridgeLedgerStatus, LedgerProjectionResponse } from '../../src/services/types.js';
import { loadScenarios } from '../../src/kit5/scenarios.js';
import { initialKit5State, start, step } from '../../src/kit5/engine.js';

const apiProjection: LedgerProjectionResponse = {
  facility: {
    currency: 'USD',
    facilityAmountCents: 5_000_000,
    reserveFloorCents: 1_000_000,
    openingExposureCents: 2_500_000,
    settledPrincipalCents: 0,
    activeReservationsCents: 0,
    deployableCents: 1_500_000,
  },
  bridges: [{
    bridgeId: 'bridge-demo',
    customerId: 'customer-demo',
    status: 'funded',
    principalOutstandingCents: 500_000,
    feeOutstandingCents: 0,
  }],
};

describe('Kit 5 ledger API client', () => {
  it('reads the server projection without credentials or provider calls', async () => {
    const fetchImpl = vi.fn().mockResolvedValue({ ok: true, status: 200, json: async () => apiProjection });
    const client = new BridgeApiClient('http://localhost:3000', fetchImpl as unknown as typeof fetch);
    await expect(client.getLedgerProjection()).resolves.toEqual(apiProjection);
    expect(fetchImpl).toHaveBeenCalledWith('http://localhost:3000/api/ledger', {
      method: 'GET',
      headers: { accept: 'application/json' },
      credentials: 'omit',
    });
  });

  it('retains deterministic scenario mode and its existing bridge status vocabulary', () => {
    const scenario = loadScenarios().find((candidate) => candidate.id === 'first-come');
    expect(scenario).toBeDefined();
    const state = step(start(initialKit5State(scenario!.inputs)));
    const mockBridge = state.queue.find((row) => row.id === 'txn_cedar_supplier_001');
    expect(mockBridge?.status).toBe('Funded');
    const statusContract: BridgeLedgerStatus | undefined = mockBridge?.status === 'Funded'
      ? 'funded'
      : mockBridge?.status === 'Repaid' ? 'repaid' : undefined;
    expect(apiProjection.bridges[0].status).toBe(statusContract);
    // API read mode is additive; scenario selection and the mock engine remain the default.
    expect(loadScenarios()[0].id).toBe('original');
  });

  it('surfaces non-success HTTP responses', async () => {
    const client = new BridgeApiClient('', vi.fn().mockResolvedValue({ ok: false, status: 503 }) as unknown as typeof fetch);
    await expect(client.getLedgerProjection()).rejects.toThrow('Bridge ledger request failed (503)');
  });
});
