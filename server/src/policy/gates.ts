import type { SalesforceCustomerSnapshot } from "../salesforce/types.js";
import type { UnderwritingProfileData } from "../underwriting/types.js";

export type GateCode =
  | "INACTIVE_ENROLLMENT" | "PROFILE_MISSING" | "PROFILE_STALE" | "PROFILE_INCOMPLETE"
  | "SNAPSHOT_MISSING" | "SNAPSHOT_STALE" | "SNAPSHOT_INCOMPLETE" | "SNAPSHOT_CUSTOMER_MISMATCH"
  | "ROSTER_CUSTOMER_MISMATCH"
  | "EVIDENCE_MISSING" | "EVIDENCE_CUSTOMER_MISMATCH" | "EVIDENCE_SNAPSHOT_MISMATCH" | "EVIDENCE_BLOCKED"
  | "ACCOUNT_NOT_ACTIVE" | "CONTRACT_NOT_ACTIVE" | "CONTRACT_EXPIRED" | "CONTRACT_EXPIRY_INVALID" | "KYB_NOT_VERIFIED"
  | "APPROVED_CAP_MISSING" | "CREDIT_REVIEW_BLOCKED" | "CREDIT_REVIEW_WATCH"
  | "AIRWALLEX_NOT_ACTIVE" | "OPEN_RFI" | "INDUSTRY_MAPPING_UNVERIFIED" | "FRAUD_MAPPING_UNVERIFIED"
  | "INDUSTRY_UNKNOWN" | "INDUSTRY_EXCLUDED" | "FRAUD_UNKNOWN" | "FRAUD_FLAGGED"
  | "PRIOR_DEFAULT_MAPPING_UNVERIFIED" | "PRIOR_DEFAULT_UNKNOWN" | "PRIOR_DEFAULT" | "CONCENTRATION_MAPPING_UNVERIFIED" | "CONCENTRATION_NO_GO";

export interface GateBlocker { code: GateCode; }
export interface VersionedMapping { verified: boolean; version: string | null; }
export interface NoGoMappingReadiness {
  industry: VersionedMapping;
  fraud: VersionedMapping;
  priorDefault: VersionedMapping;
  concentration: VersionedMapping;
  /** Canonical results from a verified source adapter; raw CRM text is never accepted. */
  industryExcluded: boolean | null;
  fraudFlag: boolean | null;
}
export const UNVERIFIED_NO_GO_MAPPINGS: NoGoMappingReadiness = Object.freeze({
  industry: { verified: false, version: null }, fraud: { verified: false, version: null },
  priorDefault: { verified: false, version: null }, concentration: { verified: false, version: null },
  industryExcluded: null, fraudFlag: null,
});

export interface GateInput {
  active: boolean;
  rosterCustomerId?: string;
  profile: { data: UnderwritingProfileData; customerId: string } | null;
  profileFresh: boolean;
  snapshot: { data: SalesforceCustomerSnapshot; customerId: string; snapshotId: string } | null;
  snapshotFresh: boolean;
  evidence: { customerId: string; snapshotId: string; status: "success" | "no_signal" | "review_required" | "unmapped" | "failed" } | null;
  mappings?: NoGoMappingReadiness;
  now?: Date;
}

export interface GateResult { blockers: GateBlocker[]; humanApprovalRequired: boolean; }

/** Apply hard gates using canonical fields only; source prose is never inspected. */
export function evaluatePolicyGates(input: GateInput): GateResult {
  const blockers: GateBlocker[] = [];
  const add = (code: GateCode) => blockers.push({ code });
  if (!input.active) add("INACTIVE_ENROLLMENT");
  if (!input.profile) add("PROFILE_MISSING");
  else {
    if (!input.profileFresh) add("PROFILE_STALE");
    if (input.profile.customerId !== input.snapshot?.customerId) add("PROFILE_INCOMPLETE");
    if (input.rosterCustomerId && input.profile.customerId !== input.rosterCustomerId) add("ROSTER_CUSTOMER_MISMATCH");
    if (input.profile.data.averageMonthlySettledInflowsCents === null || input.profile.data.expectedDateReliability === null || input.profile.data.historyDays !== 90 ||
        input.profile.data.eligibleSpend === "unknown" || input.profile.data.connectedAccountStatus === "unknown" ||
        input.profile.data.openRfi === null || input.profile.data.repaymentHistory === "unknown" ||
        input.profile.data.topPayerConcentration === null || input.profile.data.lowBalanceDays === null) add("PROFILE_INCOMPLETE");
  }
  if (!input.snapshot) add("SNAPSHOT_MISSING");
  else {
    if (!input.snapshotFresh) add("SNAPSHOT_STALE");
    if (input.snapshot.customerId !== input.profile?.customerId) add("SNAPSHOT_CUSTOMER_MISMATCH");
    if (input.rosterCustomerId && input.snapshot.customerId !== input.rosterCustomerId) add("ROSTER_CUSTOMER_MISMATCH");
    if (!input.snapshot.data.mappingVerified) add("SNAPSHOT_INCOMPLETE");
    const sf = input.snapshot.data;
    if (sf.account.customerStatus.value !== "active") add("ACCOUNT_NOT_ACTIVE");
    if (sf.contract.status.value !== "active") add("CONTRACT_NOT_ACTIVE");
    const endDate = sf.contract.endDate.value;
    if (endDate !== null) {
      const end = Date.parse(endDate);
      if (!Number.isFinite(end)) add("CONTRACT_EXPIRY_INVALID");
      else if (end <= (input.now ?? new Date()).getTime()) add("CONTRACT_EXPIRED");
    }
    if (sf.account.kybStatus.value !== "verified") add("KYB_NOT_VERIFIED");
    if (sf.account.approvedBridgeCap.value === null) add("APPROVED_CAP_MISSING");
    if (sf.account.creditReviewStatus.value === "watch") add("CREDIT_REVIEW_WATCH");
    else if (sf.account.creditReviewStatus.value !== "approved") add("CREDIT_REVIEW_BLOCKED");
  }
  if (!input.evidence) add("EVIDENCE_MISSING");
  else {
    if (input.evidence.customerId !== input.profile?.customerId) add("EVIDENCE_CUSTOMER_MISMATCH");
    if (input.evidence.snapshotId !== input.snapshot?.snapshotId) add("EVIDENCE_SNAPSHOT_MISMATCH");
    if (input.evidence.status === "review_required" || input.evidence.status === "unmapped" || input.evidence.status === "failed") add("EVIDENCE_BLOCKED");
  }
  if (input.profile) {
    if (input.profile.data.connectedAccountStatus !== "active") add("AIRWALLEX_NOT_ACTIVE");
    if (input.profile.data.openRfi !== false) add("OPEN_RFI");
  }
  const mappings = input.mappings ?? UNVERIFIED_NO_GO_MAPPINGS;
  if (!isVerified(mappings.industry)) add("INDUSTRY_MAPPING_UNVERIFIED");
  if (mappings.industryExcluded === null) add("INDUSTRY_UNKNOWN");
  else if (mappings.industryExcluded) add("INDUSTRY_EXCLUDED");
  if (!isVerified(mappings.fraud)) add("FRAUD_MAPPING_UNVERIFIED");
  if (mappings.fraudFlag === null) add("FRAUD_UNKNOWN");
  else if (mappings.fraudFlag) add("FRAUD_FLAGGED");
  if (!isVerified(mappings.priorDefault)) add("PRIOR_DEFAULT_MAPPING_UNVERIFIED");
  else if (input.snapshot?.data.account.priorDefaultStatus.value === "yes") add("PRIOR_DEFAULT");
  else if (input.snapshot?.data.account.priorDefaultStatus.value !== "no") add("PRIOR_DEFAULT_UNKNOWN");
  if (!isVerified(mappings.concentration)) add("CONCENTRATION_MAPPING_UNVERIFIED");
  else if (input.profile?.data.topPayerConcentration !== null && input.profile?.data.topPayerConcentration !== undefined && input.profile.data.topPayerConcentration > 0.7) add("CONCENTRATION_NO_GO");
  return { blockers: unique(blockers), humanApprovalRequired: input.snapshot?.data.account.creditReviewStatus.value === "watch" };
}

function isVerified(mapping: VersionedMapping): boolean { return mapping.verified && Boolean(mapping.version?.trim()); }
function unique(blockers: GateBlocker[]): GateBlocker[] { return [...new Map(blockers.map((item) => [item.code, item])).values()]; }
