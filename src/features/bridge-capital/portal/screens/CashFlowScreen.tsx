/** Money in and out of the wallet by month, and the 60-day forecast with its inputs. */
import { forecastBalance } from '../../policy/forecast.js';
import { color, font, space } from '../tokens.js';
import { monthLabel, shortDate, usd } from '../../ui/format.js';
import { CashFlowChart, ForecastChart, Legend } from '../../ui/charts.js';
import { chartColor } from '../../ui/chartColor.js';
import type { Portal } from '../usePortal.js';
import { Pill } from '../components/ui.js';
import { Kpi, PageHeader, Panel } from '../components/layout.js';

export function CashFlowScreen({ portal }: { portal: Portal }) {
  const { data, customer, today, wallet } = portal;
  if (data === null || customer === null) return null;

  const flows = data.cashFlows[customer.id] ?? [];
  const full = flows.slice(0, -1);
  const totalIn = full.reduce((s, f) => s + f.inflow, 0);
  const totalOut = full.reduce((s, f) => s + f.outflow, 0);
  const forecast = forecastBalance(customer, wallet, data.transactions, today);

  return (
    <>
      <PageHeader title="Cash flow" sub="Money in and out of your FlowCard wallet, by month." />

      <div className="bcp-g3">
        <Panel><Kpi label={`In, last ${full.length} months`} value={usd(totalIn)} valueColor={color.positive} /></Panel>
        <Panel><Kpi label={`Out, last ${full.length} months`} value={usd(totalOut)} /></Panel>
        <Panel>
          <Kpi label="Net" value={usd(totalIn - totalOut)} valueColor={totalIn - totalOut >= 0 ? color.positive : color.critical} />
        </Panel>
      </div>

      <Panel
        title="Monthly in, out and net"
        meta={
          <Legend
            items={[
              { label: 'In', color: chartColor.inflow },
              { label: 'Out', color: chartColor.outflow },
              { label: 'Net', color: chartColor.net },
            ]}
          />
        }
      >
        <CashFlowChart flows={flows} />
        {flows.length > 0 && (
          <p style={{ margin: `${space.lg} 0 0`, fontSize: font.size.sm, color: color.textMuted }}>
            {monthLabel(flows[flows.length - 1].month)} is month to date. Money out is card spend.
          </p>
        )}
      </Panel>

      <Panel
        title="Forecast, next 60 days"
        meta={
          forecast.crossing === null ? (
            <Pill tone="positive">Stays above your low line</Pill>
          ) : (
            <Pill tone="critical">Below your low line on {shortDate(forecast.crossing.date)}</Pill>
          )
        }
      >
        <ForecastChart forecast={forecast} />
        <div style={{ overflowX: 'auto', marginTop: space.xl }}>
          <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: font.size.md, fontVariantNumeric: 'tabular-nums' }}>
            <thead>
              <tr style={{ color: color.textSecondary, textAlign: 'left' }}>
                <th style={th}>Expected deposit</th>
                <th style={th}>Usually arrives</th>
                <th style={{ ...th, textAlign: 'right' }}>Amount</th>
              </tr>
            </thead>
            <tbody>
              {forecast.arrivals.map((a) => (
                <tr key={`${a.payer_name}_${a.due_date}`}>
                  <td style={td}>{a.payer_name}</td>
                  <td style={td}>
                    {shortDate(a.expected_date)}
                    {a.expected_date !== a.due_date && <span style={{ color: color.textMuted }}> · due {shortDate(a.due_date)}</span>}
                  </td>
                  <td style={{ ...td, textAlign: 'right' }}>{usd(a.amount)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        <p style={{ margin: `${space.lg} 0 0`, fontSize: font.size.sm, color: color.textMuted }}>
          Spend assumed at {usd(forecast.daily_spend)} a day, your recent average. The low line is 10% of your 90-day average balance.
        </p>
      </Panel>
    </>
  );
}

const th = { padding: `${space.md} ${space.md}`, fontWeight: font.weight.regular, boxShadow: `inset 0 -1px 0 ${color.border}` } as const;
const td = { padding: `${space.lg} ${space.md}`, boxShadow: `inset 0 -1px 0 ${color.border}`, color: color.text } as const;
