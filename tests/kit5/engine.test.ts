import { describe, it, expect } from 'vitest';
import {
  initialKit5State,
  start,
  step,
  nextEvent,
  revealCrm,
  reEvaluateCrm,
  approve,
  acknowledge,
  evaluateKit5CustomerPolicy,
} from '../../src/kit5/engine.js';
import { evaluateDashboardCustomerPolicy, initialState, scanDeclines, processCustomer } from '../../src/state/machine.js';
import { CUSTOMERS } from '../../src/seed.js';
import type { DemoPolicyReadiness } from '../../src/policy/types.js';
import { SCENARIOS } from '../../src/kit5/scenarios.js';
import type { Kit5State } from '../../src/kit5/types.js';

function scenario(id: string) {
  const s = SCENARIOS.find((x) => x.id === id);
  if (!s) throw new Error(id);
  return s.inputs;
}

/** Plays a scenario to the end the way a presenter would: reveal CRM, approve every human door. */
function playAll(s: Kit5State, onEach?: (s: Kit5State) => void): Kit5State {
  s = start(s);
  for (let guard = 0; guard < 50; guard++) {
    if (s.pause?.kind === 'crm') s = reEvaluateCrm(revealCrm(s));
    else if (s.pause?.kind === 'human') s = approve(s, s.pause.requestId);
    else if (s.pause?.kind === 'ack') s = acknowledge(s);
    else if (nextEvent(s)) s = step(s);
    else return s;
    onEach?.(s);
  }
  throw new Error('scenario did not finish');
}

function status(s: Kit5State, requestId: string) {
  return s.queue.find((q) => q.id === requestId)?.status;
}

describe('Kit 5 facility', () => {
  it('starts at $15,000 deployable (37.5%, Less) with a $10,000 floor', () => {
    const s = initialKit5State(scenario('original'));
    expect(s.floor).toBe(10_000);
    expect(s.lentOut).toBe(25_000);
    expect(s.deployableCapital).toBe(15_000);
    expect(s.band).toBe('less');
  });

  it('blocks unresolved evidence and continues without creating a review action', () => {
    let s = start(initialKit5State(scenario('original')));
    s = step(s); // Bloom's evidence fixture is review-required.
    expect(status(s, 'txn_bloom_payroll_001')).toBe('Blocked');
    expect(s.customerDetails.txn_bloom_payroll_001.reasonCodes).toContain('EVIDENCE_REVIEW_REQUIRED');
    s = step(s); // Atlas can proceed independently.
    expect(status(s, 'txn_atlas_supplier_001')).toBe('Funded');
  });
});

describe('Original scenario', () => {
  it('moves deployable capital from $15K to $10K after Atlas, then to $4K after Nova', () => {
    let atlasDeployable: number | undefined;
    let novaDeployable: number | undefined;
    const s = playAll(initialKit5State(scenario('original')), (current) => {
      const atlas = current.queue.find((q) => q.id === 'txn_atlas_supplier_001');
      const nova = current.queue.find((q) => q.id === 'txn_nova_contractor_001');
      if (atlas?.status === 'Funded' && atlasDeployable === undefined) atlasDeployable = current.deployableCapital;
      if (nova?.status === 'Funded' && novaDeployable === undefined) novaDeployable = current.deployableCapital;
    });

    expect(atlasDeployable).toBe(10_000);
    expect(novaDeployable).toBe(4_000);
    expect(s.deployableCapital).toBe(10_000);
  });

  it('runs GD’s cast in arrival order on the 20% floor', () => {
    let deployableBeforeCollection = 0;
    const s = playAll(initialKit5State(scenario('original')), (x) => {
      if (nextEvent(x)?.type === 'repayment' && !x.pause) deployableBeforeCollection = x.deployableCapital;
    });
    expect(status(s, 'txn_bloom_payroll_001')).toBe('Blocked');
    expect(s.customerDetails.txn_bloom_payroll_001.reasonCodes).toContain('EVIDENCE_REVIEW_REQUIRED');
    expect(status(s, 'txn_atlas_supplier_001')).toBe('Funded');
    expect(status(s, 'txn_nova_contractor_001')).toBe('Repaid');
    expect(status(s, 'txn_pixel_ads_001')).toBe('Ineligible');
    expect(deployableBeforeCollection).toBe(4_000);
    expect(s.deployableCapital).toBe(10_000);
    expect(s.customerDetails.txn_nova_contractor_001.collectionRows?.[0].amount).toBe(27);
  });

  it('blocks Bloom for unresolved CRM evidence before re-evaluation or funding', () => {
    let s = start(initialKit5State(scenario('original')));
    s = step(s);

    expect(s.pause).toBeNull();
    expect(status(s, 'txn_bloom_payroll_001')).toBe('Blocked');
    expect(s.customerDetails.txn_bloom_payroll_001.reasonCodes).toContain('EVIDENCE_REVIEW_REQUIRED');
    expect(s.customerDetails.txn_bloom_payroll_001.bloomAgentPhase).toBeUndefined();
    expect(s.events[0]).toMatchObject({ paymentAmount: 9_000, walletBalance: 2_000 });
    expect(s.queue.find((row) => row.id === 'txn_bloom_payroll_001')?.shortfall).toBe(7_000);

    s = step(s);
    expect(status(s, 'txn_atlas_supplier_001')).toBe('Funded');
    expect(status(s, 'txn_bloom_payroll_001')).toBe('Blocked');
  });

  it('dates every bridge: funded day, collect-by (30 days), repaid day', () => {
    const s = playAll(initialKit5State(scenario('original')));
    const atlas = s.queue.find((q) => q.id === 'txn_atlas_supplier_001')!;
    const nova = s.queue.find((q) => q.id === 'txn_nova_contractor_001')!;
    expect(atlas).toMatchObject({ fundedOn: 'Oct 19', dueOn: 'Nov 18' });
    expect(atlas.repaidOn).toBeUndefined();
    expect(nova).toMatchObject({ fundedOn: 'Oct 19', dueOn: 'Nov 18', repaidOn: 'Oct 28' });
    expect(s.bridges.txn_atlas_supplier_001.dueDay).toBe(30);
  });

  it('explains the 90-day history gate for Pixel', () => {
    const s = playAll(initialKit5State(scenario('original')));
    expect(s.queue.find((q) => q.id === 'txn_pixel_ads_001')?.ineligibleReason).toBe(
      'No line yet: 41 days of history, 90 required (eligible in 49 days)',
    );
  });

  it('sends Nova to a credit officer (Tier B at Less)', () => {
    let s = start(initialKit5State(scenario('original')));
    s = step(s); // Bloom is blocked by unresolved evidence.
    expect(status(s, 'txn_bloom_payroll_001')).toBe('Blocked');
    s = step(s); // Atlas
    expect(s.pause).toEqual({ kind: 'ack', requestId: 'txn_atlas_supplier_001' });
    expect(step(s)).toBe(s); // waits for the presenter
    s = step(acknowledge(s)); // Nova
    expect(s.pause).toEqual({ kind: 'human', requestId: 'txn_nova_contractor_001' });
    expect(s.customerDetails.txn_nova_contractor_001.approvalTerms).toMatchObject({
      amount: 6_000, creditLine: 7_000, door: 'human',
      reviewRequirement: 'Salesforce credit review is on watch.',
    });
    expect(s.customerDetails.txn_nova_contractor_001.approvalTerms?.capitalImpact).toContain('$10,000 → $4,000');
  });
});

describe('GD demo snapshot and evidence gates', () => {
  it('blocks Bloom on unresolved fixture evidence and requires exact-term review for Nova watch', () => {
    let state = scanDeclines(initialState());
    state = processCustomer(state, 'acct_bloom001');
    expect(state.queue.find((row) => row.customerId === 'acct_bloom001')?.status).toBe('Blocked');
    expect(state.customerDetails.acct_bloom001.reasonCodes).toContain('EVIDENCE_REVIEW_REQUIRED');

    state = processCustomer(state, 'acct_atlas001');
    state = processCustomer(state, 'acct_nova001');
    expect(state.queue.find((row) => row.customerId === 'acct_nova001')?.status).toBe('Pending approval');
    expect(state.customerDetails.acct_nova001.approvalTerms).toMatchObject({
      amount: 6_000, creditLine: 7_000, door: 'human',
      reviewRequirement: 'Salesforce credit review is on watch; a human decision is required.',
    });
  });
});

describe('Kit 5 target scenario', () => {
  it('keeps Bloom at a $9,000 payment, $2,000 opening balance, and $7,000 shortfall', () => {
    const s = initialKit5State(scenario('kit5-target'));
    const bloom = s.events.find((event) => event.type === 'decline' && event.customerKey === 'bloom');

    expect(bloom).toMatchObject({ paymentAmount: 9_000, walletBalance: 2_000 });
    if (bloom?.type === 'decline') expect(bloom.paymentAmount - bloom.walletBalance).toBe(7_000);
  });

  it('refuses Juniper at the floor after Nova used the capital', () => {
    const s = playAll(initialKit5State(scenario('kit5-target')));
    expect(status(s, 'txn_nova_contractor_001')).toBe('Repaid');
    expect(status(s, 'txn_juniper_contractor_001')).toBe('Declined');
    expect(s.customerDetails.txn_juniper_contractor_001.rationale).toContain('exceeds the $4,000 left above the floor');
    expect(s.customerDetails.txn_juniper_contractor_001.policyChecks?.some((check) => !check.passed)).toBe(true);
  });
});

describe('First come, first served', () => {
  it('funds the first identical request, refuses the second, and funds its retry after repayment', () => {
    const s = playAll(initialKit5State(scenario('first-come')));
    expect(status(s, 'txn_cedar_supplier_001')).toBe('Repaid');
    expect(status(s, 'txn_maple_supplier_001')).toBe('Declined');
    expect(status(s, 'txn_maple_supplier_002')).toBe('Funded');
    expect(s.deployableCapital).toBe(7_000);
  });

  it('flips the outcome when the arrival order flips', () => {
    const inputs = structuredClone(scenario('first-come'));
    inputs.events[0].time = '9:10'; // Cedar now arrives after Maple
    const s = playAll(initialKit5State(inputs));
    expect(status(s, 'txn_maple_supplier_001')).toBe('Funded');
    expect(status(s, 'txn_cedar_supplier_001')).toBe('Declined');
  });
});

describe('Tight capital → human', () => {
  it('auto-funds the first request and escalates the identical second one', () => {
    let s = start(initialKit5State(scenario('tight-escalates')));
    s = step(s);
    expect(status(s, 'txn_orchid_contractor_001')).toBe('Funded');
    expect(s.band).toBe('critical');
    s = step(acknowledge(s));
    expect(s.pause).toEqual({ kind: 'human', requestId: 'txn_willow_contractor_001' });
    s = approve(s, 'txn_willow_contractor_001');
    expect(status(s, 'txn_willow_contractor_001')).toBe('Funded');
    expect(s.deployableCapital).toBe(500);
  });
});

describe('shared demo decision fixtures', () => {
  const profile = { ...CUSTOMERS.atlas, signals: { ...CUSTOMERS.atlas.signals } };
  const facility = { facilitySize: 50_000, reserveFloorPct: 0.2, maxPerCustomerPct: 0.2, feeRatePer30Days: 0.015, minHistoryDays: 90, approvedBridgeCap: 8_000 };
  const base = {
    profile,
    facility,
    shortfall: 3_000,
    outstanding: 0,
    band: 'a_lot' as const,
    deployableCapital: 40_000,
  };
  const readiness = (overrides: Partial<DemoPolicyReadiness> = {}): DemoPolicyReadiness => ({
    snapshot: 'fresh', evidence: 'no_signal', creditReview: 'approved', priorDefault: 'no', ...overrides,
  });

  it.each([
    [0, 2_000], [2, 2_000], [3, 6_000],
  ])('applies %i on-time repayments through the shared multiplier in both engines', (repayments, expectedLine) => {
    const input = { ...base, profile: { ...profile, fullyRepaidOnTimeBridges: repayments }, readiness: readiness() };
    const dashboard = evaluateDashboardCustomerPolicy(input);
    const kit5 = evaluateKit5CustomerPolicy(input);
    expect(dashboard).toEqual(kit5);
    expect(dashboard.creditLine).toBe(expectedLine);
  });

  it.each([undefined, -1, 1.5])('blocks both engines when profile repayment history is missing or invalid: %s', (repayments) => {
    const input = { ...base, profile: { ...profile, fullyRepaidOnTimeBridges: repayments }, readiness: readiness() };
    const dashboard = evaluateDashboardCustomerPolicy(input);
    const kit5 = evaluateKit5CustomerPolicy(input);
    expect(dashboard).toEqual(kit5);
    expect(dashboard).toMatchObject({ status: 'blocked', tier: 'none', creditLine: 0, reasonCodes: ['REPAYMENT_HISTORY_UNKNOWN'] });
  });

  it.each([
    ['stale snapshot', readiness({ snapshot: 'stale' }), 'blocked', 'SNAPSHOT_STALE'],
    ['review-required evidence', readiness({ evidence: 'review_required' }), 'blocked', 'EVIDENCE_REVIEW_REQUIRED'],
    ['valid no-signal evidence', readiness(), 'approved', undefined],
    ['watch status', readiness({ creditReview: 'watch' }), 'human_review', 'CREDIT_REVIEW_WATCH'],
    ['prior default', readiness({ priorDefault: 'yes' }), 'ineligible', 'PRIOR_DEFAULT'],
    ['capital exhaustion', readiness(), 'declined', 'INSUFFICIENT_DEPLOYABLE_CAPITAL'],
  ] as const)('%s produces the same deterministic result in both demo engines', (_name, gate, expectedStatus, reasonCode) => {
    const capitalCase = _name === 'capital exhaustion'
      ? { band: 'at_floor' as const, deployableCapital: 0 }
      : { band: base.band, deployableCapital: base.deployableCapital };
    const input = { ...base, ...capitalCase, readiness: gate };
    const dashboardResult = evaluateDashboardCustomerPolicy(input);
    const kit5Result = evaluateKit5CustomerPolicy(input);
    expect(dashboardResult).toEqual(kit5Result);
    expect(dashboardResult.status).toBe(expectedStatus);
    if (reasonCode) expect(dashboardResult.reasonCodes).toContain(reasonCode);
  });
});
