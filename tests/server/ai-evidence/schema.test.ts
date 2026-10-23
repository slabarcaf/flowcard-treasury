import { describe, expect, it } from "vitest";
import { loadSignalCatalog, SIGNAL_CATALOG_PRODUCTION_APPROVED, SIGNAL_CATALOG_VERSION } from "../../../server/src/ai-evidence/catalog.js";
import { parseExtractionResult } from "../../../server/src/ai-evidence/schema.js";
import { AI_EVIDENCE_CATALOG_VERSION } from "../../../server/src/ai-evidence/types.js";

const model = {
  modelId: "fixture-v1",
  promptVersion: "prompt-v1",
  catalogVersion: "proposed-v1",
  outputSchemaVersion: 1,
  packetBuilderVersion: "packet-v1",
  calibrationVersion: null,
};

const base = {
  schemaVersion: 1,
  customerId: "customer-1",
  snapshotId: "snapshot-1",
  status: "success",
  signals: [
    {
      type: "client_payment_dispute",
      value: { disputed: true, revenueShare: 0.45 },
      counterparty: { name: "Acme, Inc." },
      evidence: [{ sourceType: "case", sourceId: "case-1" }],
      quotes: [{ sourceType: "case", sourceId: "case-1", quote: "Acme disputes payment", start: 0, end: 22 }],
      rawScore: 0.92,
      calibratedScore: null,
      alternatives: [],
      selectionRationale: "Directly stated in the case note.",
    },
  ],
  unmapped: [],
  model,
};

describe("AI evidence result schema", () => {
  it("uses the catalog's version as the extraction metadata version", () => {
    expect(AI_EVIDENCE_CATALOG_VERSION).toBe(SIGNAL_CATALOG_VERSION);
  });

  it("parses a valid quote-backed signal with version metadata", () => {
    expect(parseExtractionResult(base)).toMatchObject({
      schemaVersion: 1,
      status: "success",
      signals: [{ type: "client_payment_dispute", rawScore: 0.92, quotes: [{ sourceId: "case-1" }] }],
      model,
    });
  });

  it("accepts a successful empty extraction only as no_signal", () => {
    expect(parseExtractionResult({ ...base, status: "no_signal", signals: [] })).toMatchObject({
      status: "no_signal",
      signals: [],
    });
    expect(() => parseExtractionResult({ ...base, status: "success", signals: [] })).toThrow(/requires at least one signal/);
  });

  it("routes unknown signal types to unmapped review while preserving evidence", () => {
    const unknown = {
      ...base,
      signals: [{ type: "unapproved_signal", value: "claim", evidence: [{ sourceType: "call_activity", sourceId: "call-1" }] }],
    };
    expect(parseExtractionResult(unknown)).toMatchObject({
      status: "unmapped",
      signals: [],
      unmapped: [{ type: "unapproved_signal", value: "claim", evidence: [{ sourceId: "call-1" }] }],
    });
  });

  it("keeps approved claims while routing accompanying unmapped evidence for review", () => {
    expect(parseExtractionResult({
      ...base,
      unmapped: [{ type: "other_relevant_evidence", value: "review this", evidence: [] }],
    })).toMatchObject({ status: "unmapped", signals: [{ type: "client_payment_dispute" }] });
  });

  it("rejects malformed evidence, scores, and status combinations", () => {
    expect(() => parseExtractionResult({ ...base, signals: [{ ...base.signals[0], rawScore: 1.2 }] })).toThrow(/rawScore/);
    expect(() => parseExtractionResult({ ...base, signals: [{ ...base.signals[0], quotes: [] }] })).not.toThrow();
    expect(() => parseExtractionResult({ ...base, status: "no_signal", signals: base.signals })).toThrow(/no_signal result cannot contain/);
    expect(() => parseExtractionResult({ ...base, signals: [{ ...base.signals[0], evidence: [{ sourceType: "email", sourceId: "e-1" }] }] })).toThrow(/sourceType/);
  });

  it("loads only the proposed closed catalog and keeps it unapproved for production", () => {
    expect(Object.keys(loadSignalCatalog())).toEqual([
      "client_payment_dispute",
      "client_revenue_share",
      "expected_inflow_delay",
      "positive_note",
    ]);
    expect(SIGNAL_CATALOG_PRODUCTION_APPROVED).toBe(false);
    expect(() => loadSignalCatalog("future-v2")).toThrow(/Unsupported signal catalog/);
  });
});
