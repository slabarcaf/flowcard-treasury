import { randomUUID } from "node:crypto";
import type { DatabasePool } from "../db/pool.js";
import { appendLedgerEvent, type LedgerEventType } from "./events.js";
import { rebuildLedgerProjections } from "./projector.js";

export type RepaymentTerm = "15_days" | "30_days" | "fixed_date" | "asap";

export interface ReserveBridgeInput {
  /** Stable UUID supplied by the approved command and reused for retries. */
  bridgeId: string;
  customerId: string;
  amountCents: number;
  repaymentTerm: RepaymentTerm;
  repaymentDueAt?: Date;
}

export interface ReservationResult {
  bridgeId: string;
  providerOperationId: string;
  requestId: string;
  amountCents: number;
}

const UUID_PATTERN = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

/** Atomically reserves USD capacity and queues the corresponding transfer. */
export async function reserveBridge(
  pool: DatabasePool,
  input: ReserveBridgeInput,
): Promise<ReservationResult> {
  if (!UUID_PATTERN.test(input.bridgeId)) throw new Error("bridgeId must be a UUID");
  if (!input.customerId.trim()) throw new Error("customerId is required");
  if (!Number.isSafeInteger(input.amountCents) || input.amountCents <= 0) {
    throw new Error("amountCents must be a positive safe integer");
  }
  if (!["15_days", "30_days", "fixed_date", "asap"].includes(input.repaymentTerm)) {
    throw new Error("Unsupported repayment term");
  }
  if (input.repaymentTerm === "fixed_date" && (!input.repaymentDueAt || !Number.isFinite(input.repaymentDueAt.getTime()))) {
    throw new Error("A valid repaymentDueAt is required for the fixed_date term");
  }
  if (input.repaymentTerm !== "fixed_date" && input.repaymentDueAt !== undefined) {
    throw new Error("repaymentDueAt is only valid for the fixed_date term");
  }

  const client = await pool.connect();
  try {
    await client.query("BEGIN");
    const facilityResult = await client.query<{
      deployable_cents: string | number;
    }>(
      `SELECT deployable_cents
         FROM facility_projections
        WHERE currency = 'USD'
        FOR UPDATE`,
    );
    const facility = facilityResult.rows[0];
    if (!facility) throw new Error("USD facility projection is not initialized");
    const hold = await client.query<{ id: string }>(
      `SELECT id FROM facility_holds WHERE is_active LIMIT 1`,
    );
    if (hold.rowCount) throw new Error("USD facility is on hold pending provider reconciliation");
    const enrollment = await client.query<{ is_active: boolean }>(
      `SELECT is_active FROM customer_roster WHERE customer_id = $1`,
      [input.customerId],
    );
    if (!enrollment.rows[0]?.is_active) {
      throw new Error("Customer is not actively enrolled in the bridge program");
    }
    const deployableCents = Number(facility.deployable_cents);
    if (!Number.isSafeInteger(deployableCents) || deployableCents < 0) {
      throw new Error("USD facility projection has an invalid deployable balance");
    }
    if (input.amountCents > deployableCents) {
      throw new Error(`Insufficient deployable USD facility capacity: ${deployableCents} cents available`);
    }
    const openBridgeResult = await client.query<{ id: string }>(
      `SELECT id
         FROM bridge_cases
        WHERE customer_id = $1
          AND status IN ('reserved', 'pending', 'suspended', 'funded', 'collecting')
        LIMIT 1`,
      [input.customerId],
    );
    if (openBridgeResult.rowCount) {
      throw new Error("Customer already has an open bridge");
    }

    const requestId = `bridge-${input.bridgeId}`;
    const providerOperationId = randomUUID();
    const amount = input.amountCents;
    await client.query(
      `INSERT INTO bridge_cases (
         id, customer_id, amount_cents, currency, repayment_term, repayment_due_at,
         status, request_id
       ) VALUES ($1, $2, $3, 'USD', $4, $5, 'reserved', $6)`,
      [input.bridgeId, input.customerId, amount, input.repaymentTerm, input.repaymentDueAt ?? null, requestId],
    );
    await client.query(
      `INSERT INTO provider_operations (
         id, bridge_id, operation_type, request_id, status,
         amount_cents, currency
       ) VALUES ($1, $2, 'bridge', $3, 'created', $4, 'USD')`,
      [providerOperationId, input.bridgeId, requestId, amount],
    );
    const appended = await appendLedgerEvent(client, {
      eventType: "bridge_reserved",
      bridgeId: input.bridgeId,
      amountCents: amount,
      provenance: "approved_command",
      sourceId: `bridge-reservation-${input.bridgeId}`,
      payload: { customerId: input.customerId, requestId },
    });
    if (!appended) throw new Error("Bridge reservation event already exists");

    await client.query(
      `INSERT INTO outbox_commands (
         provider_operation_id, command_type, payload
       ) VALUES ($1, 'create_bridge_transfer', $2::jsonb)`,
      [
        providerOperationId,
        JSON.stringify({
          bridgeId: input.bridgeId,
          amountCents: amount,
          currency: "USD",
          requestId,
        }),
      ],
    );
    await rebuildLedgerProjections(client);
    await client.query("COMMIT");
    return { bridgeId: input.bridgeId, providerOperationId, requestId, amountCents: amount };
  } catch (error) {
    await client.query("ROLLBACK");
    throw error;
  } finally {
    client.release();
  }
}

/** Records an idempotent provider status observation and updates projections. */
export async function recordTransferObservation(
  pool: DatabasePool,
  input: {
    bridgeId: string;
    eventType: Extract<LedgerEventType, "transfer_pending" | "transfer_suspended" | "transfer_settled" | "transfer_failed">;
    sourceId: string;
    amountCents: number;
  },
): Promise<boolean> {
  return appendTransferEvent(pool, {
    ...input,
    provenance: "provider_observation",
  });
}

/** Releases a failed transfer reservation only after its return is confirmed. */
export async function confirmTransferReturn(
  pool: DatabasePool,
  input: { bridgeId: string; sourceId: string; amountCents: number },
): Promise<boolean> {
  return appendTransferEvent(pool, {
    ...input,
    eventType: "transfer_return_confirmed",
    provenance: "provider_return_confirmation",
  });
}

async function appendTransferEvent(
  pool: DatabasePool,
  input: {
    bridgeId: string;
    eventType: LedgerEventType;
    sourceId: string;
    amountCents: number;
    provenance: string;
  },
): Promise<boolean> {
  const client = await pool.connect();
  try {
    await client.query("BEGIN");
    const locked = await client.query(
      `SELECT currency
         FROM facility_projections
        WHERE currency = 'USD'
        FOR UPDATE`,
    );
    if (locked.rowCount !== 1) throw new Error("USD facility projection is not initialized");
    const appended = await appendLedgerEvent(client, input);
    if (appended) await rebuildLedgerProjections(client);
    await client.query("COMMIT");
    return appended;
  } catch (error) {
    await client.query("ROLLBACK");
    throw error;
  } finally {
    client.release();
  }
}
