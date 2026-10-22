import type { ProposedSignalType } from "./types.js";

export const CALIBRATION_MAPPING_VERSION = "empirical-score-bins-v1" as const;
const BIN_COUNT = 10;

/** A warm-up label is used to fit a calibrator and is never qualification evidence. */
export interface CalibrationTrainingExample {
  signalType: ProposedSignalType;
  rawScore: number;
  correct: boolean;
  labelId: string;
  labeledAt: string;
}

export interface CalibrationBin {
  lowerInclusive: number;
  upperExclusive: number;
  sampleCount: number;
  accuracy: number | null;
}

export interface TypeCalibrationMapping {
  signalType: ProposedSignalType;
  version: string;
  mappingVersion: typeof CALIBRATION_MAPPING_VERSION;
  fittedAt: string;
  trainingLabelIds: string[];
  bins: CalibrationBin[];
}

export function fitCalibrationMapping(
  signalType: ProposedSignalType,
  version: string,
  examples: readonly CalibrationTrainingExample[],
  fittedAt = new Date().toISOString(),
): TypeCalibrationMapping {
  if (!version.trim()) throw new Error("calibration version is required");
  const selected = examples.filter((example) => example.signalType === signalType);
  assertUniqueLabels(selected);
  const bins = Array.from({ length: BIN_COUNT }, (_, index) => {
    const lowerInclusive = index / BIN_COUNT;
    const upperExclusive = (index + 1) / BIN_COUNT;
    const rows = selected.filter((example) => {
      assertScore(example.rawScore);
      return bucket(example.rawScore) === index;
    });
    const correctCount = rows.filter((row) => row.correct).length;
    return {
      lowerInclusive,
      upperExclusive,
      sampleCount: rows.length,
      accuracy: rows.length === 0 ? null : correctCount / rows.length,
    };
  });
  return {
    signalType,
    version,
    mappingVersion: CALIBRATION_MAPPING_VERSION,
    fittedAt,
    trainingLabelIds: selected.map((example) => example.labelId).sort(),
    bins,
  };
}

export function calibrateScore(mapping: TypeCalibrationMapping, rawScore: number): number {
  assertScore(rawScore);
  if (mapping.mappingVersion !== CALIBRATION_MAPPING_VERSION) throw new Error("unsupported calibration mapping version");
  const value = mapping.bins[bucket(rawScore)]?.accuracy;
  return value ?? rawScore;
}

function bucket(score: number): number {
  return Math.min(BIN_COUNT - 1, Math.floor(score * BIN_COUNT));
}

function assertScore(score: number): void {
  if (!Number.isFinite(score) || score < 0 || score > 1) throw new Error("score must be between 0 and 1");
}

function assertUniqueLabels(examples: readonly CalibrationTrainingExample[]): void {
  const ids = new Set<string>();
  for (const example of examples) {
    if (!example.labelId.trim() || ids.has(example.labelId)) throw new Error("calibration label IDs must be non-empty and unique");
    ids.add(example.labelId);
  }
}
