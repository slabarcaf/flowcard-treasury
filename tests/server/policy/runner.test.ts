import { describe, expect, it } from "vitest";
import type { DatabasePool, QueryResult } from "../../../server/src/db/pool.js";
import type { ExtractionResult } from "../../../server/src/ai-evidence/types.js";
import { getDemoSalesforceSnapshot } from "../../../server/src/salesforce/fixture-source.js";
import { runDailyPolicyPrechecks } from "../../../server/src/policy/runner.js";
import type { NoGoMappingReadiness } from "../../../server/src/policy/gates.js";
import { buildEvidencePacket } from "../../../server/src/ai-evidence/packet.js";
import { FixtureEvidenceProvider } from "../../../server/src/ai-evidence/fixture-provider.js";
import { extractEvidence } from "../../../server/src/ai-evidence/extract.js";
import { applyReviewToNextExtractionWithProvenance } from "../../../server/src/ai-evidence/review.js";

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
  decisionAllowed: boolean | undefined = true;
  async query<Row>(sql: string, values: readonly unknown[] = []): Promise<QueryResult<Row>> {
    if (sql.includes("SELECT customer_id, salesforce_account_id") && sql.includes("FROM customer_roster")) {
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
    if (sql.includes("FROM ai_evidence_extractions")) return { rows: [{ extraction_id: evidenceId, configuration_version: "config-v1", output: evidence,
      decision_allowed: this.decisionAllowed, decision_block_reason: this.decisionAllowed ? null : "fixture gate denied" }] as Row[], rowCount: 1 };
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

  it.each([false, undefined])("rejects stored evidence with decision_allowed=%s even when output is no_signal", async (decisionAllowed) => {
    const pool = new FakePool();
    pool.decisionAllowed = decisionAllowed;
    const results = await runDailyPolicyPrechecks({
      pool, now, mappings, qualifications: {},
      facility: { facilitySize: 1_000_000, reserveFloorPct: 0.2, maxPerCustomerPct: 0.2, feeRatePer30Days: 0, minHistoryDays: 90 },
    });
    expect(results.find(({ customerId: id }) => id === customerId)).toMatchObject({ status: "evaluated", outcome: "blocked" });
    expect(pool.writes).toHaveLength(1);
  });

  it("keeps Bloom's daily policy blocked until its configured reviewer resolves the exact source-bound result", async () => {
    const bloomCustomerId = "acct_bloom001";
    const bloomSnapshotId = "00000000-0000-4000-8000-000000000022";
    const bloomProfileId = "00000000-0000-4000-8000-000000000021";
    const bloomRosterEntry = { customerId: bloomCustomerId, salesforceAccountId: "demo-sf-bloom001",
      airwallexConnectedAccountId: "fixture-account-alias", isActive: true, enrolledAt: now,
      enrolledBy: "test", enrollmentReason: "synthetic fixture", deactivatedAt: null,
      deactivatedBy: null, deactivationReason: null, updatedAt: now };
    const bloomData = { averageMonthlySettledInflowsCents: 4_000_000, expectedDateReliability: 0.94,
      topPayerConcentration: 0.38, lowBalanceDays: 8, historyDays: 90, eligibleSpend: "supported",
      connectedAccountStatus: "active", openRfi: false, repaymentHistory: "never_borrowed" };
    const canonical = { ...getDemoSalesforceSnapshot(bloomCustomerId)!, mappingVerified: true };
    const packetReadModel = { customerId: bloomCustomerId, active: true, freshness: "fresh" as const,
      ageSeconds: 0, snapshot: { snapshotId: bloomSnapshotId, version: 2, capturedAt: now.toISOString(),
        publishedAt: now.toISOString(), sourceMode: "demo" as const, schemaVersion: 1,
        mappingVersion: "sf-map-v1", data: canonical }, latestAttempt: { status: "succeeded" as const,
        finishedAt: now.toISOString() }, validationErrors: [] };
    const packetResult = buildEvidencePacket(packetReadModel, { now, maxPacketBytes: 32_000, maxNarrativeChars: 4_000 });
    if (packetResult.status !== "ready") throw new Error("Bloom fixture packet must be ready");
    const provider = new FixtureEvidenceProvider();
    const currentEvidence = await extractEvidence(packetResult.packet, provider);
    const priorEvidence = await extractEvidence({ ...packetResult.packet, snapshotId: "bloom-reviewed-snapshot" }, provider);
    const reviewed = applyReviewToNextExtractionWithProvenance(currentEvidence, priorEvidence, {
      extractionId: "bloom-reviewed-run", reviewerId: "configured-reviewer", action: "confirmed",
    }, packetResult.packet);
    expect(reviewed.exactReviewerResolution).toBe(true);

    class BloomPolicyPool implements DatabasePool {
      writes: readonly unknown[][] = [];
      constructor(private readonly savedEvidence: ExtractionResult, private readonly decisionAllowed: boolean) {}
      async query<Row>(sql: string, values: readonly unknown[] = []): Promise<QueryResult<Row>> {
        if (sql.includes("SELECT customer_id, salesforce_account_id") && sql.includes("FROM customer_roster")) {
          return { rows: [{ customer_id: bloomCustomerId, salesforce_account_id: bloomRosterEntry.salesforceAccountId,
            airwallex_connected_account_id: bloomRosterEntry.airwallexConnectedAccountId, is_active: true,
            enrolled_at: now, enrolled_by: "test", enrollment_reason: "synthetic fixture", deactivated_at: null,
            deactivated_by: null, deactivation_reason: null, updated_at: now } as Row], rowCount: 1 };
        }
        if (sql.includes("FROM underwriting_profile_current")) {
          return { rows: [{ profile_id: bloomProfileId, customer_id: bloomCustomerId, version: 1, run_id: "bloom-profile",
            source_mode: "demo", schema_version: 1, feature_version: "fixture-v1",
            source_versions: { account: "a1", balances: "b1", transactions: "t1", expected_inflows: "i1", repayments: "r1" },
            window_start: new Date("2026-07-24T00:00:00Z"), window_end: now, started_at: now, captured_at: now,
            published_at: now, query_completion: Object.fromEntries(["account", "balances", "transactions", "expected_inflows", "repayments"]
              .map((key) => [key, { complete: true, pages: 1 }])), canonical_payload: bloomData } as Row], rowCount: 1 };
        }
        if (sql.includes("FROM customer_roster AS roster")) {
          return { rows: [{ is_active: true, snapshot_id: bloomSnapshotId, version: 2, source_mode: "demo",
            schema_version: 1, mapping_version: "sf-map-v1", run_id: "bloom-snapshot-run", captured_at: now,
            published_at: now, canonical_payload: canonical, attempt_run_id: "bloom-snapshot-run",
            attempt_status: "succeeded", attempt_finished_at: now } as Row], rowCount: 1 };
        }
        if (sql.includes("FROM ai_evidence_extractions")) {
          return { rows: [{ extraction_id: "bloom-evidence-run", configuration_version: "fixture-v1",
            output: this.savedEvidence, decision_allowed: this.decisionAllowed,
            decision_block_reason: this.decisionAllowed ? null : "Evidence requires reviewer resolution" } as Row], rowCount: 1 };
        }
        if (sql.includes("FROM bridge_cases bridge")) return { rows: [{ fully_repaid_on_time: 0, ledger_sequence: 12 } as Row], rowCount: 1 };
        if (sql.includes("INSERT INTO policy_evaluations")) {
          this.writes = [...this.writes, [...values]];
          return { rows: [], rowCount: 1 };
        }
        if (sql.includes("SELECT evaluation_id FROM policy_evaluations")) return { rows: [{ evaluation_id: "bloom-policy-evaluation" } as Row], rowCount: 1 };
        throw new Error(`Unexpected SQL: ${sql}`);
      }
      async connect() { throw new Error("daily policy route does not open a transaction client"); }
      async end() {}
    }

    const run = async (savedEvidence: ExtractionResult, decisionAllowed: boolean) => {
      const pool = new BloomPolicyPool(savedEvidence, decisionAllowed);
      const results = await runDailyPolicyPrechecks({ pool, now, mappings, qualifications: {}, customerIds: [bloomCustomerId],
        facility: { facilitySize: 1_000_000, reserveFloorPct: 0.2, maxPerCustomerPct: 0.2, feeRatePer30Days: 0, minHistoryDays: 90 } });
      return { result: results[0], pool };
    };

    const blocked = await run(currentEvidence, false);
    expect(blocked.result).toMatchObject({ customerId: bloomCustomerId, status: "evaluated", outcome: "blocked" });
    expect(blocked.pool.writes[0]?.[10]).toBe("none");
    const accepted = await run(reviewed.result, true);
    expect(accepted.result).toMatchObject({ customerId: bloomCustomerId, status: "evaluated", outcome: "ready" });
    expect(accepted.pool.writes[0]?.[10]).toBe("C");
    expect(accepted.pool.writes[0]?.[11]).toBe(400_000);
    expect(JSON.parse(String(accepted.pool.writes[0]?.[9]))).toEqual({ inflowReliability: "yellow",
      inflowConcentration: "yellow", cashBuffer: "yellow", trackRecord: "green" });
  });

});
