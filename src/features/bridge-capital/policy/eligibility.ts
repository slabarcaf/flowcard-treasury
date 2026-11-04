/**
 * Eligibility rules and the per-request decision (spec §4, with the §6 regime gates).
 */
import type {
  BridgeRequest,
  Customer,
  Decision,
  PlatformCapital,
  RuleResult,
  Tier,
} from '../types.js';
import { computeTier, customerLimit, historyGap, outstandingBalance, tierAtLeast } from './tiers.js';
import { availableAboveFloor, currentRegime, regimeRule } from './regime.js';
import { REGIME_LABELS, ruleLabel } from './policy.config.js';
import { addDays, daysBetween, round2 } from './util.js';

export interface EvaluationContext {
  /** Defaults to the request's own timestamp. */
  today?: string;
  /**
   * Decisions already taken. A request for a transaction that appears here is a
   * duplicate, and the earlier decision is returned unchanged (spec §4 rule 6).
   */
  priorDecisions?: Decision[];
  /**
   * Outstanding to count on top of what the customer record shows, used while
   * ranking a batch so capital funded earlier in the batch is not double-spent.
   */
  extraOutstanding?: number;
}

function money(n: number): string {
  return `$${round2(n).toLocaleString('en-US', { minimumFractionDigits: 0, maximumFractionDigits: 2 })}`;
}

/**
 * Run every eligibility rule for one request.
 *
 * Rules 1–7 are the spec §4 list in order. Rule 8 is the spec §6 tier gate
 * ("Normal: tier B or better", "Critical: tier A only"), which §4 does not list
 * but §6 requires, so it is reported alongside the others.
 */
export function evaluate(
  request: BridgeRequest,
  customer: Customer,
  capital: PlatformCapital,
  ctx: EvaluationContext = {},
): Decision {
  const today = ctx.today ?? request.requested_at;
  const transactionId = request.transaction.id;

  // Rule 6 short-circuit: a transaction already decided keeps its decision.
  const prior = (ctx.priorDecisions ?? []).find((d) => d.transaction_id === transactionId);
  if (prior) {
    return { ...prior, duplicate_of: prior.request_id };
  }

  const tier = computeTier(customer);
  const limit = customerLimit(customer, tier);
  const outstanding = round2(outstandingBalance(customer) + (ctx.extraOutstanding ?? 0));
  const regime = currentRegime(capital);
  const rule = regimeRule(regime);
  const available = availableAboveFloor(capital);
  const criticality = request.transaction.criticality;

  const rules: RuleResult[] = [];
  // Labels come from the rule catalogue in policy.config, which the back
  // office renders as its rules reference.
  const add = (rule_id: string, passed: boolean, detail: string) =>
    rules.push({ rule_id, label: ruleLabel(rule_id), passed, detail });

  // 1 — Has history
  const gap = historyGap(customer);
  add(
    'R1_HAS_HISTORY',
    gap === null,
    gap === null
      ? `${customer.tenure_months} months tenure, ${customer.balance_history.length} days of balances`
      : gap,
  );

  // 2 — No bridge currently past due
  const pastDue = customer.bridge_history.filter(
    (b) => b.outcome === 'OPEN' && daysBetween(b.due_date, today) > 0,
  );
  add(
    'R2_NO_PAST_DUE',
    pastDue.length === 0,
    pastDue.length === 0
      ? 'No open bridge past its due date'
      : `${pastDue.length} open bridge past due (${pastDue.map((b) => b.id).join(', ')})`,
  );

  // 3 — Outstanding + new bridge <= customer limit
  const withinLimit = tier !== 'NONE' && round2(outstanding + request.principal) <= limit;
  add(
    'R3_WITHIN_LIMIT',
    withinLimit,
    tier === 'NONE'
      ? 'No limit: customer is not in a tier'
      : `${money(outstanding)} outstanding + ${money(request.principal)} requested against a ${money(limit)} limit (tier ${tier})`,
  );

  // 4 — Spend criticality allowed in the current capital regime
  const criticalityOk = rule.allowedCriticality.includes(criticality);
  add(
    'R4_CRITICALITY_ALLOWED',
    criticalityOk,
    criticalityOk
      ? `${criticality} spend is allowed in ${REGIME_LABELS[regime]}`
      : `${criticality} spend is not allowed in ${REGIME_LABELS[regime]} (allowed: ${rule.allowedCriticality.join(', ') || 'none'})`,
  );

  // 5 — Bridge does not breach the platform reserve floor
  const floorOk = request.principal <= available;
  add(
    'R5_RESERVE_FLOOR',
    floorOk,
    floorOk
      ? `${money(request.principal)} fits the ${money(available)} available above the floor`
      : `${money(request.principal)} exceeds the ${money(available)} available above the floor`,
  );

  // 6 — No duplicate bridge for the same transaction
  const existingForTxn = customer.bridge_history.filter((b) => b.transaction_id === transactionId);
  add(
    'R6_NO_DUPLICATE',
    existingForTxn.length === 0,
    existingForTxn.length === 0
      ? `No existing bridge for transaction ${transactionId}`
      : `Transaction ${transactionId} already bridged by ${existingForTxn[0].id}`,
  );

  // 7 — An expected receivable is due before the term ends
  const termEnd = addDays(today, request.term_days);
  const covering = customer.expected_receivables.filter(
    (r) => daysBetween(r.due_date, termEnd) >= 0,
  );
  add(
    'R7_RECEIVABLE_IN_TERM',
    covering.length > 0,
    covering.length > 0
      ? `${covering.length} receivable(s) due by ${termEnd}, earliest ${covering[0].payer_name} on ${covering[0].due_date}`
      : `No expected receivable due by ${termEnd}`,
  );

  // 8 — Regime tier gate (spec §6)
  const minTier: Tier | null = rule.minTier;
  const tierOk =
    rule.approval !== 'CLOSED' && tier !== 'NONE' && (minTier === null || tierAtLeast(tier, minTier));
  add(
    'R8_REGIME_TIER',
    tierOk,
    rule.approval === 'CLOSED'
      ? `No new bridges in ${REGIME_LABELS[regime]}`
      : minTier === null
        ? `Any eligible tier is allowed in ${REGIME_LABELS[regime]} (tier ${tier})`
        : `${REGIME_LABELS[regime]} requires tier ${minTier} or better (tier ${tier})`,
  );

  const failed = rules.filter((r) => !r.passed);
  const limitHeadroom = tier === 'NONE' ? 0 : Math.max(0, round2(limit - outstanding));
  const maxBridgeable =
    rule.approval === 'CLOSED' ? 0 : round2(Math.min(limitHeadroom, available));

  const needsApproval =
    rule.approval === 'HUMAN' ||
    (rule.humanApprovalAbove !== null && request.principal > rule.humanApprovalAbove);

  return {
    request_id: request.id,
    transaction_id: transactionId,
    customer_id: customer.id,
    tier,
    limit,
    outstanding,
    regime,
    rules,
    reasons: failed.map((r) => r.detail),
    eligible: failed.length === 0,
    requested: round2(request.principal),
    max_bridgeable: maxBridgeable,
    needs_human_approval: needsApproval,
  };
}
