/**
 * Inline SVG charts shared by the portal and the back office. No charting
 * dependency: one scale per chart, labels only at values the chart reaches.
 */
import type { ReactNode } from 'react';
import type { MonthlyCashFlow } from '../types.js';
import type { Forecast } from '../policy/forecast.js';
import { color } from './theme.js';
import { font, radius, space } from './scale.js';
import { usd } from './format.js';
import { chartColor } from './chartColor.js';

const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];


function niceMax(v: number): number {
  if (v <= 0) return 1;
  const p = 10 ** Math.floor(Math.log10(v));
  const n = v / p;
  return (n <= 1 ? 1 : n <= 2 ? 2 : n <= 2.5 ? 2.5 : n <= 5 ? 5 : 10) * p;
}

function compact(n: number): string {
  const a = Math.abs(n);
  const s = a >= 1000 ? `${(a / 1000).toFixed(a >= 100_000 ? 0 : 1).replace(/\.0$/, '')}k` : `${Math.round(a)}`;
  return `${n < 0 ? '−' : ''}$${s}`;
}

function shortDay(iso: string): string {
  const [, m, d] = iso.split('-').map(Number);
  return `${d} ${MONTHS[m - 1]}`;
}

const axisText = { fill: chartColor.axis, fontSize: 11, fontFamily: font.sans };

export function Legend({ items }: { items: Array<{ label: string; color: string; dashed?: boolean }> }) {
  return (
    <div style={{ display: 'flex', gap: space.xl, flexWrap: 'wrap', fontSize: font.size.sm, color: color.textSecondary }}>
      {items.map((i) => (
        <span key={i.label} style={{ display: 'inline-flex', alignItems: 'center', gap: space.sm }}>
          <span
            style={{
              width: '12px',
              height: i.dashed ? '0' : '10px',
              borderRadius: '3px',
              background: i.dashed ? 'none' : i.color,
              borderTop: i.dashed ? `2px dashed ${i.color}` : 'none',
            }}
          />
          {i.label}
        </span>
      ))}
    </div>
  );
}

/** Grouped in/out bars per month with the net as a line. */
export function CashFlowChart({ flows, height = 260 }: { flows: MonthlyCashFlow[]; height?: number }) {
  if (flows.length === 0) return <ChartEmpty>No money has moved yet.</ChartEmpty>;
  const W = 720, H = height, L = 56, R = 12, T = 14, B = 30;
  const maxV = niceMax(Math.max(...flows.map((f) => Math.max(f.inflow, f.outflow))));
  const minNet = Math.min(0, ...flows.map((f) => f.net));
  const minV = minNet < 0 ? -niceMax(-minNet) : 0;
  const y = (v: number) => T + ((maxV - v) / (maxV - minV)) * (H - T - B);
  const step = (W - L - R) / flows.length;
  const bw = Math.min(18, step / 3);
  const ticks = [0, 1, 2, 3, 4].map((i) => minV + ((maxV - minV) * i) / 4);
  const cx = (i: number) => L + step * i + step / 2;
  const netLine = flows.map((f, i) => `${cx(i)},${y(f.net)}`).join(' ');

  return (
    <svg viewBox={`0 0 ${W} ${H}`} role="img" aria-label="Money in, money out and net by month" style={{ width: '100%', height: 'auto', display: 'block' }}>
      {ticks.map((v) => (
        <g key={v}>
          <line x1={L} x2={W - R} y1={y(v)} y2={y(v)} stroke={chartColor.grid} />
          <text x={L - 8} y={y(v) + 4} textAnchor="end" {...axisText}>{compact(v)}</text>
        </g>
      ))}
      {minV < 0 && <line x1={L} x2={W - R} y1={y(0)} y2={y(0)} stroke={chartColor.axis} />}
      {flows.map((f, i) => {
        const [yr, m] = f.month.split('-').map(Number);
        const partial = i === flows.length - 1;
        return (
          <g key={f.month} opacity={partial ? 0.55 : 1}>
            <rect x={cx(i) - bw - 1} y={y(f.inflow)} width={bw} height={Math.max(0, y(0) - y(f.inflow))} rx={3} fill={chartColor.inflow}>
              <title>{`${MONTHS[m - 1]} ${yr}: in ${usd(f.inflow)}`}</title>
            </rect>
            <rect x={cx(i) + 1} y={y(f.outflow)} width={bw} height={Math.max(0, y(0) - y(f.outflow))} rx={3} fill={chartColor.outflow}>
              <title>{`${MONTHS[m - 1]} ${yr}: out ${usd(f.outflow)}`}</title>
            </rect>
            <text x={cx(i)} y={H - 10} textAnchor="middle" {...axisText}>{MONTHS[m - 1]}</text>
          </g>
        );
      })}
      {flows.length > 1 && <polyline points={netLine} fill="none" stroke={chartColor.net} strokeWidth={2.5} />}
      {flows.map((f, i) => (
        <circle key={f.month} cx={cx(i)} cy={y(f.net)} r={i === flows.length - 1 ? 4.5 : 3} fill={chartColor.net}>
          <title>{`Net ${usd(f.net)}`}</title>
        </circle>
      ))}
    </svg>
  );
}

/** Projected balance with the low line and the first day below it. */
export function ForecastChart({ forecast, height = 240 }: { forecast: Forecast; height?: number }) {
  const W = 720, H = height, L = 56, R = 14, T = 16, B = 28;
  const pts = forecast.points;
  const vals = pts.map((p) => p.balance).concat([forecast.low_line, 0]);
  const maxV = niceMax(Math.max(...vals));
  const rawMin = Math.min(...vals);
  const minV = rawMin < 0 ? -niceMax(-rawMin) : 0;
  const x = (i: number) => L + (i / (pts.length - 1)) * (W - L - R);
  const y = (v: number) => T + ((maxV - v) / (maxV - minV)) * (H - T - B);
  const line = pts.map((p, i) => `${x(i)},${y(p.balance)}`).join(' ');
  const ticks = (height < 200 ? [0, 1, 2] : [0, 1, 2, 3, 4]).map((i, _, a) => minV + ((maxV - minV) * i) / (a.length - 1));
  const labelAt = [0, 15, 30, 45, pts.length - 1];
  const crossIndex = forecast.crossing === null ? -1 : pts.indexOf(forecast.crossing);

  return (
    <svg viewBox={`0 0 ${W} ${H}`} role="img" aria-label="Projected balance" style={{ width: '100%', height: 'auto', display: 'block' }}>
      {ticks.map((v) => (
        <g key={v}>
          <line x1={L} x2={W - R} y1={y(v)} y2={y(v)} stroke={chartColor.grid} />
          <text x={L - 8} y={y(v) + 4} textAnchor="end" {...axisText}>{compact(v)}</text>
        </g>
      ))}
      {minV < 0 && <line x1={L} x2={W - R} y1={y(0)} y2={y(0)} stroke={chartColor.axis} />}
      <polygon points={`${x(0)},${y(minV)} ${line} ${x(pts.length - 1)},${y(minV)}`} fill={color.accentSubtle} />
      <polyline points={line} fill="none" stroke={chartColor.net} strokeWidth={2.5} />
      <line x1={L} x2={W - R} y1={y(forecast.low_line)} y2={y(forecast.low_line)} stroke={chartColor.low} strokeWidth={1.5} strokeDasharray="5 5" />
      <text x={W - R} y={y(forecast.low_line) - 6} textAnchor="end" fill={chartColor.low} fontSize={11} fontWeight={600} fontFamily={font.sans}>
        Low line {compact(forecast.low_line)}
      </text>
      {labelAt.map((i) => (
        <text key={i} x={x(i)} y={H - 8} textAnchor={i === 0 ? 'start' : i === pts.length - 1 ? 'end' : 'middle'} {...axisText}>
          {i === 0 ? 'Today' : shortDay(pts[i].date)}
        </text>
      ))}
      {crossIndex >= 0 && (
        <g>
          <circle cx={x(crossIndex)} cy={y(pts[crossIndex].balance)} r={5} fill={chartColor.cross} />
          <text
            x={Math.min(x(crossIndex) + 8, W - R - 60)}
            y={Math.max(y(pts[crossIndex].balance) - 10, T + 10)}
            fill={chartColor.cross}
            fontSize={11}
            fontWeight={600}
            fontFamily={font.sans}
          >
            {shortDay(pts[crossIndex].date)}
          </text>
        </g>
      )}
    </svg>
  );
}

/** Labelled horizontal bars, for vendors, categories, tiers and buckets. */
export function HBars({
  items,
  format = (v) => usd(v),
  barColor = color.accent,
}: {
  items: Array<{ label: string; value: number; color?: string; meta?: string }>;
  format?: (v: number) => string;
  barColor?: string;
}) {
  const max = Math.max(1, ...items.map((i) => i.value));
  return (
    <div style={{ display: 'grid', gap: space.md }}>
      {items.map((i) => (
        <div key={i.label} style={{ display: 'grid', gridTemplateColumns: 'minmax(0, 150px) minmax(0, 1fr) auto', gap: space.lg, alignItems: 'center' }}>
          <span style={{ fontSize: font.size.md, color: color.text, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
            {i.label}
            {i.meta !== undefined && <span style={{ color: color.textMuted }}> · {i.meta}</span>}
          </span>
          <span style={{ height: '8px', background: color.surfaceRaised, borderRadius: radius.pill, overflow: 'hidden' }}>
            <span style={{ display: 'block', height: '100%', width: `${(i.value / max) * 100}%`, background: i.color ?? barColor, borderRadius: radius.pill }} />
          </span>
          <span style={{ fontSize: font.size.md, color: color.textSecondary, fontVariantNumeric: 'tabular-nums' }}>{format(i.value)}</span>
        </div>
      ))}
    </div>
  );
}

function ChartEmpty({ children }: { children: ReactNode }) {
  return <div style={{ padding: space['3xl'], textAlign: 'center', color: color.textMuted, fontSize: font.size.md }}>{children}</div>;
}
