import { describe, expect, it, vi } from 'vitest';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { initialKit5State, start } from '../../src/kit5/engine.js';
import { loadScenarios } from '../../src/kit5/scenarios.js';
import { BridgeApiClient } from '../../src/services/http/BridgeApiClient.js';
import type { Kit5NextCustomerRequest, Kit5NextCustomerResponse, Kit5ProviderTransaction } from '../../src/services/types.js';
import { Kit5Dashboard } from '../../src/kit5/Kit5Dashboard.js';
import type { FixtureCheckpointActivity } from '../../src/kit5/useKit5Dashboard.js';

const { useKit5DashboardMock } = vi.hoisted(() => ({ useKit5DashboardMock: vi.fn() }));
vi.mock('../../src/kit5/useKit5Dashboard.js', async (importOriginal) => ({
  ...await importOriginal<typeof import('../../src/kit5/useKit5Dashboard.js')>(),
  useKit5Dashboard: useKit5DashboardMock,
}));

const request: Kit5NextCustomerRequest = {
  scenarioId: 'original',
  customerId: 'acct_bloom001',
  actionId: 'original:txn_bloom_payroll_001',
};

const completed: Kit5NextCustomerResponse = {
  status: 'completed',
  attemptId: request.actionId,
  attemptedAmount: { amountCents: 900_000, currency: 'USD' },
  result: {
    processResult: 'DECLINED',
    failureReason: 'INSUFFICIENT_FUNDS',
    cardTransactionEventId: 'fixture-event-id',
    cardTransactionId: 'fixture-transaction-id',
    cardTransactionLifecycleId: 'fixture-lifecycle-id',
  },
};

describe('Kit 5 sandbox-live transaction client', () => {
  it('posts a customer action and returns the provider result unchanged', async () => {
    const fetchImpl = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => completed,
    });
    const client = new BridgeApiClient('http://localhost:8000', fetchImpl as unknown as typeof fetch);

    await expect(client.nextCustomer(request)).resolves.toEqual({
      status: 'completed',
      attemptId: request.actionId,
      attemptedAmount: { amountCents: 900_000, currency: 'USD' },
      result: {
        processResult: 'DECLINED',
        failureReason: 'INSUFFICIENT_FUNDS',
        cardTransactionEventId: 'fixture-event-id',
        cardTransactionId: 'fixture-transaction-id',
        cardTransactionLifecycleId: 'fixture-lifecycle-id',
      },
    });
    expect(fetchImpl).toHaveBeenCalledWith('http://localhost:8000/api/kit5/next-customer', {
      method: 'POST',
      headers: { accept: 'application/json', 'content-type': 'application/json' },
      credentials: 'include',
      body: JSON.stringify(request),
    });
  });

  it('surfaces provider action errors without manufacturing a transaction result', async () => {
    const fetchImpl = vi.fn().mockResolvedValue({
      ok: false,
      status: 502,
      json: async () => ({ error: 'provider_unavailable' }),
    });
    const client = new BridgeApiClient('', fetchImpl as unknown as typeof fetch);

    await expect(client.nextCustomer(request)).rejects.toThrow('provider_unavailable');
  });

  it('returns a rejected server action so the dashboard can retry without advancing', async () => {
    const fetchImpl = vi.fn().mockResolvedValue({
      ok: false,
      status: 409,
      json: async () => ({ status: 'rejected', reason: 'previous_attempt_unresolved' }),
    });
    const client = new BridgeApiClient('', fetchImpl as unknown as typeof fetch);
    await expect(client.nextCustomer(request)).resolves.toEqual({
      status: 'rejected', reason: 'previous_attempt_unresolved',
    });
  });

  it('keeps fixture mode as the initial dashboard mode', () => {
    useKit5DashboardMock.mockReturnValue(dashboardHookValue('fixture'));
    const html = renderToStaticMarkup(createElement(Kit5Dashboard, { versionToggle: null }));

    expect(html).toContain('Fixture');
    expect(html).toContain('Sandbox live');
    expect(html).not.toContain('Sandbox live provider activity');
    expect(html).toContain('Scan Declines');
  });

  it('renders attempted scenario amount and the provider result identifiers and reason', () => {
    useKit5DashboardMock.mockReturnValue(dashboardHookValue('sandbox-live', [{
      status: 'completed',
      attemptId: request.actionId,
      amount: 9000,
      customerName: 'Bloom & Co.',
      result: completed.status === 'completed' ? completed.result : undefined,
    }]));
    const html = renderToStaticMarkup(createElement(Kit5Dashboard, { versionToggle: null }));

    expect(html).toContain('Scenario amount attempted');
    expect(html).toContain('$9,000 USD (provider response does not include amount)');
    expect(html).toContain('DECLINED');
    expect(html).toContain('INSUFFICIENT_FUNDS');
    expect(html).toContain('fixture-event-id');
    expect(html).toContain('fixture-transaction-id');
    expect(html).toContain('fixture-lifecycle-id');
  });

  it('renders the hybrid checkpoint as a provider receipt plus a separate fixture decision', () => {
    const hook = dashboardHookValue('fixture-checkpoint');
    hook.state = start(hook.state);
    hook.fixtureCheckpointPaused = true;
    hook.operatorAuthenticated = true;
    useKit5DashboardMock.mockReturnValue(hook);
    const promptHtml = renderToStaticMarkup(createElement(Kit5Dashboard, { versionToggle: null }));

    expect(promptHtml).toContain('Fixture + Airwallex checkpoint');
    expect(promptHtml).toContain('Run Airwallex Sandbox attempt');
    expect(promptHtml).toContain('The scenario clock and fixture decision remain paused');
    expect(promptHtml).not.toContain('Next customer');
    expect(promptHtml).not.toContain('Restart sequence');
    expect(promptHtml).not.toContain('Create Nova');
    expect(promptHtml).not.toContain('Card retry');

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
        cardTransactionEventId: 'fixture-event-id',
        cardTransactionId: 'fixture-transaction-id',
        cardTransactionLifecycleId: 'fixture-lifecycle-id',
      },
    };
    const receiptHook = dashboardHookValue('fixture-checkpoint', [], confirmedActivity);
    receiptHook.state = start(receiptHook.state);
    receiptHook.fixtureCheckpointPaused = true;
    receiptHook.operatorAuthenticated = true;
    useKit5DashboardMock.mockReturnValue(receiptHook);
    const receiptHtml = renderToStaticMarkup(createElement(Kit5Dashboard, { versionToggle: null }));

    expect(receiptHtml).toContain('Airwallex provider receipt');
    expect(receiptHtml).toContain('Provider result</dt><dd>DECLINED');
    expect(receiptHtml).toContain('$9,000 USD');
    expect(receiptHtml).toContain('INSUFFICIENT_FUNDS');
    expect(receiptHtml).toContain('fixture-event-id');
    expect(receiptHtml).toContain('fixture-transaction-id');
    expect(receiptHtml).toContain('fixture-lifecycle-id');
    expect(receiptHtml).toContain('Fixture policy decision');
    expect(receiptHtml).toContain('Continue fixture sequence');
    expect(receiptHtml).not.toContain('Run Airwallex Sandbox attempt');
    expect(receiptHtml).not.toContain('Next customer');
    expect(receiptHtml).not.toContain('Create Nova');
    expect(receiptHtml).not.toContain('Card retry');
    expect(receiptHtml).not.toContain('Start new Airwallex checkpoint run');
  });

  it('renders a separate action for starting a new provider run after continuation', () => {
    const activity: FixtureCheckpointActivity = {
      scenarioId: 'kit5-target:run',
      customerId: 'acct_bloom001',
      actionId: 'kit5-target:run:txn_bloom_supplier_001',
      status: 'completed',
      lifecycleId: 'lifecycle-demo',
      result: {
        processResult: 'DECLINED',
        failureReason: 'INSUFFICIENT_FUNDS',
        cardTransactionEventId: 'event-demo',
        cardTransactionId: 'transaction-demo',
        cardTransactionLifecycleId: 'lifecycle-demo',
      },
    };
    useKit5DashboardMock.mockReturnValue(dashboardHookValue('fixture-checkpoint', [], activity, 'confirmed'));
    const html = renderToStaticMarkup(createElement(Kit5Dashboard, { versionToggle: null }));

    expect(html).toContain('Start new Airwallex checkpoint run');
    expect(html).not.toContain('Run Airwallex Sandbox attempt');
  });

  it('does not expose the new provider run action while the checkpoint result is unresolved', () => {
    const hook = dashboardHookValue('fixture-checkpoint', [], {
      status: 'ambiguous',
      actionId: 'run:txn_bloom_supplier_001',
      scenarioId: 'kit5-target:run',
      customerId: 'acct_bloom001',
      lifecycleId: null,
    });
    hook.fixtureCheckpointUnresolved = true;
    hook.fixtureCheckpointPaused = true;
    useKit5DashboardMock.mockReturnValue(hook);
    const html = renderToStaticMarkup(createElement(Kit5Dashboard, { versionToggle: null }));

    expect(html).not.toContain('Start new Airwallex checkpoint run');
    expect(html).toContain('Lifecycle ID from Airwallex');
    expect(html).toContain('Verify lifecycle');
  });
});

function dashboardHookValue(mode: 'fixture' | 'fixture-checkpoint' | 'sandbox-live', liveHistory: Array<{
  status: string;
  attemptId?: string;
  result?: Kit5ProviderTransaction;
  amount?: number;
  customerName?: string;
}> = [], fixtureCheckpointActivity: FixtureCheckpointActivity | null = null,
fixtureCheckpointResolution: 'awaiting' | 'confirmed' | 'fixture-only' = 'awaiting') {
  const scenarios = loadScenarios();
  const scenario = scenarios.find((item) => item.id === (mode === 'fixture-checkpoint' ? 'kit5-target' : 'original'))!;
  return {
    scenarios,
    scenario,
    inputs: scenario.inputs,
    state: initialKit5State(scenario.inputs),
    ledgerProjection: null,
    ledgerProjectionLoading: false,
    ledgerProjectionError: null,
    mode,
    fixtureCheckpointResolution,
    fixtureCheckpointPaused: false,
    fixtureCheckpointRunId: 'run-demo',
    fixtureCheckpointScenarioId: `${scenario.id}:run-demo`,
    fixtureCheckpointActivity,
    fixtureCheckpointTermsCompatible: true,
    fixtureCheckpointIdentityKnown: true,
    fixtureCheckpointIdentityPersisted: true,
    fixtureCheckpointProviderConfirmed: false,
    fixtureCheckpointUnresolved: false,
    fixtureCheckpointPending: false,
    fixtureCheckpointError: null,
    liveAction: null,
    liveHistory,
    liveError: null,
    livePending: false,
    liveBlocked: false,
    operatorAuthenticated: false,
    operatorApprovalAvailable: false,
    nextLiveCustomer: undefined,
    liveComplete: false,
    ledgerApiEnabled: true,
    selectScenario: vi.fn(),
    applyInputs: vi.fn(),
    scan: vi.fn(),
    advance: vi.fn(),
    reset: vi.fn(),
    selectCustomer: vi.fn(),
    crmReveal: vi.fn(),
    crmReEvaluate: vi.fn(),
    approve: vi.fn(),
    continueAfterAuto: vi.fn(),
    confirmFixtureCheckpoint: vi.fn(),
    continueFixtureOnly: vi.fn(),
    runFixtureCheckpoint: vi.fn(),
    reconcileFixtureCheckpoint: vi.fn(),
    reconcileFixtureCheckpointLifecycle: vi.fn(),
    startNewFixtureCheckpointRun: vi.fn(),
    selectMode: vi.fn(),
    authenticateOperator: vi.fn(),
    submitNextCustomer: vi.fn(),
  };
}
