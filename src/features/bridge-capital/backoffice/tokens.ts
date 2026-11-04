/**
 * The back office's palette.
 *
 * This is the one file to swap when the shared design style lands: nothing in
 * backoffice/ hard-codes a colour or a pixel value, and no Tailwind utility
 * classes are used there. Spacing, type and status tones come from the shared
 * ui/ layer and are re-exported here so a swap only has to touch colour.
 */
export { space, radius, font, shadow } from '../ui/scale.js';
export {
  tone,
  stageToneFor,
  REGIME_TONE,
  TIER_TONE,
  CRITICALITY_TONE,
  STAGE_TONE,
} from '../ui/tone.js';
export type { ToneName, ToneStyle } from '../ui/tone.js';

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

/** Chart geometry, so the sparkline matches the rest of the spacing scale. */
export const chart = {
  height: 96,
  strokeWidth: 1.5,
  line: color.accent,
  fill: 'rgba(47,95,224,0.10)',
  gridline: color.border,
  lowPoint: color.critical,
} as const;

/** Neutrals the shared Timeline needs to sit on a back-office card. */
export const timelineColors = {
  text: color.text,
  textMuted: color.textMuted,
  border: color.border,
} as const;
