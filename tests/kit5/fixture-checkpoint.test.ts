import { beforeEach, describe, expect, it, vi } from 'vitest';

const runtime = vi.hoisted(() => ({ values: [] as unknown[], index: 0 }));

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
  useRef(initial: unknown) { return { current: initial }; },
}));

import { initialKit5State } from '../../src/kit5/engine.js';
import { loadScenarios } from '../../src/kit5/scenarios.js';
import {
  isFixtureCheckpointEventNext,
  shouldPauseForFixtureCheckpoint,
  useKit5Dashboard,
} from '../../src/kit5/useKit5Dashboard.js';

function renderDashboard(apiBaseUrl = '') {
  runtime.index = 0;
  // The hook is exercised directly through this lightweight test harness.
  // oxlint-disable-next-line react-hooks/rules-of-hooks
  return useKit5Dashboard(apiBaseUrl);
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

describe('Kit 5 fixture provider checkpoint', () => {
  beforeEach(() => {
    runtime.values = [];
    const storage = new Map<string, string>();
    vi.stubGlobal('window', { sessionStorage: {
      clear: () => storage.clear(),
      getItem: (key: string) => storage.get(key) ?? null,
      setItem: (key: string, value: string) => { storage.set(key, value); },
    } });
  });

  it('marks only Bloom’s Kit 5 target event as the Airwallex checkpoint', () => {
    const scenarios = loadScenarios();
    const target = scenarios.find((scenario) => scenario.id === 'kit5-target')!;

    expect(target.providerCheckpoint).toEqual({
      provider: 'airwallex',
      eventRequestId: 'txn_bloom_supplier_001',
      customerKey: 'bloom',
    });
    expect(scenarios.filter((scenario) => scenario.providerCheckpoint)).toEqual([target]);
    expect(target.inputs.events[0]).toMatchObject({
      type: 'decline',
      requestId: target.providerCheckpoint?.eventRequestId,
      customerKey: target.providerCheckpoint?.customerKey,
      paymentAmount: 9_000,
      walletBalance: 2_000,
    });
  });

  it('pauses only the configured event in checkpoint mode while pure Fixture and Sandbox live remain unpaused', () => {
    const target = loadScenarios().find((scenario) => scenario.id === 'kit5-target')!;
    const state = initialKit5State(target.inputs);

    expect(isFixtureCheckpointEventNext(target, state)).toBe(true);
    expect(shouldPauseForFixtureCheckpoint('fixture-checkpoint', target, state, 'awaiting')).toBe(true);
    expect(shouldPauseForFixtureCheckpoint('fixture', target, state, 'awaiting')).toBe(false);
    expect(shouldPauseForFixtureCheckpoint('sandbox-live', target, state, 'awaiting')).toBe(false);
    expect(shouldPauseForFixtureCheckpoint('fixture-checkpoint', target, state, 'confirmed')).toBe(false);
    expect(shouldPauseForFixtureCheckpoint('fixture-checkpoint', target, state, 'fixture-only')).toBe(false);

    const original = loadScenarios().find((scenario) => scenario.id === 'original')!;
    expect(isFixtureCheckpointEventNext(original, initialKit5State(original.inputs))).toBe(false);
  });

  it('blocks the marked event until an exact provider decline is confirmed, then processes it once', async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce({ ok: true, status: 200, json: async () => ({ approvalAvailable: false }) })
      .mockResolvedValueOnce({ ok: true, status: 200, json: async () => ({
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
      })});
    vi.stubGlobal('fetch', fetchMock);
    let dashboard = renderDashboard('http://localhost:8000');
    await dashboard.authenticateOperator('runtime-only-token');
    dashboard = renderDashboard('http://localhost:8000');
    dashboard.selectScenario('kit5-target');
    dashboard = renderDashboard('http://localhost:8000');
    dashboard.selectMode('fixture-checkpoint');
    dashboard = renderDashboard('http://localhost:8000');
    dashboard.scan();
    dashboard = renderDashboard('http://localhost:8000');

    expect(dashboard.fixtureCheckpointPaused).toBe(true);
    dashboard.advance();
    dashboard = renderDashboard('http://localhost:8000');
    expect(dashboard.state.cursor).toBe(0);
    expect(dashboard.state.queue).toHaveLength(0);

    await dashboard.runFixtureCheckpoint();
    dashboard = renderDashboard('http://localhost:8000');
    expect(fetchMock).toHaveBeenCalledTimes(2);
    expect(dashboard.fixtureCheckpointActivity?.status).toBe('completed');
    expect(dashboard.fixtureCheckpointProviderConfirmed).toBe(true);
    expect(dashboard.fixtureCheckpointResolution).toBe('awaiting');
    dashboard.confirmFixtureCheckpoint();
    dashboard = renderDashboard('http://localhost:8000');
    expect(dashboard.fixtureCheckpointResolution).toBe('confirmed');

    dashboard.advance();
    dashboard = renderDashboard('http://localhost:8000');
    expect(dashboard.state.cursor).toBe(1);
    expect(dashboard.state.queue.filter((row) => row.id === 'txn_bloom_supplier_001')).toHaveLength(1);

    dashboard.advance();
    dashboard = renderDashboard('http://localhost:8000');
    expect(dashboard.state.queue.filter((row) => row.id === 'txn_bloom_supplier_001')).toHaveLength(1);
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it('continues the provider-confirmed Bloom decline through ordered fixture policy outcomes', async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce({ ok: true, status: 200, json: async () => ({ approvalAvailable: false }) })
      .mockResolvedValueOnce({ ok: true, status: 200, json: async () => completedCheckpoint({
        processResult: 'DECLINED', failureReason: 'INSUFFICIENT_FUNDS',
      }) });
    vi.stubGlobal('fetch', fetchMock);
    let dashboard = renderDashboard('http://localhost:8000');
    await dashboard.authenticateOperator('runtime-only-token');
    dashboard = renderDashboard('http://localhost:8000');
    dashboard.selectScenario('kit5-target');
    dashboard = renderDashboard('http://localhost:8000');
    dashboard.selectMode('fixture-checkpoint');
    dashboard = renderDashboard('http://localhost:8000');
    dashboard.scan();
    dashboard = renderDashboard('http://localhost:8000');
    dashboard.advance();
    dashboard = renderDashboard('http://localhost:8000');

    await dashboard.runFixtureCheckpoint();
    dashboard = renderDashboard('http://localhost:8000');
    expect(fetchMock).toHaveBeenCalledTimes(2);
    expect(dashboard.fixtureCheckpointActivity?.status).toBe('completed');
    expect(dashboard.fixtureCheckpointProviderConfirmed).toBe(true);
    dashboard.confirmFixtureCheckpoint();
    dashboard = renderDashboard('http://localhost:8000');
    dashboard.advance();
    dashboard = renderDashboard('http://localhost:8000');

    const bloom = dashboard.state.queue.find((row) => row.id === 'txn_bloom_supplier_001');
    expect(bloom?.status).toBe('Blocked');
    expect(dashboard.state.customerDetails.txn_bloom_supplier_001.reasonCodes).toContain('EVIDENCE_REVIEW_REQUIRED');
    expect(dashboard.state.queue.filter((row) => row.id === 'txn_bloom_supplier_001')).toHaveLength(1);

    const expectedArrivalOrder = dashboard.scenario.inputs.events
      .filter((event) => event.type === 'decline')
      .map((event) => event.requestId);
    for (let guard = 0; dashboard.state.queue.length < expectedArrivalOrder.length && guard < 20; guard++) {
      if (dashboard.state.pause?.kind === 'ack') dashboard.continueAfterAuto();
      else if (dashboard.state.pause?.kind === 'human') dashboard.approve(dashboard.state.pause.requestId);
      else if (dashboard.state.pause?.kind === 'crm') {
        dashboard.crmReveal();
        dashboard = renderDashboard('http://localhost:8000');
        dashboard.crmReEvaluate();
      }
      dashboard = renderDashboard('http://localhost:8000');
      dashboard.advance();
      dashboard = renderDashboard('http://localhost:8000');
    }

    expect(dashboard.state.queue.map((row) => row.id)).toEqual(expectedArrivalOrder);
    expect(dashboard.state.queue.find((row) => row.id === 'txn_bloom_supplier_001')?.status).toBe('Blocked');
    expect(fetchMock.mock.calls.map(([url]) => url)).toEqual([
      'http://localhost:8000/api/kit5/session',
      'http://localhost:8000/api/kit5/fixture-checkpoint',
    ]);
  });

  it('lets the presenter continue fixture-only after a checkpoint result is not confirmed', () => {
    let dashboard = renderDashboard();
    dashboard.selectScenario('kit5-target');
    dashboard = renderDashboard();
    dashboard.selectMode('fixture-checkpoint');
    dashboard = renderDashboard();
    dashboard.scan();
    dashboard = renderDashboard();

    expect(dashboard.fixtureCheckpointPaused).toBe(true);
    dashboard.continueFixtureOnly();
    dashboard = renderDashboard();
    expect(dashboard.fixtureCheckpointResolution).toBe('fixture-only');
    expect(dashboard.fixtureCheckpointPaused).toBe(false);
    dashboard.advance();
    dashboard = renderDashboard();
    expect(dashboard.state.cursor).toBe(1);
    expect(dashboard.state.queue.filter((row) => row.id === 'txn_bloom_supplier_001')).toHaveLength(1);
  });

  it('keeps pure Fixture advancing the same Bloom event without checkpoint resolution', () => {
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);
    let dashboard = renderDashboard();
    dashboard.selectScenario('kit5-target');
    dashboard = renderDashboard();
    dashboard.scan();
    dashboard = renderDashboard();
    dashboard.advance();
    dashboard = renderDashboard();

    expect(dashboard.mode).toBe('fixture');
    expect(dashboard.fixtureCheckpointPaused).toBe(false);
    expect(dashboard.fixtureCheckpointResolution).toBe('awaiting');
    expect(dashboard.state.cursor).toBe(1);
    expect(dashboard.state.queue.filter((row) => row.id === 'txn_bloom_supplier_001')).toHaveLength(1);
    expect(fetchMock).not.toHaveBeenCalled();
  });
});
