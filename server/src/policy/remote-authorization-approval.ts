import type { AirwallexGateway } from "../airwallex/client.js";
import type { DatabasePool } from "../db/pool.js";
import { reservePolicyBridge, type RepaymentTerm } from "../ledger/reservations.js";
import type { AirwallexRemoteAuthorizationV2 } from "../http/routes/remote-authorization.js";
import { evaluateBridgeRequest } from "./decision-service.js";
import { confirmApprovedPolicyDispatch, loadPolicyState, loadRequestFacts, stableApprovalId, stableBridgeId, toBridgeRequest } from "./remote-authorization-data.js";

/** Exact-term operator approval always re-reads trusted request and current policy inputs. */
export function createRemoteAuthorizationApprovalRevalidator(input: {
  pool: DatabasePool;
  gateway: AirwallexGateway;
  repaymentTerm?: RepaymentTerm;
}) {
  return async (approval: {
    decisionId: string;
    evaluationId: string;
    requestId: string;
    customerId: string;
    amountCents: number;
    actorId: string;
    reason: string;
  }): Promise<{ outcome: string; amountCents: number; reserved: boolean }> => {
    if (!input.repaymentTerm || !approval.actorId.trim() || !approval.reason.trim()) {
      return { outcome: "blocked", amountCents: 0, reserved: false };
    }
    const stored = await input.pool.query<{
      customer_id: string; account_id: string; card_id: string; card_transaction_id: string;
      card_transaction_lifecycle_id: string; amount_cents: number | string; currency: string;
      transaction_type: "AUTHORIZATION" | "CLEARING"; transaction_category: string;
      merchant_category_code: string;
    }>(
      `SELECT customer_id, account_id, card_id, card_transaction_id,
              card_transaction_lifecycle_id, amount_cents, currency,
              transaction_type, transaction_category, merchant_category_code
         FROM remote_authorization_requests
        WHERE card_transaction_event_id = $1`, [approval.requestId]);
    const row = stored.rows[0];
    if (!row || row.customer_id !== approval.customerId || row.currency !== "USD" ||
        row.transaction_type !== "AUTHORIZATION" || row.transaction_category !== "PURCHASE" ||
        !["5411", "5812", "5814", "5734"].includes(row.merchant_category_code)) {
      return { outcome: "blocked", amountCents: 0, reserved: false };
    }
    const request: AirwallexRemoteAuthorizationV2 = {
      version: 2, accountId: row.account_id, cardId: row.card_id,
      cardTransactionEventId: approval.requestId, cardTransactionId: row.card_transaction_id,
      cardTransactionLifecycleId: row.card_transaction_lifecycle_id, transactionType: row.transaction_type,
      transactionCategory: row.transaction_category, transactionAmount: Number(row.amount_cents) / 100,
      transactionCurrency: row.currency, merchantCategoryCode: row.merchant_category_code,
    };
    const current = await loadRequestFacts(input.pool, approval.customerId, request, new Date());
    if (!current) return { outcome: "blocked", amountCents: 0, reserved: false };
    const currentBalance = await input.gateway.getBalance(current.accountId, "USD");
    const state = await loadPolicyState(input.pool, current, request);
    if (!state) return { outcome: "blocked", amountCents: 0, reserved: false };
    const humanApproval = {
      requestId: approval.requestId,
      amountCents: approval.amountCents,
      actorId: approval.actorId,
      reason: approval.reason,
    };
    const decision = await evaluateBridgeRequest({
      request: toBridgeRequest(request, approval.customerId), state,
      readCurrentBalance: async () => currentBalance, humanApproval, balanceTimeoutMs: 1_000,
    });
    if (decision.outcome !== "auto_approved" || decision.amountCents !== approval.amountCents) {
      return { outcome: decision.outcome, amountCents: decision.amountCents, reserved: false };
    }
    const approvalDecisionId = stableApprovalId(approval.requestId, approval.actorId, approval.reason);
    const result = await reservePolicyBridge(input.pool, {
      bridgeId: stableBridgeId(approval.requestId), customerId: approval.customerId,
      amountCents: approval.amountCents, repaymentTerm: input.repaymentTerm,
      decisionId: approvalDecisionId,
      requestId: approval.requestId, currentBalance, humanApproval,
      revalidate: async (context) => {
        const latest = await loadRequestFacts(context.client, approval.customerId, request, new Date());
        if (!latest || latest.precheck.snapshotId !== context.snapshotId || latest.precheck.evidenceRunId !== context.evidenceRunId ||
            latest.precheck.profileId !== current.precheck.profileId) throw new Error("Current policy versions changed before approval");
        const lockedState = await loadPolicyState(context.client, latest, request,
          context.deployableCents, context.openBridgeCount);
        if (!lockedState) throw new Error("Current ledger state is unavailable");
        const lockedDecision = await evaluateBridgeRequest({
          request: toBridgeRequest(request, approval.customerId), state: lockedState,
          readCurrentBalance: async () => currentBalance, humanApproval, balanceTimeoutMs: 1_000,
        });
        return { decision: lockedDecision, precheck: latest.precheck };
      },
    });
    if (result.reservation && !await confirmApprovedPolicyDispatch(input.pool, approval.requestId, approvalDecisionId)) {
      throw new Error("Approved bridge reservation could not be released to the transfer outbox");
    }
    return {
      outcome: result.decision.outcome,
      amountCents: result.decision.amountCents,
      reserved: result.reservation !== null,
    };
  };
}
