/** Tier assignment and limits (spec §3). */
import type { Customer, PastBridge, Tier } from '../types.js';
import { HISTORY, TIERS, TIER_LIMIT_PCT, TIER_DOWNGRADE, TIER_RANK } from './policy.config.js';
import { daysBetween, round2 } from './util.js';

/**
 * Spec §3: "No history: tenure < 3 months or < 90 days of balance data."
 * Returns the reason it fails, or null when the customer has enough history.
 */
export function historyGap(customer: Customer): string | null {
  if (customer.tenure_months < HISTORY.minTenureMonths) {
    return `Tenure ${customer.tenure_months} months, ${HISTORY.minTenureMonths} required`;
  }
  if (customer.balance_history.length < HISTORY.minBalanceDays) {
    return `${customer.balance_history.length} days of balance data, ${HISTORY.minBalanceDays} required`;
  }
  return null;
}

/** The date tier maths is measured against: the customer's most recent balance. */
function asOf(customer: Customer): string {
  const last = customer.balance_history.at(-1);
  return last ? last.date : customer.crm.onboarding_date;
}

/** Bridges that closed within `days` of the as-of date. */
function closedWithin(customer: Customer, days: number): PastBridge[] {
  const today = asOf(customer);
  return customer.bridge_history.filter((b) => {
    const closed = b.closed_at;
    if (closed === null) return false;
    return daysBetween(closed, today) <= days;
  });
}

/**
 * Assign a tier from spec §3.
 *
 * CRM data is deliberately not consulted: spec §2 says it is context for ops
 * and not used in scoring.
 */
export function computeTier(customer: Customer): Tier {
  if (historyGap(customer) !== null) return 'NONE';

  // Tier C's gate is also the eligibility gate: a default inside 12 months
  // makes the customer ineligible (spec §5, "Ineligible for 12 months").
  const recentDefaults = closedWithin(customer, TIERS.C.defaultLookbackDays).filter(
    (b) => b.outcome === 'DEFAULTED',
  );
  if (recentDefaults.length > 0) return 'NONE';

  const aWindow = closedWithin(customer, TIERS.A.lateLookbackDays);
  const lateInA = aWindow.filter((b) => b.outcome === 'LATE').length;
  const defaultedInA = aWindow.filter((b) => b.outcome === 'DEFAULTED').length;

  if (
    customer.tenure_months >= TIERS.A.minTenureMonths &&
    lateInA <= TIERS.A.maxLateInLookback &&
    defaultedInA <= TIERS.A.maxDefaultsInLookback &&
    customer.balance_stats.days_near_zero_90d <= TIERS.A.maxDaysNearZero90d
  ) {
    return 'A';
  }

  const lateInB = closedWithin(customer, TIERS.B.lateLookbackDays).filter(
    (b) => b.outcome === 'LATE',
  ).length;
  const everDefaulted = customer.bridge_history.some((b) => b.outcome === 'DEFAULTED');

  if (
    customer.tenure_months >= TIERS.B.minTenureMonths &&
    lateInB <= TIERS.B.maxLateInLookback &&
    !everDefaulted
  ) {
    return 'B';
  }

  return 'C';
}

/**
 * The customer's bridge limit: a share of monthly_inflows set by tier (spec §3).
 * Spec §3 also says the limit applies to total outstanding bridge balance
 * (existing + new) — that comparison lives in the eligibility rules.
 */
export function customerLimit(customer: Customer, tier: Tier = computeTier(customer)): number {
  return round2(customer.inflow_stats.monthly_inflows * TIER_LIMIT_PCT[tier]);
}

/** Total principal + fee still owed on open bridges; counts against the limit. */
export function outstandingBalance(customer: Customer): number {
  return round2(
    customer.bridge_history
      .filter((b) => b.outcome === 'OPEN')
      .reduce((sum, b) => sum + b.outstanding_balance, 0),
  );
}

/** Spec §5: tier downgraded one level at 8 DPD. */
export function downgradeTier(tier: Tier): Tier {
  return TIER_DOWNGRADE[tier];
}

/** True when `tier` is at least as good as `floor`. */
export function tierAtLeast(tier: Tier, floor: Tier): boolean {
  return TIER_RANK[tier] >= TIER_RANK[floor];
}
