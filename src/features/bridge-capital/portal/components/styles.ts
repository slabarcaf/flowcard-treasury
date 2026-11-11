/** Portal style constants: the scoped stylesheet for breakpoints, and shared inline styles. */
import type { CSSProperties } from 'react';
import { color, font, radius, space } from '../tokens.js';

export const PORTAL_CSS = `
.bcp-shell { display: grid; grid-template-columns: 220px minmax(0, 1fr); gap: 28px; max-width: 1320px; margin: 0 auto; padding: 24px max(16px, env(safe-area-inset-right, 0px)) 64px max(16px, env(safe-area-inset-left, 0px)); }
.bcp-nav { display: flex; flex-direction: column; gap: 2px; position: sticky; top: 24px; align-self: start; }
.bcp-g2 { display: grid; gap: 16px; grid-template-columns: repeat(2, minmax(0, 1fr)); }
.bcp-g3 { display: grid; gap: 16px; grid-template-columns: repeat(3, minmax(0, 1fr)); }
.bcp-span2 { grid-column: span 2; }
.bcp-cards { display: grid; gap: 16px; grid-template-columns: repeat(auto-fill, minmax(240px, 1fr)); }
.bcp-narrow { max-width: 640px; display: grid; }
@media (max-width: 1080px) { .bcp-g3 { grid-template-columns: repeat(2, minmax(0, 1fr)); } }
@media (max-width: 860px) {
  .bcp-shell { grid-template-columns: minmax(0, 1fr); gap: 16px; padding-top: 12px; }
  .bcp-nav { position: static; flex-direction: row; overflow-x: auto; padding-bottom: 4px; }
  .bcp-nav button { white-space: nowrap; }
  .bcp-nav-label { display: none; }
  .bcp-g2, .bcp-g3 { grid-template-columns: minmax(0, 1fr); }
  .bcp-span2 { grid-column: auto; }
}
`;

export const inputStyle: CSSProperties = {
  background: color.surfaceSubtle,
  border: 'none',
  borderRadius: radius.lg,
  padding: `${space.lg} ${space.xl}`,
  fontSize: font.size.lg,
  fontFamily: font.sans,
  color: color.text,
  fontVariantNumeric: 'tabular-nums',
  width: '100%',
};

export const linkStyle = {
  background: 'none',
  border: 'none',
  padding: 0,
  color: color.accent,
  fontSize: font.size.md,
  fontWeight: font.weight.medium,
  fontFamily: font.sans,
  cursor: 'pointer',
} as const;
