import type { FacilityConfig, RepaymentHistory, Signals, Tier } from './types.js';

const MULTIPLIERS: Record<Exclude<Tier, 'none'>, number> = {
  A: 0.30,
  B: 0.20,
  C: 0.10,
};

/** Maximum exposure for a starter borrower who has not reached promotion. */
export function starterMultiplier(
  tier: Tier,
  signals: Signals,
  repaymentHistory: RepaymentHistory,
): number {
  if (repaymentHistory.fullyRepaidOnTimeBridges >= 3) {
    return MULTIPLIERS[tier === 'none' ? 'C' : tier];
  }
  const otherSignals = [
    signals.inflowReliability,
    signals.inflowConcentration,
    signals.cashBuffer,
  ];
  return otherSignals.every((rating) => rating === 'green') ? 0.10 : 0.05;
}

/**
 * Calculate a credit line, limited by the customer facility cap and approved
 * bridge cap. Supplying ledger repayment history applies starter limits until
 * three fully repaid on-time bridges have been recorded.
 */
export function computeCreditLine(
  tier: Tier,
  monthlyInflow: number,
  config: FacilityConfig,
  signals?: Signals,
  repaymentHistory?: RepaymentHistory,
): number {
  if (tier === 'none' || monthlyInflow <= 0) return 0;
  const multiplier = signals && repaymentHistory
    ? starterMultiplier(tier, signals, repaymentHistory)
    : MULTIPLIERS[tier];
  const customerCap = config.facilitySize * config.maxPerCustomerPct;
  const approvedCap = config.approvedBridgeCap ?? Number.POSITIVE_INFINITY;
  return Math.max(0, Math.min(monthlyInflow * multiplier, customerCap, approvedCap));
}
