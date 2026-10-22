import { describe, expect, it, vi } from "vitest";
import { runDailyUnderwritingProfileBuild } from "../../../server/src/underwriting/runner.js";
import type { AirwallexUnderwritingGateway } from "../../../server/src/airwallex/client.js";
import type { DatabasePool } from "../../../server/src/db/pool.js";

describe("daily underwriting profile runner", () => {
  it("uses active roster mappings, ledger repayment rows, and blocks profiles without trusted expected dates", async () => {
    const pool = {
      query: vi.fn(async (sql: string) => {
        if (sql.includes("FROM customer_roster")) return { rows: [{ customer_id: "c1", airwallex_connected_account_id: "acct-1" }], rowCount: 1 };
        if (sql.includes("FROM bridge_cases")) return { rows: [{ customer_id: "c1", due_at: new Date("2026-10-01"), repaid_at: new Date("2026-10-02"), status: "repaid" }], rowCount: 1 };
        throw new Error("unexpected query");
      }),
      connect: vi.fn(), end: vi.fn(),
    } as unknown as DatabasePool;
    const gateway: AirwallexUnderwritingGateway = {
      createPlatformReport: vi.fn(async () => ({ reportId: "r" })),
      getPlatformReport: vi.fn(async () => ({ status: "completed", downloadUrl: "https://short.invalid/r" })),
      downloadPlatformReport: vi.fn(async () => []),
      getConnectedAccount: vi.fn(async () => ({ status: "ACTIVE" })),
      getOpenRfis: vi.fn(async () => ({ items: [], hasMore: false })),
      getBalanceHistory: vi.fn(async () => ({ items: [{ posted_at: "2026-08-01T00:00:00Z", balance: 1000, currency: "USD" }], hasMore: false })),
    };
    const result = await runDailyUnderwritingProfileBuild({ pool, gateway, now: new Date("2026-10-22T12:00:00Z"), runId: "daily-1" });
    expect(result).toHaveLength(1);
    expect(result[0]).toMatchObject({ customerId: "c1", publishable: false, blockedReason: "expected_inflow_dates_unavailable" });
    expect(result[0]?.candidate.data.repaymentHistory).toBe("late");
    expect(result[0]?.candidate.data.expectedDateReliability).toBeNull();
    expect(result[0]?.candidate.queryCompletion.expected_inflows).toEqual({ complete: false, pages: 0 });
    expect(pool.query).toHaveBeenCalledTimes(2);
  });

  it("processes an empty roster without calling account scoped APIs", async () => {
    const pool = { query: vi.fn(async () => ({ rows: [], rowCount: 0 })), connect: vi.fn(), end: vi.fn() } as unknown as DatabasePool;
    const gateway = { createPlatformReport: vi.fn(async () => ({ reportId: "r" })), getPlatformReport: vi.fn(async () => ({ status: "completed", downloadUrl: "https://x.invalid" })), downloadPlatformReport: vi.fn(async () => []) } as unknown as AirwallexUnderwritingGateway;
    await expect(runDailyUnderwritingProfileBuild({ pool, gateway, now: new Date("2026-10-22T12:00:00Z"), runId: "empty" })).resolves.toEqual([]);
  });
});
