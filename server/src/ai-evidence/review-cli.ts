import type { DatabasePool } from "../db/pool.js";
import { parseExtractionResult } from "./schema.js";
import { recordEvidenceReview } from "./repository.js";
import type { ExtractedSignal } from "./types.js";

type ReviewCommand = "list" | "inspect" | "confirm" | "correct" | "clear";

function parseOptions(args: string[]): Map<string, string> {
  const values = new Map<string, string>();
  for (let index = 0; index < args.length; index += 1) {
    const name = args[index];
    const value = args[index + 1];
    if (!name?.startsWith("--") || !value || value.startsWith("--")) {
      throw new Error(`Expected --option value near: ${name ?? "<end>"}`);
    }
    if (values.has(name)) throw new Error(`Duplicate option: ${name}`);
    values.set(name, value);
    index += 1;
  }
  return values;
}

function required(options: Map<string, string>, name: string): string {
  const value = options.get(name)?.trim();
  if (!value) throw new Error(`${name} is required`);
  return value;
}

/** Operator-only local CLI. It has database read/audit access only. */
export async function runEvidenceReviewCli(
  pool: DatabasePool,
  args: string[],
  env: NodeJS.ProcessEnv,
  write: (text: string) => void = (text) => process.stdout.write(text),
): Promise<void> {
  if (env.AI_EVIDENCE_REVIEW_ENABLED?.trim().toLowerCase() !== "true") {
    throw new Error("AI_EVIDENCE_REVIEW_ENABLED=true is required for operator review commands");
  }
  const [rawCommand, ...rest] = args;
  if (!rawCommand || !["list", "inspect", "confirm", "correct", "clear"].includes(rawCommand)) {
    throw new Error("Usage: server:review <list|inspect|confirm|correct|clear> [--options]");
  }
  const command = rawCommand as ReviewCommand;
  const options = parseOptions(rest);
  if (command === "list") {
    const result = await pool.query(`
      SELECT e.extraction_id AS "extractionId", e.customer_id AS "customerId",
             e.snapshot_id AS "snapshotId", e.status, e.created_at AS "createdAt",
             e.output->'unmapped' AS unmapped
        FROM ai_evidence_extractions e
       WHERE e.status IN ('review_required', 'unmapped')
         AND NOT EXISTS (SELECT 1 FROM ai_evidence_reviews r WHERE r.extraction_id = e.extraction_id)
       ORDER BY e.created_at, e.customer_id`);
    write(`${JSON.stringify(result.rows, null, 2)}\n`);
    return;
  }

  const extractionId = required(options, "--extraction-id");
  const extraction = await pool.query<{
    extraction_id: string; customer_id: string; status: string; output: unknown;
  }>(`SELECT extraction_id, customer_id, status, output
        FROM ai_evidence_extractions WHERE extraction_id = $1`, [extractionId]);
  const row = extraction.rows[0];
  if (!row) throw new Error("extraction not found");

  if (command === "inspect") {
    write(`${JSON.stringify(row, null, 2)}\n`);
    return;
  }
  if (row.status !== "review_required") {
    throw new Error("only mapped review_required extractions can be dispositioned");
  }
  const reviewerId = required(options, "--reviewer-id");
  const configuredReviewerId = env.AI_EVIDENCE_REVIEWER_ID?.trim();
  if (!configuredReviewerId) throw new Error("AI_EVIDENCE_REVIEWER_ID is required for evidence disposition");
  if (reviewerId !== configuredReviewerId) throw new Error("--reviewer-id must match AI_EVIDENCE_REVIEWER_ID");
  if (command === "confirm") {
    await recordEvidenceReview(pool, { extractionId, reviewerId, action: "confirmed" });
  } else if (command === "correct") {
    const correctedSignal = parseSignal(required(options, "--signal-json"));
    await recordEvidenceReview(pool, { extractionId, reviewerId, action: "corrected", correctedSignal });
  } else {
    await recordEvidenceReview(pool, {
      extractionId, reviewerId, action: "cleared", clearReason: required(options, "--reason"),
    });
  }
  write(`${JSON.stringify({ extractionId, customerId: row.customer_id, reviewerId, disposition: command })}\n`);
}

function parseSignal(serialized: string): ExtractedSignal {
  let value: unknown;
  try {
    value = JSON.parse(serialized);
  } catch {
    throw new Error("--signal-json must contain valid JSON");
  }
  // Reuse the closed extraction schema to validate the corrected signal shape.
  const parsed = parseExtractionResult({
    schemaVersion: 1, customerId: "review", snapshotId: "review", status: "success",
    signals: [value], unmapped: [],
    model: { modelId: "review", promptVersion: "review", catalogVersion: "proposed-v1",
      outputSchemaVersion: 1, packetBuilderVersion: "review", calibrationVersion: null },
  });
  return parsed.signals[0]!;
}
