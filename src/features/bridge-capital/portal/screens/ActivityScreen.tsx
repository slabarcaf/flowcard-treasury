/** Everything that moved money, a month at a time. */
import { useMemo, useState } from 'react';
import { color, font, radius, space } from '../tokens.js';
import { usd, dayLabel, monthLabel, titleCase } from '../../ui/format.js';
import {
  buildActivity,
  filterActivity,
  groupByDay,
  monthKey,
  monthsWithActivity,
  totals,
  type ActivityFilter,
  type ActivityItem,
} from '../activity.js';
import { openerFor, type Portal } from '../usePortal.js';
import { Card, ListRow, SectionTitle } from '../components/ui.js';

const FILTERS: Array<{ id: ActivityFilter; label: string }> = [
  { id: 'ALL', label: 'All' },
  { id: 'CARD', label: 'Card' },
  { id: 'DEPOSITS', label: 'Deposits' },
  { id: 'BRIDGES', label: 'Bridges' },
];

function MonthSwitcher({
  months,
  index,
  onChange,
}: {
  months: string[];
  index: number;
  onChange: (next: number) => void;
}) {
  // Months run newest first, so "older" means a higher index.
  const older = index + 1 < months.length;
  const newer = index > 0;

  const arrow = (enabled: boolean, label: string, glyph: string, next: number) => (
    <button
      type="button"
      onClick={() => enabled && onChange(next)}
      disabled={!enabled}
      aria-label={label}
      style={{
        background: 'none',
        border: 'none',
        padding: space.md,
        fontSize: font.size.xl,
        lineHeight: 1,
        fontFamily: font.sans,
        color: enabled ? color.text : color.textMuted,
        opacity: enabled ? 1 : 0.35,
        cursor: enabled ? 'pointer' : 'default',
      }}
    >
      {glyph}
    </button>
  );

  return (
    <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
      {arrow(older, 'Previous month', '‹', index + 1)}
      <span style={{ fontSize: font.size.lg, fontWeight: font.weight.medium }}>
        {monthLabel(months[index])}
      </span>
      {arrow(newer, 'Next month', '›', index - 1)}
    </div>
  );
}

function FilterRow({
  value,
  onChange,
}: {
  value: ActivityFilter;
  onChange: (next: ActivityFilter) => void;
}) {
  return (
    <div style={{ display: 'flex', gap: space.sm, flexWrap: 'wrap' }}>
      {FILTERS.map((f) => {
        const selected = f.id === value;
        return (
          <button
            key={f.id}
            type="button"
            onClick={() => onChange(f.id)}
            style={{
              background: selected ? color.accentSubtle : color.surfaceSubtle,
              color: selected ? color.accent : color.textSecondary,
              border: 'none',
              borderRadius: radius.pill,
              padding: `${space.sm} ${space.xl}`,
              fontSize: font.size.md,
              fontFamily: font.sans,
              fontWeight: font.weight.medium,
              cursor: 'pointer',
            }}
          >
            {f.label}
          </button>
        );
      })}
    </div>
  );
}

function Total({ label, value, tone }: { label: string; value: string; tone: string }) {
  return (
    <div style={{ display: 'grid', gap: '2px' }}>
      <span style={{ fontSize: font.size.sm, color: color.textSecondary }}>{label}</span>
      <span
        style={{
          fontSize: font.size.xl,
          fontWeight: font.weight.semibold,
          color: tone,
          fontVariantNumeric: 'tabular-nums',
        }}
      >
        {value}
      </span>
    </div>
  );
}

/** Amount as the customer reads it: money in with a plus, a decline with neither. */
function amountLabel(item: ActivityItem): string {
  if (item.kind === 'CARD_DECLINED') return usd(item.transaction?.amount ?? 0);
  return `${item.amount > 0 ? '+' : '−'}${usd(Math.abs(item.amount))}`;
}

function toneFor(item: ActivityItem) {
  if (item.kind === 'CARD_DECLINED') return 'critical' as const;
  return item.amount > 0 ? ('positive' as const) : undefined;
}

export function ActivityScreen({ portal }: { portal: Portal }) {
  const { data, customer, go } = portal;
  const [monthIndex, setMonthIndex] = useState(0);
  const [filter, setFilter] = useState<ActivityFilter>('ALL');

  const all = useMemo(
    () => (data === null || customer === null ? [] : buildActivity(data, customer.id)),
    [data, customer],
  );
  const months = useMemo(() => monthsWithActivity(all), [all]);

  if (data === null || customer === null) return null;

  if (months.length === 0) {
    return (
      <Card>
        <span style={{ color: color.textSecondary }}>No activity on this account yet.</span>
      </Card>
    );
  }

  const month = months[Math.min(monthIndex, months.length - 1)];
  const items = filterActivity(all, month, filter);
  const sum = totals(filterActivity(all, month, 'ALL'));
  const days = groupByDay(items);

  return (
    <div style={{ display: 'grid', gap: space['2xl'] }}>
      <Card>
        <div style={{ display: 'grid', gap: space.xl }}>
          <MonthSwitcher months={months} index={monthIndex} onChange={setMonthIndex} />
          <div style={{ display: 'flex', justifyContent: 'space-between', gap: space.lg }}>
            <Total label="Money in" value={usd(sum.in)} tone={color.positive} />
            <Total label="Money out" value={usd(sum.out)} tone={color.text} />
            <Total
              label="Net"
              value={`${sum.net < 0 ? '−' : ''}${usd(Math.abs(sum.net))}`}
              tone={sum.net < 0 ? color.critical : color.positive}
            />
          </div>
        </div>
      </Card>

      <FilterRow value={filter} onChange={setFilter} />

      {days.length === 0 ? (
        <Card>
          <span style={{ color: color.textSecondary }}>
            Nothing in {monthLabel(month)} under this filter.
          </span>
        </Card>
      ) : (
        days.map((day) => (
          <Card key={day.date}>
            <div style={{ display: 'grid', gap: space.md }}>
              <SectionTitle>{dayLabel(day.date)}</SectionTitle>
              <div style={{ display: 'grid' }}>
                {day.items.map((item, i) => (
                  <ListRow
                    key={item.id}
                    first={i === 0}
                    title={item.title}
                    subtitle={
                      item.kind === 'CARD_DECLINED' ? (
                        <span style={{ color: color.critical }}>{item.subtitle}</span>
                      ) : (
                        titleCase(item.subtitle)
                      )
                    }
                    amount={amountLabel(item)}
                    tone={toneFor(item)}
                    onClick={openerFor(item, go)}
                  />
                ))}
              </div>
            </div>
          </Card>
        ))
      )}

      <span style={{ fontSize: font.size.sm, color: color.textMuted, textAlign: 'center' }}>
        {monthKey(all[0].at) === month ? 'Up to date' : `${all.length} movements on record`}
      </span>
    </div>
  );
}
