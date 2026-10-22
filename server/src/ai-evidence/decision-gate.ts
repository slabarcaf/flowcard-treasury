import type { ExtractionResult, ProposedSignalType } from "./types.js";
import type { TypeQualification } from "./qualification.js";
import { calibrateScore, type TypeCalibrationMapping } from "./calibration.js";

export type CustomerDecisionGate =
  | { allowed: true; reason: null }
  | { allowed: false; reason: string };

/** A successful no_signal result is safe; every unresolved state blocks only its customer. */
export function evaluateCustomerDecisionGate(
  result: ExtractionResult,
  qualificationByType: Readonly<Partial<Record<ProposedSignalType, TypeQualification>>>,
  calibrationByType: Readonly<Partial<Record<ProposedSignalType, TypeCalibrationMapping>>> = {},
  exactReviewerResolution = false,
): CustomerDecisionGate {
  if (result.status === "review_required") return { allowed: false, reason: "Evidence requires reviewer resolution" };
  if (result.status === "unmapped") return { allowed: false, reason: "Evidence contains an unmapped signal" };
  if (result.status === "failed") return { allowed: false, reason: result.failureReason ?? "Evidence extraction failed" };
  if (result.unmapped.length > 0) return { allowed: false, reason: "Evidence contains an unmapped signal" };
  if (result.status === "no_signal") return { allowed: true, reason: null };
  for (const signal of result.signals) {
    // A matching, provenance-validated reviewer disposition authorizes this
    // exact customer result for manual policy use. It never changes the
    // qualification state for later, unreviewed outputs of this signal type.
    if (exactReviewerResolution) continue;
    const qualification = qualificationByType[signal.type];
    if (!qualification?.qualified || qualification.mode !== "auto_apply_with_audit") {
      return { allowed: false, reason: `Signal type ${signal.type} is not qualified` };
    }
    const calibration = calibrationByType[signal.type];
    if (!calibration || calibration.signalType !== signal.type ||
        calibration.version !== qualification.calibrationVersion) {
      return { allowed: false, reason: `Signal type ${signal.type} lacks a matching qualified calibration score` };
    }
    // Validate that a server-owned mapping can produce this score. Provider
    // calibratedScore and model.calibrationVersion are untrusted metadata.
    try {
      calibrateScore(calibration, signal.rawScore);
    } catch {
      return { allowed: false, reason: `Signal type ${signal.type} has an invalid calibration mapping` };
    }
  }
  return { allowed: true, reason: null };
}
