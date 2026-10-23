import { describe, expect, it, vi } from "vitest";
import type { DatabasePool, QueryResult } from "../../server/src/db/pool.js";
import { SALESFORCE_FIELDS } from "../../server/src/salesforce/field-map.js";
import type { SalesforceMcpQueryResults } from "../../server/src/salesforce/mcp-source.js";
import type { CustomerRosterEntry } from "../../server/src/roster/types.js";
import { scheduleDailySnapshotSync, snapshotDailyRunTime } from "../../server/src/snapshots/scheduler.js";
import { startDemoSnapshotSync } from "../../server/src/snapshots/runtime.js";
import { createDemoSnapshotSource, mapRecordsInBatches, SnapshotSyncWorker, type SnapshotSyncSource } from "../../server/src/snapshots/sync-worker.js";

const textLimits = { maxCaseTextLength: 200, maxActivityTextLength: 200 };
const now = new Date("2026-10-21T12:00:00.000Z");

describe("Salesforce daily snapshot worker", () => {
  it("verifies canonical synthetic demo mappings while leaving MCP mappings unverified", async () => {
    const demoSource = createDemoSnapshotSource();
    const demoResult = await demoSource.fetchCustomer(roster("acct_atlas001"));
    expect(demoSource.mode).toBe("demo");
    expect(demoResult.kind).toBe("fixture");
    if (demoResult.kind === "fixture") {
      expect(demoResult.payload).toMatchObject({
        mappingVersion: "salesforce-demo-fixture-v1",
        mappingVerified: true,
      });
    }

    let published: { payload: { mappingVerified: boolean } } | undefined;
    const worker = new SnapshotSyncWorker({
      pool: rosterPool([roster("mcp-mapping")]),
      source: sourceFor(async () => ({ kind: "mcp", queries: queryResults({ accountId: "sf-mcp-mapping" }) })),
      textLimits,
      now: () => now,
      publish: async (_pool, candidate) => { published = candidate; return {} as never; },
    });
    await worker.run();
    expect(published?.payload.mappingVerified).toBe(false);
  });

  it("uses only active internal roster entries and maps more than 1,000 records in bounded batches", async () => {
    const customers = [roster("active-1"), roster("inactive", false)];
    const pool = rosterPool(customers);
    const payloads: unknown[] = [];
    const source = sourceFor(async (customer) => {
      if (customer.customerId !== "active-1") throw new Error("inactive customer must not be queried");
      return { kind: "mcp", queries: queryResults({ accountId: "sf-active-1", cases: Array.from({ length: 1_001 }, (_, index) => caseRecord(index, "sf-active-1")) }) };
    });
    const worker = new SnapshotSyncWorker({
      pool,
      source,
      textLimits,
      now: () => now,
      runId: () => "run-many",
      publish: async (_pool, candidate) => { payloads.push(candidate); return {} as never; },
    });

    const result = await worker.run();

    expect(result.customers).toEqual([{ customerId: "active-1", status: "published" }]);
    expect(payloads).toHaveLength(1);
    expect((payloads[0] as { payload: { cases: unknown[] } }).payload.cases).toHaveLength(1_001);
    expect(pool.sql[0]).toContain("WHERE is_active");

    const observed: number[] = [];
    mapRecordsInBatches(Array.from({ length: 2_501 }, (_, value) => value), (value) => { observed.push(value); return value; });
    expect(observed).toHaveLength(2_501);
    expect(() => mapRecordsInBatches([1], (value) => value, 1_001)).toThrow("between 1 and 1000");
  });

  it("keyset-pages more than 1,000 roster customers and processes every customer once", async () => {
    const customers = Array.from({ length: 1_005 }, (_, index) => roster(`customer-${String(index).padStart(4, "0")}`));
    const pool = rosterPool(customers);
    const fetched: string[] = [];
    const published: string[] = [];
    const worker = new SnapshotSyncWorker({
      pool,
      source: sourceFor(async (customer) => {
        fetched.push(customer.customerId);
        return { kind: "mcp", queries: queryResults({ accountId: customer.salesforceAccountId }) };
      }),
      textLimits,
      now: () => now,
      publish: async (_pool, candidate) => { published.push(candidate.customerId); return {} as never; },
    });

    const result = await worker.run();

    expect(pool.sql).toHaveLength(2);
    expect(pool.sql.every((sql) => sql.includes("LIMIT $2"))).toBe(true);
    expect(pool.values.map((values) => values[1])).toEqual([1_000, 1_000]);
    expect(pool.values[0]?.[0]).toBeNull();
    expect(pool.values[1]?.[0]).toBe(customers[999]?.customerId);
    expect(result.customers).toHaveLength(1_005);
    expect(fetched).toHaveLength(1_005);
    expect(new Set(fetched).size).toBe(1_005);
    expect(published).toEqual(fetched);
  });

  it("publishes successful empty case and activity results as complete", async () => {
    let published: { payload: { cases: unknown[]; callActivities: unknown[] }; queryCompletion: { case: { complete: boolean }; activity: { complete: boolean } } } | undefined;
    const worker = new SnapshotSyncWorker({
      pool: rosterPool([roster("empty")]),
      source: sourceFor(async () => ({ kind: "mcp", queries: queryResults({ accountId: "sf-empty" }) })),
      textLimits,
      now: () => now,
      publish: async (_pool, candidate) => { published = candidate; return {} as never; },
    });

    const result = await worker.run();

    expect(result.customers[0]?.status).toBe("published");
    expect(published?.payload.cases).toEqual([]);
    expect(published?.payload.callActivities).toEqual([]);
    expect(published?.queryCompletion.case.complete).toBe(true);
    expect(published?.queryCompletion.activity.complete).toBe(true);
  });

  it.each([
    ["Account Id", (queries: SalesforceMcpQueryResults) => { queries.account.records[0]!.Id = "sf-other"; }],
    ["Contract AccountId", (queries: SalesforceMcpQueryResults) => { queries.contract.records[0]!.AccountId = "sf-other"; }],
    ["Case AccountId", (queries: SalesforceMcpQueryResults) => { queries.case.records.push({ Id: "case-other", AccountId: "sf-other" }); }],
    ["Task WhatId", (queries: SalesforceMcpQueryResults) => { queries.activity.records.push({ Id: "task-other", WhatId: "sf-other" }); }],
  ])("rejects MCP %s values that do not match the roster Account", async (_label, corrupt) => {
    const queries = queryResults({ accountId: "sf-mismatch" });
    corrupt(queries);
    const published: string[] = [];
    const failures: string[] = [];
    const worker = new SnapshotSyncWorker({
      pool: rosterPool([roster("mismatch")]),
      source: sourceFor(async () => ({ kind: "mcp", queries })),
      textLimits,
      now: () => now,
      publish: async (_pool, candidate) => { published.push(candidate.customerId); return {} as never; },
      recordFailure: async (_pool, attempt) => { failures.push(attempt.errorMessage); },
    });

    const result = await worker.run();

    expect(result.customers[0]?.status).toBe("failed");
    expect(result.customers[0]?.error).toContain("unexpected");
    expect(failures).toHaveLength(1);
    expect(published).toEqual([]);
  });

  it("retains only risk-relevant Cases that remain open or are escalated", async () => {
    let published: { payload: { cases: { recordId: string }[] }; recordCounts: { case: number } } | undefined;
    const fields = SALESFORCE_FIELDS.case;
    const makeCase = (id: string, status: string, risk: string | null, escalated: boolean) => ({
      Id: id,
      AccountId: "sf-risk-cases",
      LastModifiedDate: now.toISOString(),
      [fields.status]: status,
      [fields.priority]: "Medium",
      [fields.reason]: "Review",
      [fields.createdAt]: now.toISOString(),
      [fields.closedAt]: status === "Closed" ? now.toISOString() : null,
      [fields.isEscalated]: escalated,
      [fields.riskCategory]: risk,
      [fields.description]: id,
    });
    const worker = new SnapshotSyncWorker({
      pool: rosterPool([roster("risk-cases")]),
      source: sourceFor(async () => ({ kind: "mcp", queries: queryResults({ accountId: "sf-risk-cases", cases: [
        makeCase("open-risk", "Working", "Repayment", false),
        makeCase("closed-risk", "Closed", "Repayment", false),
        makeCase("escalated-risk", "Closed", "Security", true),
        makeCase("open-non-risk", "New", null, false),
      ] }) })),
      textLimits,
      now: () => now,
      publish: async (_pool, candidate) => { published = candidate; return {} as never; },
    });

    await worker.run();

    expect(published?.payload.cases.map(({ recordId }) => recordId)).toEqual(["open-risk", "escalated-risk"]);
    expect(published?.recordCounts.case).toBe(2);
  });

  it("fetches all source query results before validating the candidate and keeps failures customer-local", async () => {
    const customers = [roster("broken"), roster("healthy")];
    const fetched: string[] = [];
    const published: string[] = [];
    const failed: string[] = [];
    const worker = new SnapshotSyncWorker({
      pool: rosterPool(customers),
      source: sourceFor(async (customer) => {
        fetched.push(customer.customerId);
        if (customer.customerId === "broken") throw new Error("activity page failed after pagination retries");
        return { kind: "mcp", queries: queryResults({ accountId: "sf-healthy" }) };
      }),
      textLimits,
      now: () => now,
      runId: () => "run-isolation",
      publish: async (_pool, candidate) => { published.push(candidate.customerId); return {} as never; },
      recordFailure: async (_pool, attempt) => { failed.push(attempt.customerId); },
    });

    const result = await worker.run();

    expect(fetched).toEqual(["broken", "healthy"]);
    expect(result.customers.map(({ status }) => status)).toEqual(["failed", "published"]);
    expect(failed).toEqual(["broken"]);
    expect(published).toEqual(["healthy"]);
  });

  it("filters call Activities to the previous 90 days and retains only the five newest", async () => {
    let published: { payload: { callActivities: { recordId: string }[] }; recordCounts: { activity: number } } | undefined;
    const activities = Array.from({ length: 8 }, (_, index) => ({
      Id: `call-${index}`,
      WhatId: "sf-activity",
      LastModifiedDate: now.toISOString(),
      ActivityDate: new Date(now.getTime() - index * 24 * 60 * 60 * 1000).toISOString(),
      Call_Outcome__c: "Connected",
      Description: `call note ${index}`,
    }));
    activities.push({
      Id: "call-old",
      WhatId: "sf-activity",
      LastModifiedDate: now.toISOString(),
      ActivityDate: "2026-07-22T12:00:00.000Z",
      Call_Outcome__c: "Connected",
      Description: "outside prior 90 days",
    });
    const worker = new SnapshotSyncWorker({
      pool: rosterPool([roster("activity")]),
      source: sourceFor(async () => ({ kind: "mcp", queries: queryResults({ accountId: "sf-activity", activities }) })),
      textLimits,
      now: () => now,
      publish: async (_pool, candidate) => { published = candidate; return {} as never; },
    });

    await worker.run();

    expect(published?.payload.callActivities.map(({ recordId }) => recordId)).toEqual(["call-0", "call-1", "call-2", "call-3", "call-4"]);
    expect(published?.recordCounts.activity).toBe(9);
  });

  it("allows MCP's three total query attempts to exhaust, records failure, and preserves the prior publication", async () => {
    const read = vi.fn(async () => { throw Object.assign(new Error("unavailable"), { code: "UNAVAILABLE" }); });
    const failed: string[] = [];
    const published: string[] = [];
    const { createMcpSnapshotSource } = await import("../../server/src/snapshots/sync-worker.js");
    const worker = new SnapshotSyncWorker({
      pool: rosterPool([roster("retry")]),
      source: createMcpSnapshotSource({ read }, {
        serverName: "salesforce",
        queryToolName: "query",
        maxAttempts: 3,
        retryDelayMs: 0,
      }, async () => {}),
      textLimits,
      now: () => now,
      publish: async (_pool, candidate) => { published.push(candidate.customerId); return {} as never; },
      recordFailure: async (_pool, attempt) => { failed.push(attempt.customerId); },
    });

    const result = await worker.run();

    expect(read).toHaveBeenCalledTimes(3);
    expect(result.customers[0]?.status).toBe("failed");
    expect(failed).toEqual(["retry"]);
    expect(published).toEqual([]);
  });
});

describe("daily Salesforce snapshot scheduler", () => {
  it("runs demo fixtures once at startup and installs the daily schedule", async () => {
    const worker = { run: vi.fn(async () => ({ runId: "startup", customers: [] })) };
    const timers: (() => void)[] = [];
    const schedule = startDemoSnapshotSync(worker, {
      runAtUtc: "02:00",
      now: () => new Date("2026-10-21T00:00:00.000Z"),
      setTimer: ((callback: () => void) => {
        timers.push(callback);
        return {} as ReturnType<typeof setTimeout>;
      }) as typeof setTimeout,
      clearTimer: vi.fn(),
    });

    await vi.waitFor(() => expect(worker.run).toHaveBeenCalledTimes(1));
    expect(timers).toHaveLength(1);
    schedule.stop();
  });

  it("uses one configurable UTC run per day and schedules the next only after completion", async () => {
    let currentTime = new Date("2026-10-21T10:30:00.000Z");
    const timers: { callback: () => void; delay: number; handle: ReturnType<typeof setTimeout> }[] = [];
    const clearTimer = vi.fn();
    const worker = { run: vi.fn(async () => {}) };
    const schedule = scheduleDailySnapshotSync(worker, {
      runAtUtc: "11:00",
      now: () => currentTime,
      setTimer: ((callback: () => void, delay?: number) => {
        const handle = {} as ReturnType<typeof setTimeout>;
        timers.push({ callback, delay: delay ?? 0, handle });
        return handle;
      }) as typeof setTimeout,
      clearTimer,
    });

    expect(timers).toHaveLength(1);
    expect(timers[0]?.delay).toBe(30 * 60 * 1000);
    currentTime = new Date("2026-10-21T11:00:00.000Z");
    timers[0]?.callback();
    await vi.waitFor(() => expect(worker.run).toHaveBeenCalledTimes(1));
    await vi.waitFor(() => expect(timers).toHaveLength(2));
    expect(timers[1]?.delay).toBe(24 * 60 * 60 * 1000);
    schedule.stop();
    expect(clearTimer).toHaveBeenCalledWith(timers[1]?.handle);
  });

  it("validates and defaults the UTC schedule configuration", () => {
    expect(snapshotDailyRunTime({})).toBe("02:00");
    expect(snapshotDailyRunTime({ SALESFORCE_SNAPSHOT_DAILY_RUN_UTC: "23:45" })).toBe("23:45");
    expect(() => snapshotDailyRunTime({ SALESFORCE_SNAPSHOT_DAILY_RUN_UTC: "24:00" })).toThrow("HH:mm in UTC");
  });
});

function sourceFor(fetch: SnapshotSyncSource["fetchCustomer"]): SnapshotSyncSource {
  return { mode: "production_mcp", fetchCustomer: fetch };
}

function roster(customerId: string, isActive = true): CustomerRosterEntry {
  return {
    customerId,
    salesforceAccountId: `sf-${customerId}`,
    airwallexConnectedAccountId: `awx-${customerId}`,
    isActive,
    enrolledAt: now,
    enrolledBy: "test",
    enrollmentReason: "test fixture",
    deactivatedAt: null,
    deactivatedBy: null,
    deactivationReason: null,
    updatedAt: now,
  };
}

function rosterPool(customers: CustomerRosterEntry[]): DatabasePool & { sql: string[]; values: readonly unknown[][] } {
  const sql: string[] = [];
  const values: unknown[][] = [];
  return {
    sql,
    values,
    async query<Row>(text: string, parameters: readonly unknown[] = []): Promise<QueryResult<Row>> {
      sql.push(text.replace(/\s+/g, " ").trim());
      values.push([...parameters]);
      if (!text.includes("FROM customer_roster")) return { rows: [], rowCount: 0 };
      const afterCustomerId = parameters[0] as string | null | undefined;
      const limit = parameters[1] as number | undefined;
      const active = customers.filter((entry) => entry.isActive && (!afterCustomerId || entry.customerId > afterCustomerId));
      const rows = (limit === undefined ? active : active.slice(0, limit)).map((entry) => ({
        customer_id: entry.customerId,
        salesforce_account_id: entry.salesforceAccountId,
        airwallex_connected_account_id: entry.airwallexConnectedAccountId,
        is_active: entry.isActive,
        enrolled_at: entry.enrolledAt,
        enrolled_by: entry.enrolledBy,
        enrollment_reason: entry.enrollmentReason,
        deactivated_at: entry.deactivatedAt,
        deactivated_by: entry.deactivatedBy,
        deactivation_reason: entry.deactivationReason,
        updated_at: entry.updatedAt,
      })) as Row[];
      return { rows, rowCount: rows.length };
    },
    async connect() { throw new Error("not used"); },
    async end() {},
  };
}

function queryResults(options: { accountId?: string; cases?: Record<string, unknown>[]; activities?: Record<string, unknown>[] } = {}): SalesforceMcpQueryResults {
  const accountId = options.accountId ?? "sf-account";
  return {
    account: { records: [accountRecord(accountId)], complete: true, pages: 1 },
    contract: { records: [contractRecord(accountId)], complete: true, pages: 1 },
    case: { records: options.cases ?? [], complete: true, pages: 2 },
    activity: { records: options.activities ?? [], complete: true, pages: 1 },
  };
}

function accountRecord(accountId: string): Record<string, unknown> {
  const fields = SALESFORCE_FIELDS.account;
  return {
    Id: accountId,
    [fields.updatedAt]: now.toISOString(),
    [fields.customerStatus]: "Active",
    [fields.kybStatus]: "Verified",
    [fields.creditReviewStatus]: "Approved",
    [fields.approvedBridgeCap]: 10_000,
    [fields.priorDefaultStatus]: "No",
    [fields.loyaltyTier]: "Standard",
  };
}

function contractRecord(accountId: string): Record<string, unknown> {
  const fields = SALESFORCE_FIELDS.contract;
  return {
    Id: "sf-contract",
    AccountId: accountId,
    [fields.updatedAt]: now.toISOString(),
    [fields.status]: "Activated",
    [fields.endDate]: "2027-10-21",
  };
}

function caseRecord(index: number, accountId: string): Record<string, unknown> {
  const fields = SALESFORCE_FIELDS.case;
  return {
    Id: `sf-case-${index}`,
    AccountId: accountId,
    [fields.updatedAt]: now.toISOString(),
    [fields.status]: "Working",
    [fields.priority]: "Medium",
    [fields.reason]: "Risk review",
    [fields.createdAt]: now.toISOString(),
    [fields.closedAt]: null,
    [fields.isEscalated]: false,
    [fields.riskCategory]: "Repayment",
    [fields.description]: `Case ${index}`,
  };
}
