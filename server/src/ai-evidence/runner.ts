import { createHash } from "node:crypto";
import type { DatabasePool } from "../db/pool.js";
import type { CustomerRosterEntry } from "../roster/types.js";
import { getCustomerSnapshotReadModel } from "../snapshots/freshness.js";
import { buildEvidencePacket, type EvidencePacketOptions } from "./packet.js";
import { extractEvidence } from "./extract.js";
import type { EvidenceModelProvider } from "./provider.js";
import { persistExtraction, getLatestEvidenceReview, getStoredExtraction } from "./repository.js";
import { applyReviewToNextExtraction } from "./review.js";
import { type ProposedSignalType, type ExtractionResult } from "./types.js";
import type { TypeQualification } from "./qualification.js";
import { evaluateCustomerDecisionGate } from "./decision-gate.js";

export interface EvidenceRunnerOptions {
  pool: DatabasePool;
  provider: EvidenceModelProvider;
  packet: EvidencePacketOptions;
  qualifications?: (at: Date) => Promise<Readonly<Partial<Record<ProposedSignalType, TypeQualification>>>>;
  now?: () => Date;
}

export interface EvidenceRunResult {
  customerId: string;
  status: "skipped" | ExtractionResult["status"];
  allowed: boolean;
  reason?: string;
  extractionId?: string;
}

/** Extracts only from the canonical, complete and fresh snapshot; provider has no CRM tools. */
export class DailyEvidenceRunner {
  private readonly now: () => Date;
  constructor(private readonly options: EvidenceRunnerOptions) {
    this.now = options.now ?? (() => new Date());
  }

  async runCustomer(customer: CustomerRosterEntry): Promise<EvidenceRunResult> {
    const at = this.now();
    const readModel = await getCustomerSnapshotReadModel(this.options.pool, customer.customerId, at);
    const packetResult = buildEvidencePacket(readModel, { ...this.options.packet, now: at });
    if (packetResult.status === "failed") {
      // A failed or stale snapshot is not evidence and must never trigger provider access.
      if (readModel.freshness !== "fresh" || !readModel.active || !readModel.snapshot) {
        return { customerId: customer.customerId, status: "skipped", allowed: false, reason: packetResult.reason };
      }
      const failed = this.failedResult(customer.customerId, readModel.snapshot.snapshotId, packetResult.reason);
      const extractionId = await persistExtraction(this.options.pool, failed,
        createHash("sha256").update(`${customer.customerId}\u0000${readModel.snapshot.snapshotId}\u0000${packetResult.reason}`).digest("hex"),
        this.options.provider.metadata.configurationVersion);
      return { customerId: customer.customerId, status: "failed", allowed: false, reason: packetResult.reason, extractionId };
    }
    const packet = packetResult.packet;
    const fingerprint = packetFingerprint(packet);
    let result = await extractEvidence(packet, this.options.provider);

    const priorReview = await getLatestEvidenceReview(this.options.pool, customer.customerId, fingerprint);
    if (priorReview) {
      const prior = await getStoredExtraction(this.options.pool, priorReview.extractionId);
      if (prior) result = applyReviewToNextExtraction(result, prior.result, priorReview);
    }

    const qualifications = await (this.options.qualifications?.(at) ?? Promise.resolve({}));
    const gate = evaluateCustomerDecisionGate(result, qualifications);
    const extractionId = await persistExtraction(
      this.options.pool, result, fingerprint, this.options.provider.metadata.configurationVersion,
    );
    return {
      customerId: customer.customerId,
      status: result.status,
      allowed: gate.allowed,
      ...(gate.reason ? { reason: gate.reason } : {}),
      extractionId,
    };
  }

  private failedResult(customerId: string, snapshotId: string, reason: string): ExtractionResult {
    const { metadata } = this.options.provider;
    return {
      schemaVersion: 1, customerId, snapshotId, status: "failed", signals: [], unmapped: [],
      model: { modelId: metadata.modelId, promptVersion: metadata.promptVersion, catalogVersion: metadata.catalogVersion,
        outputSchemaVersion: metadata.outputSchemaVersion, packetBuilderVersion: metadata.packetBuilderVersion,
        calibrationVersion: metadata.calibrationVersion },
      failureReason: reason,
    };
  }
}

function packetFingerprint(packet: { customerId: string; entries: readonly { sourceType: string; fields: Readonly<Record<string, unknown>> }[] }): string {
  const canonical = JSON.stringify({
    customerId: packet.customerId,
    entries: packet.entries.map(({ sourceType, fields }) => ({ sourceType, fields }))
      .sort((left, right) => left.sourceType.localeCompare(right.sourceType) || JSON.stringify(left.fields).localeCompare(JSON.stringify(right.fields))),
  });
  return createHash("sha256").update(canonical, "utf8").digest("hex");
}
