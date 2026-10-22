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
  async query<Row>(sql: string, values: readonly unknown[] = []): Promise<QueryResult<Row>> {
    this.calls.push({ sql: sql.replace(/\s+/g, " ").trim(), values });
    if (sql.includes("FROM ai_evidence_extractions WHERE extraction_id")) {
      return { rows: [{ extraction_id: "extract-1", customer_id: "customer-1", status: "unmapped", output: { unmapped: [{ type: "new_type" }] } } as Row], rowCount: 1 };
    }
    if (sql.includes("INSERT INTO ai_evidence_reviews")) return { rows: [], rowCount: 1 };
    return { rows: [{ extractionId: "extract-1", status: "unmapped", unmapped: [{ type: "new_type" }] } as Row], rowCount: 1 };
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
    expect(JSON.parse(output[0]!)).toMatchObject([{ status: "unmapped", unmapped: [{ type: "new_type" }] }]);
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
      await runEvidenceReviewCli(pool, args, { AI_EVIDENCE_REVIEW_ENABLED: "true" }, () => {});
      const insert = pool.calls.find(({ sql }) => sql.includes("INSERT INTO ai_evidence_reviews"));
      expect(insert?.values).toContain("ramil");
      expect(insert?.sql).toContain("INSERT INTO ai_evidence_reviews");
      expect(insert?.sql).not.toMatch(/policy|airwallex|salesforce/i);
    }
    const inspectPool = new ReviewPool();
    const output: string[] = [];
    await runEvidenceReviewCli(inspectPool, ["inspect", "--extraction-id", "extract-1"],
      { AI_EVIDENCE_REVIEW_ENABLED: "true" }, (text) => output.push(text));
    expect(JSON.parse(output[0]!)).toMatchObject({ output: { unmapped: [{ type: "new_type" }] } });
  });

  it("rejects malformed correction, absent reviewer and resolved records", async () => {
    await expect(runEvidenceReviewCli(new ReviewPool(), ["correct", "--extraction-id", "extract-1", "--reviewer-id", "r", "--signal-json", "{}"],
      { AI_EVIDENCE_REVIEW_ENABLED: "true" })).rejects.toThrow();
    await expect(runEvidenceReviewCli(new ReviewPool(), ["confirm", "--extraction-id", "extract-1"],
      { AI_EVIDENCE_REVIEW_ENABLED: "true" })).rejects.toThrow("--reviewer-id is required");
  });
});
