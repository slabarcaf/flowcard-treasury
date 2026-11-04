/**
 * Customer-facing copy for a failed rule.
 *
 * This is wording only. Which rule failed comes from the engine's decision;
 * nothing here decides anything. The engine's own `detail` is written for the
 * ops team and names internal ids, so the portal says the same thing in the
 * customer's terms, using the numbers the decision carries.
 */
import type { Customer, Decision, RuleResult } from '../types.js';
import { computeTier } from '../policy/tiers.js';
import { HISTORY, TIERS } from '../policy/policy.config.js';
import { usd } from '../ui/format.js';

export const RULE_GUIDANCE: Record<string, string> = {
  R1_HAS_HISTORY:
    'Keep running deposits and card spend through FlowCard. Bridges open up once you have three months with us and 90 days of balance history.',
  R2_NO_PAST_DUE:
    'Clear the bridge that is past due. Your next deposit will be collected against it automatically, and new bridges open up again once it is repaid.',
  R3_WITHIN_LIMIT:
    'Your limit is a share of your monthly deposits, so it grows as they do. You could also repay part of what is outstanding to free up room.',
  R4_CRITICALITY_ALLOWED:
    'We are protecting capital for essentials like suppliers, payroll and inventory right now. Try again when this clears, or use the card for this once funds land.',
  R5_RESERVE_FLOOR:
    'We do not have enough available today to cover the full amount, and we never bridge part of a payment. Try again once capital frees up.',
  R6_NO_DUPLICATE:
    'This payment already has a bridge. Open it to see what is outstanding and when it is due.',
  R7_RECEIVABLE_IN_TERM:
    'We need an invoice due before the bridge ends so there is something to collect from. Choose a longer term, or try again when an invoice is closer to its due date.',
  R8_REGIME_TIER:
    'Capital is tight today and we are prioritising our longest-standing accounts. Your standing improves with tenure and with bridges repaid on time.',
};

export function guidanceFor(ruleId: string): string {
  return (
    RULE_GUIDANCE[ruleId] ??
    'Get in touch with support and we will walk through the options with you.'
  );
}

/**
 * One clear sentence for the rule that failed, in the customer's terms.
 * Falls back to the engine's own wording for anything unmapped.
 */
export function reasonFor(rule: RuleResult, decision: Decision): string {
  switch (rule.rule_id) {
    case 'R1_HAS_HISTORY':
      return 'Your account is too new for a bridge just yet.';
    case 'R2_NO_PAST_DUE':
      return 'You have a bridge that is past due, so we cannot open another one.';
    case 'R3_WITHIN_LIMIT':
      return decision.outstanding > 0
        ? `You can bridge up to ${usd(decision.limit)} at a time, and ${usd(decision.outstanding)} of that is already in use.`
        : `This is more than your ${usd(decision.limit)} bridge limit.`;
    case 'R4_CRITICALITY_ALLOWED':
      return 'We are only covering essential spend at the moment, and this payment is not in that group.';
    case 'R5_RESERVE_FLOOR':
      return 'We do not have enough available today to cover the whole amount, and we never cover part of a payment.';
    case 'R6_NO_DUPLICATE':
      return 'This payment already has a bridge.';
    case 'R7_RECEIVABLE_IN_TERM':
      return 'You have no invoice due before this bridge would end.';
    case 'R8_REGIME_TIER':
      return 'Capital is tight today, so we are only covering our longest-standing accounts.';
    default:
      return rule.detail;
  }
}

// ---------------------------------------------------------------------------
// The customer's bridge line, in their own terms
// ---------------------------------------------------------------------------

/** One reason the line is the size it is. */
export interface LinePoint {
  label: string;
  detail: string;
  /** True when this is working in the customer's favour. */
  good: boolean;
}

/**
 * Why the line is what it is.
 *
 * Every point reads a value the engine already computed. Tier letters are
 * deliberately not shown: they are an internal grade, and the customer only
 * needs to know what moves the number.
 */
export function lineExplanation(customer: Customer): LinePoint[] {
  const tier = computeTier(customer);
  const { bridge_stats, balance_stats, inflow_stats, tenure_months } = customer;
  const closed = bridge_stats.on_time_count + bridge_stats.late_count;

  const points: LinePoint[] = [
    {
      label: 'Time with FlowCard',
      detail: `${tenure_months} months`,
      good: tenure_months >= TIERS.A.minTenureMonths,
    },
    {
      label: 'Monthly deposits',
      detail: `${usd(inflow_stats.monthly_inflows)} a month on average, and your limit is a share of that`,
      good: inflow_stats.monthly_inflows > 0,
    },
  ];

  if (closed > 0) {
    points.push({
      label: 'Repayment record',
      detail:
        bridge_stats.late_count === 0
          ? `${bridge_stats.on_time_count} of ${closed} bridges repaid on time`
          : `${bridge_stats.late_count} of ${closed} bridges repaid late`,
      good: bridge_stats.late_count === 0,
    });
  }

  points.push({
    label: 'Balance stability',
    detail:
      balance_stats.days_near_zero_90d <= TIERS.A.maxDaysNearZero90d
        ? `Your wallet stayed healthy on all but ${balance_stats.days_near_zero_90d} of the last 90 days`
        : `Your wallet ran close to zero on ${balance_stats.days_near_zero_90d} of the last 90 days`,
    good: balance_stats.days_near_zero_90d <= TIERS.A.maxDaysNearZero90d,
  });

  if (tier === 'NONE') {
    points.push({
      label: 'Not available yet',
      detail: guidanceFor('R1_HAS_HISTORY'),
      good: false,
    });
  }

  return points;
}

/** The single most useful thing this customer could do to raise their line. */
export function lineNextStep(customer: Customer): string | null {
  const tier = computeTier(customer);
  if (tier === 'NONE') {
    const months = Math.max(0, HISTORY.minTenureMonths - customer.tenure_months);
    return months > 0
      ? `Bridges open up after ${HISTORY.minTenureMonths} months with FlowCard — ${months} to go.`
      : `Bridges open up once you have ${HISTORY.minBalanceDays} days of balance history with us.`;
  }
  if (tier === 'C') {
    return 'Repay your next bridges on time and keep your wallet above zero, and your limit moves up with your standing.';
  }
  if (tier === 'B') {
    const months = Math.max(0, TIERS.A.minTenureMonths - customer.tenure_months);
    return months > 0
      ? `${months} more months with no late repayment would move you to our top limit.`
      : 'Keep repaying on time and your limit moves up to our top band.';
  }
  return 'You are on our highest limit. It grows as your monthly deposits do.';
}
