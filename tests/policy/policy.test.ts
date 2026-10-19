import { describe, it, expect } from 'vitest';
import {
  assignTier,
  hasNoGo,
  computeCreditLine,
  lendable,
  reserveFloor,
  deployable,
  capitalLevel,
  toBand,
  getDoor,
  checkBridge,
  bridgeFee,
  bridgeFeeVariable,
} from '../../src/policy/index.js';
import type { FacilityConfig, Signals } from '../../src/policy/index.js';

/** Demo facility config from the doc. */
const FACILITY: FacilityConfig = {
  facilitySize: 50_000,
  reserveFloorPct: 0.05,
  maxPerCustomerPct: 0.20,
  feeRatePer30Days: 0.015,
  minHistoryDays: 90,
};

// ---------------------------------------------------------------------------
// Tier assignment
// ---------------------------------------------------------------------------
describe('assignTier', () => {
  it('all green → A', () => {
    const s: Signals = {
      inflowReliability: 'green',
      inflowConcentration: 'green',
      cashBuffer: 'green',
      trackRecord: 'green',
    };
    expect(assignTier(s)).toBe('A');
  });

  it('one yellow (cash buffer) → A (Bloom at start)', () => {
    const s: Signals = {
      inflowReliability: 'green',
      inflowConcentration: 'green',
      cashBuffer: 'yellow',
      trackRecord: 'green',
    };
    expect(assignTier(s)).toBe('A');
  });

  it('two yellows → B (Nova at start)', () => {
    const s: Signals = {
      inflowReliability: 'green',
      inflowConcentration: 'yellow',
      cashBuffer: 'yellow',
      trackRecord: 'green',
    };
    expect(assignTier(s)).toBe('B');
  });

  it('three yellows → C (Bloom after CRM note)', () => {
    const s: Signals = {
      inflowReliability: 'yellow',
      inflowConcentration: 'yellow',
      cashBuffer: 'yellow',
      trackRecord: 'green',
    };
    expect(assignTier(s)).toBe('C');
  });

  it('red on inflow reliability → none', () => {
    const s: Signals = {
      inflowReliability: 'red',
      inflowConcentration: 'green',
      cashBuffer: 'green',
      trackRecord: 'green',
    };
    expect(assignTier(s)).toBe('none');
  });

  it('one red on track record (not reliability) → C', () => {
    const s: Signals = {
      inflowReliability: 'green',
      inflowConcentration: 'green',
      cashBuffer: 'green',
      trackRecord: 'red',
    };
    expect(assignTier(s)).toBe('C');
  });
});

// ---------------------------------------------------------------------------
// No-go rules
// ---------------------------------------------------------------------------
describe('hasNoGo', () => {
  it('all false → no block', () => {
    expect(
      hasNoGo({
        excludedIndustry: false,
        fraudFlag: false,
        accountFrozen: false,
        pastWriteOff: false,
        monoClient: false,
      }),
    ).toBe(false);
  });

  it('any true → blocked', () => {
    expect(
      hasNoGo({
        excludedIndustry: false,
        fraudFlag: false,
        accountFrozen: false,
        pastWriteOff: false,
        monoClient: true,
      }),
    ).toBe(true);
  });
});

// ---------------------------------------------------------------------------
// Credit line calculation
// ---------------------------------------------------------------------------
describe('computeCreditLine', () => {
  it('Bloom: Tier A, $40k inflow → $10,000 (capped at 20% of $50k)', () => {
    // 40000 * 0.30 = 12000, cap = 50000 * 0.20 = 10000
    expect(computeCreditLine('A', 40_000, FACILITY)).toBe(10_000);
  });

  it('Atlas: Tier A, $20k inflow → $6,000', () => {
    // 20000 * 0.30 = 6000, cap = 10000
    expect(computeCreditLine('A', 20_000, FACILITY)).toBe(6_000);
  });

  it('Nova: Tier B, $35k inflow → $7,000', () => {
    // 35000 * 0.20 = 7000, cap = 10000
    expect(computeCreditLine('B', 35_000, FACILITY)).toBe(7_000);
  });

  it('Bloom after re-tier: Tier C, $40k inflow → $4,000', () => {
    // 40000 * 0.10 = 4000, cap = 10000
    expect(computeCreditLine('C', 40_000, FACILITY)).toBe(4_000);
  });

  it('Tier none → $0', () => {
    expect(computeCreditLine('none', 100_000, FACILITY)).toBe(0);
  });
});

// ---------------------------------------------------------------------------
// Facility calculations
// ---------------------------------------------------------------------------
describe('facility', () => {
  it('lendable = $47,500', () => {
    expect(lendable(FACILITY)).toBe(47_500);
  });

  it('reserve floor = $2,500', () => {
    expect(reserveFloor(FACILITY)).toBe(2_500);
  });

  it('deployable with $32,500 outstanding = $15,000', () => {
    expect(deployable(FACILITY, 32_500)).toBe(15_000);
  });

  it('deployable with $37,500 outstanding = $10,000 (after Atlas funded)', () => {
    expect(deployable(FACILITY, 37_500)).toBe(10_000);
  });

  it('deployable with $43,500 outstanding = $4,000 (after Nova funded)', () => {
    expect(deployable(FACILITY, 43_500)).toBe(4_000);
  });

  it('deployable never goes negative', () => {
    expect(deployable(FACILITY, 50_000)).toBe(0);
  });
});

// ---------------------------------------------------------------------------
// Capital bands
// ---------------------------------------------------------------------------
describe('toBand', () => {
  it('31.6% → less (start of demo)', () => {
    const level = 15_000 / 47_500; // ≈ 0.3158
    expect(toBand(level)).toBe('less');
  });

  it('21.1% → less (after Atlas funded)', () => {
    const level = 10_000 / 47_500; // ≈ 0.2105
    expect(toBand(level)).toBe('less');
  });

  it('8.4% → critical (after Nova funded)', () => {
    const level = 4_000 / 47_500; // ≈ 0.0842
    expect(toBand(level)).toBe('critical');
  });

  it('0 → at_floor', () => {
    expect(toBand(0)).toBe('at_floor');
  });

  it('75% → a_lot', () => {
    expect(toBand(0.75)).toBe('a_lot');
  });

  it('50% → normal', () => {
    expect(toBand(0.50)).toBe('normal');
  });

  it('40% → normal (inclusive boundary)', () => {
    expect(toBand(0.40)).toBe('normal');
  });

  it('10% → less (boundary)', () => {
    expect(toBand(0.10)).toBe('less');
  });
});

// ---------------------------------------------------------------------------
// Doors
// ---------------------------------------------------------------------------
describe('getDoor', () => {
  it('band less, tier A → auto', () => {
    expect(getDoor('less', 'A')).toBe('auto');
  });

  it('band less, tier B → human', () => {
    expect(getDoor('less', 'B')).toBe('human');
  });

  it('band less, tier C → closed', () => {
    expect(getDoor('less', 'C')).toBe('closed');
  });

  it('band critical, tier A → human', () => {
    expect(getDoor('critical', 'A')).toBe('human');
  });

  it('band critical, tier B → closed', () => {
    expect(getDoor('critical', 'B')).toBe('closed');
  });

  it('band a_lot, all tiers auto', () => {
    expect(getDoor('a_lot', 'A')).toBe('auto');
    expect(getDoor('a_lot', 'B')).toBe('auto');
    expect(getDoor('a_lot', 'C')).toBe('auto');
  });

  it('tier none → always closed', () => {
    expect(getDoor('a_lot', 'none')).toBe('closed');
    expect(getDoor('less', 'none')).toBe('closed');
  });
});

// ---------------------------------------------------------------------------
// Bridge decision
// ---------------------------------------------------------------------------
describe('checkBridge', () => {
  const baseParams = {
    facilityConfig: FACILITY,
    band: 'less' as const,
  };

  it('Atlas: Tier A, band less, $5k shortfall, $6k line → approved auto', () => {
    const result = checkBridge({
      ...baseParams,
      shortfall: 5_000,
      creditLine: 6_000,
      outstanding: 0,
      tier: 'A',
      deployableCapital: 15_000,
    });
    expect(result.approved).toBe(true);
    expect(result.door).toBe('auto');
    expect(result.allocation).toBe(5_000);
  });

  it('Bloom before CRM: Tier A, band less, $10k shortfall, $10k line → approved auto', () => {
    const result = checkBridge({
      ...baseParams,
      shortfall: 10_000,
      creditLine: 10_000,
      outstanding: 0,
      tier: 'A',
      deployableCapital: 15_000,
    });
    expect(result.approved).toBe(true);
    expect(result.door).toBe('auto');
    expect(result.allocation).toBe(10_000);
  });

  it('Bloom after CRM: Tier C, band less, door closed → rejected', () => {
    const result = checkBridge({
      ...baseParams,
      shortfall: 10_000,
      creditLine: 4_000,
      outstanding: 0,
      tier: 'C',
      deployableCapital: 10_000,
    });
    expect(result.approved).toBe(false);
    expect(result.door).toBe('closed');
    expect(result.reasons).toContain('Tier C door is Closed in band less');
  });

  it('Bloom after CRM: partial bridge reason included', () => {
    const result = checkBridge({
      ...baseParams,
      shortfall: 10_000,
      creditLine: 4_000,
      outstanding: 0,
      tier: 'C',
      deployableCapital: 10_000,
    });
    expect(result.reasons.some((r) => r.includes('does not cover'))).toBe(true);
  });

  it('Nova: Tier B, band less → human approval', () => {
    const result = checkBridge({
      ...baseParams,
      shortfall: 6_000,
      creditLine: 7_000,
      outstanding: 0,
      tier: 'B',
      deployableCapital: 10_000,
    });
    expect(result.approved).toBe(true);
    expect(result.door).toBe('human');
    expect(result.allocation).toBe(6_000);
  });

  it('insufficient deployable capital → rejected', () => {
    const result = checkBridge({
      ...baseParams,
      shortfall: 6_000,
      creditLine: 7_000,
      outstanding: 0,
      tier: 'A',
      deployableCapital: 3_000,
    });
    expect(result.approved).toBe(false);
    expect(result.reasons.some((r) => r.includes('insufficient'))).toBe(true);
  });

  it('outstanding reduces remaining line', () => {
    const result = checkBridge({
      ...baseParams,
      shortfall: 6_000,
      creditLine: 7_000,
      outstanding: 3_000,
      tier: 'A',
      deployableCapital: 15_000,
    });
    expect(result.approved).toBe(false);
    expect(result.reasons.some((r) => r.includes('exceeds remaining line'))).toBe(true);
  });

  it('tier none → always rejected', () => {
    const result = checkBridge({
      ...baseParams,
      shortfall: 1_000,
      creditLine: 0,
      outstanding: 0,
      tier: 'none',
      deployableCapital: 15_000,
    });
    expect(result.approved).toBe(false);
  });
});

// ---------------------------------------------------------------------------
// Fee calculation
// ---------------------------------------------------------------------------
describe('bridgeFee', () => {
  it('Nova: $6,000 for 9 days → $27.00', () => {
    expect(bridgeFee(6_000, 9)).toBe(27.0);
  });

  it('$5,000 for 30 days → $75.00', () => {
    expect(bridgeFee(5_000, 30)).toBe(75.0);
  });

  it('$5,000 for 1 day → $2.50', () => {
    expect(bridgeFee(5_000, 1)).toBe(2.5);
  });

  it('$0 principal → $0 fee', () => {
    expect(bridgeFee(0, 30)).toBe(0);
  });

  it('0 days → $0 fee', () => {
    expect(bridgeFee(5_000, 0)).toBe(0);
  });
});

describe('bridgeFeeVariable', () => {
  it('$5,000 for 10 days then $2,000 for 5 days → $30.00 (doc example)', () => {
    const balances = [
      ...Array(10).fill(5_000),
      ...Array(5).fill(2_000),
    ];
    expect(bridgeFeeVariable(balances)).toBe(30.0);
  });

  it('constant $6,000 for 9 days matches simple formula', () => {
    const balances = Array(9).fill(6_000);
    expect(bridgeFeeVariable(balances)).toBe(27.0);
  });
});

// ---------------------------------------------------------------------------
// End-to-end demo scenario
// ---------------------------------------------------------------------------
describe('demo scenario walkthrough', () => {
  it('start state: $32,500 outstanding → $15,000 deployable, band less', () => {
    const dep = deployable(FACILITY, 32_500);
    expect(dep).toBe(15_000);
    const level = capitalLevel(dep, lendable(FACILITY));
    expect(toBand(level)).toBe('less');
  });

  it('after Atlas $5k funded: $37,500 outstanding → $10,000 deployable, band less', () => {
    const dep = deployable(FACILITY, 37_500);
    expect(dep).toBe(10_000);
    const level = capitalLevel(dep, lendable(FACILITY));
    expect(toBand(level)).toBe('less');
  });

  it('after Nova $6k funded: $43,500 outstanding → $4,000 deployable, band critical', () => {
    const dep = deployable(FACILITY, 43_500);
    expect(dep).toBe(4_000);
    const level = capitalLevel(dep, lendable(FACILITY));
    expect(toBand(level)).toBe('critical');
  });

  it('after Nova repays $6k: $37,500 outstanding → $10,000 deployable, band less', () => {
    const dep = deployable(FACILITY, 37_500);
    expect(dep).toBe(10_000);
    const level = capitalLevel(dep, lendable(FACILITY));
    expect(toBand(level)).toBe('less');
  });
});
