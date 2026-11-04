/**
 * The customer portal's palette: warmer and softer than the back office.
 *
 * As with the back office, this is the one file to swap for a restyle.
 * Spacing, type and status tones come from the shared ui/ layer.
 */
export { space, radius, font, shadow } from '../ui/scale.js';
export { tone, stageToneFor, STAGE_TONE } from '../ui/tone.js';
export type { ToneName, ToneStyle } from '../ui/tone.js';

export const color = {
  /** Warm off-white canvas. */
  bg: '#faf7f4',
  surface: '#ffffff',
  surfaceSubtle: '#f5f1ec',
  surfaceSunken: '#efe9e1',

  border: '#ebe4db',
  borderStrong: '#dbd2c6',

  text: '#2a2520',
  textSecondary: '#5e564d',
  textMuted: '#8d8377',
  textInverse: '#ffffff',

  /** Terracotta: the one action colour. */
  accent: '#c04e2a',
  accentHover: '#a84222',
  accentSubtle: '#fdf1ec',

  positive: '#167a4c',
  positiveSubtle: '#e9f5ee',
  warning: '#8a5a00',
  warningSubtle: '#fdf3e2',
  critical: '#ad281f',
  criticalSubtle: '#fcece9',
} as const;

/** The portal is a single phone-width column, centred on larger screens. */
export const layout = {
  maxWidth: '440px',
  gutter: '16px',
} as const;

/** Neutrals the shared Timeline needs to sit on a portal card. */
export const timelineColors = {
  text: color.text,
  textMuted: color.textMuted,
  border: color.border,
} as const;
