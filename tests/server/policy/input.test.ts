import { describe, expect, it } from "vitest";
import { joinPolicyInputs } from "../../../server/src/policy/input.js";
import { getDemoSalesforceSnapshot } from "../../../server/src/salesforce/fixture-source.js";
import type { CustomerRosterEntry } from "../../../server/src/roster/types.js";
import type { ExtractionResult } from "../../../server/src/ai-evidence/types.js";
import type { CurrentProfileReadModel } from "../../../server/src/underwriting/repository.js";
import type { CustomerSnapshotReadModel } from "../../../server/src/snapshots/freshness.js";
import type { NoGoMappingReadiness } from "../../../server/src/policy/gates.js";

const roster: CustomerRosterEntry = {
  customerId: "acct_atlas001", salesforceAccountId: "sf-atlas", airwallexConnectedAccountId: "aw-atlas",
  isActive: true, enrolledAt: new Date("2026-01-01"), enrolledBy: "test", enrollmentReason: "fixture",
  deactivatedAt: null, deactivatedBy: null, deactivationReason: null, updatedAt: new Date("2026-01-01"),
};
const profile: CurrentProfileReadModel = {
  freshness: "fresh", ageSeconds: 60, profile: {
    customerId: roster.customerId, profileId: "p1", version: 1, runId: "r1", sourceMode: "demo", schemaVersion: 1,
    featureVersion: "features-v1", sourceVersions: { account: "v1", balances: "v1", transactions: "v1", expected_inflows: "v1", repayments: "v1" },
    windowStart: new Date("2026-07-24"), windowEnd: new Date("2026-10-22"), startedAt: new Date("2026-10-22"),
    capturedAt: new Date("2026-10-22"), publishedAt: new Date("2026-10-22"),
    queryCompletion: Object.fromEntries(["account", "balances", "transactions", "expected_inflows", "repayments"].map((key) => [key, { complete: true, pages: 1 }])) as never,
    data: {
      averageMonthlySettledInflowsCents: 100_000, expectedDateReliability: 0.95, topPayerConcentration: 0.4,
      lowBalanceDays: 3, historyDays: 90, eligibleSpend: "supported", connectedAccountStatus: "active", openRfi: false,
      repaymentHistory: "on_time",
    },
  },
};
const snapshotData = getDemoSalesforceSnapshot(roster.customerId)!;
const snapshot: CustomerSnapshotReadModel = {
  customerId: roster.customerId, active: true, freshness: "fresh", ageSeconds: 60,
  snapshot: { snapshotId: "s1", version: 1, capturedAt: "2026-10-22T00:00:00Z", publishedAt: "2026-10-22T00:00:00Z", sourceMode: "demo", schemaVersion: 1, mappingVersion: "sf-v1", data: snapshotData },
  latestAttempt: { status: "succeeded", finishedAt: "2026-10-22T00:00:00Z" }, validationErrors: [],
};
const evidence: ExtractionResult = {
  schemaVersion: 1, customerId: roster.customerId, snapshotId: "s1", status: "no_signal", signals: [], unmapped: [],
  model: { modelId: "fixture", promptVersion: "p1", catalogVersion: "proposed-v1", outputSchemaVersion: 1, packetBuilderVersion: "p1", calibrationVersion: null },
};
const verifiedMappings: NoGoMappingReadiness = {
  industry: { verified: true, version: "industry-v1" }, fraud: { verified: true, version: "fraud-v1" },
  priorDefault: { verified: true, version: "default-v1" }, concentration: { verified: true, version: "concentration-v1" },
  industryExcluded: false, fraudFlag: false,
};

describe("policy input join", () => {
  it("joins only canonical policy values and fails closed for unverified demo CRM mappings", () => {
    const joined = joinPolicyInputs({ roster, profile, snapshot, evidence, qualifications: {}, now: new Date("2026-10-22T12:00:00Z") });
    expect(joined.customerId).toBe(roster.customerId);
    expect(joined.snapshotId).toBe("s1");
    expect(joined.profileId).toBe("p1");
    expect(joined.signals).toBeNull();
    expect(joined.blockers.map(({ code }) => code)).toEqual(expect.arrayContaining(["SNAPSHOT_INCOMPLETE", "INDUSTRY_MAPPING_UNVERIFIED", "FRAUD_MAPPING_UNVERIFIED"]));
    expect(JSON.stringify(joined)).not.toContain("Spoke with");
    expect(JSON.stringify(joined)).not.toContain("note");
  });

  it("blocks missing or stale source versions and customer-mismatched evidence", () => {
    const result = joinPolicyInputs({ roster, profile: { ...profile, freshness: "stale" }, snapshot, evidence: { ...evidence, customerId: "other" }, qualifications: {} });
    expect(result.blockers.map(({ code }) => code)).toEqual(expect.arrayContaining(["PROFILE_STALE", "EVIDENCE_CUSTOMER_MISMATCH"]));
  });

  it("does not leak evidence text or rationale through the policy signal boundary", () => {
    const result = joinPolicyInputs({ roster, profile, snapshot, evidence, qualifications: {} });
    expect(Object.keys(result).sort()).toEqual(["approvedCap", "blockers", "customerId", "humanApprovalRequired", "priorDefault", "profileId", "signals", "snapshotId"].sort());
  });

  it("preserves valid no_signal and emits only typed ratings for explicitly mapped evidence", () => {
    const verifiedSnapshot = structuredClone(snapshot);
    verifiedSnapshot.snapshot!.data!.mappingVerified = true;
    const noSignal = joinPolicyInputs({ roster, profile, snapshot: verifiedSnapshot, evidence, qualifications: {}, noGoMappings: verifiedMappings });
    expect(noSignal.blockers).toEqual([]);
    expect(noSignal.signals).toEqual({ inflowReliability: "green", inflowConcentration: "green", cashBuffer: "green", trackRecord: "green" });

    const success: ExtractionResult = {
      ...evidence, status: "success", model: { ...evidence.model, calibrationVersion: "cal-v1" },
      signals: [{ type: "client_payment_dispute", value: "confirmed", counterparty: { name: "Private Client" },
        evidence: [{ sourceType: "call_activity", sourceId: "activity-1" }],
        quotes: [{ sourceType: "call_activity", sourceId: "activity-1", quote: "private raw text", start: 0, end: 16 }],
        rawScore: 0.99, calibratedScore: 0.99, alternatives: [], selectionRationale: "private rationale" }],
    };
    const qualified = { signalType: "client_payment_dispute" as const, calibrationVersion: "cal-v1",
      policyVersion: "one-sided-exact-binomial-95-v1" as const, evaluatedAt: "2026-10-22T00:00:00Z", sampleCount: 59,
      errors: 0, precisionLowerBound: 0.95, qualified: true, mode: "auto_apply_with_audit" as const };
    const mapped = joinPolicyInputs({ roster, profile, snapshot: verifiedSnapshot, evidence: success,
      qualifications: { client_payment_dispute: qualified }, noGoMappings: verifiedMappings,
      signalMappings: { client_payment_dispute: { key: "trackRecord", ratingsByValue: { '"confirmed"': "red" } } } });
    expect(mapped.blockers).toEqual([]);
    expect(mapped.signals?.trackRecord).toBe("red");
    expect(JSON.stringify(mapped)).not.toContain("private raw text");
    expect(JSON.stringify(mapped)).not.toContain("private rationale");
    expect(JSON.stringify(mapped)).not.toContain("Private Client");
  });
});
