/** Chart colours, shared by charts and their legends. */
import { color } from './theme.js';

/** In and out bars use these, so legends and bars always agree. */
export const chartColor = {
  inflow: color.positive,
  outflow: '#5E6B80',
  net: color.accent,
  low: color.warning,
  cross: color.critical,
  grid: color.divider,
  axis: color.textMuted,
} as const;
