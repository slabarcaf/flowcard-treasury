/**
 * After acceptance: retry the payment, or re-accept if the terms moved.
 *
 * The acceptance is bound to amount, fee and term. If any of them changed
 * before the money moved, the engine says the acceptance is void and the
 * customer is shown the new offer instead of a retry button.
 */
import { buildOffer, priceOffer } from '../../policy/pricing.js';
import { isAcceptanceValid, isApprovalValid } from '../../policy/binding.js';
import { evaluate } from '../../policy/eligibility.js';
import { color, font, space } from '../tokens.js';
import { usd, shortDate } from '../../ui/format.js';
import type { Portal } from '../usePortal.js';
import { BackLink, Button, Callout, Card, Heading, Row, SectionTitle } from '../components/ui.js';

export function AcceptedScreen({ portal, transactionId }: { portal: Portal; transactionId: string }) {
  const { data, customer, go, termFor, acceptanceFor, accept, retryPayment } = portal;
  if (data === null || customer === null) return null;

  const request = data.requests.find((r) => r.transaction.id === transactionId);
  if (request === undefined) return null;

  const termDays = termFor(transactionId);
  const offer = buildOffer({ ...request, term_days: termDays });
  const price = priceOffer(request.principal, termDays);
  const acceptance = acceptanceFor(offer.id);

  if (acceptance === undefined) {
    return (
      <div style={{ display: 'grid', gap: space['2xl'] }}>
        <BackLink onClick={() => go({ screen: 'wallet' })}>Wallet</BackLink>
        <Heading>Nothing accepted yet</Heading>
        <Button variant="secondary" onClick={() => go({ screen: 'decline', transactionId })}>
          See the offer
        </Button>
      </div>
    );
  }

  const stillValid = isAcceptanceValid(acceptance, offer);

  if (!stillValid) {
    return (
      <div style={{ display: 'grid', gap: space['2xl'] }}>
        <BackLink onClick={() => go({ screen: 'wallet' })}>Wallet</BackLink>
        <Heading sub="The terms moved before the money went out, so what you accepted no longer stands. Here is the new offer.">
          Your terms changed
        </Heading>

        <Card>
          <div style={{ display: 'grid', gap: space.xl }}>
            <SectionTitle>You accepted</SectionTitle>
            <div>
              <Row label="Amount" value={usd(acceptance.principal)} />
              <Row label="Term" value={`${acceptance.term_days} days`} />
              <Row label="Fee" value={usd(acceptance.fee, { cents: true })} />
            </div>
          </div>
        </Card>

        <Card>
          <div style={{ display: 'grid', gap: space.xl }}>
            <SectionTitle>New offer</SectionTitle>
            <div>
              <Row label="Amount" value={usd(offer.principal)} strong />
              <Row label="Term" value={`${offer.term_days} days`} strong />
              <Row label="Fee" value={usd(offer.fee, { cents: true })} strong />
              <Row label="You repay" value={usd(price.total, { cents: true })} />
              <Row label="By" value={shortDate(offer.due_date)} />
            </div>
          </div>
        </Card>

        <Button onClick={() => void accept(request, offer)}>Accept the new terms</Button>
        <Button variant="secondary" onClick={() => go({ screen: 'decline', transactionId })}>
          Change the term
        </Button>
      </div>
    );
  }

  const decision = evaluate({ ...request, term_days: termDays }, customer, data.capital, { today: portal.today });
  const approval = data.approvals[offer.id];
  const awaitingApproval = decision.needs_human_approval && (approval === undefined || !isApprovalValid(approval, offer));

  if (!decision.eligible || awaitingApproval) {
    return (
      <div style={{ display: 'grid', gap: space['2xl'] }}>
        <BackLink onClick={() => go({ screen: 'wallet' })}>Wallet</BackLink>
        <Heading>{awaitingApproval ? 'Accepted, awaiting approval' : 'This bridge needs review'}</Heading>
        <Callout tone="warning">
          {awaitingApproval
            ? 'Your acceptance is saved. A credit officer must approve these exact terms before the payment can be retried.'
            : 'The current policy checks do not allow funding. Your wallet has not been funded.'}
        </Callout>
        <Button variant="secondary" onClick={() => void portal.reload()}>Check approval status</Button>
        <Button variant="secondary" onClick={() => go({ screen: 'decline', transactionId })}>Review the offer</Button>
      </div>
    );
  }

  return (
    <div style={{ display: 'grid', gap: space['2xl'] }}>
      <BackLink onClick={() => go({ screen: 'wallet' })}>Wallet</BackLink>

      <Heading sub="We are ready to cover the shortfall. Retry the payment and the money moves into your wallet first.">
        Accepted
      </Heading>

      <Card>
        <div>
          <Row label="We cover" value={usd(offer.principal)} strong />
          <Row label="Fee" value={usd(offer.fee, { cents: true })} />
          <Row label="You repay" value={usd(price.total, { cents: true })} strong />
          <Row label="By" value={shortDate(offer.due_date)} />
        </div>
      </Card>

      <Callout>
        Accepted on these exact terms. If the amount, fee or term changes before the money moves,
        we will ask you to accept again.
      </Callout>

      {portal.actionError !== null && <Callout tone="warning">{portal.actionError}</Callout>}

      <Button disabled={portal.retrying} onClick={() => void retryPayment(request, offer)}>
        {portal.retrying ? 'Retrying payment…' : `Retry payment to ${request.transaction.merchant_name}`}
      </Button>

      <Button variant="secondary" onClick={() => go({ screen: 'decline', transactionId })}>
        Change the term
      </Button>

      <span style={{ fontSize: font.size.md, color: color.textMuted, textAlign: 'center' }}>
        {usd(offer.principal)} lands in your wallet, then the card payment runs again.
      </span>
    </div>
  );
}
