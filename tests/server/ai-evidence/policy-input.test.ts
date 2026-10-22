import { describe, expect, it } from "vitest";
import { buildPolicyInput } from "../../../server/src/ai-evidence/policy-input.js";
import type { ExtractionResult } from "../../../server/src/ai-evidence/types.js";
import type { Signals } from "../../../src/policy/types.js";

const existing: Signals = { inflowReliability: "yellow", inflowConcentration: "green", cashBuffer: "yellow", trackRecord: "green" };
const noSignal: ExtractionResult = {
  schemaVersion: 1, customerId: "customer-a", snapshotId: "snapshot-a", status: "no_signal", signals: [], unmapped: [],
  model: { modelId: "fixture", promptVersion: "p1", catalogVersion: "proposed-v1", outputSchemaVersion: 1,
    packetBuilderVersion: "packet-v1", calibrationVersion: null },
};

describe("deterministic policy input boundary", () => {
  it("passes existing profile data unchanged for valid no_signal", () => {
    expect(buildPolicyInput(existing, noSignal, {})).toEqual({ gate: { allowed: true, reason: null }, signals: existing });
  });

  it("fails closed for unqualified, unmapped, or not source-approved signal meanings", () => {
    const success: ExtractionResult = { ...noSignal, status: "success", model: { ...noSignal.model, calibrationVersion: "cal-v1" }, signals: [{
      type: "client_payment_dispute", value: { disputed: true }, counterparty: { name: "Northstar" },
      evidence: [{ sourceType: "case", sourceId: "case-a" }],
      quotes: [{ sourceType: "case", sourceId: "case-a", quote: "claim", start: 0, end: 5 }],
      rawScore: 0.99, calibratedScore: 0.99, alternatives: [], selectionRationale: null,
    }] };
    expect(buildPolicyInput(existing, success, {}).signals).toBeNull();
    const result = buildPolicyInput(existing, success, {}, { client_payment_dispute: { key: "trackRecord", ratingsByValue: { '{"disputed":true}': "red" } } });
    expect(result.gate.allowed).toBe(false);
    expect(result.signals).toBeNull();
  });

  it("uses an explicitly approved meaning without changing calculation code or values", () => {
    const success: ExtractionResult = { ...noSignal, status: "success", model: { ...noSignal.model, calibrationVersion: "cal-v1" }, signals: [{
      type: "client_payment_dispute", value: "confirmed-dispute", counterparty: { name: "Northstar" },
      evidence: [{ sourceType: "case", sourceId: "case-a" }],
      quotes: [{ sourceType: "case", sourceId: "case-a", quote: "claim", start: 0, end: 5 }],
      rawScore: 0.99, calibratedScore: 0.99, alternatives: [], selectionRationale: null,
    }] };
    const qualification = { signalType: "client_payment_dispute" as const, calibrationVersion: "cal-v1",
      policyVersion: "one-sided-exact-binomial-95-v1" as const, evaluatedAt: "2026-10-22T00:00:00Z", sampleCount: 59,
      errors: 0, precisionLowerBound: 0.95, qualified: true, mode: "auto_apply_with_audit" as const };
    expect(buildPolicyInput(existing, success, { client_payment_dispute: qualification }, {
      client_payment_dispute: { key: "trackRecord", ratingsByValue: { '"confirmed-dispute"': "red" } },
    })).toMatchObject({ gate: { allowed: true }, signals: { ...existing, trackRecord: "red" } });
  });
});
