/** Bridge Capital: types, policy engine and data layer. */
export * from './types.js';
export * from './policy/index.js';
export type { DataAdapter } from './data/adapter.js';
export { MockAdapter, demoSeed } from './data/MockAdapter.js';
export type { MockAdapterSeed } from './data/MockAdapter.js';
export {
  generateCustomer,
  generateCustomers,
  generateBalanceHistory,
  generateInvoices,
  computeBalanceStats,
  computeInflowStats,
  computeBridgeStats,
  BALANCE_DAYS,
  INVOICE_MONTHS,
} from './data/generator.js';
export type {
  CustomerBlueprint,
  PayerBlueprint,
  PastBridgeBlueprint,
  ReceivableBlueprint,
  GeneratorOptions,
} from './data/generator.js';
export {
  AS_OF,
  DEMO_CAPITAL,
  DEMO_BLUEPRINTS,
  DEMO_TRANSACTIONS,
  DEMO_REQUESTS,
  DEMO_BRIDGES,
  DEMO_TIMELINE,
} from './data/customers.js';
export {
  formatCustomerStatsReport,
  formatCustomerStats,
  formatCapital,
} from './data/statsReport.js';
