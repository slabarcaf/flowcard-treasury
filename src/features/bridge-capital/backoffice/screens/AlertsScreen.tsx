/** Early warnings: customers whose numbers are slipping before they ask for a bridge (spec §8.2). */
import { computeTier, customerLimit } from '../../policy/tiers.js';
import { V1_1_PLACEHOLDERS } from '../../policy/policy.config.js';
import { TIER_TONE, color, font, space } from '../tokens.js';
import { usd } from '../../ui/format.js';
import type { BackOffice } from '../useBackOffice.js';
import { customersWithWarnings } from '../warnings.js';
import { Badge, Button, Card, CardHeader, Empty, Row } from '../components/primitives.js';

export function AlertsScreen({ office }: { office: BackOffice }) {
  const { data, go } = office;
  if (data === null) return <Empty>Loading…</Empty>;
  const rows = customersWithWarnings(office);
  const triggers = V1_1_PLACEHOLDERS.filter((p) => p.source === '§8.2');

  return (
    <div style={{ display: 'grid', gap: space.xl }}>
      <Card>
        <CardHeader title="Triggers" meta={<Badge tone="warning">Placeholder · spec §8.2</Badge>} />
        {triggers.map((t) => (
          <Row key={t.label} label={t.label.replace('Early warning: ', '')} value={t.value} />
        ))}
      </Card>
      {rows.length === 0 && <Card><Empty>No early warnings today.</Empty></Card>}
      {rows.map(({ customer, warnings }) => {
        const tier = computeTier(customer);
        return (
          <Card key={customer.id}>
            <CardHeader
              title={customer.name}
              meta={
                <span style={{ display: 'inline-flex', gap: space.md, alignItems: 'center' }}>
                  <Badge tone={TIER_TONE[tier]}>{tier === 'NONE' ? 'No tier' : `Tier ${tier}`}</Badge>
                  <span>limit {usd(customerLimit(customer, tier))}</span>
                  <Button onClick={() => go({ screen: 'customer', customerId: customer.id })}>Open profile</Button>
                </span>
              }
            />
            <div style={{ display: 'grid', gap: space.md }}>
              {warnings.map((w) => (
                <div key={w.id} style={{ display: 'flex', gap: space.lg, alignItems: 'center', fontSize: font.size.md, color: color.text }}>
                  <Badge tone={w.severity === 'HIGH' ? 'critical' : 'warning'}>{w.severity === 'HIGH' ? 'High' : 'Watch'}</Badge>
                  {w.text}
                </div>
              ))}
            </div>
          </Card>
        );
      })}
    </div>
  );
}
