import { beforeEach, describe, expect, it, vi } from 'vitest';

const runtime = vi.hoisted(() => ({ values: [] as unknown[], refs: [] as Array<{ current: unknown }>, index: 0, refIndex: 0 }));

vi.mock('react', () => ({
  useState(initial: unknown) {
    const index = runtime.index++;
    if (runtime.values[index] === undefined) runtime.values[index] = typeof initial === 'function' ? (initial as () => unknown)() : initial;
    return [runtime.values[index], (next: unknown) => {
      runtime.values[index] = typeof next === 'function' ? (next as (value: unknown) => unknown)(runtime.values[index]) : next;
    }];
  },
  useMemo(callback: () => unknown) { return callback(); },
  useCallback(callback: unknown) { return callback; },
  useEffect() {},
  useRef(initial: unknown) {
    const index = runtime.refIndex++;
    runtime.refs[index] ??= { current: initial };
    return runtime.refs[index];
  },
}));

import { useKit5Dashboard } from '../../src/kit5/useKit5Dashboard.js';
import { loadScenarios } from '../../src/kit5/scenarios.js';

function resetHookRenderCursor() {
  runtime.index = 0;
  runtime.refIndex = 0;
}

function useDashboardForTest() {
  resetHookRenderCursor();
  return useKit5Dashboard('http://localhost:8000');
}

describe('Kit 5 live dashboard recovery state', () => {
  beforeEach(() => {
    runtime.values = [];
    runtime.refs = [];
    vi.stubGlobal('fetch', vi.fn()
      .mockResolvedValueOnce({ ok: true, json: async () => ({ status: 'authenticated' }) })
      .mockResolvedValueOnce({ ok: true, json: async () => ({
        status: 'completed',
        attemptId: 'original:txn_bloom_payroll_001',
        attemptedAmount: { amountCents: 900_000, currency: 'USD' },
        result: {
          processResult: 'DECLINED', failureReason: 'INSUFFICIENT_FUNDS',
          cardTransactionEventId: 'event', cardTransactionId: 'transaction', cardTransactionLifecycleId: 'lifecycle',
        },
      }) }));
  });

  it('clears live order, action, history, and errors when the scenario changes', async () => {
    let dashboard = useDashboardForTest();
    await dashboard.authenticateOperator('runtime-only-token');
    dashboard = useDashboardForTest();
    dashboard.selectMode('sandbox-live');
    dashboard = useDashboardForTest();
    await dashboard.submitNextCustomer();
    dashboard = useDashboardForTest();
    expect(dashboard.liveHistory).toHaveLength(1);
    expect(dashboard.liveAction?.status).toBe('completed');

    dashboard.selectScenario(loadScenarios()[1]!.id);
    dashboard = useDashboardForTest();
    expect(dashboard.liveAction).toBeNull();
    expect(dashboard.liveHistory).toEqual([]);
    expect(dashboard.liveError).toBeNull();
    expect(dashboard.liveComplete).toBe(false);
    expect(dashboard.nextLiveCustomer?.event.requestId).toBe(loadScenarios()[1]!.inputs.events.find((event) => event.type === 'decline')?.requestId);
  });

  it('recovers a rejected request by refreshing its saved action without advancing early', async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce({ ok: true, json: async () => ({ status: 'authenticated' }) })
      .mockResolvedValueOnce({ ok: false, status: 503, json: async () => ({ error: 'kit5_attempt_unavailable' }) })
      .mockResolvedValueOnce({ ok: true, json: async () => ({
        status: 'completed',
        attemptId: 'original:txn_bloom_payroll_001',
        attemptedAmount: { amountCents: 900_000, currency: 'USD' },
        result: {
          processResult: 'DECLINED', failureReason: 'INSUFFICIENT_FUNDS',
          cardTransactionEventId: 'event', cardTransactionId: 'transaction', cardTransactionLifecycleId: 'lifecycle',
        },
      }) });
    vi.stubGlobal('fetch', fetchMock);
    let dashboard = useDashboardForTest();
    await dashboard.authenticateOperator('runtime-only-token');
    dashboard = useDashboardForTest();
    dashboard.selectMode('sandbox-live');
    dashboard = useDashboardForTest();
    await dashboard.submitNextCustomer();
    dashboard = useDashboardForTest();
    expect(dashboard.liveAction?.status).toBe('unresolved');
    expect(dashboard.liveComplete).toBe(false);
    expect(dashboard.livePending).toBe(false);
    await dashboard.refreshLiveAction();
    dashboard = useDashboardForTest();
    expect(dashboard.liveAction?.status).toBe('completed');
    expect(dashboard.liveHistory).toHaveLength(1);
    expect(dashboard.liveComplete).toBe(false);
    expect(fetchMock).toHaveBeenCalledTimes(3);
  });

  it('allows same-ID retry when a network failure was not persisted and refresh returns 404', async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce({ ok: true, json: async () => ({ status: 'authenticated' }) })
      .mockRejectedValueOnce(new TypeError('Failed to fetch'))
      .mockResolvedValueOnce({ ok: false, status: 404, json: async () => ({ error: 'kit5_attempt_not_found' }) })
      .mockResolvedValueOnce({ ok: true, json: async () => ({
        status: 'completed',
        attemptId: 'original:txn_bloom_payroll_001',
        attemptedAmount: { amountCents: 900_000, currency: 'USD' },
        result: {
          processResult: 'DECLINED', failureReason: 'INSUFFICIENT_FUNDS',
          cardTransactionEventId: 'event', cardTransactionId: 'transaction', cardTransactionLifecycleId: 'lifecycle',
        },
      }) });
    vi.stubGlobal('fetch', fetchMock);
    let dashboard = useDashboardForTest();
    await dashboard.authenticateOperator('runtime-only-token');
    dashboard = useDashboardForTest();
    dashboard.selectMode('sandbox-live');
    dashboard = useDashboardForTest();
    const originalCustomerIndex = dashboard.nextLiveCustomer?.event.requestId;
    await dashboard.submitNextCustomer();
    dashboard = useDashboardForTest();
    expect(dashboard.liveAction?.status).toBe('unresolved');
    expect(dashboard.liveBlocked).toBe(true);

    await dashboard.refreshLiveAction();
    dashboard = useDashboardForTest();
    expect(dashboard.liveAction?.status).toBe('not_persisted');
    expect(dashboard.liveBlocked).toBe(false);
    expect(dashboard.nextLiveCustomer?.event.requestId).toBe(originalCustomerIndex);
    expect(dashboard.liveError).toContain('Retry this same customer and action');

    await dashboard.submitNextCustomer();
    dashboard = useDashboardForTest();
    expect(dashboard.liveAction?.status).toBe('completed');
    expect(dashboard.liveAction?.attemptId).toBe('original:txn_bloom_payroll_001');
    expect(dashboard.liveHistory).toHaveLength(1);
    const postCalls = fetchMock.mock.calls.filter(([url]) => url === 'http://localhost:8000/api/kit5/next-customer');
    expect(postCalls).toHaveLength(2);
    const firstRequest = JSON.parse(String(postCalls[0]![1]?.body));
    const retryRequest = JSON.parse(String(postCalls[1]![1]?.body));
    expect(retryRequest).toEqual(firstRequest);
    expect(retryRequest).toMatchObject({ customerId: 'acct_bloom001' });
    expect(retryRequest.scenarioId).toMatch(/^original:[0-9a-f-]{36}$/i);
    expect(retryRequest.actionId).toBe(`${retryRequest.scenarioId}:txn_bloom_payroll_001`);
  });

  it('releases the submission lock after an explicit rejection and retries the same customer', async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce({ ok: true, json: async () => ({ status: 'authenticated' }) })
      .mockResolvedValueOnce({ ok: false, status: 409, json: async () => ({ status: 'rejected', reason: 'previous_attempt_unresolved' }) })
      .mockResolvedValueOnce({ ok: true, json: async () => ({
        status: 'completed',
        attemptId: 'original:txn_bloom_payroll_001',
        attemptedAmount: { amountCents: 900_000, currency: 'USD' },
        result: {
          processResult: 'DECLINED', failureReason: 'INSUFFICIENT_FUNDS',
          cardTransactionEventId: 'event', cardTransactionId: 'transaction', cardTransactionLifecycleId: 'lifecycle',
        },
      }) });
    vi.stubGlobal('fetch', fetchMock);
    let dashboard = useDashboardForTest();
    await dashboard.authenticateOperator('runtime-only-token');
    dashboard = useDashboardForTest();
    dashboard.selectMode('sandbox-live');
    dashboard = useDashboardForTest();
    await dashboard.submitNextCustomer();
    dashboard = useDashboardForTest();
    expect(dashboard.liveAction).toBeNull();
    expect(dashboard.liveComplete).toBe(false);
    await dashboard.submitNextCustomer();
    dashboard = useDashboardForTest();
    expect(dashboard.liveHistory).toHaveLength(1);
    expect(dashboard.liveAction?.status).toBe('completed');
    expect(dashboard.liveComplete).toBe(false);
    expect(fetchMock).toHaveBeenCalledTimes(3);
  });

  it('keeps fixture interactions away from provider-action endpoints', async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);
    const dashboard = useDashboardForTest();
    dashboard.scan();
    dashboard.advance();
    await dashboard.submitNextCustomer();
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('holds the target Bloom event in checkpoint mode without creating a live request', () => {
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);
    let dashboard = useDashboardForTest();
    dashboard.selectScenario('kit5-target');
    dashboard = useDashboardForTest();
    dashboard.selectMode('fixture-checkpoint');
    dashboard = useDashboardForTest();
    dashboard.scan();
    dashboard.advance();
    dashboard = useDashboardForTest();

    expect(dashboard.fixtureCheckpointPaused).toBe(true);
    expect(dashboard.state.cursor).toBe(0);
    expect(dashboard.state.queue).toHaveLength(0);
    expect(fetchMock).not.toHaveBeenCalled();
  });
});
