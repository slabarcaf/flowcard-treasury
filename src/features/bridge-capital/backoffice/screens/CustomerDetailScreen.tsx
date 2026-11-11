/** Customer 360: everything ops needs about one customer on one page. */
import { applyDelinquency } from '../../policy/delinquency.js';
import { cardAccess } from '../../policy/cardAccess.js';
import { earlyWarnings } from '../../policy/earlyWarning.js';
import { forecastBalance } from '../../policy/forecast.js';
import { priceOffer } from '../../policy/pricing.js';
import { amountDue } from '../../policy/repayment.js';
import { computeTier, customerLimit, outstandingBalance } from '../../policy/tiers.js';
import { TIER_LIMIT_PCT } from '../../policy/policy.config.js';
import { TIER_TONE, color, font, space, stageToneFor, timelineColors } from '../tokens.js';
import { pct, shortDate, titleCase, usd } from '../../ui/format.js';
import { CashFlowChart, ForecastChart, Legend } from '../../ui/charts.js';
import { chartColor } from '../../ui/chartColor.js';
import { Timeline } from '../../ui/Timeline.js';
import type { BackOffice } from '../useBackOffice.js';
import { Badge, Breadcrumb, Button, Card, CardHeader, ClickableTr, Empty, Row, Stat, Table, Td, Th } from '../components/primitives.js';
import { BalanceChart } from '../components/BalanceChart.js';
import { DelinquencyPanel } from '../components/DelinquencyPanel.js';

const ACCESS_LABEL = { NORMAL: 'Cards normal', RESTRICTED: 'Cards restricted', FROZEN: 'Cards frozen' } as const;

export function CustomerDetailScreen({ office, customerId }: { office: BackOffice; customerId: string }) {
  const { data, today, go, liveCustomers } = office;
  if (data === null) return <Empty>Loading…</Empty>;
  const customer = liveCustomers.find((c) => c.id === customerId);
  if (customer === undefined) return <Empty>Customer not found</Empty>;

  const tier = computeTier(customer);
  const bridges = data.bridges.filter((b) => b.customer_id === customer.id);
  const open = bridges.filter((b) => b.status !== 'REPAID');
  const pastDue = open.find((b) => applyDelinquency(b, today).days_past_due > 0);
  const wallet = data.wallets[customer.id] ?? 0;
  const forecast = forecastBalance(customer, wallet, data.transactions, today);
  const warnings = earlyWarnings(customer, forecast, today);
  const access = cardAccess(tier, bridges, data.collectionActions.filter((a) => a.customer_id === customer.id), today);
  const events = data.timeline.filter((e) => e.customer_id === customer.id).sort((a, b) => a.at.localeCompare(b.at));
  const history = customer.bridge_history.slice().sort((a, b) => b.opened_at.localeCompare(a.opened_at));
  const s = customer.balance_stats;

  return (
    <div style={{ display: 'grid', gap: space.xl }}>
      <Breadcrumb items={[{ label: 'Customers', onClick: () => go({ screen: 'customers' }) }, { label: customer.name }]} />
      <div style={{ display: 'flex', alignItems: 'center', gap: space.lg, flexWrap: 'wrap' }}>
        <h1 style={{ margin: 0, fontSize: font.size['2xl'], fontWeight: font.weight.semibold, letterSpacing: '-0.02em' }}>{customer.name}</h1>
        <Badge tone={TIER_TONE[tier]}>{tier === 'NONE' ? 'No tier' : `Tier ${tier}`}</Badge>
        {pastDue !== undefined && (
          <Badge tone={stageToneFor(applyDelinquency(pastDue, today).status)}>
            {titleCase(applyDelinquency(pastDue, today).status)} · {applyDelinquency(pastDue, today).days_past_due} DPD
          </Badge>
        )}
        <Badge tone={access.state === 'NORMAL' ? 'neutral' : access.state === 'FROZEN' ? 'critical' : 'warning'}>{ACCESS_LABEL[access.state]}</Badge>
      </div>

      {warnings.length > 0 && (
        <Card style={{ background: color.warningSubtle }}>
          <CardHeader title={`${warnings.length} early-warning signal${warnings.length > 1 ? 's' : ''}`} meta="§8.2 placeholder thresholds" />
          <div style={{ display: 'grid', gap: space.sm }}>
            {warnings.map((w) => (
              <span key={w.id} style={{ fontSize: font.size.md, color: color.text }}>
                <Badge tone={w.severity === 'HIGH' ? 'critical' : 'warning'}>{w.severity === 'HIGH' ? 'High' : 'Watch'}</Badge> {w.text}
              </span>
            ))}
          </div>
        </Card>
      )}

      <Card>
        <div style={{ display: 'flex', gap: space['4xl'], flexWrap: 'wrap' }}>
          <Stat label="Limit" value={usd(customerLimit(customer, tier))} sub={tier === 'NONE' ? 'Not eligible' : `${pct(TIER_LIMIT_PCT[tier], 0)} of monthly inflows`} />
          <Stat label="Outstanding" value={usd(outstandingBalance(customer))} />
          <Stat label="Wallet" value={usd(wallet)} />
          <Stat label="Monthly inflows" value={usd(customer.inflow_stats.monthly_inflows)} sub={`Top payer ${pct(customer.inflow_stats.top_payer_concentration, 0)}`} />
          <Stat label="Bridges" value={String(customer.bridge_history.length)} sub={`${customer.bridge_stats.on_time_count} on time · ${customer.bridge_stats.late_count} late`} />
        </div>
      </Card>

      {pastDue !== undefined && (
        <Card>
          <CardHeader
            title={`Delinquency · ${pastDue.id}`}
            meta={<Button variant="secondary" onClick={() => go({ screen: 'collections', customerId: customer.id })}>Open in collections</Button>}
          />
          <DelinquencyPanel bridge={pastDue} today={today} />
        </Card>
      )}

      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(380px, 1fr))', gap: space.xl }}>
        <Card>
          <CardHeader
            title="Cash flow"
            meta={<Legend items={[{ label: 'In', color: chartColor.inflow }, { label: 'Out', color: chartColor.outflow }, { label: 'Net', color: chartColor.net }]} />}
          />
          <CashFlowChart flows={data.cashFlows[customer.id] ?? []} height={220} />
        </Card>
        <Card>
          <CardHeader
            title="Forecast, 60 days"
            meta={forecast.crossing === null ? <Badge tone="positive">Clear</Badge> : <Badge tone="critical">Low on {shortDate(forecast.crossing.date)}</Badge>}
          />
          <ForecastChart forecast={forecast} height={220} />
        </Card>
      </div>

      <Card padded={false}>
        <div style={{ padding: `${space['2xl']} ${space['2xl']} 0` }}>
          <CardHeader title="All bridges" meta={`${history.length} on record`} />
        </div>
        <Table>
          <thead>
            <tr>
              <Th>Opened</Th>
              <Th align="right">Amount</Th>
              <Th align="right">Term</Th>
              <Th align="right">Fee</Th>
              <Th>Status</Th>
              <Th align="right">Owed</Th>
            </tr>
          </thead>
          <tbody>
            {history.map((h) => {
              const live = bridges.find((b) => b.transaction_id === h.transaction_id || b.id === h.id);
              const status = live !== undefined && live.status !== 'REPAID' ? applyDelinquency(live, today).status : h.outcome;
              return (
                <ClickableTr key={h.id} onClick={live === undefined ? undefined : () => go({ screen: 'bridge', bridgeId: live.id })}>
                  <Td>{shortDate(h.opened_at)}</Td>
                  <Td align="right">{usd(h.amount)}</Td>
                  <Td align="right">{h.term_days}d</Td>
                  <Td align="right">{usd(priceOffer(h.amount, h.term_days).fee, { cents: true })}</Td>
                  <Td>
                    <Badge tone={stageToneFor(status === 'ON_TIME' ? 'REPAID' : status === 'LATE' ? 'WATCH' : status)}>{titleCase(status)}</Badge>
                  </Td>
                  <Td align="right">{live !== undefined && live.status !== 'REPAID' ? usd(amountDue(live, today), { cents: true }) : '—'}</Td>
                </ClickableTr>
              );
            })}
          </tbody>
        </Table>
      </Card>

      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(320px, 1fr))', gap: space.xl, alignItems: 'start' }}>
        <Card>
          <CardHeader title="Balance, 180 days" />
          <BalanceChart history={customer.balance_history} stats={s} />
          <div style={{ marginTop: space.xl }}>
            <Row label="Average 30 / 90 / 180 days" value={`${usd(s.avg_balance_30d)} / ${usd(s.avg_balance_90d)} / ${usd(s.avg_balance_180d)}`} />
            <Row label="Low point, 90 days (p10)" value={usd(s.low_point_balance_90d)} />
            <Row label="Days near zero, 90 days" value={String(s.days_near_zero_90d)} />
            <Row label="Tenure" value={`${customer.tenure_months} months`} />
          </div>
        </Card>
        <Card>
          <CardHeader title="Collections and bridge history" meta={`${events.length} events`} />
          <Timeline events={events} colors={timelineColors} emptyLabel="Nothing on record yet" />
        </Card>
        <Card>
          <CardHeader title="CRM" meta={customer.crm.contract_status} />
          <Row label="Onboarded" value={shortDate(customer.crm.onboarding_date)} />
          <div style={{ display: 'grid', gap: space.lg, marginTop: space.xl }}>
            {customer.crm.support_notes.map((n) => (
              <div key={n.timestamp + n.author} style={{ display: 'grid', gap: space.xs }}>
                <span style={{ fontSize: font.size.sm, color: color.textMuted }}>{shortDate(n.timestamp)} · {n.author}</span>
                <span style={{ fontSize: font.size.md, color: color.text, lineHeight: 1.5 }}>{n.text}</span>
              </div>
            ))}
          </div>
          <p style={{ margin: `${space.xl} 0 0`, fontSize: font.size.sm, color: color.textMuted }}>Context only. Not used in scoring (spec §2).</p>
        </Card>
      </div>
    </div>
  );
}
