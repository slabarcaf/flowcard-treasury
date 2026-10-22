import { describe, expect, it } from "vitest";
import { DEFAULT_AUDIT_QUOTAS, selectAuditResults } from "../../../server/src/ai-evidence/audit-sampler.js";
import {
  evaluateQualification,
  exactOneSidedPrecisionLowerBound,
  type QualificationExample,
} from "../../../server/src/ai-evidence/qualification.js";

const now = "2026-10-22T12:00:00.000Z";

function examples(count: number, errors = 0, overrides: Partial<QualificationExample> = {}): QualificationExample[] {
  return Array.from({ length: count }, (_, index) => ({
    signalType: "client_payment_dispute" as const,
    correct: index >= errors,
    labelId: `qual-${index}`,
    labeledAt: now,
    calibrationVersion: "cal-v1",
    ...overrides,
  }));
}

describe("qualification and audit", () => {
  it("uses the exact one-sided 95% binomial lower confidence bound", () => {
    expect(exactOneSidedPrecisionLowerBound(59, 59)).toBeGreaterThanOrEqual(0.95);
    expect(exactOneSidedPrecisionLowerBound(58, 58)).toBeLessThan(0.95);
    expect(exactOneSidedPrecisionLowerBound(91, 92)).toBeLessThan(0.95);
    expect(exactOneSidedPrecisionLowerBound(92, 93)).toBeGreaterThanOrEqual(0.95);
    expect(exactOneSidedPrecisionLowerBound(98, 100)).toBeLessThan(0.95);
    expect(exactOneSidedPrecisionLowerBound(0, 0)).toBe(0);
    expect(exactOneSidedPrecisionLowerBound(0, 10)).toBe(0);
  });

  it("keeps qualification held out by signal type and calibration version", () => {
    const rows = [
      ...examples(59),
      ...examples(100, 100, { signalType: "positive_note" }),
      ...examples(100, 100, { calibrationVersion: "old-config" }),
    ];
    const result = evaluateQualification("client_payment_dispute", "cal-v1", rows, now);
    expect(result.sampleCount).toBe(59);
    expect(result.qualified).toBe(true);
    expect(result.mode).toBe("auto_apply_with_audit");
  });

  it("expires labels outside the rolling 90-day window and demotes on errors or insufficient evidence", () => {
    const old = "2026-07-23T11:59:59.999Z";
    const boundary = "2026-07-24T12:00:00.000Z";
    const current = evaluateQualification("client_payment_dispute", "cal-v1", [
      ...examples(59, 0, { labeledAt: old }),
      ...examples(59).map((example) => ({ ...example, labeledAt: boundary, labelId: `boundary-${example.labelId}` })),
    ], now);
    const errors = evaluateQualification("client_payment_dispute", "cal-v1", examples(100, 2), now);
    expect(current.sampleCount).toBe(59);
    expect(current.qualified).toBe(true);
    expect(errors.qualified).toBe(false);
    expect(errors.mode).toBe("review_only");
  });

  it("audits all auto-applied results below quota, then samples at the configured rate", () => {
    const results = ["a", "b", "c"].map((id) => ({ id, signalType: "client_payment_dispute" as const }));
    const qualification = evaluateQualification("client_payment_dispute", "cal-v1", examples(59), now);
    const belowQuota = selectAuditResults(results, { client_payment_dispute: qualification }, DEFAULT_AUDIT_QUOTAS, () => 0.99);
    const customQuota = {
      ...DEFAULT_AUDIT_QUOTAS,
      minimumRecentLabelsByType: { ...DEFAULT_AUDIT_QUOTAS.minimumRecentLabelsByType, client_payment_dispute: 59 },
      randomAuditRateByType: { ...DEFAULT_AUDIT_QUOTAS.randomAuditRateByType, client_payment_dispute: 0.5 },
    };
    const sampled = selectAuditResults(results, { client_payment_dispute: qualification }, customQuota, () => 0.4);
    expect(belowQuota).toHaveLength(3);
    expect(sampled).toHaveLength(3);
    expect(selectAuditResults(results, { client_payment_dispute: qualification }, customQuota, () => 0.6)).toHaveLength(0);
  });
});
