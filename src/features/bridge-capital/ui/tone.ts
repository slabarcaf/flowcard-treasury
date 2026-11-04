/**
 * Status colours, shared by both surfaces, tuned for the dark theme.
 *
 * A delinquent bridge should look the same to the ops team and to the
 * customer, so these semantics live in one place. Fills are a low-alpha wash
 * of the foreground, which is how the reference screens tint a status chip on
 * a dark panel — a solid pastel would glow.
 */
import type { BridgeStatus, Regime, SpendCriticality, Tier } from '../types.js';
import { color } from './theme.js';

export interface ToneStyle {
  fg: string;
  bg: string;
}

export const tone = {
  positive: { fg: color.positive, bg: 'rgba(47, 212, 143, 0.14)' },
  warning: { fg: color.warning, bg: 'rgba(255, 176, 32, 0.14)' },
  critical: { fg: color.critical, bg: 'rgba(255, 107, 107, 0.14)' },
  neutral: { fg: color.textSecondary, bg: 'rgba(255, 255, 255, 0.07)' },
  accent: { fg: color.accent, bg: color.accentSubtle },
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
