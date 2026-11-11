/**
 * "You can bridge up to $X" (spec §8): the room left under the customer's limit,
 * capped by capital above the reserve floor. The same numbers eligibility rules
 * 1, 2, 3 and 5 use, so the figure shown never promises more than a request
 * would get. Spend criticality is still checked per request (rule 4).
 */
import type { Customer, PlatformCapital, Tier } from '../types.js';
import { computeTier, customerLimit, historyGap, outstandingBalance } from './tiers.js';
import { availableAboveFloor, currentRegime, regimeRule } from './regime.js';
import { HISTORY } from './policy.config.js';
import { daysBetween, round2 } from './util.js';

export type PreApprovalBlock = 'NO_HISTORY' | 'NOT_ELIGIBLE' | 'PAST_DUE' | 'AT_LIMIT' | 'NO_CAPITAL';

export interface PreApproval {
  amount: number;
  tier: Tier;
  limit: number;
  outstanding: number;
  blocked: PreApprovalBlock | null;
  /** Customer-facing sentence for when amount is 0. */
  reason: string | null;
}

/** `customer` should already carry live bridges (data/live.ts). */
export function preApproved(customer: Customer, capital: PlatformCapital, today: string): PreApproval {
  const tier = computeTier(customer);
  const limit = customerLimit(customer, tier);
  const outstanding = outstandingBalance(customer);
  const base = { tier, limit, outstanding };

  if (historyGap(customer) !== null) {
    return {
      ...base,
      amount: 0,
      blocked: 'NO_HISTORY',
      reason: `Bridges unlock after ${HISTORY.minTenureMonths} months on FlowCard. You have ${customer.tenure_months}.`,
    };
  }
  if (tier === 'NONE') {
    return { ...base, amount: 0, blocked: 'NOT_ELIGIBLE', reason: 'Bridges are not available on your account right now.' };
  }
  const pastDue = customer.bridge_history.some((b) => b.outcome === 'OPEN' && daysBetween(b.due_date, today) > 0);
  if (pastDue) {
    return { ...base, amount: 0, blocked: 'PAST_DUE', reason: 'Available again once your overdue bridge is repaid.' };
  }
  const headroom = Math.max(0, round2(limit - outstanding));
  if (headroom === 0) {
    return { ...base, amount: 0, blocked: 'AT_LIMIT', reason: 'You are using your full bridge limit. It frees up as you repay.' };
  }
  const capitalRoom = regimeRule(currentRegime(capital)).approval === 'CLOSED' ? 0 : availableAboveFloor(capital);
  const amount = round2(Math.min(headroom, capitalRoom));
  if (amount === 0) {
    return { ...base, amount: 0, blocked: 'NO_CAPITAL', reason: 'New bridges are paused for now. Try again in a few days.' };
  }
  return { ...base, amount, blocked: null, reason: null };
}
