/**
 * Spec §8 proposals (PLACEHOLDER thresholds): forecast, pre-approved amount,
 * early warnings, card access, portfolio, cash flow, live bridges, Flex pricing.
 *
 * Most cases run on the demo data the deployed app shows, so a regression in
 * what Santiago demos fails here first.
 */
import { describe, it, expect } from 'vitest';
import {
  AS_OF,
  MockAdapter,
  addDays,
  cardAccess,
  computeTier,
  earlyWarnings,
  expectedArrival,
  forecastBalance,
  portfolio,
  preApproved,
  priceOffer,
  EARLY_WARNING,
  FORECAST,
} from '../../../src/features/bridge-capital/index.js';
import type { Bridge, CollectionAction, Customer } from '../../../src/features/bridge-capital/types.js';
import { withLiveBridges } from '../../../src/features/bridge-capital/data/live.js';
import { monthlyCashFlow, monthsEnding } from '../../../src/features/bridge-capital/data/cashflow.js';

async function demo() {
  const adapter = new MockAdapter();
  const [raw, bridges, capital, transactions] = await Promise.all([
    adapter.listCustomers(),
    adapter.listBridges(),
    adapter.getCapital(),
    adapter.listTransactions(),
  ]);
  const customers = raw.map((c) => withLiveBridges(c, bridges, AS_OF));
  const byName = (name: string) => customers.find((c) => c.name === name) as Customer;
  return { adapter, customers, bridges, capital, transactions, byName };
}

describe('cash forecast (§8.1)', () => {
  it('Harbor Foods drops below its low line inside the horizon; Northwind does not', async () => {
    const { adapter, transactions, byName } = await demo();
    const harbor = byName('Harbor Foods');
    const northwind = byName('Northwind Supply');
    const fh = forecastBalance(harbor, await adapter.getWalletBalance(harbor.id), transactions, AS_OF);
    const fn = forecastBalance(northwind, await adapter.getWalletBalance(northwind.id), transactions, AS_OF);
    expect(fh.crossing).not.toBeNull();
    expect(fn.crossing).toBeNull();
    expect(fh.points).toHaveLength(FORECAST.horizonDays + 1);
  });

  it('puts the low line at the configured share of the 90-day average balance', async () => {
    const { adapter, transactions, byName } = await demo();
    const c = byName('Lumen Studio');
    const f = forecastBalance(c, await adapter.getWalletBalance(c.id), transactions, AS_OF);
    expect(f.low_line).toBeCloseTo(c.balance_stats.avg_balance_90d * FORECAST.lowLinePctOfAvgBalance90d, 2);
  });

  it('lands each receivable on its due date plus the payer’s average lateness', () => {
    expect(
      expectedArrival({
        payer_name: 'P',
        amount: 1,
        due_date: '2026-11-20',
        payer_on_time_rate: 0.6,
        payer_avg_days_late: 11,
        relationship_months: 3,
      }),
    ).toBe('2026-12-01');
  });
});

describe('pre-approved amount', () => {
  it('is the limit headroom capped by capital, and never more than eligibility would allow', async () => {
    const { capital, byName } = await demo();
    const lumen = byName('Lumen Studio');
    const pa = preApproved(lumen, capital, AS_OF);
    expect(pa.blocked).toBeNull();
    expect(pa.amount).toBeCloseTo(Math.min(pa.limit - pa.outstanding, 12_000), 2);
  });

  it('is zero with a reason when a bridge is overdue', async () => {
    const { capital, byName } = await demo();
    const pa = preApproved(byName('Harbor Foods'), capital, AS_OF);
    expect(pa.amount).toBe(0);
    expect(pa.blocked).toBe('PAST_DUE');
  });

  it('is zero with a reason without enough history', async () => {
    const { capital, byName } = await demo();
    const pa = preApproved(byName('Vertex Labs'), capital, AS_OF);
    expect(pa.amount).toBe(0);
    expect(pa.blocked).toBe('NO_HISTORY');
    expect(pa.reason).toMatch(/3 months/);
  });
});

describe('early warnings (§8.2)', () => {
  it('flags Harbor Foods on days near zero, a slow payer and the forecast', async () => {
    const { adapter, transactions, byName } = await demo();
    const harbor = byName('Harbor Foods');
    const f = forecastBalance(harbor, await adapter.getWalletBalance(harbor.id), transactions, AS_OF);
    const ids = earlyWarnings(harbor, f, AS_OF).map((w) => w.id);
    expect(ids).toEqual(expect.arrayContaining(['NEAR_ZERO', 'SLOW_PAYER', 'FORECAST_LOW']));
  });

  it('raises nothing for Northwind Supply', async () => {
    const { adapter, transactions, byName } = await demo();
    const c = byName('Northwind Supply');
    const f = forecastBalance(c, await adapter.getWalletBalance(c.id), transactions, AS_OF);
    expect(earlyWarnings(c, f, AS_OF)).toEqual([]);
  });

  it('trips the trend trigger only past the configured drop', async () => {
    const { byName } = await demo();
    const base = byName('Northwind Supply');
    const at = (avg30: number): Customer => ({
      ...base,
      balance_stats: { ...base.balance_stats, avg_balance_180d: 100_000, avg_balance_30d: avg30 },
    });
    const edge = 100_000 * (1 - EARLY_WARNING.trendDropPct);
    expect(earlyWarnings(at(edge), null, AS_OF).map((w) => w.id)).not.toContain('TREND_DROP');
    expect(earlyWarnings(at(edge - 1), null, AS_OF).map((w) => w.id)).toContain('TREND_DROP');
  });
});

describe('card access', () => {
  const bridge = (over: Partial<Bridge> = {}): Bridge => ({
    id: 'b1',
    customer_id: 'c',
    transaction_id: 't',
    principal: 5_000,
    fee: 150,
    term_days: 30,
    status: 'COLLECTING',
    disbursed_at: addDays(AS_OF, -5),
    due_date: addDays(AS_OF, 25),
    principal_outstanding: 5_000,
    fee_outstanding: 150,
    accrued_fee: 0,
    ...over,
  });
  const action = (type: CollectionAction['type']): CollectionAction => ({
    id: 'a', customer_id: 'c', bridge_id: 'b1', type, at: AS_OF, by: 'ops',
  });

  it('restricts tier C while a bridge is open, capped at the bridged amount', () => {
    expect(cardAccess('C', [bridge()], [], AS_OF)).toEqual({ state: 'RESTRICTED', source: 'TIER_C_WHILE_BRIDGED', cap: 5_000 });
    expect(cardAccess('B', [bridge()], [], AS_OF).state).toBe('NORMAL');
  });

  it('restricts every tier at 8 days past due and freezes at 31', () => {
    expect(cardAccess('A', [bridge({ due_date: addDays(AS_OF, -8) })], [], AS_OF).state).toBe('RESTRICTED');
    expect(cardAccess('A', [bridge({ due_date: addDays(AS_OF, -31) })], [], AS_OF).state).toBe('FROZEN');
  });

  it('applies an ops freeze until that bridge is repaid', () => {
    expect(cardAccess('A', [bridge()], [action('FREEZE_CARDS')], AS_OF).state).toBe('FROZEN');
    expect(cardAccess('A', [bridge({ status: 'REPAID' })], [action('FREEZE_CARDS')], AS_OF).state).toBe('NORMAL');
  });

  it('shows Harbor Foods restricted on the demo data', async () => {
    const { bridges, byName } = await demo();
    const harbor = byName('Harbor Foods');
    expect(cardAccess(computeTier(harbor), bridges.filter((b) => b.customer_id === harbor.id), [], AS_OF).state).toBe('RESTRICTED');
  });
});

describe('portfolio', () => {
  it('buckets the demo bridges by days past due and sums outstanding by tier', async () => {
    const { customers, bridges } = await demo();
    const p = portfolio(customers, bridges, AS_OF);
    expect(p.dpd_buckets.find((b) => b.label === 'Current')?.count).toBe(1);
    expect(p.dpd_buckets.find((b) => b.label === '8–30 days')?.count).toBe(1);
    expect(p.loss_rate).toBeGreaterThan(0);
    expect(p.loss_rate).toBeCloseTo(p.defaulted / p.originated, 4);
  });
});

describe('cash flow', () => {
  it('lists the twelve months ending with the as-of month', () => {
    const m = monthsEnding('2026-11-03', 12);
    expect(m[0]).toBe('2025-12');
    expect(m.at(-1)).toBe('2026-11');
  });

  it('drops months before a young account’s first activity', async () => {
    const { adapter, byName } = await demo();
    const vertex = byName('Vertex Labs');
    const flows = monthlyCashFlow(vertex.id, await adapter.listDeposits(), await adapter.listTransactions(), AS_OF);
    expect(flows.length).toBeLessThanOrEqual(3);
    for (const f of flows) expect(f.net).toBeCloseTo(f.inflow - f.outflow, 2);
  });
});

describe('live bridges', () => {
  it('closes a repaid overdue bridge as LATE and lifts the past-due block', async () => {
    const { adapter, capital } = await demo();
    const raw = (await adapter.listCustomers()).find((c) => c.name === 'Harbor Foods') as Customer;
    const bridges = (await adapter.listBridges()).map((b) =>
      b.customer_id === raw.id ? { ...b, status: 'REPAID' as const, principal_outstanding: 0, fee_outstanding: 0 } : b,
    );
    const live = withLiveBridges(raw, bridges, AS_OF);
    expect(live.bridge_history.filter((h) => h.outcome === 'OPEN')).toHaveLength(0);
    expect(live.bridge_history.filter((h) => h.outcome === 'LATE').length).toBe(2);
    expect(preApproved(live, capital, AS_OF).blocked).not.toBe('PAST_DUE');
  });

  it('adds a bridge disbursed in this session as OPEN against the limit', async () => {
    const { adapter, capital } = await demo();
    const raw = (await adapter.listCustomers()).find((c) => c.name === 'Lumen Studio') as Customer;
    const before = preApproved(withLiveBridges(raw, [], AS_OF), capital, AS_OF).amount;
    const fresh: Bridge = {
      id: 'bridge_new', customer_id: raw.id, transaction_id: 'txn_planned', principal: 2_000, fee: 90, term_days: 30,
      status: 'DISBURSED', disbursed_at: AS_OF, due_date: addDays(AS_OF, 30),
      principal_outstanding: 2_000, fee_outstanding: 90, accrued_fee: 0,
    };
    const after = preApproved(withLiveBridges(raw, [fresh], AS_OF), capital, AS_OF).amount;
    expect(before - after).toBeCloseTo(2_090, 2);
  });
});

describe('Flex on bills (§8.3)', () => {
  it('prices like a bridge: $10,000 for 60 days is $450', () => {
    expect(priceOffer(10_000, 60).fee).toBe(450);
  });
});
