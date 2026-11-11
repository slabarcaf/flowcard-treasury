/**
 * A customer record brought up to date with the bridges on the books.
 *
 * The generated bridge_history is a snapshot. Bridges disbursed or repaid in
 * this session live in the adapter, so before the engine reads a customer we
 * fold them in: an open history entry takes its live balance (or closes once
 * repaid), and new bridges join as OPEN. Bridge statistics are recomputed.
 */
import type { Bridge, Customer, PastBridge } from '../types.js';
import { daysBetween, round2 } from '../policy/util.js';
import { computeBridgeStats } from './generator.js';

const CLOSED: Bridge['status'][] = ['REPAID', 'WITHDRAWN', 'DECLINED'];

function owed(b: Bridge): number {
  return round2(b.principal_outstanding + b.fee_outstanding + b.accrued_fee);
}

export function withLiveBridges(customer: Customer, bridges: Bridge[], asOf: string): Customer {
  const live = bridges.filter((b) => b.customer_id === customer.id);
  const byTxn = new Map(live.map((b) => [b.transaction_id, b]));

  const history: PastBridge[] = customer.bridge_history.map((h) => {
    const b = h.outcome === 'OPEN' ? byTxn.get(h.transaction_id) : undefined;
    if (b === undefined) return h;
    if (b.status === 'REPAID') {
      const late = daysBetween(b.due_date, asOf) > 0;
      return {
        ...h,
        closed_at: asOf,
        days_outstanding: daysBetween(h.opened_at, asOf),
        outcome: late ? 'LATE' : 'ON_TIME',
        outstanding_balance: 0,
      };
    }
    return { ...h, outstanding_balance: owed(b) };
  });

  const known = new Set(customer.bridge_history.map((h) => h.transaction_id));
  for (const b of live) {
    if (known.has(b.transaction_id) || b.disbursed_at === null) continue;
    if (CLOSED.includes(b.status) && b.status !== 'REPAID') continue;
    const repaid = b.status === 'REPAID';
    // A repaid bridge from before the snapshot is already in the history under its own id.
    if (repaid && b.disbursed_at < asOf) continue;
    history.push({
      id: b.id,
      transaction_id: b.transaction_id,
      amount: b.principal,
      term_days: b.term_days,
      opened_at: b.disbursed_at,
      closed_at: repaid ? asOf : null,
      days_outstanding: Math.max(0, daysBetween(b.disbursed_at, asOf)),
      outcome: repaid ? (daysBetween(b.due_date, asOf) > 0 ? 'LATE' : 'ON_TIME') : 'OPEN',
      outstanding_balance: repaid ? 0 : owed(b),
      due_date: b.due_date,
    });
  }

  return { ...customer, bridge_history: history, bridge_stats: computeBridgeStats(history, asOf) };
}
