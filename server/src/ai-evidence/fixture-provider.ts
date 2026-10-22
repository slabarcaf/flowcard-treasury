import type { EvidencePacket } from "./packet.js";
import type { EvidenceModelProvider } from "./provider.js";
import type { EvidenceProviderMetadata } from "./provider.js";

export type EvidenceFixtureScenario = "direct" | "ambiguous" | "conflicting" | "irrelevant" | "duplicate" | "superseded";

/** Synthetic packets are selected explicitly by customer ID; fixture output is stable and offline. */
export const EVIDENCE_FIXTURES: Readonly<Record<string, EvidenceFixtureScenario>> = Object.freeze({
  "fixture-direct": "direct",
  "fixture-ambiguous": "ambiguous",
  "fixture-conflicting": "conflicting",
  "fixture-irrelevant": "irrelevant",
  "fixture-duplicate": "duplicate",
  "fixture-superseded": "superseded",
});

const FIXTURE_MODEL: EvidenceProviderMetadata = Object.freeze({
  modelId: "synthetic-fixture-v1",
  promptVersion: "evidence-prompt-v1",
  catalogVersion: "proposed-v1",
  outputSchemaVersion: 1,
  packetBuilderVersion: "packet-v1",
  calibrationVersion: null,
  configurationVersion: "evidence-config-v1",
});

export class FixtureEvidenceProvider implements EvidenceModelProvider {
  readonly metadata = FIXTURE_MODEL;
  readonly toolsEnabled = false as const;

  async extract(packet: EvidencePacket): Promise<unknown> {
    const scenario = EVIDENCE_FIXTURES[packet.customerId];
    if (!scenario) throw new Error(`No AI evidence fixture for customer: ${packet.customerId}`);
    const evidence = packet.entries[0];
    const ref = evidence ? { sourceType: evidence.sourceType, sourceId: evidence.sourceIds[0] } : null;
    const quote = evidence ? firstText(evidence.fields) : null;
    const signal = (value: unknown, rawScore: number, rationale: string) => ({
      type: "client_payment_dispute",
      value,
      counterparty: { name: "Northstar Supply" },
      evidence: ref ? [ref] : [],
      quotes: ref && quote ? [{ ...ref, quote, start: 0, end: quote.length }] : [],
      rawScore,
      calibratedScore: null,
      alternatives: [],
      selectionRationale: rationale,
    });
    const common = { schemaVersion: 1, customerId: packet.customerId, snapshotId: packet.snapshotId, model: this.metadata };
    switch (scenario) {
      case "direct":
        return { ...common, status: "success", signals: [signal({ disputed: true }, 0.98, "Direct synthetic claim.")], unmapped: [] };
      case "ambiguous":
        return { ...common, status: "review_required", signals: [signal({ disputed: "unclear" }, 0.51, "Synthetic wording is ambiguous.")], unmapped: [] };
      case "conflicting":
        return { ...common, status: "review_required", signals: [signal({ disputed: true }, 0.72, "Synthetic sources conflict.")], unmapped: [] };
      case "irrelevant":
        return { ...common, status: "no_signal", signals: [], unmapped: [] };
      case "duplicate":
        return { ...common, status: "success", signals: [signal({ duplicateEvidence: true }, 0.97, "Exact duplicate provenance is retained in the packet.")], unmapped: [] };
      case "superseded":
        return { ...common, status: "review_required", signals: [signal({ supersessionRequiresReview: true }, 0.8, "Superseded synthetic claim remains for review.")], unmapped: [] };
    }
  }
}

function firstText(value: unknown): string | null {
  if (typeof value === "string") return value;
  if (Array.isArray(value)) {
    for (const item of value) { const found = firstText(item); if (found !== null) return found; }
  } else if (value && typeof value === "object") {
    for (const item of Object.values(value)) { const found = firstText(item); if (found !== null) return found; }
  }
  return null;
}
