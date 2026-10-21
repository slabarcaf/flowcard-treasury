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
} from '../../src/kit5/engine.js';
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

  it('never processes the next request while one is waiting on a person', () => {
    let s = start(initialKit5State(scenario('original')));
    s = step(s); // Bloom → held for CRM
    expect(s.pause?.kind).toBe('crm');
    expect(step(s)).toBe(s);
  });
});

describe('Original scenario', () => {
  it('runs GD’s cast in arrival order on the 20% floor', () => {
    let deployableBeforeCollection = 0;
    const s = playAll(initialKit5State(scenario('original')), (x) => {
      if (nextEvent(x)?.type === 'repayment' && !x.pause) deployableBeforeCollection = x.deployableCapital;
    });
    expect(status(s, 'txn_bloom_payroll_001')).toBe('Declined');
    expect(s.customerDetails.txn_bloom_payroll_001.tierChange).toEqual({ from: 'A', to: 'C' });
    expect(status(s, 'txn_atlas_supplier_001')).toBe('Funded');
    expect(status(s, 'txn_nova_contractor_001')).toBe('Repaid');
    expect(status(s, 'txn_pixel_ads_001')).toBe('Ineligible');
    expect(deployableBeforeCollection).toBe(4_000);
    expect(s.deployableCapital).toBe(10_000);
    expect(s.customerDetails.txn_nova_contractor_001.collectionRows?.[0].amount).toBe(27);
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
    s = reEvaluateCrm(revealCrm(step(s))); // Bloom
    s = step(s); // Atlas
    expect(s.pause).toEqual({ kind: 'ack', requestId: 'txn_atlas_supplier_001' });
    expect(step(s)).toBe(s); // waits for the presenter
    s = step(acknowledge(s)); // Nova
    expect(s.pause).toEqual({ kind: 'human', requestId: 'txn_nova_contractor_001' });
    expect(s.customerDetails.txn_nova_contractor_001.approvalTerms?.capitalImpact).toContain('$10,000 → $4,000');
  });
});

describe('Kit 5 target scenario', () => {
  it('refuses Juniper at the floor after Nova used the capital', () => {
    const s = playAll(initialKit5State(scenario('kit5-target')));
    expect(status(s, 'txn_nova_contractor_001')).toBe('Repaid');
    expect(status(s, 'txn_juniper_contractor_001')).toBe('Declined');
    expect(s.customerDetails.txn_juniper_contractor_001.rationale).toContain('exceeds the $4,000 left above the floor');
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
