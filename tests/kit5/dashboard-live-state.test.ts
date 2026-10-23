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

import {
  fixtureCheckpointControls,
  fixtureCheckpointReceipt,
  fixtureCheckpointRecoveryState,
  useKit5Dashboard,
  type FixtureCheckpointActivity,
} from '../../src/kit5/useKit5Dashboard.js';
import { loadScenarios } from '../../src/kit5/scenarios.js';

function resetHookRenderCursor() {
  runtime.index = 0;
  runtime.refIndex = 0;
}

function renderDashboard() {
  resetHookRenderCursor();
  // The hook is exercised directly through this lightweight test harness.
  // oxlint-disable-next-line react-hooks/rules-of-hooks
  return useKit5Dashboard('http://localhost:8000');
}

async function prepareFixtureCheckpoint(fetchMock: ReturnType<typeof vi.fn>) {
  vi.stubGlobal('fetch', fetchMock);
  let dashboard = renderDashboard();
  await dashboard.authenticateOperator('runtime-only-token');
  dashboard = renderDashboard();
  dashboard.selectScenario('kit5-target');
  dashboard = renderDashboard();
  dashboard.selectMode('fixture-checkpoint');
  dashboard = renderDashboard();
  dashboard.scan();
  dashboard = renderDashboard();
  dashboard.advance();
  return renderDashboard();
}

function completedCheckpoint(result: {
  processResult: 'APPROVED' | 'DECLINED';
  failureReason: string | null;
}) {
  return {
    status: 'completed',
    attemptId: 'attempt-demo',
    lifecycleId: 'lifecycle-demo',
    attemptedAmount: { amountCents: 900_000, currency: 'USD' },
    result: {
      ...result,
      cardTransactionEventId: 'event-demo',
      cardTransactionId: 'transaction-demo',
      cardTransactionLifecycleId: 'lifecycle-demo',
    },
  };
}

function persistCheckpointRun(input: {
  runId?: string;
  activity?: FixtureCheckpointActivity | null;
  resolution?: 'awaiting' | 'confirmed' | 'fixture-only';
  enabled?: boolean;
}) {
  const runId = input.runId ?? '00000000-0000-4000-8000-000000000001';
  window.sessionStorage.setItem('flowcard-kit5-fixture-checkpoint:kit5-target', JSON.stringify({
    runId,
    activity: input.activity ?? null,
    resolution: input.resolution ?? 'awaiting',
    ...(input.enabled === undefined ? {} : { enabled: input.enabled }),
  }));
  return runId;
}

function checkpointActivity(
  runId: string,
  status: FixtureCheckpointActivity['status'],
  result?: FixtureCheckpointActivity['result'],
): FixtureCheckpointActivity {
  const scenarioId = `kit5-target:${runId}`;
  return {
    scenarioId,
    customerId: 'acct_bloom001',
    actionId: `${scenarioId}:txn_bloom_supplier_001`,
    status,
    attemptId: `${scenarioId}:txn_bloom_supplier_001`,
    lifecycleId: 'lifecycle-bloom',
    attemptedAmount: { amountCents: 900_000, currency: 'USD' },
    ...(result ? { result } : {}),
  };
}

describe('Kit 5 live dashboard recovery state', () => {
  beforeEach(() => {
    runtime.values = [];
    runtime.refs = [];
    const storage = new Map<string, string>();
    vi.stubGlobal('window', { sessionStorage: {
      clear: () => storage.clear(),
      getItem: (key: string) => storage.get(key) ?? null,
      setItem: (key: string, value: string) => { storage.set(key, value); },
    } });
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
    const fetchMock = vi.fn()
      .mockResolvedValueOnce({ ok: true, json: async () => ({ status: 'authenticated' }) })
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
    let dashboard = renderDashboard();
    await dashboard.authenticateOperator('runtime-only-token');
    dashboard = renderDashboard();
    dashboard.selectMode('sandbox-live');
    dashboard = renderDashboard();
    await dashboard.submitNextCustomer();
    dashboard = renderDashboard();
    expect(dashboard.liveHistory).toHaveLength(1);
    expect(dashboard.liveAction?.status).toBe('completed');
    expect(fetchMock.mock.calls.map(([url]) => url)).toEqual([
      'http://localhost:8000/api/kit5/session',
      'http://localhost:8000/api/kit5/next-customer',
    ]);
    expect(JSON.parse(String(fetchMock.mock.calls[1]![1]?.body))).toMatchObject({ customerId: 'acct_bloom001' });

    dashboard.selectScenario(loadScenarios()[1]!.id);
    dashboard = renderDashboard();
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
    let dashboard = renderDashboard();
    await dashboard.authenticateOperator('runtime-only-token');
    dashboard = renderDashboard();
    dashboard.selectMode('sandbox-live');
    dashboard = renderDashboard();
    await dashboard.submitNextCustomer();
    dashboard = renderDashboard();
    expect(dashboard.liveAction?.status).toBe('unresolved');
    expect(dashboard.liveComplete).toBe(false);
    expect(dashboard.livePending).toBe(false);
    await dashboard.refreshLiveAction();
    dashboard = renderDashboard();
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
    let dashboard = renderDashboard();
    await dashboard.authenticateOperator('runtime-only-token');
    dashboard = renderDashboard();
    dashboard.selectMode('sandbox-live');
    dashboard = renderDashboard();
    const originalCustomerIndex = dashboard.nextLiveCustomer?.event.requestId;
    await dashboard.submitNextCustomer();
    dashboard = renderDashboard();
    expect(dashboard.liveAction?.status).toBe('unresolved');
    expect(dashboard.liveBlocked).toBe(true);

    await dashboard.refreshLiveAction();
    dashboard = renderDashboard();
    expect(dashboard.liveAction?.status).toBe('not_persisted');
    expect(dashboard.liveBlocked).toBe(false);
    expect(dashboard.nextLiveCustomer?.event.requestId).toBe(originalCustomerIndex);
    expect(dashboard.liveError).toContain('Retry this same customer and action');

    await dashboard.submitNextCustomer();
    dashboard = renderDashboard();
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
    let dashboard = renderDashboard();
    await dashboard.authenticateOperator('runtime-only-token');
    dashboard = renderDashboard();
    dashboard.selectMode('sandbox-live');
    dashboard = renderDashboard();
    await dashboard.submitNextCustomer();
    dashboard = renderDashboard();
    expect(dashboard.liveAction).toBeNull();
    expect(dashboard.liveComplete).toBe(false);
    await dashboard.submitNextCustomer();
    dashboard = renderDashboard();
    expect(dashboard.liveHistory).toHaveLength(1);
    expect(dashboard.liveAction?.status).toBe('completed');
    expect(dashboard.liveComplete).toBe(false);
    expect(fetchMock).toHaveBeenCalledTimes(3);
  });

  it('keeps fixture interactions away from provider-action endpoints', async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);
    const dashboard = renderDashboard();
    dashboard.scan();
    dashboard.advance();
    await dashboard.submitNextCustomer();
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('holds the target Bloom event in checkpoint mode without creating a live request', () => {
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);
    let dashboard = renderDashboard();
    dashboard.selectScenario('kit5-target');
    dashboard = renderDashboard();
    dashboard.selectMode('fixture-checkpoint');
    dashboard = renderDashboard();
    dashboard.scan();
    dashboard.advance();
    dashboard = renderDashboard();

    expect(dashboard.fixtureCheckpointPaused).toBe(true);
    expect(dashboard.state.cursor).toBe(0);
    expect(dashboard.state.queue).toHaveLength(0);
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('restores checkpoint mode after reload when the presenter enabled it before the provider attempt', () => {
    let dashboard = renderDashboard();
    dashboard.selectScenario('kit5-target');
    dashboard = renderDashboard();
    dashboard.selectMode('fixture-checkpoint');

    const saved = JSON.parse(window.sessionStorage.getItem('flowcard-kit5-fixture-checkpoint:kit5-target') ?? '{}') as Record<string, unknown>;
    expect(saved.enabled).toBe(true);
    const runId = saved.runId;

    runtime.values = [];
    runtime.refs = [];
    dashboard = renderDashboard();
    dashboard.selectScenario('kit5-target');
    dashboard = renderDashboard();

    expect(dashboard.mode).toBe('fixture-checkpoint');
    expect(dashboard.fixtureCheckpointRunId).toBe(runId);
    expect(dashboard.fixtureCheckpointPaused).toBe(true);
    expect(dashboard.state.cursor).toBe(0);
  });

  it.each([
    ['an unresolved provider result', 'ambiguous' as const, undefined, 'awaiting' as const, false],
    ['a completed result awaiting presenter choice', 'completed' as const,
      completedCheckpoint({ processResult: 'DECLINED', failureReason: 'INSUFFICIENT_FUNDS' }).result, 'awaiting' as const, false],
  ])('restores the saved pause for %s after reload', (_description, status, result, resolution, enabled) => {
    const runId = '00000000-0000-4000-8000-000000000002';
    persistCheckpointRun({ runId, activity: checkpointActivity(runId, status, result), resolution, enabled });

    let dashboard = renderDashboard();
    dashboard.selectScenario('kit5-target');
    dashboard = renderDashboard();

    expect(dashboard.mode).toBe('fixture-checkpoint');
    expect(dashboard.fixtureCheckpointRunId).toBe(runId);
    expect(dashboard.fixtureCheckpointResolution).toBe('awaiting');
    expect(dashboard.fixtureCheckpointPaused).toBe(true);
    expect(dashboard.state.cursor).toBe(0);
    dashboard.advance();
    dashboard = renderDashboard();
    expect(dashboard.state.cursor).toBe(0);
    dashboard.selectMode('fixture');
    dashboard = renderDashboard();
    expect(dashboard.mode).toBe('fixture-checkpoint');
  });

  it('restores a resolved checkpoint receipt and preserves the presenter choice after reload', () => {
    const runId = '00000000-0000-4000-8000-000000000003';
    const activity = checkpointActivity(runId, 'completed', completedCheckpoint({
      processResult: 'DECLINED', failureReason: 'INSUFFICIENT_FUNDS',
    }).result);
    persistCheckpointRun({ runId, activity, resolution: 'confirmed' });

    let dashboard = renderDashboard();
    dashboard.selectScenario('kit5-target');
    dashboard = renderDashboard();

    expect(dashboard.mode).toBe('fixture-checkpoint');
    expect(dashboard.fixtureCheckpointActivity).toEqual(activity);
    expect(dashboard.fixtureCheckpointResolution).toBe('confirmed');
    expect(dashboard.fixtureCheckpointPaused).toBe(false);
  });

  it('keeps provider receipt provenance separate and exposes only checkpoint-safe controls', () => {
    const confirmedActivity: FixtureCheckpointActivity = {
      scenarioId: 'kit5-target:run',
      customerId: 'acct_bloom001',
      actionId: 'kit5-target:run:txn_bloom_supplier_001',
      status: 'completed',
      attemptId: 'attempt-demo',
      lifecycleId: 'lifecycle-demo',
      attemptedAmount: { amountCents: 900_000, currency: 'USD' },
      result: {
        processResult: 'DECLINED',
        failureReason: 'INSUFFICIENT_FUNDS',
        cardTransactionEventId: 'event-demo',
        cardTransactionId: 'transaction-demo',
        cardTransactionLifecycleId: 'lifecycle-demo',
      },
    };
    expect(fixtureCheckpointReceipt(confirmedActivity)).toEqual({
      providerResult: 'DECLINED',
      attemptStatus: 'completed',
      failureReason: 'INSUFFICIENT_FUNDS',
      amountCents: 900_000,
      currency: 'USD',
      eventId: 'event-demo',
      transactionId: 'transaction-demo',
      lifecycleId: 'lifecycle-demo',
    });
    expect(fixtureCheckpointRecoveryState(confirmedActivity, 'awaiting', false, true)).toContain('Presenter continuation is required');
    expect(fixtureCheckpointControls({
      mode: 'fixture-checkpoint', paused: true, pending: false, resolution: 'awaiting', activity: confirmedActivity,
    })).toEqual(['continue-fixture']);

    const unexpectedActivity: FixtureCheckpointActivity = {
      ...confirmedActivity,
      result: {
        processResult: 'APPROVED',
        failureReason: null,
        cardTransactionEventId: 'event-demo',
        cardTransactionId: 'transaction-demo',
        cardTransactionLifecycleId: 'lifecycle-demo',
      },
    };
    expect(fixtureCheckpointRecoveryState(unexpectedActivity, 'awaiting', false, true)).toContain('Fixture-only continuation is available');
    expect(fixtureCheckpointControls({
      mode: 'fixture-checkpoint', paused: true, pending: false, resolution: 'awaiting', activity: unexpectedActivity,
    })).toEqual(['continue-fixture-only']);

    const unresolvedActivity = { ...confirmedActivity, status: 'ambiguous' as const };
    expect(fixtureCheckpointControls({
      mode: 'fixture-checkpoint', paused: true, pending: false, resolution: 'awaiting', activity: unresolvedActivity,
    })).toEqual(['reconcile-attempt']);
    expect(fixtureCheckpointControls({
      mode: 'fixture-checkpoint', paused: true, pending: true, resolution: 'awaiting', activity: unresolvedActivity,
    })).toEqual([]);
    expect(fixtureCheckpointControls({
      mode: 'fixture-checkpoint', paused: true, pending: false, resolution: 'awaiting', activity: null,
    })).toEqual(['run-attempt']);
  });

  it('keeps the fixture cursor and decision unchanged while the provider request is pending', async () => {
    let finishCreate: ((value: { ok: boolean; status: number; json: () => Promise<unknown> }) => void) | undefined;
    const pendingResponse = new Promise<{ ok: boolean; status: number; json: () => Promise<unknown> }>((resolve) => {
      finishCreate = resolve;
    });
    const fetchMock = vi.fn()
      .mockResolvedValueOnce({ ok: true, status: 200, json: async () => ({ approvalAvailable: false }) })
      .mockReturnValueOnce(pendingResponse);
    let dashboard = await prepareFixtureCheckpoint(fetchMock);
    const clockBeforeRequest = dashboard.state.demoClock;
    const request = dashboard.runFixtureCheckpoint();
    dashboard = renderDashboard();

    expect(dashboard.fixtureCheckpointPending).toBe(true);
    expect(dashboard.fixtureCheckpointPaused).toBe(true);
    expect(dashboard.state.demoClock).toBe(clockBeforeRequest);
    expect(dashboard.state.cursor).toBe(0);
    expect(dashboard.state.queue).toHaveLength(0);

    finishCreate?.({
      ok: true,
      status: 200,
      json: async () => completedCheckpoint({ processResult: 'DECLINED', failureReason: 'INSUFFICIENT_FUNDS' }),
    });
    await request;
    dashboard = renderDashboard();
    expect(dashboard.fixtureCheckpointPaused).toBe(true);
    expect(dashboard.state.cursor).toBe(0);
    expect(dashboard.state.queue).toHaveLength(0);
  });

  it('requires the exact provider insufficient-funds decline and reuses one action identity per run', async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce({ ok: true, status: 200, json: async () => ({ approvalAvailable: false }) })
      .mockResolvedValueOnce({ ok: true, status: 200, json: async () => completedCheckpoint({ processResult: 'DECLINED', failureReason: 'INSUFFICIENT_FUNDS' }) });
    let dashboard = await prepareFixtureCheckpoint(fetchMock);

    expect(dashboard.fixtureCheckpointPaused).toBe(true);
    await dashboard.runFixtureCheckpoint();
    dashboard = renderDashboard();
    expect(dashboard.fixtureCheckpointProviderConfirmed).toBe(true);
    expect(dashboard.fixtureCheckpointResolution).toBe('awaiting');
    expect(dashboard.fixtureCheckpointPaused).toBe(true);

    const postCalls = fetchMock.mock.calls.filter(([url]) => url === 'http://localhost:8000/api/kit5/fixture-checkpoint');
    expect(postCalls).toHaveLength(1);
    const input = JSON.parse(String(postCalls[0]![1]?.body)) as Record<string, string>;
    expect(Object.keys(input).sort()).toEqual(['actionId', 'customerId', 'scenarioId']);
    expect(input.customerId).toBe('acct_bloom001');
    expect(input.scenarioId).toMatch(/^kit5-target:[0-9a-f-]{36}$/i);
    expect(input.actionId).toBe(`${input.scenarioId}:txn_bloom_supplier_001`);

    dashboard.reset();
    dashboard = renderDashboard();
    expect(dashboard.fixtureCheckpointRunId).toBe(input.scenarioId.split(':')[1]);
    await dashboard.runFixtureCheckpoint();
    expect(fetchMock.mock.calls.filter(([url]) => url === 'http://localhost:8000/api/kit5/fixture-checkpoint')).toHaveLength(1);

    dashboard = renderDashboard();
    dashboard.selectMode('fixture');
    dashboard.selectScenario('original');
    dashboard = renderDashboard();
    expect(dashboard.mode).toBe('fixture-checkpoint');
    expect(dashboard.scenario.id).toBe('kit5-target');

    dashboard.confirmFixtureCheckpoint();
    dashboard = renderDashboard();
    expect(dashboard.fixtureCheckpointResolution).toBe('confirmed');
  });

  it.each([
    ['approved', { processResult: 'APPROVED', failureReason: null }],
    ['declined for a different reason', { processResult: 'DECLINED', failureReason: 'DO_NOT_HONOR' }],
  ] as const)('does not confirm the fixture checkpoint for an %s provider result', async (_label, result) => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce({ ok: true, status: 200, json: async () => ({ approvalAvailable: false }) })
      .mockResolvedValueOnce({ ok: true, status: 200, json: async () => completedCheckpoint(result) });
    let dashboard = await prepareFixtureCheckpoint(fetchMock);
    await dashboard.runFixtureCheckpoint();
    dashboard = renderDashboard();

    expect(dashboard.fixtureCheckpointProviderConfirmed).toBe(false);
    expect(dashboard.fixtureCheckpointResolution).toBe('awaiting');
    expect(dashboard.fixtureCheckpointPaused).toBe(true);
    dashboard.confirmFixtureCheckpoint();
    dashboard = renderDashboard();
    expect(dashboard.fixtureCheckpointResolution).toBe('awaiting');
    expect(dashboard.fixtureCheckpointError).toContain('does not confirm an insufficient-funds decline');
  });

  it('reconciles an ambiguous checkpoint by replaying the same provider-only request', async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce({ ok: true, status: 200, json: async () => ({ approvalAvailable: false }) })
      .mockResolvedValueOnce({ ok: true, status: 202, json: async () => ({
        status: 'ambiguous',
        attemptId: 'attempt-demo',
        lifecycleId: 'lifecycle-demo',
        attemptedAmount: { amountCents: 900_000, currency: 'USD' },
      }) })
      .mockResolvedValueOnce({ ok: true, status: 200, json: async () => completedCheckpoint({ processResult: 'DECLINED', failureReason: 'INSUFFICIENT_FUNDS' }) });
    let dashboard = await prepareFixtureCheckpoint(fetchMock);
    await dashboard.runFixtureCheckpoint();
    dashboard = renderDashboard();

    expect(dashboard.fixtureCheckpointUnresolved).toBe(true);
    expect(dashboard.fixtureCheckpointPaused).toBe(true);
    dashboard.continueFixtureOnly();
    dashboard = renderDashboard();
    expect(dashboard.fixtureCheckpointResolution).toBe('awaiting');

    await dashboard.reconcileFixtureCheckpoint();
    dashboard = renderDashboard();
    expect(dashboard.fixtureCheckpointUnresolved).toBe(false);
    expect(dashboard.fixtureCheckpointProviderConfirmed).toBe(true);
    expect(dashboard.fixtureCheckpointResolution).toBe('awaiting');

    const checkpointCalls = fetchMock.mock.calls.filter(([url]) => String(url).includes('/api/kit5/fixture-checkpoint'));
    expect(checkpointCalls).toHaveLength(2);
    expect(checkpointCalls.map((call) => call[0])).toEqual([
      'http://localhost:8000/api/kit5/fixture-checkpoint',
      'http://localhost:8000/api/kit5/fixture-checkpoint',
    ]);
    expect(checkpointCalls.map((call) => call[1]?.body)).toEqual([checkpointCalls[0]![1]?.body, checkpointCalls[0]![1]?.body]);
    expect(fetchMock.mock.calls.some(([url]) => String(url).includes('/api/kit5/attempt-status'))).toBe(false);
  });

  it('deduplicates concurrent checkpoint submissions and gives an explicit new run a new identity', async () => {
    let finishCreate: ((value: { ok: boolean; status: number; json: () => Promise<unknown> }) => void) | undefined;
    const pendingResponse = new Promise<{ ok: boolean; status: number; json: () => Promise<unknown> }>((resolve) => {
      finishCreate = resolve;
    });
    const fetchMock = vi.fn()
      .mockResolvedValueOnce({ ok: true, status: 200, json: async () => ({ approvalAvailable: false }) })
      .mockReturnValueOnce(pendingResponse)
      .mockResolvedValueOnce({ ok: true, status: 200, json: async () => completedCheckpoint({ processResult: 'APPROVED', failureReason: null }) });
    let dashboard = await prepareFixtureCheckpoint(fetchMock);
    const firstRunId = dashboard.fixtureCheckpointRunId;
    const firstSubmit = dashboard.runFixtureCheckpoint();
    await dashboard.runFixtureCheckpoint();
    expect(fetchMock.mock.calls.filter(([url]) => String(url).includes('/api/kit5/fixture-checkpoint'))).toHaveLength(1);
    finishCreate?.({ ok: true, status: 200, json: async () => completedCheckpoint({ processResult: 'DECLINED', failureReason: 'INSUFFICIENT_FUNDS' }) });
    await firstSubmit;

    dashboard = renderDashboard();
    dashboard.startNewFixtureCheckpointRun();
    dashboard = renderDashboard();
    expect(dashboard.fixtureCheckpointRunId).not.toBe(firstRunId);
    await dashboard.runFixtureCheckpoint();
    const checkpointCalls = fetchMock.mock.calls.filter(([url]) => String(url).includes('/api/kit5/fixture-checkpoint'));
    expect(checkpointCalls).toHaveLength(2);
    const firstRequest = JSON.parse(String(checkpointCalls[0]![1]?.body)) as { scenarioId: string; actionId: string };
    const nextRequest = JSON.parse(String(checkpointCalls[1]![1]?.body)) as { scenarioId: string; actionId: string };
    expect(nextRequest.scenarioId).not.toBe(firstRequest.scenarioId);
    expect(nextRequest.actionId).not.toBe(firstRequest.actionId);
  });
});
