/** Portal building blocks: bigger touch targets and softer edges than the back office. */
import type { CSSProperties, ReactNode } from 'react';
import { color, font, radius, space, tone, type ToneName } from '../tokens.js';
import { Chevron } from '../../ui/Surface.js';

export function Card({ children, style }: { children: ReactNode; style?: CSSProperties }) {
  return (
    <div
      style={{
        background: color.surface,
        borderRadius: radius.xl,
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
    <div style={{ display: 'grid', gap: space.lg }}>
      <h1
        style={{
          margin: 0,
          fontSize: font.size['2xl'],
          lineHeight: 1.25,
          fontWeight: font.weight.semibold,
          letterSpacing: '-0.025em',
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
        fontWeight: font.weight.regular,
        color: color.textSecondary,
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
    primary: { background: color.accent, color: color.text },
    secondary: { background: color.surfaceSubtle, color: color.text },
    ghost: { background: 'transparent', color: color.accent },
  };
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      style={{
        ...palette[variant],
        border: 'none',
        width: '100%',
        borderRadius: radius.pill,
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
        fontWeight: font.weight.medium,
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
        <span style={{ fontSize: font.size.md, color: color.textSecondary }}>{label}</span>
      )}
      <span
        style={{
          fontSize: font.size['3xl'],
          fontWeight: font.weight.semibold,
          letterSpacing: '-0.03em',
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
        boxShadow: `inset 0 -1px 0 ${color.border}`,
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
        borderRadius: radius.xl,
        padding: space['2xl'],
        display: 'grid',
        gap: space.sm,
      }}
    >
      {title !== undefined && (
        <span style={{ fontSize: font.size.lg, fontWeight: font.weight.medium, color: t.fg }}>
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
  first = false,
}: {
  title: string;
  subtitle: ReactNode;
  amount: string;
  tone?: ToneName;
  onClick?: () => void;
  first?: boolean;
}) {
  const interactive = onClick !== undefined;
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={!interactive}
      style={{
        width: '100%',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'space-between',
        gap: space.lg,
        background: 'none',
        border: 'none',
        boxShadow: first ? 'none' : `inset 0 1px 0 ${color.border}`,
        padding: `${space.xl} 0`,
        cursor: interactive ? 'pointer' : 'default',
        textAlign: 'left',
        fontFamily: font.sans,
      }}
    >
      <span style={{ display: 'grid', gap: '2px', minWidth: 0 }}>
        <span style={{ fontSize: font.size.lg, color: color.text, fontWeight: font.weight.medium }}>
          {title}
        </span>
        <span style={{ fontSize: font.size.md, color: color.textSecondary }}>{subtitle}</span>
      </span>
      <span style={{ display: 'flex', alignItems: 'center', gap: space.md }}>
        <span
          style={{
            fontSize: font.size.lg,
            fontWeight: font.weight.medium,
            color: toneName ? tone[toneName].fg : color.text,
            fontVariantNumeric: 'tabular-nums',
            whiteSpace: 'nowrap',
          }}
        >
          {amount}
        </span>
        {interactive && <Chevron color={color.textMuted} />}
      </span>
    </button>
  );
}
