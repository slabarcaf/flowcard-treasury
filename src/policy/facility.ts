import type { Band, Door, Tier, FacilityConfig } from './types.js';

/**
 * Compute lendable capital (facility minus floor).
 */
export function lendable(config: FacilityConfig): number {
  return config.facilitySize * (1 - config.reserveFloorPct);
}

/**
 * Compute the reserve floor in dollars.
 */
export function reserveFloor(config: FacilityConfig): number {
  return config.facilitySize * config.reserveFloorPct;
}

/**
 * Compute deployable capital: lendable minus outstanding.
 */
export function deployable(config: FacilityConfig, outstanding: number): number {
  return Math.max(0, lendable(config) - outstanding);
}

/**
 * Capital level as a fraction of lendable capital.
 */
export function capitalLevel(deployableAmount: number, lendableAmount: number): number {
  if (lendableAmount <= 0) return 0;
  return deployableAmount / lendableAmount;
}

/**
 * Map a capital level fraction to a band.
 *
 * a_lot: >70%, normal: 40–70%, less: 10–40%, critical: <10%, at_floor: 0%.
 */
export function toBand(level: number): Band {
  if (level <= 0) return 'at_floor';
  if (level < 0.10) return 'critical';
  if (level < 0.40) return 'less';
  if (level <= 0.70) return 'normal';
  return 'a_lot';
}

/**
 * Door lookup: given a band and tier, what approval path applies?
 */
const DOORS: Record<Band, Partial<Record<Tier, Door>>> = {
  a_lot:    { A: 'auto',   B: 'auto',   C: 'auto' },
  normal:   { A: 'auto',   B: 'auto',   C: 'human' },
  less:     { A: 'auto',   B: 'human',  C: 'closed' },
  critical: { A: 'human',  B: 'closed', C: 'closed' },
  at_floor: { A: 'closed', B: 'closed', C: 'closed' },
};

export function getDoor(band: Band, tier: Tier): Door {
  if (tier === 'none') return 'closed';
  return DOORS[band][tier] ?? 'closed';
}

/**
 * Band display labels for the UI.
 */
export const BAND_LABELS: Record<Band, string> = {
  a_lot: 'A lot',
  normal: 'Normal',
  less: 'Less',
  critical: 'Critical',
  at_floor: 'At floor',
};
