/** Every eligibility rule, pass or fail, with the detail the engine returned. */
import type { RuleResult } from '../../types.js';
import { color, font, radius, space, tone } from '../tokens.js';

function Mark({ passed }: { passed: boolean }) {
  const t = passed ? tone.positive : tone.critical;
  return (
    <span
      aria-hidden
      style={{
        width: '16px',
        height: '16px',
        flexShrink: 0,
        marginTop: '1px',
        borderRadius: radius.pill,
        background: t.bg,
        color: t.fg,
        display: 'inline-flex',
        alignItems: 'center',
        justifyContent: 'center',
        fontSize: font.size['2xs'],
        fontWeight: font.weight.semibold,
      }}
    >
      {passed ? '✓' : '✕'}
    </span>
  );
}

export function RuleList({ rules }: { rules: RuleResult[] }) {
  return (
    <div style={{ display: 'grid' }}>
      {rules.map((rule, i) => (
        <div
          key={rule.rule_id}
          style={{
            display: 'flex',
            gap: space.lg,
            padding: `${space.lg} 0`,
            borderTop: i === 0 ? 'none' : `1px solid ${color.border}`,
          }}
        >
          <Mark passed={rule.passed} />
          <div style={{ display: 'grid', gap: space.xs, minWidth: 0 }}>
            <div style={{ display: 'flex', alignItems: 'baseline', gap: space.md, flexWrap: 'wrap' }}>
              <span
                style={{
                  fontSize: font.size.md,
                  fontWeight: font.weight.medium,
                  color: rule.passed ? color.text : color.critical,
                }}
              >
                {rule.label}
              </span>
              <span style={{ fontSize: font.size['2xs'], fontFamily: font.mono, color: color.textMuted }}>
                {rule.rule_id}
              </span>
            </div>
            <span style={{ fontSize: font.size.sm, color: color.textSecondary, lineHeight: 1.5 }}>
              {rule.detail}
            </span>
          </div>
        </div>
      ))}
    </div>
  );
}
