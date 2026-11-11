/**
 * The dark theme both Bridge Capital surfaces are built on.
 *
 * Followed from the Revolut Business web reference set in
 * `Revolut Business web Dec 2025/` (gitignored), specifically:
 *   129 — Home: sidebar, borderless cards, a table inside a card, right rail
 *   263 — grouped rows with leading icons inside one panel
 *   100 — grouped rows with avatars and verified states
 *    19 — form controls: filled inputs, dropdowns, disabled pill button
 *   300 — a review/confirmation sheet
 *   505 — centred single-column layout
 *
 * What we took: the dark canvas with a blue glow, borderless panels that gain
 * depth from tone rather than 1px rules, grouped rows, pill buttons, sentence-case
 * grey labels, and the generous spacing. What we did NOT take: Revolut's palette,
 * wordmark or iconography. The accent below is FlowCard's own indigo.
 */

export const color = {
  /** Page canvas. */
  canvas: '#0B0F16',
  /** Sidebar, a shade below the canvas. */
  sidebar: '#090D13',
  /** Cards and panels. */
  surface: '#161B23',
  /** Rows, inputs, secondary buttons: one step up from a card. */
  surfaceRaised: '#1C222C',
  /** Row hover and active nav. */
  surfaceHover: '#232A36',
  /** Inset wells inside a card. */
  surfaceSunken: '#11161D',

  /** Hairline between grouped rows. Depth comes from tone; this is the only rule we draw. */
  divider: 'rgba(255, 255, 255, 0.07)',
  dividerStrong: 'rgba(255, 255, 255, 0.12)',

  text: '#FFFFFF',
  textSecondary: '#8E97A4',
  textMuted: '#69717E',
  /** On an accent or white fill. */
  textInverse: '#0B0F16',

  /** FlowCard indigo, lifted for a dark background. */
  accent: '#5B6CF0',
  accentHover: '#6E7CF4',
  accentSubtle: 'rgba(91, 108, 240, 0.16)',

  positive: '#2FD48F',
  warning: '#FFB020',
  critical: '#FF6B6B',
} as const;

/**
 * The soft blue glow in the top-left of every reference screen.
 * Applied as a background image on the shell.
 */
export const glow =
  'radial-gradient(1200px 700px at 0% -10%, rgba(45, 85, 190, 0.28) 0%, rgba(45, 85, 190, 0.08) 38%, rgba(11, 15, 22, 0) 70%)';

/** Neutrals the shared Timeline needs on a dark card. */
export const timelineColors = {
  text: color.text,
  textMuted: color.textMuted,
  border: color.divider,
} as const;
