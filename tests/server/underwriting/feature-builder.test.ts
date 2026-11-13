import { describe, expect, it } from "vitest";
import { buildProfileCandidate } from "../../../server/src/underwriting/feature-builder.js";
import type { BalancePoint, LedgerTransaction } from "../../../server/src/underwriting/airwallex-source.js";
import { PROFILE_QUERY_NAMES } from "../../../server/src/underwriting/types.js";

const start = new Date("2026-07-24T00:00:00.000Z");
const end = new Date("2026-10-22T00:00:00.000Z");
const transaction = (id: string, day: string, amountCents: number, kind: "credit" | "debit", payerKey: string | null, sourceType = "bank_transfer"): LedgerTransaction => ({
  id, accountId: "acct-1", occurredAt: new Date(day), amountCents, currency: "USD", kind, status: "settled", sourceType, payerKey, captured: kind === "debit",
});

describe("underwriting feature builder", () => {
  it("computes 90-day inflows, payer concentration, low balance and ledger repayment features", () => {
    const candidate = buildProfileCandidate({
      customerId: "c1", runId: "run-1", capturedAt: end, startedAt: end, windowStart: start, windowEnd: end, accountId: "acct-1",
      transactions: [transaction("a", "2026-08-01T12:00:00Z", 9_000, "credit", "payer-a"), transaction("b", "2026-09-01T12:00:00Z", 3_000, "credit", "payer-b"), transaction("a", "2026-08-01T12:00:00Z", 9_000, "credit", "payer-a"), transaction("s", "2026-09-10T12:00:00Z", 6_000, "debit", null, "card_capture")],
      balances: [{ accountId: "acct-1", postedAt: new Date("2026-08-01T00:00:00Z"), balanceCents: 50, currency: "USD" }, { accountId: "acct-1", postedAt: new Date("2026-08-02T00:00:00Z"), balanceCents: 500, currency: "USD" }] as BalancePoint[],
      standing: { status: "active", openRfi: false }, repayments: [{ dueAt: new Date("2026-09-01"), repaidAt: new Date("2026-08-30"), status: "repaid" }],
      sourceVersions: Object.fromEntries(PROFILE_QUERY_NAMES.map((name) => [name, "v1"])) as Record<(typeof PROFILE_QUERY_NAMES)[number], string>,
      queryPages: Object.fromEntries(PROFILE_QUERY_NAMES.map((name) => [name, { complete: true, pages: 1 }])) as Record<(typeof PROFILE_QUERY_NAMES)[number], { complete: boolean; pages: number }>,
    });
    expect(candidate.data).toMatchObject({ averageMonthlySettledInflowsCents: 4_000, topPayerConcentration: 0.75, lowBalanceDays: 1, eligibleSpend: "supported", connectedAccountStatus: "active", openRfi: false, repaymentHistory: "on_time", expectedDateReliability: null });
  });

  it("deduplicates repeated ledger rows and fails on conflicting duplicate IDs", () => {
    const common = {
      customerId: "c1", runId: "r", capturedAt: end, startedAt: end, windowStart: start, windowEnd: end, accountId: "acct-1", balances: [], standing: { status: "active" as const, openRfi: false }, repayments: [],
      sourceVersions: Object.fromEntries(PROFILE_QUERY_NAMES.map((name) => [name, "v1"])) as Record<(typeof PROFILE_QUERY_NAMES)[number], string>,
      queryPages: Object.fromEntries(PROFILE_QUERY_NAMES.map((name) => [name, { complete: true, pages: 1 }])) as Record<(typeof PROFILE_QUERY_NAMES)[number], { complete: boolean; pages: number }>,
    };
    expect(buildProfileCandidate({ ...common, transactions: [transaction("same", "2026-08-01T00:00:00Z", 100, "credit", "p"), transaction("same", "2026-08-01T00:00:00Z", 100, "credit", "p")] }).data.averageMonthlySettledInflowsCents).toBe(33);
    expect(() => buildProfileCandidate({ ...common, transactions: [transaction("same", "2026-08-01T00:00:00Z", 100, "credit", "p"), transaction("same", "2026-08-01T00:00:00Z", 200, "credit", "p")] })).toThrow(/conflicting duplicate/);
  });

  it("keeps expected-date reliability unknown and isolates account and window boundaries", () => {
    const row = transaction("edge", "2026-10-22T00:00:00Z", 1000, "credit", "p");
    const candidate = buildProfileCandidate({
      customerId: "c1", runId: "r", capturedAt: end, startedAt: end, windowStart: start, windowEnd: end, accountId: "acct-1", transactions: [row, { ...row, id: "other", accountId: "other" }], balances: [], standing: { status: "unknown", openRfi: null }, repayments: [],
      sourceVersions: Object.fromEntries(PROFILE_QUERY_NAMES.map((name) => [name, "v1"])) as Record<(typeof PROFILE_QUERY_NAMES)[number], string>,
      queryPages: Object.fromEntries(PROFILE_QUERY_NAMES.map((name) => [name, { complete: true, pages: 1 }])) as Record<(typeof PROFILE_QUERY_NAMES)[number], { complete: boolean; pages: number }>,
    });
    expect(candidate.data.averageMonthlySettledInflowsCents).toBe(0);
    expect(candidate.data.expectedDateReliability).toBeNull();
  });
});
