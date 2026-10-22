import { describe, expect, it } from "vitest";
import type { FacilityConfig, Signals } from "../../../shared/policy/types.js";
import type { JoinedPolicyInput } from "../../../server/src/policy/input.js";
import { computeDailyPrecheck } from "../../../server/src/policy/precheck.js";
import type { UnderwritingProfileData } from "../../../server/src/underwriting/types.js";
import { UNVERIFIED_NO_GO_MAPPINGS, type NoGoMappingReadiness } from "../../../server/src/policy/gates.js";

const mappings: NoGoMappingReadiness = {
  industry: { verified: true, version: "industry-2" },
  fraud: { verified: true, version: "fraud-3" },
  priorDefault: { verified: true, version: "default-1" },
  concentration: { verified: true, version: "concentration-4" },
  industryExcluded: false,
  fraudFlag: false,
};
const profile: UnderwritingProfileData = {
  averageMonthlySettledInflowsCents: 1_000_000, expectedDateReliability: 0.95,
  topPayerConcentration: 0.4, lowBalanceDays: 3, historyDays: 90,
  eligibleSpend: "supported", connectedAccountStatus: "active", openRfi: false,
  repaymentHistory: "never_borrowed",
};
const signals: Signals = {
  inflowReliability: "green", inflowConcentration: "green", cashBuffer: "green", trackRecord: "yellow",
};
const base = (overrides: Partial<JoinedPolicyInput> = {}) => computeDailyPrecheck({
  policyInput: {
    customerId: "c1", profileId: "p1", snapshotId: "s1", signals, blockers: [],
    humanApprovalRequired: false, approvedCap: 7_500, priorDefault: "no", ...overrides,
  },
  profile, evidenceRunId: "e1", rosterUpdatedAt: new Date("2026-10-22T00:00:00Z"),
  facility: { facilitySize: 1_000_000, reserveFloorPct: 0.2, maxPerCustomerPct: 0.2, feeRatePer30Days: 0, minHistoryDays: 90 },
  mappings, fullyRepaidOnTimeBridges: 0, inputVersions: { profileVersion: 1 }, evaluatedAt: new Date("2026-10-22T01:00:00Z"),
});

describe("daily policy precheck", () => {
  it("applies shared tier, starter line, and the Salesforce cap", () => {
    const result = base({ approvedCap: 750 });
    expect(result).toMatchObject({ tier: "A", outcome: "ready", creditLineCents: 75_000, ratings: signals });
    expect(result.mappingVersions).toEqual({ industry: "industry-2", fraud: "fraud-3", priorDefault: "default-1", concentration: "concentration-4" });
  });

  it("promotes only after three on-time repayments and uses the promoted tier multiplier", () => {
    const result = computeDailyPrecheck({
      policyInput: { ...baseInput(), signals: { ...signals, trackRecord: "green" } }, profile,
      evidenceRunId: "e1", rosterUpdatedAt: new Date(), facility: facility(), mappings,
      fullyRepaidOnTimeBridges: 3, inputVersions: {},
    });
    expect(result.creditLineCents).toBe(300_000);
    const starter = computeDailyPrecheck({
      policyInput: { ...baseInput(), signals: { ...signals, trackRecord: "green" } }, profile,
      evidenceRunId: "e1", rosterUpdatedAt: new Date(), facility: facility(), mappings,
      fullyRepaidOnTimeBridges: 2, inputVersions: {},
    });
    expect(starter.creditLineCents).toBe(100_000);
  });

  it("uses the canonical Bloom multiplier only for the synthetic demo and retains ledger history in production", () => {
    const bloomSignals: Signals = { inflowReliability: "yellow", inflowConcentration: "yellow",
      cashBuffer: "yellow", trackRecord: "green" };
    const bloomProfile: UnderwritingProfileData = { ...profile,
      averageMonthlySettledInflowsCents: 4_000_000, lowBalanceDays: 8, repaymentHistory: "never_borrowed" };
    const facilityConfig = facility();
    const calculate = (profileSourceMode: "demo" | "production", fullyRepaidOnTimeBridges: number) => computeDailyPrecheck({
      policyInput: { ...baseInput(), customerId: "acct_bloom001", profileSourceMode, signals: bloomSignals, approvedCap: null },
      profile: bloomProfile, evidenceRunId: "bloom-evidence", rosterUpdatedAt: new Date(),
      facility: facilityConfig, mappings, fullyRepaidOnTimeBridges, inputVersions: {},
    });

    expect(calculate("demo", 0)).toMatchObject({ tier: "C", creditLineCents: 400_000 });
    expect(calculate("production", 0)).toMatchObject({ tier: "C", creditLineCents: 200_000 });
    expect(calculate("production", 3)).toMatchObject({ tier: "C", creditLineCents: 400_000 });
  });

  it("stores no line for unmapped blockers, watch review, and deterministic no-go outcomes", () => {
    const blocked = base({ blockers: [{ code: "PROFILE_STALE" }] });
    expect(blocked).toMatchObject({ outcome: "blocked", creditLineCents: 0, reasonCodes: ["PROFILE_STALE"] });
    const watch = base({ humanApprovalRequired: true, blockers: [{ code: "CREDIT_REVIEW_WATCH" }] });
    expect(watch).toMatchObject({ outcome: "ready", humanApprovalRequired: true, creditLineCents: 100_000 });
    const noGo = computeDailyPrecheck({
      policyInput: { ...baseInput(), priorDefault: "yes" }, profile,
      evidenceRunId: "e1", rosterUpdatedAt: new Date(), facility: facility(), mappings,
      fullyRepaidOnTimeBridges: 0, inputVersions: {},
    });
    expect(noGo).toMatchObject({ outcome: "ineligible", tier: "A", creditLineCents: 0, reasonCodes: ["NO_GO_APPLIED"] });
  });

  it("fails closed for unknown mapping readiness without carrying source prose", () => {
    const result = computeDailyPrecheck({
      policyInput: { ...baseInput(), signals: null, blockers: [{ code: "INDUSTRY_MAPPING_UNVERIFIED" }] },
      profile, evidenceRunId: "e1", rosterUpdatedAt: new Date(), facility: facility(),
      mappings: UNVERIFIED_NO_GO_MAPPINGS, fullyRepaidOnTimeBridges: 0, inputVersions: {},
    });
    expect(result.outcome).toBe("blocked");
    expect(result.creditLineCents).toBe(0);
    expect(JSON.stringify(result)).not.toContain("raw");
  });
});

function baseInput(): JoinedPolicyInput {
  return { customerId: "c1", profileId: "p1", snapshotId: "s1", signals, blockers: [], humanApprovalRequired: false, approvedCap: 7_500, priorDefault: "no" };
}
function facility(): FacilityConfig {
  return { facilitySize: 1_000_000, reserveFloorPct: 0.2, maxPerCustomerPct: 0.2, feeRatePer30Days: 0, minHistoryDays: 90 };
}
