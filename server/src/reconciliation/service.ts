import { normalizeProviderStatus, type AirwallexGateway, type ProviderOperationResponse } from "../airwallex/client.js";
import type { DatabasePool } from "../db/pool.js";
import { appendLedgerEvent } from "../ledger/events.js";
import { rebuildLedgerProjections } from "../ledger/projector.js";

interface UnresolvedOperation {
  id: string;
  bridge_id: string | null;
  operation_type: "bridge" | "fee" | "principal";
  request_id: string;
  provider_object_id: string | null;
  status: string;
  amount_cents: string | number;
  outbox_status: string | null;
}

export interface ReconciliationResult {
  scanned: number;
  recovered: number;
  held: number;
  holdReleased: boolean;
}

/** Reconciles open provider operations by their durable request ID. */
export async function reconcileProviderOperations(
  pool: DatabasePool,
  gateway: AirwallexGateway,
): Promise<ReconciliationResult> {
  const result = await pool.query<UnresolvedOperation>(
    `SELECT operation.id, operation.bridge_id, operation.operation_type,
            operation.request_id, operation.provider_object_id, operation.status,
            operation.amount_cents, outbox.status AS outbox_status
       FROM provider_operations AS operation
       LEFT JOIN outbox_commands AS outbox ON outbox.provider_operation_id = operation.id
      WHERE operation.status NOT IN ('settled', 'failed')
         OR (operation.operation_type = 'bridge' AND (
           (operation.status = 'pending' AND NOT EXISTS (
             SELECT 1 FROM ledger_events event
              WHERE event.bridge_id = operation.bridge_id AND event.event_type = 'transfer_pending'
           )) OR
           (operation.status = 'suspended' AND NOT EXISTS (
             SELECT 1 FROM ledger_events event
              WHERE event.bridge_id = operation.bridge_id AND event.event_type = 'transfer_suspended'
           )) OR
           (operation.status = 'settled' AND NOT EXISTS (
             SELECT 1 FROM ledger_events event
              WHERE event.bridge_id = operation.bridge_id AND event.event_type = 'transfer_settled'
           )) OR
           (operation.status = 'failed' AND NOT EXISTS (
             SELECT 1 FROM ledger_events event
              WHERE event.bridge_id = operation.bridge_id AND event.event_type = 'transfer_failed'
           ))
         ))
      ORDER BY operation.created_at, operation.id`,
  );
  let recovered = 0;
  let held = 0;
  let scanComplete = true;
  for (const operation of result.rows) {
    let observed: ProviderOperationResponse | null;
    try {
      observed = operation.operation_type === "bridge"
        ? await gateway.getTransferByRequestId(operation.request_id)
        : await gateway.getChargeByRequestId(operation.request_id);
    } catch (error) {
      scanComplete = false;
      if (operation.status !== "created" || operation.outbox_status === "done" || operation.outbox_status === "failed") {
        await placeHold(pool, operation, "provider_lookup_failed", { message: safeMessage(error) });
        held++;
      }
      continue;
    }

    if (!observed) {
      // A queued command with no provider object is expected. Once a command
      // was submitted or the ledger recorded a status, absence is unexplained.
      if (operation.status !== "created" || operation.outbox_status === "done" || operation.outbox_status === "failed") {
        await placeHold(pool, operation, "provider_operation_missing", { status: operation.status, outboxStatus: operation.outbox_status });
        held++;
      }
      continue;
    }

    const mismatch = mismatchReason(operation, observed);
    if (mismatch) {
      await placeHold(pool, operation, mismatch.reason, mismatch.details);
      held++;
      continue;
    }

    const status = normalizeProviderStatus(observed.status);
    const client = await pool.connect();
    try {
      await client.query("BEGIN");
      const locked = await client.query<UnresolvedOperation>(
        `SELECT id, bridge_id, operation_type, request_id, provider_object_id,
                status, amount_cents, NULL::text AS outbox_status
           FROM provider_operations WHERE id = $1 FOR UPDATE`,
        [operation.id],
      );
      const current = locked.rows[0];
      if (!current) throw new Error("Provider operation disappeared during reconciliation");
      if (current.provider_object_id && current.provider_object_id !== observed.id) {
        await client.query("ROLLBACK");
        await placeHold(pool, operation, "provider_object_id_changed", { expected: current.provider_object_id, observed: observed.id });
        held++;
        continue;
      }
      const sourceId = `reconciliation-${operation.id}-${status}-${observed.id}`;
      await client.query(
        `INSERT INTO reconciliation_observations
           (provider_operation_id, request_id, provider_object_id, provider_status, payload, source_id)
         VALUES ($1, $2, $3, $4, $5::jsonb, $6)
         ON CONFLICT (source_id) DO NOTHING`,
        [operation.id, operation.request_id, observed.id, status, JSON.stringify(observed), sourceId],
      );
      await client.query(
        `UPDATE provider_operations SET provider_object_id = $2, status = $3, updated_at = now() WHERE id = $1`,
        [operation.id, observed.id, status],
      );
      if (operation.operation_type === "bridge" && operation.bridge_id) {
        const eventType = transferEvent(status);
        if (eventType) {
          await appendLedgerEvent(client, {
            eventType,
            bridgeId: operation.bridge_id,
            amountCents: Number(operation.amount_cents),
            provenance: "provider_reconciliation",
            sourceId: `reconciliation-event-${operation.id}-${status}-${observed.id}`,
            payload: { requestId: operation.request_id, providerObjectId: observed.id, status },
          });
          await rebuildLedgerProjections(client);
        }
      }
      await client.query("COMMIT");
      recovered++;
    } catch (error) {
      await client.query("ROLLBACK");
      throw error;
    } finally {
      client.release();
    }
  }

  let holdReleased = false;
  if (scanComplete && held === 0) {
    const client = await pool.connect();
    try {
      await client.query("BEGIN");
      await client.query(`SELECT currency FROM facility_projections WHERE currency = 'USD' FOR UPDATE`);
      const released = await client.query(
        `UPDATE facility_holds SET is_active = false, resolved_at = now()
          WHERE is_active`,
      );
      await client.query("COMMIT");
      holdReleased = Boolean(released.rowCount);
    } catch (error) {
      await client.query("ROLLBACK");
      throw error;
    } finally {
      client.release();
    }
  }
  return { scanned: result.rows.length, recovered, held, holdReleased };
}

function mismatchReason(operation: UnresolvedOperation, response: ProviderOperationResponse) {
  if (response.requestId && response.requestId !== operation.request_id) {
    return { reason: "request_id_mismatch", details: { expected: operation.request_id, observed: response.requestId } };
  }
  if (operation.provider_object_id && response.id !== operation.provider_object_id) {
    return { reason: "provider_object_id_changed", details: { expected: operation.provider_object_id, observed: response.id } };
  }
  if (normalizeProviderStatus(response.status) === "unknown") {
    return { reason: "provider_status_unrecognized", details: { observed: response.rawStatus ?? response.status } };
  }
  return null;
}

async function placeHold(pool: DatabasePool, operation: UnresolvedOperation, reason: string, details: Record<string, unknown>) {
  const client = await pool.connect();
  try {
    await client.query("BEGIN");
    await client.query(`SELECT currency FROM facility_projections WHERE currency = 'USD' FOR UPDATE`);
    await client.query(
      `INSERT INTO facility_holds (provider_operation_id, request_id, reason, mismatch)
       VALUES ($1, $2, $3, $4::jsonb)
       ON CONFLICT (provider_operation_id, reason) WHERE is_active DO NOTHING`,
      [operation.id, operation.request_id, reason, JSON.stringify(details)],
    );
    await client.query("COMMIT");
  } catch (error) {
    await client.query("ROLLBACK");
    throw error;
  } finally {
    client.release();
  }
}

function transferEvent(status: string): "transfer_pending" | "transfer_suspended" | "transfer_settled" | "transfer_failed" | null {
  switch (status) {
    case "pending": return "transfer_pending";
    case "suspended": return "transfer_suspended";
    case "settled": return "transfer_settled";
    case "failed": return "transfer_failed";
    default: return null;
  }
}

function safeMessage(error: unknown): string {
  const message = error instanceof Error ? error.message : String(error);
  return message.replace(/Bearer\s+[^\s,;]+/gi, "Bearer [redacted]").slice(0, 300);
}
