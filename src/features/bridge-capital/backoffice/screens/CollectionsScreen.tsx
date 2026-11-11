/** Collections workspace: past-due bridges, oldest first, and what ops can do about each. */
import { useState } from 'react';
import { applyDelinquency } from '../../policy/delinquency.js';
import { cardAccess } from '../../policy/cardAccess.js';
import { amountDue, lateFeeOwed } from '../../policy/repayment.js';
import { computeTier } from '../../policy/tiers.js';
import { addDays } from '../../policy/util.js';
import { color, font, radius, space, stageToneFor, timelineColors } from '../tokens.js';
import { shortDate, titleCase, usd } from '../../ui/format.js';
import { Timeline } from '../../ui/Timeline.js';
import type { BackOffice } from '../useBackOffice.js';
import { Badge, Button, Card, CardHeader, Empty, Row } from '../components/primitives.js';
import { DelinquencyPanel } from '../components/DelinquencyPanel.js';

export function CollectionsScreen({ office, customerId }: { office: BackOffice; customerId?: string }) {
  const { data, today, go, liveCustomers, act, notice } = office;
  const [selected, setSelected] = useState<string | undefined>(customerId);
  const [promise, setPromise] = useState(() => addDays(today, 7));
  if (data === null) return <Empty>Loading…</Empty>;

  const queue = data.bridges
    .filter((b) => b.status !== 'REPAID')
    .map((b) => ({ bridge: b, stage: applyDelinquency(b, today) }))
    .filter((x) => x.stage.days_past_due > 0)
    .sort((a, b) => b.stage.days_past_due - a.stage.days_past_due);

  if (queue.length === 0) return <Card><Empty>No past-due bridges. Nothing to collect.</Empty></Card>;

  const current = queue.find((q) => q.bridge.customer_id === selected) ?? queue[0];
  const { bridge, stage } = current;
  const customer = liveCustomers.find((c) => c.id === bridge.customer_id);
  const actions = data.collectionActions.filter((a) => a.bridge_id === bridge.id);
  const access = customer === undefined ? null : cardAccess(computeTier(customer), data.bridges.filter((b) => b.customer_id === bridge.customer_id), actions, today);
  const events = data.timeline.filter((e) => e.bridge_id === bridge.id).sort((a, b) => a.at.localeCompare(b.at));
  const promised = actions.filter((a) => a.type === 'PROMISE_TO_PAY').at(-1);

  return (
    <div style={{ display: 'grid', gridTemplateColumns: 'minmax(240px, 1fr) minmax(0, 2.2fr)', gap: space.xl, alignItems: 'start' }}>
      <Card>
        <CardHeader title="Queue" meta={`${queue.length} past due`} />
        <div style={{ display: 'grid', gap: space.xs }}>
          {queue.map(({ bridge: b, stage: s }) => {
            const c = liveCustomers.find((x) => x.id === b.customer_id);
            const on = b.id === bridge.id;
            return (
              <button
                key={b.id}
                type="button"
                onClick={() => setSelected(b.customer_id)}
                style={{
                  display: 'flex',
                  justifyContent: 'space-between',
                  alignItems: 'center',
                  gap: space.lg,
                  background: on ? color.surfaceHover : 'transparent',
                  border: 'none',
                  borderRadius: radius.lg,
                  padding: `${space.lg} ${space.lg}`,
                  color: color.text,
                  fontFamily: font.sans,
                  textAlign: 'left',
                  cursor: 'pointer',
                }}
              >
                <span style={{ display: 'grid', gap: '2px' }}>
                  <span style={{ fontWeight: font.weight.medium }}>{c?.name ?? b.customer_id}</span>
                  <span style={{ fontSize: font.size.sm, color: color.textMuted }}>{usd(amountDue(b, today), { cents: true })} due</span>
                </span>
                <Badge tone={stageToneFor(s.status)}>{s.days_past_due} DPD</Badge>
              </button>
            );
          })}
        </div>
      </Card>

      <div style={{ display: 'grid', gap: space.xl }}>
        {notice !== null && (
          <div role="status" style={{ background: color.positiveSubtle, color: color.positive, borderRadius: radius.xl, padding: `${space.lg} ${space['2xl']}`, fontSize: font.size.md }}>
            {notice}
          </div>
        )}
        <Card>
          <CardHeader
            title={`${customer?.name ?? bridge.customer_id} · ${bridge.id}`}
            meta={
              <span style={{ display: 'inline-flex', gap: space.sm }}>
                {promised?.promised_date !== undefined && <Badge tone="accent">Promised {shortDate(promised.promised_date)}</Badge>}
                <Badge tone={stageToneFor(stage.status)}>{titleCase(stage.status)}</Badge>
              </span>
            }
          />
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(260px, 1fr))', gap: space['2xl'] }}>
            <div>
              <Row label="Principal outstanding" value={usd(bridge.principal_outstanding, { cents: true })} />
              <Row label="Fee outstanding" value={usd(bridge.fee_outstanding, { cents: true })} />
              <Row label="Late fee (2%/month)" value={usd(lateFeeOwed(bridge, today), { cents: true })} />
              <Row label="Amount due" value={usd(amountDue(bridge, today), { cents: true })} />
              <Row label="Was due" value={shortDate(bridge.due_date)} />
              <Row label="Cards" value={access === null ? '—' : titleCase(access.state)} />
            </div>
            <DelinquencyPanel bridge={bridge} today={today} />
          </div>
        </Card>

        <Card>
          <CardHeader title="Actions" meta="Logged to the timeline" />
          <div style={{ display: 'flex', gap: space.md, flexWrap: 'wrap', alignItems: 'center' }}>
            <Button onClick={() => void act(bridge, 'REMIND')}>Send reminder</Button>
            <Button disabled={access?.state !== 'NORMAL'} onClick={() => void act(bridge, 'RESTRICT_CARDS')}>Restrict cards</Button>
            <Button variant="danger" disabled={access?.state === 'FROZEN'} onClick={() => void act(bridge, 'FREEZE_CARDS')}>Freeze cards</Button>
            <Button onClick={() => void act(bridge, 'ESCALATE')}>Escalate to collector</Button>
            {customer !== undefined && <Button onClick={() => go({ screen: 'customer', customerId: customer.id })}>Customer 360</Button>}
          </div>
          <div style={{ display: 'flex', gap: space.md, alignItems: 'center', flexWrap: 'wrap', marginTop: space.xl }}>
            <label style={{ display: 'inline-flex', alignItems: 'center', gap: space.md, fontSize: font.size.md, color: color.textSecondary }}>
              Promised to pay by
              <input
                type="date"
                value={promise}
                min={today}
                onChange={(e) => setPromise(e.target.value)}
                style={{ background: color.surfaceSubtle, border: 'none', borderRadius: radius.lg, padding: `${space.md} ${space.lg}`, color: color.text, fontFamily: font.sans, colorScheme: 'dark' }}
              />
            </label>
            <Button variant="primary" onClick={() => void act(bridge, 'PROMISE_TO_PAY', promise)}>Record promise</Button>
          </div>
        </Card>

        <Card>
          <CardHeader title="History" meta={`${events.length} events`} />
          <Timeline events={events} colors={timelineColors} />
        </Card>
      </div>
    </div>
  );
}
