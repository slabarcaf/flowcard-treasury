import type { IssuingSimulationGateway, IssuingSimulationResponse } from "../airwallex/client.js";
import { ProviderHttpError } from "../airwallex/client.js";
import { AmbiguousIssuingResponseError, normalizeIssuingResponse, simulateIssuingTransaction, type IssuingSimulationInput } from "../airwallex/issuing.js";
import {
  acceptKit5CardAttempt,
  claimKit5Funding,
  claimKit5ProviderCreate,
  finishKit5Attempt,
  getKit5CardAttempt,
  setKit5AttemptLifecycle,
  type Kit5CardAttempt,
} from "../db/repositories.js";
import type { DatabasePool } from "../db/pool.js";
import type { ReserveBridgeInput } from "../ledger/reservations.js";

export interface Kit5CustomerFixture {
  orderedCustomerIds: readonly string[];
  resolveCustomer: (customerId: string) => Omit<IssuingSimulationInput, "scenarioId"> | null;
  policyFacts?: (customerId: string) => { transactionType: "AUTHORIZATION"; transactionCategory: "PURCHASE"; merchantCategoryCode: string } | null;
}

export interface NextCustomerInput {
  scenarioId: string;
  customerId: string;
  actionId: string;
}

export type NextCustomerOutcome =
  | { status: "completed"; attempt: Kit5CardAttempt; result: IssuingSimulationResponse }
  | { status: "in_progress" | "ambiguous" | "failed"; attempt: Kit5CardAttempt }
  | { status: "rejected"; reason: string };

export interface Kit5PolicyInput {
  scenarioId: string;
  customerId: string;
  actionId: string;
  attemptedAmountCents: number;
  accountId: string;
  cardId: string;
  currency: string;
  providerResult: IssuingSimulationResponse;
}

export type Kit5BridgePolicyDecision =
  | { status: "approved"; command: ReserveBridgeInput & { scenarioId: string; actionId: string; policyInput: {
      accountId: string; cardId: string; attemptedAmountCents: number; currency: string; providerResult: IssuingSimulationResponse;
    } } }
  | { status: "rejected" | "ineligible" };

/**
 * Optional bridge boundary supplied by the Kit 5 policy/API integration.
 * `evaluate` must return the policy-owned approved command, and
 * `reserveAndWaitForSettlement` must use the durable bridge ledger path.
 */
export interface Kit5BridgeFundingBoundary {
  evaluate(input: Kit5PolicyInput): Promise<Kit5BridgePolicyDecision>;
  reserveAndWaitForSettlement(command: ReserveBridgeInput & { scenarioId: string; actionId: string; policyInput: {
    accountId: string; cardId: string; attemptedAmountCents: number; currency: string; providerResult: IssuingSimulationResponse;
  } }): Promise<{
    bridgeId: string;
    status: "settled" | "pending" | "failed";
  }>;
  retryCardAfterSettlement(input: {
    scenarioId: string;
    customerId: string;
    actionId: string;
    accountId: string;
    cardId: string;
    amountCents: number;
    currency: string;
    attemptedAmountCents: number;
    providerResult: IssuingSimulationResponse;
  }): Promise<void>;
  recover?(input: Kit5PolicyInput & { amountCents: number }): Promise<void>;
  approveHumanReview?(input: { scenarioId: string; customerId: string; actionId: string; actorId: string; reason: string }): Promise<{ status: "approved" | "pending" | "blocked"; bridgeId?: string }>;
}

export async function processNextCustomerAction(
  pool: DatabasePool,
  gateway: IssuingSimulationGateway,
  input: NextCustomerInput,
  fixture: Kit5CustomerFixture | null,
  bridgeFunding?: Kit5BridgeFundingBoundary,
): Promise<NextCustomerOutcome> {
  if (!fixture) return { status: "rejected", reason: "unknown_scenario" };
  const customer = fixture.resolveCustomer(input.customerId);
  if (!customer) return { status: "rejected", reason: "customer_fixture_unmapped" };
  const policyFacts = fixture.policyFacts?.(input.customerId);
  if (!policyFacts || !/^\d{4}$/.test(policyFacts.merchantCategoryCode)) {
    return { status: "rejected", reason: "trusted_policy_facts_unavailable" };
  }
  const action = await acceptKit5CardAttempt(pool, {
    ...input,
    orderedCustomerIds: fixture.orderedCustomerIds,
    requestFacts: { accountId: customer.accountId, cardId: customer.cardId,
      amountCents: customer.amountCents, currency: customer.currency, merchant: customer.merchant, ...policyFacts },
  });
  if (!action.accepted) return { status: "rejected", reason: action.reason };

  let attempt = action.attempt;
  let ownsProviderCreate = false;
  if (!action.created && attempt.status === "completed" && attempt.result) {
    await bridgeFunding?.recover?.({ ...input, accountId: customer.accountId, cardId: customer.cardId,
      attemptedAmountCents: customer.amountCents, amountCents: customer.amountCents, currency: customer.currency,
      providerResult: decodeResult(attempt.result) });
    const refreshed = await requiredAttempt(pool, input);
    const result = decodeResult(attempt.result);
    return isFundingPending(refreshed, result)
      ? { status: "in_progress", attempt: refreshed }
      : { status: "completed", attempt: refreshed, result };
  }
  if (!action.created && attempt.status === "failed") return { status: "failed", attempt };

  if (!action.created && attempt.status === "accepted" && attempt.lifecycleId === null) {
    if (await claimKit5ProviderCreate(pool, input)) {
      attempt = { ...attempt, status: "submitting" };
      ownsProviderCreate = true;
    } else {
      attempt = await requiredAttempt(pool, input);
    }
  } else if (action.created) {
    if (!(await claimKit5ProviderCreate(pool, input))) {
      return { status: "in_progress", attempt: await requiredAttempt(pool, input) };
    }
    attempt = { ...attempt, status: "submitting" };
    ownsProviderCreate = true;
  }

  if (!ownsProviderCreate && attempt.status !== "submitting" && attempt.status !== "in_progress" && attempt.status !== "ambiguous") {
    return { status: "in_progress", attempt };
  }
  if (!ownsProviderCreate) {
    const existing = attempt;
    if ((existing.status === "in_progress" || existing.status === "ambiguous") && existing.lifecycleId) {
      try {
        const lifecycle = await gateway.getIssuingLifecycle(existing.lifecycleId, customer.accountId);
        if (lifecycle !== null) {
          const result = normalizeIssuingResponse(lifecycle, existing.lifecycleId);
          await finishKit5Attempt(pool, { ...input, status: "completed", result: resultRecord(result) });
          const refreshed = await requiredAttempt(pool, input);
          return { status: "completed", attempt: refreshed, result };
        }
      } catch {
        // Keep the durable unresolved state. A lifecycle lookup failure never resubmits the create.
      }
    }
    return { status: existing.status === "ambiguous" ? "ambiguous" : "in_progress", attempt: existing };
  }

  let result: IssuingSimulationResponse;
  try {
    result = await simulateIssuingTransaction({ ...customer, scenarioId: input.scenarioId }, {
      gateway,
      persistLifecycleId: async (scenarioId, lifecycleId) => {
        await setKit5AttemptLifecycle(pool, { scenarioId, actionId: input.actionId, lifecycleId });
      },
    });
  } catch (error) {
    const persisted = await requiredAttempt(pool, input);
    if (error instanceof AmbiguousIssuingResponseError
      || (persisted.status === "submitting" && !(error instanceof ProviderHttpError && error.status < 500))) {
      if (error instanceof AmbiguousIssuingResponseError && error.lifecycleId && !persisted.lifecycleId) {
        await setKit5AttemptLifecycle(pool, {
          scenarioId: input.scenarioId,
          actionId: input.actionId,
          lifecycleId: error.lifecycleId,
        }).catch(() => {});
      }
      await finishKit5Attempt(pool, { ...input, status: "ambiguous", error: "provider_result_unresolved" });
      return { status: "ambiguous", attempt: await requiredAttempt(pool, input) };
    }
    if (persisted.status === "submitting" && error instanceof ProviderHttpError && error.status < 500) {
      await finishKit5Attempt(pool, { ...input, status: "failed", error: error.name });
    }
    throw error;
  }
  await finishKit5Attempt(pool, { ...input, status: "completed", result: resultRecord(result) });
  await maybeFundAndRetry(pool, bridgeFunding, input, customer, result);
  const finalAttempt = await requiredAttempt(pool, input);
  return isFundingPending(finalAttempt, result)
    ? { status: "in_progress", attempt: finalAttempt }
    : { status: "completed", attempt: finalAttempt, result };
}

export async function reconcileKit5CustomerAction(
  pool: DatabasePool,
  gateway: IssuingSimulationGateway,
  input: Pick<NextCustomerInput, "scenarioId" | "customerId" | "actionId">,
  fixture: Kit5CustomerFixture | null,
): Promise<Kit5CardAttempt | null> {
  if (!fixture) return null;
  const customer = fixture.resolveCustomer(input.customerId);
  if (!customer) return null;
  const attempt = await getKit5CardAttempt(pool, input.scenarioId, input.actionId);
  if (!attempt || attempt.customerId !== input.customerId) return null;
  if ((attempt.status !== "in_progress" && attempt.status !== "ambiguous") || !attempt.lifecycleId) return attempt;
  try {
    const lifecycle = await gateway.getIssuingLifecycle(attempt.lifecycleId, customer.accountId);
    if (lifecycle === null) return attempt;
    const result = normalizeIssuingResponse(lifecycle, attempt.lifecycleId);
    await finishKit5Attempt(pool, {
      ...input,
      status: "completed",
      result: resultRecord(result),
    });
    return await getKit5CardAttempt(pool, input.scenarioId, input.actionId);
  } catch {
    return attempt;
  }
}

async function maybeFundAndRetry(
  pool: DatabasePool,
  boundary: Kit5BridgeFundingBoundary | undefined,
  input: NextCustomerInput,
  customer: Omit<IssuingSimulationInput, "scenarioId">,
  providerResult: IssuingSimulationResponse,
): Promise<void> {
  if (!boundary
    || providerResult.processResult !== "DECLINED"
    || providerResult.failureReason !== "INSUFFICIENT_FUNDS") return;
  if (!(await claimKit5Funding(pool, input))) return;

  const decision = await boundary.evaluate({
    ...input,
    attemptedAmountCents: customer.amountCents,
    accountId: customer.accountId,
    cardId: customer.cardId,
    currency: customer.currency,
    providerResult,
  });
  if (decision.status !== "approved") return;
  validateApprovedCommand(decision.command, input.customerId);

  const funding = await boundary.reserveAndWaitForSettlement(decision.command);
  if (funding.status !== "settled") return;
  await boundary.retryCardAfterSettlement({
    ...input,
    accountId: customer.accountId,
    cardId: customer.cardId,
    amountCents: customer.amountCents,
    currency: customer.currency,
    attemptedAmountCents: customer.amountCents,
    providerResult,
  });
}

function validateApprovedCommand(command: ReserveBridgeInput, customerId: string): void {
  if (command.customerId !== customerId) throw new Error("Policy approved a bridge for a different customer");
  if (!Number.isSafeInteger(command.amountCents) || command.amountCents <= 0) {
    throw new Error("Policy approved an invalid bridge amount");
  }
  if (!/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(command.bridgeId)) {
    throw new Error("Policy approved an invalid bridge ID");
  }
}

function isFundingPending(attempt: Kit5CardAttempt, result: IssuingSimulationResponse): boolean {
  return result.processResult === "DECLINED" && result.failureReason === "INSUFFICIENT_FUNDS" &&
    (["evaluating", "pending_review", "reserving", "reserved", "settled", "retry_submitting", "retry_ambiguous"].includes(attempt.policyStatus) ||
      (attempt.fundingClaimed && attempt.policyStatus === "not_started"));
}

async function requiredAttempt(pool: DatabasePool, input: NextCustomerInput): Promise<Kit5CardAttempt> {
  const attempt = await getKit5CardAttempt(pool, input.scenarioId, input.actionId);
  if (!attempt) throw new Error("Kit 5 card attempt disappeared after persistence");
  return attempt;
}

function resultRecord(result: IssuingSimulationResponse): Record<string, unknown> {
  return {
    processResult: result.processResult,
    failureReason: result.failureReason,
    cardTransactionEventId: result.cardTransactionEventId,
    cardTransactionId: result.cardTransactionId,
    cardTransactionLifecycleId: result.cardTransactionLifecycleId,
    ...(result.transactionType === undefined ? {} : { transactionType: result.transactionType }),
    ...(result.transactionCategory === undefined ? {} : { transactionCategory: result.transactionCategory }),
    ...(result.merchantCategoryCode === undefined ? {} : { merchantCategoryCode: result.merchantCategoryCode }),
    ...(result.accountId === undefined ? {} : { accountId: result.accountId }),
    ...(result.cardId === undefined ? {} : { cardId: result.cardId }),
    ...(result.transactionAmountCents === undefined ? {} : { transactionAmountCents: result.transactionAmountCents }),
    ...(result.transactionCurrency === undefined ? {} : { transactionCurrency: result.transactionCurrency }),
  };
}

function decodeResult(result: Record<string, unknown>): IssuingSimulationResponse {
  if ((result.processResult !== "APPROVED" && result.processResult !== "DECLINED")
    || typeof result.cardTransactionEventId !== "string"
    || typeof result.cardTransactionId !== "string"
    || typeof result.cardTransactionLifecycleId !== "string"
    || !(typeof result.failureReason === "string" || result.failureReason === null)) {
    throw new Error("Stored Kit 5 provider result is malformed");
  }
  return {
    processResult: result.processResult,
    failureReason: result.failureReason,
    cardTransactionEventId: result.cardTransactionEventId,
    cardTransactionId: result.cardTransactionId,
    cardTransactionLifecycleId: result.cardTransactionLifecycleId,
    ...(result.transactionType === "AUTHORIZATION" || result.transactionType === "CLEARING" ? { transactionType: result.transactionType } : {}),
    ...(typeof result.transactionCategory === "string" ? { transactionCategory: result.transactionCategory } : {}),
    ...(typeof result.merchantCategoryCode === "string" ? { merchantCategoryCode: result.merchantCategoryCode } : {}),
    ...(typeof result.accountId === "string" ? { accountId: result.accountId } : {}),
    ...(typeof result.cardId === "string" ? { cardId: result.cardId } : {}),
    ...(typeof result.transactionAmountCents === "number" ? { transactionAmountCents: result.transactionAmountCents } : {}),
    ...(typeof result.transactionCurrency === "string" ? { transactionCurrency: result.transactionCurrency } : {}),
  };
}
