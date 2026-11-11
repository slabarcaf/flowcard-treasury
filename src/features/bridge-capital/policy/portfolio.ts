/** Book-level numbers for the back office, computed from customers and bridges. */
import type { Bridge, Customer, Tier } from '../types.js';
import { applyDelinquency } from './delinquency.js';
import { priceOffer } from './pricing.js';
import { computeTier, outstandingBalance } from './tiers.js';
import { round2 } from './util.js';

export interface DpdBucket {
  label: string;
  count: number;
  amount: number;
}

export interface Portfolio {
  outstanding_by_tier: Array<{ tier: Tier; amount: number }>;
  dpd_buckets: DpdBucket[];
  fees_earned: number;
  originated: number;
  defaulted: number;
  /** Defaulted principal over originated principal. */
  loss_rate: number;
  /** Closed bridges repaid on time. */
  on_time_rate: number;
}

const BUCKETS: Array<{ label: string; from: number; to: number }> = [
  { label: 'Current', from: 0, to: 0 },
  { label: '1–7 days', from: 1, to: 7 },
  { label: '8–30 days', from: 8, to: 30 },
  { label: '31–60 days', from: 31, to: 60 },
  { label: '60+ days', from: 61, to: Number.POSITIVE_INFINITY },
];

const OPEN: Bridge['status'][] = ['DISBURSED', 'COLLECTING', 'WATCH', 'DELINQUENT', 'SERIOUS', 'DEFAULTED'];

/** `customers` should already carry live bridges (data/live.ts). */
export function portfolio(customers: Customer[], bridges: Bridge[], today: string): Portfolio {
  const byTier = new Map<Tier, number>();
  for (const c of customers) {
    const tier = computeTier(c);
    byTier.set(tier, round2((byTier.get(tier) ?? 0) + outstandingBalance(c)));
  }

  const dpd = BUCKETS.map((b) => ({ label: b.label, count: 0, amount: 0 }));
  for (const b of bridges.filter((x) => OPEN.includes(x.status))) {
    const r = applyDelinquency(b, today);
    const i = BUCKETS.findIndex((k) => r.days_past_due >= k.from && r.days_past_due <= k.to);
    dpd[i].count += 1;
    dpd[i].amount = round2(dpd[i].amount + b.principal_outstanding + b.fee_outstanding + r.accrued_fee);
  }

  const history = customers.flatMap((c) => c.bridge_history);
  const closed = history.filter((h) => h.closed_at !== null);
  const originated = round2(history.reduce((s, h) => s + h.amount, 0));
  const defaulted = round2(history.filter((h) => h.outcome === 'DEFAULTED').reduce((s, h) => s + h.amount, 0));
  const closedFees = closed
    .filter((h) => h.outcome !== 'DEFAULTED')
    .reduce((s, h) => s + priceOffer(h.amount, h.term_days).fee, 0);
  const liveFees = bridges
    .filter((b) => OPEN.includes(b.status))
    .reduce((s, b) => s + (b.fee - b.fee_outstanding), 0);

  return {
    outstanding_by_tier: (['A', 'B', 'C', 'NONE'] as Tier[])
      .filter((t) => byTier.has(t))
      .map((tier) => ({ tier, amount: byTier.get(tier) ?? 0 })),
    dpd_buckets: dpd,
    fees_earned: round2(closedFees + liveFees),
    originated,
    defaulted,
    loss_rate: originated === 0 ? 0 : round2(defaulted / originated * 10_000) / 10_000,
    on_time_rate: closed.length === 0 ? 0 : closed.filter((h) => h.outcome === 'ON_TIME').length / closed.length,
  };
}
