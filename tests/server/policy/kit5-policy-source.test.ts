import { describe, expect, it } from 'vitest';
import type { Kit5CardAttempt } from '../../../server/src/db/repositories.js';
import type { Kit5PolicyInput } from '../../../server/src/demo/next-customer.js';
import { isKit5ActionSourceConsistent, sameKit5ApprovalTerms } from '../../../server/src/policy/kit5-funding.js';

const action: Kit5PolicyInput = {
  scenarioId: 'fixture-scenario', customerId: 'fixture-customer', actionId: 'fixture-action',
  accountId: 'fixture-account', cardId: 'fixture-card', attemptedAmountCents: 90_000, currency: 'USD',
  providerResult: { processResult: 'DECLINED', failureReason: 'INSUFFICIENT_FUNDS',
    cardTransactionEventId: 'evt-1', cardTransactionId: 'txn-1', cardTransactionLifecycleId: 'life-1' },
};

function attempt(overrides: Partial<Kit5CardAttempt> = {}): Kit5CardAttempt {
  return {
    scenarioId: action.scenarioId, customerId: action.customerId, actionId: action.actionId, sequenceIndex: 0,
    lifecycleId: action.providerResult.cardTransactionLifecycleId, status: 'completed',
    result: { ...action.providerResult }, error: null, fundingClaimed: true,
    requestFacts: { accountId: action.accountId, cardId: action.cardId, amountCents: action.attemptedAmountCents,
      currency: 'USD', merchant: 'Fixture supplier', transactionType: 'AUTHORIZATION', transactionCategory: 'PURCHASE',
      merchantCategoryCode: '5411' },
    policyStatus: 'evaluating', policyDecisionId: null, pendingPolicyDecisionId: null,
    policyActorId: null, policyApprovalReason: null, bridgeId: null,
    retryLifecycleId: null, retryRequestId: null, retryResult: null, retryError: null,
    ...overrides,
  };
}

describe('Kit 5 policy source binding', () => {
  it('requires the saved response event and transaction IDs to match the action', () => {
    expect(isKit5ActionSourceConsistent(attempt(), action)).toBe(true);
    expect(isKit5ActionSourceConsistent(attempt({ result: { ...action.providerResult, cardTransactionEventId: 'evt-other' } }), action)).toBe(false);
    expect(isKit5ActionSourceConsistent(attempt({ result: { ...action.providerResult, cardTransactionId: 'txn-other' } }), action)).toBe(false);
  });

  it('requires the explicit saved card binding and known transaction classification', () => {
    expect(isKit5ActionSourceConsistent(attempt({ requestFacts: { ...attempt().requestFacts!, cardId: 'other-card' } }), action)).toBe(false);
    expect(isKit5ActionSourceConsistent(attempt({ requestFacts: { ...attempt().requestFacts!, amountCents: 90_001 } }), action)).toBe(false);
    expect(isKit5ActionSourceConsistent(attempt({ requestFacts: { ...attempt().requestFacts!, merchantCategoryCode: '9999' } }), action)).toBe(false);
    expect(isKit5ActionSourceConsistent(attempt({ requestFacts: { ...attempt().requestFacts!, transactionCategory: 'UNKNOWN' as 'PURCHASE' } }), action)).toBe(false);
  });

  it('requires exact persisted human-review terms when revalidating approval', () => {
    const pending = { amountCents: 70_000, creditLineCents: 100_000, tier: 'A' as const,
      capitalBand: 'normal' as const, door: 'human' as const, reasonCodes: ['HUMAN_REVIEW_REQUIRED'] };
    const approved = { ...pending, reasonCodes: ['AUTO_APPROVED'] };
    expect(sameKit5ApprovalTerms(pending, approved)).toBe(true);
    expect(sameKit5ApprovalTerms(pending, { ...approved, amountCents: 69_999 })).toBe(false);
    expect(sameKit5ApprovalTerms(pending, { ...approved, creditLineCents: 99_999 })).toBe(false);
    expect(sameKit5ApprovalTerms(pending, { ...approved, door: 'closed' })).toBe(false);
  });
});
