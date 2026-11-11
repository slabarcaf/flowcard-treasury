/**
 * What a customer owes on a bridge today, and applying a repayment from the
 * wallet. Late fees accrue from days past due (spec §5); what has already been
 * paid toward them is remembered so a part-payment never pays them twice.
 */
import type { Bridge } from '../types.js';
import { applyDelinquency } from './delinquency.js';
import { collect, type CollectionResult } from './collection.js';
import { round2 } from './util.js';

/** Late fee accrued to date and not yet paid. */
export function lateFeeOwed(bridge: Bridge, today: string): number {
  const accrued = applyDelinquency(bridge, today).accrued_fee;
  return round2(Math.max(0, accrued - (bridge.accrued_paid ?? 0)));
}

/** Principal + fee + unpaid late fee. */
export function amountDue(bridge: Bridge, today: string): number {
  return round2(bridge.principal_outstanding + bridge.fee_outstanding + lateFeeOwed(bridge, today));
}

/** Apply a payment: late fee first, then the fee, then principal (the §5 collection order). */
export function applyRepayment(bridge: Bridge, amount: number, today: string): CollectionResult {
  const late = lateFeeOwed(bridge, today);
  const result = collect({ ...bridge, accrued_fee: late }, amount);
  const lateFeePaid = round2(Math.min(result.applied_to_fee, late));
  return {
    ...result,
    bridge: {
      ...result.bridge,
      accrued_fee: 0,
      accrued_paid: round2((bridge.accrued_paid ?? 0) + lateFeePaid),
    },
  };
}
