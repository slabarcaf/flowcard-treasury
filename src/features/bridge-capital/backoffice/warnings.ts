/** Early-warning rows for the back office, computed from live customers. */
import { earlyWarnings, type EarlyWarning } from '../policy/earlyWarning.js';
import { forecastBalance } from '../policy/forecast.js';
import type { Customer } from '../types.js';
import type { BackOffice } from './useBackOffice.js';

/** Every customer with at least one signal, most HIGH signals first. Shared with the overview. */
export function customersWithWarnings(office: BackOffice): Array<{ customer: Customer; warnings: EarlyWarning[] }> {
  const { data, today, liveCustomers } = office;
  if (data === null) return [];
  return liveCustomers
    .map((customer) => {
      const forecast = forecastBalance(customer, data.wallets[customer.id] ?? 0, data.transactions, today);
      return { customer, warnings: earlyWarnings(customer, forecast, today) };
    })
    .filter((x) => x.warnings.length > 0)
    .sort((a, b) => b.warnings.filter((w) => w.severity === 'HIGH').length - a.warnings.filter((w) => w.severity === 'HIGH').length);
}
