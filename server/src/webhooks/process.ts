import { normalizeProviderStatus, type AirwallexGateway, type ProviderStatus } from "../airwallex/client.js";
import type { DatabasePool } from "../db/pool.js";
import { appendLedgerEvent, type LedgerEventType } from "../ledger/events.js";
import { rebuildLedgerProjections } from "../ledger/projector.js";
import { applySettledCollectionCharge, processSettledDeposit } from "../collections/service.js";
import { bindKit5DepositSettlement, collectKit5SettledDeposit } from "../collections/kit5-repayment-deposit.js";
import type { AirwallexWebhookEvent } from "./inbox.js";

interface InboxRow { provider_event_id: string; event_type: string; raw_body: Buffer; processed_at: Date | null }
interface OperationRow {
  id: string; bridge_id: string | null; operation_type: "bridge" | "fee" | "principal";
  request_id: string; provider_object_id: string | null; amount_cents: string | number;
}

/** Processes one durable inbox event, using provider state rather than delivery order as authority. */
export async function processWebhookEvent(pool: DatabasePool, gateway: AirwallexGateway, eventId: string): Promise<boolean> {
  const inbox = await pool.query<InboxRow>(
    `SELECT provider_event_id, event_type, raw_body, processed_at FROM webhook_inbox WHERE provider_event_id = $1`,
    [eventId],
  );
  const row = inbox.rows[0];
  if (!row) throw new Error("Webhook event was not found in the inbox");
  if (row.processed_at) return false;
  const event = JSON.parse(row.raw_body.toString("utf8")) as AirwallexWebhookEvent;
  const object = event.data?.object ?? {};
  const requestId = stringValue(object.request_id);
  const objectId = stringValue(object.id ?? object.transfer_id ?? object.charge_id);
  const kind = operationKind(row.event_type);

  if (row.event_type === "deposit.settled") {
    const depositId = stringValue(object.id ?? object.deposit_id);
    const accountId = stringValue(object.account_id);
    if (depositId && accountId) {
      const mappedCustomerId = await resolveInternalCustomerId(pool, accountId);
      const deposit = await gateway.getDeposit(depositId, accountId);
      if (deposit.id !== depositId || deposit.currency !== "USD") {
        throw new Error("Provider deposit lookup did not match the USD deposit event");
      }
      if (deposit.status === "settled") {
        if (!deposit.settledAt || !Number.isFinite(deposit.settledAt.getTime())) {
          throw new Error("Provider settled deposit did not include a valid settled_at timestamp");
        }
        const association = await bindKit5DepositSettlement(pool, {
          eventId,
          depositId,
          accountId,
          globalAccountId: stringValue(object.global_account_id) ??
            (typeof object.source === "object" && object.source !== null
              ? stringValue((object.source as Record<string, unknown>).global_account_id)
              : null),
          statementRef: stringValue(object.statement_ref ?? object.reference),
          settledAt: deposit.settledAt,
        });
        if (association) {
          if (association.customerId !== mappedCustomerId || association.accountId !== accountId ||
              association.amountCents !== deposit.amountCents) {
            throw new Error("Settled deposit customer or terms do not match the saved Kit 5 bridge association");
          }
          await collectKit5SettledDeposit(pool, gateway, {
            depositId,
            bridgeId: association.bridgeId,
            customerId: association.customerId,
            accountId,
            amountCents: deposit.amountCents,
            settledAt: deposit.settledAt,
          });
        } else {
          const statementRef = stringValue(object.statement_ref ?? object.reference);
          if (statementRef?.startsWith("kit5-")) {
            throw new Error("Kit 5 deposit reference has no valid saved bridge association");
          }
          await processSettledDeposit(pool, gateway, {
            depositId,
            customerId: mappedCustomerId,
            accountId,
            amountCents: deposit.amountCents,
            settledAt: deposit.settledAt,
          });
        }
      }
    }
  }

  let operation: OperationRow | undefined;
  let currentStatus: ProviderStatus | null = null;
  let confirmed: { id: string; status: ProviderStatus } | null = null;
  if (kind && (requestId || objectId)) {
    const lookup = await pool.query<OperationRow>(
      `SELECT id, bridge_id, operation_type, request_id, provider_object_id, amount_cents
         FROM provider_operations
        WHERE ($1::text IS NOT NULL AND request_id = $1)
           OR ($1::text IS NULL AND $2::text IS NOT NULL AND provider_object_id = $2)`,
      [requestId ?? null, objectId ?? null],
    );
    operation = lookup.rows[0];
    if (operation) {
      const fetched = operation.operation_type === "bridge"
        ? await gateway.getTransferByRequestId(operation.request_id)
        : await gateway.getChargeByRequestId(operation.request_id);
      if (fetched) {
        if (fetched.requestId && fetched.requestId !== operation.request_id) throw new Error("Provider lookup returned a different request ID");
        if (operation.provider_object_id && fetched.id !== operation.provider_object_id) throw new Error("Provider lookup returned a different object ID");
        confirmed = { id: fetched.id, status: normalizeProviderStatus(fetched.status) };
        currentStatus = confirmed.status;
      }
    }
  }

  let settledChargeBalanceCents = 0;
  if (operation && operation.operation_type === "fee" && confirmed?.status === "settled") {
    const attempt = await pool.query<{ account_id: string }>(
      `SELECT account_id FROM collection_attempts WHERE fee_operation_id = $1`,
      [operation.id],
    );
    const accountId = attempt.rows[0]?.account_id;
    if (accountId) {
      const balance = await gateway.getBalance(accountId, "USD");
      if (balance.currency !== "USD" || !Number.isSafeInteger(balance.availableCents) || balance.availableCents < 0) {
        throw new Error("Airwallex returned an invalid USD available balance");
      }
      settledChargeBalanceCents = balance.availableCents;
    }
  }

  const client = await pool.connect();
  try {
    await client.query("BEGIN");
    if (operation) {
      const locked = await client.query<OperationRow>(
        `SELECT id, bridge_id, operation_type, request_id, provider_object_id, amount_cents
           FROM provider_operations
          WHERE id = $1
          FOR UPDATE`,
        [operation.id],
      );
      operation = locked.rows[0];
      if (operation && confirmed) await client.query(
        `UPDATE provider_operations SET provider_object_id = $2, status = $3, updated_at = now() WHERE id = $1`,
        [operation.id, confirmed.id, currentStatus],
      );
      if (operation && operation.operation_type !== "bridge" && operation.bridge_id && confirmed?.status === "settled") {
        await applySettledCollectionCharge(
          client,
          { ...operation, bridge_id: operation.bridge_id!, operation_type: operation.operation_type as "fee" | "principal" },
          event.created_at ? new Date(event.created_at) : new Date(),
          settledChargeBalanceCents,
        );
      }
    }

    await client.query(
      `INSERT INTO provider_observations
         (provider_event_id, provider_operation_id, event_type, provider_status, provider_object_id, request_id, payload)
       VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb)
       ON CONFLICT (provider_event_id) DO NOTHING`,
      [eventId, operation?.id ?? null, row.event_type, currentStatus, confirmed?.id ?? objectId ?? null, requestId, JSON.stringify(object)],
    );

    const ledgerType = operation && confirmed && operation.operation_type === "bridge" && operation.bridge_id
      ? transferLedgerType(currentStatus)
      : null;
    if (ledgerType) {
      await appendLedgerEvent(client, {
        eventType: ledgerType,
        bridgeId: operation!.bridge_id!,
        amountCents: Number(operation!.amount_cents),
        provenance: "provider_observation",
        sourceId: `airwallex-webhook-${eventId}`,
        payload: { requestId: operation!.request_id, providerObjectId: confirmed!.id, status: currentStatus },
        occurredAt: event.created_at ? new Date(event.created_at) : undefined,
      });
      // Failure alone never confirms a return; the existing projector retains its reservation.
      await rebuildLedgerProjections(client);
    }
    await client.query("UPDATE webhook_inbox SET processed_at = now(), processing_at = NULL WHERE provider_event_id = $1 AND processed_at IS NULL", [eventId]);
    await client.query("COMMIT");
    return true;
  } catch (error) {
    await client.query("ROLLBACK");
    throw error;
  } finally {
    client.release();
  }
}

async function resolveInternalCustomerId(pool: Pick<DatabasePool, "query">, accountId: string): Promise<string> {
  if (!accountId.trim()) throw new Error("Settled deposit has an invalid connected-account ID");
  const matches = await pool.query<{ customer_id: string }>(
    `SELECT customer_id FROM customer_roster
      WHERE airwallex_connected_account_id = $1 AND is_active`, [accountId]);
  if (matches.rowCount !== 1) throw new Error("Settled deposit connected account does not resolve to exactly one active customer");
  const customerId = matches.rows[0]?.customer_id;
  if (typeof customerId !== "string" || !customerId.trim()) {
    throw new Error("Settled deposit connected-account binding is malformed");
  }
  return customerId.trim();
}

function stringValue(value: unknown): string | null {
  return typeof value === "string" && value.length ? value : null;
}

function operationKind(eventType: string): "transfer" | "charge" | null {
  if (eventType.startsWith("connected_account_transfer.") || eventType.startsWith("transfer.")) return "transfer";
  if (eventType.startsWith("charge.")) return "charge";
  return null;
}

function transferLedgerType(status: ProviderStatus | null): Extract<LedgerEventType,
  "transfer_pending" | "transfer_suspended" | "transfer_settled" | "transfer_failed"> | null {
  switch (status) {
    case "pending": return "transfer_pending";
    case "suspended": return "transfer_suspended";
    case "settled": return "transfer_settled";
    case "failed": return "transfer_failed";
    default: return null;
  }
}
