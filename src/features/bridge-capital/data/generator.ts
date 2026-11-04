/**
 * Builds customers from a short blueprint: 180 days of daily balances and
 * 24 months of invoices, then the derived statistics of spec §2.
 *
 * Everything is seeded, so the same blueprint always produces the same numbers
 * and the stats report can be checked against the spec by hand.
 */
import type {
  BalanceStats,
  BridgeStats,
  CrmContext,
  Customer,
  DailyBalance,
  ExpectedReceivable,
  Invoice,
  PastBridge,
  BridgeOutcome,
} from '../types.js';
import { addDays, daysBetween, mean, percentile, round2 } from '../policy/util.js';

/** Days of balance history we generate when tenure allows it. */
export const BALANCE_DAYS = 180;
/** Months of invoices we generate. */
export const INVOICE_MONTHS = 24;
/** Window the spec uses for monthly_inflows: "average monthly deposits over the last 3 months". */
const INFLOW_WINDOW_DAYS = 90;
/** Window used for top_payer_concentration; 12 months keeps it stable. */
const CONCENTRATION_WINDOW_DAYS = 365;

/** Deterministic PRNG (mulberry32), so blueprints are reproducible. */
function rng(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

export interface PayerBlueprint {
  name: string;
  /** Share of inflows, 0–1. Shares are normalised if they do not sum to 1. */
  share: number;
  on_time_rate: number;
  avg_days_late: number;
  relationship_months: number;
}

export interface PastBridgeBlueprint {
  amount: number;
  term_days: number;
  outcome: BridgeOutcome;
  /** Days before the as-of date the bridge closed. Ignored when outcome is OPEN. */
  closed_days_ago?: number;
  days_outstanding?: number;
  /** Still owed, for an OPEN bridge. */
  outstanding_balance?: number;
  /** Days before (negative) or after (positive) the as-of date it falls due. */
  due_in_days?: number;
  transaction_id?: string;
}

export interface ReceivableBlueprint {
  payer_name: string;
  amount: number;
  /** Days after the as-of date it is due. */
  due_in_days: number;
}

export interface CustomerBlueprint {
  id: string;
  name: string;
  seed: number;
  tenure_months: number;
  /** Target average monthly deposits; drives invoice sizes. */
  monthly_inflow_target: number;
  /** Typical end-of-day balance as a share of monthly inflow. */
  balance_level: number;
  /** Days in the last 90 that dip to near zero. Drives days_near_zero_90d. */
  near_zero_days_in_90: number;
  /** Spread of the ordinary (non-dip) daily balances, 0–1. */
  balance_noise: number;
  trend: 'UP' | 'FLAT' | 'DOWN';
  payers: PayerBlueprint[];
  bridges: PastBridgeBlueprint[];
  receivables: ReceivableBlueprint[];
  crm: CrmContext;
}

/** The day every window is measured back from. */
export interface GeneratorOptions {
  as_of: string;
}

// ---------------------------------------------------------------------------
// Balances
// ---------------------------------------------------------------------------

/**
 * 180 end-of-day balances (fewer when tenure is shorter), oldest first.
 *
 * Dip days are spaced evenly through the last 90 so that `near_zero_days_in_90`
 * comes back out of the computed statistics.
 */
export function generateBalanceHistory(bp: CustomerBlueprint, asOf: string): DailyBalance[] {
  const rand = rng(bp.seed);
  const days = Math.min(BALANCE_DAYS, Math.round(bp.tenure_months * 30));
  const base = bp.monthly_inflow_target * bp.balance_level;

  const dipDays = new Set<number>();
  if (bp.near_zero_days_in_90 > 0) {
    const window = Math.min(90, days);
    const spacing = window / bp.near_zero_days_in_90;
    for (let i = 0; i < bp.near_zero_days_in_90; i++) {
      // Offsets counted back from the most recent day.
      dipDays.add(days - 1 - Math.floor(i * spacing + spacing / 2));
    }
  }

  const trendSlope = bp.trend === 'UP' ? 0.4 : bp.trend === 'DOWN' ? -0.4 : 0;

  const out: DailyBalance[] = [];
  for (let i = 0; i < days; i++) {
    const date = addDays(asOf, -(days - 1 - i));
    // progress runs 0 at the oldest day to 1 at the most recent.
    const progress = days === 1 ? 1 : i / (days - 1);
    const trended = base * (1 + trendSlope * (progress - 0.5));
    const balance = dipDays.has(i)
      ? round2(base * (0.01 + rand() * 0.05))
      : round2(trended * (1 + (rand() - 0.5) * 2 * bp.balance_noise));
    out.push({ date, balance: Math.max(0, balance) });
  }
  return out;
}

// ---------------------------------------------------------------------------
// Invoices
// ---------------------------------------------------------------------------

/**
 * 24 months of invoices, oldest first, sized so that deposits average
 * `monthly_inflow_target` a month and the largest payer holds its share.
 *
 * Invoices due after the as-of date are left unpaid; they are what the
 * expected-receivable rule looks at.
 */
export function generateInvoices(bp: CustomerBlueprint, asOf: string): Invoice[] {
  const rand = rng(bp.seed + 7);
  const months = Math.min(INVOICE_MONTHS, Math.max(1, Math.round(bp.tenure_months)));
  const shareTotal = bp.payers.reduce((s, p) => s + p.share, 0) || 1;

  const out: Invoice[] = [];
  for (let m = months - 1; m >= 0; m--) {
    // Month m counts back from the as-of date; m = 0 is the most recent month.
    const monthTotal = bp.monthly_inflow_target * (1 + (rand() - 0.5) * 0.06);
    bp.payers.forEach((payer, pi) => {
      const amount = round2((monthTotal * payer.share) / shareTotal);
      if (amount <= 0) return;
      // Spread payers through the month so a 90-day window catches three of each.
      const dayInMonth = 4 + Math.floor((pi / Math.max(1, bp.payers.length)) * 20);
      const issued = addDays(asOf, -(m * 30 + 30 - dayInMonth));
      const due = addDays(issued, 30);
      const paysLate = rand() > payer.on_time_rate;
      const paid = paysLate ? addDays(due, Math.round(payer.avg_days_late)) : due;
      out.push({
        id: `inv_${bp.id}_${m}_${pi}`,
        payer_name: payer.name,
        amount,
        issued_date: issued,
        due_date: due,
        paid_date: daysBetween(paid, asOf) >= 0 ? paid : null,
      });
    });
  }
  return out.sort((a, b) => a.issued_date.localeCompare(b.issued_date));
}

// ---------------------------------------------------------------------------
// Derived statistics (spec §2)
// ---------------------------------------------------------------------------

export function computeBalanceStats(history: DailyBalance[]): BalanceStats {
  const balances = history.map((d) => d.balance);
  const last = (n: number) => balances.slice(Math.max(0, balances.length - n));

  const avg30 = round2(mean(last(30)));
  const avg90 = round2(mean(last(90)));
  const avg180 = round2(mean(last(180)));

  const window90 = last(90);
  const nearZeroThreshold = avg90 * 0.10;

  const ratio = avg180 === 0 ? 1 : avg30 / avg180;
  const trend: BalanceStats['balance_trend'] = ratio > 1.05 ? 'UP' : ratio < 0.95 ? 'DOWN' : 'FLAT';

  return {
    avg_balance_30d: avg30,
    avg_balance_90d: avg90,
    avg_balance_180d: avg180,
    balance_trend: trend,
    low_point_balance_90d: round2(percentile(window90, 0.10)),
    days_near_zero_90d: window90.filter((b) => b < nearZeroThreshold).length,
  };
}

/** monthly_inflows and top_payer_concentration, from paid invoices (spec §2). */
export function computeInflowStats(invoices: Invoice[], asOf: string) {
  const paidWithin = (days: number) =>
    invoices.filter((inv) => {
      if (inv.paid_date === null) return false;
      const age = daysBetween(inv.paid_date, asOf);
      return age >= 0 && age < days;
    });

  const recent = paidWithin(INFLOW_WINDOW_DAYS);
  const monthlyInflows = round2(
    recent.reduce((s, inv) => s + inv.amount, 0) / (INFLOW_WINDOW_DAYS / 30),
  );

  const forConcentration = paidWithin(CONCENTRATION_WINDOW_DAYS);
  const byPayer = new Map<string, number>();
  for (const inv of forConcentration) {
    byPayer.set(inv.payer_name, (byPayer.get(inv.payer_name) ?? 0) + inv.amount);
  }
  const total = [...byPayer.values()].reduce((a, b) => a + b, 0);
  const largest = Math.max(0, ...byPayer.values());

  return {
    monthly_inflows: monthlyInflows,
    top_payer_concentration: total === 0 ? 0 : round2(largest / total),
  };
}

export function computeBridgeStats(bridges: PastBridge[], asOf: string): BridgeStats {
  const closed = bridges.filter((b) => b.closed_at !== null);
  const open = bridges.filter((b) => b.outcome === 'OPEN');
  return {
    bridges_count: bridges.length,
    avg_days_outstanding: round2(mean(closed.map((b) => b.days_outstanding))),
    on_time_count: closed.filter((b) => b.outcome === 'ON_TIME').length,
    late_count: closed.filter((b) => b.outcome === 'LATE').length,
    open_bridge: open.length > 0,
    open_bridge_past_due: open.some((b) => daysBetween(b.due_date, asOf) > 0),
  };
}

// ---------------------------------------------------------------------------
// Assembly
// ---------------------------------------------------------------------------

function buildBridges(bp: CustomerBlueprint, asOf: string): PastBridge[] {
  return bp.bridges.map((b, i) => {
    const closedAgo = b.closed_days_ago ?? 0;
    const isOpen = b.outcome === 'OPEN';
    const daysOut = b.days_outstanding ?? b.term_days;
    const openedAt = isOpen
      ? addDays(asOf, -daysOut)
      : addDays(asOf, -(closedAgo + daysOut));
    return {
      id: `br_${bp.id}_${i + 1}`,
      transaction_id: b.transaction_id ?? `txn_${bp.id}_past_${i + 1}`,
      amount: b.amount,
      term_days: b.term_days,
      opened_at: openedAt,
      closed_at: isOpen ? null : addDays(asOf, -closedAgo),
      days_outstanding: daysOut,
      outcome: b.outcome,
      outstanding_balance: isOpen ? (b.outstanding_balance ?? b.amount) : 0,
      due_date: b.due_in_days !== undefined ? addDays(asOf, b.due_in_days) : addDays(openedAt, b.term_days),
    };
  });
}

function buildReceivables(bp: CustomerBlueprint, asOf: string): ExpectedReceivable[] {
  return bp.receivables.map((r) => {
    const payer = bp.payers.find((p) => p.name === r.payer_name);
    return {
      payer_name: r.payer_name,
      amount: r.amount,
      due_date: addDays(asOf, r.due_in_days),
      payer_on_time_rate: payer?.on_time_rate ?? 0,
      payer_avg_days_late: payer?.avg_days_late ?? 0,
      relationship_months: payer?.relationship_months ?? 0,
    };
  });
}

/** Build one customer, with all derived statistics computed from the generated data. */
export function generateCustomer(bp: CustomerBlueprint, opts: GeneratorOptions): Customer {
  const balance_history = generateBalanceHistory(bp, opts.as_of);
  const invoices = generateInvoices(bp, opts.as_of);
  const bridge_history = buildBridges(bp, opts.as_of);

  return {
    id: bp.id,
    name: bp.name,
    tenure_months: bp.tenure_months,
    balance_history,
    invoices,
    expected_receivables: buildReceivables(bp, opts.as_of),
    bridge_history,
    balance_stats: computeBalanceStats(balance_history),
    inflow_stats: computeInflowStats(invoices, opts.as_of),
    bridge_stats: computeBridgeStats(bridge_history, opts.as_of),
    crm: bp.crm,
  };
}

export function generateCustomers(
  blueprints: CustomerBlueprint[],
  opts: GeneratorOptions,
): Customer[] {
  return blueprints.map((bp) => generateCustomer(bp, opts));
}
