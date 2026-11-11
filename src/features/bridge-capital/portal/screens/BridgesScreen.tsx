/** Bridges: what is running, planning a new one, and every past bridge. */
import { useState } from 'react';
import { applyDelinquency } from '../../policy/delinquency.js';
import { backingReceivable, forecastBalance } from '../../policy/forecast.js';
import { preApproved } from '../../policy/preApproved.js';
import { priceOffer } from '../../policy/pricing.js';
import { amountDue } from '../../policy/repayment.js';
import { addDays } from '../../policy/util.js';
import { color, font, space } from '../tokens.js';
import { usd, shortDate } from '../../ui/format.js';
import { openBridges, type Portal } from '../usePortal.js';
import { OUTCOME_COPY, PURPOSES, restrictionSentence } from '../copy.js';
import { ListRow, Pill, Row } from '../components/ui.js';
import { TermPicker } from '../components/TermPicker.js';
import { Banner, Chips, PageHeader, Panel, SmallButton } from '../components/layout.js';
import { inputStyle } from '../components/styles.js';

export function BridgesScreen({ portal }: { portal: Portal }) {
  const { data, customer, today, go, wallet, myBridges, access, raiseRequest } = portal;
  const pa = data === null || customer === null ? null : preApproved(customer, data.capital, today);
  const suggested = (() => {
    if (data === null || customer === null || pa === null || pa.amount === 0) return 1_000;
    const f = forecastBalance(customer, wallet, data.transactions, today);
    const need = f.crossing === null ? 1_000 : Math.ceil((f.low_line - f.lowest.balance) / 100) * 100;
    return Math.max(100, Math.min(Math.floor(pa.amount / 100) * 100, need));
  })();
  const [amount, setAmount] = useState(suggested);
  const [term, setTerm] = useState(30);
  const [category, setCategory] = useState('suppliers');
  if (data === null || customer === null || pa === null) return null;

  const open = openBridges(myBridges);
  const past = customer.bridge_history.filter((h) => h.outcome !== 'OPEN').sort((a, b) => (b.closed_at ?? '').localeCompare(a.closed_at ?? ''));
  const valid = amount >= 100 && amount <= pa.amount;
  const price = priceOffer(Math.max(0, amount), term);
  const backing = backingReceivable(customer, term, today);
  const restriction = restrictionSentence(access);

  return (
    <>
      <PageHeader title="Bridges" sub="Short-term funding when a card payment or a bill would otherwise fail." />

      {open.map((b) => {
        const late = applyDelinquency(b, today).days_past_due;
        return (
          <Panel
            key={b.id}
            title={`Bridge ${usd(b.principal)}`}
            meta={late > 0 ? <Pill tone="critical">Overdue by {late} days</Pill> : <Pill tone="accent">Repaying · due {shortDate(b.due_date)}</Pill>}
          >
            <Row label="Amount due" value={usd(amountDue(b, today), { cents: true })} strong />
            <Row label="Fee" value={usd(b.fee, { cents: true })} />
            {late === 0 && <Row label="Due" value={shortDate(b.due_date)} />}
            {late > 0 && restriction !== null && (
              <p style={{ margin: `${space.lg} 0 0`, fontSize: font.size.md, color: color.textSecondary }}>{restriction}</p>
            )}
            <div style={{ display: 'flex', gap: space.md, marginTop: space.xl, flexWrap: 'wrap' }}>
              <SmallButton variant="primary" onClick={() => go({ screen: 'bridge', bridgeId: b.id })}>{late > 0 ? 'Repay now' : 'Repay early'}</SmallButton>
            </div>
          </Panel>
        );
      })}

      {pa.amount > 0 ? (
        <Panel title="Plan a bridge" meta={`Up to ${usd(pa.amount)}`}>
          <div className="bcp-g2">
            <label style={{ display: 'grid', gap: space.sm }}>
              <span style={{ fontSize: font.size.md, color: color.textSecondary }}>Amount (USD)</span>
              <input
                type="number"
                min={100}
                step={100}
                value={amount}
                onChange={(e) => setAmount(Math.max(0, Math.round(Number(e.target.value) || 0)))}
                style={inputStyle}
              />
            </label>
            <div style={{ display: 'grid', gap: space.sm }}>
              <span style={{ fontSize: font.size.md, color: color.textSecondary }}>What it is for</span>
              <Chips
                options={PURPOSES.map((p) => p.category)}
                value={category}
                onChange={setCategory}
                label={(c) => PURPOSES.find((p) => p.category === c)?.label ?? c}
              />
            </div>
          </div>
          <div style={{ display: 'grid', gap: space.sm, marginTop: space.xl }}>
            <span style={{ fontSize: font.size.md, color: color.textSecondary }}>How long you need</span>
            <TermPicker value={term} onChange={setTerm} />
          </div>
          <div style={{ marginTop: space.xl }}>
            <Row label="Fee ($50 + 2% a month)" value={usd(price.fee, { cents: true })} />
            <Row label="You repay" value={usd(price.total, { cents: true })} strong />
            <Row label="Repay by" value={shortDate(addDays(today, term))} />
          </div>
          <p style={{ margin: `${space.lg} 0 0`, fontSize: font.size.sm, color: backing === null ? color.warning : color.textMuted }}>
            {backing === null
              ? 'No expected deposit arrives within this term. Choose a longer term.'
              : `Expected to be repaid from ${backing.payer_name} (${usd(backing.amount)}, usually arrives ${shortDate(backing.expected_date)}).`}
          </p>
          <div style={{ display: 'flex', gap: space.md, alignItems: 'center', marginTop: space.xl, flexWrap: 'wrap' }}>
            <SmallButton variant="primary" disabled={!valid} onClick={() => void raiseRequest('PLANNED', { amount, termDays: term, category })}>
              See my offer
            </SmallButton>
            {!valid && <span style={{ fontSize: font.size.sm, color: color.textMuted }}>Enter between $100 and {usd(pa.amount)}.</span>}
          </div>
        </Panel>
      ) : (
        <Banner title="New bridges aren’t available right now">{pa.reason}</Banner>
      )}

      <Panel title="Past bridges" meta={past.length}>
        {past.length === 0 ? (
          <span style={{ fontSize: font.size.md, color: color.textMuted }}>No past bridges yet.</span>
        ) : (
          past.map((h, i) => (
            <ListRow
              key={h.id}
              first={i === 0}
              title={`${usd(h.amount)} · ${h.term_days} days`}
              subtitle={`${shortDate(h.opened_at)} – ${h.closed_at === null ? 'open' : shortDate(h.closed_at)}`}
              amount={OUTCOME_COPY[h.outcome].label}
              tone={OUTCOME_COPY[h.outcome].tone}
              onClick={() => go({ screen: 'pastBridge', pastBridgeId: h.id })}
            />
          ))
        )}
      </Panel>
    </>
  );
}
