import type { DatabasePool } from "./pool.js";

export type Kit5AttemptStatus = "accepted" | "submitting" | "in_progress" | "completed" | "ambiguous" | "failed";

export interface Kit5CardAttempt {
  scenarioId: string;
  customerId: string;
  actionId: string;
  sequenceIndex: number;
  lifecycleId: string | null;
  status: Kit5AttemptStatus;
  result: Record<string, unknown> | null;
  error: string | null;
  fundingClaimed: boolean;
  requestFacts: { accountId: string; cardId: string; amountCents: number; currency: string; merchant: string; transactionType: "AUTHORIZATION"; transactionCategory: "PURCHASE"; merchantCategoryCode: string } | null;
  policyStatus: "not_started" | "evaluating" | "pending_review" | "blocked" | "rejected" | "reserving" | "reserved" | "settled" | "retry_submitting" | "retry_ambiguous" | "retry_completed" | "retry_failed";
  policyError: string | null;
  policyDecisionId: string | null;
  pendingPolicyDecisionId: string | null;
  policyActorId: string | null;
  policyApprovalReason: string | null;
  bridgeId: string | null;
  retryLifecycleId: string | null;
  retryRequestId: string | null;
  retryResult: Record<string, unknown> | null;
  retryError: string | null;
}

interface Kit5AttemptRow {
  scenario_id: string;
  customer_id: string;
  action_id: string;
  sequence_index: number;
  lifecycle_id: string | null;
  status: Kit5AttemptStatus;
  provider_result: Record<string, unknown> | null;
  error: string | null;
  funding_claimed: boolean;
  request_facts: Kit5CardAttempt["requestFacts"];
  policy_status: Kit5CardAttempt["policyStatus"];
  policy_error: string | null;
  policy_decision_id: string | null;
  pending_policy_decision_id: string | null;
  policy_actor_id: string | null;
  policy_approval_reason: string | null;
  bridge_id: string | null;
  retry_lifecycle_id: string | null;
  retry_request_id: string | null;
  retry_provider_result: Record<string, unknown> | null;
  retry_error: string | null;
}

function mapKit5Attempt(row: Kit5AttemptRow): Kit5CardAttempt {
  return {
    scenarioId: row.scenario_id,
    customerId: row.customer_id,
    actionId: row.action_id,
    sequenceIndex: row.sequence_index,
    lifecycleId: row.lifecycle_id,
    status: row.status,
    result: row.provider_result,
    error: row.error,
    fundingClaimed: row.funding_claimed,
    requestFacts: row.request_facts,
    policyStatus: row.policy_status ?? "not_started",
    policyError: row.policy_error ?? null,
    policyDecisionId: row.policy_decision_id ?? null,
    pendingPolicyDecisionId: row.pending_policy_decision_id ?? null,
    policyActorId: row.policy_actor_id ?? null,
    policyApprovalReason: row.policy_approval_reason ?? null,
    bridgeId: row.bridge_id ?? null,
    retryLifecycleId: row.retry_lifecycle_id ?? null,
    retryRequestId: row.retry_request_id ?? null,
    retryResult: row.retry_provider_result ?? null,
    retryError: row.retry_error ?? null,
  };
}

export type AcceptKit5AttemptResult =
  | { accepted: true; created: boolean; attempt: Kit5CardAttempt }
  | { accepted: false; reason: "unknown_scenario" | "sequence_complete" | "out_of_order" | "previous_attempt_unresolved" | "action_id_conflict" };

/** Reserve the next scenario action atomically. A repeated action ID returns its original row. */
export async function acceptKit5CardAttempt(
  pool: DatabasePool,
  input: { scenarioId: string; customerId: string; actionId: string; orderedCustomerIds: readonly string[]; requestFacts?: Kit5CardAttempt["requestFacts"] },
): Promise<AcceptKit5AttemptResult> {
  const client = await pool.connect();
  try {
    await client.query("BEGIN");
    await client.query(
      `INSERT INTO kit5_demo_scenario_state (scenario_id, next_customer_index)
       VALUES ($1, 0) ON CONFLICT (scenario_id) DO NOTHING`,
      [input.scenarioId],
    );
    const state = await client.query<{ next_customer_index: number }>(
      `SELECT next_customer_index FROM kit5_demo_scenario_state
        WHERE scenario_id = $1 FOR UPDATE`,
      [input.scenarioId],
    );
    const scenarioState = state.rows[0];
    if (!scenarioState) throw new Error("Kit 5 scenario state was not initialized");

    const prior = await client.query<Kit5AttemptRow>(
      `SELECT scenario_id, customer_id, action_id, sequence_index, lifecycle_id,
              status, provider_result, error, funding_claimed, request_facts,
              policy_status, policy_error, policy_decision_id, bridge_id, retry_lifecycle_id,
              pending_policy_decision_id, policy_actor_id, policy_approval_reason,
              retry_request_id, retry_provider_result, retry_error
         FROM kit5_card_attempts WHERE scenario_id = $1 AND action_id = $2`,
      [input.scenarioId, input.actionId],
    );
    const existing = prior.rows[0];
    if (existing) {
      await client.query("COMMIT");
      if (existing.customer_id !== input.customerId) return { accepted: false, reason: "action_id_conflict" };
      if (!sameRequestFacts(existing.request_facts, input.requestFacts ?? null)) return { accepted: false, reason: "action_id_conflict" };
      return { accepted: true, created: false, attempt: mapKit5Attempt(existing) };
    }

    const index = Number(scenarioState.next_customer_index);
    if (!Number.isSafeInteger(index) || index < 0) throw new Error("Kit 5 scenario state has an invalid customer index");
    if (input.orderedCustomerIds.length === 0) {
      await client.query("ROLLBACK");
      return { accepted: false, reason: "unknown_scenario" };
    }
    if (index >= input.orderedCustomerIds.length) {
      await client.query("ROLLBACK");
      return { accepted: false, reason: "sequence_complete" };
    }
    if (input.orderedCustomerIds[index] !== input.customerId) {
      await client.query("ROLLBACK");
      return { accepted: false, reason: "out_of_order" };
    }
    if (index > 0) {
      const previous = await client.query<{ status: Kit5AttemptStatus; policy_status: Kit5CardAttempt["policyStatus"]; provider_result: Record<string, unknown> | null }>(
        `SELECT status, policy_status, provider_result FROM kit5_card_attempts
          WHERE scenario_id = $1 AND sequence_index = $2`,
        [input.scenarioId, index - 1],
      );
      const priorAttempt = previous.rows[0];
      const fundingPending = priorAttempt?.provider_result?.processResult === "DECLINED" &&
        priorAttempt.provider_result.failureReason === "INSUFFICIENT_FUNDS" &&
        !["blocked", "rejected", "retry_completed", "retry_failed"].includes(priorAttempt.policy_status);
      if (priorAttempt?.status !== "completed" || fundingPending) {
        await client.query("ROLLBACK");
        return { accepted: false, reason: "previous_attempt_unresolved" };
      }
    }
    const inserted = await client.query<Kit5AttemptRow>(
      `INSERT INTO kit5_card_attempts (
         scenario_id, customer_id, action_id, sequence_index, status, request_facts
       ) VALUES ($1, $2, $3, $4, 'accepted', $5::jsonb)
       RETURNING scenario_id, customer_id, action_id, sequence_index, lifecycle_id,
                 status, provider_result, error, funding_claimed, request_facts,
                 policy_status, policy_error, policy_decision_id, bridge_id, retry_lifecycle_id,
                 pending_policy_decision_id, policy_actor_id, policy_approval_reason,
                 retry_request_id, retry_provider_result, retry_error`,
      [input.scenarioId, input.customerId, input.actionId, index, JSON.stringify(input.requestFacts ?? null)],
    );
    const attempt = inserted.rows[0];
    if (!attempt) throw new Error("Kit 5 card attempt insert did not return a row");
    await client.query(
      `UPDATE kit5_demo_scenario_state SET next_customer_index = $2, updated_at = now()
        WHERE scenario_id = $1`,
      [input.scenarioId, index + 1],
    );
    await client.query("COMMIT");
    return { accepted: true, created: true, attempt: mapKit5Attempt(attempt) };
  } catch (error) {
    await client.query("ROLLBACK");
    throw error;
  } finally {
    client.release();
  }
}

function sameRequestFacts(
  saved: Kit5CardAttempt["requestFacts"] | undefined,
  requested: Kit5CardAttempt["requestFacts"],
): boolean {
  if (saved == null || requested === null) return saved == null && requested === null;
  return saved.accountId === requested.accountId && saved.cardId === requested.cardId &&
    saved.amountCents === requested.amountCents && saved.currency === requested.currency &&
    saved.merchant === requested.merchant && saved.transactionType === requested.transactionType &&
    saved.transactionCategory === requested.transactionCategory &&
    saved.merchantCategoryCode === requested.merchantCategoryCode;
}

/** Claim the one permitted provider create. A crash after this claim fails closed. */
export async function claimKit5ProviderCreate(
  pool: Pick<DatabasePool, "query">,
  input: { scenarioId: string; actionId: string },
): Promise<boolean> {
  const result = await pool.query(
    `UPDATE kit5_card_attempts SET status = 'submitting', updated_at = now()
      WHERE scenario_id = $1 AND action_id = $2 AND status = 'accepted'`,
    [input.scenarioId, input.actionId],
  );
  return result.rowCount === 1;
}

/** Claim bridge policy and reservation work once after a completed provider result. */
export async function claimKit5Funding(
  pool: Pick<DatabasePool, "query">,
  input: { scenarioId: string; actionId: string },
): Promise<boolean> {
  const result = await pool.query(
    `UPDATE kit5_card_attempts SET funding_claimed = true, policy_status = 'evaluating', updated_at = now()
      WHERE scenario_id = $1 AND action_id = $2 AND status = 'completed'
        AND funding_claimed = false AND policy_status = 'not_started'`,
    [input.scenarioId, input.actionId],
  );
  return result.rowCount === 1;
}

export async function setKit5PolicyOutcome(
  pool: Pick<DatabasePool, "query">,
  input: { scenarioId: string; actionId: string },
  outcome: { status: "pending_review" | "blocked" | "rejected" | "reserving" | "reserved"; decisionId: string; bridgeId: string | null; pendingDecisionId?: string | null; actorId?: string | null; reason?: string | null; clearApproval?: boolean },
): Promise<void> {
  const result = await pool.query(
    `UPDATE kit5_card_attempts
        SET policy_status = $3, policy_error = NULL, policy_decision_id = $4::uuid, bridge_id = $5,
            pending_policy_decision_id = COALESCE($6::uuid, pending_policy_decision_id),
            policy_actor_id = CASE WHEN $9 THEN NULL ELSE COALESCE($7, policy_actor_id) END,
            policy_approval_reason = CASE WHEN $9 THEN NULL ELSE COALESCE($8, policy_approval_reason) END,
            updated_at = now()
      WHERE scenario_id = $1 AND action_id = $2 AND status = 'completed'
        AND (policy_status IN ('not_started', 'evaluating', 'pending_review', 'blocked') OR
             (policy_status = 'reserving' AND policy_decision_id = $4::uuid) OR
             (policy_status IN ('reserved', 'reserving') AND policy_decision_id = $4::uuid AND bridge_id = $5))`,
    [input.scenarioId, input.actionId, outcome.status, outcome.decisionId, outcome.bridgeId,
      outcome.pendingDecisionId ?? null, outcome.actorId ?? null, outcome.reason ?? null, outcome.clearApproval === true],
  );
  if (result.rowCount !== 1) throw new Error("Kit 5 policy outcome could not be durably linked to its action");
}

export async function setKit5PolicyBlocked(
  pool: Pick<DatabasePool, "query">,
  input: { scenarioId: string; actionId: string },
  reason: string,
): Promise<void> {
  const result = await pool.query(
    `UPDATE kit5_card_attempts SET policy_status = 'blocked', policy_error = $3, updated_at = now()
      WHERE scenario_id = $1 AND action_id = $2 AND status = 'completed'
        AND policy_status IN ('evaluating', 'blocked', 'reserving')`,
    [input.scenarioId, input.actionId, reason.slice(0, 160)],
  );
  if (result.rowCount !== 1) throw new Error("Kit 5 blocked policy state could not be persisted");
}

export async function claimKit5Retry(
  pool: Pick<DatabasePool, "query">,
  input: { scenarioId: string; actionId: string; lifecycleId: string; requestId: string },
): Promise<boolean> {
  const result = await pool.query(
    `UPDATE kit5_card_attempts
        SET policy_status = 'retry_submitting', retry_lifecycle_id = $3::uuid,
            retry_request_id = $4, retry_provider_result = NULL, retry_error = NULL, updated_at = now()
      WHERE scenario_id = $1 AND action_id = $2 AND policy_status = 'settled'
        AND retry_lifecycle_id IS NULL AND retry_request_id IS NULL`,
    [input.scenarioId, input.actionId, input.lifecycleId, input.requestId],
  );
  return result.rowCount === 1;
}

export async function finishKit5Retry(
  pool: Pick<DatabasePool, "query">,
  input: { scenarioId: string; actionId: string; status: "retry_completed" | "retry_ambiguous" | "retry_failed"; result?: Record<string, unknown>; error?: string },
): Promise<void> {
  const updated = await pool.query(
    `UPDATE kit5_card_attempts SET policy_status = $3, retry_provider_result = $4::jsonb,
            retry_error = $5, updated_at = now()
      WHERE scenario_id = $1 AND action_id = $2
        AND policy_status IN ('retry_submitting', 'retry_ambiguous')`,
    [input.scenarioId, input.actionId, input.status, input.result ? JSON.stringify(input.result) : null, input.error ?? null],
  );
  if (updated.rowCount !== 1) throw new Error("Kit 5 retry result could not be durably persisted");
}

export async function setKit5FundingSettled(
  pool: Pick<DatabasePool, "query">,
  input: { scenarioId: string; actionId: string; bridgeId: string },
): Promise<boolean> {
  const result = await pool.query(
    `UPDATE kit5_card_attempts AS attempt SET policy_status = 'settled', updated_at = now()
      WHERE attempt.scenario_id = $1 AND attempt.action_id = $2
        AND attempt.policy_status = 'reserved' AND attempt.bridge_id = $3
        AND EXISTS (SELECT 1 FROM bridge_projections AS projection
          WHERE projection.bridge_id = $3 AND projection.status = 'funded')`,
    [input.scenarioId, input.actionId, input.bridgeId],
  );
  return result.rowCount === 1;
}

export async function setKit5AttemptLifecycle(
  pool: Pick<DatabasePool, "query">,
  input: { scenarioId: string; actionId: string; lifecycleId: string },
): Promise<void> {
  const result = await pool.query(
    `UPDATE kit5_card_attempts
        SET lifecycle_id = $3, status = 'in_progress', updated_at = now()
      WHERE scenario_id = $1 AND action_id = $2 AND status = 'submitting' AND lifecycle_id IS NULL`,
    [input.scenarioId, input.actionId, input.lifecycleId],
  );
  if (result.rowCount !== 1) throw new Error("Kit 5 attempt lifecycle could not be persisted before provider call");
}

export async function finishKit5Attempt(
  pool: Pick<DatabasePool, "query">,
  input: { scenarioId: string; actionId: string; status: Extract<Kit5AttemptStatus, "completed" | "ambiguous" | "failed">; result?: Record<string, unknown>; error?: string },
): Promise<void> {
  const result = await pool.query(
    `UPDATE kit5_card_attempts
        SET status = $3, provider_result = $4::jsonb, error = $5, updated_at = now()
      WHERE scenario_id = $1 AND action_id = $2 AND status IN ('submitting', 'in_progress', 'ambiguous')`,
    [input.scenarioId, input.actionId, input.status, input.result ? JSON.stringify(input.result) : null, input.error ?? null],
  );
  if (result.rowCount !== 1) throw new Error("Kit 5 attempt could not be finalized");
}

export async function getKit5CardAttempt(
  pool: Pick<DatabasePool, "query">,
  scenarioId: string,
  actionId: string,
): Promise<Kit5CardAttempt | null> {
  const result = await pool.query<Kit5AttemptRow>(
    `SELECT scenario_id, customer_id, action_id, sequence_index, lifecycle_id,
            status, provider_result, error, funding_claimed, request_facts,
            policy_status, policy_error, policy_decision_id, bridge_id, retry_lifecycle_id,
            pending_policy_decision_id, policy_actor_id, policy_approval_reason,
            retry_request_id, retry_provider_result, retry_error
       FROM kit5_card_attempts WHERE scenario_id = $1 AND action_id = $2`,
    [scenarioId, actionId],
  );
  return result.rows[0] ? mapKit5Attempt(result.rows[0]) : null;
}

export async function applyMigration(pool: DatabasePool, sql: string): Promise<void> {
  await pool.query(sql);
}

export async function initializeFacilityProjection(pool: DatabasePool): Promise<void> {
  await pool.query(
    `INSERT INTO facility_projections (
       currency, facility_amount_cents, reserve_floor_cents,
       opening_exposure_cents, settled_principal_cents,
       active_reservations_cents, deployable_cents
     )
     SELECT config.currency,
            config.facility_amount_cents,
            (config.facility_amount_cents * config.reserve_floor_bps / 10000)::bigint,
            COALESCE(SUM(event.amount_cents) FILTER (
              WHERE event.event_type = 'opening_exposure'
            ), 0),
            0, 0,
            GREATEST(
              config.facility_amount_cents
              - (config.facility_amount_cents * config.reserve_floor_bps / 10000)::bigint
              - COALESCE(SUM(event.amount_cents) FILTER (
                WHERE event.event_type = 'opening_exposure'
              ), 0),
              0
            )
       FROM facility_configs AS config
       LEFT JOIN ledger_events AS event ON event.currency = config.currency
       WHERE config.is_active
       GROUP BY config.currency, config.facility_amount_cents, config.reserve_floor_bps
       ON CONFLICT (currency) DO NOTHING`,
  );
}

export async function insertWebhookOnce(
  pool: DatabasePool,
  event: { id: string; type: string; rawBody: Uint8Array },
): Promise<boolean> {
  const result = await pool.query<{ id: string }>(
    `INSERT INTO webhook_inbox (provider_event_id, event_type, raw_body)
     VALUES ($1, $2, $3)
     ON CONFLICT (provider_event_id) DO NOTHING
     RETURNING provider_event_id AS id`,
    [event.id, event.type, event.rawBody],
  );
  return result.rowCount === 1;
}

export async function getFacilityProjection(pool: DatabasePool) {
  const result = await pool.query<{
    currency: string;
    facility_amount_cents: string;
    reserve_floor_cents: string;
    opening_exposure_cents: string;
    deployable_cents: string;
  }>(
    `SELECT currency, facility_amount_cents, reserve_floor_cents,
            opening_exposure_cents, deployable_cents
       FROM facility_projections
      WHERE currency = 'USD'`,
  );
  return result.rows[0] ?? null;
}
