/** Balance, this month at a glance, and a banner for any bridge that is running. */
import { useMemo } from 'react';
import type { Bridge } from '../../types.js';
import { applyDelinquency } from '../../policy/delinquency.js';
import { color, font, radius, space, stageToneFor } from '../tokens.js';
import { usd, shortDate, titleCase } from '../../ui/format.js';
import { addDays } from '../../policy/util.js';
import { buildActivity, spendByCategory, totals } from '../activity.js';
import { openerFor, type Portal } from '../usePortal.js';
import { Amount, Button, Card, ListRow, Pill, SectionTitle } from '../components/ui.js';

function BridgeBanner({ bridge, today, onOpen }: { bridge: Bridge; today: string; onOpen: () => void }) {
  const stage = applyDelinquency(bridge, today);
  const pastDue = stage.days_past_due > 0;
  const outstanding = bridge.principal_outstanding + bridge.fee_outstanding + stage.accrued_fee;

  return (
    <button
      type="button"
      onClick={onOpen}
      style={{
        width: '100%',
        textAlign: 'left',
        background: pastDue ? color.criticalSubtle : color.surface,
        border: 'none',
        borderRadius: radius.xl,
        padding: space['2xl'],
        display: 'grid',
        gap: space.md,
        cursor: 'pointer',
        fontFamily: font.sans,
      }}
    >
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: space.lg }}>
        <span style={{ fontSize: font.size.md, fontWeight: font.weight.semibold, color: color.text }}>
          Bridge running
        </span>
        <Pill tone={stageToneFor(stage.status)}>{titleCase(stage.status)}</Pill>
      </div>
      <span
        style={{
          fontSize: font.size['2xl'],
          fontWeight: font.weight.semibold,
          color: color.text,
          fontVariantNumeric: 'tabular-nums',
        }}
      >
        {usd(outstanding, { cents: true })}
      </span>
      <span style={{ fontSize: font.size.md, color: color.textSecondary }}>
        {pastDue
          ? `${stage.days_past_due} days past due — tap to see what happens next`
          : `Due ${shortDate(bridge.due_date)} · collected from your deposits`}
      </span>
    </button>
  );
}

/** Money in and money out this month, side by side. */
function MonthTotals({ moneyIn, moneyOut }: { moneyIn: number; moneyOut: number }) {
  return (
    <div style={{ display: 'flex', justifyContent: 'space-between', gap: space.lg }}>
      {[
        { label: 'In, last 30 days', value: moneyIn, tone: color.positive },
        { label: 'Out', value: moneyOut, tone: color.text },
      ].map((t) => (
        <div key={t.label} style={{ display: 'grid', gap: '2px' }}>
          <span style={{ fontSize: font.size.sm, color: color.textSecondary }}>{t.label}</span>
          <span
            style={{
              fontSize: font.size.xl,
              fontWeight: font.weight.semibold,
              color: t.tone,
              fontVariantNumeric: 'tabular-nums',
            }}
          >
            {usd(t.value)}
          </span>
        </div>
      ))}
    </div>
  );
}

/** Where the card went this month, biggest category first. */
function CategoryBars({ rows }: { rows: Array<{ category: string; amount: number }> }) {
  const top = rows.slice(0, 4);
  const largest = top[0]?.amount ?? 0;
  if (largest <= 0) return null;

  return (
    <div style={{ display: 'grid', gap: space.lg }}>
      {top.map((row) => (
        <div key={row.category} style={{ display: 'grid', gap: space.sm }}>
          <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: font.size.md }}>
            <span style={{ color: color.textSecondary }}>{titleCase(row.category)}</span>
            <span style={{ color: color.text, fontVariantNumeric: 'tabular-nums' }}>
              {usd(row.amount)}
            </span>
          </div>
          <div style={{ height: '6px', borderRadius: radius.pill, background: color.surfaceSunken }}>
            <div
              style={{
                width: `${(row.amount / largest) * 100}%`,
                height: '100%',
                borderRadius: radius.pill,
                background: color.accent,
              }}
            />
          </div>
        </div>
      ))}
    </div>
  );
}

export function WalletHome({ portal }: { portal: Portal }) {
  const { data, customer, today, go } = portal;

  const activity = useMemo(
    () => (data === null || customer === null ? [] : buildActivity(data, customer.id)),
    [data, customer],
  );

  if (data === null || customer === null) return null;

  const balance = customer.balance_history.at(-1)?.balance ?? 0;
  const bridges = data.bridges.filter((b) => b.customer_id === customer.id && b.status !== 'REPAID');

  // The last 30 days rather than the calendar month: three days into November
  // a month-to-date figure says more about the date than about the business.
  const since = addDays(today, -30);
  const recentWindow = activity.filter((i) => i.at.slice(0, 10) >= since);
  const month = totals(recentWindow);
  const categories = spendByCategory(recentWindow);
  const recent = activity.slice(0, 5);

  return (
    <div style={{ display: 'grid', gap: space['2xl'] }}>
      <Card>
        <div style={{ display: 'grid', gap: space.xl }}>
          <Amount label="Wallet balance" value={usd(balance)} />
          <MonthTotals moneyIn={month.in} moneyOut={month.out} />
        </div>
      </Card>

      {bridges.map((bridge) => (
        <BridgeBanner
          key={bridge.id}
          bridge={bridge}
          today={today}
          onOpen={() => go({ screen: 'bridge', bridgeId: bridge.id })}
        />
      ))}

      {categories.length > 0 && (
        <Card>
          <div style={{ display: 'grid', gap: space.xl }}>
            <SectionTitle>Where the card went, last 30 days</SectionTitle>
            <CategoryBars rows={categories} />
          </div>
        </Card>
      )}

      <Card>
        <div style={{ display: 'grid', gap: space.md }}>
          <SectionTitle>Recent activity</SectionTitle>
          <div style={{ display: 'grid' }}>
            {recent.map((item, i) => {
              const declined = item.kind === 'CARD_DECLINED';
              const open = openerFor(item, go);
              return (
                <ListRow
                  key={item.id}
                  first={i === 0}
                  title={item.title}
                  subtitle={
                    declined ? (
                      <span style={{ color: color.critical }}>
                        Declined · {shortDate(item.at)}
                      </span>
                    ) : (
                      `${titleCase(item.subtitle)} · ${shortDate(item.at)}`
                    )
                  }
                  amount={
                    declined
                      ? usd(item.transaction?.amount ?? 0)
                      : `${item.amount > 0 ? '+' : '\u2212'}${usd(Math.abs(item.amount))}`
                  }
                  tone={declined ? 'critical' : item.amount > 0 ? 'positive' : undefined}
                  onClick={open}
                />
              );
            })}
          </div>
          <Button variant="ghost" onClick={() => go({ screen: 'activity' })}>
            See all activity
          </Button>
        </div>
      </Card>
    </div>
  );
}
