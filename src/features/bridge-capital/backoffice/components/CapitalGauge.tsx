/** Facility, deployed, available and reserve floor as one stacked bar. */
import type { PlatformCapital } from '../../types.js';
import {
  availableAboveFloor,
  capitalRatio,
  currentRegime,
  lendable,
  reserveFloor,
} from '../../policy/regime.js';
import { REGIME_LABELS } from '../../policy/policy.config.js';
import { color, font, radius, space, REGIME_TONE } from '../tokens.js';
import { usd, pct } from '../format.js';
import { Badge, Label } from './primitives.js';

export function CapitalGauge({ capital }: { capital: PlatformCapital }) {
  const floor = reserveFloor(capital);
  const available = availableAboveFloor(capital);
  const deployed = capital.outstanding;
  const total = capital.facility_size;
  const regime = currentRegime(capital);

  const segments = [
    { key: 'deployed', label: 'Deployed', value: deployed, fill: color.accent },
    { key: 'available', label: 'Available', value: available, fill: color.positive },
    { key: 'floor', label: 'Reserve floor', value: floor, fill: color.borderStrong },
  ];

  return (
    <div style={{ display: 'grid', gap: space.xl }}>
      <div style={{ display: 'flex', alignItems: 'flex-end', justifyContent: 'space-between', gap: space.lg }}>
        <div style={{ display: 'grid', gap: space.xs }}>
          <Label>Facility</Label>
          <div
            style={{
              fontSize: font.size['3xl'],
              fontWeight: font.weight.semibold,
              letterSpacing: '-0.025em',
              fontVariantNumeric: 'tabular-nums',
              color: color.text,
            }}
          >
            {usd(total)}
          </div>
        </div>
        <div style={{ display: 'grid', gap: space.sm, justifyItems: 'end' }}>
          <Badge tone={REGIME_TONE[regime]}>{REGIME_LABELS[regime]}</Badge>
          <div style={{ fontSize: font.size.sm, color: color.textMuted, fontVariantNumeric: 'tabular-nums' }}>
            {pct(capitalRatio(capital))} of facility above floor
          </div>
        </div>
      </div>

      {/* Stacked bar */}
      <div
        style={{
          display: 'flex',
          height: '10px',
          borderRadius: radius.pill,
          overflow: 'hidden',
          background: color.surfaceSubtle,
        }}
      >
        {segments.map((s) => (
          <div
            key={s.key}
            title={`${s.label}: ${usd(s.value)}`}
            style={{ width: `${total === 0 ? 0 : (s.value / total) * 100}%`, background: s.fill }}
          />
        ))}
      </div>

      <div style={{ display: 'flex', gap: space['3xl'], flexWrap: 'wrap' }}>
        {segments.map((s) => (
          <div key={s.key} style={{ display: 'flex', alignItems: 'center', gap: space.md }}>
            <span
              style={{
                width: '8px',
                height: '8px',
                borderRadius: radius.pill,
                background: s.fill,
                flexShrink: 0,
              }}
            />
            <span style={{ fontSize: font.size.sm, color: color.textSecondary }}>{s.label}</span>
            <span
              style={{
                fontSize: font.size.md,
                fontWeight: font.weight.semibold,
                color: color.text,
                fontVariantNumeric: 'tabular-nums',
              }}
            >
              {usd(s.value)}
            </span>
          </div>
        ))}
        <div style={{ display: 'flex', alignItems: 'center', gap: space.md }}>
          <span style={{ fontSize: font.size.sm, color: color.textSecondary }}>Lendable</span>
          <span
            style={{
              fontSize: font.size.md,
              fontWeight: font.weight.semibold,
              color: color.text,
              fontVariantNumeric: 'tabular-nums',
            }}
          >
            {usd(lendable(capital))}
          </span>
        </div>
      </div>
    </div>
  );
}
