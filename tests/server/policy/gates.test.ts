import { describe, expect, it } from "vitest";
import { evaluatePolicyGates, type GateInput, type NoGoMappingReadiness } from "../../../server/src/policy/gates.js";
import type { SalesforceCustomerSnapshot } from "../../../server/src/salesforce/types.js";
import type { UnderwritingProfileData } from "../../../server/src/underwriting/types.js";

const mappings: NoGoMappingReadiness = {
  industry: { verified: true, version: "industry-v1" }, fraud: { verified: true, version: "fraud-v1" },
  priorDefault: { verified: true, version: "default-v1" }, concentration: { verified: true, version: "concentration-v1" },
  industryExcluded: false, fraudFlag: false,
};
const profile: UnderwritingProfileData = {
  averageMonthlySettledInflowsCents: 100_000, expectedDateReliability: 0.95, topPayerConcentration: 0.4,
  lowBalanceDays: 3, historyDays: 90, eligibleSpend: "supported", connectedAccountStatus: "active",
  openRfi: false, repaymentHistory: "on_time",
};
const snapshot = {
  schemaVersion: 1, mappingVersion: "sf-v1", mappingVerified: true,
  account: {
    recordId: "a", sourceUpdatedAt: null,
    customerStatus: { value: "active", source: { recordId: "a", sourceUpdatedAt: null } },
    kybStatus: { value: "verified", source: { recordId: "a", sourceUpdatedAt: null } },
    creditReviewStatus: { value: "approved", source: { recordId: "a", sourceUpdatedAt: null } },
    approvedBridgeCap: { value: 5000, source: { recordId: "a", sourceUpdatedAt: null } },
    priorDefaultStatus: { value: "no", source: { recordId: "a", sourceUpdatedAt: null } },
    loyaltyTier: { value: "starter", source: { recordId: "a", sourceUpdatedAt: null } },
  },
  contract: {
    recordId: "c", sourceUpdatedAt: null,
    status: { value: "active", source: { recordId: "c", sourceUpdatedAt: null } },
    endDate: { value: null, source: { recordId: "c", sourceUpdatedAt: null } },
  },
  cases: [], callActivities: [],
} as SalesforceCustomerSnapshot;
const goodInput = (overrides: Partial<GateInput> = {}): GateInput => ({
  active: true, profile: { customerId: "c1", data: profile }, profileFresh: true,
  snapshot: { customerId: "c1", snapshotId: "s1", data: snapshot }, snapshotFresh: true,
  evidence: { customerId: "c1", snapshotId: "s1", status: "no_signal" }, mappings, now: new Date("2026-10-22T00:00:00Z"),
  ...overrides,
});

describe("customer policy gates", () => {
  it("allows complete inputs and treats mapped null contract expiry as no scheduled expiry", () => {
    expect(evaluatePolicyGates(goodInput())).toEqual({ blockers: [], humanApprovalRequired: false });
  });

  it("blocks inactive, stale, incomplete, mismatched, and unresolved inputs by stable code", () => {
    const result = evaluatePolicyGates(goodInput({
      active: false, profileFresh: false, snapshotFresh: false,
      evidence: { customerId: "other", snapshotId: "old", status: "failed" },
    }));
    expect(result.blockers.map(({ code }) => code)).toEqual(expect.arrayContaining([
      "INACTIVE_ENROLLMENT", "PROFILE_STALE", "SNAPSHOT_STALE", "EVIDENCE_CUSTOMER_MISMATCH",
      "EVIDENCE_SNAPSHOT_MISMATCH", "EVIDENCE_BLOCKED",
    ]));
  });

  it("requires each no-go source mapping and blocks positive no-go facts", () => {
    expect(evaluatePolicyGates(goodInput()).blockers).toEqual([]);
    expect(evaluatePolicyGates(goodInput({ mappings: undefined })).blockers.map(({ code }) => code)).toEqual(expect.arrayContaining([
      "INDUSTRY_MAPPING_UNVERIFIED", "INDUSTRY_UNKNOWN", "FRAUD_MAPPING_UNVERIFIED", "FRAUD_UNKNOWN",
      "PRIOR_DEFAULT_MAPPING_UNVERIFIED", "CONCENTRATION_MAPPING_UNVERIFIED",
    ]));
    expect(evaluatePolicyGates(goodInput({ mappings: { ...mappings, industryExcluded: true, fraudFlag: true } })).blockers.map(({ code }) => code)).toEqual(expect.arrayContaining(["INDUSTRY_EXCLUDED", "FRAUD_FLAGGED"]));
  });

  it("enforces account, contract, KYB, cap, review, Airwallex, RFI, default, and concentration rules", () => {
    const altered = structuredClone(snapshot);
    altered.account.customerStatus.value = "paused";
    altered.account.kybStatus.value = "pending";
    altered.account.approvedBridgeCap.value = null;
    altered.account.creditReviewStatus.value = "watch";
    altered.contract.status.value = "expired";
    altered.contract.endDate.value = "2026-10-21T00:00:00Z";
    altered.account.priorDefaultStatus.value = "yes";
    const result = evaluatePolicyGates(goodInput({
      snapshot: { customerId: "c1", snapshotId: "s1", data: altered },
      profile: { customerId: "c1", data: { ...profile, connectedAccountStatus: "frozen", openRfi: true, topPayerConcentration: 0.71 } },
    }));
    expect(result.humanApprovalRequired).toBe(true);
    expect(result.blockers.map(({ code }) => code)).toEqual(expect.arrayContaining([
      "ACCOUNT_NOT_ACTIVE", "CONTRACT_NOT_ACTIVE", "CONTRACT_EXPIRED", "KYB_NOT_VERIFIED", "APPROVED_CAP_MISSING",
      "CREDIT_REVIEW_WATCH", "AIRWALLEX_NOT_ACTIVE", "OPEN_RFI", "PRIOR_DEFAULT", "CONCENTRATION_NO_GO",
    ]));
  });

  it("blocks unknown review and evidence states without turning no_signal into failure", () => {
    expect(evaluatePolicyGates(goodInput({ evidence: { customerId: "c1", snapshotId: "s1", status: "no_signal" } })).blockers).toEqual([]);
    expect(evaluatePolicyGates(goodInput({ evidence: { customerId: "c1", snapshotId: "s1", status: "review_required" } })).blockers).toContainEqual({ code: "EVIDENCE_BLOCKED" });
    const unknown = structuredClone(snapshot);
    unknown.account.creditReviewStatus.value = null;
    expect(evaluatePolicyGates(goodInput({ snapshot: { customerId: "c1", snapshotId: "s1", data: unknown } })).blockers).toContainEqual({ code: "CREDIT_REVIEW_BLOCKED" });
  });
});
