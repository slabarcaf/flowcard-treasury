import { describe, expect, it, vi } from "vitest";
import type { DatabasePool } from "../../../server/src/db/pool.js";
import { DailyEvidenceRunner } from "../../../server/src/ai-evidence/runner.js";
import type { EvidenceModelProvider } from "../../../server/src/ai-evidence/provider.js";
import { SnapshotSyncWorker } from "../../../server/src/snapshots/sync-worker.js";
import bloom from "../../../server/src/salesforce/fixtures/bloom.json";
import { FixtureEvidenceProvider } from "../../../server/src/ai-evidence/fixture-provider.js";
import type { QueryResult, DatabaseClient } from "../../../server/src/db/pool.js";

const customer = {
  customerId: "customer-a", salesforceAccountId: "sf-a", airwallexConnectedAccountId: "awx-a", isActive: true,
  enrolledAt: new Date(), enrolledBy: "test", enrollmentReason: "test", deactivatedAt: null, deactivatedBy: null,
  deactivationReason: null, updatedAt: new Date(),
};

function pool(snapshotState: "missing" | "stale"): DatabasePool {
  return {
    async query() {
      return { rowCount: 1, rows: [{ is_active: true, snapshot_id: null, version: null, source_mode: null,
        schema_version: null, mapping_version: null, run_id: null, captured_at: null, published_at: null,
        canonical_payload: null, attempt_run_id: "run-a", attempt_status: snapshotState === "stale" ? "failed" : null,
        attempt_finished_at: snapshotState === "stale" ? new Date() : null }] } as never;
    },
    async connect() { throw new Error("provider must not reach persistence for incomplete snapshots"); },
    async end() {},
  };
}

const provider: EvidenceModelProvider = {
  metadata: { modelId: "fixture", promptVersion: "p1", catalogVersion: "proposed-v1", outputSchemaVersion: 1,
    packetBuilderVersion: "packet-v1", calibrationVersion: null, configurationVersion: "cfg-v1" },
  toolsEnabled: false,
  extract: vi.fn(async () => { throw new Error("stale snapshot must not be sent to model"); }),
};

describe("daily AI evidence runner", () => {
  it("requires a complete fresh snapshot before any provider request", async () => {
    const runner = new DailyEvidenceRunner({ pool: pool("stale"), provider,
      packet: { maxPacketBytes: 10_000, maxNarrativeChars: 2_000 } });
    const outcome = await runner.runCustomer(customer);
    expect(outcome).toMatchObject({ customerId: "customer-a", status: "skipped", allowed: false });
    expect(provider.extract).not.toHaveBeenCalled();
  });

  it("continues the daily customer batch after one customer's extraction callback fails", async () => {
    const customers = [customer, { ...customer, customerId: "customer-b", salesforceAccountId: "sf-b" }];
    const callbackCustomers: string[] = [];
    const worker = new SnapshotSyncWorker({
      pool: { ...pool("missing"), async query() { return { rowCount: 2, rows: customers.map((item) => ({
        customer_id: item.customerId, salesforce_account_id: item.salesforceAccountId,
        airwallex_connected_account_id: item.airwallexConnectedAccountId, is_active: true,
        enrolled_at: item.enrolledAt, enrolled_by: item.enrolledBy, enrollment_reason: item.enrollmentReason,
        deactivated_at: null, deactivated_by: null, deactivation_reason: null, updated_at: item.updatedAt,
      })) }; } } as DatabasePool,
      source: { mode: "demo", async fetchCustomer() { return { kind: "fixture", payload: bloom as never }; } },
      textLimits: { maxCaseTextLength: 2_000, maxActivityTextLength: 2_000 },
      publish: async () => ({} as never),
      onPublished: async (item) => {
        callbackCustomers.push(item.customerId);
        if (item.customerId === "customer-a") throw new Error("customer-specific failure");
      },
    });
    const errorSpy = vi.spyOn(console, "error").mockImplementation(() => {});
    try {
      const result = await worker.run();
      expect(callbackCustomers).toEqual(["customer-a", "customer-b"]);
      expect(result.customers.map((item) => item.status)).toEqual(["published", "published"]);
    } finally {
      errorSpy.mockRestore();
    }
  });

  it("runs a ready published snapshot through the provider, persists it, and returns the customer gate", async () => {
    const now = new Date("2026-10-22T12:00:00Z");
    const persisted: unknown[][] = [];
    const completeSnapshot = {
      is_active: true, snapshot_id: "00000000-0000-4000-8000-000000000031", version: 1, source_mode: "demo",
      schema_version: 1, mapping_version: "proposed-v1", run_id: "run-a", captured_at: now, published_at: now,
      canonical_payload: bloom, attempt_run_id: "run-a", attempt_status: "succeeded", attempt_finished_at: now,
    };
    const integrationPool: DatabasePool = {
      async query<Row>(sql: string): Promise<QueryResult<Row>> {
        if (sql.includes("FROM ai_evidence_reviews")) return { rows: [], rowCount: 0 };
        return { rows: [completeSnapshot as Row], rowCount: 1 };
      },
      async connect(): Promise<DatabaseClient> {
        return {
          async query<Row>(sql: string, values: readonly unknown[] = []) {
            if (sql.includes("INSERT INTO ai_evidence_extractions")) persisted.push([...values]);
            return { rows: [], rowCount: 1 } as QueryResult<Row>;
          },
          release() {},
        };
      },
      async end() {},
    };
    const fixtureProvider = new FixtureEvidenceProvider();
    const extractSpy = vi.spyOn(fixtureProvider, "extract");
    const runner = new DailyEvidenceRunner({ pool: integrationPool, provider: fixtureProvider,
      packet: { maxPacketBytes: 32_000, maxNarrativeChars: 4_000 }, now: () => now });

    const outcome = await runner.runCustomer({ ...customer, customerId: "acct_bloom001" });

    expect(extractSpy).toHaveBeenCalledOnce();
    expect(outcome).toMatchObject({ customerId: "acct_bloom001", status: "no_signal", allowed: true });
    expect(outcome.extractionId).toMatch(/^[0-9a-f-]{36}$/);
    expect(persisted).toHaveLength(1);
    expect(persisted[0]).toContain("no_signal");
    expect(persisted[0]).toContain(true);
  });
});
