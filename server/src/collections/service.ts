import { randomUUID } from "node:crypto";
import type { AirwallexGateway } from "../airwallex/client.js";
import type { DatabaseClient, DatabasePool } from "../db/pool.js";
import { appendLedgerEvent } from "../ledger/events.js";
import { rebuildLedgerProjections } from "../ledger/projector.js";
import { calculateBridgeFeeCents } from "./fee.js";

interface BridgeRow {
  id: string;
  customer_id: string;
  amount_cents: string | number;
  repayment_term: "15_days" | "30_days" | "fixed_date" | "asap";
  repayment_due_at: Date | string | null;
  status: string;
}

interface CollectionProjection {
  principal_outstanding_cents: string | number;
  fee_outstanding_cents: string | number;
}

interface PendingChargeRow {
  operation_type: "fee" | "principal";
  pending_cents: string | number;
}

export interface SettledDepositInput {
  depositId: string;
  customerId: string;
  accountId: string;
  amountCents: number;
  settledAt: Date;
}

export interface CollectionAttemptResult {
  created: boolean;
  bridgeId?: string;
  chargedCents?: number;
  chargeOperationId?: string;
}

/** Creates one ASAP collection attempt for a settled deposit, capped to that deposit. */
export async function processSettledDeposit(
  pool: DatabasePool,
  gateway: AirwallexGateway,
  deposit: SettledDepositInput,
): Promise<CollectionAttemptResult> {
  validateDeposit(deposit);
  const balance = await gateway.getBalance(deposit.accountId, "USD");
  if (balance.currency !== "USD" || !Number.isSafeInteger(balance.availableCents) || balance.availableCents < 0) {
    throw new Error("Airwallex returned an invalid USD available balance");
  }

  const client = await pool.connect();
  try {
    await client.query("BEGIN");
    const bridgeResult = await client.query<BridgeRow>(
      `SELECT id, customer_id, amount_cents, repayment_term, repayment_due_at, status
         FROM bridge_cases
        WHERE customer_id = $1 AND status IN ('funded', 'collecting')
        ORDER BY created_at DESC LIMIT 1 FOR UPDATE`,
      [deposit.customerId],
    );
    const bridge = bridgeResult.rows[0];
    if (!bridge) {
      await client.query("COMMIT");
      return { created: false };
    }

    const feeAssessment = await client.query<{ exists: boolean; funded_at: Date | string | null }>(
      `SELECT EXISTS (
         SELECT 1 FROM ledger_events WHERE bridge_id = $1 AND event_type = 'fee_assessed'
       ) AS exists,
       (SELECT occurred_at FROM ledger_events WHERE bridge_id = $1 AND event_type = 'transfer_settled'
         ORDER BY id LIMIT 1) AS funded_at`,
      [bridge.id],
    );
    const assessment = feeAssessment.rows[0];
    if (!assessment?.funded_at) throw new Error("Settled bridge funding timestamp was not found");
    const fundedAt = new Date(assessment.funded_at);
    const dueAt = repaymentDueAt(bridge, fundedAt);
    if (deposit.settledAt < dueAt) {
      await client.query("COMMIT");
      return { created: false, bridgeId: bridge.id };
    }

    const attemptId = randomUUID();
    const inserted = await client.query<{ id: string }>(
      `INSERT INTO collection_attempts (
         id, bridge_id, deposit_id, account_id, deposit_amount_cents,
         available_balance_cents, assessed_fee_cents
       ) VALUES ($1, $2, $3, $4, $5, $6, 0)
       ON CONFLICT (bridge_id, deposit_id) DO NOTHING
       RETURNING id`,
      [attemptId, bridge.id, deposit.depositId, deposit.accountId, deposit.amountCents, balance.availableCents],
    );
    if (inserted.rowCount !== 1) {
      await client.query("COMMIT");
      return { created: false, bridgeId: bridge.id };
    }

    const projectionResult = await client.query<CollectionProjection>(
      `SELECT principal_outstanding_cents, fee_outstanding_cents
         FROM bridge_projections WHERE bridge_id = $1 FOR UPDATE`,
      [bridge.id],
    );
    const projection = projectionResult.rows[0];
    if (!projection) throw new Error("Bridge projection was not found");

    let feeDueCents = Number(projection.fee_outstanding_cents);
    if (!assessment.exists) {
      feeDueCents = calculateBridgeFeeCents({
        principalCents: Number(bridge.amount_cents),
        fundedAt,
        triggeredAt: deposit.settledAt,
      });
      await appendLedgerEvent(client, {
        eventType: "fee_assessed",
        bridgeId: bridge.id,
        amountCents: feeDueCents,
        provenance: "collection_trigger",
        sourceId: `fee-assessed-${bridge.id}`,
        payload: { depositId: deposit.depositId, assessedAt: deposit.settledAt.toISOString() },
        occurredAt: deposit.settledAt,
      });
      await rebuildLedgerProjections(client);
      await client.query(
        `UPDATE collection_attempts SET assessed_fee_cents = $2 WHERE id = $1`,
        [attemptId, feeDueCents],
      );
    }

    const capCents = Math.min(deposit.amountCents, balance.availableCents);
    const principalDueCents = Number(projection.principal_outstanding_cents);
    const pending = await getPendingCharges(client, bridge.id);
    const feeChargeableCents = Math.max(feeDueCents - pending.feeCents, 0);
    const principalChargeableCents = Math.max(principalDueCents - pending.principalCents, 0);
    const firstCharge = feeDueCents > 0
      ? Math.min(feeChargeableCents, capCents)
      : Math.min(principalChargeableCents, capCents);
    let chargeOperationId: string | undefined;
    if (firstCharge > 0) {
      const operationType = feeChargeableCents > 0 ? "fee" : "principal";
      chargeOperationId = await queueCharge(client, {
        bridgeId: bridge.id,
        operationType,
        amountCents: firstCharge,
        depositId: deposit.depositId,
        accountId: deposit.accountId,
      });
      await client.query(
        `UPDATE collection_attempts SET ${operationType}_operation_id = $2 WHERE id = $1`,
        [attemptId, chargeOperationId],
      );
    }
    await client.query("COMMIT");
    return { created: true, bridgeId: bridge.id, chargedCents: firstCharge, chargeOperationId };
  } catch (error) {
    await client.query("ROLLBACK");
    throw error;
  } finally {
    client.release();
  }
}

/** Applies a provider-confirmed settled charge and queues principal only after fee settlement. */
export async function applySettledCollectionCharge(
  client: DatabaseClient,
  operation: { id: string; bridge_id: string; operation_type: "fee" | "principal"; amount_cents: string | number },
  settledAt: Date,
  availableBalanceCents: number,
): Promise<void> {
  const attemptResult = await client.query<{
    id: string;
    deposit_id: string;
    account_id: string;
    deposit_amount_cents: string | number;
    available_balance_cents: string | number;
    fee_operation_id: string | null;
  }>(
    `SELECT id, deposit_id, account_id, deposit_amount_cents, available_balance_cents, fee_operation_id
       FROM collection_attempts
      WHERE fee_operation_id = $1 OR principal_operation_id = $1
      FOR UPDATE`,
    [operation.id],
  );
  const attempt = attemptResult.rows[0];
  if (!attempt) return;
  const eventType = operation.operation_type === "fee" ? "fee_repaid" : "principal_repaid";
  const appended = await appendLedgerEvent(client, {
    eventType,
    bridgeId: operation.bridge_id,
    amountCents: Number(operation.amount_cents),
    provenance: "provider_charge_settled",
    sourceId: `collection-charge-settled-${operation.id}`,
    payload: { depositId: attempt.deposit_id, operationId: operation.id },
    occurredAt: settledAt,
  });
  if (!appended) return;
  await rebuildLedgerProjections(client);
  if (operation.operation_type !== "fee") return;

  const attemptCap = Math.min(Number(attempt.deposit_amount_cents), Number(attempt.available_balance_cents));
  const principalCap = Math.max(attemptCap - Number(operation.amount_cents), 0);
  if (!principalCap) return;
  const principalResult = await client.query<CollectionProjection>(
    `SELECT principal_outstanding_cents, fee_outstanding_cents
       FROM bridge_projections WHERE bridge_id = $1`,
    [operation.bridge_id],
  );
  const projection = principalResult.rows[0];
  if (!projection || Number(projection.fee_outstanding_cents) > 0) return;
  const pending = await getPendingCharges(client, operation.bridge_id);
  const chargeablePrincipal = Math.max(Number(projection.principal_outstanding_cents) - pending.principalCents, 0);
  const amountCents = Math.min(principalCap, availableBalanceCents, chargeablePrincipal);
  if (amountCents <= 0) return;
  const principalOperationId = await queueCharge(client, {
    bridgeId: operation.bridge_id,
    operationType: "principal",
    amountCents,
    depositId: attempt.deposit_id,
    accountId: attempt.account_id,
  });
  await client.query(
    `UPDATE collection_attempts SET principal_operation_id = $2 WHERE id = $1 AND principal_operation_id IS NULL`,
    [attempt.id, principalOperationId],
  );
}

async function getPendingCharges(client: DatabaseClient, bridgeId: string): Promise<{ feeCents: number; principalCents: number }> {
  const result = await client.query<PendingChargeRow>(
    `SELECT operation_type, COALESCE(SUM(amount_cents), 0) AS pending_cents
       FROM provider_operations
      WHERE bridge_id = $1 AND operation_type IN ('fee', 'principal')
        AND status NOT IN ('settled', 'failed')
        AND NOT EXISTS (
          SELECT 1 FROM outbox_commands AS command
           WHERE command.provider_operation_id = provider_operations.id
             AND command.status = 'failed'
        )
      GROUP BY operation_type`,
    [bridgeId],
  );
  return result.rows.reduce((pending, row) => {
    if (row.operation_type === "fee") pending.feeCents = Number(row.pending_cents);
    else pending.principalCents = Number(row.pending_cents);
    return pending;
  }, { feeCents: 0, principalCents: 0 });
}

async function queueCharge(
  client: DatabaseClient,
  input: { bridgeId: string; operationType: "fee" | "principal"; amountCents: number; depositId: string; accountId: string },
): Promise<string> {
  const id = randomUUID();
  const requestId = `${input.operationType}-${randomUUID()}`;
  await client.query(
    `INSERT INTO provider_operations (id, bridge_id, operation_type, request_id, status, amount_cents, currency)
     VALUES ($1, $2, $3, $4, 'created', $5, 'USD')`,
    [id, input.bridgeId, input.operationType, requestId, input.amountCents],
  );
  await client.query(
    `INSERT INTO outbox_commands (provider_operation_id, command_type, payload)
     VALUES ($1, 'create_collection_charge', $2::jsonb)`,
    [id, JSON.stringify({
      source: input.accountId,
      reason: input.operationType === "fee" ? "professional_business_services" : "loan_credit_repayment",
      reference: input.operationType === "fee" ? `Bridge fee ${input.bridgeId}` : `Bridge repayment ${input.depositId}`,
    })],
  );
  return id;
}

function validateDeposit(deposit: SettledDepositInput): void {
  if (!deposit.depositId.trim() || !deposit.customerId.trim() || !deposit.accountId.trim()) {
    throw new Error("Deposit, customer, and account IDs are required");
  }
  if (!Number.isSafeInteger(deposit.amountCents) || deposit.amountCents <= 0) {
    throw new Error("Settled deposit amount must be a positive safe integer");
  }
  if (!Number.isFinite(deposit.settledAt.getTime())) throw new Error("Deposit settlement timestamp is invalid");
}

function repaymentDueAt(bridge: BridgeRow, fundedAt: Date): Date {
  switch (bridge.repayment_term) {
    case "asap":
      return fundedAt;
    case "15_days":
      return new Date(fundedAt.getTime() + 15 * 24 * 60 * 60 * 1000);
    case "30_days":
      return new Date(fundedAt.getTime() + 30 * 24 * 60 * 60 * 1000);
    case "fixed_date": {
      if (!bridge.repayment_due_at) throw new Error("Fixed-date bridge is missing repayment_due_at");
      const dueAt = new Date(bridge.repayment_due_at);
      if (!Number.isFinite(dueAt.getTime())) throw new Error("Fixed repayment date is invalid");
      return dueAt;
    }
  }
}
