import { describe, expect, it } from 'vitest';
import { BridgeApiClient } from '../../src/services/http/BridgeApiClient.js';
import { BridgeApiError } from '../../src/services/http/BridgeApiClient.js';
import { formatCollectionCharge } from '../../src/kit5/live-format.js';
import { canApproveKit5Decision, canCreateNovaRepaymentDeposit, liveActivityNeedsReconciliation, upsertLiveHistory } from '../../src/kit5/useKit5Dashboard.js';

describe('Kit 5 live history recovery', () => {
  it('sends only action identity and reason in the approval request', async () => {
    let postedBody = '';
    const client = new BridgeApiClient('https://demo.example', async (_input, init) => {
      postedBody = String(init?.body ?? '');
      return new Response(JSON.stringify({ status: 'pending' }), { status: 200 });
    });

    await client.approveHumanReview({ scenarioId: 's', customerId: 'c', actionId: 'a', reason: 'reviewed' });

    expect(Object.keys(JSON.parse(postedBody) as Record<string, unknown>).sort()).toEqual([
      'actionId', 'customerId', 'reason', 'scenarioId',
    ]);
  });

  it('updates one completed card row through human review, settlement, and retry without changing its identity', () => {
    const pending = {
      status: 'in_progress', attemptId: 'scenario:action-1', customerId: 'bloom', customerName: 'Bloom',
      result: {
        processResult: 'DECLINED' as const, failureReason: 'INSUFFICIENT_FUNDS',
        cardTransactionEventId: 'evt-1', cardTransactionId: 'txn-1', cardTransactionLifecycleId: 'life-1',
      },
      policyStatus: 'pending_review', policyDecisionId: 'decision-1',
    };
    const history = upsertLiveHistory([], pending);
    const decision = { outcome: 'human_review' as const, amountCents: 700_000, creditLineCents: 900_000,
      tier: 'A', capitalBand: 'normal', door: 'human', reasonCodes: ['HUMAN_REVIEW_REQUIRED'], rationale: 'Review required.' };
    expect(canApproveKit5Decision(false, pending.policyStatus, decision)).toBe(false);
    expect(canApproveKit5Decision(true, pending.policyStatus, null)).toBe(false);
    expect(canApproveKit5Decision(true, pending.policyStatus, decision)).toBe(true);
    expect(liveActivityNeedsReconciliation({ ...pending, status: 'completed' })).toBe(true);

    const completed = upsertLiveHistory(history, {
      ...pending, status: 'completed', policyStatus: 'retry_completed', bridgeId: 'bridge-1',
      retryLifecycleId: 'retry-life-1', retryResult: {
        requestId: 'kit5-retry-1', processResult: 'APPROVED', failureReason: null,
        cardTransactionEventId: 'retry-evt', cardTransactionId: 'retry-txn', cardTransactionLifecycleId: 'retry-life-1',
      },
    });
    expect(completed).toHaveLength(1);
    expect(completed[0]).toMatchObject({ attemptId: 'scenario:action-1', policyStatus: 'retry_completed', bridgeId: 'bridge-1' });
    expect(completed[0]?.retryResult?.cardTransactionEventId).toBe('retry-evt');
    expect(canApproveKit5Decision(true, 'retry_completed', decision)).toBe(false);
    expect(liveActivityNeedsReconciliation(completed[0] ?? null)).toBe(false);
  });

  it('permits the Nova repayment action only after authenticated human approval, bridge settlement, and completed retry', () => {
    const ready = {
      status: 'completed', attemptId: 'kit5-target:nova-action', customerId: 'nova-internal', customerName: 'Nova Agency',
      result: { processResult: 'DECLINED' as const, failureReason: 'INSUFFICIENT_FUNDS', cardTransactionEventId: 'evt', cardTransactionId: 'txn', cardTransactionLifecycleId: 'life' },
      policyStatus: 'retry_completed', humanApproved: true, bridgeId: 'bridge-1',
      retryResult: { processResult: 'APPROVED' as const, failureReason: null, cardTransactionEventId: 'retry-evt', cardTransactionId: 'retry-txn', cardTransactionLifecycleId: 'retry-life' },
    };
    expect(canCreateNovaRepaymentDeposit(false, ready)).toBe(false);
    expect(canCreateNovaRepaymentDeposit(true, { ...ready, humanApproved: false })).toBe(false);
    expect(canCreateNovaRepaymentDeposit(true, { ...ready, policyStatus: 'pending_review' })).toBe(false);
    expect(canCreateNovaRepaymentDeposit(true, { ...ready, policyStatus: 'reserved' })).toBe(false);
    expect(canCreateNovaRepaymentDeposit(true, { ...ready, retryResult: null })).toBe(false);
    expect(canCreateNovaRepaymentDeposit(true, { ...ready, retryError: 'retry_unresolved' })).toBe(false);
    expect(canCreateNovaRepaymentDeposit(true, { ...ready, customerName: 'Atlas Supply' })).toBe(false);
    expect(canCreateNovaRepaymentDeposit(true, ready)).toBe(true);

    const initial = upsertLiveHistory([], ready);
    const refreshed = upsertLiveHistory(initial, {
      ...ready,
      repaymentDeposit: {
        status: 'settled', depositId: 'dep-nova', providerStatus: 'SETTLED', bridgeId: 'bridge-1', statementRef: 'kit5-ref', error: null,
        collection: { fee: { status: 'settled', amountCents: 12_000, providerOperationId: 'fee-op', providerObjectId: 'fee-charge' },
          principal: { status: 'created', amountCents: 788_000, providerOperationId: 'principal-op', providerObjectId: null } },
      },
    });
    expect(refreshed).toHaveLength(1);
    expect(refreshed[0]).toMatchObject({ attemptId: ready.attemptId, repaymentDeposit: { depositId: 'dep-nova', status: 'settled' } });
    expect(refreshed[0]?.repaymentDeposit?.collection?.fee?.providerObjectId).toBe('fee-charge');
    expect(refreshed[0]?.repaymentDeposit?.collection?.principal?.amountCents).toBe(788_000);
    expect(formatCollectionCharge(refreshed[0]?.repaymentDeposit?.collection?.fee)).toContain('settled, $120 USD');
    expect(formatCollectionCharge(refreshed[0]?.repaymentDeposit?.collection?.principal)).toContain('created, $7,880 USD');
    expect(refreshed[0]?.repaymentDeposit?.depositId).toBe('dep-nova');
  });

  it('posts only scenario, customer, and action IDs to the authenticated Nova deposit API', async () => {
    let requestBody = '';
    let credentials = '';
    const client = new BridgeApiClient('https://demo.example', async (_input, init) => {
      requestBody = String(init?.body ?? '');
      credentials = String(init?.credentials ?? '');
      return new Response(JSON.stringify({ status: 'created', depositId: 'dep-nova', providerStatus: 'SETTLED', bridgeId: 'bridge-1', statementRef: 'kit5-ref', error: null, collection: null }), { status: 200 });
    });
    const response = await client.createOrRefreshNovaRepaymentDeposit({ scenarioId: 'kit5-target', customerId: 'nova-internal', actionId: 'nova-action' });
    expect(Object.keys(JSON.parse(requestBody) as Record<string, unknown>).sort()).toEqual(['actionId', 'customerId', 'scenarioId']);
    expect(credentials).toBe('include');
    expect(response.depositId).toBe('dep-nova');
  });

  it('surfaces a fail-closed mapping explanation returned by the server', async () => {
    const client = new BridgeApiClient('https://demo.example', async () => new Response(JSON.stringify({
      error: 'Verified Nova connected-account and USD Global Account mappings are unavailable',
    }), { status: 409 }));
    let caught: unknown;
    try {
      await client.createOrRefreshNovaRepaymentDeposit({ scenarioId: 'kit5-target', customerId: 'nova-internal', actionId: 'nova-action' });
    } catch (error) {
      caught = error;
    }
    expect(caught).toBeInstanceOf(BridgeApiError);
    expect(caught).toMatchObject({ status: 409 });
    expect((caught as Error).message).toContain('mappings are unavailable');
  });
});
