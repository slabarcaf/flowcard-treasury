import type { DatabasePool } from "../db/pool.js";

export interface AirwallexWebhookEvent {
  id: string;
  name: string;
  accountId?: string;
  account_id?: string;
  data?: Record<string, unknown>;
  created_at?: string;
}

export const AIRWALLEX_WEBHOOK_EVENTS = [
  "connected_account_transfer.new",
  "connected_account_transfer.pending",
  "connected_account_transfer.settled",
  "connected_account_transfer.suspended",
  "connected_account_transfer.failed",
  "charge.new",
  "charge.pending",
  "charge.settled",
  "charge.suspended",
  "charge.failed",
  "deposit.settled",
  "issuing.card_transaction.declined",
] as const;

export function isOnboardingWebhookEvent(eventType: string): boolean {
  return eventType.startsWith("account.") || eventType.startsWith("rfi.");
}

/** Stores verified bytes once. The route acknowledges only after this durable insert completes. */
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
  const isNew = result.rowCount === 1;
  if (isNew && isOnboardingWebhookEvent(event.type)) {
    await pool.query(
      `INSERT INTO onboarding_webhook_forwarding (provider_event_id, event_type)
       VALUES ($1, $2)
       ON CONFLICT (provider_event_id) DO NOTHING`,
      [event.id, event.type],
    );
  }
  return isNew;
}
