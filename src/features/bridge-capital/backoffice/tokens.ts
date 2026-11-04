/**
 * Every colour, space, radius and type size the back office uses.
 *
 * This is the one file to swap when the shared design style lands: nothing in
 * backoffice/ hard-codes a colour or a pixel value, and no Tailwind utility
 * classes are used for anything but nothing — layout included comes from here.
 */
import type { BridgeStatus, Regime, SpendCriticality, Tier } from '../types.js';

export const color = {
  /** Page canvas. */
  bg: '#f7f7f8',
  /** Cards, tables, headers. */
  surface: '#ffffff',
  /** Zebra rows, inset panels, chart fills. */
  surfaceSubtle: '#f3f3f5',
  surfaceHover: '#eeeef1',
  /** Sidebar. */
  sidebar: '#16161a',
  sidebarText: '#9c9ca8',
  sidebarTextActive: '#ffffff',
  sidebarActiveBg: 'rgba(255,255,255,0.08)',

  border: '#e6e6ea',
  borderStrong: '#d2d2d9',

  text: '#16161a',
  textSecondary: '#55555f',
  textMuted: '#8b8b96',
  textInverse: '#ffffff',

  accent: '#2f5fe0',
  accentHover: '#2750c4',
  accentSubtle: '#eef2fd',

  positive: '#167a4c',
  positiveSubtle: '#e8f5ee',
  warning: '#9a6400',
  warningSubtle: '#fdf3e0',
  critical: '#b3261e',
  criticalSubtle: '#fdeceb',
  neutral: '#55555f',
  neutralSubtle: '#f0f0f3',
} as const;

/** 4px base scale. */
export const space = {
  px: '1px',
  xs: '4px',
  sm: '6px',
  md: '8px',
  lg: '12px',
  xl: '16px',
  '2xl': '20px',
  '3xl': '24px',
  '4xl': '32px',
} as const;

export const radius = {
  sm: '4px',
  md: '6px',
  lg: '8px',
  pill: '999px',
} as const;

export const font = {
  sans: "-apple-system, BlinkMacSystemFont, 'Inter', 'Segoe UI', sans-serif",
  mono: "ui-monospace, SFMono-Regular, 'SF Mono', Menlo, monospace",
  size: {
    '2xs': '10px',
    xs: '11px',
    sm: '12px',
    md: '13px',
    lg: '15px',
    xl: '18px',
    '2xl': '22px',
    '3xl': '28px',
  },
  weight: {
    regular: 400,
    medium: 500,
    semibold: 600,
  },
} as const;

export const shadow = {
  card: '0 1px 2px rgba(16,16,20,0.04)',
  raised: '0 2px 8px rgba(16,16,20,0.08)',
} as const;

/** Chart geometry, so the sparkline matches the rest of the spacing scale. */
export const chart = {
  height: 96,
  strokeWidth: 1.5,
  line: color.accent,
  fill: 'rgba(47,95,224,0.10)',
  gridline: color.border,
  lowPoint: color.critical,
} as const;

export interface ToneStyle {
  fg: string;
  bg: string;
}

export const tone = {
  positive: { fg: color.positive, bg: color.positiveSubtle },
  warning: { fg: color.warning, bg: color.warningSubtle },
  critical: { fg: color.critical, bg: color.criticalSubtle },
  neutral: { fg: color.neutral, bg: color.neutralSubtle },
  accent: { fg: color.accent, bg: color.accentSubtle },
} as const satisfies Record<string, ToneStyle>;

export type ToneName = keyof typeof tone;

/** Capital regimes, worst to best. */
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
