import type { EvidencePacket } from "./packet.js";
import type { EvidenceModelProvider } from "./provider.js";
import { validateExtractionResult, type ValidatedExtraction } from "./validate.js";
import { AI_EVIDENCE_SCHEMA_VERSION } from "./types.js";

export interface ExtractionOptions {
  /** Number of retries after the initial invalid response. Defaults to two. */
  maxRetries?: number;
}

/** One bounded model request per attempt; only deterministic validation can accept output. */
export async function extractEvidence(
  packet: EvidencePacket,
  provider: EvidenceModelProvider,
  options: ExtractionOptions = {},
): Promise<ValidatedExtraction> {
  const maxRetries = options.maxRetries ?? 2;
  if (!Number.isSafeInteger(maxRetries) || maxRetries < 0 || maxRetries > 5) {
    throw new Error("maxRetries must be a safe integer between 0 and 5");
  }

  let lastError = "Model output could not be validated";
  for (let attempt = 0; attempt <= maxRetries; attempt += 1) {
    try {
      return validateExtractionResult(await provider.extract(packet), packet);
    } catch (error) {
      lastError = error instanceof Error ? error.message : "Provider or validation failed";
    }
  }

  return {
    schemaVersion: AI_EVIDENCE_SCHEMA_VERSION,
    customerId: packet.customerId,
    snapshotId: packet.snapshotId,
    status: "failed",
    signals: [],
    unmapped: [],
    model: {
      modelId: provider.metadata.modelId,
      promptVersion: provider.metadata.promptVersion,
      catalogVersion: provider.metadata.catalogVersion,
      outputSchemaVersion: provider.metadata.outputSchemaVersion,
      packetBuilderVersion: provider.metadata.packetBuilderVersion,
      calibrationVersion: provider.metadata.calibrationVersion,
    },
    failureReason: lastError,
  };
}
