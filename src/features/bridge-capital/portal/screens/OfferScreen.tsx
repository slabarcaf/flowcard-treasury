/**
 * The offer for a planned bridge or Flex on a bill (spec §8.3). Same engine as a
 * card decline: eligibility, price and approval all come from the decision.
 */
import { evaluate } from '../../policy/eligibility.js';
import { buildOffer } from '../../policy/pricing.js';
import { PRICING } from '../../policy/policy.config.js';
import { color, font, space } from '../tokens.js';
import { usd, shortDate } from '../../ui/format.js';
import type { Portal } from '../usePortal.js';
import { guidanceFor, reasonFor } from '../guidance.js';
import { categoryLabel } from '../copy.js';
import { BackLink, Row } from '../components/ui.js';
import { Banner, PageHeader, Panel, SmallButton } from '../components/layout.js';

export function OfferScreen({ portal, requestId }: { portal: Portal; requestId: string }) {
  const { data, customer, today, go, acceptAndFund } = portal;
  if (data === null || customer === null) return null;
  const request = data.requests.find((r) => r.id === requestId);
  if (request === undefined) return null;

  const isBill = request.purpose === 'BILL';
  const back = isBill ? 'bills' : 'bridges';
  const decision = evaluate(request, customer, data.capital, { today });
  const offer = buildOffer(request);
  const firstFailure = decision.rules.find((r) => !r.passed);

  return (
    <>
      <BackLink onClick={() => go({ screen: back })}>{isBill ? 'Bills' : 'Bridges'}</BackLink>
      <PageHeader
        title={isBill ? `Flex ${request.transaction.merchant_name}` : 'Your bridge offer'}
        sub={isBill ? `We pay ${request.transaction.merchant_name} today. You repay us over ${offer.term_days} days.` : `For ${categoryLabel(request.transaction.merchant_category).toLowerCase()}.`}
      />

      {decision.eligible ? (
        <div className="bcp-g2">
          <Panel title="The offer">
            <Row label={isBill ? 'Bill' : 'Amount'} value={usd(offer.principal)} strong />
            <Row label="Fee" value={usd(offer.fee, { cents: true })} />
            <Row label="You repay" value={usd(offer.principal + offer.fee, { cents: true })} strong />
            <Row label="By" value={shortDate(offer.due_date)} />
            <div style={{ marginTop: space.xl }}>
              <SmallButton variant="primary" onClick={() => void acceptAndFund(request, offer, decision.needs_human_approval)}>
                {isBill ? `Accept and pay ${request.transaction.merchant_name}` : `Accept and get ${usd(offer.principal)}`}
              </SmallButton>
            </div>
          </Panel>
          <div style={{ display: 'grid', gap: space.lg, alignContent: 'start' }}>
            <Banner title="The fee is for the full term" tone="warning">
              ${PRICING.serviceFee} plus {PRICING.monthlyRate * 100}% a month, worked out day by day. Repaying early doesn’t reduce it.
            </Banner>
            <Banner title="How you repay">
              We collect from your deposits as they land, fee first, until it’s cleared. If the amount, fee or term changes before the money moves, we ask you to accept again.
            </Banner>
            {decision.needs_human_approval && (
              <Banner title="A person checks this one" tone="accent">
                Bridges of this size get a quick review from our credit team before the money moves.
              </Banner>
            )}
          </div>
        </div>
      ) : (
        <Panel title="We can’t offer this one">
          <p style={{ margin: 0, fontSize: font.size.lg, lineHeight: 1.55, color: color.text }}>
            {firstFailure === undefined ? 'This is not eligible right now.' : reasonFor(firstFailure, decision)}
          </p>
          <p style={{ margin: `${space.lg} 0 0`, fontSize: font.size.md, lineHeight: 1.55, color: color.textSecondary }}>
            {guidanceFor(firstFailure?.rule_id ?? '')}
          </p>
          <div style={{ marginTop: space.xl }}>
            <SmallButton onClick={() => go({ screen: back })}>Back</SmallButton>
          </div>
        </Panel>
      )}
    </>
  );
}
