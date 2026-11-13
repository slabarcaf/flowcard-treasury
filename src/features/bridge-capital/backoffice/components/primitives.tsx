/** Small styled building blocks. Every value comes from tokens.ts. */
import type { CSSProperties, ReactNode } from 'react';
import { color, font, radius, space, tone, type ToneName } from '../tokens.js';

export function Card({
  children,
  style,
  padded = true,
}: {
  children: ReactNode;
  style?: CSSProperties;
  padded?: boolean;
}) {
  return (
    <div
      style={{
        background: color.surface,
        borderRadius: radius.xl,
        padding: padded ? space['3xl'] : 0,
        overflow: 'hidden',
        ...style,
      }}
    >
      {children}
    </div>
  );
}

export function CardHeader({ title, meta }: { title: string; meta?: ReactNode }) {
  return (
    <div
      style={{
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'space-between',
        gap: space.lg,
        marginBottom: space['2xl'],
      }}
    >
      <h2
        style={{
          margin: 0,
          fontSize: font.size.lg,
          fontWeight: font.weight.semibold,
          color: color.text,
          letterSpacing: '-0.01em',
        }}
      >
        {title}
      </h2>
      {meta !== undefined && (
        <div style={{ fontSize: font.size.md, color: color.textSecondary }}>{meta}</div>
      )}
    </div>
  );
}

/** Grey label above a value. Sentence case — the reference never shouts in caps. */
export function Label({ children }: { children: ReactNode }) {
  return (
    <div style={{ fontSize: font.size.md, color: color.textSecondary }}>{children}</div>
  );
}

export function Badge({
  children,
  tone: toneName = 'neutral',
  mono = false,
}: {
  children: ReactNode;
  tone?: ToneName;
  mono?: boolean;
}) {
  const t = tone[toneName];
  return (
    <span
      style={{
        display: 'inline-flex',
        alignItems: 'center',
        gap: space.xs,
        background: t.bg,
        color: t.fg,
        borderRadius: radius.pill,
        padding: `3px ${space.lg}`,
        fontSize: font.size.sm,
        fontWeight: font.weight.medium,
        fontFamily: mono ? font.mono : font.sans,
        whiteSpace: 'nowrap',
      }}
    >
      {children}
    </span>
  );
}

export function Button({
  children,
  onClick,
  variant = 'secondary',
  disabled = false,
  title,
}: {
  children: ReactNode;
  onClick?: () => void;
  variant?: 'primary' | 'secondary' | 'danger';
  disabled?: boolean;
  title?: string;
}) {
  const palette: Record<string, CSSProperties> = {
    primary: { background: color.accent, color: color.text },
    secondary: { background: color.surfaceSubtle, color: color.text },
    danger: { background: color.surfaceSubtle, color: color.critical },
  };
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      title={title}
      style={{
        ...palette[variant],
        border: 'none',
        borderRadius: radius.pill,
        padding: `${space.lg} ${space['2xl']}`,
        fontSize: font.size.md,
        fontWeight: font.weight.medium,
        fontFamily: font.sans,
        cursor: disabled ? 'not-allowed' : 'pointer',
        opacity: disabled ? 0.45 : 1,
        textAlign: 'left',
        lineHeight: 1.4,
      }}
    >
      {children}
    </button>
  );
}

/** A number with its label above it. The back office is full of these. */
export function Stat({
  label,
  value,
  sub,
  tone: toneName,
}: {
  label: string;
  value: ReactNode;
  sub?: ReactNode;
  tone?: ToneName;
}) {
  return (
    <div style={{ display: 'grid', gap: space.xs, minWidth: 0 }}>
      <Label>{label}</Label>
      <div
        style={{
          fontSize: font.size['2xl'],
          fontWeight: font.weight.semibold,
          color: toneName ? tone[toneName].fg : color.text,
          letterSpacing: '-0.02em',
          fontVariantNumeric: 'tabular-nums',
        }}
      >
        {value}
      </div>
      {sub !== undefined && (
        <div style={{ fontSize: font.size.md, color: color.textMuted }}>{sub}</div>
      )}
    </div>
  );
}

/** Label / value row, used down the customer snapshot. */
export function Row({ label, value }: { label: ReactNode; value: ReactNode }) {
  return (
    <div
      style={{
        display: 'flex',
        alignItems: 'baseline',
        justifyContent: 'space-between',
        gap: space.lg,
        padding: `${space.lg} 0`,
        boxShadow: `inset 0 -1px 0 ${color.border}`,
        fontSize: font.size.md,
      }}
    >
      <span style={{ color: color.textSecondary }}>{label}</span>
      <span
        style={{
          color: color.text,
          fontWeight: font.weight.medium,
          fontVariantNumeric: 'tabular-nums',
          textAlign: 'right',
        }}
      >
        {value}
      </span>
    </div>
  );
}

export function Table({ children }: { children: ReactNode }) {
  return (
    <div style={{ overflowX: 'auto' }}>
    <table
      style={{
        width: '100%',
        borderCollapse: 'collapse',
        fontSize: font.size.md,
        fontFamily: font.sans,
      }}
    >
      {children}
    </table>
    </div>
  );
}

export function Th({ children, align = 'left' }: { children: ReactNode; align?: 'left' | 'right' }) {
  return (
    <th
      style={{
        textAlign: align,
        padding: `${space.md} ${space.lg}`,
        boxShadow: `inset 0 -1px 0 ${color.border}`,
        fontSize: font.size.md,
        fontWeight: font.weight.regular,
        color: color.textSecondary,
        whiteSpace: 'nowrap',
      }}
    >
      {children}
    </th>
  );
}

export function Td({
  children,
  align = 'left',
  mono = false,
  muted = false,
}: {
  children: ReactNode;
  align?: 'left' | 'right';
  mono?: boolean;
  muted?: boolean;
}) {
  return (
    <td
      style={{
        textAlign: align,
        padding: `${space.xl} ${space.lg}`,
        boxShadow: `inset 0 -1px 0 ${color.border}`,
        color: muted ? color.textSecondary : color.text,
        fontFamily: mono ? font.mono : font.sans,
        fontVariantNumeric: 'tabular-nums',
        verticalAlign: 'middle',
      }}
    >
      {children}
    </td>
  );
}

/** Clickable table row. */
export function ClickableTr({ children, onClick }: { children: ReactNode; onClick?: () => void }) {
  return (
    <tr
      onClick={onClick}
      style={{ cursor: onClick ? 'pointer' : 'default', transition: 'background 120ms ease' }}
      onMouseEnter={(e) => {
        if (onClick) e.currentTarget.style.background = color.surfaceHover;
      }}
      onMouseLeave={(e) => {
        if (onClick) e.currentTarget.style.background = 'transparent';
      }}
    >
      {children}
    </tr>
  );
}

export function Breadcrumb({ items }: { items: Array<{ label: string; onClick?: () => void }> }) {
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: space.sm, fontSize: font.size.md }}>
      {items.map((item, i) => (
        <span key={item.label} style={{ display: 'flex', alignItems: 'center', gap: space.sm }}>
          {i > 0 && <span style={{ color: color.textMuted }}>/</span>}
          {item.onClick ? (
            <button
              type="button"
              onClick={item.onClick}
              style={{
                background: 'none',
                border: 'none',
                padding: 0,
                color: color.accent,
                fontSize: font.size.md,
                fontFamily: font.sans,
                cursor: 'pointer',
              }}
            >
              {item.label}
            </button>
          ) : (
            <span style={{ color: color.textMuted }}>{item.label}</span>
          )}
        </span>
      ))}
    </div>
  );
}

export function Empty({ children }: { children: ReactNode }) {
  return (
    <div
      style={{
        padding: space['4xl'],
        textAlign: 'center',
        color: color.textMuted,
        fontSize: font.size.md,
      }}
    >
      {children}
    </div>
  );
}
