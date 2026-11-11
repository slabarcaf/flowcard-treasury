/** Every customer, with tier, limit, outstanding and worst delinquency stage. */
import type { Bridge } from '../../types.js';
import { applyDelinquency } from '../../policy/delinquency.js';
import { computeTier, customerLimit, outstandingBalance } from '../../policy/tiers.js';
import { TIER_TONE, color, font, space, stageToneFor } from '../tokens.js';
import { usd, titleCase } from '../../ui/format.js';
import type { BackOffice } from '../useBackOffice.js';
import {
  Badge,
  Card,
  CardHeader,
  ClickableTr,
  Empty,
  Table,
  Td,
  Th,
} from '../components/primitives.js';

const STAGE_SEVERITY = ['REPAID', 'DISBURSED', 'COLLECTING', 'WATCH', 'DELINQUENT', 'SERIOUS', 'DEFAULTED'];

/** The worst stage across a customer's bridges, or null when they have none. */
function worstStage(bridges: Bridge[], today: string): string | null {
  if (bridges.length === 0) return null;
  return bridges
    .map((b) => (b.status === 'REPAID' ? 'REPAID' : applyDelinquency(b, today).status))
    .reduce((worst, stage) =>
      STAGE_SEVERITY.indexOf(stage) > STAGE_SEVERITY.indexOf(worst) ? stage : worst,
    );
}

export function CustomersScreen({ office }: { office: BackOffice }) {
  const { data, today, go, bridgesFor } = office;
  if (data === null) return <Empty>Loading…</Empty>;

  return (
    <Card padded={false}>
      <div style={{ padding: `${space['2xl']} ${space['2xl']} 0` }}>
        <CardHeader title="Customers" meta={`${data.customers.length} on the platform`} />
      </div>
      <Table>
        <thead>
          <tr>
            <Th>Customer</Th>
            <Th>Tier</Th>
            <Th align="right">Monthly inflows</Th>
            <Th align="right">Limit</Th>
            <Th align="right">Outstanding</Th>
            <Th>Stage</Th>
          </tr>
        </thead>
        <tbody>
          {data.customers.map((customer) => {
            const tier = computeTier(customer);
            const bridges = bridgesFor(customer.id);
            const stage = worstStage(bridges, today);
            return (
              <ClickableTr
                key={customer.id}
                onClick={() => go({ screen: 'customer', customerId: customer.id })}
              >
                <Td>
                  <div style={{ display: 'grid' }}>
                    <span style={{ fontWeight: font.weight.medium }}>{customer.name}</span>
                    <span style={{ fontSize: font.size.sm, color: color.textMuted }}>
                      {customer.tenure_months} months · {customer.balance_history.length} days of balances
                    </span>
                  </div>
                </Td>
                <Td>
                  <Badge tone={TIER_TONE[tier]}>{tier === 'NONE' ? 'No tier' : `Tier ${tier}`}</Badge>
                </Td>
                <Td align="right">{usd(customer.inflow_stats.monthly_inflows)}</Td>
                <Td align="right">{usd(customerLimit(customer, tier))}</Td>
                <Td align="right">{usd(outstandingBalance(customer))}</Td>
                <Td>
                  {stage === null ? (
                    <span style={{ color: color.textMuted }}>—</span>
                  ) : (
                    <Badge tone={stageToneFor(stage)}>{titleCase(stage)}</Badge>
                  )}
                </Td>
              </ClickableTr>
            );
          })}
        </tbody>
      </Table>
    </Card>
  );
}
