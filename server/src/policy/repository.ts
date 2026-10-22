import { createHash, randomUUID } from "node:crypto";
import type { DatabasePool } from "../db/pool.js";
import type { DailyPrecheck } from "./precheck.js";

export interface StoredDailyPrecheck extends DailyPrecheck {
  rosterUpdatedAt: Date;
}

/** Append a daily evaluation once for its exact customer and source versions. */
export async function persistDailyPrecheck(pool: DatabasePool, evaluation: StoredDailyPrecheck): Promise<string> {
  const key = policyEvaluationKey(evaluation);
  const evaluationId = randomUUID();
  await pool.query(
    `INSERT INTO policy_evaluations (
       evaluation_id, idempotency_key, customer_id, profile_id, snapshot_id,
       evidence_run_id, policy_version, roster_updated_at, evaluated_at,
       ratings, tier, credit_line_cents, outcome, human_approval_required,
       reason_codes, input_versions, mapping_versions
     ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10::jsonb, $11, $12, $13, $14, $15::jsonb, $16::jsonb, $17::jsonb)
     ON CONFLICT (idempotency_key) DO NOTHING`,
    [evaluationId, key, evaluation.customerId, evaluation.profileId, evaluation.snapshotId,
      evaluation.evidenceRunId, evaluation.policyVersion, evaluation.rosterUpdatedAt,
      evaluation.evaluatedAt, evaluation.ratings === null ? null : JSON.stringify(evaluation.ratings),
      evaluation.tier, evaluation.creditLineCents, evaluation.outcome,
      evaluation.humanApprovalRequired, JSON.stringify(evaluation.reasonCodes),
      JSON.stringify(evaluation.inputVersions), JSON.stringify(evaluation.mappingVersions)],
  );
  const stored = await pool.query<{ evaluation_id: string }>(
    `SELECT evaluation_id FROM policy_evaluations WHERE idempotency_key = $1`, [key],
  );
  const row = stored.rows[0];
  if (!row) throw new Error("Policy evaluation insert was not visible after idempotent write");
  return row.evaluation_id;
}

export function policyEvaluationKey(input: Pick<StoredDailyPrecheck,
  "customerId" | "profileId" | "snapshotId" | "evidenceRunId" | "policyVersion">): string {
  const identity = JSON.stringify([
    input.customerId, input.profileId, input.snapshotId, input.evidenceRunId, input.policyVersion,
  ]);
  return createHash("sha256").update(identity, "utf8").digest("hex");
}
