/** Supplier bills: pay from the wallet, or Flex over 30, 60 or 90 days (spec §8.3). */
import { useState } from 'react';
import type { Bill } from '../../types.js';
import { preApproved } from '../../policy/preApproved.js';
import { priceOffer } from '../../policy/pricing.js';
import { FLEX } from '../../policy/policy.config.js';
import { color, font, space } from '../tokens.js';
import { usd, shortDate } from '../../ui/format.js';
import type { Portal } from '../usePortal.js';
import { categoryLabel } from '../copy.js';
import { Pill } from '../components/ui.js';
import { Banner, Chips, PageHeader, Panel, SmallButton } from '../components/layout.js';

export function BillsScreen({ portal }: { portal: Portal }) {
  const { data, customer, today, wallet, payBill, raiseRequest } = portal;
  const [flexing, setFlexing] = useState<string | null>(null);
  const [term, setTerm] = useState<number>(FLEX.termDays[0]);
  if (data === null || customer === null) return null;

  const bills = data.bills.filter((b) => b.customer_id === customer.id).sort((a, b) => a.due_date.localeCompare(b.due_date));
  const pa = preApproved(customer, data.capital, today);

  const status = (b: Bill) =>
    b.status === 'PAID' ? <Pill tone="positive">Paid {b.paid_at === null ? '' : shortDate(b.paid_at)}</Pill>
    : b.status === 'FLEXED' ? <Pill tone="accent">Paid with Flex</Pill>
    : <Pill>Due {shortDate(b.due_date)}</Pill>;

  return (
    <>
      <PageHeader title="Bills" sub="Pay suppliers on time. Flex pays them today and lets you repay us over 30, 60 or 90 days." />
      <Banner title={pa.amount > 0 ? `Flex available: up to ${usd(pa.amount)}` : 'Flex isn’t available right now'} tone={pa.amount > 0 ? 'accent' : 'neutral'}>
        {pa.amount > 0 ? 'Same terms as a bridge: $50 plus 2% a month, repaid from your deposits.' : pa.reason}
      </Banner>

      <Panel>
        <div style={{ display: 'grid' }}>
          {bills.map((b, i) => {
            const open = b.status === 'OPEN';
            const price = priceOffer(b.amount, term);
            return (
              <div key={b.id} style={{ display: 'grid', gap: space.lg, padding: `${space.xl} 0`, boxShadow: i === 0 ? 'none' : `inset 0 1px 0 ${color.border}` }}>
                <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: space.lg, flexWrap: 'wrap' }}>
                  <div style={{ display: 'grid', gap: '2px', minWidth: 0 }}>
                    <span style={{ fontSize: font.size.lg, fontWeight: font.weight.medium }}>{b.vendor}</span>
                    <span style={{ fontSize: font.size.md, color: color.textSecondary }}>{categoryLabel(b.merchant_category)}</span>
                  </div>
                  <div style={{ display: 'flex', alignItems: 'center', gap: space.lg, flexWrap: 'wrap' }}>
                    <span style={{ fontSize: font.size.lg, fontWeight: font.weight.medium, fontVariantNumeric: 'tabular-nums' }}>{usd(b.amount)}</span>
                    {status(b)}
                    {open && (
                      <>
                        <SmallButton disabled={wallet < b.amount} onClick={() => void payBill(b)}>Pay now</SmallButton>
                        <SmallButton variant="primary" disabled={pa.amount < b.amount} onClick={() => setFlexing(flexing === b.id ? null : b.id)}>
                          Flex
                        </SmallButton>
                      </>
                    )}
                  </div>
                </div>
                {open && wallet < b.amount && flexing !== b.id && (
                  <span style={{ fontSize: font.size.sm, color: color.textMuted }}>Your wallet is {usd(b.amount - wallet)} short to pay this now.</span>
                )}
                {flexing === b.id && (
                  <div style={{ display: 'grid', gap: space.lg, background: color.surfaceSubtle, borderRadius: '12px', padding: space.xl }}>
                    <Chips options={FLEX.termDays} value={term} onChange={setTerm} label={(d) => `${d} days`} />
                    <span style={{ fontSize: font.size.md, color: color.textSecondary }}>
                      Fee {usd(price.fee, { cents: true })} · you repay {usd(price.total, { cents: true })}
                    </span>
                    <div>
                      <SmallButton
                        variant="primary"
                        onClick={() => void raiseRequest('BILL', { amount: b.amount, termDays: term, category: b.merchant_category }, b)}
                      >
                        See Flex offer
                      </SmallButton>
                    </div>
                  </div>
                )}
              </div>
            );
          })}
        </div>
      </Panel>
    </>
  );
}
