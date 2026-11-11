/**
 * Cash forecast (spec §8.1, PLACEHOLDER): today's balance, plus each expected
 * receivable on the day its payer usually pays, minus average daily card spend.
 */
import type { CardTransaction, Customer, ExpectedReceivable } from '../types.js';
import { FORECAST } from './policy.config.js';
import { addDays, daysBetween, round2 } from './util.js';

export interface ForecastPoint {
  date: string;
  balance: number;
}

export interface ExpectedArrival extends ExpectedReceivable {
  /** due_date plus the payer's average days late. */
  expected_date: string;
}

export interface Forecast {
  points: ForecastPoint[];
  /** The line below which the wallet counts as running low. */
  low_line: number;
  /** First day below the low line, or null. */
  crossing: ForecastPoint | null;
  lowest: ForecastPoint;
  daily_spend: number;
  arrivals: ExpectedArrival[];
}

/** When a receivable usually lands: its due date plus the payer's average lateness. */
export function expectedArrival(r: ExpectedReceivable): string {
  return addDays(r.due_date, Math.round(r.payer_avg_days_late));
}

/** Average settled card spend per day over the lookback window. */
export function averageDailySpend(transactions: CardTransaction[], customerId: string, today: string): number {
  const days = FORECAST.spendLookbackDays;
  const total = transactions
    .filter((t) => t.customer_id === customerId && t.status !== 'DECLINED')
    .filter((t) => {
      const age = daysBetween(t.created_at, today);
      return age >= 0 && age < days;
    })
    .reduce((s, t) => s + t.amount, 0);
  return round2(total / days);
}

export function forecastBalance(
  customer: Customer,
  startBalance: number,
  transactions: CardTransaction[],
  today: string,
): Forecast {
  const dailySpend = averageDailySpend(transactions, customer.id, today);
  const arrivals = customer.expected_receivables
    .map((r) => ({ ...r, expected_date: expectedArrival(r) }))
    .sort((a, b) => a.expected_date.localeCompare(b.expected_date));
  const lowLine = round2(customer.balance_stats.avg_balance_90d * FORECAST.lowLinePctOfAvgBalance90d);

  const points: ForecastPoint[] = [];
  let balance = startBalance;
  for (let i = 0; i <= FORECAST.horizonDays; i++) {
    const date = addDays(today, i);
    if (i > 0) balance -= dailySpend;
    for (const a of arrivals) if (a.expected_date === date) balance += a.amount;
    points.push({ date, balance: round2(balance) });
  }

  const crossing = points.find((p) => p.balance < lowLine) ?? null;
  const lowest = points.reduce((min, p) => (p.balance < min.balance ? p : min), points[0]);
  return { points, low_line: lowLine, crossing, lowest, daily_spend: dailySpend, arrivals };
}

/** The earliest receivable expected to land within `termDays`: what would repay a bridge. */
export function backingReceivable(customer: Customer, termDays: number, today: string): ExpectedArrival | null {
  const end = addDays(today, termDays);
  return (
    customer.expected_receivables
      .map((r) => ({ ...r, expected_date: expectedArrival(r) }))
      .filter((r) => r.expected_date >= today && r.expected_date <= end)
      .sort((a, b) => a.expected_date.localeCompare(b.expected_date))[0] ?? null
  );
}
