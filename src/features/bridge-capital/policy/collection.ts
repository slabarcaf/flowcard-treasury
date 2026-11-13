/** Collecting a bridge from incoming deposits (spec §5). */
import type { Bridge, BridgeStatus, Deposit } from '../types.js';
import { round2 } from './util.js';

export interface CollectionResult {
  /** The bridge after the deposit was applied. The input is not mutated. */
  bridge: Bridge;
  applied: number;
  applied_to_fee: number;
  applied_to_principal: number;
  /** Principal + fee still owed after this deposit. */
  remaining: number;
  /** Deposit left over once the bridge is fully repaid. */
  excess: number;
  fully_repaid: boolean;
}

/** Statuses that a part-payment should move to COLLECTING; the ladder's own are kept. */
const PRE_COLLECTION: BridgeStatus[] = ['OFFERED', 'ACCEPTED', 'NEEDS_APPROVAL', 'DISBURSED'];

/**
 * Apply a deposit to a bridge: fee first (including anything accrued while late),
 * then principal.
 *
 * The spec does not fix the order within "principal + fee auto-collected"; fee
 * first matches the collection waterfall the dashboard already shows.
 */
export function collect(bridge: Bridge, deposit: Deposit | number): CollectionResult {
  const amount = typeof deposit === 'number' ? deposit : deposit.amount;
  if (amount < 0) {
    throw new RangeError(`deposit must not be negative, got ${amount}`);
  }

  const feeOwed = round2(bridge.fee_outstanding + bridge.accrued_fee);
  const toFee = round2(Math.min(amount, feeOwed));
  const toPrincipal = round2(Math.min(amount - toFee, bridge.principal_outstanding));
  const applied = round2(toFee + toPrincipal);

  // Accrued fee is drawn down before the contracted fee.
  const accruedPaid = round2(Math.min(toFee, bridge.accrued_fee));
  const accruedLeft = round2(bridge.accrued_fee - accruedPaid);
  const feeLeft = round2(bridge.fee_outstanding - (toFee - accruedPaid));
  const principalLeft = round2(bridge.principal_outstanding - toPrincipal);
  const remaining = round2(principalLeft + feeLeft + accruedLeft);
  const fullyRepaid = remaining <= 0;

  const status: BridgeStatus = fullyRepaid
    ? 'REPAID'
    : PRE_COLLECTION.includes(bridge.status)
      ? 'COLLECTING'
      : bridge.status;

  return {
    bridge: {
      ...bridge,
      principal_outstanding: principalLeft,
      fee_outstanding: feeLeft,
      accrued_fee: accruedLeft,
      status,
    },
    applied,
    applied_to_fee: toFee,
    applied_to_principal: toPrincipal,
    remaining,
    excess: round2(amount - applied),
    fully_repaid: fullyRepaid,
  };
}
