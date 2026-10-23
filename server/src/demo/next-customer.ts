import { randomUUID } from "node:crypto";
import type { IssuingSimulationGateway, IssuingSimulationResponse } from "../airwallex/client.js";
import { ProviderHttpError } from "../airwallex/client.js";
import { AmbiguousIssuingResponseError, normalizeIssuingResponse, simulateIssuingTransaction, type IssuingSimulationInput } from "../airwallex/issuing.js";
import {
  acceptKit5CardAttempt,
  claimKit5Funding,
  claimKit5ProviderCreate,
  finishKit5Attempt,
  getKit5CardAttempt,
  invalidateKit5CheckpointResult,
  resolveKit5AttemptLifecycle,
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
  | { status: "rejected"; reason: string; attempt?: Kit5CardAttempt };

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
  options: { requireCheckpointIdentity?: boolean; allowNewCheckpointRun?: boolean; protectCheckpointRun?: boolean } = {},
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
    ...((options.requireCheckpointIdentity || options.protectCheckpointRun) ? {
      checkpointRun: { allowNewRun: options.protectCheckpointRun || options.allowNewCheckpointRun === true },
    } : {}),
  });
  if (!action.accepted) return {
    status: "rejected",
    reason: action.reason,
    ...("attempt" in action ? { attempt: action.attempt } : {}),
  };

  let attempt = action.attempt;
  let ownsProviderCreate = false;
  if (!action.created && attempt.status === "completed" && options.requireCheckpointIdentity) {
    let result: IssuingSimulationResponse | null = null;
    if (attempt.result) {
      try {
        result = decodeResult(attempt.result);
      } catch {
        // Malformed durable provider data remains unresolved and requires event reconciliation.
      }
    }
    if (result && matchesCheckpointResult(result, attempt.requestFacts, customer.accountId, attempt.lifecycleId)) {
      return { status: "completed", attempt, result };
    }
    await invalidateKit5CheckpointResult(pool, input);
    attempt = await requiredAttempt(pool, input);
  } else if (!action.created && attempt.status === "completed" && attempt.result) {
    if (!options.requireCheckpointIdentity) {
      await bridgeFunding?.recover?.({ ...input, accountId: customer.accountId, cardId: customer.cardId,
        attemptedAmountCents: customer.amountCents, amountCents: customer.amountCents, currency: customer.currency,
        providerResult: decodeResult(attempt.result) });
      const refreshed = await requiredAttempt(pool, input);
      const result = decodeResult(attempt.result);
      return isFundingPending(refreshed, result)
        ? { status: "in_progress", attempt: refreshed }
        : { status: "completed", attempt: refreshed, result };
    }
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
      if (options.requireCheckpointIdentity) {
        const result = await reconcileSavedCheckpointLifecycle(pool, gateway, input, existing, customer, policyFacts);
        if (result) return result;
        return { status: existing.status === "ambiguous" ? "ambiguous" : "in_progress", attempt: existing };
      }
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
  const checkpointLifecycleId = options.requireCheckpointIdentity ? randomUUID() : undefined;
  try {
    result = await simulateIssuingTransaction({
      ...customer,
      scenarioId: input.scenarioId,
      ...(checkpointLifecycleId ? { lifecycleId: checkpointLifecycleId } : {}),
    }, {
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
  if (options.requireCheckpointIdentity) {
    const persisted = await requiredAttempt(pool, input);
    if (!matchesCheckpointResult(result, persisted.requestFacts, customer.accountId, persisted.lifecycleId)) {
      await finishKit5Attempt(pool, { ...input, status: "ambiguous", error: "checkpoint_identity_unverified" });
      return { status: "ambiguous", attempt: await requiredAttempt(pool, input) };
    }
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
  options: { requireCheckpointIdentity?: boolean } = {},
): Promise<Kit5CardAttempt | null> {
  if (!fixture) return null;
  const customer = fixture.resolveCustomer(input.customerId);
  if (!customer) return null;
  let attempt = await getKit5CardAttempt(pool, input.scenarioId, input.actionId);
  if (!attempt || attempt.customerId !== input.customerId) return null;
  if (options.requireCheckpointIdentity && attempt.status === "completed" && attempt.result) {
    const policyFacts = fixture.policyFacts?.(input.customerId);
    const result = decodeResult(attempt.result);
    if (!policyFacts || !matchesTrustedCheckpointRequest(attempt.requestFacts, customer, policyFacts) ||
        !matchesCheckpointResult(result, attempt.requestFacts, customer.accountId, attempt.lifecycleId)) {
      await invalidateKit5CheckpointResult(pool, input);
      attempt = await requiredAttempt(pool, input);
    }
  }
  if ((attempt.status !== "in_progress" && attempt.status !== "ambiguous") || !attempt.lifecycleId) return attempt;
  if (options.requireCheckpointIdentity) {
    const policyFacts = fixture.policyFacts?.(input.customerId);
    if (!policyFacts) return attempt;
    const outcome = await reconcileSavedCheckpointLifecycle(pool, gateway, {
      ...input,
      customerId: input.customerId,
      actionId: input.actionId,
    }, attempt, customer, policyFacts);
    return outcome.status === "completed" ? outcome.attempt : await getKit5CardAttempt(pool, input.scenarioId, input.actionId);
  }
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

/** Resolve an attempt with no saved lifecycle by verifying an operator-found lifecycle in Airwallex. */
export async function reconcileKit5CheckpointLifecycle(
  pool: DatabasePool,
  gateway: IssuingSimulationGateway,
  input: Pick<NextCustomerInput, "scenarioId" | "customerId" | "actionId"> & { lifecycleId: string },
  fixture: Kit5CustomerFixture | null,
): Promise<NextCustomerOutcome> {
  if (!fixture) return { status: "rejected", reason: "unknown_scenario" };
  const customer = fixture.resolveCustomer(input.customerId);
  if (!customer) return { status: "rejected", reason: "customer_fixture_unmapped" };
  let attempt = await getKit5CardAttempt(pool, input.scenarioId, input.actionId);
  if (!attempt || attempt.customerId !== input.customerId) {
    return { status: "rejected", reason: "checkpoint_attempt_not_found" };
  }
  // Older attempts with no server-generated correlation ID cannot safely be
  // matched to an operator-supplied lifecycle by transaction terms alone.
  if (!attempt.lifecycleId) {
    return { status: "rejected", reason: "checkpoint_lifecycle_correlation_unavailable" };
  }
  if (attempt.lifecycleId && attempt.lifecycleId !== input.lifecycleId) {
    return { status: "rejected", reason: "checkpoint_lifecycle_conflict" };
  }
  if (attempt.status === "completed" && attempt.result) {
    const policyFacts = fixture.policyFacts?.(input.customerId);
    const result = decodeResult(attempt.result);
    if (policyFacts && matchesTrustedCheckpointRequest(attempt.requestFacts, customer, policyFacts) &&
        matchesCheckpointResult(result, attempt.requestFacts, customer.accountId, attempt.lifecycleId)) {
      return { status: "completed", attempt, result };
    }
    await invalidateKit5CheckpointResult(pool, input);
    attempt = await requiredAttempt(pool, input);
  }
  if (!["submitting", "in_progress", "ambiguous"].includes(attempt.status)) {
    return { status: "rejected", reason: "checkpoint_attempt_not_unresolved" };
  }
  const policyFacts = fixture.policyFacts?.(input.customerId);
  if (!policyFacts || !matchesTrustedCheckpointRequest(attempt.requestFacts, customer, policyFacts)) {
    return unresolvedCheckpoint(attempt);
  }

  let lifecycleEvents: unknown[] | null;
  try {
    lifecycleEvents = await gateway.getIssuingLifecycleEvents?.(input.lifecycleId, customer.accountId) ?? null;
  } catch {
    return { status: attempt.status === "ambiguous" ? "ambiguous" : "in_progress", attempt };
  }
  if (lifecycleEvents === null) return unresolvedCheckpoint(attempt);

  const matchingEvents = lifecycleEvents.filter((event) =>
    matchesCheckpointLifecycle(event, attempt!.requestFacts, customer.accountId, input.lifecycleId));
  if (matchingEvents.length !== 1) return unresolvedCheckpoint(attempt);

  let result: IssuingSimulationResponse;
  try {
    const event = normalizeCheckpointLifecycleEvent(matchingEvents[0], input.lifecycleId);
    if (!event) return unresolvedCheckpoint(attempt);
    result = normalizeIssuingResponse(event, input.lifecycleId);
  } catch {
    return { status: "ambiguous", attempt };
  }
  if (!attempt.lifecycleId) {
    const saved = await resolveKit5AttemptLifecycle(pool, input);
    if (!saved) {
      attempt = await requiredAttempt(pool, input);
      if (attempt.status === "completed" && attempt.result) {
        return { status: "completed", attempt, result: decodeResult(attempt.result) };
      }
      return { status: attempt.status === "ambiguous" ? "ambiguous" : "in_progress", attempt };
    }
  }
  await finishKit5Attempt(pool, { ...input, status: "completed", result: resultRecord(result) });
  return { status: "completed", attempt: await requiredAttempt(pool, input), result };
}

function matchesTrustedCheckpointRequest(
  saved: Kit5CardAttempt["requestFacts"],
  customer: Omit<IssuingSimulationInput, "scenarioId">,
  policyFacts: ReturnType<NonNullable<Kit5CustomerFixture["policyFacts"]>>,
): boolean {
  return saved != null && saved.accountId === customer.accountId && saved.cardId === customer.cardId &&
    saved.amountCents === customer.amountCents && saved.currency === customer.currency &&
    saved.merchant === customer.merchant && saved.transactionType === policyFacts.transactionType &&
    saved.transactionCategory === policyFacts.transactionCategory &&
    saved.merchantCategoryCode === policyFacts.merchantCategoryCode;
}

/** A provider lookup is scoped to the trusted connected account and must establish every checkpoint identity. */
function matchesCheckpointLifecycle(
  value: unknown,
  facts: Kit5CardAttempt["requestFacts"],
  scopedAccountId: string,
  expectedLifecycleId: string,
): boolean {
  if (facts == null || typeof value !== "object" || value === null || Array.isArray(value)) return false;
  const lifecycle = value as Record<string, unknown>;
  const merchant = typeof lifecycle.merchant === "object" && lifecycle.merchant !== null && !Array.isArray(lifecycle.merchant)
    ? lifecycle.merchant as Record<string, unknown>
    : null;
  const transactionAmount = lifecycle.transaction_amount;
  const amountCents = typeof transactionAmount === "number" && Number.isFinite(transactionAmount)
    ? Math.round(transactionAmount * 100)
    : null;
  const responseAccountId = lifecycle.account_id;
  const transactionCurrency = typeof lifecycle.transaction_currency === "string"
    ? lifecycle.transaction_currency.trim().toUpperCase()
    : "";
  const merchantName = typeof merchant?.name === "string" ? merchant.name.trim() : "";
  const merchantCategoryCode = typeof merchant?.category_code === "string"
    ? merchant.category_code.trim()
    : typeof lifecycle.merchant_category_code === "string"
      ? lifecycle.merchant_category_code.trim()
      : "";
  const transactionType = lifecycle.type ?? lifecycle.transaction_type;
  const responseLifecycleId = lifecycle.card_transaction_lifecycle_id ?? lifecycle.lifecycle_id;

  return facts.accountId === scopedAccountId &&
    (responseAccountId === undefined || responseAccountId === scopedAccountId) &&
    responseLifecycleId === expectedLifecycleId &&
    lifecycle.card_id === facts.cardId &&
    amountCents === facts.amountCents && typeof transactionAmount === "number" &&
    Math.abs(transactionAmount * 100 - facts.amountCents) <= 1e-6 &&
    transactionCurrency === facts.currency.toUpperCase() &&
    merchantName === facts.merchant &&
    merchantCategoryCode === facts.merchantCategoryCode &&
    transactionType === facts.transactionType &&
    lifecycle.transaction_category === facts.transactionCategory;
}

function normalizeCheckpointLifecycleEvent(value: unknown, expectedLifecycleId: string): Record<string, unknown> | null {
  if (typeof value !== "object" || value === null || Array.isArray(value)) return null;
  const event = value as Record<string, unknown>;
  const eventId = event.card_transaction_event_id ?? event.id;
  const lifecycleId = event.card_transaction_lifecycle_id ?? event.lifecycle_id;
  if (typeof eventId !== "string" || typeof lifecycleId !== "string" || lifecycleId !== expectedLifecycleId) return null;
  return {
    ...event,
    card_transaction_event_id: eventId,
    card_transaction_lifecycle_id: lifecycleId,
  };
}

function unresolvedCheckpoint(attempt: Kit5CardAttempt): NextCustomerOutcome {
  return { status: attempt.status === "ambiguous" ? "ambiguous" : "in_progress", attempt };
}

async function reconcileSavedCheckpointLifecycle(
  pool: DatabasePool,
  gateway: IssuingSimulationGateway,
  input: NextCustomerInput,
  attempt: Kit5CardAttempt,
  customer: Omit<IssuingSimulationInput, "scenarioId">,
  policyFacts: NonNullable<ReturnType<NonNullable<Kit5CustomerFixture["policyFacts"]>>>,
): Promise<NextCustomerOutcome> {
  if (!attempt.lifecycleId || !matchesTrustedCheckpointRequest(attempt.requestFacts, customer, policyFacts)) {
    return unresolvedCheckpoint(attempt);
  }

  let events: unknown[] | null;
  try {
    events = await gateway.getIssuingLifecycleEvents?.(attempt.lifecycleId, customer.accountId) ?? null;
  } catch {
    return unresolvedCheckpoint(attempt);
  }
  if (events === null) return unresolvedCheckpoint(attempt);

  const matchingEvents = events.filter((event) =>
    matchesCheckpointLifecycle(event, attempt.requestFacts, customer.accountId, attempt.lifecycleId!));
  if (matchingEvents.length !== 1) {
    if (events.length > 0) {
      await finishKit5Attempt(pool, { ...input, status: "ambiguous", error: "checkpoint_identity_unverified" });
      return { status: "ambiguous", attempt: await requiredAttempt(pool, input) };
    }
    return unresolvedCheckpoint(attempt);
  }

  try {
    const event = normalizeCheckpointLifecycleEvent(matchingEvents[0], attempt.lifecycleId);
    if (!event) return unresolvedCheckpoint(attempt);
    const result = normalizeIssuingResponse(event, attempt.lifecycleId);
    if (!matchesCheckpointResult(result, attempt.requestFacts, customer.accountId, attempt.lifecycleId)) {
      await finishKit5Attempt(pool, { ...input, status: "ambiguous", error: "checkpoint_identity_unverified" });
      return { status: "ambiguous", attempt: await requiredAttempt(pool, input) };
    }
    await finishKit5Attempt(pool, { ...input, status: "completed", result: resultRecord(result) });
    return { status: "completed", attempt: await requiredAttempt(pool, input), result };
  } catch {
    return unresolvedCheckpoint(attempt);
  }
}

function matchesCheckpointResult(
  result: IssuingSimulationResponse,
  facts: Kit5CardAttempt["requestFacts"],
  scopedAccountId: string,
  expectedLifecycleId: string | null,
): boolean {
  return facts != null && expectedLifecycleId !== null && facts.accountId === scopedAccountId &&
    (result.accountId === undefined || result.accountId === scopedAccountId) && result.cardId === facts.cardId &&
    result.transactionAmountCents === facts.amountCents &&
    result.transactionCurrency?.toUpperCase() === facts.currency.toUpperCase() &&
    result.merchantName === facts.merchant &&
    result.merchantCategoryCode === facts.merchantCategoryCode &&
    result.transactionType === facts.transactionType &&
    result.transactionCategory === facts.transactionCategory &&
    result.cardTransactionLifecycleId === expectedLifecycleId;
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
    ...(result.merchantName === undefined ? {} : { merchantName: result.merchantName }),
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
    ...(typeof result.merchantName === "string" ? { merchantName: result.merchantName } : {}),
    ...(typeof result.merchantCategoryCode === "string" ? { merchantCategoryCode: result.merchantCategoryCode } : {}),
    ...(typeof result.accountId === "string" ? { accountId: result.accountId } : {}),
    ...(typeof result.cardId === "string" ? { cardId: result.cardId } : {}),
    ...(typeof result.transactionAmountCents === "number" ? { transactionAmountCents: result.transactionAmountCents } : {}),
    ...(typeof result.transactionCurrency === "string" ? { transactionCurrency: result.transactionCurrency } : {}),
  };
}
