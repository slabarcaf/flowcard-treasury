/**
 * A printable report of each customer's computed statistics, tier and limit,
 * so the numbers can be checked against docs/bridge-capital-spec.md by hand.
 */
import type { Customer, PlatformCapital } from '../types.js';
import { computeTier, customerLimit, outstandingBalance } from '../policy/tiers.js';
import {
  availableAboveFloor,
  capitalRatio,
  currentRegime,
  lendable,
  reserveFloor,
} from '../policy/regime.js';
import { REGIME_LABELS, TIER_LIMIT_PCT } from '../policy/policy.config.js';

function usd(n: number): string {
  return `$${Math.round(n).toLocaleString('en-US')}`;
}

function pct(n: number): string {
  return `${(n * 100).toFixed(1)}%`;
}

function row(label: string, value: string): string {
  return `  ${label.padEnd(26)}${value}`;
}

/** One customer's block: §2 statistics, then the §3 tier and limit. */
export function formatCustomerStats(customer: Customer): string {
  const tier = computeTier(customer);
  const limit = customerLimit(customer, tier);
  const b = customer.balance_stats;
  const f = customer.inflow_stats;
  const h = customer.bridge_stats;

  const lines = [
    `${customer.name}  (${customer.id})`,
    row('tenure_months', String(customer.tenure_months)),
    row('balance days', String(customer.balance_history.length)),
    row('invoices', String(customer.invoices.length)),
    row('avg_balance_30d', usd(b.avg_balance_30d)),
    row('avg_balance_90d', usd(b.avg_balance_90d)),
    row('avg_balance_180d', usd(b.avg_balance_180d)),
    row('trend (30d vs 180d)', b.balance_trend),
    row('low_point_balance_90d', usd(b.low_point_balance_90d)),
    row('days_near_zero_90d', String(b.days_near_zero_90d)),
    row('monthly_inflows', usd(f.monthly_inflows)),
    row('top_payer_concentration', pct(f.top_payer_concentration)),
    row(
      'bridge history',
      `${h.bridges_count} total, ${h.on_time_count} on time, ${h.late_count} late` +
        `${customer.bridge_history.some((x) => x.outcome === 'DEFAULTED') ? ', 1+ defaulted' : ''}`,
    ),
    row('avg_days_outstanding', String(h.avg_days_outstanding)),
    row('open bridge', h.open_bridge ? (h.open_bridge_past_due ? 'yes, past due' : 'yes') : 'no'),
    row('outstanding balance', usd(outstandingBalance(customer))),
    row('TIER', tier),
    row('LIMIT', `${usd(limit)}  (${pct(TIER_LIMIT_PCT[tier])} of monthly_inflows)`),
  ];
  return lines.join('\n');
}

/** The facility header: floor, lendable, available and the regime it implies. */
export function formatCapital(capital: PlatformCapital): string {
  const regime = currentRegime(capital);
  return [
    'Platform capital',
    row('facility_size', usd(capital.facility_size)),
    row('reserve floor', `${usd(reserveFloor(capital))}  (${pct(capital.reserve_floor_pct)})`),
    row('lendable', usd(lendable(capital))),
    row('outstanding', usd(capital.outstanding)),
    row('available above floor', usd(availableAboveFloor(capital))),
    row('as % of facility', pct(capitalRatio(capital))),
    row('REGIME', REGIME_LABELS[regime]),
  ].join('\n');
}

export function formatCustomerStatsReport(
  customers: Customer[],
  capital: PlatformCapital,
): string {
  const divider = '-'.repeat(68);
  return [
    divider,
    'Bridge Capital — computed customer statistics',
    divider,
    formatCapital(capital),
    '',
    ...customers.flatMap((c) => [formatCustomerStats(c), '']),
    divider,
  ].join('\n');
}
