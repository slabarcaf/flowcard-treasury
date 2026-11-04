/** Portal building blocks: bigger touch targets and softer edges than the back office. */
import type { CSSProperties, ReactNode } from 'react';
import { color, font, radius, shadow, space, tone, type ToneName } from '../tokens.js';

export function Card({ children, style }: { children: ReactNode; style?: CSSProperties }) {
  return (
    <div
      style={{
        background: color.surface,
        border: `1px solid ${color.border}`,
        borderRadius: radius.xl,
        boxShadow: shadow.card,
        padding: space['2xl'],
        ...style,
      }}
    >
      {children}
    </div>
  );
}

export function Heading({ children, sub }: { children: ReactNode; sub?: ReactNode }) {
  return (
    <div style={{ display: 'grid', gap: space.md }}>
      <h1
        style={{
          margin: 0,
          fontSize: font.size['2xl'],
          lineHeight: 1.25,
          fontWeight: font.weight.semibold,
          letterSpacing: '-0.02em',
          color: color.text,
        }}
      >
        {children}
      </h1>
      {sub !== undefined && (
        <p style={{ margin: 0, fontSize: font.size.lg, lineHeight: 1.55, color: color.textSecondary }}>
          {sub}
        </p>
      )}
    </div>
  );
}

export function SectionTitle({ children }: { children: ReactNode }) {
  return (
    <h2
      style={{
        margin: 0,
        fontSize: font.size.md,
        fontWeight: font.weight.semibold,
        color: color.textSecondary,
        letterSpacing: '0.02em',
      }}
    >
      {children}
    </h2>
  );
}

export function Button({
  children,
  onClick,
  variant = 'primary',
  disabled = false,
}: {
  children: ReactNode;
  onClick?: () => void;
  variant?: 'primary' | 'secondary' | 'ghost';
  disabled?: boolean;
}) {
  const palette: Record<string, CSSProperties> = {
    primary: { background: color.accent, color: color.textInverse, border: '1px solid transparent' },
    secondary: { background: color.surface, color: color.text, border: `1px solid ${color.borderStrong}` },
    ghost: { background: 'transparent', color: color.accent, border: '1px solid transparent' },
  };
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      style={{
        ...palette[variant],
        width: '100%',
        borderRadius: radius.lg,
        padding: `${space.xl} ${space['2xl']}`,
        fontSize: font.size.lg,
        fontWeight: font.weight.medium,
        fontFamily: font.sans,
        cursor: disabled ? 'not-allowed' : 'pointer',
        opacity: disabled ? 0.45 : 1,
        lineHeight: 1.4,
      }}
    >
      {children}
    </button>
  );
}

export function Pill({ children, tone: toneName = 'neutral' }: { children: ReactNode; tone?: ToneName }) {
  const t = tone[toneName];
  return (
    <span
      style={{
        display: 'inline-flex',
        alignItems: 'center',
        background: t.bg,
        color: t.fg,
        borderRadius: radius.pill,
        padding: `${space.xs} ${space.lg}`,
        fontSize: font.size.sm,
        fontWeight: font.weight.semibold,
        whiteSpace: 'nowrap',
      }}
    >
      {children}
    </span>
  );
}

/** Big headline number, for balances and bridge amounts. */
export function Amount({ value, label, tone: toneName }: { value: string; label?: string; tone?: ToneName }) {
  return (
    <div style={{ display: 'grid', gap: space.xs }}>
      {label !== undefined && (
        <span style={{ fontSize: font.size.md, color: color.textMuted }}>{label}</span>
      )}
      <span
        style={{
          fontSize: font.size['3xl'],
          fontWeight: font.weight.semibold,
          letterSpacing: '-0.025em',
          color: toneName ? tone[toneName].fg : color.text,
          fontVariantNumeric: 'tabular-nums',
        }}
      >
        {value}
      </span>
    </div>
  );
}

export function Row({ label, value, strong = false }: { label: ReactNode; value: ReactNode; strong?: boolean }) {
  return (
    <div
      style={{
        display: 'flex',
        alignItems: 'baseline',
        justifyContent: 'space-between',
        gap: space.lg,
        padding: `${space.lg} 0`,
        borderBottom: `1px solid ${color.border}`,
        fontSize: font.size.lg,
      }}
    >
      <span style={{ color: color.textSecondary }}>{label}</span>
      <span
        style={{
          color: color.text,
          fontWeight: strong ? font.weight.semibold : font.weight.medium,
          fontVariantNumeric: 'tabular-nums',
          textAlign: 'right',
        }}
      >
        {value}
      </span>
    </div>
  );
}

/** A soft panel for the things a customer needs to read, not skim. */
export function Callout({
  children,
  tone: toneName = 'neutral',
  title,
}: {
  children: ReactNode;
  tone?: ToneName;
  title?: string;
}) {
  const t = tone[toneName];
  return (
    <div
      style={{
        background: t.bg,
        borderRadius: radius.lg,
        padding: space.xl,
        display: 'grid',
        gap: space.sm,
      }}
    >
      {title !== undefined && (
        <span style={{ fontSize: font.size.md, fontWeight: font.weight.semibold, color: t.fg }}>
          {title}
        </span>
      )}
      <span style={{ fontSize: font.size.md, lineHeight: 1.6, color: color.textSecondary }}>
        {children}
      </span>
    </div>
  );
}

export function BackLink({ onClick, children }: { onClick: () => void; children: ReactNode }) {
  return (
    <button
      type="button"
      onClick={onClick}
      style={{
        background: 'none',
        border: 'none',
        padding: 0,
        color: color.textSecondary,
        fontSize: font.size.lg,
        fontFamily: font.sans,
        cursor: 'pointer',
        textAlign: 'left',
      }}
    >
      ← {children}
    </button>
  );
}

/** Tappable list row, used for transactions. */
export function ListRow({
  title,
  subtitle,
  amount,
  tone: toneName,
  onClick,
}: {
  title: string;
  subtitle: ReactNode;
  amount: string;
  tone?: ToneName;
  onClick?: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={onClick === undefined}
      style={{
        width: '100%',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'space-between',
        gap: space.lg,
        background: 'none',
        border: 'none',
        borderBottom: `1px solid ${color.border}`,
        padding: `${space.xl} 0`,
        cursor: onClick === undefined ? 'default' : 'pointer',
        textAlign: 'left',
        fontFamily: font.sans,
      }}
    >
      <span style={{ display: 'grid', gap: space.xs, minWidth: 0 }}>
        <span style={{ fontSize: font.size.lg, color: color.text, fontWeight: font.weight.medium }}>
          {title}
        </span>
        <span style={{ fontSize: font.size.md, color: color.textMuted }}>{subtitle}</span>
      </span>
      <span
        style={{
          fontSize: font.size.lg,
          fontWeight: font.weight.semibold,
          color: toneName ? tone[toneName].fg : color.text,
          fontVariantNumeric: 'tabular-nums',
          whiteSpace: 'nowrap',
        }}
      >
        {amount}
      </span>
    </button>
  );
}
