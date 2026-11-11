/**
 * Desktop layout pieces for the portal: page header, panels and responsive
 * grids. Inline styles cannot hold media queries, so the few breakpoints live
 * in one scoped stylesheet the shell renders once.
 */
import type { CSSProperties, ReactNode } from 'react';
import { color, font, radius, space, tone, type ToneName } from '../tokens.js';


export function PageHeader({ title, sub, action }: { title: ReactNode; sub?: ReactNode; action?: ReactNode }) {
  return (
    <div style={{ display: 'flex', alignItems: 'flex-end', justifyContent: 'space-between', gap: space.lg, flexWrap: 'wrap' }}>
      <div style={{ display: 'grid', gap: space.sm }}>
        <h1 style={{ margin: 0, fontSize: font.size['3xl'], fontWeight: font.weight.semibold, letterSpacing: '-0.025em', color: color.text }}>
          {title}
        </h1>
        {sub !== undefined && <p style={{ margin: 0, fontSize: font.size.lg, color: color.textSecondary }}>{sub}</p>}
      </div>
      {action}
    </div>
  );
}

export function Panel({
  title,
  meta,
  children,
  style,
  className,
}: {
  title?: ReactNode;
  meta?: ReactNode;
  children: ReactNode;
  style?: CSSProperties;
  className?: string;
}) {
  return (
    <section className={className} style={{ background: color.surface, borderRadius: radius.xl, padding: space['2xl'], minWidth: 0, ...style }}>
      {(title !== undefined || meta !== undefined) && (
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: space.lg, marginBottom: space.xl, flexWrap: 'wrap' }}>
          {title !== undefined && (
            <h2 style={{ margin: 0, fontSize: font.size.lg, fontWeight: font.weight.semibold, color: color.text }}>{title}</h2>
          )}
          {meta !== undefined && <span style={{ fontSize: font.size.md, color: color.textSecondary }}>{meta}</span>}
        </div>
      )}
      {children}
    </section>
  );
}

export function Kpi({ label, value, sub, valueColor }: { label: string; value: ReactNode; sub?: ReactNode; valueColor?: string }) {
  return (
    <div style={{ display: 'grid', gap: '2px', minWidth: 0 }}>
      <span style={{ fontSize: font.size.md, color: color.textSecondary }}>{label}</span>
      <span
        style={{
          fontSize: font.size['2xl'],
          fontWeight: font.weight.semibold,
          letterSpacing: '-0.02em',
          color: valueColor ?? color.text,
          fontVariantNumeric: 'tabular-nums',
        }}
      >
        {value}
      </span>
      {sub !== undefined && <span style={{ fontSize: font.size.sm, color: color.textMuted }}>{sub}</span>}
    </div>
  );
}

/** A full-width notice with an optional action on the right. */
export function Banner({
  title,
  children,
  tone: toneName = 'neutral',
  action,
}: {
  title: ReactNode;
  children?: ReactNode;
  tone?: ToneName;
  action?: ReactNode;
}) {
  const t = tone[toneName];
  return (
    <div
      style={{
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'space-between',
        gap: space.xl,
        flexWrap: 'wrap',
        background: t.bg,
        borderRadius: radius.xl,
        padding: `${space.xl} ${space['2xl']}`,
      }}
    >
      <div style={{ display: 'grid', gap: space.xs, minWidth: 0, flex: '1 1 260px' }}>
        <span style={{ fontSize: font.size.lg, fontWeight: font.weight.semibold, color: t.fg }}>{title}</span>
        {children !== undefined && <span style={{ fontSize: font.size.md, lineHeight: 1.55, color: color.textSecondary }}>{children}</span>}
      </div>
      {action}
    </div>
  );
}

/** A compact pill button for inline actions. */
export function SmallButton({
  children,
  onClick,
  variant = 'secondary',
  disabled = false,
}: {
  children: ReactNode;
  onClick?: () => void;
  variant?: 'primary' | 'secondary' | 'danger';
  disabled?: boolean;
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
      style={{
        ...palette[variant],
        border: 'none',
        borderRadius: radius.pill,
        padding: `${space.md} ${space.xl}`,
        fontSize: font.size.md,
        fontWeight: font.weight.medium,
        fontFamily: font.sans,
        cursor: disabled ? 'not-allowed' : 'pointer',
        opacity: disabled ? 0.45 : 1,
        whiteSpace: 'nowrap',
      }}
    >
      {children}
    </button>
  );
}

export function Chips<T extends string | number>({
  options,
  value,
  onChange,
  label = (v) => String(v),
}: {
  options: readonly T[];
  value: T | readonly T[];
  onChange: (v: T) => void;
  label?: (v: T) => string;
}) {
  const selected = (v: T) => (Array.isArray(value) ? (value as readonly T[]).includes(v) : value === v);
  return (
    <div style={{ display: 'flex', gap: space.sm, flexWrap: 'wrap' }}>
      {options.map((o) => (
        <button
          key={String(o)}
          type="button"
          aria-pressed={selected(o)}
          onClick={() => onChange(o)}
          style={{
            border: 'none',
            borderRadius: radius.pill,
            padding: `${space.md} ${space.xl}`,
            fontSize: font.size.md,
            fontWeight: font.weight.medium,
            fontFamily: font.sans,
            cursor: 'pointer',
            background: selected(o) ? color.text : color.surfaceSubtle,
            color: selected(o) ? color.textInverse : color.textSecondary,
          }}
        >
          {label(o)}
        </button>
      ))}
    </div>
  );
}
