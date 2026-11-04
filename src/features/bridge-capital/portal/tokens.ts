/**
 * The customer portal's palette: the same dark theme as the back office, with
 * roomier controls and bigger touch targets.
 *
 * As with the back office, this is the one file to swap for a restyle.
 */
import { color as theme, glow } from '../ui/theme.js';

export { space, radius, font, shadow } from '../ui/scale.js';
export { tone, stageToneFor, STAGE_TONE } from '../ui/tone.js';
export type { ToneName, ToneStyle } from '../ui/tone.js';
export { glow };

export const color = {
  bg: theme.canvas,
  surface: theme.surface,
  surfaceSubtle: theme.surfaceRaised,
  surfaceSunken: theme.surfaceSunken,
  surfaceHover: theme.surfaceHover,

  border: theme.divider,
  borderStrong: theme.dividerStrong,

  text: theme.text,
  textSecondary: theme.textSecondary,
  textMuted: theme.textMuted,
  textInverse: theme.textInverse,

  accent: theme.accent,
  accentHover: theme.accentHover,
  accentSubtle: theme.accentSubtle,

  positive: theme.positive,
  positiveSubtle: 'rgba(47, 212, 143, 0.14)',
  warning: theme.warning,
  warningSubtle: 'rgba(255, 176, 32, 0.14)',
  critical: theme.critical,
  criticalSubtle: 'rgba(255, 107, 107, 0.14)',
} as const;

/** The portal is a single phone-width column, centred on larger screens. */
export const layout = {
  maxWidth: '440px',
  gutter: '16px',
} as const;

/** Neutrals the shared Timeline needs on a portal card. */
export const timelineColors = {
  text: color.text,
  textMuted: color.textMuted,
  border: color.border,
} as const;
