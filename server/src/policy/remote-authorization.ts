import type { AirwallexGateway } from "../airwallex/client.js";
import type { DatabasePool } from "../db/pool.js";
import { reservePolicyBridge, type RepaymentTerm } from "../ledger/reservations.js";
import type { RemoteAuthorizationResolver } from "../http/routes/remote-authorization.js";
import { getCurrentUnderwritingProfile } from "../underwriting/repository.js";
import { getCustomerSnapshotReadModel } from "../snapshots/freshness.js";
import { evaluateBridgeRequest } from "./decision-service.js";
import type { TrustedBridgeDecisionResult } from "../http/routes/bridge-decision.js";
import { assertActive, claimRequest, completeRequest, loadPolicyState, loadRequestFacts, persistDecision, stableBridgeId, toBridgeRequest } from "./remote-authorization-data.js";

/** Read a redacted decision only for a transaction already stored from signed Airwallex input. */
export async function resolveStoredRemoteAuthorizationDecision(
  pool: DatabasePool,
  requestId: string,
): Promise<TrustedBridgeDecisionResult> {
  const result = await pool.query<{
    evaluation_id: string; request_id: string; customer_id: string; ledger_sequence: number | string;
    outcome: TrustedBridgeDecisionResult["outcome"];
    amount_cents: number | string; credit_line_cents: number | string;
    reason_codes: string[]; evaluated_at: Date; policy_version: string;
    profile_id: string | null; snapshot_id: string | null; evidence_run_id: string | null;
    input_versions: Record<string, string | number | null>;
  }>(
    `SELECT decision.evaluation_id, decision.request_id, decision.customer_id,
            decision.outcome, decision.amount_cents, decision.credit_line_cents,
            decision.reason_codes, evaluation.evaluated_at, evaluation.policy_version,
            evaluation.profile_id, evaluation.snapshot_id, evaluation.evidence_run_id,
            evaluation.input_versions, decision.ledger_sequence
       FROM policy_request_evaluations AS decision
       JOIN remote_authorization_requests AS request
         ON request.card_transaction_event_id = decision.request_id
       JOIN policy_evaluations AS evaluation ON evaluation.evaluation_id = decision.evaluation_id
      WHERE decision.request_id = $1
      ORDER BY decision.evaluated_at DESC, decision.decision_id DESC LIMIT 1`,
    [requestId],
  );
  const row = result.rows[0];
  if (!row) throw new Error("Stored remote authorization decision was not found");
  const now = new Date();
  const profile = await getCurrentUnderwritingProfile(pool, row.customer_id, now);
  const snapshot = await getCustomerSnapshotReadModel(pool, row.customer_id, now);
  return {
    evaluationId: row.evaluation_id,
    requestId: row.request_id,
    customerId: row.customer_id,
    outcome: row.outcome,
    amountCents: Number(row.amount_cents),
    creditLineCents: Number(row.credit_line_cents),
    reasonCodes: row.reason_codes,
    freshness: {
      evaluatedAt: row.evaluated_at.toISOString(),
      profileAgeSeconds: profile.ageSeconds,
      profileFreshness: profile.freshness,
      snapshotAgeSeconds: snapshot.ageSeconds,
      snapshotFreshness: snapshot.freshness,
    },
    versions: {
      policyVersion: row.policy_version,
      profileId: row.profile_id,
      snapshotId: row.snapshot_id,
      evidenceRunId: row.evidence_run_id,
      rosterUpdatedAt: row.input_versions.rosterUpdatedAt ?? null,
      profileVersion: row.input_versions.profileVersion ?? null,
      profileFeatureVersion: row.input_versions.profileFeatureVersion ?? null,
      profileAccountSourceVersion: row.input_versions.profileAccountSourceVersion ?? null,
      profileBalancesSourceVersion: row.input_versions.profileBalancesSourceVersion ?? null,
      profileTransactionsSourceVersion: row.input_versions.profileTransactionsSourceVersion ?? null,
      profileExpectedInflowsSourceVersion: row.input_versions.profileExpectedInflowsSourceVersion ?? null,
      profileRepaymentsSourceVersion: row.input_versions.profileRepaymentsSourceVersion ?? null,
      salesforceSnapshotId: row.input_versions.salesforceSnapshotId ?? null,
      salesforceSnapshotVersion: row.input_versions.salesforceSnapshotVersion ?? null,
      salesforceMappingVersion: row.input_versions.salesforceMappingVersion ?? null,
      evidenceConfigurationVersion: row.input_versions.evidenceConfigurationVersion ?? null,
      evidenceCatalogVersion: row.input_versions.evidenceCatalogVersion ?? null,
      evidenceSchemaVersion: row.input_versions.evidenceSchemaVersion ?? null,
      ledgerSequence: String(row.ledger_sequence),
    },
  };
}

/** Resolve signed provider facts only through an active roster and explicit card binding. */
export function createRemoteAuthorizationResolver(input: {
  pool: DatabasePool;
  gateway: AirwallexGateway;
  repaymentTerm?: RepaymentTerm;
  now?: () => Date;
}): RemoteAuthorizationResolver {
  return async (request, nonce, signal) => {
    assertActive(signal);
    const claim = await claimRequest(input.pool, request, nonce, input.now?.() ?? new Date());
    if (claim.responseStatus) return { responseStatus: claim.responseStatus, statusReason: claim.statusReason ?? "request_replayed" };
    if (!claim.customerId || !claim.decisionId) return completeRequest(input.pool, request.cardTransactionEventId, "DECLINED", "card_not_enrolled");
    if (!input.repaymentTerm) return completeRequest(input.pool, request.cardTransactionEventId, "DECLINED", "repayment_term_not_configured");

    const current = await loadRequestFacts(input.pool, claim.customerId, request, input.now?.() ?? new Date());
    if (!current) return completeRequest(input.pool, request.cardTransactionEventId, "DECLINED", "current_policy_inputs_unavailable");
    const requestInput = toBridgeRequest(request, current.customerId);
    const currentBalance = await input.gateway.getBalance(current.accountId, "USD");
    const state = await loadPolicyState(input.pool, current, request);
    if (!state) return completeRequest(input.pool, request.cardTransactionEventId, "DECLINED", "current_policy_inputs_unavailable");
    const decision = await evaluateBridgeRequest({
      request: requestInput,
      state,
      readCurrentBalance: async () => currentBalance,
      now: input.now?.() ?? new Date(),
      balanceTimeoutMs: 1_000,
    });
    assertActive(signal);

    if (decision.outcome === "auto_approved") {
      const result = await reservePolicyBridge(input.pool, {
        bridgeId: stableBridgeId(request.cardTransactionEventId),
        customerId: current.customerId,
        amountCents: decision.amountCents,
        repaymentTerm: input.repaymentTerm,
        decisionId: claim.decisionId,
        requestId: request.cardTransactionEventId,
        currentBalance,
        signal,
        revalidate: async (context) => {
          assertActive(signal);
          const latest = await loadRequestFacts(context.client, current.customerId, request, input.now?.() ?? new Date());
          if (!latest || latest.precheck.snapshotId !== context.snapshotId || latest.precheck.evidenceRunId !== context.evidenceRunId ||
              latest.precheck.profileId !== current.precheck.profileId) {
            throw new Error("Current policy versions changed before reservation");
          }
          const lockedState = await loadPolicyState(context.client, latest, request,
            context.deployableCents, context.openBridgeCount);
          if (!lockedState) throw new Error("Current ledger state is unavailable");
          const currentDecision = await evaluateBridgeRequest({
            request: requestInput, state: lockedState, readCurrentBalance: async () => currentBalance,
            humanApproval: context.approval, now: input.now?.() ?? new Date(), balanceTimeoutMs: 1_000,
          });
          assertActive(signal);
          const status = currentDecision.outcome === "auto_approved" ? "AUTHORIZED" : "DECLINED";
          const reason = currentDecision.reasonCodes[0]?.toLowerCase() ??
            (status === "AUTHORIZED" ? "policy_approved" : "policy_declined");
          const completed = await context.client.query(
            `UPDATE remote_authorization_requests
                SET decision_id = $2::uuid, response_status = $3, status_reason = $4
              WHERE card_transaction_event_id = $1 AND response_status IS NULL`,
            [request.cardTransactionEventId, claim.decisionId, status, reason],
          );
          if (completed.rowCount !== 1) throw new Error("Remote authorization request is no longer pending");
          return { decision: currentDecision, precheck: latest.precheck };
        },
      });
      if (!result.reservation) {
        return completeRequest(input.pool, request.cardTransactionEventId, "DECLINED",
          result.decision.reasonCodes[0]?.toLowerCase() ?? "policy_declined");
      }
      return completeRequest(input.pool, request.cardTransactionEventId, "AUTHORIZED", "policy_approved");
    }

    await persistDecision(input.pool, claim.decisionId, current.evaluationId, request.cardTransactionEventId, decision);
    const reason = decision.outcome === "human_review" ? "human_review_required" :
      decision.reasonCodes[0]?.toLowerCase() ?? "policy_declined";
    return completeRequest(input.pool, request.cardTransactionEventId, "DECLINED", reason);
  };
}


export { createRemoteAuthorizationApprovalRevalidator } from "./remote-authorization-approval.js";
export { createRemoteAuthorizationNonceClaimer } from "./remote-authorization-nonce.js";
