import { createHash } from "node:crypto";
import type { AirwallexRemoteAuthorizationV2 } from "../http/routes/remote-authorization.js";
import type { DatabasePool } from "../db/pool.js";
import { getCurrentUnderwritingProfile } from "../underwriting/repository.js";
import { getCustomerSnapshotReadModel } from "../snapshots/freshness.js";
import type { RequestPolicyState } from "./decision-service.js";
import type { StoredDailyPrecheck } from "./repository.js";
import { persistRequestDecision } from "./repository.js";
import type { RequestDecision } from "./decision-service.js";

interface EvaluationRow {
  evaluation_id: string;
  customer_id: string;
  policy_version: string;
  profile_id: string | null;
  snapshot_id: string | null;
  evidence_run_id: string | null;
  evaluated_at: Date;
  ratings: StoredDailyPrecheck["ratings"];
  tier: StoredDailyPrecheck["tier"];
  credit_line_cents: number | string;
  outcome: StoredDailyPrecheck["outcome"];
  human_approval_required: boolean;
  reason_codes: string[];
  input_versions: Record<string, string | number | null>;
  mapping_versions: Record<string, string>;
  roster_updated_at: Date;
}

interface RequestFacts {
  customerId: string;
  accountId: string;
  profile: NonNullable<Awaited<ReturnType<typeof getCurrentUnderwritingProfile>>["profile"]>;
  precheck: StoredDailyPrecheck;
  evaluationId: string;
}

export function toBridgeRequest(request: AirwallexRemoteAuthorizationV2, customerId: string) {
  return {
    requestId: request.cardTransactionEventId,
    customerId,
    transactionId: request.cardTransactionId,
    transactionAmountCents: Math.round(request.transactionAmount * 100),
    currency: request.transactionCurrency,
    spendCategory: "other_business",
    traceable: true,
  };
}

export async function loadRequestFacts(
  pool: Pick<DatabasePool, "query">,
  customerId: string,
  request: AirwallexRemoteAuthorizationV2,
  now: Date,
): Promise<RequestFacts | null> {
  const result = await pool.query<EvaluationRow & { airwallex_connected_account_id: string }>(
    `SELECT evaluation.evaluation_id, evaluation.customer_id, evaluation.policy_version,
            evaluation.profile_id, evaluation.snapshot_id, evaluation.evidence_run_id,
            evaluation.evaluated_at, evaluation.ratings, evaluation.tier,
            evaluation.credit_line_cents, evaluation.outcome,
            evaluation.human_approval_required, evaluation.reason_codes,
            evaluation.input_versions, evaluation.mapping_versions,
            evaluation.roster_updated_at, roster.airwallex_connected_account_id
       FROM policy_evaluations AS evaluation
       JOIN customer_roster AS roster ON roster.customer_id = evaluation.customer_id
       JOIN issuing_card_bindings AS card ON card.customer_id = roster.customer_id
      WHERE evaluation.customer_id = $1 AND card.is_active AND roster.is_active
        AND card.card_id = $2 AND card.airwallex_account_id = $3
        AND roster.airwallex_connected_account_id = $3
        AND evaluation.outcome IN ('ready', 'ineligible', 'blocked')
      ORDER BY evaluation.evaluated_at DESC, evaluation.evaluation_id DESC LIMIT 1`,
    [customerId, request.cardId, request.accountId],
  );
  const row = result.rows[0];
  if (!row || !row.profile_id || !row.snapshot_id || !row.evidence_run_id) return null;
  const profileRead = await getCurrentUnderwritingProfile(pool, customerId, now);
  if (!profileRead.profile || profileRead.freshness !== "fresh" || profileRead.profile.profileId !== row.profile_id) return null;
  const snapshotRead = await getCustomerSnapshotReadModel(pool, customerId, now);
  if (!snapshotRead.active || snapshotRead.freshness !== "fresh" || snapshotRead.snapshot?.snapshotId !== row.snapshot_id) return null;
  const evidence = await pool.query<{ extraction_id: string; status: string }>(
    `SELECT extraction_id, status FROM ai_evidence_extractions
      WHERE customer_id = $1 AND snapshot_id = $2
      ORDER BY created_at DESC, extraction_id DESC LIMIT 1`, [customerId, row.snapshot_id]);
  const evidenceRow = evidence.rows[0];
  if (!evidenceRow || evidenceRow.extraction_id !== row.evidence_run_id || !["success", "no_signal"].includes(evidenceRow.status)) return null;
  const accountId = row.airwallex_connected_account_id;
  if (!accountId) return null;
  const precheck: StoredDailyPrecheck = {
    customerId: row.customer_id,
    policyVersion: row.policy_version,
    profileId: row.profile_id,
    snapshotId: row.snapshot_id,
    evidenceRunId: row.evidence_run_id,
    ratings: row.ratings,
    tier: row.tier,
    creditLineCents: Number(row.credit_line_cents),
    outcome: row.outcome,
    humanApprovalRequired: row.human_approval_required,
    reasonCodes: row.reason_codes,
    inputVersions: row.input_versions,
    mappingVersions: row.mapping_versions,
    rosterUpdatedAt: row.roster_updated_at,
    evaluatedAt: row.evaluated_at,
  };
  return { customerId, accountId, profile: profileRead.profile, precheck, evaluationId: row.evaluation_id };
}

export async function loadPolicyState(
  pool: Pick<DatabasePool, "query">,
  current: RequestFacts,
  request: AirwallexRemoteAuthorizationV2,
  transactionDeployableCents?: number,
  transactionOpenBridgeCount?: number,
): Promise<RequestPolicyState | null> {
  const [roster, facility, exposure] = await Promise.all([
    pool.query<{ airwallex_connected_account_id: string }>(
      `SELECT airwallex_connected_account_id FROM customer_roster
        WHERE customer_id = $1 AND is_active`, [current.customerId]),
    pool.query<{ facility_amount_cents: number | string; reserve_floor_cents: number | string; deployable_cents: number | string; ledger_sequence: number | string }>(
      `SELECT facility_amount_cents, reserve_floor_cents, deployable_cents,
              (SELECT COALESCE(MAX(id), 0) FROM ledger_events WHERE currency = 'USD') AS ledger_sequence
         FROM facility_projections WHERE currency = 'USD'`),
    pool.query<{ open_count: number; outstanding_cents: number | string; due_at: Date | null }>(
      `SELECT count(*)::int AS open_count,
              COALESCE(sum(COALESCE(projection.principal_outstanding_cents, 0) +
                COALESCE(projection.fee_outstanding_cents, 0) +
                CASE WHEN COALESCE(projection.principal_outstanding_cents, 0) = 0
                           AND bridge.status IN ('reserved', 'pending', 'suspended')
                     THEN bridge.amount_cents ELSE 0 END), 0) AS outstanding_cents,
              min(bridge.repayment_due_at) AS due_at
         FROM bridge_cases AS bridge
         LEFT JOIN bridge_projections AS projection ON projection.bridge_id = bridge.id
        WHERE bridge.customer_id = $1
          AND bridge.status IN ('reserved', 'pending', 'suspended', 'funded', 'collecting')`, [current.customerId]),
  ]);
  const accountId = roster.rows[0]?.airwallex_connected_account_id;
  const facilityRow = facility.rows[0];
  const exposureRow = exposure.rows[0];
  if (!accountId?.trim() || !facilityRow || !exposureRow) return null;
  if (request.accountId !== accountId) return null;
  const facilitySizeCents = Number(facilityRow.facility_amount_cents);
  const reserveFloorCents = Number(facilityRow.reserve_floor_cents);
  const deployableCents = transactionDeployableCents ?? Number(facilityRow.deployable_cents);
  const openBridgeCount = transactionOpenBridgeCount ?? Number(exposureRow.open_count);
  return {
    precheck: current.precheck,
    eligibleSpend: current.profile.data.eligibleSpend,
    outstandingCents: Number(exposureRow.outstanding_cents),
    openBridgeCount,
    oldestOutstandingDueAt: exposureRow.due_at,
    facilitySizeCents,
    reserveFloorCents,
    deployableCents,
    accountId,
    ledgerSequence: String(facilityRow.ledger_sequence),
  };
}

export async function claimRequest(
  pool: DatabasePool,
  request: AirwallexRemoteAuthorizationV2,
  nonce: string,
  receivedAt: Date,
): Promise<{ customerId: string | null; responseStatus: "AUTHORIZED" | "DECLINED" | null; statusReason: string | null }> {
  const binding = await pool.query<{ customer_id: string }>(
    `SELECT roster.customer_id
       FROM customer_roster AS roster
       JOIN issuing_card_bindings AS card ON card.customer_id = roster.customer_id
      WHERE roster.airwallex_connected_account_id = $1 AND card.card_id = $2
        AND card.airwallex_account_id = $1 AND roster.is_active AND card.is_active`,
    [request.accountId, request.cardId],
  );
  const customerId = binding.rows[0]?.customer_id ?? null;
  if (!customerId) return { customerId: null, responseStatus: null, statusReason: null };
  const inserted = await pool.query(
    `INSERT INTO remote_authorization_requests (
       card_transaction_event_id, nonce, card_transaction_id, card_transaction_lifecycle_id,
       account_id, card_id, customer_id, amount_cents, currency, transaction_type,
       transaction_category, merchant_category_code, request_received_at
     ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
     ON CONFLICT DO NOTHING RETURNING card_transaction_event_id`,
    [request.cardTransactionEventId, nonce, request.cardTransactionId, request.cardTransactionLifecycleId,
      request.accountId, request.cardId, customerId, Math.round(request.transactionAmount * 100),
      request.transactionCurrency, request.transactionType, request.transactionCategory,
      request.merchantCategoryCode, receivedAt],
  );
  if (inserted.rowCount === 1) return { customerId, responseStatus: null, statusReason: null };
  const prior = await pool.query<{
    customer_id: string; card_transaction_id: string; account_id: string; card_id: string;
    amount_cents: number | string; currency: string; transaction_type: string;
    transaction_category: string; merchant_category_code: string;
    response_status: "AUTHORIZED" | "DECLINED" | null; status_reason: string | null;
  }>(
    `SELECT customer_id, card_transaction_id, account_id, card_id, amount_cents, currency,
            transaction_type, transaction_category, merchant_category_code,
            response_status, status_reason
       FROM remote_authorization_requests WHERE card_transaction_event_id = $1`,
    [request.cardTransactionEventId],
  );
  const row = prior.rows[0];
  if (!row || row.customer_id !== customerId || row.card_transaction_id !== request.cardTransactionId ||
      row.account_id !== request.accountId || row.card_id !== request.cardId ||
      Number(row.amount_cents) !== Math.round(request.transactionAmount * 100) || row.currency !== request.transactionCurrency ||
      row.transaction_type !== request.transactionType || row.transaction_category !== request.transactionCategory ||
      row.merchant_category_code !== request.merchantCategoryCode) {
    return { customerId: null, responseStatus: "DECLINED", statusReason: "request_replay_mismatch" };
  }
  if (!row.response_status) {
    const declined = await pool.query(
      `UPDATE remote_authorization_requests
          SET response_status = 'DECLINED', status_reason = 'request_in_progress_or_incomplete'
        WHERE card_transaction_event_id = $1 AND response_status IS NULL`,
      [request.cardTransactionEventId],
    );
    if (declined.rowCount === 1) {
      return { customerId, responseStatus: "DECLINED", statusReason: "request_in_progress_or_incomplete" };
    }
  }
  const final = await pool.query<{ response_status: "AUTHORIZED" | "DECLINED" | null; status_reason: string | null }>(
    `SELECT response_status, status_reason FROM remote_authorization_requests WHERE card_transaction_event_id = $1`,
    [request.cardTransactionEventId],
  );
  const finalRow = final.rows[0];
  return {
    customerId,
    responseStatus: finalRow?.response_status ?? "DECLINED",
    statusReason: finalRow?.status_reason ?? "request_in_progress_or_incomplete",
  };
}

export async function completeRequest(
  pool: DatabasePool,
  eventId: string,
  evaluationId: string | null,
  responseStatus: "AUTHORIZED" | "DECLINED",
  statusReason: string,
): Promise<RemoteAuthorizationDecision> {
  const reason = statusReason.slice(0, 100);
  await pool.query(
    `UPDATE remote_authorization_requests
        SET decision_id = COALESCE(decision_id, $2::uuid),
            response_status = COALESCE(response_status, $3),
            status_reason = COALESCE(status_reason, $4)
      WHERE card_transaction_event_id = $1`,
    [eventId, evaluationId, responseStatus, reason],
  );
  const stored = await pool.query<{ response_status: "AUTHORIZED" | "DECLINED"; status_reason: string }>(
    `SELECT response_status, status_reason FROM remote_authorization_requests WHERE card_transaction_event_id = $1`, [eventId]);
  const row = stored.rows[0];
  return { responseStatus: row?.response_status ?? responseStatus, statusReason: row?.status_reason ?? reason };
}

export async function persistDecision(
  pool: DatabasePool,
  evaluationId: string,
  eventId: string,
  decision: RequestDecision,
): Promise<void> {
  const client = await pool.connect();
  try {
    await client.query("BEGIN");
    const position = await client.query<{ ledger_sequence: number | string }>(
      "SELECT COALESCE(MAX(id), 0) AS ledger_sequence FROM ledger_events WHERE currency = 'USD'",
    );
    await persistRequestDecision(client, {
      decisionId: eventId,
      evaluationId,
      requestId: eventId,
      decision,
      ledgerSequence: decision.ledgerSequence ?? String(position.rows[0]?.ledger_sequence ?? 0),
      evaluatedAt: new Date(),
    });
    await client.query("COMMIT");
  } catch (error) {
    await client.query("ROLLBACK");
    throw error;
  } finally {
    client.release();
  }
}

export function stableBridgeId(eventId: string): string {
  return stableId("bridge", eventId);
}

export function stableApprovalId(eventId: string, actorId: string, reason: string): string {
  return stableId("approval", `${eventId}\0${actorId}\0${reason}`);
}

export function stableId(scope: string, value: string): string {
  const bytes = createHash("sha256").update(`${scope}\0${value}`).digest();
  bytes[6] = (bytes[6]! & 0x0f) | 0x50;
  bytes[8] = (bytes[8]! & 0x3f) | 0x80;
  const hex = bytes.subarray(0, 16).toString("hex");
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
}

export function assertActive(signal: AbortSignal): void {
  if (signal.aborted) throw signal.reason instanceof Error ? signal.reason : new Error("Remote authorization cancelled");
}
