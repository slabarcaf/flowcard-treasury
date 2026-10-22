import { createHash, randomUUID } from "node:crypto";
import type { DatabaseClient, DatabasePool } from "../db/pool.js";
import type { DailyPrecheck } from "./precheck.js";

export interface StoredDailyPrecheck extends DailyPrecheck {
  rosterUpdatedAt: Date;
}

export interface StoredRequestDecision {
  decisionId: string;
  evaluationId: string;
  requestId: string;
  decision: {
    customerId: string;
    outcome: "auto_approved" | "human_review" | "declined" | "blocked";
    amountCents: number;
    creditLineCents: number;
    tier: "A" | "B" | "C" | "none";
    capitalBand: "a_lot" | "normal" | "less" | "critical" | "at_floor" | null;
    door: "auto" | "human" | "closed" | null;
    reasonCodes: string[];
    rationale: string;
  };
  actorId?: string;
  reason?: string;
  evaluatedAt: Date;
}

/** Append the request-time decision in the caller's ledger transaction. */
export async function persistRequestDecision(
  client: DatabaseClient,
  evaluation: StoredRequestDecision,
): Promise<void> {
  await client.query(
    `INSERT INTO policy_request_evaluations (
       decision_id, evaluation_id, request_id, customer_id, outcome,
       amount_cents, credit_line_cents, tier, capital_band, door,
       reason_codes, rationale, actor_id, approval_reason, evaluated_at
     ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11::jsonb, $12, $13, $14, $15)
     ON CONFLICT (decision_id) DO NOTHING`,
    [evaluation.decisionId, evaluation.evaluationId, evaluation.requestId,
      evaluation.decision.customerId, evaluation.decision.outcome,
      evaluation.decision.amountCents, evaluation.decision.creditLineCents, evaluation.decision.tier,
      evaluation.decision.capitalBand, evaluation.decision.door,
      JSON.stringify(evaluation.decision.reasonCodes), evaluation.decision.rationale,
      evaluation.actorId ?? null, evaluation.reason ?? null, evaluation.evaluatedAt],
  );
  const stored = await client.query<{
    evaluation_id: string;
    request_id: string;
    customer_id: string;
    outcome: StoredRequestDecision["decision"]["outcome"];
    amount_cents: string | number;
    credit_line_cents: string | number;
    tier: StoredRequestDecision["decision"]["tier"];
    capital_band: StoredRequestDecision["decision"]["capitalBand"];
    door: StoredRequestDecision["decision"]["door"];
    reason_codes: string[];
    rationale: string;
    actor_id: string | null;
    approval_reason: string | null;
  }>(
    `SELECT evaluation_id, request_id, customer_id, outcome, amount_cents,
            credit_line_cents, tier, capital_band, door, reason_codes,
            rationale, actor_id, approval_reason
       FROM policy_request_evaluations WHERE decision_id = $1`,
    [evaluation.decisionId],
  );
  const row = stored.rows[0];
  if (!row || row.evaluation_id !== evaluation.evaluationId || row.request_id !== evaluation.requestId ||
      row.customer_id !== evaluation.decision.customerId || row.outcome !== evaluation.decision.outcome ||
      Number(row.amount_cents) !== evaluation.decision.amountCents ||
      Number(row.credit_line_cents) !== evaluation.decision.creditLineCents ||
      row.tier !== evaluation.decision.tier || row.capital_band !== evaluation.decision.capitalBand ||
      row.door !== evaluation.decision.door ||
      JSON.stringify(row.reason_codes) !== JSON.stringify(evaluation.decision.reasonCodes) ||
      row.rationale !== evaluation.decision.rationale || row.actor_id !== (evaluation.actorId ?? null) ||
      row.approval_reason !== (evaluation.reason ?? null)) {
    throw new Error("Request decision ID was already used for different immutable policy results");
  }
}

/** Append a daily evaluation once for its exact customer and source versions. */
export async function persistDailyPrecheck(pool: DatabasePool | DatabaseClient, evaluation: StoredDailyPrecheck): Promise<string> {
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
