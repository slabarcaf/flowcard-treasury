import { randomUUID } from "node:crypto";
import type { DatabasePool } from "../db/pool.js";
import type { DatabaseClient } from "../db/pool.js";
import { appendLedgerEvent, type LedgerEventType } from "./events.js";
import { rebuildLedgerProjections } from "./projector.js";
import { getCustomerSnapshotReadModel } from "../snapshots/freshness.js";
import type { CurrentBalance, RequestDecision, HumanApprovalAuthorization } from "../policy/decision-service.js";
import type { StoredDailyPrecheck } from "../policy/repository.js";
import { persistDailyPrecheck, persistRequestDecision } from "../policy/repository.js";

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

export interface PolicyReservationResult {
  decision: RequestDecision;
  reservation: ReservationResult | null;
}

export interface PolicyReservationInput extends Omit<ReserveBridgeInput, "bridgeId"> {
  bridgeId: string;
  decisionId: string;
  requestId: string;
  currentBalance: CurrentBalance;
  humanApproval?: HumanApprovalAuthorization;
  signal?: AbortSignal;
  /** Must load and evaluate current profile, evidence, exposure, and door using this transaction. */
  revalidate: (context: {
    client: DatabaseClient;
    requestId: string;
    customerId: string;
    amountCents: number;
    currentBalance: CurrentBalance;
    deployableCents: number;
    snapshotId: string;
    evidenceRunId: string;
    openBridgeCount: number;
    approval?: HumanApprovalAuthorization;
  }) => Promise<{ decision: RequestDecision; precheck: StoredDailyPrecheck }>;
}

const UUID_PATTERN = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

/** Revalidates policy under the facility lock and atomically records evaluation and reservation. */
export async function reservePolicyBridge(
  pool: DatabasePool,
  input: PolicyReservationInput,
): Promise<PolicyReservationResult> {
  if (!UUID_PATTERN.test(input.bridgeId) || !UUID_PATTERN.test(input.decisionId)) {
    throw new Error("bridgeId and decisionId must be UUIDs");
  }
  if (!input.requestId.trim()) throw new Error("requestId is required");
  const client = await pool.connect();
  try {
    assertActive(input.signal);
    await client.query("BEGIN");
    const facilityResult = await client.query<{ deployable_cents: string | number }>(
      `SELECT deployable_cents FROM facility_projections WHERE currency = 'USD' FOR UPDATE`,
    );
    const facility = facilityResult.rows[0];
    if (!facility) throw new Error("USD facility projection is not initialized");
    const deployableCents = Number(facility.deployable_cents);
    if (!Number.isSafeInteger(deployableCents) || deployableCents < 0) {
      throw new Error("USD facility projection has an invalid deployable balance");
    }
    const existing = await client.query<{
      customer_id: string;
      amount_cents: string | number;
      bridge_request_id: string;
      provider_operation_id: string;
    }>(
      `SELECT bridge.customer_id, bridge.amount_cents, bridge.request_id AS bridge_request_id,
              operation.id AS provider_operation_id
         FROM bridge_cases AS bridge
         JOIN provider_operations AS operation ON operation.bridge_id = bridge.id
        WHERE bridge.id = $1 AND operation.operation_type = 'bridge'`,
      [input.bridgeId],
    );
    if (existing.rows[0]) {
      const row = existing.rows[0];
      if (row.customer_id !== input.customerId || Number(row.amount_cents) !== input.amountCents ||
          row.bridge_request_id !== `bridge-${input.bridgeId}`) {
        throw new Error("Reservation retry does not match the original bridge terms");
      }
      const stored = await client.query<{
        request_id: string;
        customer_id: string;
        outcome: RequestDecision["outcome"];
        amount_cents: string | number;
        credit_line_cents: string | number;
        tier: RequestDecision["tier"];
        capital_band: RequestDecision["capitalBand"];
        door: RequestDecision["door"];
        reason_codes: RequestDecision["reasonCodes"];
        rationale: string;
      }>(
        `SELECT request_id, customer_id, outcome, amount_cents, credit_line_cents,
                tier, capital_band, door, reason_codes, rationale
           FROM policy_request_evaluations WHERE decision_id = $1`,
        [input.decisionId],
      );
      const saved = stored.rows[0];
      if (!saved || saved.request_id !== input.requestId || saved.customer_id !== input.customerId) {
        throw new Error("Existing reservation has no matching immutable policy decision");
      }
      await client.query("COMMIT");
      return {
        decision: {
          requestId: saved.request_id,
          customerId: saved.customer_id,
          outcome: saved.outcome,
          amountCents: Number(saved.amount_cents),
          creditLineCents: Number(saved.credit_line_cents),
          tier: saved.tier,
          capitalBand: saved.capital_band,
          door: saved.door,
          reasonCodes: saved.reason_codes,
          rationale: saved.rationale,
        },
        reservation: {
          bridgeId: input.bridgeId,
          providerOperationId: row.provider_operation_id,
          requestId: row.bridge_request_id,
          amountCents: Number(row.amount_cents),
        },
      };
    }
    const hold = await client.query<{ id: string }>("SELECT id FROM facility_holds WHERE is_active LIMIT 1");
    if (hold.rowCount) throw new Error("USD facility is on hold pending provider reconciliation");

    const snapshot = await getCustomerSnapshotReadModel(client, input.customerId, new Date(), true);
    if (!snapshot.active) throw new Error("Customer is not actively enrolled in the bridge program");
    if (snapshot.freshness !== "fresh" || !snapshot.snapshot) {
      throw new Error(`Customer Salesforce snapshot is ${snapshot.freshness}`);
    }
    const runtimeSnapshotMode = process.env.SALESFORCE_SNAPSHOT_MODE?.trim();
    if (runtimeSnapshotMode !== "demo" && runtimeSnapshotMode !== "production_mcp") {
      throw new Error("Salesforce snapshot mode is not configured; bridge reservations are disabled");
    }
    if (snapshot.snapshot.sourceMode !== runtimeSnapshotMode) {
      throw new Error(`Customer Salesforce snapshot source does not match runtime mode ${runtimeSnapshotMode}`);
    }
    const evidence = await client.query<{ extraction_id: string; status: string }>(
      `SELECT extraction_id, status FROM ai_evidence_extractions
        WHERE customer_id = $1 AND snapshot_id = $2
        ORDER BY created_at DESC, extraction_id DESC LIMIT 1 FOR SHARE`,
      [input.customerId, snapshot.snapshot.snapshotId],
    );
    const currentEvidence = evidence.rows[0];
    if (!currentEvidence || !["success", "no_signal"].includes(currentEvidence.status)) {
      throw new Error("Current AI evidence is missing or unresolved");
    }
    const openBridge = await client.query<{ id: string }>(
      `SELECT id FROM bridge_cases WHERE customer_id = $1
        AND status IN ('reserved', 'pending', 'suspended', 'funded', 'collecting') LIMIT 1 FOR UPDATE`,
      [input.customerId],
    );

    const { decision, precheck } = await input.revalidate({
      client,
      requestId: input.requestId,
      customerId: input.customerId,
      amountCents: input.amountCents,
      currentBalance: input.currentBalance,
      deployableCents,
      snapshotId: snapshot.snapshot.snapshotId,
      evidenceRunId: currentEvidence.extraction_id,
      openBridgeCount: openBridge.rowCount ?? 0,
      approval: input.humanApproval,
    });
    assertActive(input.signal);
    if (precheck.customerId !== input.customerId || precheck.snapshotId !== snapshot.snapshot.snapshotId ||
        precheck.evidenceRunId !== currentEvidence.extraction_id) {
      throw new Error("Revalidated policy inputs do not match current snapshot and evidence versions");
    }
    if (decision.requestId !== input.requestId || decision.customerId !== input.customerId) {
      throw new Error("Revalidated policy decision does not match the request");
    }
    const evaluationId = await persistDailyPrecheck(client, precheck);
    await persistRequestDecision(client, {
      decisionId: input.decisionId,
      evaluationId,
      requestId: input.requestId,
      decision,
      actorId: input.humanApproval?.actorId,
      reason: input.humanApproval?.reason,
      evaluatedAt: new Date(),
    });
    if (decision.outcome !== "auto_approved") {
      assertActive(input.signal);
      await client.query("COMMIT");
      return { decision, reservation: null };
    }
    if (decision.amountCents <= 0 || decision.amountCents !== input.amountCents) {
      throw new Error("Revalidated decision amount does not match requested reservation terms");
    }
    if (input.humanApproval && (input.humanApproval.requestId !== input.requestId ||
        input.humanApproval.amountCents !== decision.amountCents || !input.humanApproval.actorId.trim() ||
        !input.humanApproval.reason.trim())) {
      throw new Error("Human approval does not match the revalidated request terms");
    }

    if (openBridge.rowCount) throw new Error("Revalidated policy decision ignored the current open bridge");
    if (decision.amountCents > deployableCents) throw new Error("Insufficient deployable USD facility capacity");

    const requestId = `bridge-${input.bridgeId}`;
    const providerOperationId = randomUUID();
    await client.query(
      `INSERT INTO bridge_cases (
         id, customer_id, amount_cents, currency, repayment_term, repayment_due_at, status, request_id
       ) VALUES ($1, $2, $3, 'USD', $4, $5, 'reserved', $6)`,
      [input.bridgeId, input.customerId, decision.amountCents, input.repaymentTerm, input.repaymentDueAt ?? null, requestId],
    );
    await client.query(
      `INSERT INTO provider_operations (id, bridge_id, operation_type, request_id, status, amount_cents, currency)
       VALUES ($1, $2, 'bridge', $3, 'created', $4, 'USD')`,
      [providerOperationId, input.bridgeId, requestId, decision.amountCents],
    );
    const appended = await appendLedgerEvent(client, {
      eventType: "bridge_reserved",
      bridgeId: input.bridgeId,
      amountCents: decision.amountCents,
      provenance: "approved_command",
      sourceId: `bridge-reservation-${input.bridgeId}`,
      payload: { customerId: input.customerId, requestId, evaluationId, decisionId: input.decisionId },
    });
    if (!appended) throw new Error("Bridge reservation event already exists");
    await client.query(
      `INSERT INTO outbox_commands (provider_operation_id, command_type, payload)
       VALUES ($1, 'create_bridge_transfer', $2::jsonb)`,
      [providerOperationId, JSON.stringify({
        bridgeId: input.bridgeId, amountCents: decision.amountCents, currency: "USD", requestId,
      })],
    );
    await rebuildLedgerProjections(client);
    assertActive(input.signal);
    await client.query("COMMIT");
    return {
      decision,
      reservation: { bridgeId: input.bridgeId, providerOperationId, requestId, amountCents: decision.amountCents },
    };
  } catch (error) {
    await client.query("ROLLBACK");
    throw error;
  } finally {
    client.release();
  }
}

function assertActive(signal: AbortSignal | undefined): void {
  if (signal?.aborted) throw signal.reason instanceof Error ? signal.reason : new Error("Policy reservation was cancelled");
}

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
    const customerSnapshot = await getCustomerSnapshotReadModel(client, input.customerId, new Date(), true);
    if (!customerSnapshot.active) {
      throw new Error("Customer is not actively enrolled in the bridge program");
    }
    if (customerSnapshot.freshness !== "fresh") {
      throw new Error(`Customer Salesforce snapshot is ${customerSnapshot.freshness}`);
    }
    const runtimeSnapshotMode = process.env.SALESFORCE_SNAPSHOT_MODE?.trim();
    if (runtimeSnapshotMode !== "demo" && runtimeSnapshotMode !== "production_mcp") {
      throw new Error("Salesforce snapshot mode is not configured; bridge reservations are disabled");
    }
    if (customerSnapshot.snapshot?.sourceMode !== runtimeSnapshotMode) {
      throw new Error(`Customer Salesforce snapshot source does not match runtime mode ${runtimeSnapshotMode}`);
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
