import type { ProposedSignalType } from "./types.js";

export const QUALIFICATION_POLICY_VERSION = "one-sided-exact-binomial-95-v1" as const;
export const QUALIFICATION_CONFIDENCE = 0.95;
export const REQUIRED_PRECISION_LOWER_BOUND = 0.95;
const ALPHA = 1 - QUALIFICATION_CONFIDENCE;
const WINDOW_MS = 90 * 24 * 60 * 60 * 1000;

/** Qualification labels are held out from calibration training by construction. */
export interface QualificationExample {
  signalType: ProposedSignalType;
  correct: boolean;
  labelId: string;
  labeledAt: string;
  calibrationVersion: string;
}

export interface TypeQualification {
  signalType: ProposedSignalType;
  calibrationVersion: string;
  policyVersion: typeof QUALIFICATION_POLICY_VERSION;
  evaluatedAt: string;
  sampleCount: number;
  errors: number;
  precisionLowerBound: number;
  qualified: boolean;
  mode: "review_only" | "auto_apply_with_audit";
}

export function exactOneSidedPrecisionLowerBound(successes: number, trials: number): number {
  if (!Number.isInteger(successes) || !Number.isInteger(trials) || trials < 0 || successes < 0 || successes > trials) {
    throw new Error("successes and trials must be integers with 0 <= successes <= trials");
  }
  if (trials === 0 || successes === 0) return 0;
  if (successes === trials) return ALPHA ** (1 / trials);
  const observedErrors = trials - successes;
  // Invert the exact binomial tail for the error probability using a monotone bisection.
  let low = 0;
  let high = 1;
  for (let iteration = 0; iteration < 80; iteration += 1) {
    const middle = (low + high) / 2;
    if (binomialCdf(observedErrors, trials, middle) > ALPHA) low = middle;
    else high = middle;
  }
  return 1 - (low + high) / 2;
}

export function evaluateQualification(
  signalType: ProposedSignalType,
  calibrationVersion: string,
  examples: readonly QualificationExample[],
  evaluatedAt: string,
): TypeQualification {
  if (!calibrationVersion.trim()) throw new Error("calibration version is required");
  const now = Date.parse(evaluatedAt);
  if (!Number.isFinite(now)) throw new Error("evaluatedAt must be a valid date");
  const cutoff = now - WINDOW_MS;
  const selected = examples.filter((example) => {
    const labeled = Date.parse(example.labeledAt);
    return example.signalType === signalType && example.calibrationVersion === calibrationVersion
      && Number.isFinite(labeled) && labeled >= cutoff && labeled <= now;
  });
  assertUniqueLabels(selected);
  const errors = selected.filter((example) => !example.correct).length;
  const precisionLowerBound = exactOneSidedPrecisionLowerBound(selected.length - errors, selected.length);
  const qualified = precisionLowerBound >= REQUIRED_PRECISION_LOWER_BOUND;
  return {
    signalType,
    calibrationVersion,
    policyVersion: QUALIFICATION_POLICY_VERSION,
    evaluatedAt,
    sampleCount: selected.length,
    errors,
    precisionLowerBound,
    qualified,
    mode: qualified ? "auto_apply_with_audit" : "review_only",
  };
}

function binomialCdf(maxErrors: number, trials: number, errorProbability: number): number {
  if (maxErrors >= trials) return 1;
  if (errorProbability <= 0) return 1;
  if (errorProbability >= 1) return 0;
  // Sum in log space to remain stable for realistic audit sample sizes.
  const logP = Math.log(errorProbability);
  const logQ = Math.log1p(-errorProbability);
  let logTerm = trials * logQ;
  let maxLogTerm = logTerm;
  let scaledSum = 1;
  for (let errors = 1; errors <= maxErrors; errors += 1) {
    logTerm += Math.log(trials - errors + 1) - Math.log(errors) + logP - logQ;
    if (logTerm > maxLogTerm) {
      scaledSum = scaledSum * Math.exp(maxLogTerm - logTerm) + 1;
      maxLogTerm = logTerm;
    } else {
      scaledSum += Math.exp(logTerm - maxLogTerm);
    }
  }
  return Math.exp(maxLogTerm) * scaledSum;
}

function assertUniqueLabels(examples: readonly QualificationExample[]): void {
  const ids = new Set<string>();
  for (const example of examples) {
    if (!example.labelId.trim() || ids.has(example.labelId)) throw new Error("qualification label IDs must be non-empty and unique");
    ids.add(example.labelId);
  }
}
