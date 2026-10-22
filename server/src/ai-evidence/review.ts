import type { ExtractionResult, ExtractedSignal } from "./types.js";
import type { EvidenceReview } from "./repository.js";

/** Apply a matching prior review to this run while leaving the stored output untouched. */
export function applyReviewToNextExtraction(
  current: ExtractionResult,
  reviewedOutput: ExtractionResult,
  review: EvidenceReview,
): ExtractionResult {
  if (current.customerId !== reviewedOutput.customerId || current.snapshotId === reviewedOutput.snapshotId) {
    throw new Error("review must apply to a later snapshot for the same customer");
  }
  if (!review.extractionId) throw new Error("review extractionId is required");
  if (current.status === "failed") return current;

  // A corrected-away or cleared model answer that repeats unchanged must be reviewed again.
  if (review.action !== "confirmed" && sameModelOutput(current, reviewedOutput)) {
    return { ...current, status: "review_required" };
  }
  if (review.action === "confirmed") {
    return { ...current, status: current.signals.length > 0 ? "success" : "no_signal" };
  }
  if (review.action === "corrected") {
    if (!isExtractedSignal(review.correctedSignal)) throw new Error("review correction is not a signal");
    return { ...current, status: "success", signals: [review.correctedSignal], unmapped: [] };
  }
  return { ...current, status: "no_signal", signals: [], unmapped: [] };
}

function sameModelOutput(current: ExtractionResult, reviewed: ExtractionResult): boolean {
  return stableJson(withoutRunState(current)) === stableJson(withoutRunState(reviewed));
}

function withoutRunState(result: ExtractionResult): Omit<ExtractionResult, "snapshotId" | "status"> {
  const copy = { ...result };
  delete (copy as Partial<ExtractionResult>).snapshotId;
  delete (copy as Partial<ExtractionResult>).status;
  return copy;
}

function stableJson(value: unknown): string {
  if (Array.isArray(value)) return `[${value.map(stableJson).join(",")}]`;
  if (value && typeof value === "object") {
    return `{${Object.entries(value).sort(([a], [b]) => a.localeCompare(b))
      .map(([key, entry]) => `${JSON.stringify(key)}:${stableJson(entry)}`).join(",")}}`;
  }
  return JSON.stringify(value);
}

function isExtractedSignal(value: unknown): value is ExtractedSignal {
  if (!value || typeof value !== "object") return false;
  const signal = value as Partial<ExtractedSignal>;
  return typeof signal.type === "string" && typeof signal.rawScore === "number"
    && Array.isArray(signal.evidence) && Array.isArray(signal.quotes);
}
