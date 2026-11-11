/**
 * Everything that moved money, in one list.
 *
 * Pure: it takes what the adapter already loaded and merges card spend,
 * deposits and bridge movements into a single stream the Activity screen can
 * group by month and by day. No React, no decisions — the engine owns those.
 */
import type { Bridge, CardTransaction, Deposit, TimelineEvent } from '../types.js';
import { round2 } from '../policy/util.js';

export type ActivityKind = 'CARD' | 'CARD_DECLINED' | 'DEPOSIT' | 'BRIDGE_IN' | 'BRIDGE_OUT';

export interface ActivityItem {
  id: string;
  /** ISO datetime, for ordering. */
  at: string;
  /** YYYY-MM-DD, for day grouping. */
  date: string;
  kind: ActivityKind;
  title: string;
  subtitle: string;
  /** Signed: positive is money in, negative is money out. A decline moved nothing. */
  amount: number;
  transaction?: CardTransaction;
  bridge_id?: string;
}

export interface ActivitySource {
  transactions: CardTransaction[];
  deposits: Deposit[];
  bridges: Bridge[];
  timeline: TimelineEvent[];
}

export type ActivityFilter = 'ALL' | 'CARD' | 'DEPOSITS' | 'BRIDGES';

const KINDS_BY_FILTER: Record<ActivityFilter, ActivityKind[] | null> = {
  ALL: null,
  CARD: ['CARD', 'CARD_DECLINED'],
  DEPOSITS: ['DEPOSIT'],
  BRIDGES: ['BRIDGE_IN', 'BRIDGE_OUT'],
};

/** YYYY-MM for an ISO date or datetime. */
export function monthKey(iso: string): string {
  return iso.slice(0, 7);
}

function dayOf(iso: string): string {
  return iso.slice(0, 10);
}

function cardItem(txn: CardTransaction): ActivityItem {
  const declined = txn.status === 'DECLINED';
  return {
    id: txn.id,
    at: txn.created_at,
    date: dayOf(txn.created_at),
    kind: declined ? 'CARD_DECLINED' : 'CARD',
    title: txn.merchant_name,
    subtitle: declined ? 'Declined — not enough in your wallet' : txn.merchant_category,
    amount: declined ? 0 : -txn.amount,
    transaction: txn,
  };
}

/**
 * How much of a bridge has come back, and when it was last collected.
 *
 * The amount is what the bridge itself says is no longer outstanding, so it
 * cannot drift from the ledger; the date comes from the last collection on the
 * timeline, and nothing is shown when there is none.
 */
function collectionItem(bridge: Bridge, timeline: TimelineEvent[]): ActivityItem | null {
  const collected = round2(
    bridge.principal + bridge.fee - bridge.principal_outstanding - bridge.fee_outstanding,
  );
  if (collected <= 0) return null;

  const last = timeline
    .filter((e) => e.bridge_id === bridge.id && e.type === 'COLLECTION')
    .sort((a, b) => a.at.localeCompare(b.at))
    .at(-1);
  if (last === undefined) return null;

  return {
    id: `${bridge.id}_collected`,
    at: last.at,
    date: dayOf(last.at),
    kind: 'BRIDGE_OUT',
    title: 'Bridge repayment',
    subtitle: bridge.principal_outstanding + bridge.fee_outstanding > 0
      ? 'Collected from your deposits so far'
      : 'Repaid in full',
    amount: -collected,
    bridge_id: bridge.id,
  };
}

/** One customer's money movements, newest first. */
export function buildActivity(source: ActivitySource, customerId: string): ActivityItem[] {
  const items: ActivityItem[] = [];

  for (const txn of source.transactions) {
    if (txn.customer_id === customerId) items.push(cardItem(txn));
  }

  for (const dep of source.deposits) {
    if (dep.customer_id !== customerId) continue;
    items.push({
      id: dep.id,
      at: dep.settled_at,
      date: dayOf(dep.settled_at),
      kind: 'DEPOSIT',
      title: dep.payer_name,
      subtitle: 'Deposit received',
      amount: dep.amount,
    });
  }

  for (const bridge of source.bridges) {
    if (bridge.customer_id !== customerId || bridge.disbursed_at === null) continue;
    const disbursedAt = bridge.disbursed_at;
    items.push({
      id: `${bridge.id}_disbursed`,
      at: disbursedAt,
      date: dayOf(disbursedAt),
      kind: 'BRIDGE_IN',
      title: 'Bridge funded',
      subtitle: `${bridge.term_days}-day bridge added to your wallet`,
      amount: bridge.principal,
      bridge_id: bridge.id,
    });
    const collected = collectionItem(bridge, source.timeline);
    if (collected !== null) items.push(collected);
  }

  return items.sort((a, b) => b.at.localeCompare(a.at));
}

/** Months that have any activity, newest first. */
export function monthsWithActivity(items: ActivityItem[]): string[] {
  const months = new Set(items.map((i) => monthKey(i.at)));
  return [...months].sort((a, b) => b.localeCompare(a));
}

export function filterActivity(
  items: ActivityItem[],
  month: string | null,
  filter: ActivityFilter,
): ActivityItem[] {
  const kinds = KINDS_BY_FILTER[filter];
  return items.filter(
    (i) =>
      (month === null || monthKey(i.at) === month) && (kinds === null || kinds.includes(i.kind)),
  );
}

export interface ActivityTotals {
  in: number;
  out: number;
  net: number;
}

export function totals(items: ActivityItem[]): ActivityTotals {
  let moneyIn = 0;
  let moneyOut = 0;
  for (const i of items) {
    if (i.amount > 0) moneyIn += i.amount;
    else moneyOut -= i.amount;
  }
  return { in: round2(moneyIn), out: round2(moneyOut), net: round2(moneyIn - moneyOut) };
}

export interface ActivityDay {
  date: string;
  items: ActivityItem[];
}

/** Grouped by day, newest day first, items already newest first within a day. */
export function groupByDay(items: ActivityItem[]): ActivityDay[] {
  const byDay = new Map<string, ActivityItem[]>();
  for (const item of items) {
    const list = byDay.get(item.date);
    if (list === undefined) byDay.set(item.date, [item]);
    else list.push(item);
  }
  return [...byDay.entries()]
    .map(([date, dayItems]) => ({ date, items: dayItems }))
    .sort((a, b) => b.date.localeCompare(a.date));
}

/** Card spend in the given items, totalled by merchant category, largest first. */
export function spendByCategory(items: ActivityItem[]): Array<{ category: string; amount: number }> {
  const byCategory = new Map<string, number>();
  for (const i of items) {
    if (i.kind !== 'CARD' || i.transaction === undefined) continue;
    const key = i.transaction.merchant_category;
    byCategory.set(key, (byCategory.get(key) ?? 0) - i.amount);
  }
  return [...byCategory.entries()]
    .map(([category, amount]) => ({ category, amount: round2(amount) }))
    .sort((a, b) => b.amount - a.amount);
}
