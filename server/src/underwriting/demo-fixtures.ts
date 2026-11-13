import { randomUUID } from "node:crypto";
import type { DatabasePool } from "../db/pool.js";
import { listActiveCustomersForSnapshots } from "../roster/repository.js";
import { publishUnderwritingProfile } from "./repository.js";
import type { UnderwritingProfileCandidate } from "./types.js";

const DATA: Readonly<Record<string, UnderwritingProfileCandidate["data"]>> = {
  acct_bloom001: { averageMonthlySettledInflowsCents: 4_000_000, expectedDateReliability: 0.94, topPayerConcentration: 0.38, lowBalanceDays: 8, historyDays: 90, eligibleSpend: "supported", connectedAccountStatus: "active", openRfi: false, repaymentHistory: "never_borrowed" },
  acct_atlas001: { averageMonthlySettledInflowsCents: 2_000_000, expectedDateReliability: 0.96, topPayerConcentration: 0.38, lowBalanceDays: 1, historyDays: 90, eligibleSpend: "supported", connectedAccountStatus: "active", openRfi: false, repaymentHistory: "never_borrowed" },
  acct_nova001: { averageMonthlySettledInflowsCents: 3_500_000, expectedDateReliability: 0.90, topPayerConcentration: 0.54, lowBalanceDays: 4, historyDays: 90, eligibleSpend: "supported", connectedAccountStatus: "active", openRfi: false, repaymentHistory: "never_borrowed" },
  acct_pixel001: { averageMonthlySettledInflowsCents: 1_200_000, expectedDateReliability: 0.70, topPayerConcentration: 0.82, lowBalanceDays: 18, historyDays: 90, eligibleSpend: "supported", connectedAccountStatus: "active", openRfi: false, repaymentHistory: "never_borrowed" },
};

/** Publish explicit synthetic profiles for the demo roster without provider calls. */
export async function publishDemoUnderwritingProfiles(pool: DatabasePool, now = new Date()): Promise<void> {
  let afterCustomerId: string | null = null;
  while (true) {
    const customers = await listActiveCustomersForSnapshots(pool, { afterCustomerId, limit: 1_000 });
    for (const customer of customers) {
      const candidate = buildDemoUnderwritingProfileCandidate(customer.customerId, now);
      if (candidate) await publishUnderwritingProfile(pool, candidate);
    }
    if (customers.length < 1_000) break;
    afterCustomerId = customers[customers.length - 1]!.customerId;
  }
}

export function buildDemoUnderwritingProfileCandidate(
  customerId: string,
  now = new Date(),
  runId = `demo-underwriting-${randomUUID()}`,
): UnderwritingProfileCandidate | null {
  const data = DATA[customerId];
  if (!data) return null;
  const windowEnd = new Date(Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), now.getUTCDate()));
  const windowStart = new Date(windowEnd.getTime() - 90 * 86_400_000);
  return {
    customerId, runId, sourceMode: "demo", capturedAt: now, startedAt: now, windowStart, windowEnd,
    sourceVersions: {
      account: "demo-account-fixture-v1", balances: "demo-balance-fixture-v1",
      transactions: "demo-transaction-fixture-v1", expected_inflows: "demo-expected-date-fixture-v1",
      repayments: "demo-ledger-repayment-fixture-v1",
    },
    queryCompletion: {
      account: { complete: true, pages: 1 }, balances: { complete: true, pages: 1 },
      transactions: { complete: true, pages: 1 }, expected_inflows: { complete: true, pages: 1 },
      repayments: { complete: true, pages: 1 },
    },
    schemaVersion: 1, featureVersion: "underwriting-demo-fixtures-v1", data,
  };
}
