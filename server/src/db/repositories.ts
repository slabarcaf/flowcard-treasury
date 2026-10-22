import type { DatabasePool } from "./pool.js";

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
