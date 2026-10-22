import { describe, expect, it, vi } from "vitest";
import { evaluateBridgeRequest, type RequestPolicyState } from "../../../server/src/policy/decision-service.js";
import type { BridgeRequest } from "../../../server/src/policy/request.js";

const now = new Date("2026-10-22T12:00:00.000Z");
const request = (overrides: Partial<BridgeRequest> = {}): BridgeRequest => ({
  requestId: "request-1",
  customerId: "customer-1",
  transactionId: "txn-1",
  transactionAmountCents: 50_000,
  currency: "USD",
  spendCategory: "supplier",
  traceable: true,
  ...overrides,
});
const state = (overrides: Partial<RequestPolicyState> = {}): RequestPolicyState => ({
  precheck: {
    customerId: "customer-1", outcome: "ready", evaluatedAt: new Date(now.getTime() - 60_000),
    creditLineCents: 100_000, tier: "A", humanApprovalRequired: false, reasonCodes: [],
  },
  eligibleSpend: "supported",
  outstandingCents: 0,
  openBridgeCount: 0,
  oldestOutstandingDueAt: null,
  facilitySizeCents: 1_000_000,
  reserveFloorCents: 200_000,
  deployableCents: 600_000,
  accountId: "airwallex-account-1",
  ...overrides,
});
const balance = vi.fn(async () => ({ availableCents: 10_000, currency: "USD" as const }));
const evaluate = (overrides: {
  request?: Partial<BridgeRequest>;
  state?: Partial<RequestPolicyState>;
  readCurrentBalance?: typeof balance;
  balanceTimeoutMs?: number;
  humanApproval?: { requestId: string; amountCents: number; actorId: string; reason: string };
} = {}) => evaluateBridgeRequest({
  request: request(overrides.request), state: state(overrides.state),
  readCurrentBalance: overrides.readCurrentBalance ?? balance,
  humanApproval: overrides.humanApproval,
  balanceTimeoutMs: overrides.balanceTimeoutMs,
  now,
});

describe("request-time policy decision service", () => {
  it("approves exactly the full shortfall and reads only the mapped current USD balance", async () => {
    const readBalance = vi.fn(async (accountId: string, currency: "USD") => {
      expect(accountId).toBe("airwallex-account-1");
      expect(currency).toBe("USD");
      return { availableCents: 10_000, currency };
    });

    const result = await evaluate({ readCurrentBalance: readBalance });

    expect(result).toMatchObject({
      outcome: "auto_approved", amountCents: 40_000, tier: "A", capitalBand: "a_lot",
      door: "auto", reasonCodes: ["AUTO_APPROVED"],
    });
    expect(readBalance).toHaveBeenCalledTimes(1);
  });

  it.each([
    ["missing precheck", { precheck: null }, "blocked", "PRECHECK_MISSING"],
    ["stale precheck", { precheck: { ...state().precheck!, evaluatedAt: new Date(now.getTime() - 86_400_001) } }, "blocked", "PRECHECK_STALE"],
    ["future precheck", { precheck: { ...state().precheck!, evaluatedAt: new Date(now.getTime() + 1) } }, "blocked", "PRECHECK_STALE"],
    ["unresolved risk gate", { precheck: { ...state().precheck!, outcome: "blocked" as const, reasonCodes: ["EVIDENCE_BLOCKED"] } }, "blocked", "PRECHECK_BLOCKED"],
    ["ineligible precheck", { precheck: { ...state().precheck!, outcome: "ineligible" as const, reasonCodes: ["NO_GO_APPLIED"] } }, "declined", "PRECHECK_INELIGIBLE"],
    ["unknown spend profile", { eligibleSpend: "unknown" as const }, "blocked", "PROFILE_SPEND_UNKNOWN"],
    ["unsupported spend profile", { eligibleSpend: "unsupported" as const }, "declined", "PROFILE_SPEND_UNSUPPORTED"],
    ["second open bridge", { openBridgeCount: 1 }, "declined", "OPEN_BRIDGE_EXISTS"],
    ["exposure above line", { outstandingCents: 100_001 }, "declined", "EXPOSURE_ABOVE_LINE"],
    ["more than two days late", { oldestOutstandingDueAt: new Date(now.getTime() - 172_800_001) }, "declined", "BALANCE_MORE_THAN_TWO_DAYS_LATE"],
    ["invalid ledger state", { deployableCents: -1 }, "blocked", "INVALID_LEDGER_STATE"],
  ] as const)("fails closed or declines for %s", async (_name, stateOverride, outcome, code) => {
    const readBalance = vi.fn(balance);
    const result = await evaluate({ state: stateOverride, readCurrentBalance: readBalance });
    expect(result.outcome).toBe(outcome);
    expect(result.reasonCodes).toContain(code);
    expect(readBalance).not.toHaveBeenCalled();
  });

  it("uses the two-day grace boundary inclusively", async () => {
    const result = await evaluate({ state: { oldestOutstandingDueAt: new Date(now.getTime() - 172_800_000) } });
    expect(result.outcome).toBe("auto_approved");
  });

  it.each([
    ["A in Critical", "A", 60_000, "human_review", "critical", "human"],
    ["B in Less", "B", 120_000, "human_review", "less", "human"],
    ["C in Normal", "C", 400_000, "human_review", "normal", "human"],
    ["C in Less", "C", 300_000, "declined", "less", "closed"],
    ["A at floor", "A", 0, "declined", "at_floor", "closed"],
  ] as const)("applies the door for %s", async (_name, tier, deployableCents, outcome, capitalBand, door) => {
    const result = await evaluate({
      state: { precheck: { ...state().precheck!, tier }, deployableCents },
    });
    expect(result).toMatchObject({ outcome, capitalBand, door });
  });

  it.each([
    ["above 70%", 560_001, "B", "a_lot", "auto_approved", "auto"],
    ["at 70%", 560_000, "B", "normal", "auto_approved", "auto"],
    ["at 40%", 320_000, "B", "normal", "auto_approved", "auto"],
    ["below 40%", 319_999, "B", "less", "human_review", "human"],
    ["at 10%", 80_000, "A", "less", "auto_approved", "auto"],
    ["below 10%", 79_999, "A", "critical", "human_review", "human"],
  ] as const)("uses the exact capital door boundary %s", async (_name, deployableCents, tier, capitalBand, outcome, door) => {
    const result = await evaluate({
      state: { precheck: { ...state().precheck!, tier }, deployableCents },
    });
    expect(result).toMatchObject({ capitalBand, outcome, door });
  });

  it("returns human review for a watched precheck without reserving funds", async () => {
    const result = await evaluate({ state: { precheck: { ...state().precheck!, humanApprovalRequired: true } } });
    expect(result).toMatchObject({ outcome: "human_review", amountCents: 40_000, reasonCodes: ["HUMAN_REVIEW_REQUIRED"] });
  });

  it("approves an exact human authorization only after re-evaluating current terms", async () => {
    const approval = { requestId: "request-1", amountCents: 40_000, actorId: "operator-1", reason: "Verified current evidence" };
    const reviewed = await evaluate({
      state: { precheck: { ...state().precheck!, humanApprovalRequired: true } },
    });
    expect(reviewed.outcome).toBe("human_review");

    const approved = await evaluate({
      state: { precheck: { ...state().precheck!, humanApprovalRequired: true } },
      humanApproval: approval,
    });
    expect(approved).toMatchObject({ outcome: "auto_approved", amountCents: 40_000 });

    const changedAmount = await evaluate({
      state: { precheck: { ...state().precheck!, humanApprovalRequired: true } },
      humanApproval: { ...approval, amountCents: 39_999 },
    });
    expect(changedAmount.outcome).toBe("human_review");

    const changedInputs = await evaluate({
      state: { precheck: { ...state().precheck!, humanApprovalRequired: true, outcome: "blocked" } },
      humanApproval: approval,
    });
    expect(changedInputs).toMatchObject({ outcome: "blocked", reasonCodes: ["PRECHECK_BLOCKED"] });
  });

  it.each([
    ["line", { precheck: { ...state().precheck!, creditLineCents: 39_999 } }, "SHORTFALL_EXCEEDS_REMAINING_LINE"],
    ["facility", { deployableCents: 39_999 }, "INSUFFICIENT_DEPLOYABLE_CAPITAL"],
  ] as const)("declines the full shortfall when %s is insufficient without partial allocation", async (_name, override, code) => {
    const result = await evaluate({ state: override });
    expect(result).toMatchObject({ outcome: "declined", amountCents: 0 });
    expect(result.reasonCodes).toContain(code);
  });

  it("declines when the current balance covers the transaction", async () => {
    const result = await evaluate({ readCurrentBalance: vi.fn(async () => ({ availableCents: 50_000, currency: "USD" })) });
    expect(result).toMatchObject({ outcome: "declined", amountCents: 0, reasonCodes: ["NO_SHORTFALL"] });
  });

  it.each([
    ["provider error", vi.fn(async () => { throw new Error("offline"); }), "BALANCE_READ_FAILED"],
    ["invalid currency", vi.fn(async () => ({ availableCents: 1, currency: "EUR" })), "BALANCE_RESPONSE_INVALID"],
    ["invalid amount", vi.fn(async () => ({ availableCents: -1, currency: "USD" })), "BALANCE_RESPONSE_INVALID"],
  ] as const)("blocks on %s", async (_name, readCurrentBalance, code) => {
    const result = await evaluate({ readCurrentBalance: readCurrentBalance as typeof balance });
    expect(result).toMatchObject({ outcome: "blocked", amountCents: 0 });
    expect(result.reasonCodes).toContain(code);
  });

  it("blocks a timed-out current balance read", async () => {
    const result = await evaluate({
      readCurrentBalance: vi.fn(() => new Promise(() => {})) as typeof balance,
      balanceTimeoutMs: 1,
    });
    expect(result).toMatchObject({ outcome: "blocked", reasonCodes: ["BALANCE_READ_TIMEOUT"] });
  });

  it("returns the same decision for a repeated request with unchanged inputs", async () => {
    const first = await evaluate();
    const retry = await evaluate();
    expect(retry).toEqual(first);
  });

  it("does not read a balance or return a partial amount for an untraceable request", async () => {
    const readBalance = vi.fn(balance);
    const result = await evaluate({ request: { traceable: false }, readCurrentBalance: readBalance });
    expect(result).toMatchObject({ outcome: "declined", amountCents: 0 });
    expect(result.reasonCodes).toContain("UNTRACEABLE_SPEND");
    expect(readBalance).not.toHaveBeenCalled();
  });
});
