/** Capital, the KPIs, and the request queue ranked by rankRequests. */
import type { Bridge } from '../../types.js';
import { applyDelinquency } from '../../policy/delinquency.js';
import { rankRequests } from '../../policy/ranking.js';
import { CRITICALITY_TONE, color, font, space, stageToneFor, TIER_TONE } from '../tokens.js';
import { usd, titleCase } from '../../ui/format.js';
import type { BackOffice } from '../useBackOffice.js';
import {
  Badge,
  Card,
  CardHeader,
  ClickableTr,
  Empty,
  Label,
  Stat,
  Table,
  Td,
  Th,
} from '../components/primitives.js';
import { CapitalGauge } from '../components/CapitalGauge.js';
import { portfolio } from '../../policy/portfolio.js';
import { HBars } from '../../ui/charts.js';
import { Button } from '../components/primitives.js';
import { customersWithWarnings } from '../warnings.js';

/** Stage of every bridge on the books, open ones run through the ladder. */
function stageCounts(bridges: Bridge[], today: string): Array<{ stage: string; count: number }> {
  const counts = new Map<string, number>();
  for (const b of bridges) {
    const stage = b.status === 'REPAID' ? 'REPAID' : applyDelinquency(b, today).status;
    counts.set(stage, (counts.get(stage) ?? 0) + 1);
  }
  const order = ['COLLECTING', 'DISBURSED', 'WATCH', 'DELINQUENT', 'SERIOUS', 'DEFAULTED', 'REPAID'];
  return [...counts.entries()]
    .sort((a, b) => order.indexOf(a[0]) - order.indexOf(b[0]))
    .map(([stage, count]) => ({ stage, count }));
}

export function OverviewScreen({ office }: { office: BackOffice }) {
  const { data, today, go, outcomes, liveCustomers } = office;
  if (data === null) return <Empty>Loading…</Empty>;

  const { capital, customers, requests, bridges } = data;
  const ranked = rankRequests(requests, customers, capital);
  const feesEarned = bridges.reduce((sum, b) => sum + (b.fee - b.fee_outstanding), 0);
  const stages = stageCounts(bridges, today);
  const book = portfolio(liveCustomers, bridges, today);
  const warned = customersWithWarnings(office);
  const bucketColor = [color.positive, color.warning, color.critical, color.critical, color.critical];

  return (
    <div style={{ display: 'grid', gap: space.xl }}>
      <div style={{ display: 'grid', gridTemplateColumns: 'minmax(0, 2fr) minmax(0, 1fr)', gap: space.xl }}>
        <Card>
          <CapitalGauge capital={capital} />
        </Card>

        <Card>
          <div style={{ display: 'grid', gap: space['2xl'] }}>
            <Stat label="Outstanding" value={usd(capital.outstanding)} sub="Principal lent, facility-wide" />
            <Stat
              label="Fees earned"
              value={usd(feesEarned, { cents: true })}
              sub="Collected on tracked bridges"
              tone="positive"
            />
            <div style={{ display: 'grid', gap: space.md }}>
              <Label>Bridges by stage</Label>
              <div style={{ display: 'flex', gap: space.sm, flexWrap: 'wrap' }}>
                {stages.length === 0 ? (
                  <span style={{ fontSize: font.size.sm, color: color.textMuted }}>None</span>
                ) : (
                  stages.map(({ stage, count }) => (
                    <Badge key={stage} tone={stageToneFor(stage)}>
                      {titleCase(stage)} · {count}
                    </Badge>
                  ))
                )}
              </div>
            </div>
          </div>
        </Card>
      </div>

      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(300px, 1fr))', gap: space.xl }}>
        <Card>
          <CardHeader title="Outstanding by tier" />
          <HBars items={book.outstanding_by_tier.map((t) => ({ label: t.tier === 'NONE' ? 'No tier' : `Tier ${t.tier}`, value: t.amount }))} />
        </Card>
        <Card>
          <CardHeader title="Days past due" meta="open bridges" />
          <HBars
            items={book.dpd_buckets.map((b, i) => ({ label: b.label, meta: String(b.count), value: b.amount, color: bucketColor[i] }))}
          />
        </Card>
        <Card>
          <CardHeader title="Book quality" meta="all bridges on record" />
          <div style={{ display: 'grid', gap: space.xl }}>
            <Stat label="Loss rate" value={`${(book.loss_rate * 100).toFixed(1)}%`} sub={`${usd(book.defaulted)} defaulted of ${usd(book.originated)} lent`} />
            <Stat label="Repaid on time" value={`${Math.round(book.on_time_rate * 100)}%`} sub="of closed bridges" />
            <Stat label="Fees earned" value={usd(book.fees_earned)} sub="closed bridges plus collected on open ones" tone="positive" />
          </div>
        </Card>
      </div>

      <Card>
        <CardHeader title="Early warnings" meta={<Button onClick={() => go({ screen: 'alerts' })}>See all</Button>} />
        {warned.length === 0 ? (
          <span style={{ fontSize: font.size.md, color: color.textMuted }}>No signals today.</span>
        ) : (
          <div style={{ display: 'grid', gap: space.md }}>
            {warned.map(({ customer, warnings }) => (
              <button
                key={customer.id}
                type="button"
                onClick={() => go({ screen: 'customer', customerId: customer.id })}
                style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: space.lg, background: color.surfaceSubtle, border: 'none', borderRadius: '12px', padding: `${space.lg} ${space.xl}`, color: color.text, fontFamily: font.sans, textAlign: 'left', cursor: 'pointer' }}
              >
                <span style={{ display: 'grid', gap: '2px' }}>
                  <span style={{ fontWeight: font.weight.medium }}>{customer.name}</span>
                  <span style={{ fontSize: font.size.sm, color: color.textSecondary }}>
                    {warnings[0].text}{warnings.length > 1 ? ` · +${warnings.length - 1} more` : ''}
                  </span>
                </span>
                <Badge tone={warnings.some((w) => w.severity === 'HIGH') ? 'critical' : 'warning'}>{warnings.length} signals</Badge>
              </button>
            ))}
          </div>
        )}
      </Card>

      <Card padded={false}>
        <div style={{ padding: `${space['2xl']} ${space['2xl']} 0` }}>
          <CardHeader
            title="Request queue"
            meta={`${ranked.length} declines · ranked by tier, criticality, then receivable quality`}
          />
        </div>

        {ranked.length === 0 ? (
          <Empty>No declines waiting</Empty>
        ) : (
          <Table>
            <thead>
              <tr>
                <Th>#</Th>
                <Th>Customer</Th>
                <Th>Tier</Th>
                <Th>Declined payment</Th>
                <Th>Spend</Th>
                <Th align="right">Shortfall</Th>
                <Th align="right">Headroom</Th>
                <Th>Outcome</Th>
              </tr>
            </thead>
            <tbody>
              {ranked.map((row) => {
                const customer = customers.find((c) => c.id === row.request.customer_id);
                const outcome = outcomes[row.request.id];
                return (
                  <ClickableTr
                    key={row.request.id}
                    onClick={() => go({ screen: 'request', requestId: row.request.id })}
                  >
                    <Td muted mono>{row.rank}</Td>
                    <Td>
                      <span style={{ fontWeight: font.weight.medium }}>{customer?.name ?? row.request.customer_id}</span>
                    </Td>
                    <Td>
                      <Badge tone={TIER_TONE[row.decision.tier]}>
                        {row.decision.tier === 'NONE' ? 'No tier' : `Tier ${row.decision.tier}`}
                      </Badge>
                    </Td>
                    <Td>
                      <div style={{ display: 'grid' }}>
                        <span>{row.request.transaction.merchant_name}</span>
                        <span style={{ fontSize: font.size.sm, color: color.textMuted }}>
                          {usd(row.request.transaction.amount)} · wallet {usd(row.request.transaction.wallet_balance)}
                        </span>
                      </div>
                    </Td>
                    <Td>
                      <Badge tone={CRITICALITY_TONE[row.request.transaction.criticality]}>
                        {titleCase(row.request.transaction.criticality)}
                      </Badge>
                    </Td>
                    <Td align="right">{usd(row.request.principal)}</Td>
                    <Td align="right" muted>{usd(row.decision.max_bridgeable)}</Td>
                    <Td>
                      {outcome !== undefined ? (
                        <Badge tone={outcome === 'APPROVED' ? 'positive' : outcome === 'DECLINED' ? 'critical' : 'warning'}>
                          {titleCase(outcome)}
                        </Badge>
                      ) : row.funded ? (
                        <Badge tone="positive">Auto-approve</Badge>
                      ) : row.decision.eligible ? (
                        <Badge tone="warning">Needs approval</Badge>
                      ) : (
                        <div style={{ display: 'grid', gap: space.xs, maxWidth: '220px' }}>
                          <Badge tone="critical">Declined</Badge>
                          <span style={{ fontSize: font.size.sm, color: color.textMuted, lineHeight: 1.4 }}>
                            {row.reason}
                          </span>
                        </div>
                      )}
                    </Td>
                  </ClickableTr>
                );
              })}
            </tbody>
          </Table>
        )}
      </Card>
    </div>
  );
}
