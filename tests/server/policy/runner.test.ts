import { describe, expect, it } from "vitest";
import type { DatabasePool, QueryResult } from "../../../server/src/db/pool.js";
import type { ExtractionResult } from "../../../server/src/ai-evidence/types.js";
import { getDemoSalesforceSnapshot } from "../../../server/src/salesforce/fixture-source.js";
import { runDailyPolicyPrechecks } from "../../../server/src/policy/runner.js";
import type { NoGoMappingReadiness } from "../../../server/src/policy/gates.js";

const now = new Date("2026-10-22T12:00:00Z");
const customerId = "acct_atlas001";
const snapshotId = "00000000-0000-4000-8000-000000000002";
const profileId = "00000000-0000-4000-8000-000000000001";
const evidenceId = "00000000-0000-4000-8000-000000000003";
const snapshotData = { ...getDemoSalesforceSnapshot(customerId)!, mappingVerified: true };
const evidence: ExtractionResult = {
  schemaVersion: 1, customerId, snapshotId, status: "no_signal", signals: [], unmapped: [],
  model: { modelId: "fixture", promptVersion: "p1", catalogVersion: "proposed-v1", outputSchemaVersion: 1, packetBuilderVersion: "packet-v1", calibrationVersion: null },
};
const mappings: NoGoMappingReadiness = {
  industry: { verified: true, version: "industry-v1" }, fraud: { verified: true, version: "fraud-v1" },
  priorDefault: { verified: true, version: "default-v1" }, concentration: { verified: true, version: "concentration-v1" },
  industryExcluded: false, fraudFlag: false,
};

class FakePool implements DatabasePool {
  writes: readonly unknown[][] = [];
  async query<Row>(sql: string, values: readonly unknown[] = []): Promise<QueryResult<Row>> {
    if (sql.includes("FROM customer_roster WHERE is_active ORDER BY customer_id")) {
      return { rows: ["acct_bloom001", customerId].map((id) => ({
        customer_id: id, salesforce_account_id: `sf-${id}`, airwallex_connected_account_id: `aw-${id}`,
        is_active: true, enrolled_at: new Date("2026-01-01"), enrolled_by: "test", enrollment_reason: "fixture",
        deactivated_at: null, deactivated_by: null, deactivation_reason: null, updated_at: now,
      })) as Row[], rowCount: 2 };
    }
    if (sql.includes("FROM underwriting_profile_current")) {
      const id = String(values[0]);
      if (id === "acct_bloom001") throw new Error("fixture profile database failure");
      return { rows: [{
        profile_id: profileId, customer_id: customerId, version: 1, run_id: "profile-run-1", source_mode: "demo",
        schema_version: 1, feature_version: "feature-v1",
        source_versions: { account: "a1", balances: "b1", transactions: "t1", expected_inflows: "i1", repayments: "r1" },
        window_start: new Date("2026-07-24"), window_end: new Date("2026-10-22"), started_at: now,
        captured_at: now, published_at: now,
        query_completion: Object.fromEntries(["account", "balances", "transactions", "expected_inflows", "repayments"].map((key) => [key, { complete: true, pages: 1 }])),
        canonical_payload: {
          averageMonthlySettledInflowsCents: 1_000_000, expectedDateReliability: 0.95, topPayerConcentration: 0.4,
          lowBalanceDays: 3, historyDays: 90, eligibleSpend: "supported", connectedAccountStatus: "active",
          openRfi: false, repaymentHistory: "never_borrowed",
        },
      }] as Row[], rowCount: 1 };
    }
    if (sql.includes("FROM customer_roster AS roster")) {
      return { rows: [{
        is_active: true, snapshot_id: snapshotId, version: 1, source_mode: "demo", schema_version: 1,
        mapping_version: "sf-map-v1", run_id: "snapshot-run-1", captured_at: now, published_at: now,
        canonical_payload: snapshotData, attempt_run_id: "snapshot-run-1", attempt_status: "succeeded", attempt_finished_at: now,
      }] as Row[], rowCount: 1 };
    }
    if (sql.includes("FROM ai_evidence_extractions")) return { rows: [{ extraction_id: evidenceId, configuration_version: "config-v1", output: evidence }] as Row[], rowCount: 1 };
    if (sql.includes("FROM bridge_cases bridge")) return { rows: [{ fully_repaid_on_time: 0, ledger_sequence: 1 }] as Row[], rowCount: 1 };
    if (sql.includes("INSERT INTO policy_evaluations")) {
      this.writes = [...this.writes, values];
      return { rows: [], rowCount: 1 };
    }
    if (sql.includes("SELECT evaluation_id FROM policy_evaluations")) return { rows: [{ evaluation_id: "evaluation-1" }] as Row[], rowCount: 1 };
    throw new Error(`Unexpected SQL: ${sql}`);
  }
  async connect() { throw new Error("not used"); }
  async end() {}
}

describe("daily policy runner", () => {
  it("waits for source tuples, persists complete customers, and continues after a customer failure", async () => {
    const pool = new FakePool();
    const results = await runDailyPolicyPrechecks({
      pool, now, mappings, qualifications: {},
      facility: { facilitySize: 1_000_000, reserveFloorPct: 0.2, maxPerCustomerPct: 0.2, feeRatePer30Days: 0, minHistoryDays: 90 },
    });
    expect(results).toEqual([
      { customerId: "acct_bloom001", status: "failed", reason: "fixture profile database failure" },
      { customerId, status: "evaluated", evaluationId: "evaluation-1", outcome: "ready" },
    ]);
    expect(pool.writes).toHaveLength(1);
    expect(pool.writes[0]?.[2]).toBe(customerId);
    expect(pool.writes[0]?.[3]).toBe(profileId);
    expect(pool.writes[0]?.[4]).toBe(snapshotId);
    expect(pool.writes[0]?.[5]).toBe(evidenceId);
    expect(JSON.parse(String(pool.writes[0]?.[15]))).toMatchObject({
      profileVersion: 1, salesforceSnapshotVersion: 1,
      evidenceRunId: evidenceId, evidenceCatalogVersion: "proposed-v1",
    });
  });
});
