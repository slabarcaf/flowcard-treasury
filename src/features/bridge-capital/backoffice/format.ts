/** Display formatting for the back office. */

export function usd(n: number, opts: { cents?: boolean } = {}): string {
  return n.toLocaleString('en-US', {
    style: 'currency',
    currency: 'USD',
    minimumFractionDigits: opts.cents ? 2 : 0,
    maximumFractionDigits: opts.cents ? 2 : 0,
  });
}

export function pct(fraction: number, dp = 1): string {
  return `${(fraction * 100).toFixed(dp)}%`;
}

/** "3 Nov 2026" — short enough for dense tables, unambiguous across locales. */
export function shortDate(iso: string): string {
  const [date] = iso.split('T');
  const [y, m, d] = date.split('-').map(Number);
  const month = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'][m - 1];
  return `${d} ${month} ${y}`;
}

/** "9:02" from an ISO timestamp, or "" when there is no time part. */
export function timeOf(iso: string): string {
  const [, time] = iso.split('T');
  if (time === undefined) return '';
  const [h, min] = time.split(':');
  return `${Number(h)}:${min}`;
}

export function titleCase(s: string): string {
  return s
    .toLowerCase()
    .split('_')
    .map((w) => w.charAt(0).toUpperCase() + w.slice(1))
    .join(' ');
}
