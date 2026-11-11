/** Date and money helpers shared by the policy functions. All dates are ISO YYYY-MM-DD. */

const MS_PER_DAY = 86_400_000;

/** Parse an ISO date (or date-time) as UTC midnight, so day maths never shifts with the local zone. */
function toUtcDay(iso: string): number {
  const [datePart] = iso.split('T');
  const [y, m, d] = datePart.split('-').map(Number);
  return Date.UTC(y, m - 1, d);
}

/** ISO YYYY-MM-DD for a UTC timestamp. */
export function isoDate(ms: number): string {
  return new Date(ms).toISOString().slice(0, 10);
}

/** Whole days from `from` to `to`. Negative when `to` is earlier. */
export function daysBetween(from: string, to: string): number {
  return Math.round((toUtcDay(to) - toUtcDay(from)) / MS_PER_DAY);
}

/** `iso` shifted by `days`. */
export function addDays(iso: string, days: number): string {
  return isoDate(toUtcDay(iso) + days * MS_PER_DAY);
}

/** Round to cents. Every dollar figure the policy returns goes through this. */
export function round2(n: number): number {
  return Math.round(n * 100) / 100;
}

/** Arithmetic mean, 0 for an empty list. */
export function mean(xs: number[]): number {
  if (xs.length === 0) return 0;
  return xs.reduce((a, b) => a + b, 0) / xs.length;
}

/**
 * Linear-interpolated percentile, p in 0–1.
 * Used for low_point_balance_90d (the 10th percentile of daily balances).
 */
export function percentile(xs: number[], p: number): number {
  if (xs.length === 0) return 0;
  const sorted = [...xs].sort((a, b) => a - b);
  if (sorted.length === 1) return sorted[0];
  const idx = (sorted.length - 1) * p;
  const lo = Math.floor(idx);
  const hi = Math.ceil(idx);
  if (lo === hi) return sorted[lo];
  return sorted[lo] + (sorted[hi] - sorted[lo]) * (idx - lo);
}
