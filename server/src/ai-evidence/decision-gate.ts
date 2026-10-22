import type { ExtractionResult, ProposedSignalType } from "./types.js";
import type { TypeQualification } from "./qualification.js";

export type CustomerDecisionGate =
  | { allowed: true; reason: null }
  | { allowed: false; reason: string };

/** A successful no_signal result is safe; every unresolved state blocks only its customer. */
export function evaluateCustomerDecisionGate(
  result: ExtractionResult,
  qualificationByType: Readonly<Partial<Record<ProposedSignalType, TypeQualification>>>,
): CustomerDecisionGate {
  if (result.status === "review_required") return { allowed: false, reason: "Evidence requires reviewer resolution" };
  if (result.status === "unmapped") return { allowed: false, reason: "Evidence contains an unmapped signal" };
  if (result.status === "failed") return { allowed: false, reason: result.failureReason ?? "Evidence extraction failed" };
  if (result.status === "no_signal") return { allowed: true, reason: null };
  for (const signal of result.signals) {
    const qualification = qualificationByType[signal.type];
    if (!qualification?.qualified || qualification.mode !== "auto_apply_with_audit") {
      return { allowed: false, reason: `Signal type ${signal.type} is not qualified` };
    }
    if (signal.calibratedScore === null || !result.model.calibrationVersion ||
        qualification.calibrationVersion !== result.model.calibrationVersion) {
      return { allowed: false, reason: `Signal type ${signal.type} lacks a matching qualified calibration score` };
    }
  }
  return { allowed: true, reason: null };
}
