import type { DatabaseClient } from "../db/pool.js";

export type LedgerEventType =
  | "opening_exposure"
  | "bridge_reserved"
  | "transfer_pending"
  | "transfer_suspended"
  | "transfer_settled"
  | "transfer_failed"
  | "transfer_return_confirmed"
  | "fee_assessed"
  | "fee_repaid"
  | "principal_repaid";

export interface LedgerEventInput {
  eventType: LedgerEventType;
  bridgeId?: string;
  amountCents: number;
  provenance: string;
  sourceId: string;
  payload?: Record<string, unknown>;
  occurredAt?: Date;
}

export interface LedgerEventRecord {
  id: number;
  event_type: LedgerEventType;
  bridge_id: string | null;
  amount_cents: string | number;
  currency: "USD";
  provenance: string;
  source_id: string | null;
  payload: Record<string, unknown>;
  occurred_at: Date | string;
}

/** Appends one immutable ledger event, returning false for a repeated source ID. */
export async function appendLedgerEvent(
  client: Pick<DatabaseClient, "query">,
  event: LedgerEventInput,
): Promise<boolean> {
  if (!Number.isSafeInteger(event.amountCents) || event.amountCents < 0) {
    throw new Error("Ledger event amount must be a non-negative safe integer");
  }
  if (!event.sourceId.trim()) throw new Error("Ledger event source ID is required");
  const result = await client.query<{ id: string }>(
    `INSERT INTO ledger_events (
       event_type, bridge_id, amount_cents, currency, provenance, source_id,
       payload, occurred_at
     ) VALUES ($1, $2, $3, 'USD', $4, $5, $6::jsonb, $7)
     ON CONFLICT (source_id) DO NOTHING
     RETURNING id`,
    [
      event.eventType,
      event.bridgeId ?? null,
      event.amountCents,
      event.provenance,
      event.sourceId,
      JSON.stringify(event.payload ?? {}),
      event.occurredAt ?? new Date(),
    ],
  );
  return result.rowCount === 1;
}
