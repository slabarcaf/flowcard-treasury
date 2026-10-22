import { buildPolicyInput, type ApprovedSignalMappings, type Signals } from "../ai-evidence/policy-input.js";
import type { ExtractionResult, ProposedSignalType } from "../ai-evidence/types.js";
import type { TypeQualification } from "../ai-evidence/qualification.js";
import type { TypeCalibrationMapping } from "../ai-evidence/calibration.js";
import type { SalesforceCustomerSnapshot } from "../salesforce/types.js";
import type { UnderwritingProfileData } from "../underwriting/types.js";
import type { CurrentProfileReadModel } from "../underwriting/repository.js";
import type { CustomerSnapshotReadModel } from "../snapshots/freshness.js";
import type { CustomerRosterEntry } from "../roster/types.js";
import { evaluatePolicyGates, type NoGoMappingReadiness } from "./gates.js";

export interface JoinedPolicyInput {
  customerId: string;
  snapshotId: string;
  profileId: string;
  signals: Signals | null;
  blockers: ReturnType<typeof evaluatePolicyGates>["blockers"];
  humanApprovalRequired: boolean;
  approvedCap: number | null;
  priorDefault: "yes" | "no" | "unknown" | null;
}

/** Join the current allowlisted inputs for one roster customer. */
export function joinPolicyInputs(input: {
  roster: CustomerRosterEntry;
  profile: CurrentProfileReadModel;
  snapshot: CustomerSnapshotReadModel;
  evidence: ExtractionResult | null;
  evidenceDecisionAllowed?: boolean;
  qualifications: Readonly<Partial<Record<ProposedSignalType, TypeQualification>>>;
  calibrations?: Readonly<Partial<Record<ProposedSignalType, TypeCalibrationMapping>>>;
  signalMappings?: ApprovedSignalMappings;
  noGoMappings?: NoGoMappingReadiness;
  now?: Date;
}): JoinedPolicyInput {
  const profile = input.profile.profile;
  const snapshot = input.snapshot.snapshot;
  const payload = snapshot?.data as SalesforceCustomerSnapshot | null | undefined;
  const gate = evaluatePolicyGates({
    active: input.roster.isActive,
    rosterCustomerId: input.roster.customerId,
    profile: profile ? { customerId: profile.customerId, data: profile.data } : null,
    profileFresh: input.profile.freshness === "fresh",
    snapshot: payload && snapshot ? { customerId: input.snapshot.customerId, snapshotId: snapshot.snapshotId, data: payload } : null,
    snapshotFresh: input.snapshot.freshness === "fresh",
    evidence: input.evidence ? { ...input.evidence, decisionAllowed: input.evidenceDecisionAllowed === true } : null,
    mappings: input.noGoMappings,
    now: input.now,
  });
  const baseline = profile ? ratingsFromProfile(profile.data) : null;
  let signals: Signals | null = null;
  const calculationAllowed = gate.blockers.every(({ code }) => code === "CREDIT_REVIEW_WATCH");
  if (calculationAllowed && input.evidence) {
    const policyInput = baseline
      ? buildPolicyInput(baseline, input.evidence, input.qualifications, input.signalMappings, input.calibrations)
      : null;
    if (policyInput?.gate.allowed) signals = policyInput.signals;
    else gate.blockers.push({ code: "EVIDENCE_BLOCKED" });
  }
  return {
    customerId: input.roster.customerId,
    snapshotId: snapshot?.snapshotId ?? "",
    profileId: profile?.profileId ?? "",
    signals,
    blockers: gate.blockers,
    humanApprovalRequired: gate.humanApprovalRequired,
    approvedCap: payload?.account.approvedBridgeCap.value ?? null,
    priorDefault: payload?.account.priorDefaultStatus.value ?? null,
  };
}

function ratingsFromProfile(data: UnderwritingProfileData): Signals | null {
  const reliability = data.expectedDateReliability;
  const concentration = data.topPayerConcentration;
  const lowBalanceDays = data.lowBalanceDays;
  if (reliability === null || concentration === null || lowBalanceDays === null || data.repaymentHistory === "unknown") return null;
  return {
    inflowReliability: reliability >= 0.9 ? "green" : reliability >= 0.75 ? "yellow" : "red",
    inflowConcentration: concentration <= 0.4 ? "green" : concentration <= 0.7 ? "yellow" : "red",
    cashBuffer: lowBalanceDays <= 5 ? "green" : lowBalanceDays <= 20 ? "yellow" : "red",
    trackRecord: data.repaymentHistory === "on_time" ? "green" : data.repaymentHistory === "never_borrowed" ? "yellow" : "red",
  };
}
