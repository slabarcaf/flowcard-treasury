import { describe, expect, it, vi } from "vitest";
import type { DatabaseClient, DatabasePool, QueryResult } from "../../../server/src/db/pool.js";
import { DailyEvidenceRunner } from "../../../server/src/ai-evidence/runner.js";
import type { EvidenceModelProvider } from "../../../server/src/ai-evidence/provider.js";
import { fitCalibrationMapping, type TypeCalibrationMapping } from "../../../server/src/ai-evidence/calibration.js";
import type { TypeQualification } from "../../../server/src/ai-evidence/qualification.js";
import { SnapshotSyncWorker } from "../../../server/src/snapshots/sync-worker.js";
import { FixtureEvidenceProvider } from "../../../server/src/ai-evidence/fixture-provider.js";
import bloom from "../../../server/src/salesforce/fixtures/bloom.json";

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

  it("uses server calibration for positive evidence and persists the fail-closed gate result", async () => {
    const now = new Date("2026-10-22T12:00:00Z");
    const qualification: TypeQualification = {
      signalType: "client_payment_dispute", calibrationVersion: "server-cal-v1",
      policyVersion: "one-sided-exact-binomial-95-v1", evaluatedAt: now.toISOString(), sampleCount: 59,
      errors: 0, precisionLowerBound: 0.95, qualified: true, mode: "auto_apply_with_audit",
    };
    const calibration = fitCalibrationMapping("client_payment_dispute", "server-cal-v1", [
      { signalType: "client_payment_dispute", rawScore: 0.99, correct: true, labelId: "label-1", labeledAt: now.toISOString() },
    ]);
    const positiveProvider: EvidenceModelProvider = {
      metadata: { modelId: "fixture-positive", promptVersion: "p1", catalogVersion: "proposed-v1", outputSchemaVersion: 1,
        packetBuilderVersion: "packet-v1", calibrationVersion: "provider-claimed-v9", configurationVersion: "cfg-positive" },
      toolsEnabled: false,
      async extract(packet) {
        const entry = packet.entries.find(({ fields }) => typeof fields.note === "string" && fields.note.includes("Bloom"));
        if (!entry || typeof entry.fields.note !== "string") throw new Error("fixture packet lacks Bloom note");
        const reference = { sourceType: entry.sourceType, sourceId: entry.sourceIds[0]! };
        return {
          schemaVersion: 1, customerId: packet.customerId, snapshotId: packet.snapshotId,
          status: "success", unmapped: [], model: {
            modelId: positiveProvider.metadata.modelId, promptVersion: positiveProvider.metadata.promptVersion,
            catalogVersion: positiveProvider.metadata.catalogVersion, outputSchemaVersion: positiveProvider.metadata.outputSchemaVersion,
            packetBuilderVersion: positiveProvider.metadata.packetBuilderVersion,
            calibrationVersion: positiveProvider.metadata.calibrationVersion,
          },
          signals: [{ type: "client_payment_dispute", value: { disputed: true }, counterparty: { name: "Bloom" },
            evidence: [reference], quotes: [{ ...reference, quote: entry.fields.note, start: 0, end: entry.fields.note.length }],
            rawScore: 0.99, calibratedScore: 0.01, alternatives: [], selectionRationale: null }],
        };
      },
    };
    const run = async (mapping?: TypeCalibrationMapping) => {
      const persisted: unknown[][] = [];
      const snapshot = {
        is_active: true, snapshot_id: "00000000-0000-4000-8000-000000000031", version: 1, source_mode: "demo",
        schema_version: 1, mapping_version: "proposed-v1", run_id: "run-a", captured_at: now, published_at: now,
        canonical_payload: bloom, attempt_run_id: "run-a", attempt_status: "succeeded", attempt_finished_at: now,
      };
      const integrationPool: DatabasePool = {
        async query<Row>(sql: string): Promise<QueryResult<Row>> {
          if (sql.includes("FROM ai_evidence_reviews")) return { rows: [], rowCount: 0 };
          return { rows: [snapshot as Row], rowCount: 1 };
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
      const runner = new DailyEvidenceRunner({
        pool: integrationPool, provider: positiveProvider,
        packet: { maxPacketBytes: 32_000, maxNarrativeChars: 4_000 }, now: () => now,
        qualifications: async () => ({ client_payment_dispute: qualification }),
        calibrations: async () => mapping ? { client_payment_dispute: mapping } : {},
      });
      const outcome = await runner.runCustomer({ ...customer, customerId: "acct_bloom001" });
      return { outcome, persisted };
    };

    const missing = await run();
    expect(missing.outcome).toMatchObject({ allowed: false, reason: expect.stringContaining("calibration score") });
    expect(missing.persisted[0]?.[6]).toBe(false);
    expect(JSON.parse(String(missing.persisted[0]?.[8])).signals[0].calibratedScore).toBeNull();

    const invalid = await run({ ...calibration, mappingVersion: "unsupported" } as unknown as TypeCalibrationMapping);
    expect(invalid.outcome).toMatchObject({ allowed: false, reason: expect.stringContaining("invalid calibration") });
    expect(invalid.persisted[0]?.[6]).toBe(false);

    const allowed = await run(calibration);
    expect(allowed.outcome.allowed).toBe(true);
    expect(allowed.persisted[0]?.[6]).toBe(true);
    const stored = JSON.parse(String(allowed.persisted[0]?.[8])) as { model: { calibrationVersion: string | null }; signals: Array<{ calibratedScore: number | null }> };
    expect(stored.model.calibrationVersion).toBe("server-cal-v1");
    expect(stored.signals[0]?.calibratedScore).toBe(1);
  });

  it("persists valid no_signal as allowed for a complete published snapshot", async () => {
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
