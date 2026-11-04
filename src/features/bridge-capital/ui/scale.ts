/**
 * Spacing, radii and type, shared by the back office and the customer portal.
 *
 * Colour is NOT here: it lives in ui/theme.ts, which both surfaces share, with
 * per-surface overrides in their own tokens files.
 */

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

/** The reference leans on large radii: 16px panels, 12px controls, pill buttons. */
export const radius = {
  sm: '6px',
  md: '8px',
  /** Controls: inputs, dropdowns, segmented buttons. */
  lg: '12px',
  /** Cards and panels. */
  xl: '16px',
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
    '4xl': '34px',
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
