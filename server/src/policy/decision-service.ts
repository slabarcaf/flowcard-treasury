import { checkBridge } from "../../../shared/policy/bridge.js";
import { capitalLevel, toBand } from "../../../shared/policy/facility.js";
import type { Band, Door, Tier } from "../../../shared/policy/types.js";
import type { DailyPrecheck } from "./precheck.js";
import type { BridgeRequest, RequestValidationCode } from "./request.js";
import { validateBridgeRequest } from "./request.js";

const DAY_MS = 24 * 60 * 60 * 1000;
const MAX_PRECHECK_AGE_MS = DAY_MS;
const LATE_GRACE_MS = 2 * DAY_MS;
export const DEFAULT_BALANCE_TIMEOUT_MS = 1_500;

export type RequestDecisionCode =
  | RequestValidationCode
  | "PRECHECK_MISSING"
  | "PRECHECK_STALE"
  | "PRECHECK_BLOCKED"
  | "PRECHECK_INELIGIBLE"
  | "PROFILE_SPEND_UNSUPPORTED"
  | "PROFILE_SPEND_UNKNOWN"
  | "INVALID_LEDGER_STATE"
  | "OPEN_BRIDGE_EXISTS"
  | "EXPOSURE_ABOVE_LINE"
  | "BALANCE_MORE_THAN_TWO_DAYS_LATE"
  | "BALANCE_READ_FAILED"
  | "BALANCE_READ_TIMEOUT"
  | "BALANCE_RESPONSE_INVALID"
  | "NO_SHORTFALL"
  | "DOOR_CLOSED"
  | "SHORTFALL_EXCEEDS_REMAINING_LINE"
  | "INSUFFICIENT_DEPLOYABLE_CAPITAL"
  | "AUTO_APPROVED"
  | "HUMAN_REVIEW_REQUIRED";

export type RequestOutcome = "auto_approved" | "human_review" | "declined" | "blocked";

export interface RequestDecision {
  requestId: string;
  customerId: string;
  outcome: RequestOutcome;
  amountCents: number;
  creditLineCents: number;
  tier: Tier;
  capitalBand: Band | null;
  door: Door | null;
  reasonCodes: RequestDecisionCode[];
  rationale: string;
}

export interface RequestPolicyState {
  precheck: Pick<DailyPrecheck,
    "customerId" | "outcome" | "evaluatedAt" | "creditLineCents" | "tier" | "humanApprovalRequired" | "reasonCodes"> | null;
  eligibleSpend: "supported" | "unsupported" | "unknown";
  /** The ledger projection's committed exposure for this customer. */
  outstandingCents: number;
  openBridgeCount: number;
  /** Oldest unpaid repayment due date, if any. */
  oldestOutstandingDueAt: Date | null;
  /** Ledger-derived capacity values before evaluating this request. */
  facilitySizeCents: number;
  reserveFloorCents: number;
  deployableCents: number;
  /** Internal account mapping. Never taken from the request payload. */
  accountId: string;
}

export interface CurrentBalance {
  availableCents: number;
  currency: string;
}

export interface EvaluateBridgeRequestInput {
  request: BridgeRequest;
  state: RequestPolicyState;
  readCurrentBalance: (accountId: string, currency: "USD") => Promise<CurrentBalance>;
  now?: Date;
  balanceTimeoutMs?: number;
}

/**
 * Evaluate one arriving bridge request against trusted, already-loaded state.
 * This function reads no historical provider data and never reserves capacity.
 */
export async function evaluateBridgeRequest(input: EvaluateBridgeRequestInput): Promise<RequestDecision> {
  const { request, state } = input;
  const base = {
    requestId: request.requestId,
    customerId: request.customerId,
    amountCents: 0,
    creditLineCents: state.precheck?.creditLineCents ?? 0,
    tier: state.precheck?.tier ?? "none" as const,
    capitalBand: null,
    door: null,
  };

  const validationErrors = validateBridgeRequest(request);
  if (validationErrors.length > 0) {
    return decision(base, "declined", validationErrors, "Request is not an eligible, traceable USD card spend.");
  }
  if (!state.precheck) return decision(base, "blocked", ["PRECHECK_MISSING"], "A current customer precheck is required.");
  if (state.precheck.customerId !== request.customerId) {
    return decision(base, "blocked", ["PRECHECK_BLOCKED"], "The current precheck does not match this customer.");
  }

  const now = input.now ?? new Date();
  const precheckAge = now.getTime() - state.precheck.evaluatedAt.getTime();
  if (!Number.isFinite(now.getTime()) || !Number.isFinite(precheckAge) || precheckAge < 0 || precheckAge > MAX_PRECHECK_AGE_MS) {
    return decision(base, "blocked", ["PRECHECK_STALE"], "The customer precheck is missing, future-dated, or older than 24 hours.");
  }
  if (state.precheck.outcome === "blocked") {
    return decision(base, "blocked", ["PRECHECK_BLOCKED"], "A required risk or source-data gate is unresolved.");
  }
  if (state.precheck.outcome === "ineligible") {
    return decision(base, "declined", ["PRECHECK_INELIGIBLE"], "The customer does not qualify for a bridge under the current precheck.");
  }
  if (state.eligibleSpend === "unknown") {
    return decision(base, "blocked", ["PROFILE_SPEND_UNKNOWN"], "The customer spend classification is unknown.");
  }
  if (state.eligibleSpend !== "supported") {
    return decision(base, "declined", ["PROFILE_SPEND_UNSUPPORTED"], "The customer profile does not support this spend.");
  }

  if (!validLedgerState(state)) {
    return decision(base, "blocked", ["INVALID_LEDGER_STATE"], "Current ledger capacity or exposure data is invalid.");
  }
  if (state.openBridgeCount > 0) {
    return decision(base, "declined", ["OPEN_BRIDGE_EXISTS"], "The customer already has an open bridge.");
  }
  if (state.outstandingCents > state.precheck.creditLineCents) {
    return decision(base, "declined", ["EXPOSURE_ABOVE_LINE"], "Current customer exposure exceeds the approved credit line.");
  }
  if (state.oldestOutstandingDueAt !== null) {
    const dueAt = state.oldestOutstandingDueAt.getTime();
    if (!Number.isFinite(dueAt)) {
      return decision(base, "blocked", ["INVALID_LEDGER_STATE"], "The current repayment due date is invalid.");
    }
    if (now.getTime() - dueAt > LATE_GRACE_MS) {
      return decision(base, "declined", ["BALANCE_MORE_THAN_TWO_DAYS_LATE"], "An outstanding balance is more than two days past due.");
    }
  }

  let balance: CurrentBalance;
  try {
    balance = await readBalanceWithTimeout(
      input.readCurrentBalance(state.accountId, "USD"),
      input.balanceTimeoutMs ?? DEFAULT_BALANCE_TIMEOUT_MS,
    );
  } catch (error) {
    const code = error instanceof BalanceReadTimeoutError ? "BALANCE_READ_TIMEOUT" : "BALANCE_READ_FAILED";
    return decision(base, "blocked", [code], "Current USD balance could not be verified.");
  }
  if (balance.currency !== "USD" || !Number.isSafeInteger(balance.availableCents) || balance.availableCents < 0) {
    return decision(base, "blocked", ["BALANCE_RESPONSE_INVALID"], "Current USD balance response is invalid.");
  }

  const shortfallCents = request.transactionAmountCents - balance.availableCents;
  if (shortfallCents <= 0) {
    return decision(base, "declined", ["NO_SHORTFALL"], "Current available balance covers this card spend.");
  }
  const lendableCents = state.facilitySizeCents - state.reserveFloorCents;
  const band = toBand(capitalLevel(state.deployableCents, lendableCents));
  const dollarDecision = checkBridge({
    shortfall: shortfallCents / 100,
    creditLine: state.precheck.creditLineCents / 100,
    outstanding: state.outstandingCents / 100,
    tier: state.precheck.tier,
    band,
    deployableCapital: state.deployableCents / 100,
    facilityConfig: {
      facilitySize: state.facilitySizeCents / 100,
      reserveFloorPct: state.facilitySizeCents === 0 ? 0 : state.reserveFloorCents / state.facilitySizeCents,
      maxPerCustomerPct: 1,
      feeRatePer30Days: 0,
      minHistoryDays: 90,
    },
  });
  const context = { ...base, amountCents: dollarDecision.allocation === 0 ? 0 : shortfallCents, capitalBand: band, door: dollarDecision.door };
  if (dollarDecision.reasonCodes.includes("NO_CREDIT_LINE")) {
    return decision(context, "declined", ["PRECHECK_INELIGIBLE"], "No credit line is available for this customer.");
  }
  if (dollarDecision.reasonCodes.includes("DOOR_CLOSED")) {
    return decision(context, "declined", ["DOOR_CLOSED"], `The ${band} capital door is closed for tier ${state.precheck.tier}.`);
  }
  if (dollarDecision.reasonCodes.includes("SHORTFALL_EXCEEDS_REMAINING_LINE") || dollarDecision.reasonCodes.includes("CREDIT_LINE_TOO_SMALL")) {
    return decision(context, "declined", ["SHORTFALL_EXCEEDS_REMAINING_LINE"], "The full shortfall exceeds the customer's remaining credit line.");
  }
  if (dollarDecision.reasonCodes.includes("INSUFFICIENT_DEPLOYABLE_CAPITAL")) {
    return decision(context, "declined", ["INSUFFICIENT_DEPLOYABLE_CAPITAL"], "Deployable facility capacity cannot cover the full shortfall.");
  }
  if (state.precheck.humanApprovalRequired || dollarDecision.door === "human") {
    return decision({ ...context, amountCents: shortfallCents }, "human_review", ["HUMAN_REVIEW_REQUIRED"], "The full shortfall meets policy limits and requires human approval.");
  }
  return decision({ ...context, amountCents: shortfallCents }, "auto_approved", ["AUTO_APPROVED"], "The full shortfall meets the current policy and capital door.");
}

function validLedgerState(state: RequestPolicyState): boolean {
  return Boolean(state.accountId.trim()) &&
    Number.isSafeInteger(state.precheck?.creditLineCents) && (state.precheck?.creditLineCents ?? -1) >= 0 &&
    Number.isSafeInteger(state.outstandingCents) && state.outstandingCents >= 0 &&
    Number.isSafeInteger(state.openBridgeCount) && state.openBridgeCount >= 0 &&
    Number.isSafeInteger(state.facilitySizeCents) && state.facilitySizeCents > 0 &&
    Number.isSafeInteger(state.reserveFloorCents) && state.reserveFloorCents >= 0 && state.reserveFloorCents < state.facilitySizeCents &&
    Number.isSafeInteger(state.deployableCents) && state.deployableCents >= 0 && state.deployableCents <= state.facilitySizeCents - state.reserveFloorCents;
}

function decision(
  base: Omit<RequestDecision, "outcome" | "reasonCodes" | "rationale">,
  outcome: RequestOutcome,
  reasonCodes: RequestDecisionCode[],
  rationale: string,
): RequestDecision {
  return { ...base, outcome, reasonCodes: [...new Set(reasonCodes)], rationale };
}

class BalanceReadTimeoutError extends Error {}

async function readBalanceWithTimeout<T>(promise: Promise<T>, timeoutMs: number): Promise<T> {
  if (!Number.isSafeInteger(timeoutMs) || timeoutMs < 1) throw new Error("Balance timeout must be a positive integer");
  let timer: ReturnType<typeof setTimeout> | undefined;
  try {
    return await Promise.race([
      promise,
      new Promise<never>((_, reject) => {
        timer = setTimeout(() => reject(new BalanceReadTimeoutError("Current balance read timed out")), timeoutMs);
      }),
    ]);
  } finally {
    if (timer !== undefined) clearTimeout(timer);
  }
}
