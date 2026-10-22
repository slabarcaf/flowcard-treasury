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

export type Kit5BridgePolicyDecision =
  | { status: "approved"; command: ReserveBridgeInput }
  | { status: "rejected" | "ineligible" };

/**
 * Optional bridge boundary supplied by the Kit 5 policy/API integration.
 * `evaluate` must return the policy-owned approved command, and
 * `reserveAndWaitForSettlement` must use the durable bridge ledger path.
 */
export interface Kit5BridgeFundingBoundary {
  evaluate(input: {
    scenarioId: string;
    customerId: string;
    actionId: string;
    attemptedAmountCents: number;
    providerResult: IssuingSimulationResponse;
  }): Promise<Kit5BridgePolicyDecision>;
  reserveAndWaitForSettlement(command: ReserveBridgeInput): Promise<{
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
  }): Promise<void>;
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
  const action = await acceptKit5CardAttempt(pool, { ...input, orderedCustomerIds: fixture.orderedCustomerIds });
  if (!action.accepted) return { status: "rejected", reason: action.reason };

  let attempt = action.attempt;
  let ownsProviderCreate = false;
  if (!action.created && attempt.status === "completed" && attempt.result) {
    return { status: "completed", attempt, result: decodeResult(attempt.result) };
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
      || (persisted.lifecycleId !== null && persisted.status === "in_progress"
        && !(error instanceof ProviderHttpError && error.status < 500))) {
      await finishKit5Attempt(pool, { ...input, status: "ambiguous", error: "provider_result_unresolved" });
      return { status: "ambiguous", attempt: await requiredAttempt(pool, input) };
    }
    if (persisted.lifecycleId && error instanceof ProviderHttpError && error.status < 500) {
      await finishKit5Attempt(pool, { ...input, status: "failed", error: error.name });
    }
    throw error;
  }
  await finishKit5Attempt(pool, { ...input, status: "completed", result: resultRecord(result) });
  const completedAttempt = await requiredAttempt(pool, input);
  await maybeFundAndRetry(pool, bridgeFunding, input, customer, result);
  return { status: "completed", attempt: completedAttempt, result };
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
  };
}
