/** Platform capital regimes (spec §6). */
import type { PlatformCapital, Regime } from '../types.js';
import { REGIME_RULES, REGIME_THRESHOLDS, type RegimeRule } from './policy.config.js';
import { round2 } from './util.js';

/** Dollars that must stay in the facility. */
export function reserveFloor(capital: PlatformCapital): number {
  return round2(capital.facility_size * capital.reserve_floor_pct);
}

/** Facility minus the floor: the most that can ever be lent at once. */
export function lendable(capital: PlatformCapital): number {
  return round2(capital.facility_size - reserveFloor(capital));
}

/** Capital available above the floor right now. Never negative. */
export function availableAboveFloor(capital: PlatformCapital): number {
  return round2(Math.max(0, lendable(capital) - capital.outstanding));
}

/**
 * Spec §6: regimes are "measured on capital available above the reserve floor,
 * as % of the facility" — so the denominator is the facility, not the lendable part.
 */
export function capitalRatio(capital: PlatformCapital): number {
  if (capital.facility_size <= 0) return 0;
  return availableAboveFloor(capital) / capital.facility_size;
}

export function currentRegime(capital: PlatformCapital): Regime {
  const ratio = capitalRatio(capital);
  if (ratio <= REGIME_THRESHOLDS.atFloorAtOrBelow) return 'AT_FLOOR';
  if (ratio < REGIME_THRESHOLDS.tightAbove) return 'CRITICAL';
  if (ratio <= REGIME_THRESHOLDS.normalAbove) return 'TIGHT';
  if (ratio <= REGIME_THRESHOLDS.abundantAbove) return 'NORMAL';
  return 'ABUNDANT';
}

/** The rules that apply in a regime. */
export function regimeRule(regime: Regime): RegimeRule {
  return REGIME_RULES[regime];
}
