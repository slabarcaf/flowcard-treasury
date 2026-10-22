import { describe, expect, it } from "vitest";
import { applyReviewToNextExtraction } from "../../../server/src/ai-evidence/review.js";
import type { ExtractionResult } from "../../../server/src/ai-evidence/types.js";

const signal = {
  type: "client_payment_dispute" as const, value: { disputed: true }, counterparty: { name: "Northstar Supply" },
  evidence: [{ sourceType: "case" as const, sourceId: "case-1" }],
  quotes: [{ sourceType: "case" as const, sourceId: "case-1", quote: "Northstar Supply disputes", start: 0, end: 26 }],
  rawScore: 0.9, calibratedScore: null, alternatives: [], selectionRationale: "Supported.",
};

function result(snapshotId: string, status: ExtractionResult["status"] = "review_required"): ExtractionResult {
  return {
    schemaVersion: 1, customerId: "customer-1", snapshotId, status,
    signals: [structuredClone(signal)], unmapped: [],
    model: { modelId: "fixture", promptVersion: "p1", catalogVersion: "proposed-v1", outputSchemaVersion: 1,
      packetBuilderVersion: "packet-v1", calibrationVersion: null },
  };
}

describe("next-run evidence review application", () => {
  it("applies a correction to a later snapshot without mutating the prior model output", () => {
    const prior = result("snapshot-1");
    const current = result("snapshot-2", "success");
    current.signals[0]!.value = { disputed: false };
    const corrected = { ...signal, value: { disputed: false } };
    const applied = applyReviewToNextExtraction(current, prior, {
      extractionId: "reviewed-1", reviewerId: "ramil", action: "corrected", correctedSignal: corrected,
    });
    expect(applied).toMatchObject({ status: "success", signals: [{ value: { disputed: false } }] });
    expect(prior.signals[0]?.value).toEqual({ disputed: true });
  });

  it("clears reviewed evidence, confirms results, and routes unchanged repeated errors back to review", () => {
    const prior = result("snapshot-1");
    const sameEvidenceResult = result("snapshot-2");
    const cleared = applyReviewToNextExtraction(sameEvidenceResult, prior, {
      extractionId: "reviewed-1", reviewerId: "ramil", action: "cleared", clearReason: "Unsupported claim",
    });
    expect(cleared).toMatchObject({ status: "review_required", signals: [{ value: { disputed: true } }] });

    const changed = result("snapshot-2");
    changed.signals[0]!.value = { disputed: false };
    expect(applyReviewToNextExtraction(changed, prior, {
      extractionId: "reviewed-1", reviewerId: "ramil", action: "cleared", clearReason: "Unsupported claim",
    })).toMatchObject({ status: "no_signal", signals: [] });

    expect(applyReviewToNextExtraction(result("snapshot-2", "review_required"), prior, {
      extractionId: "reviewed-1", reviewerId: "ramil", action: "confirmed",
    })).toMatchObject({ status: "success" });
  });

  it("rejects review reuse on the same snapshot or a different customer", () => {
    expect(() => applyReviewToNextExtraction(result("snapshot-1"), result("snapshot-1"), {
      extractionId: "reviewed-1", reviewerId: "ramil", action: "confirmed",
    })).toThrow("later snapshot");
    const differentCustomer = result("snapshot-1");
    differentCustomer.customerId = "customer-2";
    expect(() => applyReviewToNextExtraction(result("snapshot-2"), differentCustomer, {
      extractionId: "reviewed-1", reviewerId: "ramil", action: "confirmed",
    })).toThrow("later snapshot");
  });

  it("keeps failed extraction blocked until extraction is retried successfully", () => {
    expect(applyReviewToNextExtraction(result("snapshot-2", "failed"), result("snapshot-1"), {
      extractionId: "reviewed-1", reviewerId: "ramil", action: "confirmed",
    })).toMatchObject({ status: "failed" });
  });
});
