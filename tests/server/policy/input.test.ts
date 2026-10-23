import { describe, expect, it } from "vitest";
import { joinPolicyInputs } from "../../../server/src/policy/input.js";
import { fitCalibrationMapping } from "../../../server/src/ai-evidence/calibration.js";
import { getDemoSalesforceSnapshot } from "../../../server/src/salesforce/fixture-source.js";
import type { CustomerRosterEntry } from "../../../server/src/roster/types.js";
import type { ExtractionResult } from "../../../server/src/ai-evidence/types.js";
import type { CurrentProfileReadModel } from "../../../server/src/underwriting/repository.js";
import type { CustomerSnapshotReadModel } from "../../../server/src/snapshots/freshness.js";
import type { NoGoMappingReadiness } from "../../../server/src/policy/gates.js";
import { buildEvidencePacket } from "../../../server/src/ai-evidence/packet.js";
import { FixtureEvidenceProvider } from "../../../server/src/ai-evidence/fixture-provider.js";
import { extractEvidence } from "../../../server/src/ai-evidence/extract.js";
import { applyReviewToNextExtractionWithProvenance } from "../../../server/src/ai-evidence/review.js";
import { evaluateTier } from "../../../shared/policy/tiers.js";
import { computeDailyPrecheck } from "../../../server/src/policy/precheck.js";

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
    const joined = joinPolicyInputs({ roster, profile, snapshot, evidence, evidenceDecisionAllowed: true, qualifications: {}, now: new Date("2026-10-22T12:00:00Z") });
    expect(joined.customerId).toBe(roster.customerId);
    expect(joined.snapshotId).toBe("s1");
    expect(joined.profileId).toBe("p1");
    expect(joined.signals).toBeNull();
    expect(joined.blockers.map(({ code }) => code)).toEqual(expect.arrayContaining(["SNAPSHOT_INCOMPLETE", "INDUSTRY_MAPPING_UNVERIFIED", "FRAUD_MAPPING_UNVERIFIED"]));
    expect(JSON.stringify(joined)).not.toContain("Spoke with");
    expect(JSON.stringify(joined)).not.toContain("note");
  });

  it("blocks missing or stale source versions and customer-mismatched evidence", () => {
    const result = joinPolicyInputs({ roster, profile: { ...profile, freshness: "stale" }, snapshot, evidence: { ...evidence, customerId: "other" }, evidenceDecisionAllowed: true, qualifications: {} });
    expect(result.blockers.map(({ code }) => code)).toEqual(expect.arrayContaining(["PROFILE_STALE", "EVIDENCE_CUSTOMER_MISMATCH"]));
  });

  it("does not leak evidence text or rationale through the policy signal boundary", () => {
    const result = joinPolicyInputs({ roster, profile, snapshot, evidence, evidenceDecisionAllowed: true, qualifications: {} });
    expect(Object.keys(result).sort()).toEqual(["approvedCap", "blockers", "customerId", "humanApprovalRequired", "priorDefault", "profileId", "profileSourceMode", "signals", "snapshotId"].sort());
  });

  it("preserves valid no_signal and emits only typed ratings for explicitly mapped evidence", () => {
    const verifiedSnapshot = structuredClone(snapshot);
    verifiedSnapshot.snapshot!.data!.mappingVerified = true;
    const noSignal = joinPolicyInputs({ roster, profile, snapshot: verifiedSnapshot, evidence, evidenceDecisionAllowed: true, qualifications: {}, noGoMappings: verifiedMappings });
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
    const calibration = fitCalibrationMapping("client_payment_dispute", "cal-v1", [
      { signalType: "client_payment_dispute", rawScore: 0.99, correct: true, labelId: "policy-calibration-1", labeledAt: "2026-10-22T00:00:00Z" },
    ]);
    const mapped = joinPolicyInputs({ roster, profile, snapshot: verifiedSnapshot, evidence: success,
      evidenceDecisionAllowed: true,
      qualifications: { client_payment_dispute: qualified }, noGoMappings: verifiedMappings,
      calibrations: { client_payment_dispute: calibration },
      signalMappings: { client_payment_dispute: { key: "trackRecord", ratingsByValue: { '"confirmed"': "red" } } } });
    expect(mapped.blockers).toEqual([]);
    expect(mapped.signals?.trackRecord).toBe("red");
    expect(JSON.stringify(mapped)).not.toContain("private raw text");
    expect(JSON.stringify(mapped)).not.toContain("private rationale");
    expect(JSON.stringify(mapped)).not.toContain("Private Client");
  });

  it("calculates a watched customer line while retaining the required human review", () => {
    const watched = structuredClone(snapshot);
    watched.snapshot!.data!.mappingVerified = true;
    watched.snapshot!.data!.account.creditReviewStatus.value = "watch";
    const result = joinPolicyInputs({ roster, profile, snapshot: watched, evidence,
      evidenceDecisionAllowed: true,
      qualifications: {}, noGoMappings: verifiedMappings });
    expect(result.humanApprovalRequired).toBe(true);
    expect(result.blockers).toContainEqual({ code: "CREDIT_REVIEW_WATCH" });
    expect(result.signals).toEqual({ inflowReliability: "green", inflowConcentration: "green", cashBuffer: "green", trackRecord: "green" });
  });

  it("keeps Bloom blocked before exact reviewer resolution, then produces Tier C from source-bound 65% dispute evidence", async () => {
    const bloomCustomerId = "acct_bloom001";
    const bloomRoster = { ...roster, customerId: bloomCustomerId, salesforceAccountId: "demo-sf-bloom001" };
    const bloomProfile = structuredClone(profile);
    bloomProfile.profile!.customerId = bloomCustomerId;
    bloomProfile.profile!.data = {
      ...bloomProfile.profile!.data, averageMonthlySettledInflowsCents: 4_000_000,
      expectedDateReliability: 0.94, topPayerConcentration: 0.38, lowBalanceDays: 8,
      repaymentHistory: "never_borrowed",
    };
    const bloomSnapshotData = { ...getDemoSalesforceSnapshot(bloomCustomerId)!, mappingVerified: true };
    const bloomSnapshot = structuredClone(snapshot);
    bloomSnapshot.customerId = bloomCustomerId;
    bloomSnapshot.snapshot!.snapshotId = "bloom-snapshot-current";
    bloomSnapshot.snapshot!.data = bloomSnapshotData;
    const packetResult = buildEvidencePacket(bloomSnapshot, { now: new Date("2026-10-22T12:00:00Z"), maxPacketBytes: 32_000, maxNarrativeChars: 4_000 });
    expect(packetResult.status).toBe("ready");
    if (packetResult.status !== "ready") throw new Error("Bloom fixture packet must be ready");

    const provider = new FixtureEvidenceProvider();
    const currentPacket = packetResult.packet;
    const priorPacket = { ...currentPacket, snapshotId: "bloom-snapshot-reviewed" };
    const current = await extractEvidence(currentPacket, provider);
    const prior = await extractEvidence(priorPacket, provider);
    expect(current).toMatchObject({ status: "review_required", signals: [
      { type: "client_payment_dispute", value: "true", evidence: [{ sourceId: "demo-call-bloom001" }] },
      { type: "client_revenue_share", value: "65%", evidence: [{ sourceId: "demo-call-bloom001" }] },
      { type: "expected_inflow_delay", value: "≥ 5 days", evidence: [{ sourceId: "demo-call-bloom001" }] },
    ] });

    const baselineEvidence = { ...current, status: "no_signal" as const, signals: [] };
    const baseline = joinPolicyInputs({ roster: bloomRoster, profile: bloomProfile, snapshot: bloomSnapshot,
      evidence: baselineEvidence, evidenceDecisionAllowed: true, qualifications: {}, noGoMappings: verifiedMappings,
      now: new Date("2026-10-22T12:00:00Z") });
    expect(baseline.signals).toEqual({ inflowReliability: "green", inflowConcentration: "green",
      cashBuffer: "yellow", trackRecord: "green" });
    const productionBloomProfile = structuredClone(bloomProfile);
    productionBloomProfile.profile!.sourceMode = "production";
    const productionBaseline = joinPolicyInputs({ roster: bloomRoster, profile: productionBloomProfile,
      snapshot: bloomSnapshot, evidence: baselineEvidence, evidenceDecisionAllowed: true, qualifications: {},
      noGoMappings: verifiedMappings, now: new Date("2026-10-22T12:00:00Z") });
    expect(productionBaseline.signals).toEqual({ inflowReliability: "green", inflowConcentration: "green",
      cashBuffer: "yellow", trackRecord: "yellow" });
    const baselinePrecheck = computeDailyPrecheck({ policyInput: baseline, profile: bloomProfile.profile!.data,
      evidenceRunId: "bloom-baseline-evidence", rosterUpdatedAt: bloomRoster.updatedAt,
      facility: { facilitySize: 1_000_000, reserveFloorPct: 0.2, maxPerCustomerPct: 0.2,
        feeRatePer30Days: 0, minHistoryDays: 90 }, mappings: verifiedMappings, fullyRepaidOnTimeBridges: 0,
      inputVersions: {}, evaluatedAt: new Date("2026-10-22T12:00:00Z") });
    expect(baselinePrecheck).toMatchObject({ tier: "A", outcome: "ready", creditLineCents: 1_000_000 });

    const unresolved = joinPolicyInputs({ roster: bloomRoster, profile: bloomProfile, snapshot: bloomSnapshot,
      evidence: current, evidenceDecisionAllowed: false, qualifications: {}, noGoMappings: verifiedMappings,
      now: new Date("2026-10-22T12:00:00Z") });
    expect(unresolved.signals).toBeNull();
    expect(unresolved.blockers).toContainEqual({ code: "EVIDENCE_BLOCKED" });

    const reviewed = applyReviewToNextExtractionWithProvenance(current, prior, {
      extractionId: "bloom-reviewed-extraction", reviewerId: "configured-reviewer", action: "confirmed",
    }, currentPacket);
    expect(reviewed.exactReviewerResolution).toBe(true);
    const resolved = joinPolicyInputs({ roster: bloomRoster, profile: bloomProfile, snapshot: bloomSnapshot,
      evidence: reviewed.result, evidenceDecisionAllowed: true, qualifications: {}, noGoMappings: verifiedMappings,
      now: new Date("2026-10-22T12:00:00Z") });
    expect(resolved.blockers).toEqual([]);
    expect(resolved.signals).toEqual({ inflowReliability: "yellow", inflowConcentration: "yellow",
      cashBuffer: "yellow", trackRecord: "green" });
    expect(resolved.signals && evaluateTier(resolved.signals).tier).toBe("C");
    const reviewedPrecheck = computeDailyPrecheck({ policyInput: resolved, profile: bloomProfile.profile!.data,
      evidenceRunId: "bloom-reviewed-evidence", rosterUpdatedAt: bloomRoster.updatedAt,
      facility: { facilitySize: 1_000_000, reserveFloorPct: 0.2, maxPerCustomerPct: 0.2,
        feeRatePer30Days: 0, minHistoryDays: 90 }, mappings: verifiedMappings, fullyRepaidOnTimeBridges: 0,
      inputVersions: {}, evaluatedAt: new Date("2026-10-22T12:00:00Z") });
    expect(reviewedPrecheck).toMatchObject({ tier: "C", outcome: "ready", creditLineCents: 400_000 });
  });
});
