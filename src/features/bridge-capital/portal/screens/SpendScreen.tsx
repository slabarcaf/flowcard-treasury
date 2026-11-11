/** Where the money goes: top vendors and categories, the trend, and receipts. */
import type { CardTransaction } from '../../types.js';
import { daysBetween, round2 } from '../../policy/util.js';
import { color, font, space } from '../tokens.js';
import { monthLabel, shortDate, usd } from '../../ui/format.js';
import { HBars } from '../../ui/charts.js';
import { chartColor } from '../../ui/chartColor.js';
import type { Portal } from '../usePortal.js';
import { categoryLabel } from '../copy.js';
import { Pill, ListRow } from '../components/ui.js';
import { PageHeader, Panel } from '../components/layout.js';

function totalsBy(txns: CardTransaction[], key: (t: CardTransaction) => string) {
  const m = new Map<string, number>();
  for (const t of txns) m.set(key(t), (m.get(key(t)) ?? 0) + t.amount);
  return [...m.entries()].map(([label, value]) => ({ label, value: round2(value) })).sort((a, b) => b.value - a.value);
}

export function SpendScreen({ portal }: { portal: Portal }) {
  const { data, customer, today, go, attachReceipt } = portal;
  if (data === null || customer === null) return null;

  const spent = data.transactions.filter((t) => t.customer_id === customer.id && t.status !== 'DECLINED');
  const last30 = spent.filter((t) => {
    const age = daysBetween(t.created_at, today);
    return age >= 0 && age < 30;
  });
  const vendors = totalsBy(last30, (t) => t.merchant_name).slice(0, 6);
  const categories = totalsBy(last30, (t) => categoryLabel(t.merchant_category));
  const total = categories.reduce((s, c) => s + c.value, 0);
  const trend = (data.cashFlows[customer.id] ?? []).slice(-6);
  const receipts = new Map(data.receipts.map((r) => [r.transaction_id, r]));
  const recent = [...last30].sort((a, b) => b.created_at.localeCompare(a.created_at)).slice(0, 12);
  const missing = recent.filter((t) => !receipts.has(t.id)).length;

  return (
    <>
      <PageHeader title="Spend" sub="Card spend over the last 30 days." />
      <div className="bcp-g2">
        <Panel title="Top vendors">
          <HBars items={vendors} />
        </Panel>
        <Panel title="By category" meta={usd(total)}>
          <HBars items={categories.map((c) => ({ ...c, meta: `${Math.round((c.value / Math.max(1, total)) * 100)}%` }))} />
        </Panel>
      </div>
      <Panel title="Spend trend" meta={`last ${trend.length} months`}>
        <HBars
          items={trend.map((m, i) => ({ label: monthLabel(m.month) + (i === trend.length - 1 ? ' (to date)' : ''), value: m.outflow }))}
          barColor={chartColor.outflow}
        />
      </Panel>
      <Panel title="Card transactions" meta={`${missing} missing receipts`}>
        {recent.map((t, i) => {
          const r = receipts.get(t.id);
          return (
            <div key={t.id} style={{ display: 'flex', alignItems: 'center', gap: space.lg, boxShadow: i === 0 ? 'none' : `inset 0 1px 0 ${color.border}` }}>
              <div style={{ flex: 1, minWidth: 0 }}>
                <ListRow first title={t.merchant_name} subtitle={`${shortDate(t.created_at)} · ${categoryLabel(t.merchant_category)}`} amount={`−${usd(t.amount)}`} onClick={() => go({ screen: 'transaction', transactionId: t.id })} />
              </div>
              {r !== undefined ? (
                <Pill tone="positive">Receipt</Pill>
              ) : (
                <label style={{ fontSize: font.size.md, color: color.accent, cursor: 'pointer', whiteSpace: 'nowrap', fontWeight: font.weight.medium }}>
                  Add receipt
                  <input
                    type="file"
                    accept="image/*,application/pdf"
                    hidden
                    onChange={(e) => {
                      const file = e.target.files?.[0];
                      if (file !== undefined) void attachReceipt(t, file.name);
                    }}
                  />
                </label>
              )}
            </div>
          );
        })}
      </Panel>
    </>
  );
}
