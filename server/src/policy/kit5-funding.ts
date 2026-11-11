import { randomUUID } from "node:crypto";
import type { AirwallexGateway, IssuingSimulationGateway, IssuingSimulationResponse } from "../airwallex/client.js";
import { normalizeIssuingResponse } from "../airwallex/issuing.js";
import type { DatabasePool } from "../db/pool.js";
import type { Kit5CardAttempt } from "../db/repositories.js";
import {
  claimKit5Retry,
  finishKit5Retry,
  getKit5CardAttempt,
  setKit5FundingSettled,
  setKit5PolicyBlocked,
  setKit5PolicyOutcome,
} from "../db/repositories.js";
import { reservePolicyBridge, type ReserveBridgeInput } from "../ledger/reservations.js";
import { evaluateBridgeRequest, type RequestDecision } from "./decision-service.js";
import { TRACEABLE_PURCHASE_MCCS, type AirwallexRemoteAuthorizationV2 } from "../http/routes/remote-authorization.js";
import { confirmKit5PolicyDispatch, loadRequestFacts, loadPolicyState, persistDecision, stableId, stableBridgeId, toBridgeRequest } from "./remote-authorization-data.js";
import type { Kit5PolicyInput } from "../demo/next-customer.js";

export type Kit5PolicyCommand = ReserveBridgeInput & {
  scenarioId: string;
  actionId: string;
  policyInput: Omit<Kit5PolicyInput, "scenarioId" | "actionId" | "customerId">;
};

/** Read-only policy evaluation followed by a separate durable reserve and gated dispatch. */
export function createKit5PolicyFundingBoundary(input: {
  pool: DatabasePool;
  gateway: AirwallexGateway & IssuingSimulationGateway;
  repaymentTerm?: ReserveBridgeInput["repaymentTerm"];
  now?: () => Date;
}) {
  return {
    async evaluate(action: Kit5PolicyInput): Promise<{ status: "approved"; command: Kit5PolicyCommand } | { status: "rejected" | "ineligible" }> {
      const attempt = await getKit5CardAttempt(input.pool, action.scenarioId, action.actionId);
      if (!attempt || attempt.status !== "completed" || attempt.customerId !== action.customerId) return { status: "rejected" };
      if (!input.repaymentTerm) {
        await setKit5PolicyBlocked(input.pool, action, "repayment_term_unconfigured");
        return { status: "rejected" };
      }
      if (!isKit5ActionSourceConsistent(attempt, action)) {
        await setKit5PolicyBlocked(input.pool, action, "saved_provider_action_facts_mismatch");
        return { status: "rejected" };
      }
      const trusted = await loadKit5TrustedRequest(input.pool, action);
      if (!trusted) {
        await setKit5PolicyBlocked(input.pool, action, "active_binding_or_fresh_policy_inputs_unavailable");
        return { status: "rejected" };
      }
      let balance: Awaited<ReturnType<AirwallexGateway["getBalance"]>>;
      try {
        balance = await input.gateway.getBalance(trusted.accountId, "USD");
      } catch {
        await setKit5PolicyBlocked(input.pool, action, "current_usd_balance_unavailable");
        return { status: "rejected" };
      }
      const state = await loadPolicyState(input.pool, trusted, trusted.request);
      if (!state) {
        await setKit5PolicyBlocked(input.pool, action, "current_policy_or_ledger_state_unavailable");
        return { status: "rejected" };
      }
      const request = toBridgeRequest(trusted.request, action.customerId);
      const decision = await evaluateBridgeRequest({ request, state, readCurrentBalance: async () => balance, now: input.now?.() ?? new Date() });
      const preDecisionId = stableId("kit5-predecision", JSON.stringify([
        actionKey(action, trusted.request.cardTransactionEventId), trusted.evaluationId, state.ledgerSequence,
        balance.availableCents, decision.outcome, decision.amountCents, decision.creditLineCents,
        decision.tier, decision.capitalBand, decision.door, decision.reasonCodes,
      ]));
      await persistDecision(input.pool, preDecisionId, trusted.evaluationId, trusted.request.cardTransactionEventId, decision);
      if (decision.outcome === "human_review") {
        await setKit5PolicyOutcome(input.pool, action, { status: "pending_review", decisionId: preDecisionId,
          pendingDecisionId: preDecisionId, bridgeId: null });
        return { status: "ineligible" };
      }
      if (decision.outcome !== "auto_approved" || decision.amountCents <= 0) {
        await setKit5PolicyOutcome(input.pool, action, { status: decision.outcome === "blocked" ? "blocked" : "rejected",
          decisionId: preDecisionId, bridgeId: null });
        return { status: "ineligible" };
      }
      return {
        status: "approved",
        command: {
          scenarioId: action.scenarioId,
          actionId: action.actionId,
          customerId: action.customerId,
          bridgeId: stableBridgeId(trusted.request.cardTransactionEventId),
          amountCents: decision.amountCents,
          repaymentTerm: input.repaymentTerm,
          policyInput: {
            accountId: action.accountId,
            cardId: action.cardId,
            attemptedAmountCents: action.attemptedAmountCents,
            currency: action.currency,
            providerResult: action.providerResult,
          },
        },
      };
    },

    async reserveAndWaitForSettlement(command: Kit5PolicyCommand) {
      const action = { ...command.policyInput, ...command, customerId: command.customerId } as Kit5PolicyInput;
      const trusted = await loadKit5TrustedRequest(input.pool, action);
      if (!trusted) return { bridgeId: command.bridgeId, status: "failed" as const };
      const balance = await input.gateway.getBalance(trusted.accountId, "USD");
      const decisionId = stableId("kit5-reservation-decision", actionKey(action, trusted.request.cardTransactionEventId));
      await setKit5PolicyOutcome(input.pool, action, { status: "reserving", decisionId, bridgeId: command.bridgeId });
      const reservation = await reservePolicyBridge(input.pool, {
        bridgeId: command.bridgeId,
        customerId: command.customerId,
        amountCents: command.amountCents,
        repaymentTerm: command.repaymentTerm,
        decisionId,
        requestId: trusted.request.cardTransactionEventId,
        currentBalance: balance,
        revalidate: async (context) => {
          const current = await loadKit5TrustedRequest(context.client, action);
          if (!current || current.precheck.profileId !== trusted.precheck.profileId ||
              current.precheck.snapshotId !== context.snapshotId || current.precheck.evidenceRunId !== context.evidenceRunId) {
            throw new Error("Kit 5 trusted action or current policy inputs changed before reservation");
          }
          const lockedState = await loadPolicyState(context.client, current, current.request,
            context.deployableCents, context.openBridgeCount);
          if (!lockedState) throw new Error("Current Kit 5 policy or ledger state is unavailable");
          const lockedDecision = await evaluateBridgeRequest({
            request: toBridgeRequest(current.request, action.customerId),
            state: lockedState,
            readCurrentBalance: async () => balance,
            now: input.now?.() ?? new Date(),
          });
          return { decision: lockedDecision, precheck: current.precheck };
        },
      });
      if (reservation.decision.outcome === "human_review") {
        await setKit5PolicyOutcome(input.pool, action, { status: "pending_review", decisionId,
          pendingDecisionId: decisionId, bridgeId: null, clearApproval: true });
        return { bridgeId: command.bridgeId, status: "failed" as const };
      }
      if (reservation.decision.outcome !== "auto_approved" || !reservation.reservation ||
          reservation.decision.amountCents !== command.amountCents) {
        await setKit5PolicyOutcome(input.pool, action, { status: reservation.decision.outcome === "blocked" ? "blocked" : "rejected",
          decisionId, bridgeId: null, clearApproval: true });
        return { bridgeId: command.bridgeId, status: "failed" as const };
      }
      await setKit5PolicyOutcome(input.pool, action, { status: "reserved", decisionId, bridgeId: command.bridgeId });
      if (!await confirmKit5PolicyDispatch(input.pool, action, trusted.request, decisionId, reservation.decision.amountCents)) {
        throw new Error("Committed Kit 5 policy decision could not safely release its transfer");
      }
      return await observeSettlement(input.pool, action, command.bridgeId);
    },

    async retryCardAfterSettlement(action: Kit5PolicyInput & { amountCents: number }) {
      await performDurableRetry(input.pool, input.gateway, action);
    },

    async approveHumanReview(approval: { scenarioId: string; customerId: string; actionId: string; actorId: string; reason: string }) {
      if (!approval.actorId.trim() || !approval.reason.trim() || !input.repaymentTerm) return { status: "blocked" as const };
      const attempt = await getKit5CardAttempt(input.pool, approval.scenarioId, approval.actionId);
      if (!attempt || attempt.customerId !== approval.customerId || attempt.status !== "completed" ||
          !["pending_review", "reserving"].includes(attempt.policyStatus) || !attempt.policyDecisionId || !attempt.result || !attempt.requestFacts) {
        return { status: "blocked" as const };
      }
      const pendingDecisionId = attempt.pendingPolicyDecisionId ?? attempt.policyDecisionId;
      if ((attempt.policyActorId && attempt.policyActorId !== approval.actorId) ||
          (attempt.policyApprovalReason && attempt.policyApprovalReason !== approval.reason)) return { status: "blocked" as const };
      const actorId = attempt.policyActorId ?? approval.actorId;
      const reason = attempt.policyApprovalReason ?? approval.reason;
      const providerResult = decodeStoredResult(attempt.result);
      const action: Kit5PolicyInput = {
        scenarioId: approval.scenarioId, customerId: approval.customerId, actionId: approval.actionId,
        attemptedAmountCents: attempt.requestFacts.amountCents, accountId: attempt.requestFacts.accountId,
        cardId: attempt.requestFacts.cardId, currency: attempt.requestFacts.currency, providerResult,
      };
      const priorDecision = await input.pool.query<{ request_id: string; customer_id: string; outcome: string; amount_cents: number | string;
        credit_line_cents: number | string; tier: RequestDecision["tier"]; capital_band: RequestDecision["capitalBand"];
        door: RequestDecision["door"]; reason_codes: RequestDecision["reasonCodes"] }>(
        `SELECT request_id, customer_id, outcome, amount_cents, credit_line_cents, tier, capital_band, door, reason_codes
           FROM policy_request_evaluations WHERE decision_id = $1::uuid`,
        [pendingDecisionId],
      );
      const pending = priorDecision.rows[0];
      if (!pending || pending.customer_id !== approval.customerId || pending.request_id !== providerResult.cardTransactionEventId ||
          pending.outcome !== "human_review" || !Number.isSafeInteger(Number(pending.amount_cents)) || Number(pending.amount_cents) <= 0) {
        return { status: "blocked" as const };
      }
      const trusted = await loadKit5TrustedRequest(input.pool, action);
      if (!trusted) return { status: "blocked" as const };
      const currentBalance = await input.gateway.getBalance(trusted.accountId, "USD");
      const state = await loadPolicyState(input.pool, trusted, trusted.request);
      if (!state) return { status: "blocked" as const };
      const humanApproval = { requestId: trusted.request.cardTransactionEventId,
        amountCents: Number(pending.amount_cents), actorId, reason };
      const evaluated = await evaluateBridgeRequest({ request: toBridgeRequest(trusted.request, approval.customerId), state,
        readCurrentBalance: async () => currentBalance, humanApproval, now: input.now?.() ?? new Date() });
      const persistedTerms = pending ? {
        amountCents: Number(pending.amount_cents), creditLineCents: Number(pending.credit_line_cents), tier: pending.tier,
        capitalBand: pending.capital_band, door: pending.door, reasonCodes: pending.reason_codes,
      } : null;
      if (evaluated.outcome !== "auto_approved" || evaluated.amountCents !== humanApproval.amountCents ||
          !persistedTerms || !sameKit5ApprovalTerms(persistedTerms, evaluated)) {
        return { status: "blocked" as const };
      }
      const decisionId = stableId("kit5-operator-approval", `${actionKey(action, trusted.request.cardTransactionEventId)}\0${approval.actorId}\0${approval.reason}`);
      const bridgeId = stableBridgeId(trusted.request.cardTransactionEventId);
      await setKit5PolicyOutcome(input.pool, approval, { status: "reserving", decisionId, bridgeId,
        pendingDecisionId, actorId, reason });
      const reservation = await reservePolicyBridge(input.pool, {
        bridgeId, customerId: approval.customerId, amountCents: humanApproval.amountCents,
        repaymentTerm: input.repaymentTerm, decisionId, requestId: trusted.request.cardTransactionEventId,
        currentBalance, humanApproval,
        revalidate: async (context) => {
          const latest = await loadKit5TrustedRequest(context.client, action);
          if (!latest || latest.precheck.profileId !== trusted.precheck.profileId ||
              latest.precheck.snapshotId !== context.snapshotId || latest.precheck.evidenceRunId !== context.evidenceRunId) {
            throw new Error("Kit 5 approval facts or policy inputs changed before reservation");
          }
          const lockedState = await loadPolicyState(context.client, latest, latest.request,
            context.deployableCents, context.openBridgeCount);
          if (!lockedState) throw new Error("Current Kit 5 policy or ledger state is unavailable");
          const lockedDecision = await evaluateBridgeRequest({ request: toBridgeRequest(latest.request, approval.customerId),
            state: lockedState, readCurrentBalance: async () => currentBalance, humanApproval,
            now: input.now?.() ?? new Date() });
          if (lockedDecision.outcome !== "auto_approved" || !persistedTerms ||
              !sameKit5ApprovalTerms(persistedTerms, lockedDecision)) {
            throw new Error("Kit 5 approval terms changed before reservation");
          }
          return { decision: lockedDecision, precheck: latest.precheck };
        },
      });
      if (!reservation.reservation || reservation.decision.outcome !== "auto_approved" ||
          reservation.decision.amountCents !== humanApproval.amountCents) {
        if (reservation.decision.outcome === "human_review") {
          await setKit5PolicyOutcome(input.pool, approval, { status: "pending_review", decisionId,
            pendingDecisionId: decisionId, bridgeId: null, clearApproval: true });
        } else {
          await setKit5PolicyOutcome(input.pool, approval, { status: "rejected", decisionId, bridgeId: null, clearApproval: true });
        }
        return { status: "blocked" as const };
      }
      await setKit5PolicyOutcome(input.pool, approval, { status: "reserved", decisionId, bridgeId });
      if (!await confirmKit5PolicyDispatch(input.pool, action, trusted.request, decisionId, humanApproval.amountCents,
        { actorId: approval.actorId, reason: approval.reason })) throw new Error("Approved Kit 5 transfer could not be released");
      const settlement = await observeSettlement(input.pool, action, bridgeId);
      return { status: settlement.status === "settled" ? "approved" as const : "pending" as const, bridgeId };
    },

    async recover(action: Kit5PolicyInput & { amountCents: number }) {
      const attempt = await getKit5CardAttempt(input.pool, action.scenarioId, action.actionId);
      if (!attempt) return;
      if (attempt.policyStatus === "reserved" && attempt.bridgeId) {
        await releaseCommittedDispatch(input.pool, action, attempt);
        const settlement = await observeSettlement(input.pool, action, attempt.bridgeId);
        if (settlement.status === "settled") await performDurableRetry(input.pool, input.gateway, action);
      } else if (attempt.policyStatus === "reserving" && attempt.bridgeId && attempt.policyDecisionId) {
        const resumed = await resumeCommittedReservation(input.pool, input.gateway, input.repaymentTerm, action, attempt);
        if (!resumed && attempt.policyActorId && attempt.policyApprovalReason) {
          const result = await this.approveHumanReview({ ...action, actorId: attempt.policyActorId, reason: attempt.policyApprovalReason });
          if (result.status === "approved") await performDurableRetry(input.pool, input.gateway, action);
        } else if (!resumed) {
          const decision = await this.evaluate(action);
          if (decision.status === "approved") {
            const settlement = await this.reserveAndWaitForSettlement(decision.command);
            if (settlement.status === "settled") await performDurableRetry(input.pool, input.gateway, action);
          }
        }
      } else if (attempt.policyStatus === "settled") {
        await performDurableRetry(input.pool, input.gateway, action);
      } else if (attempt.policyStatus === "retry_submitting" || attempt.policyStatus === "retry_ambiguous") {
        await reconcileDurableRetry(input.pool, input.gateway, action);
      } else if (attempt.policyStatus === "blocked" && attempt.bridgeId && attempt.policyDecisionId) {
        const resumed = await resumeCommittedReservation(input.pool, input.gateway, input.repaymentTerm, action, attempt);
        if (resumed) return;
        const decision = await this.evaluate(action);
        if (decision.status === "approved") {
          const settlement = await this.reserveAndWaitForSettlement(decision.command);
          if (settlement.status === "settled") await performDurableRetry(input.pool, input.gateway, action);
        }
      } else if (attempt.policyStatus === "evaluating" || attempt.policyStatus === "blocked") {
        const decision = await this.evaluate(action);
        if (decision.status === "approved") {
          const settlement = await this.reserveAndWaitForSettlement(decision.command);
          if (settlement.status === "settled") await performDurableRetry(input.pool, input.gateway, action);
        }
      }
    },
  };
}

/** Restart-safe reconciliation for durable funding and retry stages. */
export async function recoverPendingKit5Funding(
  pool: DatabasePool,
  boundary: Pick<ReturnType<typeof createKit5PolicyFundingBoundary>, "recover">,
  limit = 50,
): Promise<void> {
  const pending = await pool.query<{
    scenario_id: string; customer_id: string; action_id: string;
    request_facts: NonNullable<Awaited<ReturnType<typeof getKit5CardAttempt>>>["requestFacts"];
    provider_result: Record<string, unknown>;
  }>(
    `SELECT scenario_id, customer_id, action_id, request_facts, provider_result
       FROM kit5_card_attempts
      WHERE status = 'completed'
        AND policy_status IN ('evaluating', 'blocked', 'reserving', 'reserved', 'settled', 'retry_submitting', 'retry_ambiguous')
      ORDER BY updated_at LIMIT $1`, [limit]);
  for (const row of pending.rows) {
    if (!row.request_facts || !row.provider_result) continue;
    try {
      const providerResult = decodeStoredResult(row.provider_result);
      await boundary.recover({
        scenarioId: row.scenario_id, customerId: row.customer_id, actionId: row.action_id,
        accountId: row.request_facts.accountId, cardId: row.request_facts.cardId,
        attemptedAmountCents: row.request_facts.amountCents, amountCents: row.request_facts.amountCents,
        currency: row.request_facts.currency, providerResult,
      });
    } catch {
      // Keep the persisted stage unresolved; the next worker pass or operator refresh can reconcile it.
    }
  }
}

async function resumeCommittedReservation(
  pool: DatabasePool,
  gateway: AirwallexGateway & IssuingSimulationGateway,
  repaymentTerm: ReserveBridgeInput["repaymentTerm"] | undefined,
  action: Kit5PolicyInput & { amountCents: number },
  attempt: NonNullable<Awaited<ReturnType<typeof getKit5CardAttempt>>>,
): Promise<boolean> {
  if (!repaymentTerm || !attempt.bridgeId || !attempt.policyDecisionId) return false;
  const committed = await pool.query<{ request_id: string; customer_id: string; outcome: string; amount_cents: string | number; actor_id: string | null; approval_reason: string | null }>(
    `SELECT request_id, customer_id, outcome, amount_cents, actor_id, approval_reason
       FROM policy_request_evaluations WHERE decision_id = $1::uuid`, [attempt.policyDecisionId]);
  const row = committed.rows[0];
  if (!row || row.customer_id !== action.customerId || row.request_id !== action.providerResult.cardTransactionEventId) return false;
  if (row.outcome !== "auto_approved") {
    const status = row.outcome === "human_review" ? "pending_review" : row.outcome === "blocked" ? "blocked" : "rejected";
    await setKit5PolicyOutcome(pool, action, { status, decisionId: attempt.policyDecisionId,
      pendingDecisionId: row.outcome === "human_review" ? attempt.policyDecisionId : undefined,
      bridgeId: null, clearApproval: true });
    return true;
  }
  const bridgeAmountCents = Number(row.amount_cents);
  if (!Number.isSafeInteger(bridgeAmountCents) || bridgeAmountCents <= 0) return false;
  const trusted = await loadKit5TrustedRequest(pool, action);
  if (!trusted) return false;
  const balance = await gateway.getBalance(trusted.accountId, "USD");
  const command = { scenarioId: action.scenarioId, actionId: action.actionId, customerId: action.customerId,
    bridgeId: attempt.bridgeId, amountCents: bridgeAmountCents, repaymentTerm,
    policyInput: { accountId: action.accountId, cardId: action.cardId,
      attemptedAmountCents: action.attemptedAmountCents, currency: action.currency, providerResult: action.providerResult } };
  const reservation = await reservePolicyBridge(pool, {
    bridgeId: command.bridgeId, customerId: command.customerId, amountCents: command.amountCents,
    repaymentTerm, decisionId: attempt.policyDecisionId, requestId: trusted.request.cardTransactionEventId,
    currentBalance: balance,
    revalidate: async () => { throw new Error("Unexpected revalidation while recovering an existing Kit 5 reservation"); },
  });
  if (!reservation.reservation) return false;
  await setKit5PolicyOutcome(pool, action, { status: "reserved", decisionId: attempt.policyDecisionId, bridgeId: attempt.bridgeId,
    actorId: row.actor_id, reason: row.approval_reason });
  if (!await confirmKit5PolicyDispatch(pool, action, trusted.request, attempt.policyDecisionId, command.amountCents,
    row.actor_id && row.approval_reason ? { actorId: row.actor_id, reason: row.approval_reason } : undefined)) return false;
  const settlement = await observeSettlement(pool, action, attempt.bridgeId);
  if (settlement.status === "settled") await performDurableRetry(pool, gateway, action);
  return true;
}

async function releaseCommittedDispatch(
  pool: DatabasePool,
  action: Kit5PolicyInput & { amountCents: number },
  attempt: NonNullable<Awaited<ReturnType<typeof getKit5CardAttempt>>>,
): Promise<boolean> {
  if (!attempt.policyDecisionId || !attempt.bridgeId) return false;
  const decision = await pool.query<{ amount_cents: string | number; actor_id: string | null; approval_reason: string | null; outcome: string }>(
    `SELECT amount_cents, actor_id, approval_reason, outcome FROM policy_request_evaluations WHERE decision_id = $1::uuid`,
    [attempt.policyDecisionId]);
  const row = decision.rows[0];
  if (!row || row.outcome !== "auto_approved") return false;
  const trusted = await loadKit5TrustedRequest(pool, action);
  if (!trusted) return false;
  return confirmKit5PolicyDispatch(pool, action, trusted.request, attempt.policyDecisionId, Number(row.amount_cents),
    row.actor_id && row.approval_reason ? { actorId: row.actor_id, reason: row.approval_reason } : undefined);
}

async function observeSettlement(pool: DatabasePool, action: Kit5PolicyInput, bridgeId: string) {
  const projection = await pool.query<{ status: string }>(
    "SELECT status FROM bridge_projections WHERE bridge_id = $1", [bridgeId]);
  if (projection.rows[0]?.status !== "funded") return { bridgeId, status: "pending" as const };
  if (!await setKit5FundingSettled(pool, { scenarioId: action.scenarioId, actionId: action.actionId, bridgeId })) {
    const attempt = await getKit5CardAttempt(pool, action.scenarioId, action.actionId);
    if (attempt?.policyStatus !== "settled" && attempt?.policyStatus !== "retry_submitting" &&
        attempt?.policyStatus !== "retry_ambiguous" && attempt?.policyStatus !== "retry_completed") {
      return { bridgeId, status: "pending" as const };
    }
  }
  return { bridgeId, status: "settled" as const };
}

export async function performDurableRetry(pool: DatabasePool, gateway: AirwallexGateway & IssuingSimulationGateway, action: Kit5PolicyInput & { amountCents: number }): Promise<void> {
  const attempt = await getKit5CardAttempt(pool, action.scenarioId, action.actionId);
  if (!attempt?.requestFacts || attempt.policyStatus !== "settled") return;
  const lifecycleId = randomUUID();
  const retryRequestId = `kit5-retry-${stableId("kit5-retry", actionKey(action, action.providerResult.cardTransactionEventId))}`;
  if (!await claimKit5Retry(pool, { scenarioId: action.scenarioId, actionId: action.actionId, lifecycleId, requestId: retryRequestId })) return;
  try {
    const result = normalizeIssuingResponse(await gateway.createIssuingSimulation({
      accountId: attempt.requestFacts.accountId,
      cardId: attempt.requestFacts.cardId,
      amountCents: attempt.requestFacts.amountCents,
      currency: "USD",
      merchant: attempt.requestFacts.merchant,
      lifecycleId,
    }), lifecycleId);
    await finishKit5Retry(pool, { scenarioId: action.scenarioId, actionId: action.actionId, status: "retry_completed", result: retryResult(retryRequestId, result) });
  } catch (error) {
    const lifecycle = await gateway.getIssuingLifecycle(lifecycleId, attempt.requestFacts.accountId).catch(() => null);
    if (lifecycle !== null) {
      const result = normalizeIssuingResponse(lifecycle, lifecycleId);
      await finishKit5Retry(pool, { scenarioId: action.scenarioId, actionId: action.actionId, status: "retry_completed", result: retryResult(retryRequestId, result) });
      return;
    }
    const permanent = error instanceof Error && error.name === "ProviderHttpError";
    await finishKit5Retry(pool, {
      scenarioId: action.scenarioId,
      actionId: action.actionId,
      status: permanent ? "retry_failed" : "retry_ambiguous",
      error: permanent ? "provider_retry_rejected" : "provider_retry_unresolved",
    });
  }
}

export async function reconcileDurableRetry(pool: DatabasePool, gateway: AirwallexGateway & IssuingSimulationGateway, action: Kit5PolicyInput & { amountCents: number }): Promise<void> {
  const attempt = await getKit5CardAttempt(pool, action.scenarioId, action.actionId);
  if (!attempt?.retryLifecycleId || !attempt.requestFacts ||
      (attempt.policyStatus !== "retry_submitting" && attempt.policyStatus !== "retry_ambiguous")) return;
  const lifecycle = await gateway.getIssuingLifecycle(attempt.retryLifecycleId, attempt.requestFacts.accountId).catch(() => null);
  if (lifecycle === null) {
    if (attempt.policyStatus === "retry_submitting") {
      await finishKit5Retry(pool, { scenarioId: action.scenarioId, actionId: action.actionId,
        status: "retry_ambiguous", error: "provider_retry_unresolved" });
    }
    return;
  }
  const result = normalizeIssuingResponse(lifecycle, attempt.retryLifecycleId);
  await finishKit5Retry(pool, {
    scenarioId: action.scenarioId,
    actionId: action.actionId,
    status: "retry_completed",
    result: retryResult(attempt.retryRequestId ?? "", result),
  });
}

function retryResult(requestId: string, result: IssuingSimulationResponse): Record<string, unknown> {
  return { requestId, processResult: result.processResult, failureReason: result.failureReason,
    cardTransactionEventId: result.cardTransactionEventId, cardTransactionId: result.cardTransactionId,
    cardTransactionLifecycleId: result.cardTransactionLifecycleId };
}

async function loadKit5TrustedRequest(pool: Pick<DatabasePool, "query">, action: Kit5PolicyInput) {
  const attempt = await getKit5CardAttempt(pool, action.scenarioId, action.actionId);
  if (!attempt || attempt.customerId !== action.customerId || attempt.status !== "completed" ||
      !attempt.requestFacts || !attempt.result || !isKit5ActionSourceConsistent(attempt, action)) return null;
  const facts = attempt.requestFacts;
  if (!matchesAction(facts, action) || facts.currency !== "USD" || action.currency !== "USD" ||
      facts.transactionType !== "AUTHORIZATION" || facts.transactionCategory !== "PURCHASE" ||
      !TRACEABLE_PURCHASE_MCCS.has(facts.merchantCategoryCode)) return null;
  const binding = await pool.query<{ customer_id: string; airwallex_account_id: string }>(
    `SELECT customer_id, airwallex_account_id FROM issuing_card_bindings
      WHERE card_id = $1 AND customer_id = $2 AND airwallex_account_id = $3 AND is_active`,
    [facts.cardId, action.customerId, facts.accountId],
  );
  if (binding.rowCount !== 1) return null;
  const request: AirwallexRemoteAuthorizationV2 = {
    version: 2, accountId: facts.accountId, cardId: facts.cardId,
    cardTransactionEventId: String(attempt.result.cardTransactionEventId),
    cardTransactionId: String(attempt.result.cardTransactionId),
    cardTransactionLifecycleId: String(attempt.result.cardTransactionLifecycleId),
    transactionType: facts.transactionType,
    transactionCategory: facts.transactionCategory,
    transactionAmount: facts.amountCents / 100,
    transactionCurrency: facts.currency,
    merchantCategoryCode: facts.merchantCategoryCode,
  };
  const current = await loadRequestFacts(pool, action.customerId, request, new Date());
  return current ? { ...current, request } : null;
}

function matchesAction(facts: NonNullable<Awaited<ReturnType<typeof getKit5CardAttempt>>>["requestFacts"], action: Kit5PolicyInput): boolean {
  return Boolean(facts && facts.accountId === action.accountId && facts.cardId === action.cardId &&
    facts.amountCents === action.attemptedAmountCents && facts.currency === action.currency);
}

export function isKit5ActionSourceConsistent(attempt: Kit5CardAttempt, action: Kit5PolicyInput): boolean {
  const facts = attempt.requestFacts;
  const result = attempt.result;
  return attempt.status === "completed" && attempt.customerId === action.customerId && Boolean(facts && result) &&
    matchesAction(facts, action) && sameProviderResult(result!, action.providerResult) &&
    (!result!.accountId || result!.accountId === facts!.accountId) &&
    (!result!.cardId || result!.cardId === facts!.cardId) &&
    (result!.transactionAmountCents === undefined || result!.transactionAmountCents === facts!.amountCents) &&
    (!result!.transactionCurrency || result!.transactionCurrency === facts!.currency) &&
    (!result!.transactionType || result!.transactionType === facts!.transactionType) &&
    (!result!.transactionCategory || result!.transactionCategory === facts!.transactionCategory) &&
    (!result!.merchantCategoryCode || result!.merchantCategoryCode === facts!.merchantCategoryCode) &&
    action.providerResult.processResult === "DECLINED" && action.providerResult.failureReason === "INSUFFICIENT_FUNDS" &&
    facts!.transactionType === "AUTHORIZATION" && facts!.transactionCategory === "PURCHASE" &&
    TRACEABLE_PURCHASE_MCCS.has(facts!.merchantCategoryCode);
}

export function sameKit5ApprovalTerms(
  persisted: Pick<RequestDecision, "amountCents" | "creditLineCents" | "tier" | "capitalBand" | "door" | "reasonCodes">,
  current: Pick<RequestDecision, "amountCents" | "creditLineCents" | "tier" | "capitalBand" | "door" | "reasonCodes">,
): boolean {
  const reasonCodes = (values: readonly string[]) => values.filter((code) => code !== "HUMAN_REVIEW_REQUIRED" && code !== "AUTO_APPROVED").sort();
  return persisted.amountCents === current.amountCents && persisted.creditLineCents === current.creditLineCents &&
    persisted.tier === current.tier && persisted.capitalBand === current.capitalBand && persisted.door === current.door &&
    JSON.stringify(reasonCodes(persisted.reasonCodes)) === JSON.stringify(reasonCodes(current.reasonCodes));
}

function sameProviderResult(saved: Record<string, unknown>, result: IssuingSimulationResponse): boolean {
  return saved.processResult === result.processResult && saved.failureReason === result.failureReason &&
    saved.cardTransactionEventId === result.cardTransactionEventId && saved.cardTransactionId === result.cardTransactionId &&
    saved.cardTransactionLifecycleId === result.cardTransactionLifecycleId &&
    saved.accountId === result.accountId && saved.cardId === result.cardId &&
    saved.transactionAmountCents === result.transactionAmountCents && saved.transactionCurrency === result.transactionCurrency &&
    saved.transactionType === result.transactionType && saved.transactionCategory === result.transactionCategory &&
    saved.merchantCategoryCode === result.merchantCategoryCode;
}

function decodeStoredResult(result: Record<string, unknown>): IssuingSimulationResponse {
  if ((result.processResult !== "APPROVED" && result.processResult !== "DECLINED") ||
      typeof result.failureReason !== "string" && result.failureReason !== null ||
      typeof result.cardTransactionEventId !== "string" || typeof result.cardTransactionId !== "string" ||
      typeof result.cardTransactionLifecycleId !== "string") throw new Error("Saved Kit 5 provider response is malformed");
  return { processResult: result.processResult, failureReason: result.failureReason,
    cardTransactionEventId: result.cardTransactionEventId, cardTransactionId: result.cardTransactionId,
    cardTransactionLifecycleId: result.cardTransactionLifecycleId,
    ...(result.transactionType === "AUTHORIZATION" || result.transactionType === "CLEARING" ? { transactionType: result.transactionType } : {}),
    ...(typeof result.transactionCategory === "string" ? { transactionCategory: result.transactionCategory } : {}),
    ...(typeof result.merchantCategoryCode === "string" ? { merchantCategoryCode: result.merchantCategoryCode } : {}),
    ...(typeof result.accountId === "string" ? { accountId: result.accountId } : {}),
    ...(typeof result.cardId === "string" ? { cardId: result.cardId } : {}),
    ...(typeof result.transactionAmountCents === "number" ? { transactionAmountCents: result.transactionAmountCents } : {}),
    ...(typeof result.transactionCurrency === "string" ? { transactionCurrency: result.transactionCurrency } : {}) };
}

function actionKey(action: Kit5PolicyInput, eventId: string): string {
  return `${action.scenarioId}\0${action.actionId}\0${action.customerId}\0${eventId}`;
}
