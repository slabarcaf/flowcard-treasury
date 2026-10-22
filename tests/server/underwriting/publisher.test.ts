import { describe, expect, it, vi } from "vitest";
import type { DatabasePool, QueryResult } from "../../../server/src/db/pool.js";
import { publishCustomerProfile } from "../../../server/src/underwriting/publisher.js";
import type { ProfileQueryName, UnderwritingProfileCandidate } from "../../../server/src/underwriting/types.js";

describe("underwriting profile publisher validation", () => {
  it("records an incomplete required query as a failed attempt without opening a publication transaction", async () => {
    const statements: string[] = [];
    const pool = {
      query: vi.fn(async <Row>(sql: string, values: readonly unknown[] = []): Promise<QueryResult<Row>> => {
        statements.push(sql.replace(/\s+/g, " ").trim());
        expect(sql).toContain("'failed'");
        expect(values[2]).toBe("demo");
        expect(values[7]).toContain("Required query transactions is incomplete");
        return { rows: [], rowCount: 1 };
      }),
      connect: vi.fn(),
      end: vi.fn(),
    } as unknown as DatabasePool;
    const candidate = completeCandidate();
    candidate.queryCompletion.transactions.complete = false;

    const result = await publishCustomerProfile(pool, candidate);

    expect(result).toMatchObject({ status: "failed", customerId: "customer-1", runId: "run-1" });
    expect(statements).toHaveLength(1);
    expect(statements[0]).toContain("INSERT INTO underwriting_profile_attempts");
    expect(pool.connect).not.toHaveBeenCalled();
  });
});

function completeCandidate(): UnderwritingProfileCandidate {
  const windowEnd = new Date("2026-10-22T10:00:00.000Z");
  const names = ["account", "balances", "transactions", "expected_inflows", "repayments"] as const;
  return {
    customerId: "customer-1", runId: "run-1", sourceMode: "demo",
    capturedAt: windowEnd, startedAt: new Date(windowEnd.getTime() - 60_000),
    sourceVersions: Object.fromEntries(names.map((name) => [name, `${name}-v1`])) as Record<ProfileQueryName, string>,
    queryCompletion: Object.fromEntries(names.map((name) => [name, { complete: true, pages: 1 }])) as UnderwritingProfileCandidate["queryCompletion"],
    schemaVersion: 1, featureVersion: "features-v1",
    windowStart: new Date(windowEnd.getTime() - 90 * 24 * 60 * 60 * 1000), windowEnd,
    data: {
      averageMonthlySettledInflowsCents: null, expectedDateReliability: 0.9,
      topPayerConcentration: 0.4, lowBalanceDays: 0, historyDays: 90,
      eligibleSpend: "supported", connectedAccountStatus: "active", openRfi: false,
      repaymentHistory: "never_borrowed",
    },
  };
}
