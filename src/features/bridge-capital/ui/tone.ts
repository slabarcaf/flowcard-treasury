/**
 * Status colours, shared by both surfaces.
 *
 * A delinquent bridge should look the same to the ops team and to the
 * customer, so these semantics live in one place even though the two surfaces
 * have different brand palettes.
 */
import type { BridgeStatus, Regime, SpendCriticality, Tier } from '../types.js';

export interface ToneStyle {
  fg: string;
  bg: string;
}

export const tone = {
  positive: { fg: '#167a4c', bg: '#e8f5ee' },
  warning: { fg: '#9a6400', bg: '#fdf3e0' },
  critical: { fg: '#b3261e', bg: '#fdeceb' },
  neutral: { fg: '#55555f', bg: '#f0f0f3' },
  accent: { fg: '#2f5fe0', bg: '#eef2fd' },
} as const satisfies Record<string, ToneStyle>;

export type ToneName = keyof typeof tone;

/** Capital regimes (spec §6). */
export const REGIME_TONE: Record<Regime, ToneName> = {
  ABUNDANT: 'positive',
  NORMAL: 'positive',
  TIGHT: 'warning',
  CRITICAL: 'critical',
  AT_FLOOR: 'critical',
};

export const TIER_TONE: Record<Tier, ToneName> = {
  A: 'positive',
  B: 'accent',
  C: 'warning',
  NONE: 'neutral',
};

export const CRITICALITY_TONE: Record<SpendCriticality, ToneName> = {
  CRITICAL: 'critical',
  STANDARD: 'neutral',
  DISCRETIONARY: 'accent',
};

/** Bridge status / delinquency stage. */
export const STAGE_TONE: Partial<Record<BridgeStatus, ToneName>> = {
  OFFERED: 'neutral',
  ACCEPTED: 'accent',
  NEEDS_APPROVAL: 'warning',
  DISBURSED: 'accent',
  COLLECTING: 'accent',
  REPAID: 'positive',
  WATCH: 'warning',
  DELINQUENT: 'warning',
  SERIOUS: 'critical',
  DEFAULTED: 'critical',
  WITHDRAWN: 'neutral',
  DECLINED: 'critical',
};

export function stageToneFor(status: string): ToneName {
  return STAGE_TONE[status as BridgeStatus] ?? 'neutral';
}
