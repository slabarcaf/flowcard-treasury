import type { ExtractionResult, ProposedSignalType } from "./types.js";
import type { TypeQualification } from "./qualification.js";
import { evaluateCustomerDecisionGate } from "./decision-gate.js";
import type { TypeCalibrationMapping } from "./calibration.js";

/** Structural mirror of the existing Signals contract, kept local to the server tsconfig. */
export interface Signals {
  inflowReliability: SignalRating;
  inflowConcentration: SignalRating;
  cashBuffer: SignalRating;
  trackRecord: SignalRating;
}
export type SignalRating = "green" | "yellow" | "red";
export type PolicySignalKey = keyof Signals;
export type ApprovedSignalMeaning =
  | { key: PolicySignalKey; ratingsByValue: Readonly<Record<string, SignalRating>> }
  | { key: null; allowedValues: readonly string[] };
export type ApprovedSignalMappings = Readonly<Partial<Record<ProposedSignalType, ApprovedSignalMeaning>>>;
export interface PolicyInputResult {
  gate: ReturnType<typeof evaluateCustomerDecisionGate>;
  signals: Signals | null;
}

/** Exact reviewed Bloom values have deterministic effects; delay is displayed without changing ratings. */
export const SOURCE_APPROVED_SIGNAL_MAPPINGS: ApprovedSignalMappings = Object.freeze({
  client_payment_dispute: {
    key: "inflowReliability",
    ratingsByValue: Object.freeze({ '"true"': "yellow" }),
  },
  client_revenue_share: {
    key: "inflowConcentration",
    ratingsByValue: Object.freeze({ '"65%"': "yellow" }),
  },
  expected_inflow_delay: {
    key: null,
    allowedValues: Object.freeze(['"≥ 5 days"']),
  },
});

export function buildPolicyInput(
  existing: Signals,
  result: ExtractionResult,
  qualificationByType: Readonly<Partial<Record<ProposedSignalType, TypeQualification>>>,
  approvedMappings: ApprovedSignalMappings = SOURCE_APPROVED_SIGNAL_MAPPINGS,
  calibrationByType: Readonly<Partial<Record<ProposedSignalType, TypeCalibrationMapping>>> = {},
  exactReviewerResolution = false,
): PolicyInputResult {
  const gate = evaluateCustomerDecisionGate(result, qualificationByType, calibrationByType, exactReviewerResolution);
  if (!gate.allowed) return { gate, signals: null };
  if (result.status === "no_signal") return { gate, signals: { ...existing } };
  const next: Signals = { ...existing };
  for (const signal of result.signals) {
    const mapping = approvedMappings[signal.type];
    if (!mapping) return { gate: { allowed: false, reason: `Signal type ${signal.type} has no source-approved policy mapping` }, signals: null };
    const encodedValue = JSON.stringify(signal.value);
    if (mapping.key === null) {
      if (!mapping.allowedValues.includes(encodedValue ?? "")) {
        return { gate: { allowed: false, reason: `Signal type ${signal.type} value is not source-approved for policy` }, signals: null };
      }
      continue;
    }
    const rating = encodedValue === undefined ? undefined : mapping.ratingsByValue[encodedValue];
    if (!rating) return { gate: { allowed: false, reason: `Signal type ${signal.type} value is not source-approved for policy` }, signals: null };
    next[mapping.key] = rating;
  }
  return { gate, signals: next };
}
