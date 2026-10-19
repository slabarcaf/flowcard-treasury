import type { Tier, FacilityConfig } from './types.js';

/** Tier-to-multiplier mapping (share of monthly inflow). */
const MULTIPLIERS: Record<Exclude<Tier, 'none'>, number> = {
  A: 0.30,
  B: 0.20,
  C: 0.10,
};

/**
 * Calculate a customer's credit line.
 *
 * credit line = monthly inflow * multiplier, capped at facility * maxPerCustomerPct.
 * Returns 0 for tier 'none'.
 */
export function computeCreditLine(
  tier: Tier,
  monthlyInflow: number,
  config: FacilityConfig,
): number {
  if (tier === 'none') return 0;
  const raw = monthlyInflow * MULTIPLIERS[tier];
  const cap = config.facilitySize * config.maxPerCustomerPct;
  return Math.min(raw, cap);
}
