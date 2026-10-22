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
  };
}

export type AcceptKit5AttemptResult =
  | { accepted: true; created: boolean; attempt: Kit5CardAttempt }
  | { accepted: false; reason: "unknown_scenario" | "sequence_complete" | "out_of_order" | "previous_attempt_unresolved" | "action_id_conflict" };

/** Reserve the next scenario action atomically. A repeated action ID returns its original row. */
export async function acceptKit5CardAttempt(
  pool: DatabasePool,
  input: { scenarioId: string; customerId: string; actionId: string; orderedCustomerIds: readonly string[] },
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
              status, provider_result, error, funding_claimed
         FROM kit5_card_attempts WHERE scenario_id = $1 AND action_id = $2`,
      [input.scenarioId, input.actionId],
    );
    const existing = prior.rows[0];
    if (existing) {
      await client.query("COMMIT");
      if (existing.customer_id !== input.customerId) return { accepted: false, reason: "action_id_conflict" };
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
      const previous = await client.query<{ status: Kit5AttemptStatus }>(
        `SELECT status FROM kit5_card_attempts
          WHERE scenario_id = $1 AND sequence_index = $2`,
        [input.scenarioId, index - 1],
      );
      if (previous.rows[0]?.status !== "completed") {
        await client.query("ROLLBACK");
        return { accepted: false, reason: "previous_attempt_unresolved" };
      }
    }
    const inserted = await client.query<Kit5AttemptRow>(
      `INSERT INTO kit5_card_attempts (
         scenario_id, customer_id, action_id, sequence_index, status
       ) VALUES ($1, $2, $3, $4, 'accepted')
       RETURNING scenario_id, customer_id, action_id, sequence_index, lifecycle_id,
                 status, provider_result, error, funding_claimed`,
      [input.scenarioId, input.customerId, input.actionId, index],
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
    `UPDATE kit5_card_attempts SET funding_claimed = true, updated_at = now()
      WHERE scenario_id = $1 AND action_id = $2 AND status = 'completed'
        AND funding_claimed = false`,
    [input.scenarioId, input.actionId],
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
            status, provider_result, error, funding_claimed
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
