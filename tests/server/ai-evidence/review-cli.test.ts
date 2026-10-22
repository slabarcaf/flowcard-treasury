import { describe, expect, it } from "vitest";
import type { DatabasePool, QueryResult } from "../../../server/src/db/pool.js";
import { runEvidenceReviewCli } from "../../../server/src/ai-evidence/review-cli.js";

const signal = {
  type: "client_payment_dispute", value: { disputed: true }, counterparty: { name: "Northstar" },
  evidence: [{ sourceType: "case", sourceId: "case-1" }],
  quotes: [{ sourceType: "case", sourceId: "case-1", quote: "Northstar disputes", start: 0, end: 18 }],
  rawScore: 0.8, calibratedScore: null, alternatives: [], selectionRationale: null,
};

class ReviewPool implements DatabasePool {
  calls: Array<{ sql: string; values: readonly unknown[] }> = [];
  constructor(private readonly extractionStatus = "review_required") {}
  async query<Row>(sql: string, values: readonly unknown[] = []): Promise<QueryResult<Row>> {
    this.calls.push({ sql: sql.replace(/\s+/g, " ").trim(), values });
    if (sql.includes("FROM ai_evidence_extractions WHERE extraction_id")) {
      const output = this.extractionStatus === "unmapped" ? { unmapped: [{ type: "new_type" }] } : { signals: [{ type: "client_payment_dispute" }] };
      return { rows: [{ extraction_id: "extract-1", customer_id: "customer-1", status: this.extractionStatus, output } as Row], rowCount: 1 };
    }
    if (sql.includes("INSERT INTO ai_evidence_reviews")) return { rows: [], rowCount: 1 };
    const unmapped = this.extractionStatus === "unmapped" ? [{ type: "new_type" }] : [];
    return { rows: [{ extractionId: "extract-1", status: this.extractionStatus, unmapped } as Row], rowCount: 1 };
  }
  async connect() { throw new Error("review CLI must not open a transaction client"); }
  async end() {}
}

describe("operator evidence review CLI", () => {
  it("fails closed when the operator review feature is not enabled", async () => {
    await expect(runEvidenceReviewCli(new ReviewPool(), ["list"], {})).rejects.toThrow("AI_EVIDENCE_REVIEW_ENABLED=true");
  });

  it("lists unresolved review and unmapped outputs without requiring write services", async () => {
    const pool = new ReviewPool();
    const output: string[] = [];
    await runEvidenceReviewCli(pool, ["list"], { AI_EVIDENCE_REVIEW_ENABLED: "true" }, (text) => output.push(text));
    expect(JSON.parse(output[0]!)).toMatchObject([{ status: "review_required", unmapped: [] }]);
    expect(pool.calls[0]?.sql).toContain("e.status IN ('review_required', 'unmapped')");
    expect(pool.calls[0]?.sql).toContain("NOT EXISTS");
  });

  it("inspects the original result and records all structured dispositions with reviewer identity", async () => {
    for (const args of [
      ["confirm", "--extraction-id", "extract-1", "--reviewer-id", "ramil"],
      ["correct", "--extraction-id", "extract-1", "--reviewer-id", "ramil", "--signal-json", JSON.stringify(signal)],
      ["clear", "--extraction-id", "extract-1", "--reviewer-id", "ramil", "--reason", "Not supported"],
    ]) {
      const pool = new ReviewPool();
      await runEvidenceReviewCli(pool, args, { AI_EVIDENCE_REVIEW_ENABLED: "true", AI_EVIDENCE_REVIEWER_ID: "ramil" }, () => {});
      const insert = pool.calls.find(({ sql }) => sql.includes("INSERT INTO ai_evidence_reviews"));
      expect(insert?.values).toContain("ramil");
      expect(insert?.sql).toContain("INSERT INTO ai_evidence_reviews");
      expect(insert?.sql).not.toMatch(/policy|airwallex|salesforce/i);
    }
    const inspectPool = new ReviewPool();
    const output: string[] = [];
    await runEvidenceReviewCli(inspectPool, ["inspect", "--extraction-id", "extract-1"],
      { AI_EVIDENCE_REVIEW_ENABLED: "true" }, (text) => output.push(text));
    expect(JSON.parse(output[0]!)).toMatchObject({ output: { signals: [{ type: "client_payment_dispute" }] } });
  });

  it("rejects malformed correction, absent reviewer, and resolved records", async () => {
    await expect(runEvidenceReviewCli(new ReviewPool(), ["correct", "--extraction-id", "extract-1", "--reviewer-id", "r", "--signal-json", "{}"],
      { AI_EVIDENCE_REVIEW_ENABLED: "true" })).rejects.toThrow();
    await expect(runEvidenceReviewCli(new ReviewPool(), ["confirm", "--extraction-id", "extract-1"],
      { AI_EVIDENCE_REVIEW_ENABLED: "true", AI_EVIDENCE_REVIEWER_ID: "ramil" })).rejects.toThrow("--reviewer-id is required");
    const resolved = new ReviewPool("success");
    await expect(runEvidenceReviewCli(resolved, ["confirm", "--extraction-id", "extract-1", "--reviewer-id", "r"],
      { AI_EVIDENCE_REVIEW_ENABLED: "true", AI_EVIDENCE_REVIEWER_ID: "r" })).rejects.toThrow("only mapped review_required extractions can be dispositioned");
    expect(resolved.calls.some(({ sql }) => sql.includes("INSERT INTO ai_evidence_reviews"))).toBe(false);
    await expect(runEvidenceReviewCli(new ReviewPool("unmapped"), ["confirm", "--extraction-id", "extract-1", "--reviewer-id", "r"],
      { AI_EVIDENCE_REVIEW_ENABLED: "true", AI_EVIDENCE_REVIEWER_ID: "r" })).rejects.toThrow("only mapped review_required extractions can be dispositioned");
  });

  it("requires the configured reviewer identity for dispositions", async () => {
    await expect(runEvidenceReviewCli(new ReviewPool(), ["confirm", "--extraction-id", "extract-1", "--reviewer-id", "other"],
      { AI_EVIDENCE_REVIEW_ENABLED: "true", AI_EVIDENCE_REVIEWER_ID: "configured" })).rejects.toThrow("must match AI_EVIDENCE_REVIEWER_ID");
    await expect(runEvidenceReviewCli(new ReviewPool(), ["confirm", "--extraction-id", "extract-1", "--reviewer-id", "configured"],
      { AI_EVIDENCE_REVIEW_ENABLED: "true" })).rejects.toThrow("AI_EVIDENCE_REVIEWER_ID is required");
  });
});
