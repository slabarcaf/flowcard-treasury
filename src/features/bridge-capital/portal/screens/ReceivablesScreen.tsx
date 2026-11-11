/** Invoices the customer's own customers owe them, and when each usually pays. */
import { useState } from 'react';
import { expectedArrival } from '../../policy/forecast.js';
import { preApproved } from '../../policy/preApproved.js';
import { daysBetween } from '../../policy/util.js';
import { color, font, space, type ToneName } from '../tokens.js';
import { usd, shortDate, pct } from '../../ui/format.js';
import type { Portal } from '../usePortal.js';
import { Pill, Row } from '../components/ui.js';
import { Kpi, PageHeader, Panel, SmallButton } from '../components/layout.js';

function reliability(rate: number): ToneName {
  return rate >= 0.9 ? 'positive' : rate >= 0.7 ? 'warning' : 'critical';
}

export function ReceivablesScreen({ portal }: { portal: Portal }) {
  const { data, customer, today, go } = portal;
  const [open, setOpen] = useState<number | null>(null);
  if (data === null || customer === null) return null;

  const rows = customer.expected_receivables;
  const total = rows.reduce((s, r) => s + r.amount, 0);
  const pa = preApproved(customer, data.capital, today);

  return (
    <>
      <PageHeader title="Receivables" sub="Invoices your customers owe you, and when they usually pay." />
      <div className="bcp-g3">
        <Panel><Kpi label="Expected in" value={usd(total)} /></Panel>
        <Panel><Kpi label="Open invoices" value={rows.length} /></Panel>
        <Panel><Kpi label="Largest payer’s share of inflows" value={pct(customer.inflow_stats.top_payer_concentration, 0)} /></Panel>
      </div>
      <Panel>
        {rows.length === 0 && <span style={{ fontSize: font.size.md, color: color.textMuted }}>No open invoices.</span>}
        {rows.map((r, i) => {
          const arrives = expectedArrival(r);
          const days = Math.max(0, daysBetween(today, arrives));
          return (
            <div key={`${r.payer_name}_${r.due_date}`} style={{ boxShadow: i === 0 ? 'none' : `inset 0 1px 0 ${color.border}` }}>
              <button
                type="button"
                onClick={() => setOpen(open === i ? null : i)}
                style={{ width: '100%', display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: space.lg, flexWrap: 'wrap', background: 'none', border: 'none', padding: `${space.xl} 0`, color: color.text, fontFamily: font.sans, textAlign: 'left', cursor: 'pointer' }}
              >
                <span style={{ display: 'grid', gap: '2px' }}>
                  <span style={{ fontSize: font.size.lg, fontWeight: font.weight.medium }}>{r.payer_name}</span>
                  <span style={{ fontSize: font.size.md, color: color.textSecondary }}>
                    Due {shortDate(r.due_date)} · usually arrives {shortDate(arrives)}
                  </span>
                </span>
                <span style={{ display: 'flex', alignItems: 'center', gap: space.lg }}>
                  <Pill tone={reliability(r.payer_on_time_rate)}>{pct(r.payer_on_time_rate, 0)} on time</Pill>
                  <span style={{ fontSize: font.size.lg, fontWeight: font.weight.medium, fontVariantNumeric: 'tabular-nums' }}>{usd(r.amount)}</span>
                </span>
              </button>
              {open === i && (
                <div style={{ display: 'grid', gap: space.lg, paddingBottom: space.xl }}>
                  <div className="bcp-g2">
                    <div>
                      <Row label="Pays on time" value={pct(r.payer_on_time_rate, 0)} />
                      <Row label="Average days late" value={`${Math.round(r.payer_avg_days_late)} days`} />
                      <Row label="Your customer for" value={`${r.relationship_months} months`} />
                    </div>
                    <div style={{ display: 'grid', gap: space.lg, alignContent: 'start' }}>
                      <span style={{ fontSize: font.size.md, lineHeight: 1.55, color: color.textSecondary }}>
                        {pa.amount > 0
                          ? `This invoice can back a bridge of up to ${usd(Math.min(pa.amount, r.amount))} for about ${days} days, repaid when it lands on ${shortDate(arrives)}.`
                          : pa.reason}
                      </span>
                      {pa.amount > 0 && (
                        <div>
                          <SmallButton variant="primary" onClick={() => go({ screen: 'bridges' })}>Plan a bridge against it</SmallButton>
                        </div>
                      )}
                    </div>
                  </div>
                </div>
              )}
            </div>
          );
        })}
      </Panel>
    </>
  );
}
