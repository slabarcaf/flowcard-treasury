import type { Band, Door, FacilityConfig, Tier } from './types.js';

export function lendable(config: FacilityConfig): number {
  return config.facilitySize * (1 - config.reserveFloorPct);
}

export function reserveFloor(config: FacilityConfig): number {
  return config.facilitySize * config.reserveFloorPct;
}

export function deployable(config: FacilityConfig, outstanding: number): number {
  return Math.max(0, lendable(config) - outstanding);
}

export function capitalLevel(deployableAmount: number, lendableAmount: number): number {
  if (lendableAmount <= 0) return 0;
  return deployableAmount / lendableAmount;
}

/** Map a capital level fraction to the policy capital band. */
export function toBand(level: number): Band {
  if (level <= 0) return 'at_floor';
  if (level < 0.10) return 'critical';
  if (level < 0.40) return 'less';
  if (level <= 0.70) return 'normal';
  return 'a_lot';
}

const DOORS: Record<Band, Partial<Record<Tier, Door>>> = {
  a_lot: { A: 'auto', B: 'auto', C: 'auto' },
  normal: { A: 'auto', B: 'auto', C: 'human' },
  less: { A: 'auto', B: 'human', C: 'closed' },
  critical: { A: 'human', B: 'closed', C: 'closed' },
  at_floor: { A: 'closed', B: 'closed', C: 'closed' },
};

export function getDoor(band: Band, tier: Tier): Door {
  if (tier === 'none') return 'closed';
  return DOORS[band][tier] ?? 'closed';
}

export const BAND_LABELS: Record<Band, string> = {
  a_lot: 'A lot',
  normal: 'Normal',
  less: 'Less',
  critical: 'Critical',
  at_floor: 'At floor',
};
