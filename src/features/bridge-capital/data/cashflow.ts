/**
 * Monthly money in and out of a wallet: deposits in, settled card spend out.
 *
 * Pure aggregation over what the adapter already holds. A real adapter can
 * return the same shape straight from the backend.
 */
import type { CardTransaction, Deposit, MonthlyCashFlow } from '../types.js';
import { round2 } from '../policy/util.js';

/** The `count` calendar months ending with the as-of month, oldest first, as YYYY-MM. */
export function monthsEnding(asOf: string, count: number): string[] {
  const [y, m] = asOf.split('-').map(Number);
  const out: string[] = [];
  for (let i = count - 1; i >= 0; i--) {
    const d = new Date(Date.UTC(y, m - 1 - i, 1));
    out.push(`${d.getUTCFullYear()}-${String(d.getUTCMonth() + 1).padStart(2, '0')}`);
  }
  return out;
}

/**
 * In, out and net per month for one customer. Months before the customer's
 * first activity are dropped, so a two-month-old account shows two months.
 */
export function monthlyCashFlow(
  customerId: string,
  deposits: Deposit[],
  transactions: CardTransaction[],
  asOf: string,
  months = 12,
): MonthlyCashFlow[] {
  const keys = monthsEnding(asOf, months);
  const inflow = new Map<string, number>();
  const outflow = new Map<string, number>();
  for (const d of deposits) {
    if (d.customer_id !== customerId || d.settled_at.slice(0, 10) > asOf) continue;
    const k = d.settled_at.slice(0, 7);
    inflow.set(k, (inflow.get(k) ?? 0) + d.amount);
  }
  for (const t of transactions) {
    if (t.customer_id !== customerId || t.status === 'DECLINED' || t.created_at.slice(0, 10) > asOf) continue;
    const k = t.created_at.slice(0, 7);
    outflow.set(k, (outflow.get(k) ?? 0) + t.amount);
  }
  const rows = keys.map((month) => {
    const i = round2(inflow.get(month) ?? 0);
    const o = round2(outflow.get(month) ?? 0);
    return { month, inflow: i, outflow: o, net: round2(i - o) };
  });
  const first = rows.findIndex((r) => r.inflow > 0 || r.outflow > 0);
  return first === -1 ? [] : rows.slice(first);
}
