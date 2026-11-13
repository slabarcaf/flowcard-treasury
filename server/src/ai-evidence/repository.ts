import { randomUUID } from "node:crypto";
import type { DatabasePool } from "../db/pool.js";
import type { ExtractionResult } from "./types.js";

export interface StoredExtraction {
  extractionId: string;
  result: ExtractionResult;
  evidenceFingerprint: string;
  configurationVersion: string;
}

export interface EvidenceReview {
  extractionId: string;
  reviewerId: string;
  action: "confirmed" | "corrected" | "cleared";
  correctedSignal?: unknown;
  clearReason?: string;
}

/** Read the original output used to interpret a reviewer decision on a later run. */
export async function getStoredExtraction(pool: DatabasePool, extractionId: string): Promise<StoredExtraction | null> {
  const query = await pool.query<{
    evidence_fingerprint: string; configuration_version: string; output: ExtractionResult;
  }>(
    `SELECT evidence_fingerprint, configuration_version, output
       FROM ai_evidence_extractions WHERE extraction_id = $1`,
    [extractionId],
  );
  const row = query.rows[0];
  return row ? {
    extractionId,
    result: row.output,
    evidenceFingerprint: row.evidence_fingerprint,
    configurationVersion: row.configuration_version,
  } : null;
}

/** Append an immutable model result and its selected and alternative claims atomically. */
export async function persistExtraction(
  pool: DatabasePool,
  result: ExtractionResult,
  evidenceFingerprint: string,
  configurationVersion: string,
  decision: { allowed: boolean; reason?: string | null } = { allowed: false, reason: "Decision gate was not evaluated" },
): Promise<string> {
  assertFingerprint(evidenceFingerprint);
  const client = await pool.connect();
  const extractionId = randomUUID();
  try {
    await client.query("BEGIN");
    await client.query(
      `INSERT INTO ai_evidence_extractions (
         extraction_id, customer_id, snapshot_id, evidence_fingerprint,
         configuration_version, status, decision_allowed, decision_block_reason, output
       ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9::jsonb)`,
      [extractionId, result.customerId, result.snapshotId, evidenceFingerprint,
        configurationVersion, result.status, decision.allowed, decision.reason ?? null, JSON.stringify(result)],
    );
    let claimIndex = 0;
    for (const signal of result.signals) {
      await client.query(
        `INSERT INTO ai_evidence_claims (extraction_id, claim_index, signal, evidence_references, is_selected)
         VALUES ($1, $2, $3::jsonb, $4::jsonb, true)`,
        [extractionId, claimIndex++, JSON.stringify(signal), JSON.stringify(signal.evidence)],
      );
      for (const alternative of signal.alternatives) {
        await client.query(
          `INSERT INTO ai_evidence_claims (extraction_id, claim_index, signal, evidence_references, is_selected)
           VALUES ($1, $2, $3::jsonb, $4::jsonb, false)`,
          [extractionId, claimIndex++, JSON.stringify(alternative), JSON.stringify(alternative.evidence)],
        );
      }
    }
    await client.query("COMMIT");
    return extractionId;
  } catch (error) {
    await client.query("ROLLBACK");
    throw error;
  } finally {
    client.release();
  }
}

/** Append a reviewer decision without modifying the model extraction. */
export async function recordEvidenceReview(pool: DatabasePool, review: EvidenceReview): Promise<void> {
  if (!review.reviewerId.trim()) throw new Error("reviewerId is required");
  if (review.action === "corrected" && review.correctedSignal === undefined) throw new Error("correctedSignal is required for correction");
  if (review.action === "cleared" && !review.clearReason?.trim()) throw new Error("clearReason is required when clearing");
  if (review.action !== "corrected" && review.correctedSignal !== undefined) throw new Error("correctedSignal is only valid for correction");
  if (review.action !== "cleared" && review.clearReason !== undefined) throw new Error("clearReason is only valid when clearing");
  const inserted = await pool.query(
    `INSERT INTO ai_evidence_reviews (
       review_id, extraction_id, customer_id, evidence_fingerprint, reviewer_id,
       action, corrected_signal, clear_reason
     ) SELECT $1, extraction_id, customer_id, evidence_fingerprint, $2, $3, $4::jsonb, $5
         FROM ai_evidence_extractions WHERE extraction_id = $6`,
    [randomUUID(), review.reviewerId.trim(), review.action,
      review.correctedSignal === undefined ? null : JSON.stringify(review.correctedSignal),
      review.clearReason ?? null, review.extractionId],
  );
  if (inserted.rowCount !== 1) throw new Error("extraction not found for review");
}

/** Get the latest matching decision to apply to a later daily extraction. */
export async function getLatestEvidenceReview(
  pool: DatabasePool,
  customerId: string,
  evidenceFingerprint: string,
): Promise<EvidenceReview | null> {
  assertFingerprint(evidenceFingerprint);
  const query = await pool.query<{
    extraction_id: string; reviewer_id: string; action: EvidenceReview["action"];
    corrected_signal: unknown; clear_reason: string | null;
  }>(
    `SELECT extraction_id, reviewer_id, action, corrected_signal, clear_reason
       FROM ai_evidence_reviews
      WHERE customer_id = $1 AND evidence_fingerprint = $2
      ORDER BY created_at DESC, review_id DESC LIMIT 1`,
    [customerId, evidenceFingerprint],
  );
  const row = query.rows[0];
  if (!row) return null;
  return {
    extractionId: row.extraction_id,
    reviewerId: row.reviewer_id,
    action: row.action,
    ...(row.corrected_signal === null ? {} : { correctedSignal: row.corrected_signal }),
    ...(row.clear_reason === null ? {} : { clearReason: row.clear_reason }),
  };
}

function assertFingerprint(value: string): void {
  if (!/^[a-f0-9]{64}$/.test(value)) throw new Error("evidenceFingerprint must be a lowercase SHA-256 hex digest");
}
