import { describe, expect, it, vi } from 'vitest';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { initialKit5State } from '../../src/kit5/engine.js';
import { loadScenarios } from '../../src/kit5/scenarios.js';
import { BridgeApiClient } from '../../src/services/http/BridgeApiClient.js';
import type { Kit5NextCustomerRequest, Kit5NextCustomerResponse, Kit5ProviderTransaction } from '../../src/services/types.js';
import { Kit5Dashboard } from '../../src/kit5/Kit5Dashboard.js';

const { useKit5DashboardMock } = vi.hoisted(() => ({ useKit5DashboardMock: vi.fn() }));
vi.mock('../../src/kit5/useKit5Dashboard.js', () => ({ useKit5Dashboard: useKit5DashboardMock }));

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
});

function dashboardHookValue(mode: 'fixture' | 'sandbox-live', liveHistory: Array<{
  status: string;
  attemptId?: string;
  result?: Kit5ProviderTransaction;
  amount?: number;
  customerName?: string;
}> = []) {
  const scenarios = loadScenarios();
  const scenario = scenarios[0]!;
  return {
    scenarios,
    scenario,
    inputs: scenario.inputs,
    state: initialKit5State(scenario.inputs),
    ledgerProjection: null,
    ledgerProjectionLoading: false,
    ledgerProjectionError: null,
    mode,
    liveAction: null,
    liveHistory,
    liveError: null,
    livePending: false,
    liveBlocked: false,
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
    selectMode: vi.fn(),
    submitNextCustomer: vi.fn(),
  };
}
