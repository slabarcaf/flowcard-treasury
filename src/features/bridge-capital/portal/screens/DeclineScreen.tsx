/**
 * The decline moment: what happened, and the offer if there is one.
 *
 * Eligibility, the amount and the fee all come from the engine. Nothing here
 * decides anything; the screen only chooses words for what it is handed.
 */
import { evaluate } from '../../policy/eligibility.js';
import { buildOffer, priceOffer } from '../../policy/pricing.js';
import { PRICING, WHILE_BRIDGED } from '../../policy/policy.config.js';
import { color, font, space } from '../tokens.js';
import { usd, shortDate } from '../../ui/format.js';
import { guidanceFor, reasonFor } from '../guidance.js';
import type { Portal } from '../usePortal.js';
import { BackLink, Button, Callout, Card, Heading, Row, SectionTitle } from '../components/ui.js';
import { TermPicker } from '../components/TermPicker.js';

export function DeclineScreen({ portal, transactionId }: { portal: Portal; transactionId: string }) {
  const { data, customer, today, go, termFor, setTerm, accept, acceptanceFor } = portal;
  if (data === null || customer === null) return null;

  const request = data.requests.find((r) => r.transaction.id === transactionId);
  if (request === undefined) return null;
  const txn = request.transaction;

  const termDays = termFor(transactionId);
  const decision = evaluate({ ...request, term_days: termDays }, customer, data.capital, { today });
  const price = priceOffer(request.principal, termDays);
  const offer = buildOffer({ ...request, term_days: termDays });
  const firstFailure = decision.rules.find((r) => !r.passed);
  const acceptance = acceptanceFor(offer.id);
  const termMoved = acceptance !== undefined && acceptance.term_days !== termDays;
  const restrictedWhileBridged = WHILE_BRIDGED.restrictedTiers.includes(decision.tier);

  return (
    <div style={{ display: 'grid', gap: space['2xl'] }}>
      <BackLink onClick={() => go({ screen: 'wallet' })}>Wallet</BackLink>

      <Heading
        sub={`Your wallet held ${usd(txn.wallet_balance)} at the time, so the payment was ${usd(request.principal)} short.`}
      >
        {usd(txn.amount)} to {txn.merchant_name} didn’t go through
      </Heading>

      {acceptance !== undefined && (
        <Callout
          title={termMoved ? 'This changes what you accepted' : 'You have already accepted this'}
          tone={termMoved ? 'warning' : 'neutral'}
        >
          You accepted {usd(acceptance.principal)} over {acceptance.term_days} days for a{' '}
          {usd(acceptance.fee, { cents: true })} fee.{' '}
          {termMoved
            ? 'Moving to a different term voids that acceptance, so we will ask you to accept the new terms.'
            : 'Go back to retry the payment whenever you are ready.'}
        </Callout>
      )}

      {acceptance !== undefined && (
        <Button variant="secondary" onClick={() => go({ screen: 'accepted', transactionId })}>
          Back to your acceptance
        </Button>
      )}

      {decision.eligible ? (
        <>
          <Card>
            <div style={{ display: 'grid', gap: space['2xl'] }}>
              <SectionTitle>We can cover the shortfall</SectionTitle>

              <div>
                <Row label="Amount" value={usd(request.principal)} strong />
                <Row label="Fee" value={usd(price.fee, { cents: true })} />
                <Row label="You repay" value={usd(price.total, { cents: true })} strong />
                <Row label="By" value={shortDate(offer.due_date)} />
              </div>

              <div style={{ display: 'grid', gap: space.lg }}>
                <SectionTitle>Choose how long you need</SectionTitle>
                <TermPicker value={termDays} onChange={(days) => setTerm(transactionId, days)} />
                <span style={{ fontSize: font.size.md, color: color.textMuted }}>
                  ${PRICING.serviceFee} service fee plus {PRICING.monthlyRate * 100}% a month on{' '}
                  {usd(request.principal)}, worked out day by day over the {termDays} days.
                </span>
              </div>
            </div>
          </Card>

          <Callout title="The fee is for the full term" tone="warning">
            If you repay early the fee stays {usd(price.fee, { cents: true })} — it is not reduced for
            the days you did not use.
          </Callout>

          <Callout title="How you repay">
            We collect automatically from deposits as they land in your wallet, fee first and then
            the amount we covered, until the {usd(price.total, { cents: true })} is cleared. There is
            nothing for you to schedule.
          </Callout>

          {restrictedWhileBridged && (
            <Callout title="While the bridge is open" tone="warning">
              Your card will be limited to essential spend — suppliers, payroll and inventory —
              capped at {usd(request.principal)}. The limit lifts as soon as you have repaid in full.
            </Callout>
          )}

          <Button onClick={() => void accept(request, offer)}>
            Accept {usd(request.principal)} for {termDays} days · fee {usd(price.fee, { cents: true })}
          </Button>
        </>
      ) : (
        <>
          <Card>
            <div style={{ display: 'grid', gap: space.xl }}>
              <SectionTitle>We can’t cover this one</SectionTitle>
              <p style={{ margin: 0, fontSize: font.size.lg, lineHeight: 1.55, color: color.text }}>
                {firstFailure === undefined
                  ? 'This payment is not eligible for a bridge right now.'
                  : reasonFor(firstFailure, decision)}
              </p>
            </div>
          </Card>

          <Callout title="What you can do">
            {guidanceFor(firstFailure?.rule_id ?? '')}
          </Callout>

          <Button variant="secondary" onClick={() => go({ screen: 'wallet' })}>
            Back to wallet
          </Button>
        </>
      )}
    </div>
  );
}
