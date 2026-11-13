import { describe, expect, it, vi } from 'vitest';
import type { AirwallexGateway, IssuingSimulationGateway } from '../../../server/src/airwallex/client.js';
import type { DatabaseClient, DatabasePool, QueryResult } from '../../../server/src/db/pool.js';
import type { Kit5CardAttempt } from '../../../server/src/db/repositories.js';
import type { Kit5PolicyInput } from '../../../server/src/demo/next-customer.js';
import { performDurableRetry, reconcileDurableRetry } from '../../../server/src/policy/kit5-funding.js';

class RetryPool implements DatabasePool {
  attempt: Kit5CardAttempt = {
    scenarioId: 'kit5-target', customerId: 'nova-fixture', actionId: 'nova-action', sequenceIndex: 2,
    lifecycleId: '00000000-0000-4000-8000-000000000001', status: 'completed', result: null, error: null,
    fundingClaimed: true,
    requestFacts: { accountId: 'fixture-account-nova', cardId: 'fixture-card-nova', amountCents: 800_000,
      currency: 'USD', merchant: 'Fixture supplier', transactionType: 'AUTHORIZATION', transactionCategory: 'PURCHASE',
      merchantCategoryCode: '5411' },
    policyStatus: 'settled', policyError: null, policyDecisionId: '00000000-0000-4000-8000-000000000002', bridgeId: '00000000-0000-4000-8000-000000000003',
    pendingPolicyDecisionId: null, policyActorId: null, policyApprovalReason: null,
    retryLifecycleId: null, retryRequestId: null, retryResult: null, retryError: null,
  };

  async query<Row>(sql: string, values: readonly unknown[] = []): Promise<QueryResult<Row>> {
    if (sql.includes('UPDATE kit5_card_attempts') && sql.includes("policy_status = 'retry_submitting'")) {
      if (this.attempt.policyStatus !== 'settled' || this.attempt.retryLifecycleId) return { rows: [], rowCount: 0 };
      this.attempt.policyStatus = 'retry_submitting';
      this.attempt.retryLifecycleId = String(values[2]);
      this.attempt.retryRequestId = String(values[3]);
      return { rows: [], rowCount: 1 };
    }
    if (sql.includes('SET policy_status = $3, retry_provider_result')) {
      this.attempt.policyStatus = values[2] as Kit5CardAttempt['policyStatus'];
      this.attempt.retryResult = values[3] ? JSON.parse(String(values[3])) as Record<string, unknown> : null;
      this.attempt.retryError = values[4] ? String(values[4]) : null;
      return { rows: [], rowCount: 1 };
    }
    if (sql.includes('SELECT scenario_id, customer_id, action_id')) {
      return { rows: [this.row() as Row], rowCount: 1 };
    }
    throw new Error(`Unexpected query: ${sql}`);
  }

  async connect(): Promise<DatabaseClient> { throw new Error('connect not used'); }
  async end(): Promise<void> {}

  private row(): Record<string, unknown> {
    return {
      scenario_id: this.attempt.scenarioId, customer_id: this.attempt.customerId,
      action_id: this.attempt.actionId, sequence_index: this.attempt.sequenceIndex,
      lifecycle_id: this.attempt.lifecycleId, status: this.attempt.status,
      provider_result: this.attempt.result, error: this.attempt.error, funding_claimed: this.attempt.fundingClaimed,
      request_facts: this.attempt.requestFacts, policy_status: this.attempt.policyStatus, policy_error: this.attempt.policyError,
      policy_decision_id: this.attempt.policyDecisionId, pending_policy_decision_id: this.attempt.pendingPolicyDecisionId,
      policy_actor_id: this.attempt.policyActorId, policy_approval_reason: this.attempt.policyApprovalReason,
      bridge_id: this.attempt.bridgeId, retry_lifecycle_id: this.attempt.retryLifecycleId,
      retry_request_id: this.attempt.retryRequestId, retry_provider_result: this.attempt.retryResult,
      retry_error: this.attempt.retryError,
    };
  }
}

const action: Kit5PolicyInput & { amountCents: number } = {
  scenarioId: 'kit5-target', customerId: 'nova-fixture', actionId: 'nova-action', accountId: 'fixture-account-nova',
  cardId: 'fixture-card-nova', attemptedAmountCents: 800_000, amountCents: 800_000, currency: 'USD',
  providerResult: { processResult: 'DECLINED', failureReason: 'INSUFFICIENT_FUNDS',
    cardTransactionEventId: 'evt-original', cardTransactionId: 'txn-original', cardTransactionLifecycleId: 'life-original' },
};

function provider() {
  return {
    listConnectedAccounts: vi.fn(), createTransfer: vi.fn(), createCharge: vi.fn(),
    getTransferByRequestId: vi.fn(), getChargeByRequestId: vi.fn(), getBalance: vi.fn(), getDeposit: vi.fn(),
    createGlobalAccountDepositSimulation: vi.fn(), retrySandboxCard: vi.fn(),
    createIssuingSimulation: vi.fn(async (request: { lifecycleId: string }) => ({
      process_result: 'APPROVED', failure_reason: null, card_transaction_event_id: 'evt-retry',
      card_transaction_id: 'txn-retry', card_transaction_lifecycle_id: request.lifecycleId,
    })),
    getIssuingLifecycle: vi.fn(async () => null),
  } satisfies AirwallexGateway & IssuingSimulationGateway;
}

describe('Kit 5 durable retry lifecycle', () => {
  it('claims and persists one retry result and IDs after settlement', async () => {
    const pool = new RetryPool();
    const gateway = provider();

    await performDurableRetry(pool, gateway, action);
    await performDurableRetry(pool, gateway, action);

    expect(gateway.createIssuingSimulation).toHaveBeenCalledOnce();
    expect(pool.attempt.policyStatus).toBe('retry_completed');
    expect(pool.attempt.retryLifecycleId).toBeTruthy();
    expect(pool.attempt.retryRequestId).toMatch(/^kit5-retry-/);
    expect(pool.attempt.retryResult).toMatchObject({
      cardTransactionEventId: 'evt-retry', cardTransactionId: 'txn-retry', processResult: 'APPROVED',
    });
  });

  it('reconciles an ambiguous retry lifecycle and never repeats provider create', async () => {
    const pool = new RetryPool();
    const gateway = provider();
    vi.mocked(gateway.createIssuingSimulation).mockRejectedValueOnce(new TypeError('network lost'));
    await performDurableRetry(pool, gateway, action);
    expect(pool.attempt.policyStatus).toBe('retry_ambiguous');
    const lifecycleId = pool.attempt.retryLifecycleId!;
    vi.mocked(gateway.getIssuingLifecycle).mockResolvedValueOnce({
      process_result: 'DECLINED', failure_reason: 'LIMIT_EXCEEDED', card_transaction_event_id: 'evt-retry',
      card_transaction_id: 'txn-retry', card_transaction_lifecycle_id: lifecycleId,
    });

    await reconcileDurableRetry(pool, gateway, action);
    await reconcileDurableRetry(pool, gateway, action);

    expect(gateway.createIssuingSimulation).toHaveBeenCalledOnce();
    expect(gateway.getIssuingLifecycle).toHaveBeenCalledTimes(2);
    expect(pool.attempt.policyStatus).toBe('retry_completed');
    expect(pool.attempt.retryResult).toMatchObject({ processResult: 'DECLINED', failureReason: 'LIMIT_EXCEEDED' });
  });
});
