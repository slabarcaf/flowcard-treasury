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

function renderDashboard() {
  runtime.index = 0;
  return useKit5Dashboard();
}

describe('Kit 5 fixture provider checkpoint', () => {
  beforeEach(() => {
    runtime.values = [];
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

  it('blocks the marked event until confirmation, then processes it once in the existing fixture queue', () => {
    let dashboard = renderDashboard();
    dashboard.selectScenario('kit5-target');
    dashboard = renderDashboard();
    dashboard.selectMode('fixture-checkpoint');
    dashboard = renderDashboard();
    dashboard.scan();
    dashboard = renderDashboard();

    expect(dashboard.fixtureCheckpointPaused).toBe(true);
    dashboard.advance();
    dashboard = renderDashboard();
    expect(dashboard.state.cursor).toBe(0);
    expect(dashboard.state.queue).toHaveLength(0);

    dashboard.confirmFixtureCheckpoint();
    dashboard = renderDashboard();
    expect(dashboard.fixtureCheckpointResolution).toBe('confirmed');
    expect(dashboard.fixtureCheckpointPaused).toBe(false);
    dashboard.advance();
    dashboard = renderDashboard();
    expect(dashboard.state.cursor).toBe(1);
    expect(dashboard.state.queue.filter((row) => row.id === 'txn_bloom_supplier_001')).toHaveLength(1);

    dashboard.advance();
    dashboard = renderDashboard();
    expect(dashboard.state.queue.filter((row) => row.id === 'txn_bloom_supplier_001')).toHaveLength(1);
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
  });
});
