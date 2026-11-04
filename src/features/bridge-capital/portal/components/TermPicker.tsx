/** 15 / 30 / 60 / 90 days, or a custom 7–90. */
import { PRICING } from '../../policy/policy.config.js';
import { isValidTerm } from '../../policy/pricing.js';
import { color, font, radius, space } from '../tokens.js';

export function TermPicker({
  value,
  onChange,
}: {
  value: number;
  onChange: (termDays: number) => void;
}) {
  const presets = PRICING.presetTermDays;
  const isCustom = !presets.includes(value as (typeof presets)[number]);

  return (
    <div style={{ display: 'grid', gap: space.lg }}>
      <div style={{ display: 'flex', gap: space.md, flexWrap: 'wrap' }}>
        {presets.map((days) => {
          const active = value === days;
          return (
            <button
              key={days}
              type="button"
              onClick={() => onChange(days)}
              style={{
                flex: '1 1 0',
                minWidth: '64px',
                background: active ? color.accent : color.surface,
                color: active ? color.textInverse : color.text,
                border: `1px solid ${active ? color.accent : color.borderStrong}`,
                borderRadius: radius.lg,
                padding: `${space.lg} ${space.md}`,
                fontSize: font.size.lg,
                fontWeight: font.weight.medium,
                fontFamily: font.sans,
                cursor: 'pointer',
              }}
            >
              {days}d
            </button>
          );
        })}
      </div>

      <label style={{ display: 'flex', alignItems: 'center', gap: space.lg }}>
        <span style={{ fontSize: font.size.md, color: color.textSecondary, whiteSpace: 'nowrap' }}>
          Or set your own
        </span>
        <input
          type="number"
          min={PRICING.minCustomTermDays}
          max={PRICING.maxCustomTermDays}
          value={isCustom ? value : ''}
          placeholder={`${PRICING.minCustomTermDays}–${PRICING.maxCustomTermDays}`}
          onChange={(e) => {
            const next = Number(e.target.value);
            if (isValidTerm(next)) onChange(next);
          }}
          style={{
            width: '96px',
            background: color.surface,
            border: `1px solid ${isCustom ? color.accent : color.borderStrong}`,
            borderRadius: radius.lg,
            padding: `${space.lg} ${space.xl}`,
            fontSize: font.size.lg,
            fontFamily: font.sans,
            color: color.text,
          }}
        />
        <span style={{ fontSize: font.size.md, color: color.textMuted }}>days</span>
      </label>
    </div>
  );
}
