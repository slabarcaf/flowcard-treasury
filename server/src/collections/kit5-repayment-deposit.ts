import { createHash, randomUUID } from "node:crypto";
import type { AirwallexGateway, GlobalAccountDepositSimulationResponse } from "../airwallex/client.js";
import type { DatabasePool } from "../db/pool.js";
import { processSettledDeposit } from "./service.js";

const NOVA_DEPOSIT_CENTS = 800_000;

interface DepositOperation {
  id: string;
  scenario_id: string;
  customer_id: string;
  action_id: string;
  bridge_id: string;
  account_id: string;
  global_account_id: string;
  amount_cents: string | number;
  currency: "USD";
  statement_ref: string;
  status: "creating" | "ambiguous" | "created" | "settled" | "blocked";
  provider_deposit_id: string | null;
  provider_result: Record<string, unknown> | null;
  error: string | null;
  settled_at: Date | null;
}

export interface Kit5DepositTarget {
  accountId: string;
  globalAccountId: string;
}

export interface Kit5RepaymentDepositResult {
  status: DepositOperation["status"];
  depositId: string | null;
  providerStatus: string | null;
  bridgeId: string;
  statementRef: string;
  error: string | null;
  collection: Kit5RepaymentCollectionSummary | null;
}

export interface Kit5RepaymentCollectionCharge {
  status: string;
  amountCents: number;
  providerOperationId: string;
  providerObjectId: string | null;
}

export interface Kit5RepaymentCollectionSummary {
  fee: Kit5RepaymentCollectionCharge | null;
  principal: Kit5RepaymentCollectionCharge | null;
}

/**
 * Creates at most one sandbox Nova repayment deposit for a committed, settled Kit 5 bridge.
 * A pre-call claim is durable. An ambiguous claim is never blindly sent a second time.
 */
export async function createKit5RepaymentDeposit(
  pool: DatabasePool,
  gateway: AirwallexGateway,
  input: { scenarioId: string; customerId: string; actionId: string; target: Kit5DepositTarget | null },
): Promise<Kit5RepaymentDepositResult> {
  if (!input.target?.accountId.trim() || !input.target.globalAccountId.trim()) {
    throw new Error("Verified Nova connected-account and USD Global Account mappings are unavailable");
  }
  const client = await pool.connect();
  let operation: DepositOperation;
  let shouldCreate = false;
  try {
    await client.query("BEGIN");
    const approved = await client.query<{
      customer_id: string; bridge_id: string; policy_status: string;
      policy_actor_id: string | null; policy_approval_reason: string | null;
      policy_decision_id: string | null; outcome: string | null;
      retry_request_id: string | null; provider_result: Record<string, unknown> | null;
    }>(
      `SELECT attempt.customer_id, attempt.bridge_id, attempt.policy_status,
              attempt.policy_actor_id, attempt.policy_approval_reason, attempt.policy_decision_id,
              attempt.retry_request_id, attempt.provider_result, decision.outcome
         FROM kit5_card_attempts AS attempt
         LEFT JOIN policy_request_evaluations AS decision ON decision.decision_id = attempt.policy_decision_id
        WHERE attempt.scenario_id = $1 AND attempt.customer_id = $2 AND attempt.action_id = $3
        FOR UPDATE OF attempt`,
      [input.scenarioId, input.customerId, input.actionId],
    );
    const action = approved.rows[0];
    if (!action || !action.bridge_id || !["settled", "retry_completed"].includes(action.policy_status) ||
        !action.policy_actor_id || !action.policy_approval_reason || action.outcome !== "auto_approved") {
      throw new Error("Kit 5 action lacks a committed operator-approved policy decision and settled bridge");
    }
    const rosterBinding = await client.query<{ customer_id: string }>(
      `SELECT customer_id FROM customer_roster
        WHERE customer_id = $1 AND airwallex_connected_account_id = $2 AND is_active`,
      [input.customerId, input.target.accountId]);
    if (rosterBinding.rows.length !== 1 || rosterBinding.rows[0]?.customer_id !== input.customerId) {
      throw new Error("Nova repayment deposit requires exactly one active internal-customer/connected-account binding");
    }
    const retryRequired = action.provider_result?.processResult === "DECLINED" &&
      action.provider_result.failureReason === "INSUFFICIENT_FUNDS";
    if (retryRequired && (!action.retry_request_id || action.policy_status !== "retry_completed")) {
      throw new Error("Kit 5 post-settlement card retry must be durably complete before Nova repayment deposit creation");
    }
    const committedBridge = await client.query<{ ready: boolean }>(
      `SELECT EXISTS (
         SELECT 1 FROM bridge_projections AS projection
         JOIN provider_operations AS operation ON operation.bridge_id = projection.bridge_id
        WHERE projection.bridge_id = $1 AND projection.status = 'funded'
          AND operation.operation_type = 'bridge' AND operation.status = 'settled'
       ) AS ready`, [action.bridge_id]);
    if (committedBridge.rows[0]?.ready !== true) {
      throw new Error("Kit 5 bridge transfer is not provider-confirmed settled");
    }

    const existing = await client.query<DepositOperation>(
      `SELECT id, scenario_id, customer_id, action_id, bridge_id, account_id, global_account_id,
              amount_cents, currency, statement_ref, status, provider_deposit_id, provider_result,
              error, settled_at
         FROM kit5_repayment_deposits WHERE bridge_id = $1 FOR UPDATE`, [action.bridge_id]);
    if (existing.rows[0]) {
      operation = existing.rows[0];
      if (operation.scenario_id !== input.scenarioId || operation.customer_id !== input.customerId ||
          operation.action_id !== input.actionId || operation.account_id !== input.target.accountId ||
          operation.global_account_id !== input.target.globalAccountId) {
        throw new Error("Persisted Kit 5 deposit binding does not match the approved action");
      }
      await client.query("COMMIT");
    } else {
      const statementRef = kit5DepositReference(action.bridge_id, input.actionId);
      const inserted = await client.query<DepositOperation>(
        `INSERT INTO kit5_repayment_deposits (
           id, scenario_id, customer_id, action_id, bridge_id, account_id, global_account_id,
           amount_cents, currency, statement_ref, status
         ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'USD', $9, 'creating')
         ON CONFLICT DO NOTHING
         RETURNING id, scenario_id, customer_id, action_id, bridge_id, account_id, global_account_id,
                   amount_cents, currency, statement_ref, status, provider_deposit_id, provider_result,
                   error, settled_at`,
        [randomUUID(), input.scenarioId, input.customerId, input.actionId, action.bridge_id,
          input.target.accountId, input.target.globalAccountId, NOVA_DEPOSIT_CENTS, statementRef],
      );
      if (!inserted.rows[0]) {
        const raced = await client.query<DepositOperation>(
          `SELECT id, scenario_id, customer_id, action_id, bridge_id, account_id, global_account_id,
                  amount_cents, currency, statement_ref, status, provider_deposit_id, provider_result,
                  error, settled_at FROM kit5_repayment_deposits WHERE bridge_id = $1 FOR UPDATE`,
          [action.bridge_id]);
        if (!raced.rows[0]) throw new Error("Kit 5 deposit claim uniqueness conflict could not be resolved");
        operation = raced.rows[0];
        if (operation.scenario_id !== input.scenarioId || operation.customer_id !== input.customerId ||
            operation.action_id !== input.actionId || operation.account_id !== input.target.accountId ||
            operation.global_account_id !== input.target.globalAccountId) {
          throw new Error("Concurrent Kit 5 deposit claim has a conflicting approved binding");
        }
      } else {
        operation = inserted.rows[0];
        shouldCreate = true;
      }
      await client.query("COMMIT");
    }
  } catch (error) {
    await client.query("ROLLBACK");
    throw error;
  } finally {
    client.release();
  }

  if (shouldCreate) {
    try {
      const result = await gateway.createGlobalAccountDepositSimulation({
        accountId: operation.account_id,
        globalAccountId: operation.global_account_id,
        amountCents: NOVA_DEPOSIT_CENTS,
        statementRef: operation.statement_ref,
      });
      await bindCreateResponse(pool, operation, result);
      operation = { ...operation, status: "created", provider_deposit_id: result.id, provider_result: depositResponse(result) };
    } catch (error) {
      await markDepositAmbiguous(pool, operation, error instanceof Error ? error.message : "provider_create_unresolved");
      operation = { ...operation, status: "ambiguous", error: error instanceof Error ? error.message : "provider_create_unresolved" };
    }
  } else if (["creating", "ambiguous", "created"].includes(operation.status)) {
    if (operation.status === "creating") {
      await markDepositAmbiguous(pool, operation, "provider_create_result_unresolved_reconciliation_required");
    }
    await recoverKit5DepositFromInbox(pool, gateway, operation);
    const refreshed = await pool.query<DepositOperation>(
      `SELECT id, scenario_id, customer_id, action_id, bridge_id, account_id, global_account_id,
              amount_cents, currency, statement_ref, status, provider_deposit_id, provider_result,
              error, settled_at FROM kit5_repayment_deposits WHERE id = $1`, [operation.id]);
    if (refreshed.rows[0]) operation = refreshed.rows[0];
  }
  const latest = await pool.query<DepositOperation>(
    `SELECT id, scenario_id, customer_id, action_id, bridge_id, account_id, global_account_id,
            amount_cents, currency, statement_ref, status, provider_deposit_id, provider_result,
            error, settled_at FROM kit5_repayment_deposits WHERE id = $1`, [operation.id]);
  if (latest.rows[0]) operation = latest.rows[0];
  return mapResult(operation, await loadCollectionSummary(pool, operation));
}

async function loadCollectionSummary(
  pool: DatabasePool,
  operation: DepositOperation,
): Promise<Kit5RepaymentCollectionSummary | null> {
  if (!operation.provider_deposit_id) return null;
  const result = await pool.query<{
    operation_type: "fee" | "principal";
    status: string;
    amount_cents: string | number;
    provider_operation_id: string;
    provider_object_id: string | null;
  }>(
    `SELECT charge.operation_type, charge.status, charge.amount_cents,
            charge.id AS provider_operation_id, charge.provider_object_id
       FROM collection_attempts AS attempt
       JOIN provider_operations AS charge
         ON charge.id IN (attempt.fee_operation_id, attempt.principal_operation_id)
      WHERE attempt.bridge_id = $1 AND attempt.deposit_id = $2
      ORDER BY charge.operation_type`,
    [operation.bridge_id, operation.provider_deposit_id],
  );
  if (operation.status !== "settled" && result.rows.length === 0) return null;
  const summary: Kit5RepaymentCollectionSummary = { fee: null, principal: null };
  for (const row of result.rows) {
    summary[row.operation_type] = {
      status: row.status,
      amountCents: Number(row.amount_cents),
      providerOperationId: row.provider_operation_id,
      providerObjectId: row.provider_object_id,
    };
  }
  return summary;
}

async function recoverKit5DepositFromInbox(
  pool: DatabasePool,
  gateway: AirwallexGateway,
  operation: DepositOperation,
): Promise<void> {
  const events = await pool.query<{ provider_event_id: string }>(
    `SELECT provider_event_id FROM webhook_inbox
      WHERE event_type = 'deposit.settled'
        AND convert_from(raw_body, 'UTF8')::jsonb #>> '{data,object,account_id}' = $1
        AND COALESCE(
          convert_from(raw_body, 'UTF8')::jsonb #>> '{data,object,global_account_id}',
          convert_from(raw_body, 'UTF8')::jsonb #>> '{data,object,source,global_account_id}'
        ) = $2
        AND (
          convert_from(raw_body, 'UTF8')::jsonb #>> '{data,object,id}' = $3
          OR convert_from(raw_body, 'UTF8')::jsonb #>> '{data,object,deposit_id}' = $3
          OR convert_from(raw_body, 'UTF8')::jsonb #>> '{data,object,statement_ref}' = $4
          OR convert_from(raw_body, 'UTF8')::jsonb #>> '{data,object,reference}' = $4
        )
      ORDER BY received_at, provider_event_id`,
    [operation.account_id, operation.global_account_id, operation.provider_deposit_id ?? "", operation.statement_ref]);
  if (events.rows.length === 0) return;
  const { processWebhookEvent } = await import("../webhooks/process.js");
  for (const event of events.rows) await processWebhookEvent(pool, gateway, event.provider_event_id);
}

/** Bind a signed deposit webhook to exactly one saved Kit 5 operation. */
export async function bindKit5DepositSettlement(
  pool: DatabasePool,
  input: { eventId: string; depositId: string; accountId: string; globalAccountId?: string | null; statementRef?: string | null; settledAt: Date },
): Promise<{ customerId: string; bridgeId: string; amountCents: number; accountId: string } | null> {
  if (!input.eventId || !input.depositId || !input.accountId || !input.globalAccountId || !input.globalAccountId.trim() ||
      !Number.isFinite(input.settledAt.getTime())) {
    throw new Error("Signed settled deposit event is missing binding facts");
  }
  const matches = await pool.query<DepositOperation>(
    `SELECT id, scenario_id, customer_id, action_id, bridge_id, account_id, global_account_id,
            amount_cents, currency, statement_ref, status, provider_deposit_id, provider_result,
            error, settled_at
       FROM kit5_repayment_deposits
      WHERE (provider_deposit_id = $1 OR ($2::text IS NOT NULL AND statement_ref = $2))
      ORDER BY created_at, id`,
    [input.depositId, input.statementRef ?? null],
  );
  if (matches.rows.length !== 1) {
    if (matches.rows.length > 1) throw new Error("Settled deposit event matches multiple Kit 5 operations");
    return null;
  }
  const operation = matches.rows[0];
  if (operation?.account_id !== input.accountId || operation.global_account_id !== input.globalAccountId) {
    throw new Error("Settled deposit event conflicts with the saved Kit 5 account or Global Account association");
  }
  if (!operation || Number(operation.amount_cents) !== NOVA_DEPOSIT_CENTS || operation.currency !== "USD") {
    throw new Error("Settled deposit does not match the saved Kit 5 USD repayment terms");
  }
  const client = await pool.connect();
  try {
    await client.query("BEGIN");
    await client.query(
      `INSERT INTO kit5_repayment_deposit_events (provider_event_id, deposit_operation_id)
       VALUES ($1, $2) ON CONFLICT (provider_event_id) DO NOTHING`, [input.eventId, operation.id]);
    const eventBinding = await client.query<{ deposit_operation_id: string }>(
      `SELECT deposit_operation_id FROM kit5_repayment_deposit_events WHERE provider_event_id = $1`, [input.eventId]);
    if (eventBinding.rows[0]?.deposit_operation_id !== operation.id) {
      throw new Error("Signed settlement event is already bound to a different Kit 5 deposit operation");
    }
    const bound = await client.query<DepositOperation>(
      `UPDATE kit5_repayment_deposits SET provider_deposit_id = COALESCE(provider_deposit_id, $2),
              status = 'settled', settled_at = COALESCE(settled_at, $3), updated_at = now(), error = NULL
        WHERE id = $1 AND account_id = $4
          AND (provider_deposit_id IS NULL OR provider_deposit_id = $2)
          AND status IN ('creating', 'ambiguous', 'created', 'settled')
        RETURNING id, scenario_id, customer_id, action_id, bridge_id, account_id, global_account_id,
                  amount_cents, currency, statement_ref, status, provider_deposit_id, provider_result,
                  error, settled_at`,
      [operation.id, input.depositId, input.settledAt, input.accountId],
    );
    if (bound.rowCount !== 1) throw new Error("Settled deposit could not be safely bound to the saved Kit 5 operation");
    const saved = bound.rows[0]!;
    await client.query("COMMIT");
    return { customerId: saved.customer_id, bridgeId: saved.bridge_id, amountCents: Number(saved.amount_cents), accountId: saved.account_id };
  } catch (error) {
    await client.query("ROLLBACK");
    throw error;
  } finally {
    client.release();
  }
}

/** Collect only the bridge explicitly associated with the signed Kit 5 deposit event. */
export async function collectKit5SettledDeposit(
  pool: DatabasePool,
  gateway: AirwallexGateway,
  input: { depositId: string; bridgeId: string; customerId: string; accountId: string; amountCents: number; settledAt: Date },
): Promise<void> {
  await processSettledDeposit(pool, gateway, { ...input, bridgeId: input.bridgeId });
}

function kit5DepositReference(bridgeId: string, actionId: string): string {
  const digest = createHash("sha256").update(`${bridgeId}:${actionId}`).digest("hex").slice(0, 40);
  return `kit5-${digest}`;
}

async function bindCreateResponse(pool: DatabasePool, operation: DepositOperation, result: GlobalAccountDepositSimulationResponse): Promise<void> {
  if (result.amountCents !== NOVA_DEPOSIT_CENTS || result.currency !== "USD" || result.depositType !== "CREDIT") {
    await markDepositAmbiguous(pool, operation, "provider_create_response_terms_mismatch");
    throw new Error("Provider deposit response did not match the saved USD credit terms");
  }
  const updated = await pool.query(
    `UPDATE kit5_repayment_deposits SET provider_deposit_id = $2, status = 'created',
            provider_result = $3::jsonb, error = NULL, updated_at = now()
      WHERE id = $1 AND status IN ('creating', 'ambiguous')
        AND (provider_deposit_id IS NULL OR provider_deposit_id = $2)`,
    [operation.id, result.id, JSON.stringify(depositResponse(result))],
  );
  if (updated.rowCount !== 1) {
    const prior = await pool.query<{ provider_deposit_id: string | null; status: string }>(
      `SELECT provider_deposit_id, status FROM kit5_repayment_deposits WHERE id = $1`, [operation.id]);
    if (prior.rows[0]?.provider_deposit_id !== result.id || !["created", "settled"].includes(prior.rows[0]?.status ?? "")) {
      throw new Error("Kit 5 deposit provider result conflicts with the saved operation");
    }
  }
}

async function markDepositAmbiguous(pool: DatabasePool, operation: DepositOperation, error: string): Promise<void> {
  await pool.query(
    `UPDATE kit5_repayment_deposits SET status = 'ambiguous', error = $2, updated_at = now()
      WHERE id = $1 AND status IN ('creating', 'ambiguous')`,
    [operation.id, error.slice(0, 240)],
  );
}

function depositResponse(result: GlobalAccountDepositSimulationResponse): Record<string, unknown> {
  return { id: result.id, amountCents: result.amountCents, currency: result.currency,
    depositType: result.depositType, feeAmountCents: result.feeAmountCents, status: result.status,
    createTime: result.createTime };
}

function mapResult(
  operation: DepositOperation,
  collection: Kit5RepaymentCollectionSummary | null,
): Kit5RepaymentDepositResult {
  return { status: operation.status, depositId: operation.provider_deposit_id,
    providerStatus: typeof operation.provider_result?.status === "string"
      ? operation.provider_result.status
      : operation.status === "settled" ? "SETTLED" : null,
    bridgeId: operation.bridge_id, statementRef: operation.statement_ref, error: operation.error, collection };
}
