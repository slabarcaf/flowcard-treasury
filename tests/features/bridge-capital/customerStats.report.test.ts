/**
 * Prints each demo customer's computed stats, tier and limit.
 *
 * Run it on its own with `npm run bridge:stats`. It is a Vitest file rather
 * than a standalone script because the repo's `.js` import specifiers need
 * Vite's resolver, and a standalone TS runner would mean a new dependency.
 */
import { describe, it, expect } from 'vitest';
import {
  AS_OF,
  DEMO_BLUEPRINTS,
  DEMO_CAPITAL,
  DEMO_REQUESTS,
  computeTier,
  currentRegime,
  customerLimit,
  formatCustomerStatsReport,
  generateCustomers,
} from '../../../src/features/bridge-capital/index.js';

const customers = generateCustomers(DEMO_BLUEPRINTS, { as_of: AS_OF });

describe('demo customer stats', () => {
  it('prints the report', () => {
    console.log('\n' + formatCustomerStatsReport(customers, DEMO_CAPITAL));
    expect(customers).toHaveLength(4);
  });

  it('puts the four customers in the tiers the spec implies', () => {
    const tiers = Object.fromEntries(customers.map((c) => [c.id, computeTier(c)]));
    expect(tiers).toEqual({
      acct_northwind: 'A',
      acct_harbor: 'C',
      acct_lumen: 'B',
      acct_vertex: 'NONE',
    });
  });

  it('starts in the Tight regime, where every request cannot be funded', () => {
    expect(currentRegime(DEMO_CAPITAL)).toBe('TIGHT');

    const totalRequested = DEMO_REQUESTS.reduce((s, r) => s + r.principal, 0);
    const available =
      DEMO_CAPITAL.facility_size * (1 - DEMO_CAPITAL.reserve_floor_pct) - DEMO_CAPITAL.outstanding;
    expect(totalRequested).toBeGreaterThan(available);
  });

  it('gives the hard case a limit well below its payroll shortfall', () => {
    const harbor = customers.find((c) => c.id === 'acct_harbor')!;
    const payroll = DEMO_REQUESTS.find((r) => r.customer_id === 'acct_harbor')!;
    expect(customerLimit(harbor)).toBeLessThan(payroll.principal);
  });
});
