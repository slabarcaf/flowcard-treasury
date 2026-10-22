import type { ProposedSignalType } from "./types.js";
import type { TypeQualification } from "./qualification.js";

/** Unspecified operational audit quotas are versioned configuration, not policy constants. */
export interface AuditQuotaConfig {
  version: string;
  minimumRecentLabelsByType: Readonly<Record<ProposedSignalType, number>>;
  randomAuditRateByType: Readonly<Record<ProposedSignalType, number>>;
}

export const DEFAULT_AUDIT_QUOTAS: AuditQuotaConfig = {
  version: "audit-quotas-v1",
  minimumRecentLabelsByType: {
    client_payment_dispute: 100,
    client_revenue_share: 100,
    expected_inflow_delay: 100,
    positive_note: 100,
  },
  randomAuditRateByType: {
    client_payment_dispute: 0.05,
    client_revenue_share: 0.05,
    expected_inflow_delay: 0.05,
    positive_note: 0.05,
  },
};

export interface AutoAppliedResult {
  id: string;
  signalType: ProposedSignalType;
}

export function selectAuditResults(
  candidates: readonly AutoAppliedResult[],
  qualificationByType: Readonly<Partial<Record<ProposedSignalType, TypeQualification>>>,
  quota: AuditQuotaConfig = DEFAULT_AUDIT_QUOTAS,
  random: () => number = Math.random,
): AutoAppliedResult[] {
  if (!quota.version.trim()) throw new Error("audit quota version is required");
  const selected: AutoAppliedResult[] = [];
  for (const candidate of candidates) {
    const minimum = quota.minimumRecentLabelsByType[candidate.signalType];
    const rate = quota.randomAuditRateByType[candidate.signalType];
    if (!Number.isInteger(minimum) || minimum < 0 || !Number.isFinite(rate) || rate < 0 || rate > 1) {
      throw new Error(`invalid audit quota for ${candidate.signalType}`);
    }
    const qualification = qualificationByType[candidate.signalType];
    if (!qualification?.qualified || qualification.sampleCount < minimum || qualification.mode !== "auto_apply_with_audit") {
      selected.push(candidate);
      continue;
    }
    const draw = random();
    if (!Number.isFinite(draw) || draw < 0 || draw >= 1) throw new Error("random source must return a value in [0, 1)");
    if (draw < rate) selected.push(candidate);
  }
  return selected;
}
