/** One card payment, in full. */
import { color, font, space } from '../tokens.js';
import { usd, shortDate, timeOf, titleCase } from '../../ui/format.js';
import type { Portal } from '../usePortal.js';
import { Amount, BackLink, Button, Callout, Card, Row } from '../components/ui.js';

/**
 * A stable four-digit stand-in for the card's last four.
 * The mock cards carry ids like `card_lumen`, not PANs.
 */
function cardLast4(cardId: string): string {
  let hash = 0;
  for (const ch of cardId) hash = (hash * 31 + ch.charCodeAt(0)) % 10_000;
  return String(hash).padStart(4, '0');
}

export function TransactionScreen({
  portal,
  transactionId,
}: {
  portal: Portal;
  transactionId: string;
}) {
  const { data, customer, go } = portal;
  if (data === null || customer === null) return null;

  const txn = data.transactions.find((t) => t.id === transactionId);
  if (txn === undefined) {
    return (
      <Card>
        <span style={{ color: color.textSecondary }}>That payment is no longer on file.</span>
      </Card>
    );
  }

  const declined = txn.status === 'DECLINED';
  const request = data.requests.find((r) => r.transaction.id === txn.id);
  const bridge = data.bridges.find((b) => b.transaction_id === txn.id);
  const shortfall = Math.max(0, txn.amount - txn.wallet_balance);

  return (
    <div style={{ display: 'grid', gap: space['2xl'] }}>
      <BackLink onClick={() => go({ screen: 'activity' })}>Activity</BackLink>

      <Card>
        <div style={{ display: 'grid', gap: space.lg }}>
          <Amount
            label={txn.merchant_name}
            value={usd(txn.amount)}
            tone={declined ? 'critical' : undefined}
          />
          <span style={{ fontSize: font.size.md, color: color.textSecondary }}>
            {shortDate(txn.created_at)} at {timeOf(txn.created_at)}
          </span>
        </div>
      </Card>

      <Card>
        <div style={{ display: 'grid' }}>
          <Row label="Status" value={declined ? 'Declined' : titleCase(txn.status)} />
          <Row label="Category" value={titleCase(txn.merchant_category)} />
          <Row label="Card" value={`•••• ${cardLast4(txn.card_id)}`} />
          {declined && <Row label="Wallet balance at the time" value={usd(txn.wallet_balance)} />}
          {declined && <Row label="Short by" value={usd(shortfall)} strong />}
        </div>
      </Card>

      {declined && bridge === undefined && request !== undefined && (
        <>
          <Callout tone="neutral" title="This payment did not go through">
            You were {usd(shortfall)} short. We can cover the shortfall and collect it back from
            your next deposits.
          </Callout>
          <Button onClick={() => go({ screen: 'decline', transactionId: txn.id })}>
            See your options
          </Button>
        </>
      )}

      {bridge !== undefined && (
        <Button variant="secondary" onClick={() => go({ screen: 'bridge', bridgeId: bridge.id })}>
          Open the bridge for this payment
        </Button>
      )}
    </div>
  );
}
