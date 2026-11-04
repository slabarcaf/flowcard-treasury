/**
 * The back office's palette: the shared dark theme, at ops density.
 *
 * This is still the one file to swap for a back-office restyle. Nothing in
 * backoffice/ hard-codes a colour or a pixel value, and no Tailwind utility
 * classes are used there. Spacing, type and status tones come from ui/.
 */
import { color as theme, glow } from '../ui/theme.js';

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
export { glow };

/**
 * Key names are kept from the previous light palette so every call site still
 * reads the same; only the values moved to the dark theme. `border` is now a
 * hairline divider rather than a drawn edge — panels get their depth from tone.
 */
export const color = {
  bg: theme.canvas,
  surface: theme.surface,
  surfaceSubtle: theme.surfaceRaised,
  surfaceHover: theme.surfaceHover,

  sidebar: theme.sidebar,
  sidebarText: theme.textSecondary,
  sidebarTextActive: theme.text,
  sidebarActiveBg: theme.surfaceHover,

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
  neutral: theme.textSecondary,
  neutralSubtle: 'rgba(255, 255, 255, 0.07)',
} as const;

/** Chart geometry and colours for the balance sparkline. */
export const chart = {
  height: 96,
  strokeWidth: 1.5,
  line: theme.accent,
  fill: 'rgba(91, 108, 240, 0.18)',
  gridline: theme.divider,
  lowPoint: theme.critical,
} as const;

/** Neutrals the shared Timeline needs on a back-office card. */
export const timelineColors = {
  text: color.text,
  textMuted: color.textMuted,
  border: color.border,
} as const;
