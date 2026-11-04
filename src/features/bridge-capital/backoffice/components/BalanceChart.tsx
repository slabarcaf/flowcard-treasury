/**
 * End-of-day balances as an inline SVG area chart, with the 90-day low point
 * and the near-zero threshold marked. No charting dependency.
 */
import type { BalanceStats, DailyBalance } from '../../types.js';
import { chart, color, font, space } from '../tokens.js';
import { usd, shortDate } from '../../ui/format.js';
import { Label } from './primitives.js';

export function BalanceChart({
  history,
  stats,
}: {
  history: DailyBalance[];
  stats: BalanceStats;
}) {
  if (history.length === 0) {
    return <div style={{ fontSize: font.size.sm, color: color.textMuted }}>No balance history</div>;
  }

  const w = 100;
  const h = chart.height;
  const max = Math.max(...history.map((d) => d.balance), 1);
  const x = (i: number) => (history.length === 1 ? 0 : (i / (history.length - 1)) * w);
  const y = (v: number) => h - (v / max) * h;

  const line = history.map((d, i) => `${i === 0 ? 'M' : 'L'}${x(i).toFixed(3)},${y(d.balance).toFixed(2)}`).join(' ');
  const area = `${line} L${w},${h} L0,${h} Z`;
  const nearZeroY = y(stats.avg_balance_90d * 0.1);
  const lowPointY = y(stats.low_point_balance_90d);

  return (
    <div style={{ display: 'grid', gap: space.md }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'baseline' }}>
        <Label>End-of-day balance · {history.length} days</Label>
        <span style={{ fontSize: font.size.sm, color: color.textMuted, fontVariantNumeric: 'tabular-nums' }}>
          peak {usd(max)}
        </span>
      </div>

      <svg
        viewBox={`0 0 ${w} ${h}`}
        preserveAspectRatio="none"
        style={{ width: '100%', height: `${h}px`, display: 'block', overflow: 'visible' }}
        role="img"
        aria-label={`Daily balances over ${history.length} days`}
      >
        <path d={area} fill={chart.fill} />
        <path
          d={line}
          fill="none"
          stroke={chart.line}
          strokeWidth={chart.strokeWidth}
          vectorEffect="non-scaling-stroke"
        />
        {/* 10% of the 90-day average: the near-zero line */}
        <line
          x1={0}
          x2={w}
          y1={nearZeroY}
          y2={nearZeroY}
          stroke={chart.lowPoint}
          strokeWidth={1}
          strokeDasharray="3 3"
          vectorEffect="non-scaling-stroke"
        />
        {/* 10th percentile of the last 90 days */}
        <line
          x1={0}
          x2={w}
          y1={lowPointY}
          y2={lowPointY}
          stroke={color.textMuted}
          strokeWidth={1}
          strokeDasharray="1 4"
          vectorEffect="non-scaling-stroke"
        />
      </svg>

      <div
        style={{
          display: 'flex',
          justifyContent: 'space-between',
          fontSize: font.size.xs,
          color: color.textMuted,
        }}
      >
        <span>{shortDate(history[0].date)}</span>
        <span style={{ color: chart.lowPoint }}>— — near-zero line (10% of 90d avg)</span>
        <span>{shortDate(history[history.length - 1].date)}</span>
      </div>
    </div>
  );
}
