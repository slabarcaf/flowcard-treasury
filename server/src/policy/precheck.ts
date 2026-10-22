import { computeCreditLine } from "../../../shared/policy/creditLine.js";
import { hasNoGo, evaluateTier } from "../../../shared/policy/tiers.js";
import { POLICY_VERSION, type FacilityConfig, type PolicyReasonCode, type Signals, type Tier } from "../../../shared/policy/types.js";
import type { UnderwritingProfileData } from "../underwriting/types.js";
import type { JoinedPolicyInput } from "./input.js";
import type { NoGoMappingReadiness } from "./gates.js";

export type PrecheckOutcome = "ready" | "ineligible" | "blocked";
export interface DailyPrecheck {
  customerId: string;
  policyVersion: string;
  profileId: string | null;
  snapshotId: string | null;
  evidenceRunId: string | null;
  ratings: Signals | null;
  tier: Tier;
  creditLineCents: number;
  outcome: PrecheckOutcome;
  humanApprovalRequired: boolean;
  reasonCodes: string[];
  inputVersions: Record<string, string | number | null>;
  mappingVersions: Record<string, string>;
  rosterUpdatedAt: Date;
  evaluatedAt: Date;
}

export function computeDailyPrecheck(input: {
  policyInput: JoinedPolicyInput;
  profile: UnderwritingProfileData | null;
  evidenceRunId: string | null;
  rosterUpdatedAt: Date;
  facility: FacilityConfig;
  mappings: NoGoMappingReadiness;
  fullyRepaidOnTimeBridges: number;
  inputVersions: Record<string, string | number | null>;
  evaluatedAt?: Date;
}): DailyPrecheck {
  const { policyInput, profile, mappings } = input;
  const noGoCodes = new Set(["CREDIT_REVIEW_WATCH", "INDUSTRY_EXCLUDED", "FRAUD_FLAGGED", "PRIOR_DEFAULT", "CONCENTRATION_NO_GO"]);
  const blockerCodes = policyInput.blockers.map(({ code }) => code).filter((code) => !noGoCodes.has(code));
  const mappingVersions = Object.fromEntries(
    Object.entries(mappings).filter(([, value]) => typeof value === "object" && value !== null)
      .map(([key, value]) => [key, (value as { version: string | null }).version]).filter(([, version]) => version !== null),
  ) as Record<string, string>;
  const noGo = {
    excludedIndustry: mappings.industryExcluded === true,
    fraudFlag: mappings.fraudFlag === true,
    accountFrozen: profile?.connectedAccountStatus === "frozen" || profile?.connectedAccountStatus === "closed",
    pastWriteOff: policyInput.priorDefault === "yes",
    monoClient: profile?.topPayerConcentration !== null && profile?.topPayerConcentration !== undefined && profile.topPayerConcentration > 0.7,
  };
  const noGoApplied = hasNoGo(noGo);
  const ratings = blockerCodes.length === 0 ? policyInput.signals : null;
  const tierResult = ratings ? evaluateTier(ratings) : { tier: "none" as const, reasonCodes: [] as PolicyReasonCode[] };
  const blocked = blockerCodes.length > 0;
  const outcome: PrecheckOutcome = blocked ? "blocked" : noGoApplied || tierResult.tier === "none" ? "ineligible" : "ready";
  const lineDollars = ratings && profile && !blocked && !noGoApplied
    ? computeCreditLine(tierResult.tier, (profile.averageMonthlySettledInflowsCents ?? 0) / 100, {
      ...input.facility,
      approvedBridgeCap: policyInput.approvedCap ?? undefined,
    }, ratings, { fullyRepaidOnTimeBridges: input.fullyRepaidOnTimeBridges })
    : 0;
  const reasonCodes = [...new Set([
    ...blockerCodes,
    ...(noGoApplied ? ["NO_GO_APPLIED"] : []),
    ...(!noGoApplied && !blocked ? tierResult.reasonCodes : []),
    ...(tierResult.tier === "none" && !noGoApplied && !blocked ? ["NO_CREDIT_LINE"] : []),
  ])];
  return {
    customerId: policyInput.customerId,
    policyVersion: POLICY_VERSION,
    profileId: policyInput.profileId || null,
    snapshotId: policyInput.snapshotId || null,
    evidenceRunId: input.evidenceRunId,
    ratings,
    tier: tierResult.tier,
    creditLineCents: Math.round(lineDollars * 100),
    outcome,
    humanApprovalRequired: policyInput.humanApprovalRequired,
    reasonCodes,
    inputVersions: input.inputVersions,
    mappingVersions,
    rosterUpdatedAt: input.rosterUpdatedAt,
    evaluatedAt: input.evaluatedAt ?? new Date(),
  };
}
