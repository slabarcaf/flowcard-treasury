import { describe, it, expect } from 'vitest';
import {
  buildActivity,
  filterActivity,
  groupByDay,
  monthsWithActivity,
  spendByCategory,
  totals,
} from '../../../src/features/bridge-capital/portal/activity.js';
import {
  generateBalanceHistory,
  generateCardTransactions,
  generateDeposits,
  generateInvoices,
  computeBalanceStats,
  computeInflowStats,
} from '../../../src/features/bridge-capital/data/generator.js';
import {
  AS_OF,
  DEMO_BLUEPRINTS,
  DEMO_BRIDGES,
  DEMO_DEPOSITS,
  DEMO_TIMELINE,
  DEMO_TRANSACTIONS,
} from '../../../src/features/bridge-capital/data/customers.js';
import type {
  Bridge,
  CardTransaction,
  Deposit,
} from '../../../src/features/bridge-capital/types.js';

const source = {
  transactions: DEMO_TRANSACTIONS,
  deposits: DEMO_DEPOSITS,
  bridges: DEMO_BRIDGES,
  timeline: DEMO_TIMELINE,
};

const northwind = DEMO_BLUEPRINTS.find((b) => b.id === 'acct_northwind')!;

function card(over: Partial<CardTransaction> = {}): CardTransaction {
  return {
    id: 'txn_1',
    customer_id: 'c1',
    card_id: 'card_1',
    amount: 100,
    currency: 'USD',
    merchant_name: 'Acme',
    merchant_category: 'suppliers',
    criticality: 'CRITICAL',
    status: 'SETTLED',
    wallet_balance: 500,
    created_at: '2026-11-02T10:00:00Z',
    ...over,
  };
}

function deposit(over: Partial<Deposit> = {}): Deposit {
  return {
    id: 'dep_1',
    customer_id: 'c1',
    amount: 400,
    payer_name: 'Payer',
    settled_at: '2026-11-02',
    ...over,
  };
}

describe('activity: merging', () => {
  it('signs card spend out and deposits in, and leaves a decline at zero', () => {
    const items = buildActivity(
      {
        transactions: [card(), card({ id: 'txn_2', status: 'DECLINED', amount: 900 })],
        deposits: [deposit()],
        bridges: [],
        timeline: [],
      },
      'c1',
    );

    expect(items.map((i) => [i.kind, i.amount])).toEqual(
      expect.arrayContaining([
        ['CARD', -100],
        ['CARD_DECLINED', 0],
        ['DEPOSIT', 400],
      ]),
    );
    expect(totals(items)).toEqual({ in: 400, out: 100, net: 300 });
  });

  it('keeps one customer out of another customer’s activity', () => {
    const items = buildActivity(
      { transactions: [card({ customer_id: 'other' })], deposits: [], bridges: [], timeline: [] },
      'c1',
    );
    expect(items).toEqual([]);
  });

  it('orders newest first', () => {
    const items = buildActivity(
      {
        transactions: [
          card({ id: 'old', created_at: '2026-09-01T10:00:00Z' }),
          card({ id: 'new', created_at: '2026-11-01T10:00:00Z' }),
        ],
        deposits: [],
        bridges: [],
        timeline: [],
      },
      'c1',
    );
    expect(items.map((i) => i.id)).toEqual(['new', 'old']);
  });
});

describe('activity: bridges', () => {
  const bridge: Bridge = {
    id: 'b1',
    customer_id: 'c1',
    transaction_id: 'txn_1',
    principal: 5_000,
    fee: 150,
    term_days: 30,
    status: 'COLLECTING',
    disbursed_at: '2026-10-05',
    due_date: '2026-11-04',
    principal_outstanding: 2_000,
    fee_outstanding: 0,
    accrued_fee: 0,
  };

  it('counts a disbursement as money in', () => {
    const items = buildActivity(
      { transactions: [], deposits: [], bridges: [bridge], timeline: [] },
      'c1',
    );
    expect(items.find((i) => i.kind === 'BRIDGE_IN')?.amount).toBe(5_000);
  });

  it('takes the collected amount from the ledger, not from the timeline text', () => {
    const items = buildActivity(
      {
        transactions: [],
        deposits: [],
        bridges: [bridge],
        timeline: [
          {
            id: 'e1',
            at: '2026-10-28T11:00:00Z',
            customer_id: 'c1',
            bridge_id: 'b1',
            type: 'COLLECTION',
            text: 'whatever this says',
          },
        ],
      },
      'c1',
    );
    const out = items.find((i) => i.kind === 'BRIDGE_OUT');
    // principal 5000 + fee 150, less 2000 still outstanding.
    expect(out?.amount).toBe(-3_150);
    expect(out?.date).toBe('2026-10-28');
  });

  it('shows no collection when nothing has been collected', () => {
    const items = buildActivity(
      {
        transactions: [],
        deposits: [],
        bridges: [{ ...bridge, principal_outstanding: 5_000, fee_outstanding: 150 }],
        timeline: [],
      },
      'c1',
    );
    expect(items.some((i) => i.kind === 'BRIDGE_OUT')).toBe(false);
  });
});

describe('activity: months, days and categories', () => {
  const items = buildActivity(source, 'acct_northwind');

  it('offers a year of months, newest first', () => {
    const months = monthsWithActivity(items);
    expect(months.length).toBeGreaterThanOrEqual(12);
    expect(months[0]).toBe('2026-11');
    expect([...months].sort((a, b) => b.localeCompare(a))).toEqual(months);
  });

  it('keeps a month’s rows inside that month', () => {
    const october = filterActivity(items, '2026-10', 'ALL');
    expect(october.length).toBeGreaterThan(0);
    expect(october.every((i) => i.at.startsWith('2026-10'))).toBe(true);
  });

  it('filters to one kind at a time', () => {
    expect(filterActivity(items, null, 'DEPOSITS').every((i) => i.kind === 'DEPOSIT')).toBe(true);
    expect(
      filterActivity(items, null, 'CARD').every(
        (i) => i.kind === 'CARD' || i.kind === 'CARD_DECLINED',
      ),
    ).toBe(true);
  });

  it('groups by day, newest day first, losing nothing', () => {
    const month = filterActivity(items, '2026-10', 'ALL');
    const days = groupByDay(month);
    expect(days.flatMap((d) => d.items)).toHaveLength(month.length);
    expect([...days].sort((a, b) => b.date.localeCompare(a.date))).toEqual(days);
  });

  it('totals card spend by category, largest first', () => {
    const rows = spendByCategory(filterActivity(items, '2026-10', 'ALL'));
    expect(rows.length).toBeGreaterThan(0);
    expect(rows[0].amount).toBeGreaterThanOrEqual(rows[rows.length - 1].amount);
    expect(rows.every((r) => r.amount > 0)).toBe(true);
  });
});

describe('generated card spend and deposits', () => {
  it('is deterministic: the same blueprint gives the same transactions', () => {
    const a = generateCardTransactions(northwind, AS_OF);
    const b = generateCardTransactions(northwind, AS_OF);
    expect(a).toEqual(b);
    expect(a.length).toBeGreaterThan(100);
  });

  it('derives criticality from the category, so the engine and the portal agree', () => {
    const txns = generateCardTransactions(northwind, AS_OF);
    const payroll = txns.find((t) => t.merchant_category === 'payroll');
    const travel = txns.find((t) => t.merchant_category === 'travel');
    expect(payroll?.criticality).toBe('CRITICAL');
    expect(travel?.criticality).toBe('DISCRETIONARY');
  });

  it('does not move the statistics the engine scores on', () => {
    // Card spend is generated from its own stream; balances and invoices are
    // untouched, so tiers and limits cannot drift when spend changes.
    const history = generateBalanceHistory(northwind, AS_OF);
    const invoices = generateInvoices(northwind, AS_OF);
    const before = {
      balance: computeBalanceStats(history),
      inflow: computeInflowStats(invoices, AS_OF),
    };
    generateCardTransactions(northwind, AS_OF, history);
    expect(computeBalanceStats(history)).toEqual(before.balance);
    expect(computeInflowStats(invoices, AS_OF)).toEqual(before.inflow);
  });

  it('builds deposits from paid invoices and nothing else', () => {
    const invoices = generateInvoices(northwind, AS_OF);
    const deposits = generateDeposits(northwind.id, invoices);
    const paid = invoices.filter((i) => i.paid_date !== null);
    expect(deposits).toHaveLength(paid.length);
    expect(deposits.reduce((s, d) => s + d.amount, 0)).toBeCloseTo(
      paid.reduce((s, i) => s + i.amount, 0),
      2,
    );
  });
});
