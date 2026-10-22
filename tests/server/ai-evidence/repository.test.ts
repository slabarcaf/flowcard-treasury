import { readFile } from "node:fs/promises";
import { describe, expect, it } from "vitest";
import type { DatabaseClient, DatabasePool, QueryResult } from "../../../server/src/db/pool.js";
import { persistExtraction, recordEvidenceReview, getLatestEvidenceReview } from "../../../server/src/ai-evidence/repository.js";
import type { ExtractionResult } from "../../../server/src/ai-evidence/types.js";

const migration = new URL("../../../server/src/db/migrations/004_ai_evidence.sql", import.meta.url);
const fingerprint = "a".repeat(64);

function extraction(snapshotId = "00000000-0000-0000-0000-000000000001"): ExtractionResult {
  return {
    schemaVersion: 1, customerId: "customer-1", snapshotId, status: "success",
    signals: [{
      type: "client_payment_dispute", value: { disputed: true }, counterparty: { name: "Northstar Supply" },
      evidence: [{ sourceType: "case", sourceId: "case-1" }],
      quotes: [{ sourceType: "case", sourceId: "case-1", quote: "Northstar Supply disputes invoice", start: 0, end: 33 }],
      rawScore: 0.9, calibratedScore: null,
      alternatives: [{ value: { disputed: false }, evidence: [{ sourceType: "case", sourceId: "case-1" }],
        quotes: [{ sourceType: "case", sourceId: "case-1", quote: "Northstar Supply disputes invoice", start: 0, end: 33 }], rationale: "Competing claim." }],
      selectionRationale: "Best supported claim.",
    }], unmapped: [],
    model: { modelId: "fixture", promptVersion: "p1", catalogVersion: "proposed-v1", outputSchemaVersion: 1,
      packetBuilderVersion: "packet-v1", calibrationVersion: null },
  };
}

class EvidencePool implements DatabasePool {
  statements: Array<{ sql: string; values: readonly unknown[] }> = [];
  shouldFail = false;
  reviewRow: { extraction_id: string; reviewer_id: string; action: "confirmed" | "corrected" | "cleared";
    corrected_signal: unknown; clear_reason: string | null } | null = null;

  async query<Row>(text: string, values: readonly unknown[] = []): Promise<QueryResult<Row>> {
    this.statements.push({ sql: normalize(text), values });
    if (normalize(text).startsWith("SELECT extraction_id, reviewer_id")) {
      return { rows: this.reviewRow ? [this.reviewRow as Row] : [], rowCount: this.reviewRow ? 1 : 0 };
    }
    return { rows: [], rowCount: 1 };
  }

  async connect(): Promise<DatabaseClient> {
    return {
      query: async <_Row>(text: string, values: readonly unknown[] = []) => {
        this.statements.push({ sql: normalize(text), values });
        if (this.shouldFail && normalize(text).startsWith("INSERT INTO ai_evidence_claims")) throw new Error("injected claim failure");
        return { rows: [], rowCount: 1 };
      },
      release() {},
    };
  }
  async end(): Promise<void> {}
}

describe("AI evidence persistence", () => {
  it("defines immutable extraction and reviewer audit history with version and provenance fields", async () => {
    const sql = await readFile(migration, "utf8");
    expect(sql).toContain("CREATE TABLE IF NOT EXISTS ai_evidence_extractions");
    expect(sql).toContain("configuration_version text NOT NULL");
    expect(sql).toContain("decision_allowed boolean NOT NULL DEFAULT false");
    expect(sql).toContain("decision_block_reason text");
    expect(sql).toContain("evidence_references jsonb NOT NULL");
    expect(sql).toContain("CREATE TABLE IF NOT EXISTS ai_evidence_reviews");
    expect(sql).toContain("reviewer_id text NOT NULL");
    expect(sql).toContain("FOREIGN KEY (customer_id, evidence_fingerprint, extraction_id)");
    expect(sql).toContain("ai_evidence_claims_immutable");
    expect(sql).toContain("reject_ai_evidence_history_mutation");
  });

  it("stores the immutable extraction with selected and alternative claims in one transaction", async () => {
    const pool = new EvidencePool();
    const id = await persistExtraction(pool, extraction(), fingerprint, "model-prompt-catalog-v1", { allowed: true });
    expect(id).toMatch(/^[0-9a-f-]{36}$/);
    expect(pool.statements.map(({ sql }) => sql)).toEqual([
      "BEGIN", "INSERT INTO ai_evidence_extractions ( extraction_id, customer_id, snapshot_id, evidence_fingerprint, configuration_version, status, decision_allowed, decision_block_reason, output ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9::jsonb)",
      "INSERT INTO ai_evidence_claims (extraction_id, claim_index, signal, evidence_references, is_selected) VALUES ($1, $2, $3::jsonb, $4::jsonb, true)",
      "INSERT INTO ai_evidence_claims (extraction_id, claim_index, signal, evidence_references, is_selected) VALUES ($1, $2, $3::jsonb, $4::jsonb, false)", "COMMIT",
    ]);
    expect(pool.statements[1]?.values[6]).toBe(true);
    expect(pool.statements[1]?.values[8]).toContain('"snapshotId"');
    expect(pool.statements[2]?.sql.endsWith("true)")).toBe(true);
    expect(pool.statements[3]?.sql.endsWith("false)")).toBe(true);
  });

  it("rolls back incomplete extraction writes and rejects malformed fingerprints", async () => {
    const pool = new EvidencePool();
    pool.shouldFail = true;
    await expect(persistExtraction(pool, extraction(), fingerprint, "v1")).rejects.toThrow("injected claim failure");
    expect(pool.statements.map(({ sql }) => sql).slice(-1)).toEqual(["ROLLBACK"]);
    await expect(persistExtraction(pool, extraction(), "not-a-digest", "v1")).rejects.toThrow("SHA-256");
  });

  it("records append-only reviewer actions and retrieves the latest matching review", async () => {
    const pool = new EvidencePool();
    await recordEvidenceReview(pool, { extractionId: "extraction-1", reviewerId: "ramil", action: "cleared", clearReason: "Not a customer claim" });
    expect(pool.statements[0]?.sql).toContain("INSERT INTO ai_evidence_reviews");
    expect(pool.statements[0]?.sql).toContain("FROM ai_evidence_extractions WHERE extraction_id = $6");
    expect(pool.statements[0]?.values[1]).toBe("ramil");
    pool.reviewRow = { extraction_id: "extraction-1", reviewer_id: "ramil", action: "cleared", corrected_signal: null, clear_reason: "Not a customer claim" };
    await expect(getLatestEvidenceReview(pool, "customer-1", fingerprint)).resolves.toMatchObject({
      extractionId: "extraction-1", reviewerId: "ramil", action: "cleared", clearReason: "Not a customer claim",
    });
    await expect(getLatestEvidenceReview(pool, "customer-1", "bad")).rejects.toThrow("SHA-256");
    await expect(recordEvidenceReview(pool, { extractionId: "x", reviewerId: "", action: "confirmed" })).rejects.toThrow("reviewerId");
  });
});

function normalize(sql: string): string {
  return sql.replace(/\s+/g, " ").trim();
}
