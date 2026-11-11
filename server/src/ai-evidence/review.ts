import type { ExtractionResult, ExtractedSignal } from "./types.js";
import type { EvidenceReview } from "./repository.js";
import { stableStringify } from "./canonicalize.js";
import { validateExtractionResult } from "./validate.js";
import type { EvidencePacket } from "./packet.js";

/** Apply a matching prior review to this run while leaving the stored output untouched. */
export function applyReviewToNextExtraction(
  current: ExtractionResult,
  reviewedOutput: ExtractionResult,
  review: EvidenceReview,
  packet: EvidencePacket,
): ExtractionResult {
  return applyReviewToNextExtractionWithProvenance(current, reviewedOutput, review, packet).result;
}

export function applyReviewToNextExtractionWithProvenance(
  current: ExtractionResult,
  reviewedOutput: ExtractionResult,
  review: EvidenceReview,
  packet: EvidencePacket,
): { result: ExtractionResult; exactReviewerResolution: boolean } {
  if (current.customerId !== reviewedOutput.customerId || current.snapshotId === reviewedOutput.snapshotId) {
    throw new Error("review must apply to a later snapshot for the same customer");
  }
  if (packet.customerId !== current.customerId || packet.snapshotId !== current.snapshotId) {
    throw new Error("review packet must match the current customer snapshot");
  }
  if (!review.extractionId) throw new Error("review extractionId is required");
  if (current.status === "failed" || current.status === "unmapped" || current.unmapped.length > 0) {
    return { result: current, exactReviewerResolution: false };
  }

  if (review.action === "confirmed") {
    // Confirmation applies only to the exact output the reviewer saw. A new claim
    // on a later snapshot must go through review even if its evidence is related.
    if (!sameModelOutput(current, reviewedOutput)) return { result: { ...current, status: "review_required" }, exactReviewerResolution: false };
    return {
      result: { ...current, status: current.signals.length > 0 ? "success" : "no_signal" },
      exactReviewerResolution: current.signals.length > 0,
    };
  }
  if (review.action === "corrected") {
    // A correction is bound to the exact extraction output and current packet.
    // Reapply it only while both still match; changed output needs a new review.
    if (!sameModelOutput(current, reviewedOutput)) {
      return { result: { ...current, status: "review_required" }, exactReviewerResolution: false };
    }
    if (!isExtractedSignal(review.correctedSignal)) throw new Error("review correction is not a signal");
    const candidate = { ...current, status: "success" as const, signals: [review.correctedSignal], unmapped: [] };
    try {
      const validated = validateExtractionResult(candidate, packet);
      if (validated.status !== "success" || validated.unmapped.length > 0) {
        return { result: { ...current, status: "review_required" }, exactReviewerResolution: false };
      }
      return { result: validated, exactReviewerResolution: true };
    } catch {
      // Reviewer corrections are untrusted until bound to the current packet.
      return { result: { ...current, status: "review_required" }, exactReviewerResolution: false };
    }
  }
  // Clear reviews currently have no claim selector. Keep every claim and block
  // the customer until a reviewer can resolve the evidence unambiguously.
  return { result: { ...current, status: "review_required" }, exactReviewerResolution: false };
}

function sameModelOutput(current: ExtractionResult, reviewed: ExtractionResult): boolean {
  return stableStringify(withoutRunState(current)) === stableStringify(withoutRunState(reviewed));
}

function withoutRunState(result: ExtractionResult): Omit<ExtractionResult, "snapshotId" | "status"> {
  const copy = { ...result };
  delete (copy as Partial<ExtractionResult>).snapshotId;
  delete (copy as Partial<ExtractionResult>).status;
  return copy;
}

function isExtractedSignal(value: unknown): value is ExtractedSignal {
  if (!value || typeof value !== "object") return false;
  const signal = value as Partial<ExtractedSignal>;
  return typeof signal.type === "string" && typeof signal.rawScore === "number"
    && Array.isArray(signal.evidence) && Array.isArray(signal.quotes);
}
