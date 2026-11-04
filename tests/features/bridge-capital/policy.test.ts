/**
 * Bridge Capital policy engine, against docs/bridge-capital-spec.md §3–§7.
 *
 * Customers here are built by hand so each test pins the one statistic it is
 * about; the generator has its own coverage in customerStats.report.test.ts.
 */
import { describe, it, expect } from 'vitest';
import {
  applyDelinquency,
  availableAboveFloor,
  collect,
  computeTier,
  criticalityForCategory,
  currentRegime,
  customerLimit,
  downgradeTier,
  evaluate,
  isAcceptanceValid,
  isApprovalValid,
  lendable,
  priceOffer,
  rankRequests,
  addDays,
} from '../../../src/features/bridge-capital/policy/index.js';
import type {
  Acceptance,
  Approval,
  Bridge,
  BridgeOffer,
  BridgeRequest,
  CardTransaction,
  Customer,
  PastBridge,
  PlatformCapital,
  SpendCriticality,
} from '../../../src/features/bridge-capital/types.js';

const TODAY = '2026-11-03';

// ---------------------------------------------------------------------------
// Builders
// ---------------------------------------------------------------------------

interface CustomerOpts {
  id?: string;
  tenure_months?: number;
  balance_days?: number;
  monthly_inflows?: number;
  days_near_zero_90d?: number;
  bridges?: PastBridge[];
  receivable_due_in_days?: number | null;
  on_time_rate?: number;
  relationship_months?: number;
}

function customer(opts: CustomerOpts = {}): Customer {
  const {
    id = 'acct_test',
    tenure_months = 24,
    balance_days = 180,
    monthly_inflows = 100_000,
    days_near_zero_90d = 0,
    bridges = [],
    receivable_due_in_days = 10,
    on_time_rate = 0.95,
    relationship_months = 18,
  } = opts;

  return {
    id,
    name: id,
    tenure_months,
    balance_history: Array.from({ length: balance_days }, (_, i) => ({
      date: addDays(TODAY, -(balance_days - 1 - i)),
      balance: 30_000,
    })),
    invoices: [],
    expected_receivables:
      receivable_due_in_days === null
        ? []
        : [
            {
              payer_name: 'Test Payer',
              amount: 50_000,
              due_date: addDays(TODAY, receivable_due_in_days),
              payer_on_time_rate: on_time_rate,
              payer_avg_days_late: 2,
              relationship_months,
            },
          ],
    bridge_history: bridges,
    balance_stats: {
      avg_balance_30d: 30_000,
      avg_balance_90d: 30_000,
      avg_balance_180d: 30_000,
      balance_trend: 'FLAT',
      low_point_balance_90d: 20_000,
      days_near_zero_90d,
    },
    inflow_stats: { monthly_inflows, top_payer_concentration: 0.4 },
    bridge_stats: {
      bridges_count: bridges.length,
      avg_days_outstanding: 25,
      on_time_count: bridges.filter((b) => b.outcome === 'ON_TIME').length,
      late_count: bridges.filter((b) => b.outcome === 'LATE').length,
      open_bridge: bridges.some((b) => b.outcome === 'OPEN'),
      open_bridge_past_due: false,
    },
    crm: {
      onboarding_date: '2024-01-01',
      contract_status: 'Active',
      support_notes: [],
    },
  };
}

function pastBridge(over: Partial<PastBridge> = {}): PastBridge {
  return {
    id: 'br_1',
    transaction_id: 'txn_old',
    amount: 5_000,
    term_days: 30,
    opened_at: addDays(TODAY, -60),
    closed_at: addDays(TODAY, -30),
    days_outstanding: 30,
    outcome: 'ON_TIME',
    outstanding_balance: 0,
    due_date: addDays(TODAY, -30),
    ...over,
  };
}

function transaction(over: Partial<CardTransaction> = {}): CardTransaction {
  return {
    id: 'txn_1',
    customer_id: 'acct_test',
    card_id: 'card_1',
    amount: 5_000,
    currency: 'USD',
    merchant_name: 'Test Merchant',
    merchant_category: 'suppliers',
    criticality: 'CRITICAL',
    status: 'DECLINED',
    decline_reason: 'INSUFFICIENT_FUNDS',
    wallet_balance: 0,
    created_at: `${TODAY}T09:00:00Z`,
    ...over,
  };
}

function request(over: Partial<BridgeRequest> = {}): BridgeRequest {
  const txn = over.transaction ?? transaction();
  return {
    id: 'req_1',
    customer_id: txn.customer_id,
    principal: 5_000,
    term_days: 30,
    requested_at: TODAY,
    ...over,
    transaction: txn,
  };
}

/**
 * Capital whose available-above-floor is `available`.
 *
 * The facility size matters: regimes are a share of it, so a small facility is
 * how a scenario stays in Tight while only having room for one more bridge.
 */
function capitalWith(available: number, floorPct = 0.20, facility = 100_000): PlatformCapital {
  return {
    facility_size: facility,
    reserve_floor_pct: floorPct,
    outstanding: facility * (1 - floorPct) - available,
  };
}

function bridge(over: Partial<Bridge> = {}): Bridge {
  return {
    id: 'bridge_1',
    customer_id: 'acct_test',
    transaction_id: 'txn_1',
    principal: 10_000,
    fee: 450,
    term_days: 60,
    status: 'DISBURSED',
    disbursed_at: addDays(TODAY, -60),
    due_date: TODAY,
    principal_outstanding: 10_000,
    fee_outstanding: 450,
    accrued_fee: 0,
    ...over,
  };
}

function offer(over: Partial<BridgeOffer> = {}): BridgeOffer {
  return {
    id: 'offer_1',
    customer_id: 'acct_test',
    transaction_id: 'txn_1',
    principal: 10_000,
    term_days: 60,
    fee: 450,
    offered_at: TODAY,
    due_date: addDays(TODAY, 60),
    ...over,
  };
}

// ---------------------------------------------------------------------------
// §3 — no history
// ---------------------------------------------------------------------------
describe('a customer with no history is always declined', () => {
  const shortTenure = customer({ id: 'acct_new', tenure_months: 2, balance_days: 180 });
  const shortData = customer({ id: 'acct_thin', tenure_months: 24, balance_days: 60 });

  it('has no tier and no limit', () => {
    expect(computeTier(shortTenure)).toBe('NONE');
    expect(computeTier(shortData)).toBe('NONE');
    expect(customerLimit(shortTenure)).toBe(0);
    expect(customerLimit(shortData)).toBe(0);
  });

  it('is declined in every regime, at any amount', () => {
    const regimes: PlatformCapital[] = [
      capitalWith(70_000), // Abundant
      capitalWith(40_000), // Normal
      capitalWith(20_000), // Tight
      capitalWith(5_000), // Critical
      capitalWith(0), // At floor
    ];

    for (const capital of regimes) {
      for (const principal of [100, 1_000, 25_000]) {
        for (const c of [shortTenure, shortData]) {
          const decision = evaluate(
            request({
              principal,
              transaction: transaction({ customer_id: c.id }),
            }),
            c,
            capital,
            { today: TODAY },
          );
          expect(decision.eligible).toBe(false);
          expect(decision.max_bridgeable).toBe(0);
          expect(decision.rules.find((r) => r.rule_id === 'R1_HAS_HISTORY')?.passed).toBe(false);
        }
      }
    }
  });

  it('says which half of the history rule it failed', () => {
    const tenureReason = evaluate(request({ transaction: transaction({ customer_id: 'acct_new' }) }), shortTenure, capitalWith(70_000), { today: TODAY });
    expect(tenureReason.reasons[0]).toContain('Tenure 2 months');

    const dataReason = evaluate(request({ transaction: transaction({ customer_id: 'acct_thin' }) }), shortData, capitalWith(70_000), { today: TODAY });
    expect(dataReason.reasons[0]).toContain('60 days of balance data');
  });
});

// ---------------------------------------------------------------------------
// §5 — pricing
// ---------------------------------------------------------------------------
describe('pricing', () => {
  it('charges $450 for $10,000 over 60 days', () => {
    const price = priceOffer(10_000, 60);
    expect(price.fee).toBe(450);
    expect(price.total).toBe(10_450);
  });

  it('is $50 plus 2% a month, prorated daily', () => {
    expect(priceOffer(10_000, 30).fee).toBe(250);
    expect(priceOffer(10_000, 90).fee).toBe(650);
    expect(priceOffer(10_000, 15).fee).toBe(150);
    expect(priceOffer(5_000, 7).fee).toBe(73.33);
  });

  it('rejects terms outside 7–90 days', () => {
    expect(() => priceOffer(10_000, 6)).toThrow(RangeError);
    expect(() => priceOffer(10_000, 91)).toThrow(RangeError);
  });

  it('still charges the full term when the bridge is repaid early', () => {
    const b = bridge({ principal: 10_000, fee: 450, term_days: 60 });

    // Day 5 of a 60-day term: the fee is unchanged, so principal alone is short.
    const principalOnly = collect(b, 10_000);
    expect(principalOnly.fully_repaid).toBe(false);
    expect(principalOnly.remaining).toBe(450);

    // Paying what 5 days would be worth if the fee were prorated is not enough.
    const proratedFiveDays = priceOffer(10_000, 7).fee;
    const underpaid = collect(b, 10_000 + proratedFiveDays);
    expect(underpaid.fully_repaid).toBe(false);

    // Only the full term's fee closes it.
    const full = collect(b, 10_450);
    expect(full.fully_repaid).toBe(true);
    expect(full.applied_to_fee).toBe(450);
    expect(full.bridge.status).toBe('REPAID');
  });
});

// ---------------------------------------------------------------------------
// §3 — tiers and limits
// ---------------------------------------------------------------------------
describe('tiers and limits', () => {
  const tierA = customer({ tenure_months: 18, days_near_zero_90d: 3, bridges: [pastBridge()] });
  const tierB = customer({ tenure_months: 8, bridges: [pastBridge({ outcome: 'LATE', closed_at: addDays(TODAY, -40) })] });
  const tierC = customer({
    tenure_months: 18,
    bridges: [
      pastBridge({ id: 'br_def', outcome: 'DEFAULTED', closed_at: addDays(TODAY, -500) }),
      pastBridge({ id: 'br_late', outcome: 'LATE', closed_at: addDays(TODAY, -40) }),
    ],
  });

  it('assigns A, B and C from tenure, lateness and near-zero days', () => {
    expect(computeTier(tierA)).toBe('A');
    expect(computeTier(tierB)).toBe('B');
    expect(computeTier(tierC)).toBe('C');
  });

  it('sets the limit at 30 / 20 / 10% of monthly inflows', () => {
    expect(customerLimit(tierA)).toBe(30_000);
    expect(customerLimit(tierB)).toBe(20_000);
    expect(customerLimit(tierC)).toBe(10_000);
  });

  it('drops a customer out of tier A once near-zero days pass 5', () => {
    expect(computeTier(customer({ tenure_months: 18, days_near_zero_90d: 6 }))).toBe('B');
  });

  it('makes a default inside 12 months ineligible outright', () => {
    const recentDefault = customer({
      tenure_months: 24,
      bridges: [pastBridge({ outcome: 'DEFAULTED', closed_at: addDays(TODAY, -100) })],
    });
    expect(computeTier(recentDefault)).toBe('NONE');
  });

  it('counts the existing outstanding balance against the limit', () => {
    // Tier A, $30,000 limit, $26,000 already outstanding on an open bridge.
    const withOpen = customer({
      tenure_months: 18,
      bridges: [
        pastBridge({
          id: 'br_open',
          outcome: 'OPEN',
          closed_at: null,
          amount: 26_000,
          outstanding_balance: 26_000,
          due_date: addDays(TODAY, 10),
        }),
      ],
    });

    const fits = evaluate(request({ principal: 4_000 }), withOpen, capitalWith(50_000), { today: TODAY });
    expect(fits.outstanding).toBe(26_000);
    expect(fits.rules.find((r) => r.rule_id === 'R3_WITHIN_LIMIT')?.passed).toBe(true);

    const doesNot = evaluate(request({ principal: 4_001 }), withOpen, capitalWith(50_000), { today: TODAY });
    expect(doesNot.rules.find((r) => r.rule_id === 'R3_WITHIN_LIMIT')?.passed).toBe(false);
    expect(doesNot.max_bridgeable).toBe(4_000);

    // Without the open bridge the same request has the full limit to work with.
    const clean = evaluate(request({ principal: 4_001 }), customer({ tenure_months: 18 }), capitalWith(50_000), { today: TODAY });
    expect(clean.rules.find((r) => r.rule_id === 'R3_WITHIN_LIMIT')?.passed).toBe(true);
  });
});

// ---------------------------------------------------------------------------
// §6 — regimes and ranking
// ---------------------------------------------------------------------------
describe('capital regimes', () => {
  it('maps available-above-floor as a share of the facility', () => {
    expect(currentRegime(capitalWith(70_000))).toBe('ABUNDANT');
    expect(currentRegime(capitalWith(45_000))).toBe('NORMAL');
    expect(currentRegime(capitalWith(25_000))).toBe('TIGHT');
    expect(currentRegime(capitalWith(5_000))).toBe('CRITICAL');
    expect(currentRegime(capitalWith(0))).toBe('AT_FLOOR');
  });

  it('maps merchant categories to criticality', () => {
    expect(criticalityForCategory('payroll')).toBe('CRITICAL');
    expect(criticalityForCategory('travel')).toBe('DISCRETIONARY');
    expect(criticalityForCategory('saas')).toBe('DISCRETIONARY');
    expect(criticalityForCategory('something-else')).toBe('STANDARD');
  });
});

describe('in the Tight regime the discretionary request loses to critical ones', () => {
  const critical = customer({ id: 'acct_crit', tenure_months: 18 });
  const discretionary = customer({ id: 'acct_disc', tenure_months: 18 });

  function reqFor(c: Customer, criticality: SpendCriticality, category: string): BridgeRequest {
    return request({
      id: `req_${c.id}`,
      principal: 4_000,
      transaction: transaction({
        id: `txn_${c.id}`,
        customer_id: c.id,
        criticality,
        merchant_category: category,
      }),
    });
  }

  it('refuses the discretionary one on the criticality rule', () => {
    const capital = capitalWith(20_000); // Tight
    const decision = evaluate(
      reqFor(discretionary, 'DISCRETIONARY', 'travel'),
      discretionary,
      capital,
      { today: TODAY },
    );
    expect(currentRegime(capital)).toBe('TIGHT');
    expect(decision.eligible).toBe(false);
    expect(decision.rules.find((r) => r.rule_id === 'R4_CRITICALITY_ALLOWED')?.passed).toBe(false);
  });

  it('funds the critical one and not the discretionary one when only one fits', () => {
    // Both are tier A with identical receivables, so criticality decides the order.
    // A $20,000 facility keeps $4,000 available inside Tight (20% of the facility)
    // while leaving room for only one of the two $4,000 requests.
    const capital = capitalWith(4_000, 0.20, 20_000);
    expect(currentRegime(capital)).toBe('TIGHT');
    const ranked = rankRequests(
      [
        reqFor(discretionary, 'DISCRETIONARY', 'travel'),
        reqFor(critical, 'CRITICAL', 'payroll'),
      ],
      [critical, discretionary],
      capital,
    );

    expect(ranked[0].request.customer_id).toBe('acct_crit');
    expect(ranked[0].funded).toBe(true);
    const disc = ranked.find((r) => r.request.customer_id === 'acct_disc')!;
    expect(disc.funded).toBe(false);
  });

  it('ranks the demo set so only the critical tier A request is funded', async () => {
    const { DEMO_BLUEPRINTS, DEMO_CAPITAL, DEMO_REQUESTS, AS_OF, generateCustomers } = await import(
      '../../../src/features/bridge-capital/index.js'
    );
    const customers = generateCustomers(DEMO_BLUEPRINTS, { as_of: AS_OF });
    const ranked = rankRequests(DEMO_REQUESTS, customers, DEMO_CAPITAL);

    const funded = ranked.filter((r) => r.funded).map((r) => r.request.customer_id);
    expect(funded).toEqual(['acct_northwind']);

    const lumen = ranked.find((r) => r.request.customer_id === 'acct_lumen')!;
    expect(lumen.decision.rules.find((r) => r.rule_id === 'R4_CRITICALITY_ALLOWED')?.passed).toBe(false);
  });
});

describe('the reserve floor is never breached', () => {
  it('never allocates more than the capital above the floor', () => {
    const capital = capitalWith(10_000);
    const available = availableAboveFloor(capital);

    const customers = [1, 2, 3, 4, 5].map((n) =>
      customer({ id: `acct_${n}`, tenure_months: 18, monthly_inflows: 100_000 }),
    );
    const requests = customers.map((c, i) =>
      request({
        id: `req_${i}`,
        principal: 4_000,
        transaction: transaction({ id: `txn_${i}`, customer_id: c.id, criticality: 'CRITICAL' }),
      }),
    );

    const ranked = rankRequests(requests, customers, capital);
    const allocated = ranked.reduce((s, r) => s + r.allocated, 0);

    expect(allocated).toBeLessThanOrEqual(available);
    expect(capital.outstanding + allocated).toBeLessThanOrEqual(lendable(capital));

    // Some are funded, some are not, and everything refused says why.
    expect(ranked.some((r) => r.funded)).toBe(true);
    expect(ranked.some((r) => !r.funded)).toBe(true);
    for (const r of ranked.filter((x) => !x.funded)) {
      expect(r.reason).not.toBe('');
    }
  });

  it('hands later requests to a person once lending drops the regime to Critical', () => {
    // $10,000 available on a $100,000 facility is Tight. Funding one $4,000
    // bridge leaves $6,000 — under 10% of the facility — which is Critical,
    // where every bridge needs approval. So the floor is protected by the
    // regime ladder before the floor rule itself has to bind.
    const capital = capitalWith(10_000);
    const customers = [1, 2].map((n) =>
      customer({ id: `acct_${n}`, tenure_months: 18, monthly_inflows: 100_000 }),
    );
    const requests = customers.map((c, i) =>
      request({
        id: `req_${i}`,
        principal: 4_000,
        transaction: transaction({ id: `txn_${i}`, customer_id: c.id, criticality: 'CRITICAL' }),
      }),
    );

    const ranked = rankRequests(requests, customers, capital);
    expect(ranked[0].decision.regime).toBe('TIGHT');
    expect(ranked[0].funded).toBe(true);
    expect(ranked[1].decision.regime).toBe('CRITICAL');
    expect(ranked[1].decision.needs_human_approval).toBe(true);
    expect(ranked[1].funded).toBe(false);
  });

  it('allows a request exactly equal to the capital above the floor, and not a cent more', () => {
    const capital = capitalWith(5_000, 0.20, 25_000); // 20% of the facility → Tight
    const c = customer({ tenure_months: 18 });

    const exact = evaluate(request({ principal: 5_000 }), c, capital, { today: TODAY });
    expect(exact.rules.find((r) => r.rule_id === 'R5_RESERVE_FLOOR')?.passed).toBe(true);

    const overBy = evaluate(request({ principal: 5_000.01 }), c, capital, { today: TODAY });
    expect(overBy.rules.find((r) => r.rule_id === 'R5_RESERVE_FLOOR')?.passed).toBe(false);
  });

  it('refuses a single request larger than the capital above the floor', () => {
    const capital = capitalWith(3_000);
    const decision = evaluate(request({ principal: 5_000 }), customer({ tenure_months: 18 }), capital, {
      today: TODAY,
    });
    expect(decision.rules.find((r) => r.rule_id === 'R5_RESERVE_FLOOR')?.passed).toBe(false);
    expect(decision.max_bridgeable).toBeLessThanOrEqual(availableAboveFloor(capital));
  });

  it('funds nothing at the floor', () => {
    const ranked = rankRequests(
      [request({ principal: 1_000 })],
      [customer({ tenure_months: 18 })],
      capitalWith(0),
    );
    expect(ranked[0].funded).toBe(false);
    expect(ranked[0].decision.max_bridgeable).toBe(0);
  });
});

// ---------------------------------------------------------------------------
// §4 rule 6 — duplicates
// ---------------------------------------------------------------------------
describe('duplicate requests', () => {
  const c = customer({ tenure_months: 18 });
  const capital = capitalWith(50_000);

  it('returns the decision already taken for that transaction', () => {
    const first = evaluate(request(), c, capital, { today: TODAY });
    expect(first.eligible).toBe(true);

    // Same transaction, a different request id, and capital that has since moved.
    const second = evaluate(
      request({ id: 'req_retry' }),
      c,
      capitalWith(1_000),
      { today: TODAY, priorDecisions: [first] },
    );

    expect(second.duplicate_of).toBe(first.request_id);
    expect(second.eligible).toBe(first.eligible);
    expect(second.max_bridgeable).toBe(first.max_bridgeable);
    expect(second.regime).toBe(first.regime);
    expect(second.rules).toEqual(first.rules);
  });

  it('fails rule 6 when the transaction already has a bridge on the record', () => {
    const withBridge = customer({
      tenure_months: 18,
      bridges: [pastBridge({ id: 'br_dup', transaction_id: 'txn_1' })],
    });
    const decision = evaluate(request(), withBridge, capital, { today: TODAY });
    expect(decision.rules.find((r) => r.rule_id === 'R6_NO_DUPLICATE')?.passed).toBe(false);
    expect(decision.eligible).toBe(false);
  });
});

// ---------------------------------------------------------------------------
// §4 rule 2 and rule 7
// ---------------------------------------------------------------------------
describe('past-due and receivable rules', () => {
  it('refuses a customer with an open bridge past due', () => {
    const c = customer({
      tenure_months: 18,
      bridges: [
        pastBridge({
          id: 'br_late_open',
          outcome: 'OPEN',
          closed_at: null,
          outstanding_balance: 1_000,
          due_date: addDays(TODAY, -3),
        }),
      ],
    });
    const decision = evaluate(request(), c, capitalWith(50_000), { today: TODAY });
    expect(decision.rules.find((r) => r.rule_id === 'R2_NO_PAST_DUE')?.passed).toBe(false);
  });

  it('needs a receivable due before the term ends', () => {
    const noneDue = customer({ tenure_months: 18, receivable_due_in_days: 45 });
    const decision = evaluate(request({ term_days: 30 }), noneDue, capitalWith(50_000), { today: TODAY });
    expect(decision.rules.find((r) => r.rule_id === 'R7_RECEIVABLE_IN_TERM')?.passed).toBe(false);

    const longer = evaluate(request({ term_days: 60 }), noneDue, capitalWith(50_000), { today: TODAY });
    expect(longer.rules.find((r) => r.rule_id === 'R7_RECEIVABLE_IN_TERM')?.passed).toBe(true);
  });
});

// ---------------------------------------------------------------------------
// §5 — the late ladder
// ---------------------------------------------------------------------------
describe('a bridge 10 days past due', () => {
  const tenDaysLate = bridge({ due_date: addDays(TODAY, -10), status: 'COLLECTING' });
  const result = applyDelinquency(tenDaysLate, TODAY);

  it('is DELINQUENT', () => {
    expect(result.days_past_due).toBe(10);
    expect(result.status).toBe('DELINQUENT');
  });

  it('restricts the card to critical spend', () => {
    expect(result.actions).toContain('RESTRICT_CARD_TO_CRITICAL');
    expect(result.actions).toContain('NOTIFY_OPS');
  });

  it('drops the tier one level', () => {
    expect(result.tier_downgrade_levels).toBe(1);
    expect(result.actions).toContain('DOWNGRADE_TIER');
    expect(downgradeTier('A')).toBe('B');
    expect(downgradeTier('B')).toBe('C');
    expect(downgradeTier('C')).toBe('NONE');
  });

  it('keeps accruing at the monthly rate', () => {
    // $10,000 x 2% x 10/30
    expect(result.accrued_fee).toBe(66.67);
  });

  it('walks the rest of the ladder', () => {
    expect(applyDelinquency(bridge({ due_date: addDays(TODAY, -3) }), TODAY).status).toBe('WATCH');
    expect(applyDelinquency(bridge({ due_date: addDays(TODAY, -40) }), TODAY).status).toBe('SERIOUS');
    expect(applyDelinquency(bridge({ due_date: addDays(TODAY, -40) }), TODAY).actions).toContain('FREEZE_CARD');

    const defaulted = applyDelinquency(bridge({ due_date: addDays(TODAY, -75) }), TODAY);
    expect(defaulted.status).toBe('DEFAULTED');
    expect(defaulted.actions).toContain('RECORD_DEFAULT');
    expect(defaulted.ineligible_until).toBe(addDays(TODAY, 360));
  });

  it('leaves a bridge inside its term alone', () => {
    const current = applyDelinquency(bridge({ due_date: addDays(TODAY, 5) }), TODAY);
    expect(current.days_past_due).toBe(0);
    expect(current.actions).toEqual([]);
  });
});

// ---------------------------------------------------------------------------
// §5 and §7 — acceptance and approval binding
// ---------------------------------------------------------------------------
describe('acceptance and approval are bound to the terms', () => {
  const o = offer();
  const acceptance: Acceptance = {
    offer_id: o.id,
    principal: o.principal,
    fee: o.fee,
    term_days: o.term_days,
    accepted_at: TODAY,
  };
  const approval: Approval = {
    offer_id: o.id,
    customer_id: o.customer_id,
    principal: o.principal,
    fee: o.fee,
    term_days: o.term_days,
    approved_by: 'credit.officer',
    approved_at: TODAY,
  };

  it('holds while nothing changes', () => {
    expect(isAcceptanceValid(acceptance, o)).toBe(true);
    expect(isApprovalValid(approval, o)).toBe(true);
  });

  it('is void once the term changes', () => {
    // 60 days became 90, which also reprices the fee.
    const repriced = offer({ term_days: 90, fee: priceOffer(o.principal, 90).fee });
    expect(isAcceptanceValid(acceptance, repriced)).toBe(false);
    expect(isApprovalValid(approval, repriced)).toBe(false);
  });

  it('is void once the amount or fee changes', () => {
    expect(isAcceptanceValid(acceptance, offer({ principal: 9_000 }))).toBe(false);
    expect(isAcceptanceValid(acceptance, offer({ fee: 400 }))).toBe(false);
  });

  it('voids an approval when the customer changes', () => {
    expect(isApprovalValid(approval, offer({ customer_id: 'acct_other' }))).toBe(false);
  });
});

// ---------------------------------------------------------------------------
// §5 — collection
// ---------------------------------------------------------------------------
describe('collection', () => {
  it('takes the fee first, then principal, and reports what is left', () => {
    const b = bridge({ principal_outstanding: 10_000, fee_outstanding: 450 });
    const partial = collect(b, 1_000);
    expect(partial.applied_to_fee).toBe(450);
    expect(partial.applied_to_principal).toBe(550);
    expect(partial.remaining).toBe(9_450);
    expect(partial.bridge.status).toBe('COLLECTING');

    const rest = collect(partial.bridge, 9_450);
    expect(rest.fully_repaid).toBe(true);
    expect(rest.remaining).toBe(0);
  });

  it('returns the excess when a deposit overpays', () => {
    const result = collect(bridge(), 11_000);
    expect(result.excess).toBe(550);
    expect(result.fully_repaid).toBe(true);
  });

  it('clears accrued late fees before the contracted fee', () => {
    const late = bridge({ accrued_fee: 66.67 });
    const result = collect(late, 66.67);
    expect(result.bridge.accrued_fee).toBe(0);
    expect(result.bridge.fee_outstanding).toBe(450);
  });
});
