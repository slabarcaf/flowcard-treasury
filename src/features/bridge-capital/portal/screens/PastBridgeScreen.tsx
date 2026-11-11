/** One closed bridge: what it covered, what it cost, and how it ended. */
import { priceOffer } from '../../policy/pricing.js';
import { usd, shortDate } from '../../ui/format.js';
import type { Portal } from '../usePortal.js';
import { OUTCOME_COPY } from '../copy.js';
import { BackLink, Pill, Row } from '../components/ui.js';
import { PageHeader, Panel } from '../components/layout.js';

export function PastBridgeScreen({ portal, pastBridgeId }: { portal: Portal; pastBridgeId: string }) {
  const { data, customer, go } = portal;
  if (data === null || customer === null) return null;
  const h = customer.bridge_history.find((b) => b.id === pastBridgeId);
  if (h === undefined) return null;

  const fee = priceOffer(h.amount, h.term_days).fee;
  const request = data.requests.find((r) => r.transaction.id === h.transaction_id);
  const outcome = OUTCOME_COPY[h.outcome];
  const late = h.closed_at !== null ? Math.max(0, h.days_outstanding - h.term_days) : 0;

  return (
    <>
      <BackLink onClick={() => go({ screen: 'bridges' })}>Bridges</BackLink>
      <PageHeader title={`Bridge ${usd(h.amount)}`} sub={request === undefined ? undefined : `Covered ${request.transaction.merchant_name}`} action={<Pill tone={outcome.tone}>{outcome.label}</Pill>} />
      <div className="bcp-g2">
        <Panel title="What it cost">
          <Row label="Amount" value={usd(h.amount)} />
          <Row label="Fee" value={usd(fee, { cents: true })} />
          <Row label="Total repaid" value={h.outcome === 'DEFAULTED' ? '—' : usd(h.amount + fee, { cents: true })} strong />
        </Panel>
        <Panel title="When">
          <Row label="Term" value={`${h.term_days} days`} />
          <Row label="Started" value={shortDate(h.opened_at)} />
          <Row label={h.outcome === 'DEFAULTED' ? 'Closed' : 'Repaid'} value={h.closed_at === null ? '—' : shortDate(h.closed_at)} />
          <Row label="Days outstanding" value={`${h.days_outstanding} days${late > 0 && h.outcome === 'LATE' ? ` · ${late} late` : ''}`} />
        </Panel>
      </div>
    </>
  );
}
