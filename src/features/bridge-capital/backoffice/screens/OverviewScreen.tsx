/** Capital, the KPIs, and the request queue ranked by rankRequests. */
import type { Bridge } from '../../types.js';
import { applyDelinquency } from '../../policy/delinquency.js';
import { rankRequests } from '../../policy/ranking.js';
import { CRITICALITY_TONE, color, font, space, stageToneFor, TIER_TONE } from '../tokens.js';
import { usd, titleCase } from '../format.js';
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
  const { data, today, go, outcomes } = office;
  if (data === null) return <Empty>Loading…</Empty>;

  const { capital, customers, requests, bridges } = data;
  const ranked = rankRequests(requests, customers, capital);
  const feesEarned = bridges.reduce((sum, b) => sum + (b.fee - b.fee_outstanding), 0);
  const stages = stageCounts(bridges, today);

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
                        <div style={{ display: 'grid', gap: space.xs, maxWidth: '280px' }}>
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
