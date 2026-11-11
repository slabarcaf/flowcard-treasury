/** Monthly statements and the bridge agreements the customer accepted. */
import { useState } from 'react';
import { priceOffer } from '../../policy/pricing.js';
import { addDays } from '../../policy/util.js';
import { color, font, space } from '../tokens.js';
import { monthLabel, shortDate, usd } from '../../ui/format.js';
import type { Portal } from '../usePortal.js';
import { ListRow, Row } from '../components/ui.js';
import { PageHeader, Panel } from '../components/layout.js';

export function StatementsScreen({ portal }: { portal: Portal }) {
  const { data, customer } = portal;
  const [month, setMonth] = useState<string | null>(null);
  const [agreement, setAgreement] = useState<string | null>(null);
  if (data === null || customer === null) return null;

  const flows = (data.cashFlows[customer.id] ?? []).slice(0, -1).reverse();
  const closeOf = (key: string) => {
    const last = customer.balance_history.filter((d) => d.date.slice(0, 7) === key).at(-1);
    return last?.balance ?? null;
  };
  const selected = flows.find((f) => f.month === month);
  const bridges = customer.bridge_history.slice().sort((a, b) => b.opened_at.localeCompare(a.opened_at));
  const chosen = bridges.find((b) => b.id === agreement);

  return (
    <>
      <PageHeader title="Statements" sub="Monthly statements and the agreements you accepted." />
      <div className="bcp-g2">
        <Panel title="Monthly statements">
          {flows.map((f, i) => (
            <ListRow
              key={f.month}
              first={i === 0}
              title={monthLabel(f.month)}
              subtitle={`In ${usd(f.inflow)} · Out ${usd(f.outflow)}`}
              amount={month === f.month ? 'Hide' : 'View'}
              onClick={() => setMonth(month === f.month ? null : f.month)}
            />
          ))}
          {selected !== undefined && (
            <div style={{ marginTop: space.lg, background: color.surfaceSubtle, borderRadius: '12px', padding: space.xl }}>
              <Row label="Account" value={`${customer.name} · USD wallet`} />
              <Row label="Money in" value={usd(selected.inflow, { cents: true })} />
              <Row label="Money out (card)" value={`−${usd(selected.outflow, { cents: true })}`} />
              <Row label="Closing balance" value={closeOf(selected.month) === null ? '—' : usd(closeOf(selected.month) ?? 0, { cents: true })} strong />
              <p style={{ margin: `${space.lg} 0 0`, fontSize: font.size.sm, color: color.textMuted }}>PDF download arrives when statements come from the backend.</p>
            </div>
          )}
        </Panel>
        <Panel title="Bridge agreements">
          {bridges.length === 0 && <span style={{ fontSize: font.size.md, color: color.textMuted }}>No agreements yet.</span>}
          {bridges.map((b, i) => (
            <ListRow
              key={b.id}
              first={i === 0}
              title={`${usd(b.amount)} · ${b.term_days} days`}
              subtitle={`Started ${shortDate(b.opened_at)}`}
              amount={agreement === b.id ? 'Hide' : 'View'}
              onClick={() => setAgreement(agreement === b.id ? null : b.id)}
            />
          ))}
          {chosen !== undefined && (
            <div style={{ marginTop: space.lg, background: color.surfaceSubtle, borderRadius: '12px', padding: space.xl }}>
              <Row label="Borrower" value={customer.name} />
              <Row label="Principal" value={usd(chosen.amount)} />
              <Row label="Fee" value={usd(priceOffer(chosen.amount, chosen.term_days).fee, { cents: true })} />
              <Row label="Term" value={`${chosen.term_days} days`} />
              <Row label="Start" value={shortDate(chosen.opened_at)} />
              <Row label="Repay by" value={shortDate(addDays(chosen.opened_at, chosen.term_days))} />
              <p style={{ margin: `${space.lg} 0 0`, fontSize: font.size.sm, color: color.textMuted }}>
                Repaid automatically from incoming deposits. The fee covers the full term, even if repaid early.
              </p>
            </div>
          )}
        </Panel>
      </div>
    </>
  );
}
