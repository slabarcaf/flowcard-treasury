/**
 * The two structural pieces both surfaces share: the glow background every
 * screen sits on, and the grouped-row panel that carries most of the content.
 *
 * Both take their neutrals as props, so each surface passes its own tokens and
 * neither has to import the other's.
 */
import type { CSSProperties, ReactNode } from 'react';
import { font, radius, space } from './scale.js';

export interface SurfaceColors {
  surface: string;
  divider: string;
  text: string;
  textSecondary: string;
  textMuted: string;
  hover: string;
}

/** The dark canvas with its soft blue glow in the top-left. */
export function GlowBackground({
  canvas,
  glow,
  children,
  style,
}: {
  canvas: string;
  glow: string;
  children: ReactNode;
  style?: CSSProperties;
}) {
  return (
    <div
      style={{
        minHeight: '100vh',
        background: canvas,
        backgroundImage: glow,
        backgroundRepeat: 'no-repeat',
        backgroundAttachment: 'fixed',
        ...style,
      }}
    >
      {children}
    </div>
  );
}

/** Sentence-case grey label above a panel. The reference never shouts in caps. */
export function GroupLabel({ children, colors }: { children: ReactNode; colors: SurfaceColors }) {
  return (
    <div
      style={{
        fontSize: font.size.md,
        fontWeight: font.weight.medium,
        color: colors.textSecondary,
        marginBottom: space.lg,
      }}
    >
      {children}
    </div>
  );
}

/**
 * A borderless panel holding rows. Depth comes from the fill, not an outline;
 * rows are separated by hairlines that stop short of the panel edge.
 */
export function GroupPanel({
  children,
  colors,
  style,
}: {
  children: ReactNode;
  colors: SurfaceColors;
  style?: CSSProperties;
}) {
  return (
    <div
      style={{
        background: colors.surface,
        borderRadius: radius.xl,
        overflow: 'hidden',
        ...style,
      }}
    >
      {children}
    </div>
  );
}

export function Chevron({ color }: { color: string }) {
  return (
    <svg width="16" height="16" viewBox="0 0 16 16" fill="none" aria-hidden style={{ flexShrink: 0 }}>
      <path
        d="M6 3.5L10.5 8L6 12.5"
        stroke={color}
        strokeWidth="1.6"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  );
}

/**
 * One row in a GroupPanel: optional leading mark, a title with a grey
 * subtitle, an optional value on the right, and a chevron when it is tappable.
 */
export function GroupRow({
  leading,
  title,
  subtitle,
  value,
  valueColor,
  chevron = false,
  first = false,
  onClick,
  colors,
}: {
  leading?: ReactNode;
  title: ReactNode;
  subtitle?: ReactNode;
  value?: ReactNode;
  valueColor?: string;
  chevron?: boolean;
  /** Skips the top hairline. */
  first?: boolean;
  onClick?: () => void;
  colors: SurfaceColors;
}) {
  const interactive = onClick !== undefined;
  return (
    <div
      role={interactive ? 'button' : undefined}
      tabIndex={interactive ? 0 : undefined}
      onClick={onClick}
      onKeyDown={
        interactive
          ? (e) => {
              if (e.key === 'Enter' || e.key === ' ') {
                e.preventDefault();
                onClick();
              }
            }
          : undefined
      }
      onMouseEnter={(e) => {
        if (interactive) e.currentTarget.style.background = colors.hover;
      }}
      onMouseLeave={(e) => {
        if (interactive) e.currentTarget.style.background = 'transparent';
      }}
      style={{
        display: 'flex',
        alignItems: 'center',
        gap: space.xl,
        padding: `${space.xl} ${space['2xl']}`,
        boxShadow: first ? 'none' : `inset 0 1px 0 ${colors.divider}`,
        cursor: interactive ? 'pointer' : 'default',
        transition: 'background 120ms ease',
        fontFamily: font.sans,
      }}
    >
      {leading}
      <div style={{ display: 'grid', gap: '2px', minWidth: 0, flex: 1 }}>
        <span style={{ fontSize: font.size.lg, color: colors.text, fontWeight: font.weight.medium }}>
          {title}
        </span>
        {subtitle !== undefined && (
          <span style={{ fontSize: font.size.md, color: colors.textSecondary }}>{subtitle}</span>
        )}
      </div>
      {value !== undefined && (
        <span
          style={{
            fontSize: font.size.lg,
            fontWeight: font.weight.medium,
            color: valueColor ?? colors.text,
            fontVariantNumeric: 'tabular-nums',
            whiteSpace: 'nowrap',
          }}
        >
          {value}
        </span>
      )}
      {chevron && <Chevron color={colors.textMuted} />}
    </div>
  );
}
