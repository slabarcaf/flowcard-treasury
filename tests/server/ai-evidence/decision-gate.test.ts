import { describe, expect, it } from "vitest";
import { evaluateCustomerDecisionGate } from "../../../server/src/ai-evidence/decision-gate.js";
import type { ExtractionResult } from "../../../server/src/ai-evidence/types.js";
import type { TypeQualification } from "../../../server/src/ai-evidence/qualification.js";
import { fitCalibrationMapping } from "../../../server/src/ai-evidence/calibration.js";

const base: ExtractionResult = {
  schemaVersion: 1, customerId: "customer-a", snapshotId: "snapshot-a", status: "no_signal", signals: [], unmapped: [],
  model: { modelId: "fixture", promptVersion: "p1", catalogVersion: "proposed-v1", outputSchemaVersion: 1,
    packetBuilderVersion: "packet-v1", calibrationVersion: null },
};
const qualified: TypeQualification = {
  signalType: "client_payment_dispute", calibrationVersion: "cal-v1", policyVersion: "one-sided-exact-binomial-95-v1",
  evaluatedAt: "2026-10-22T00:00:00Z", sampleCount: 59, errors: 0, precisionLowerBound: 0.95,
  qualified: true, mode: "auto_apply_with_audit",
};
const calibrated = fitCalibrationMapping("client_payment_dispute", "cal-v1", [
  { signalType: "client_payment_dispute", rawScore: 0.99, correct: true, labelId: "cal-label-1", labeledAt: "2026-10-22T00:00:00Z" },
]);

describe("customer evidence decision gate", () => {
  it.each(["review_required", "unmapped", "failed"] as const)("blocks %s for this customer", (status) => {
    const result = { ...base, status } as ExtractionResult;
    expect(evaluateCustomerDecisionGate(result, {}).allowed).toBe(false);
  });

  it("allows valid no_signal, gates every proposed signal by qualification, and does not affect another customer", () => {
    expect(evaluateCustomerDecisionGate(base, {}).allowed).toBe(true);
    const result: ExtractionResult = { ...base, status: "success", model: { ...base.model, calibrationVersion: "cal-v1" }, signals: [{
      type: "client_payment_dispute", value: true, counterparty: { name: "Northstar" },
      evidence: [{ sourceType: "case", sourceId: "case-a" }],
      quotes: [{ sourceType: "case", sourceId: "case-a", quote: "claim", start: 0, end: 5 }],
      rawScore: 0.99, calibratedScore: 0.99, alternatives: [], selectionRationale: null,
    }] };
    expect(evaluateCustomerDecisionGate(result, {}).allowed).toBe(false);
    expect(evaluateCustomerDecisionGate(result, { client_payment_dispute: qualified }, { client_payment_dispute: calibrated }).allowed).toBe(true);
    // Provider supplied calibrated scores and model versions cannot qualify a signal without
    // a trusted server mapping, even when the claimed values match qualification.
    expect(evaluateCustomerDecisionGate(result, { client_payment_dispute: qualified }).allowed).toBe(false);
    expect(evaluateCustomerDecisionGate({ ...result, customerId: "customer-b", status: "no_signal", signals: [] }, {}).allowed).toBe(true);
  });

  it("requires a calibrated score tied to the qualification version before automatic use", () => {
    const result: ExtractionResult = { ...base, status: "success", model: { ...base.model, calibrationVersion: "cal-v1" }, signals: [{
      type: "client_payment_dispute", value: true, counterparty: { name: "Northstar" },
      evidence: [{ sourceType: "case", sourceId: "case-a" }],
      quotes: [{ sourceType: "case", sourceId: "case-a", quote: "claim", start: 0, end: 5 }],
      rawScore: 0.99, calibratedScore: 0.99, alternatives: [], selectionRationale: null,
    }] };
    expect(evaluateCustomerDecisionGate(result, { client_payment_dispute: qualified }, { client_payment_dispute: calibrated }).allowed).toBe(true);
    expect(evaluateCustomerDecisionGate({ ...result, signals: [{ ...result.signals[0]!, calibratedScore: null }] },
      { client_payment_dispute: qualified })).toMatchObject({ allowed: false, reason: expect.stringContaining("calibration score") });
    expect(evaluateCustomerDecisionGate(result,
      { client_payment_dispute: qualified }, { client_payment_dispute: { ...calibrated, version: "cal-v2" } }))
      .toMatchObject({ allowed: false, reason: expect.stringContaining("calibration score") });
  });
});
