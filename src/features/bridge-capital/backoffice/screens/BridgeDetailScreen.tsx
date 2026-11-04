/** One bridge: its whole life, and what the ladder requires if it is late. */
import { color, font, space, stageToneFor } from '../tokens.js';
import { usd, shortDate, titleCase } from '../format.js';
import type { BackOffice } from '../useBackOffice.js';
import {
  Badge,
  Breadcrumb,
  Card,
  CardHeader,
  Empty,
  Row,
  Stat,
} from '../components/primitives.js';
import { Timeline } from '../components/Timeline.js';
import { DelinquencyPanel } from '../components/DelinquencyPanel.js';

export function BridgeDetailScreen({ office, bridgeId }: { office: BackOffice; bridgeId: string }) {
  const { data, today, go } = office;
  if (data === null) return <Empty>Loading…</Empty>;

  const bridge = data.bridges.find((b) => b.id === bridgeId);
  const customer = bridge === undefined ? undefined : data.customers.find((c) => c.id === bridge.customer_id);
  if (bridge === undefined || customer === undefined) return <Empty>Bridge not found</Empty>;

  const events = data.timeline
    .filter((e) => e.bridge_id === bridge.id)
    .sort((a, b) => a.at.localeCompare(b.at));

  return (
    <div style={{ display: 'grid', gap: space.xl }}>
      <div style={{ display: 'grid', gap: space.lg }}>
        <Breadcrumb
          items={[
            { label: 'Overview', onClick: () => go({ screen: 'overview' }) },
            { label: 'Customers', onClick: () => go({ screen: 'customers' }) },
            { label: customer.name },
          ]}
        />
        <div style={{ display: 'flex', alignItems: 'center', gap: space.lg, flexWrap: 'wrap' }}>
          <h1
            style={{
              margin: 0,
              fontSize: font.size['2xl'],
              fontWeight: font.weight.semibold,
              letterSpacing: '-0.02em',
              color: color.text,
            }}
          >
            {customer.name}
          </h1>
          <span style={{ fontFamily: font.mono, fontSize: font.size.sm, color: color.textMuted }}>
            {bridge.id}
          </span>
          <Badge tone={stageToneFor(bridge.status)}>{titleCase(bridge.status)}</Badge>
        </div>
      </div>

      <Card>
        <div style={{ display: 'flex', gap: space['4xl'], flexWrap: 'wrap' }}>
          <Stat label="Principal" value={usd(bridge.principal)} />
          <Stat label="Fee" value={usd(bridge.fee, { cents: true })} sub={`${bridge.term_days}-day term`} />
          <Stat label="Outstanding" value={usd(bridge.principal_outstanding + bridge.fee_outstanding, { cents: true })} />
          <Stat label="Due" value={shortDate(bridge.due_date)} />
        </div>
      </Card>

      <div style={{ display: 'grid', gridTemplateColumns: 'minmax(0, 1.25fr) minmax(0, 1fr)', gap: space.xl, alignItems: 'start' }}>
        <Card>
          <CardHeader title="Timeline" meta={`${events.length} events`} />
          <Timeline events={events} />
        </Card>

        <div style={{ display: 'grid', gap: space.xl }}>
          <Card>
            <CardHeader title="Collection status" />
            <DelinquencyPanel bridge={bridge} today={today} />
          </Card>

          <Card>
            <CardHeader title="Terms" />
            <div>
              <Row label="Disbursed" value={bridge.disbursed_at === null ? '—' : shortDate(bridge.disbursed_at)} />
              <Row label="Term" value={`${bridge.term_days} days`} />
              <Row label="Due date" value={shortDate(bridge.due_date)} />
              <Row label="Principal outstanding" value={usd(bridge.principal_outstanding)} />
              <Row label="Fee outstanding" value={usd(bridge.fee_outstanding, { cents: true })} />
              <Row
                label="Transaction"
                value={<span style={{ fontFamily: font.mono, fontSize: font.size.sm }}>{bridge.transaction_id}</span>}
              />
            </div>
          </Card>
        </div>
      </div>
    </div>
  );
}
