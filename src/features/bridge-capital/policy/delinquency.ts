/** The late-repayment ladder (spec §5). */
import type { Bridge, BridgeStatus } from '../types.js';
import { DELINQUENCY } from './policy.config.js';
import { accruedLateFee } from './pricing.js';
import { addDays, daysBetween } from './util.js';

export type DelinquencyAction =
  | 'SEND_DAILY_REMINDER'
  | 'COLLECT_FROM_ANY_DEPOSIT'
  | 'BLOCK_NEW_BRIDGES'
  | 'RESTRICT_CARD_TO_CRITICAL'
  | 'DOWNGRADE_TIER'
  | 'NOTIFY_OPS'
  | 'FREEZE_CARD'
  | 'ESCALATE_TO_COLLECTIONS'
  | 'SET_LIMIT_ZERO'
  | 'RECORD_DEFAULT'
  | 'REFER_TO_COLLECTIONS';

export interface DelinquencyResult {
  status: BridgeStatus;
  days_past_due: number;
  actions: DelinquencyAction[];
  /** Fee accrued past the due date at the monthly rate. */
  accrued_fee: number;
  /** How many tier levels to drop. 1 from 8 DPD (spec §5). */
  tier_downgrade_levels: number;
  /** Set once defaulted: ineligible for 12 months from this date. */
  ineligible_until?: string;
}

/**
 * Move a bridge along the ladder for a given day and say what must happen.
 *
 *   1-7 DPD   Watch       daily reminders, collect from any deposit, no new bridges
 *   8-30 DPD  Delinquent  card restricted to critical spend, tier down one, ops notified
 *   31-60 DPD Serious     card frozen, escalated to collections, limit set to 0
 *   60+ DPD   Default     recorded as DEFAULTED, referred to collections
 *
 * A repaid bridge is left alone, and a bridge inside its term produces no actions.
 */
export function applyDelinquency(bridge: Bridge, today: string): DelinquencyResult {
  if (bridge.status === 'REPAID' || bridge.status === 'WITHDRAWN' || bridge.status === 'DECLINED') {
    return {
      status: bridge.status,
      days_past_due: 0,
      actions: [],
      accrued_fee: 0,
      tier_downgrade_levels: 0,
    };
  }

  const dpd = Math.max(0, daysBetween(bridge.due_date, today));
  const accrued = accruedLateFee(bridge.principal_outstanding, dpd);

  if (dpd < DELINQUENCY.watch.from) {
    return {
      status: bridge.status === 'DISBURSED' ? 'COLLECTING' : bridge.status,
      days_past_due: 0,
      actions: [],
      accrued_fee: 0,
      tier_downgrade_levels: 0,
    };
  }

  if (dpd <= DELINQUENCY.watch.to) {
    return {
      status: 'WATCH',
      days_past_due: dpd,
      actions: ['SEND_DAILY_REMINDER', 'COLLECT_FROM_ANY_DEPOSIT', 'BLOCK_NEW_BRIDGES'],
      accrued_fee: accrued,
      tier_downgrade_levels: 0,
    };
  }

  if (dpd <= DELINQUENCY.delinquent.to) {
    return {
      status: 'DELINQUENT',
      days_past_due: dpd,
      actions: [
        'SEND_DAILY_REMINDER',
        'COLLECT_FROM_ANY_DEPOSIT',
        'BLOCK_NEW_BRIDGES',
        'RESTRICT_CARD_TO_CRITICAL',
        'DOWNGRADE_TIER',
        'NOTIFY_OPS',
      ],
      accrued_fee: accrued,
      tier_downgrade_levels: 1,
    };
  }

  if (dpd <= DELINQUENCY.serious.to) {
    return {
      status: 'SERIOUS',
      days_past_due: dpd,
      actions: [
        'COLLECT_FROM_ANY_DEPOSIT',
        'BLOCK_NEW_BRIDGES',
        'FREEZE_CARD',
        'ESCALATE_TO_COLLECTIONS',
        'SET_LIMIT_ZERO',
      ],
      accrued_fee: accrued,
      tier_downgrade_levels: 1,
    };
  }

  return {
    status: 'DEFAULTED',
    days_past_due: dpd,
    actions: [
      'FREEZE_CARD',
      'BLOCK_NEW_BRIDGES',
      'SET_LIMIT_ZERO',
      'RECORD_DEFAULT',
      'REFER_TO_COLLECTIONS',
    ],
    accrued_fee: accrued,
    tier_downgrade_levels: 1,
    ineligible_until: addDays(today, DELINQUENCY.defaultIneligibleMonths * 30),
  };
}
