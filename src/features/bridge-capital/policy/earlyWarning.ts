/** Early-warning signals for ops (spec §8.2, PLACEHOLDER thresholds). */
import type { Customer } from '../types.js';
import type { Forecast } from './forecast.js';
import { EARLY_WARNING } from './policy.config.js';
import { daysBetween } from './util.js';

export type WarningId = 'TREND_DROP' | 'NEAR_ZERO' | 'SLOW_PAYER' | 'FORECAST_LOW' | 'CONCENTRATION';

export interface EarlyWarning {
  id: WarningId;
  severity: 'HIGH' | 'WATCH';
  text: string;
}

const pct = (n: number) => `${Math.round(n * 100)}%`;

export function earlyWarnings(customer: Customer, forecast: Forecast | null, today: string): EarlyWarning[] {
  const out: EarlyWarning[] = [];
  const s = customer.balance_stats;

  if (s.avg_balance_180d > 0 && s.avg_balance_30d < s.avg_balance_180d * (1 - EARLY_WARNING.trendDropPct)) {
    out.push({
      id: 'TREND_DROP',
      severity: 'HIGH',
      text: `30-day average balance is ${pct(1 - s.avg_balance_30d / s.avg_balance_180d)} below the 180-day average`,
    });
  }
  if (s.days_near_zero_90d > EARLY_WARNING.maxDaysNearZero90d) {
    out.push({ id: 'NEAR_ZERO', severity: 'WATCH', text: `${s.days_near_zero_90d} days near zero in the last 90` });
  }
  const slow = customer.expected_receivables.find((r) => r.payer_on_time_rate < EARLY_WARNING.slowPayerOnTimeBelow);
  if (slow !== undefined) {
    out.push({
      id: 'SLOW_PAYER',
      severity: 'WATCH',
      text: `${slow.payer_name} pays on time ${pct(slow.payer_on_time_rate)} of the time, ${Math.round(slow.payer_avg_days_late)} days late on average`,
    });
  }
  if (forecast?.crossing && daysBetween(today, forecast.crossing.date) <= EARLY_WARNING.forecastWithinDays) {
    out.push({ id: 'FORECAST_LOW', severity: 'HIGH', text: `Forecast drops below the low line on ${forecast.crossing.date}` });
  }
  if (customer.inflow_stats.top_payer_concentration > EARLY_WARNING.topPayerShareAbove) {
    out.push({
      id: 'CONCENTRATION',
      severity: 'WATCH',
      text: `${pct(customer.inflow_stats.top_payer_concentration)} of inflows come from one payer`,
    });
  }
  return out;
}
