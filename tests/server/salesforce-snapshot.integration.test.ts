import { describe, expect, it } from "vitest";
import type { DatabasePool, QueryResult } from "../../server/src/db/pool.js";
import { getDemoSalesforceSnapshot } from "../../server/src/salesforce/fixture-source.js";
import type { CustomerRosterEntry } from "../../server/src/roster/types.js";
import { createDemoSnapshotSource, SnapshotSyncWorker } from "../../server/src/snapshots/sync-worker.js";
import type { SnapshotCandidate } from "../../server/src/snapshots/repository.js";

const now = new Date("2026-10-21T12:00:00.000Z");
const textLimits = { maxCaseTextLength: 200, maxActivityTextLength: 200 };

describe("Salesforce snapshot acceptance composition", () => {
  it("publishes complete fixture snapshots for active roster customers", async () => {
    const published: SnapshotCandidate[] = [];
    const worker = new SnapshotSyncWorker({
      pool: rosterPool([roster("acct_atlas001"), roster("acct_bloom001"), roster("acct_pixel001", false)]),
      source: createDemoSnapshotSource(),
      textLimits,
      now: () => now,
      runId: () => "acceptance-fixture-run",
      publish: async (_pool, candidate) => { published.push(candidate); return {} as never; },
    });

    const result = await worker.run();

    expect(result.customers.map(({ customerId, status }) => [customerId, status])).toEqual([
      ["acct_atlas001", "published"],
      ["acct_bloom001", "published"],
    ]);
    expect(published).toHaveLength(2);
    expect(published.every((candidate) => Object.values(candidate.queryCompletion).every(({ complete }) => complete))).toBe(true);
    const bloomFixture = getDemoSalesforceSnapshot("acct_bloom001");
    expect(bloomFixture).not.toBeNull();
    expect(published.find(({ customerId }) => customerId === "acct_bloom001")?.payload).toEqual({
      ...bloomFixture!,
      mappingVersion: "salesforce-demo-fixture-v1",
      mappingVerified: true,
    });
  });

  it("keeps a per-customer source failure isolated from complete fixture publication", async () => {
    const published: string[] = [];
    const failed: string[] = [];
    const worker = new SnapshotSyncWorker({
      pool: rosterPool([roster("acct_atlas001"), roster("acct_bloom001")]),
      source: {
        mode: "demo",
        async fetchCustomer(customer) {
          if (customer.customerId === "acct_bloom001") throw new Error("fixture unavailable");
          const payload = getDemoSalesforceSnapshot(customer.customerId);
          if (!payload) throw new Error("fixture unavailable");
          return { kind: "fixture", payload };
        },
      },
      textLimits,
      now: () => now,
      runId: () => "acceptance-isolation-run",
      publish: async (_pool, candidate) => { published.push(candidate.customerId); return {} as never; },
      recordFailure: async (_pool, attempt) => { failed.push(attempt.customerId); },
    });

    const result = await worker.run();

    expect(result.customers.map(({ customerId, status }) => [customerId, status])).toEqual([
      ["acct_atlas001", "published"],
      ["acct_bloom001", "failed"],
    ]);
    expect(published).toEqual(["acct_atlas001"]);
    expect(failed).toEqual(["acct_bloom001"]);
  });

  it("isolates a publication failure and still publishes the next customer", async () => {
    const currentVersions = new Map([["acct_atlas001", 1]]);
    const attempts: string[] = [];
    const worker = new SnapshotSyncWorker({
      pool: rosterPool([roster("acct_atlas001"), roster("acct_bloom001")]),
      source: createDemoSnapshotSource(),
      textLimits,
      now: () => now,
      runId: () => "acceptance-publication-run",
      publish: async (_pool, candidate) => {
        if (candidate.customerId === "acct_atlas001") throw new Error("publication transaction rolled back");
        currentVersions.set(candidate.customerId, 1);
        return {} as never;
      },
      recordFailure: async (_pool, attempt) => { attempts.push(attempt.customerId); },
    });

    const result = await worker.run();

    expect(result.customers.map(({ customerId, status }) => [customerId, status])).toEqual([
      ["acct_atlas001", "failed"],
      ["acct_bloom001", "published"],
    ]);
    expect(currentVersions).toEqual(new Map([["acct_atlas001", 1], ["acct_bloom001", 1]]));
    expect(attempts).toEqual(["acct_atlas001"]);
  });
});

function rosterPool(entries: CustomerRosterEntry[]): DatabasePool {
  return {
    async query<Row>(text: string): Promise<QueryResult<Row>> {
      if (!text.includes("FROM customer_roster") || !text.includes("WHERE is_active")) return { rows: [], rowCount: 0 };
      const rows = entries.filter(({ isActive }) => isActive).map((entry) => ({
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
    async connect() { throw new Error("The acceptance composition test uses the injected publisher"); },
    async end() {},
  };
}

function roster(customerId: string, isActive = true): CustomerRosterEntry {
  return {
    customerId,
    salesforceAccountId: `sf-${customerId}`,
    airwallexConnectedAccountId: `awx-${customerId}`,
    isActive,
    enrolledAt: now,
    enrolledBy: "acceptance-test",
    enrollmentReason: "synthetic acceptance fixture",
    deactivatedAt: isActive ? null : now,
    deactivatedBy: isActive ? null : "acceptance-test",
    deactivationReason: isActive ? null : "inactive fixture",
    updatedAt: now,
  };
}
