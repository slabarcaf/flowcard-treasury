/** Home: balance, what is pre-approved, the next 60 days, and anything that needs the customer. */
import { applyDelinquency } from '../../policy/delinquency.js';
import { forecastBalance } from '../../policy/forecast.js';
import { preApproved } from '../../policy/preApproved.js';
import { amountDue } from '../../policy/repayment.js';
import { color, font, space } from '../tokens.js';
import { usd, shortDate } from '../../ui/format.js';
import { ForecastChart } from '../../ui/charts.js';
import { openBridges, type Portal } from '../usePortal.js';
import { restrictionSentence } from '../copy.js';
import { Pill, ListRow } from '../components/ui.js';
import { Banner, Kpi, PageHeader, Panel, SmallButton } from '../components/layout.js';
import { linkStyle } from '../components/styles.js';

export function HomeScreen({ portal }: { portal: Portal }) {
  const { data, customer, today, go, wallet, myBridges, access } = portal;
  if (data === null || customer === null) return null;

  const open = openBridges(myBridges);
  const overdue = open.filter((b) => applyDelinquency(b, today).days_past_due > 0);
  const running = open.filter((b) => applyDelinquency(b, today).days_past_due === 0);
  const bridgedTxns = new Set(myBridges.map((b) => b.transaction_id));
  const declines = data.requests.filter(
    (r) => r.customer_id === customer.id && (r.purpose ?? 'CARD_DECLINE') === 'CARD_DECLINE' && !bridgedTxns.has(r.transaction.id),
  );
  const flows = data.cashFlows[customer.id] ?? [];
  const lastFull = flows.at(-2);
  const pa = preApproved(customer, data.capital, today);
  const forecast = forecastBalance(customer, wallet, data.transactions, today);
  const restriction = restrictionSentence(access);
  const recent = data.transactions
    .filter((t) => t.customer_id === customer.id && t.status !== 'DECLINED')
    .sort((a, b) => b.created_at.localeCompare(a.created_at))
    .slice(0, 5);

  return (
    <>
      <PageHeader title={customer.name} sub={`Today is ${shortDate(today)}`} />

      {overdue.map((b) => {
        const days = applyDelinquency(b, today).days_past_due;
        return (
          <Banner
            key={b.id}
            tone="critical"
            title={`Bridge overdue by ${days} days · ${usd(amountDue(b, today), { cents: true })} due`}
            action={<SmallButton variant="primary" onClick={() => go({ screen: 'bridge', bridgeId: b.id })}>Repay</SmallButton>}
          >
            {restriction ?? undefined}
          </Banner>
        );
      })}

      {declines.map((r) => (
        <Banner
          key={r.id}
          tone="warning"
          title={`${usd(r.transaction.amount)} to ${r.transaction.merchant_name} didn’t go through`}
          action={<SmallButton onClick={() => go({ screen: 'decline', transactionId: r.transaction.id })}>See options</SmallButton>}
        >
          Your wallet was {usd(r.principal)} short. We may be able to cover it.
        </Banner>
      ))}

      <div className="bcp-g3">
        <Panel className="bcp-span2">
          <div style={{ display: 'grid', gap: space.xl }}>
            <div style={{ display: 'grid', gap: space.xs }}>
              <span style={{ fontSize: font.size.md, color: color.textSecondary }}>Wallet balance · USD</span>
              <span style={{ fontSize: font.size['4xl'], fontWeight: font.weight.semibold, letterSpacing: '-0.03em', fontVariantNumeric: 'tabular-nums' }}>
                {usd(wallet, { cents: true })}
              </span>
            </div>
            {lastFull !== undefined && (
              <div className="bcp-g2">
                <Kpi label="In, last month" value={usd(lastFull.inflow)} valueColor={color.positive} />
                <Kpi label="Out, last month" value={usd(lastFull.outflow)} />
              </div>
            )}
          </div>
        </Panel>
        <Panel>
          {pa.amount > 0 ? (
            <div style={{ display: 'grid', gap: space.lg }}>
              <Kpi
                label="You can bridge up to"
                value={usd(pa.amount)}
                sub={`Tier ${pa.tier} · ${usd(pa.outstanding)} in use of ${usd(pa.limit)}`}
              />
              <div>
                <SmallButton variant="primary" onClick={() => go({ screen: 'bridges' })}>Plan a bridge</SmallButton>
              </div>
            </div>
          ) : (
            <Kpi label="Bridge available" value="Not right now" valueColor={color.textMuted} sub={pa.reason ?? undefined} />
          )}
        </Panel>
      </div>

      <Panel
        title="Next 60 days"
        meta={
          forecast.crossing === null ? (
            <Pill tone="positive">Stays above your low line</Pill>
          ) : (
            <Pill tone="critical">Low on {shortDate(forecast.crossing.date)}</Pill>
          )
        }
      >
        <ForecastChart forecast={forecast} height={180} />
        <p style={{ margin: `${space.lg} 0 0`, fontSize: font.size.sm, color: color.textMuted }}>
          Projected from today’s balance, your expected invoices on the date each customer usually pays, and your average daily spend of{' '}
          {usd(forecast.daily_spend)}.
        </p>
        {forecast.crossing !== null && pa.amount > 0 && (
          <div style={{ marginTop: space.lg }}>
            <SmallButton onClick={() => go({ screen: 'bridges' })}>Plan a bridge before {shortDate(forecast.crossing.date)}</SmallButton>
          </div>
        )}
      </Panel>

      <div className="bcp-g2">
        <Panel title="Recent card spend" meta={<button type="button" onClick={() => go({ screen: 'spend' })} style={linkStyle}>See all</button>}>
          {recent.map((t, i) => (
            <ListRow
              key={t.id}
              first={i === 0}
              title={t.merchant_name}
              subtitle={shortDate(t.created_at)}
              amount={`−${usd(t.amount)}`}
              onClick={() => go({ screen: 'transaction', transactionId: t.id })}
            />
          ))}
        </Panel>
        <Panel title="Bridges" meta={<button type="button" onClick={() => go({ screen: 'bridges' })} style={linkStyle}>Open</button>}>
          {running.length === 0 ? (
            <span style={{ fontSize: font.size.md, color: color.textMuted }}>
              {overdue.length > 0 ? 'See the overdue bridge above.' : 'No bridge running.'}
            </span>
          ) : (
            running.map((b, i) => (
              <ListRow
                key={b.id}
                first={i === 0}
                title={`Bridge ${usd(b.principal)}`}
                subtitle={`Repaying from your deposits · due ${shortDate(b.due_date)}`}
                amount={`${usd(amountDue(b, today))} left`}
                onClick={() => go({ screen: 'bridge', bridgeId: b.id })}
              />
            ))
          )}
        </Panel>
      </div>
    </>
  );
}
