import { describe, it, expect, afterEach } from 'vitest';
import {
  CONFIG_FIELDS,
  isModified,
  modifiedCount,
  resetPolicyConfig,
  specDefault,
} from '../../../src/features/bridge-capital/backoffice/adminConfig.js';
import { DELINQUENCY, PRICING } from '../../../src/features/bridge-capital/policy/policy.config.js';
import { priceOffer } from '../../../src/features/bridge-capital/policy/pricing.js';

const field = (id: string) => {
  const f = CONFIG_FIELDS.find((x) => x.id === id);
  if (f === undefined) throw new Error(`no field ${id}`);
  return f;
};

// The panel writes into module-level config, so every test puts it back.
afterEach(() => resetPolicyConfig());

describe('admin thresholds', () => {
  it('starts at the spec values', () => {
    expect(modifiedCount()).toBe(0);
    expect(PRICING.serviceFee).toBe(50);
    expect(specDefault('pricing.serviceFee')).toBe(50);
  });

  it('changes what the engine charges', () => {
    // Spec §5: $10,000 over 60 days costs $450.
    expect(priceOffer(10_000, 60).fee).toBe(450);
    field('pricing.serviceFee').set(75);
    expect(priceOffer(10_000, 60).fee).toBe(475);
    expect(isModified(field('pricing.serviceFee'))).toBe(true);
  });

  it('keeps the late-repayment ladder contiguous', () => {
    field('dpd.watch').set(10);
    expect(DELINQUENCY.watch.to).toBe(10);
    expect(DELINQUENCY.delinquent.from).toBe(11);
  });

  it('puts everything back', () => {
    field('pricing.serviceFee').set(999);
    field('limits.A').set(0.9);
    field('dpd.watch').set(3);
    expect(modifiedCount()).toBe(3);

    resetPolicyConfig();

    expect(modifiedCount()).toBe(0);
    expect(priceOffer(10_000, 60).fee).toBe(450);
    expect(DELINQUENCY.delinquent.from).toBe(8);
  });
});
