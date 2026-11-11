import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { AirwallexGateway, IssuingSimulationGateway } from '../../../server/src/airwallex/client.js';
import type { DatabaseClient, DatabasePool, QueryResult } from '../../../server/src/db/pool.js';
import type { Kit5CardAttempt } from '../../../server/src/db/repositories.js';
import type { Kit5PolicyInput } from '../../../server/src/demo/next-customer.js';
import { recoverPendingKit5Funding } from '../../../server/src/policy/kit5-funding.js';

const mocks = vi.hoisted(() => ({
  evaluate: vi.fn(), reserve: vi.fn(), persistDecision: vi.fn(), confirm: vi.fn(), loadFacts: vi.fn(), loadState: vi.fn(),
}));

vi.mock('../../../server/src/policy/decision-service.js', () => ({ evaluateBridgeRequest: mocks.evaluate }));
vi.mock('../../../server/src/ledger/reservations.js', () => ({ reservePolicyBridge: mocks.reserve }));
vi.mock('../../../server/src/policy/remote-authorization-data.js', () => ({
  loadRequestFacts: mocks.loadFacts,
  loadPolicyState: mocks.loadState,
  toBridgeRequest: (request: unknown) => request,
  persistDecision: mocks.persistDecision,
  confirmKit5PolicyDispatch: mocks.confirm,
  stableId: (_scope: string, value: string) => `00000000-0000-4000-8000-${String(value.length).padStart(12, '0')}`,
  stableBridgeId: () => '00000000-0000-4000-8000-000000000003',
}));

import { createKit5PolicyFundingBoundary } from '../../../server/src/policy/kit5-funding.js';

const action: Kit5PolicyInput = {
  scenarioId: 'fixture-scenario', customerId: 'fixture-customer', actionId: 'fixture-action',
  accountId: 'fixture-account', cardId: 'fixture-card', attemptedAmountCents: 90_000, currency: 'USD',
  providerResult: { processResult: 'DECLINED', failureReason: 'INSUFFICIENT_FUNDS',
    cardTransactionEventId: 'evt-1', cardTransactionId: 'txn-1', cardTransactionLifecycleId: 'life-1' },
};

class PolicyPool implements DatabasePool {
  status: Kit5CardAttempt['policyStatus'] = 'not_started';
  committedDecision: Record<string, unknown> | null = null;
  reservationCommitted = false;
  readonly attempt: Kit5CardAttempt = {
    scenarioId: action.scenarioId, customerId: action.customerId, actionId: action.actionId, sequenceIndex: 0,
    lifecycleId: 'life-1', status: 'completed', result: { ...action.providerResult }, error: null, fundingClaimed: true,
    requestFacts: { accountId: action.accountId, cardId: action.cardId, amountCents: action.attemptedAmountCents,
      currency: 'USD', merchant: 'Fixture supplier', transactionType: 'AUTHORIZATION', transactionCategory: 'PURCHASE', merchantCategoryCode: '5411' },
    policyStatus: 'not_started', policyError: null, policyDecisionId: null, pendingPolicyDecisionId: null,
    policyActorId: null, policyApprovalReason: null, bridgeId: null, retryLifecycleId: null,
    retryRequestId: null, retryResult: null, retryError: null,
  };

  async query<Row>(sql: string, values: readonly unknown[] = []): Promise<QueryResult<Row>> {
    if (sql.includes('FROM kit5_card_attempts') && sql.includes("policy_status IN ('evaluating'")) {
      return { rows: [{ scenario_id: this.attempt.scenarioId, customer_id: this.attempt.customerId,
        action_id: this.attempt.actionId, request_facts: this.attempt.requestFacts,
        provider_result: this.attempt.result } as Row], rowCount: 1 };
    }
    if (sql.includes('FROM policy_request_evaluations')) {
      return { rows: this.committedDecision ? [this.committedDecision as Row] : [], rowCount: this.committedDecision ? 1 : 0 };
    }
    if (sql.includes('FROM kit5_card_attempts WHERE scenario_id = $1 AND action_id = $2')) return { rows: [this.row() as Row], rowCount: 1 };
    if (sql.includes('FROM issuing_card_bindings')) return { rows: [{ customer_id: action.customerId, airwallex_account_id: action.accountId } as Row], rowCount: 1 };
    if (sql.includes('UPDATE kit5_card_attempts') && sql.includes('SET policy_status = $3')) {
      this.status = values[2] as Kit5CardAttempt['policyStatus'];
      this.attempt.policyStatus = this.status;
      this.attempt.policyError = null;
      this.attempt.policyDecisionId = String(values[3]);
      this.attempt.bridgeId = values[4] === null ? null : String(values[4]);
      return { rows: [], rowCount: 1 };
    }
    if (sql.includes('SELECT status FROM bridge_projections')) return { rows: [{ status: 'pending' } as Row], rowCount: 1 };
    throw new Error(`Unexpected query: ${sql}`);
  }

  async connect(): Promise<DatabaseClient> { throw new Error('reservation is mocked'); }
  async end(): Promise<void> {}
  private row(): Record<string, unknown> {
    return {
      scenario_id: this.attempt.scenarioId, customer_id: this.attempt.customerId, action_id: this.attempt.actionId,
      sequence_index: this.attempt.sequenceIndex, lifecycle_id: this.attempt.lifecycleId, status: this.attempt.status,
      provider_result: this.attempt.result, error: this.attempt.error, funding_claimed: this.attempt.fundingClaimed,
      request_facts: this.attempt.requestFacts, policy_status: this.status, policy_error: this.attempt.policyError,
      policy_decision_id: this.attempt.policyDecisionId,
      pending_policy_decision_id: null, policy_actor_id: null, policy_approval_reason: null,
      bridge_id: this.attempt.bridgeId, retry_lifecycle_id: null, retry_request_id: null,
      retry_provider_result: null, retry_error: null,
    };
  }
}

function policyDecision(outcome: 'auto_approved' | 'human_review' | 'declined' | 'blocked') {
  return { requestId: 'evt-1', customerId: action.customerId, outcome, amountCents: outcome === 'declined' ? 0 : 50_000,
    creditLineCents: 100_000, tier: 'A' as const, capitalBand: 'normal' as const, door: outcome === 'human_review' ? 'human' as const : 'auto' as const,
    reasonCodes: [outcome === 'human_review' ? 'HUMAN_REVIEW_REQUIRED' : outcome === 'auto_approved' ? 'AUTO_APPROVED' : 'DOOR_CLOSED'],
    rationale: outcome, ledgerSequence: '10' };
}

const trustedRequest = {
  version: 2 as const, accountId: action.accountId, cardId: action.cardId,
  cardTransactionEventId: 'evt-1', cardTransactionId: 'txn-1', cardTransactionLifecycleId: 'life-1',
  transactionType: 'AUTHORIZATION' as const, transactionCategory: 'PURCHASE', transactionAmount: 900,
  transactionCurrency: 'USD', merchantCategoryCode: '5411',
};
const currentFacts = { customerId: action.customerId, accountId: action.accountId,
  profile: { data: { eligibleSpend: 'supported' } }, precheck: { customerId: action.customerId, profileId: 'profile',
    snapshotId: 'snapshot', evidenceRunId: 'evidence', outcome: 'ready', evaluatedAt: new Date(), creditLineCents: 100_000,
    tier: 'A', humanApprovalRequired: false, reasonCodes: [], policyVersion: 'v1', ratings: null,
    inputVersions: {}, mappingVersions: {}, rosterUpdatedAt: new Date() }, evaluationId: 'evaluation', request: trustedRequest };
const state = { precheck: currentFacts.precheck, eligibleSpend: 'supported', outstandingCents: 0, openBridgeCount: 0,
  oldestOutstandingDueAt: null, facilitySizeCents: 1_000_000, reserveFloorCents: 100_000,
  deployableCents: 500_000, accountId: action.accountId, ledgerSequence: '10' };

describe('Kit 5 policy funding composition', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.loadFacts.mockResolvedValue(currentFacts);
    mocks.loadState.mockResolvedValue(state);
    mocks.evaluate.mockResolvedValue(policyDecision('auto_approved'));
    mocks.persistDecision.mockResolvedValue(undefined);
    mocks.confirm.mockResolvedValue(true);
  });

  it('evaluates without reserving, then reserves exactly once and releases after commit', async () => {
    const pool = new PolicyPool();
    let committed = false;
    mocks.reserve.mockImplementation(async (_pool: unknown, reserveInput: { revalidate: (context: unknown) => Promise<unknown> }) => {
      const validation = await reserveInput.revalidate({ client: pool, snapshotId: 'snapshot', evidenceRunId: 'evidence',
        deployableCents: 500_000, openBridgeCount: 0 });
      committed = true;
      return { decision: (validation as { decision: ReturnType<typeof policyDecision> }).decision,
        reservation: { bridgeId: '00000000-0000-4000-8000-000000000003', providerOperationId: 'op', requestId: 'bridge', amountCents: 50_000 } };
    });
    mocks.confirm.mockImplementation(async () => committed && pool.status === 'reserved');
    const gateway = { getBalance: vi.fn(async () => ({ availableCents: 10_000, currency: 'USD' })) } as unknown as AirwallexGateway & IssuingSimulationGateway;
    const boundary = createKit5PolicyFundingBoundary({ pool, gateway, repaymentTerm: 'asap' });

    const evaluated = await boundary.evaluate(action);
    expect(evaluated.status).toBe('approved');
    expect(mocks.reserve).not.toHaveBeenCalled();
    if (evaluated.status !== 'approved') throw new Error('Expected approved command');
    const funding = await boundary.reserveAndWaitForSettlement(evaluated.command);

    expect(funding.status).toBe('pending');
    expect(mocks.reserve).toHaveBeenCalledOnce();
    expect(mocks.persistDecision).toHaveBeenCalledOnce();
    expect(mocks.confirm).toHaveBeenCalledOnce();
    await expect(mocks.confirm.mock.results[0]?.value).resolves.toBe(true);
  });

  it.each(['human_review', 'declined', 'blocked'] as const)('does not reserve or release for %s decisions', async (outcome) => {
    const pool = new PolicyPool();
    mocks.evaluate.mockResolvedValue(policyDecision(outcome));
    const gateway = { getBalance: vi.fn(async () => ({ availableCents: 10_000, currency: 'USD' })) } as unknown as AirwallexGateway & IssuingSimulationGateway;
    const boundary = createKit5PolicyFundingBoundary({ pool, gateway, repaymentTerm: 'asap' });

    const result = await boundary.evaluate(action);

    expect(result.status).toBe('ineligible');
    expect(mocks.reserve).not.toHaveBeenCalled();
    expect(mocks.confirm).not.toHaveBeenCalled();
    expect(pool.status).toBe(outcome === 'human_review' ? 'pending_review' : outcome === 'blocked' ? 'blocked' : 'rejected');
  });

  it('recovers an evaluating action after restart without repeating provider create or policy decision writes', async () => {
    const pool = new PolicyPool();
    pool.status = 'evaluating';
    pool.attempt.policyStatus = 'evaluating';
    mocks.reserve.mockImplementation(async (_pool: unknown, reserveInput: { revalidate: (context: unknown) => Promise<unknown> }) => {
      if (!pool.reservationCommitted) {
        const validation = await reserveInput.revalidate({ client: pool, snapshotId: 'snapshot', evidenceRunId: 'evidence',
          deployableCents: 500_000, openBridgeCount: 0 });
        pool.reservationCommitted = true;
        pool.committedDecision = { request_id: 'evt-1', customer_id: action.customerId, outcome: 'auto_approved',
          amount_cents: 50_000, actor_id: null, approval_reason: null };
        return { decision: (validation as { decision: ReturnType<typeof policyDecision> }).decision,
          reservation: { bridgeId: '00000000-0000-4000-8000-000000000003', providerOperationId: 'op', requestId: 'bridge', amountCents: 50_000 } };
      }
      return { decision: policyDecision('auto_approved'),
        reservation: { bridgeId: '00000000-0000-4000-8000-000000000003', providerOperationId: 'op', requestId: 'bridge', amountCents: 50_000 } };
    });
    mocks.confirm.mockImplementation(async () => pool.status === 'reserved');
    const gateway = { getBalance: vi.fn(async () => ({ availableCents: 10_000, currency: 'USD' })) } as unknown as AirwallexGateway & IssuingSimulationGateway;
    const boundary = createKit5PolicyFundingBoundary({ pool, gateway, repaymentTerm: 'asap' });

    await recoverPendingKit5Funding(pool, boundary);
    await recoverPendingKit5Funding(pool, boundary);

    expect(mocks.persistDecision).toHaveBeenCalledOnce();
    expect(mocks.reserve).toHaveBeenCalledOnce();
    expect(mocks.confirm).toHaveBeenCalledTimes(2);
    expect(pool.status).toBe('reserved');
  });

  it('recovers a reserving action after restart when reservation did not commit', async () => {
    const pool = new PolicyPool();
    pool.status = 'reserving';
    pool.attempt.policyStatus = 'reserving';
    pool.attempt.policyDecisionId = '00000000-0000-4000-8000-000000000098';
    pool.attempt.bridgeId = '00000000-0000-4000-8000-000000000003';
    const gateway = { getBalance: vi.fn(async () => ({ availableCents: 10_000, currency: 'USD' })) } as unknown as AirwallexGateway & IssuingSimulationGateway;
    let reservationCommitted = false;
    mocks.reserve.mockImplementation(async (_pool: unknown, reserveInput: { revalidate: (context: unknown) => Promise<unknown> }) => {
      const validation = await reserveInput.revalidate({ client: pool, snapshotId: 'snapshot', evidenceRunId: 'evidence',
        deployableCents: 500_000, openBridgeCount: 0 });
      reservationCommitted = true;
      return { decision: (validation as { decision: ReturnType<typeof policyDecision> }).decision,
        reservation: { bridgeId: '00000000-0000-4000-8000-000000000003', providerOperationId: 'op', requestId: 'bridge', amountCents: 50_000 } };
    });
    mocks.confirm.mockImplementation(async () => reservationCommitted && pool.status === 'reserved');
    const boundary = createKit5PolicyFundingBoundary({ pool, gateway, repaymentTerm: 'asap' });

    await recoverPendingKit5Funding(pool, boundary);

    expect(mocks.evaluate).toHaveBeenCalledTimes(2);
    expect(mocks.reserve).toHaveBeenCalledOnce();
    expect(mocks.confirm).toHaveBeenCalledOnce();
    expect(pool.status).toBe('reserved');
  });

  it('releases a committed reservation after restart without creating another reservation', async () => {
    const pool = new PolicyPool();
    pool.status = 'reserving';
    pool.attempt.policyStatus = 'reserving';
    pool.attempt.policyDecisionId = '00000000-0000-4000-8000-000000000099';
    pool.attempt.bridgeId = '00000000-0000-4000-8000-000000000003';
    pool.reservationCommitted = true;
    pool.committedDecision = { request_id: 'evt-1', customer_id: action.customerId, outcome: 'auto_approved',
      amount_cents: 50_000, actor_id: null, approval_reason: null };
    const gateway = { getBalance: vi.fn(async () => ({ availableCents: 10_000, currency: 'USD' })) } as unknown as AirwallexGateway & IssuingSimulationGateway;
    const boundary = createKit5PolicyFundingBoundary({ pool, gateway, repaymentTerm: 'asap' });
    mocks.reserve.mockImplementation(async () => ({ decision: policyDecision('auto_approved'),
      reservation: { bridgeId: pool.attempt.bridgeId!, providerOperationId: 'op', requestId: 'bridge', amountCents: 50_000 } }));
    mocks.confirm.mockImplementation(async () => pool.status === 'reserved');

    await recoverPendingKit5Funding(pool, boundary);

    expect(mocks.persistDecision).not.toHaveBeenCalled();
    expect(mocks.reserve).toHaveBeenCalledOnce();
    expect(mocks.confirm).toHaveBeenCalledOnce();
    expect(pool.status).toBe('reserved');
  });

  it('blocks human approval when the persisted shortfall changes and does not release an outbox', async () => {
    const pool = new PolicyPool();
    pool.status = 'pending_review';
    pool.attempt.policyStatus = 'pending_review';
    pool.attempt.policyDecisionId = '00000000-0000-4000-8000-000000000077';
    pool.attempt.pendingPolicyDecisionId = pool.attempt.policyDecisionId;
    pool.committedDecision = { request_id: 'evt-1', customer_id: action.customerId, outcome: 'human_review',
      amount_cents: 50_000, credit_line_cents: 100_000, tier: 'A', capital_band: 'normal', door: 'human',
      reason_codes: ['HUMAN_REVIEW_REQUIRED'] };
    mocks.evaluate.mockResolvedValue({ ...policyDecision('auto_approved'), amountCents: 49_999, door: 'human' });
    const gateway = { getBalance: vi.fn(async () => ({ availableCents: 10_000, currency: 'USD' })) } as unknown as AirwallexGateway & IssuingSimulationGateway;
    const boundary = createKit5PolicyFundingBoundary({ pool, gateway, repaymentTerm: 'asap' });

    const result = await boundary.approveHumanReview({ ...action, actorId: 'server-operator', reason: 'Reviewed exact terms' });

    expect(result.status).toBe('blocked');
    expect(mocks.reserve).not.toHaveBeenCalled();
    expect(mocks.confirm).not.toHaveBeenCalled();
  });

  it('persists the authenticated actor and reason with exact approved terms before release', async () => {
    const pool = new PolicyPool();
    pool.status = 'pending_review';
    pool.attempt.policyStatus = 'pending_review';
    pool.attempt.policyDecisionId = '00000000-0000-4000-8000-000000000077';
    pool.attempt.pendingPolicyDecisionId = pool.attempt.policyDecisionId;
    pool.committedDecision = { request_id: 'evt-1', customer_id: action.customerId, outcome: 'human_review',
      amount_cents: 50_000, credit_line_cents: 100_000, tier: 'A', capital_band: 'normal', door: 'human',
      reason_codes: ['HUMAN_REVIEW_REQUIRED'] };
    mocks.evaluate.mockResolvedValue({ ...policyDecision('auto_approved'), door: 'human' });
    let transactionCommitted = false;
    mocks.reserve.mockImplementation(async (_pool: unknown, reserveInput: { humanApproval?: unknown; revalidate: (context: unknown) => Promise<unknown> }) => {
      const validation = await reserveInput.revalidate({ client: pool, snapshotId: 'snapshot', evidenceRunId: 'evidence',
        deployableCents: 500_000, openBridgeCount: 0 });
      transactionCommitted = true;
      return { decision: (validation as { decision: ReturnType<typeof policyDecision> }).decision,
        reservation: { bridgeId: '00000000-0000-4000-8000-000000000003', providerOperationId: 'op', requestId: 'bridge', amountCents: 50_000 } };
    });
    mocks.confirm.mockImplementation(async (_pool: unknown, _action: unknown, _request: unknown,
      _decisionId: string, amount: number, approval?: { actorId: string; reason: string }) =>
      transactionCommitted && amount === 50_000 && approval?.actorId === 'server-operator' && approval.reason === 'Reviewed exact terms');
    const gateway = { getBalance: vi.fn(async () => ({ availableCents: 10_000, currency: 'USD' })) } as unknown as AirwallexGateway & IssuingSimulationGateway;
    const boundary = createKit5PolicyFundingBoundary({ pool, gateway, repaymentTerm: 'asap' });

    const result = await boundary.approveHumanReview({ ...action, actorId: 'server-operator', reason: 'Reviewed exact terms' });

    expect(result.status).toBe('pending');
    expect(mocks.reserve).toHaveBeenCalledOnce();
    expect(mocks.reserve.mock.calls[0]?.[1]).toMatchObject({
      amountCents: 50_000, humanApproval: { requestId: 'evt-1', amountCents: 50_000,
        actorId: 'server-operator', reason: 'Reviewed exact terms' },
    });
    expect(mocks.confirm).toHaveBeenCalledOnce();
    expect(transactionCommitted).toBe(true);
  });
});
