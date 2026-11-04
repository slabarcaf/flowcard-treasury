/** Small styled building blocks. Every value comes from tokens.ts. */
import type { CSSProperties, ReactNode } from 'react';
import { color, font, radius, shadow, space, tone, type ToneName } from '../tokens.js';

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
        border: `1px solid ${color.border}`,
        borderRadius: radius.lg,
        boxShadow: shadow.card,
        padding: padded ? space['2xl'] : 0,
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
        alignItems: 'baseline',
        justifyContent: 'space-between',
        gap: space.lg,
        marginBottom: space.xl,
      }}
    >
      <h2
        style={{
          margin: 0,
          fontSize: font.size.md,
          fontWeight: font.weight.semibold,
          color: color.text,
          letterSpacing: '-0.01em',
        }}
      >
        {title}
      </h2>
      {meta !== undefined && (
        <div style={{ fontSize: font.size.sm, color: color.textMuted }}>{meta}</div>
      )}
    </div>
  );
}

/** Uppercase micro-label used above values and over table sections. */
export function Label({ children }: { children: ReactNode }) {
  return (
    <div
      style={{
        fontSize: font.size['2xs'],
        fontWeight: font.weight.semibold,
        letterSpacing: '0.06em',
        textTransform: 'uppercase',
        color: color.textMuted,
      }}
    >
      {children}
    </div>
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
        borderRadius: radius.sm,
        padding: `2px ${space.sm}`,
        fontSize: font.size.xs,
        fontWeight: font.weight.semibold,
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
    primary: { background: color.accent, color: color.textInverse, border: '1px solid transparent' },
    secondary: { background: color.surface, color: color.text, border: `1px solid ${color.borderStrong}` },
    danger: { background: color.surface, color: color.critical, border: `1px solid ${color.borderStrong}` },
  };
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      title={title}
      style={{
        ...palette[variant],
        borderRadius: radius.md,
        padding: `${space.md} ${space.lg}`,
        fontSize: font.size.md,
        fontWeight: font.weight.medium,
        fontFamily: font.sans,
        cursor: disabled ? 'not-allowed' : 'pointer',
        opacity: disabled ? 0.5 : 1,
        textAlign: 'left',
        lineHeight: 1.35,
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
          fontSize: font.size.xl,
          fontWeight: font.weight.semibold,
          color: toneName ? tone[toneName].fg : color.text,
          letterSpacing: '-0.02em',
          fontVariantNumeric: 'tabular-nums',
        }}
      >
        {value}
      </div>
      {sub !== undefined && (
        <div style={{ fontSize: font.size.sm, color: color.textMuted }}>{sub}</div>
      )}
    </div>
  );
}

/** Label / value row, used down the customer snapshot. */
export function Row({ label, value }: { label: string; value: ReactNode }) {
  return (
    <div
      style={{
        display: 'flex',
        alignItems: 'baseline',
        justifyContent: 'space-between',
        gap: space.lg,
        padding: `${space.sm} 0`,
        borderBottom: `1px solid ${color.border}`,
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
  );
}

export function Th({ children, align = 'left' }: { children: ReactNode; align?: 'left' | 'right' }) {
  return (
    <th
      style={{
        textAlign: align,
        padding: `${space.md} ${space.lg}`,
        borderBottom: `1px solid ${color.border}`,
        fontSize: font.size['2xs'],
        fontWeight: font.weight.semibold,
        letterSpacing: '0.06em',
        textTransform: 'uppercase',
        color: color.textMuted,
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
        padding: `${space.lg}`,
        borderBottom: `1px solid ${color.border}`,
        color: muted ? color.textMuted : color.text,
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
      style={{ cursor: onClick ? 'pointer' : 'default' }}
      onMouseEnter={(e) => {
        if (onClick) e.currentTarget.style.background = color.surfaceSubtle;
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
    <div style={{ display: 'flex', alignItems: 'center', gap: space.sm, fontSize: font.size.sm }}>
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
                fontSize: font.size.sm,
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
        padding: space['3xl'],
        textAlign: 'center',
        color: color.textMuted,
        fontSize: font.size.md,
      }}
    >
      {children}
    </div>
  );
}
