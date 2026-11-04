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

const MONTH_NAMES = [
  'January', 'February', 'March', 'April', 'May', 'June',
  'July', 'August', 'September', 'October', 'November', 'December',
];

/** "November 2026" from a YYYY-MM key. */
export function monthLabel(key: string): string {
  const [y, m] = key.split('-').map(Number);
  return `${MONTH_NAMES[m - 1]} ${y}`;
}

/** "Monday 3 November" — a day heading, no year. */
export function dayLabel(iso: string): string {
  const date = new Date(`${iso.slice(0, 10)}T12:00:00Z`);
  const weekday = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday'][
    date.getUTCDay()
  ];
  return `${weekday} ${date.getUTCDate()} ${MONTH_NAMES[date.getUTCMonth()]}`;
}
