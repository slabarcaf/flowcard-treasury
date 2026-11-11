/**
 * A running bridge, in the customer's terms: what is due, by when, and a way to
 * repay. If it is late, one sentence on the card restriction in force. The
 * ladder, collections log and internal actions are back office only (spec §8.4).
 */
import { useState } from 'react';
import { applyDelinquency } from '../../policy/delinquency.js';
import { amountDue } from '../../policy/repayment.js';
import { PRICING } from '../../policy/policy.config.js';
import { color, font, space } from '../tokens.js';
import { usd, shortDate } from '../../ui/format.js';
import type { Portal } from '../usePortal.js';
import { restrictionSentence } from '../copy.js';
import { BackLink, Pill, Row } from '../components/ui.js';
import { Kpi, PageHeader, Panel, SmallButton } from '../components/layout.js';
import { inputStyle } from '../components/styles.js';

export function ActiveBridgeScreen({ portal, bridgeId }: { portal: Portal; bridgeId: string }) {
  const { data, today, go, wallet, access, repay } = portal;
  const bridge = data?.bridges.find((b) => b.id === bridgeId);
  const due = bridge === undefined ? 0 : amountDue(bridge, today);
  const [pay, setPay] = useState(() => Math.min(due, Math.max(0, wallet)));
  if (data === null || bridge === undefined) return null;

  if (bridge.status === 'REPAID') {
    return (
      <>
        <BackLink onClick={() => go({ screen: 'bridges' })}>Bridges</BackLink>
        <PageHeader title={`Bridge ${usd(bridge.principal)}`} sub="Repaid in full. Thank you." />
      </>
    );
  }

  const late = applyDelinquency(bridge, today).days_past_due;
  const restriction = late > 0 ? restrictionSentence(access) : null;
  const canPay = pay > 0 && pay <= Math.min(due, wallet) + 0.005;
  const request = data.requests.find((r) => r.transaction.id === bridge.transaction_id);

  return (
    <>
      <BackLink onClick={() => go({ screen: 'bridges' })}>Bridges</BackLink>
      <PageHeader
        title={`Bridge ${usd(bridge.principal)}`}
        sub={request === undefined ? undefined : `Covered ${request.transaction.merchant_name}`}
        action={late > 0 ? <Pill tone="critical">Overdue by {late} days</Pill> : <Pill tone="accent">Repaying · due {shortDate(bridge.due_date)}</Pill>}
      />

      <div className="bcp-g2">
        <Panel>
          <div style={{ display: 'grid', gap: space.xl }}>
            <Kpi label="Amount due" value={usd(due, { cents: true })} valueColor={late > 0 ? color.critical : undefined} />
            <div>
              <Row label="We covered" value={usd(bridge.principal)} />
              <Row label="Fee" value={usd(bridge.fee, { cents: true })} />
              <Row label={late > 0 ? 'Was due' : 'Due'} value={shortDate(bridge.due_date)} />
            </div>
            {restriction !== null && <p style={{ margin: 0, fontSize: font.size.md, lineHeight: 1.55, color: color.textSecondary }}>{restriction}</p>}
            {late === 0 && (
              <p style={{ margin: 0, fontSize: font.size.sm, color: color.textMuted }}>
                We take repayments automatically from incoming deposits. You can also repay early; the fee covers the full term and stays the same.
              </p>
            )}
          </div>
        </Panel>

        <Panel title="Repay from your wallet" meta={`Wallet ${usd(wallet, { cents: true })}`}>
          <div style={{ display: 'grid', gap: space.lg }}>
            <label style={{ display: 'grid', gap: space.sm }}>
              <span style={{ fontSize: font.size.md, color: color.textSecondary }}>Amount (USD)</span>
              <input
                type="number"
                min={1}
                step={10}
                value={pay}
                onChange={(e) => setPay(Math.max(0, Number(e.target.value) || 0))}
                style={inputStyle}
              />
            </label>
            <div style={{ display: 'flex', gap: space.md, flexWrap: 'wrap' }}>
              <SmallButton onClick={() => setPay(Math.min(due, wallet))}>Pay all I can</SmallButton>
            </div>
            <SmallButton variant="primary" disabled={!canPay} onClick={() => void repay(bridge, pay)}>
              Pay {usd(pay, { cents: true })}
            </SmallButton>
            {wallet < due && (
              <span style={{ fontSize: font.size.sm, color: color.textMuted }}>
                Your wallet covers {usd(Math.max(0, wallet), { cents: true })}. The rest comes from your next deposits.
              </span>
            )}
          </div>
        </Panel>
      </div>

      <Panel title="Your agreement">
        <Row label="Principal" value={usd(bridge.principal)} />
        <Row label="Fee" value={`${usd(bridge.fee, { cents: true })} ($${PRICING.serviceFee} + ${PRICING.monthlyRate * 100}% a month × ${bridge.term_days}/30)`} />
        <Row label="Term" value={`${bridge.term_days} days`} />
        <Row label="Started" value={bridge.disbursed_at === null ? '—' : shortDate(bridge.disbursed_at)} />
        <Row label="Repay by" value={shortDate(bridge.due_date)} />
        <p style={{ margin: `${space.lg} 0 0`, fontSize: font.size.sm, color: color.textMuted }}>
          Repaid automatically from incoming deposits. A late balance keeps accruing at {PRICING.monthlyRate * 100}% a month.
        </p>
      </Panel>
    </>
  );
}
