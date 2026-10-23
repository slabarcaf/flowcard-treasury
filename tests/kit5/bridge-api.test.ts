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

  it('sends only the run-scoped checkpoint identity with the operator session cookie', async () => {
    const checkpoint = {
      status: 'completed',
      attemptId: 'kit5-target:00000000-0000-4000-8000-000000000001:txn_bloom_supplier_001',
      lifecycleId: 'lifecycle-demo',
      attemptedAmount: { amountCents: 900_000, currency: 'USD' },
      result: {
        processResult: 'DECLINED',
        failureReason: 'INSUFFICIENT_FUNDS',
        cardTransactionEventId: 'event-demo',
        cardTransactionId: 'transaction-demo',
        cardTransactionLifecycleId: 'lifecycle-demo',
      },
    } as const;
    const fetchImpl = vi.fn().mockResolvedValue({ ok: true, status: 200, json: async () => checkpoint });
    const client = new BridgeApiClient('http://localhost:8000', fetchImpl as unknown as typeof fetch);
    const input = {
      scenarioId: 'kit5-target:00000000-0000-4000-8000-000000000001',
      customerId: 'acct_bloom001',
      actionId: 'kit5-target:00000000-0000-4000-8000-000000000001:txn_bloom_supplier_001',
    };

    await expect(client.fixtureCheckpoint(input)).resolves.toEqual(checkpoint);
    expect(fetchImpl).toHaveBeenCalledWith('http://localhost:8000/api/kit5/fixture-checkpoint', {
      method: 'POST',
      headers: { accept: 'application/json', 'content-type': 'application/json' },
      credentials: 'include',
      body: JSON.stringify(input),
    });
    const submittedBody = JSON.parse(String(fetchImpl.mock.calls[0]![1]?.body)) as Record<string, unknown>;
    expect(Object.keys(submittedBody).sort()).toEqual(['actionId', 'customerId', 'scenarioId']);
  });

  it('recovers the saved server checkpoint identity after browser storage loss', async () => {
    const recovery = {
      status: 'ambiguous',
      scenarioId: 'kit5-target:00000000-0000-4000-8000-000000000009',
      customerId: 'acct_bloom001',
      actionId: 'kit5-target:00000000-0000-4000-8000-000000000009:txn_bloom_supplier_001',
      attemptId: 'kit5-target:00000000-0000-4000-8000-000000000009:txn_bloom_supplier_001',
      lifecycleId: '33333333-3333-4333-8333-333333333333',
      attemptedAmount: { amountCents: 900_000, currency: 'USD' },
    } as const;
    const fetchImpl = vi.fn().mockResolvedValue({ ok: true, status: 200, json: async () => recovery });
    const client = new BridgeApiClient('http://localhost:8000', fetchImpl as unknown as typeof fetch);

    await expect(client.recoverFixtureCheckpoint()).resolves.toEqual(recovery);
    expect(fetchImpl).toHaveBeenCalledWith('http://localhost:8000/api/kit5/fixture-checkpoint/recover', {
      method: 'POST',
      headers: { accept: 'application/json', 'content-type': 'application/json' },
      credentials: 'include',
      body: '{}',
    });
  });

  it('marks a newly confirmed checkpoint run explicitly', async () => {
    const input = {
      scenarioId: 'kit5-target:00000000-0000-4000-8000-000000000002',
      customerId: 'acct_bloom001',
      actionId: 'kit5-target:00000000-0000-4000-8000-000000000002:txn_bloom_supplier_001',
      startNew: true,
    };
    const response = {
      status: 'in_progress',
      scenarioId: input.scenarioId,
      customerId: input.customerId,
      actionId: input.actionId,
      attemptId: input.actionId,
      lifecycleId: null,
      attemptedAmount: { amountCents: 900_000, currency: 'USD' },
    } as const;
    const fetchImpl = vi.fn().mockResolvedValue({ ok: true, status: 202, json: async () => response });
    const client = new BridgeApiClient('', fetchImpl as unknown as typeof fetch);

    await expect(client.fixtureCheckpoint(input)).resolves.toEqual(response);
    expect(JSON.parse(String(fetchImpl.mock.calls[0]![1]?.body))).toEqual(input);
  });

  it('preserves an ambiguous response for same-identity recovery', async () => {
    const checkpoint = {
      status: 'ambiguous',
      attemptId: 'action-demo',
      lifecycleId: null,
      attemptedAmount: { amountCents: 900_000, currency: 'USD' },
    } as const;
    const client = new BridgeApiClient('', vi.fn().mockResolvedValue({
      ok: true,
      status: 202,
      json: async () => checkpoint,
    }) as unknown as typeof fetch);

    await expect(client.fixtureCheckpoint({ scenarioId: 'kit5-target:run', customerId: 'acct_bloom001', actionId: 'action-demo' }))
      .resolves.toEqual(checkpoint);
  });

  it('verifies an operator-supplied lifecycle without retrying the checkpoint create', async () => {
    const lifecycleId = '33333333-3333-4333-8333-333333333333';
    const responseBody = {
      status: 'completed',
      attemptId: 'action-demo',
      lifecycleId,
      attemptedAmount: { amountCents: 900_000, currency: 'USD' },
      result: {
        processResult: 'DECLINED',
        failureReason: 'INSUFFICIENT_FUNDS',
        cardTransactionEventId: 'event-demo',
        cardTransactionId: 'transaction-demo',
        cardTransactionLifecycleId: lifecycleId,
      },
    } as const;
    const input = {
      scenarioId: 'kit5-target:00000000-0000-4000-8000-000000000001',
      customerId: 'acct_bloom001',
      actionId: 'kit5-target:00000000-0000-4000-8000-000000000001:txn_bloom_supplier_001',
      lifecycleId,
    };
    const fetchImpl = vi.fn().mockResolvedValue({ ok: true, status: 200, json: async () => responseBody });
    const client = new BridgeApiClient('http://localhost:8000', fetchImpl as unknown as typeof fetch);

    await expect(client.reconcileFixtureCheckpointLifecycle(input)).resolves.toEqual(responseBody);
    expect(fetchImpl).toHaveBeenCalledWith('http://localhost:8000/api/kit5/fixture-checkpoint/reconcile', {
      method: 'POST',
      headers: { accept: 'application/json', 'content-type': 'application/json' },
      credentials: 'include',
      body: JSON.stringify(input),
    });
  });

  it('returns a persisted failed attempt from the route’s non-success status', async () => {
    const checkpoint = {
      status: 'failed',
      attemptId: 'action-demo',
      lifecycleId: null,
      attemptedAmount: { amountCents: 900_000, currency: 'USD' },
    } as const;
    const client = new BridgeApiClient('', vi.fn().mockResolvedValue({
      ok: false,
      status: 502,
      json: async () => checkpoint,
    }) as unknown as typeof fetch);

    await expect(client.fixtureCheckpoint({ scenarioId: 'kit5-target:run', customerId: 'acct_bloom001', actionId: 'action-demo' }))
      .resolves.toEqual(checkpoint);
  });

  it('rejects malformed checkpoint responses instead of treating them as provider confirmation', async () => {
    const client = new BridgeApiClient('', vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({
        status: 'completed',
        attemptId: 'action-demo',
        lifecycleId: null,
        attemptedAmount: { amountCents: 900_000, currency: 'USD' },
        result: { processResult: 'DECLINED', failureReason: 'INSUFFICIENT_FUNDS' },
      }),
    }) as unknown as typeof fetch);

    await expect(client.fixtureCheckpoint({ scenarioId: 'kit5-target:run', customerId: 'acct_bloom001', actionId: 'action-demo' }))
      .rejects.toThrow('Kit 5 fixture checkpoint failed (200)');
  });
});
