/**
 * Every Bridge Capital threshold mirrors the design spec:
 * docs/designs/bridge-capital-policy-spec.md.
 *
 * The spec calls its numbers placeholders, so they live here and nowhere else:
 * no policy function hard-codes a threshold.
 */
import type { Regime, SpendCriticality, Tier } from '../types.js';

// ---------------------------------------------------------------------------
// §3 Eligibility floor: "No history: tenure < 3 months or < 90 days of balance data."
// ---------------------------------------------------------------------------
export const HISTORY = {
  minTenureMonths: 3,
  minBalanceDays: 90,
} as const;

// ---------------------------------------------------------------------------
// §3 Tiers and limits
//
//   Tier A: tenure >= 12 months, no late or defaulted bridges in 180 days,
//           days_near_zero_90d <= 5.          Limit 30% of monthly_inflows.
//   Tier B: tenure >= 6 months, at most 1 late bridge in 180 days, no defaults.
//                                             Limit 20% of monthly_inflows.
//   Tier C: everyone else eligible, no default in 12 months.
//                                             Limit 10% of monthly_inflows.
//
// Reading note: tier B's "no defaults" is unqualified in the spec, so it is
// applied to the whole bridge history, which keeps the ladder monotonic against
// tier C's narrower "no default in 12 months".
// ---------------------------------------------------------------------------
export const TIERS = {
  A: {
    minTenureMonths: 12,
    lateLookbackDays: 180,
    maxLateInLookback: 0,
    maxDefaultsInLookback: 0,
    maxDaysNearZero90d: 5,
    limitPctOfMonthlyInflows: 0.30,
  },
  B: {
    minTenureMonths: 6,
    lateLookbackDays: 180,
    maxLateInLookback: 1,
    /** Applied to the whole history, not just the lookback — see note above. */
    allowAnyDefaultEver: false,
    limitPctOfMonthlyInflows: 0.20,
  },
  C: {
    defaultLookbackDays: 365,
    limitPctOfMonthlyInflows: 0.10,
  },
} as const;

/** Limit as a share of monthly_inflows, by tier. NONE has no limit. */
export const TIER_LIMIT_PCT: Record<Tier, number> = {
  A: TIERS.A.limitPctOfMonthlyInflows,
  B: TIERS.B.limitPctOfMonthlyInflows,
  C: TIERS.C.limitPctOfMonthlyInflows,
  NONE: 0,
};

/** Ordering used for ranking and for "one level down" downgrades. */
export const TIER_RANK: Record<Tier, number> = { A: 3, B: 2, C: 1, NONE: 0 };

/** §5: tier downgraded one level when a bridge reaches 8 DPD. */
export const TIER_DOWNGRADE: Record<Tier, Tier> = { A: 'B', B: 'C', C: 'NONE', NONE: 'NONE' };

// ---------------------------------------------------------------------------
// §4 Spend criticality: critical (suppliers, payroll, inventory), standard,
// discretionary (travel, entertainment, SaaS), mapped from merchant category.
// ---------------------------------------------------------------------------
export const CRITICALITY_BY_CATEGORY: Record<string, SpendCriticality> = {
  suppliers: 'CRITICAL',
  payroll: 'CRITICAL',
  inventory: 'CRITICAL',
  utilities: 'STANDARD',
  logistics: 'STANDARD',
  professional_services: 'STANDARD',
  travel: 'DISCRETIONARY',
  entertainment: 'DISCRETIONARY',
  saas: 'DISCRETIONARY',
};

/** Used when a merchant category is not in the map above. */
export const DEFAULT_CRITICALITY: SpendCriticality = 'STANDARD';

/** Ordering used when ranking scarce capital. */
export const CRITICALITY_RANK: Record<SpendCriticality, number> = {
  CRITICAL: 3,
  STANDARD: 2,
  DISCRETIONARY: 1,
};

// ---------------------------------------------------------------------------
// §5 Pricing and terms
//   Fee = $50 service fee + principal x 2% per month, prorated daily:
//         50 + principal x 0.02 x term_days / 30
//   Charged for the full chosen term even if repaid early.
// ---------------------------------------------------------------------------
export const PRICING = {
  serviceFee: 50,
  monthlyRate: 0.02,
  daysPerMonth: 30,
  presetTermDays: [15, 30, 60, 90],
  minCustomTermDays: 7,
  maxCustomTermDays: 90,
} as const;

// ---------------------------------------------------------------------------
// §5 Late repayment ladder, in days past due.
// Outstanding keeps accruing at the monthly rate while late.
// ---------------------------------------------------------------------------
export const DELINQUENCY = {
  watch: { from: 1, to: 7 },
  delinquent: { from: 8, to: 30 },
  serious: { from: 31, to: 60 },
  /** 60+ DPD is a default; the spec's bands make 61 the first day past "31-60". */
  defaulted: { from: 61 },
  /** §5: ineligible for 12 months after a default. */
  defaultIneligibleMonths: 12,
} as const;

// ---------------------------------------------------------------------------
// §6 Platform capital regimes, measured on capital available above the reserve
// floor as a share of the facility.
// ---------------------------------------------------------------------------
export const REGIME_THRESHOLDS = {
  abundantAbove: 0.60,
  normalAbove: 0.30,
  tightAbove: 0.10,
  /** At or below this, no new bridges. */
  atFloorAtOrBelow: 0,
} as const;

export interface RegimeRule {
  /** Lowest tier allowed. null means any eligible tier. */
  minTier: Tier | null;
  allowedCriticality: SpendCriticality[];
  /** Whether bridges clear automatically, need a person, or are closed outright. */
  approval: 'AUTO' | 'HUMAN' | 'CLOSED';
  /** Principal above which a person must approve. null means no such rule. */
  humanApprovalAbove: number | null;
  /** Whether requests compete for capital instead of being decided first-come. */
  rankRequests: boolean;
}

const ALL_CRITICALITY: SpendCriticality[] = ['CRITICAL', 'STANDARD', 'DISCRETIONARY'];

/**
 * §6:
 *   Abundant > 60%: any eligible customer, auto-approve.
 *   Normal 30-60%: tier B or better, auto-approve within limit.
 *   Tight 10-30%: critical spend only, requests ranked, bridges above $5,000 need approval.
 *   Critical < 10%: tier A and critical spend only, every bridge needs approval.
 *   At floor: no new bridges, collect only.
 */
export const REGIME_RULES: Record<Regime, RegimeRule> = {
  ABUNDANT: {
    minTier: null,
    allowedCriticality: ALL_CRITICALITY,
    approval: 'AUTO',
    humanApprovalAbove: null,
    rankRequests: false,
  },
  NORMAL: {
    minTier: 'B',
    allowedCriticality: ALL_CRITICALITY,
    approval: 'AUTO',
    humanApprovalAbove: null,
    rankRequests: false,
  },
  TIGHT: {
    minTier: null,
    allowedCriticality: ['CRITICAL'],
    approval: 'AUTO',
    humanApprovalAbove: 5_000,
    rankRequests: true,
  },
  CRITICAL: {
    minTier: 'A',
    allowedCriticality: ['CRITICAL'],
    approval: 'HUMAN',
    humanApprovalAbove: 0,
    rankRequests: true,
  },
  AT_FLOOR: {
    minTier: null,
    allowedCriticality: [],
    approval: 'CLOSED',
    humanApprovalAbove: null,
    rankRequests: false,
  },
};

/** Labels for the UI and the stats report. */
export const REGIME_LABELS: Record<Regime, string> = {
  ABUNDANT: 'Abundant',
  NORMAL: 'Normal',
  TIGHT: 'Tight',
  CRITICAL: 'Critical',
  AT_FLOOR: 'At floor',
};

// ---------------------------------------------------------------------------
// §5 While bridged: tier C cards restricted to critical spend, capped at the
// bridged amount. Restriction lifts on full repayment.
// ---------------------------------------------------------------------------
export const WHILE_BRIDGED = {
  restrictedTiers: ['C'] as Tier[],
  allowedCriticality: ['CRITICAL'] as SpendCriticality[],
} as const;

// ---------------------------------------------------------------------------
// §4 The eligibility rules themselves, in the order the engine runs them.
//
// The engine takes each rule's label from here, and the back office renders
// this same list, so the rules shown to ops and the rules applied cannot drift.
// ---------------------------------------------------------------------------
export interface RuleSpec {
  id: string;
  /** Short name, as it appears on a decision. */
  label: string;
  /** What the rule actually checks, for the rules reference. */
  checks: string;
  /** The corresponding policy section in docs/designs/bridge-capital-policy-spec.md. */
  source: string;
}

export const RULE_CATALOGUE: RuleSpec[] = [
  {
    id: 'R1_HAS_HISTORY',
    label: 'Has history',
    checks: `Tenure of at least ${HISTORY.minTenureMonths} months and at least ${HISTORY.minBalanceDays} days of balance data.`,
    source: '§3, §4 rule 1',
  },
  {
    id: 'R2_NO_PAST_DUE',
    label: 'No bridge currently past due',
    checks: 'No open bridge is past its due date.',
    source: '§4 rule 2',
  },
  {
    id: 'R3_WITHIN_LIMIT',
    label: 'Outstanding + new bridge within limit',
    checks: 'Everything already outstanding plus this bridge stays inside the tier limit.',
    source: '§3, §4 rule 3',
  },
  {
    id: 'R4_CRITICALITY_ALLOWED',
    label: 'Spend criticality allowed in current capital regime',
    checks: 'The merchant category maps to a criticality the current regime still covers.',
    source: '§4 rule 4, §6',
  },
  {
    id: 'R5_RESERVE_FLOOR',
    label: 'Bridge does not breach the platform reserve floor',
    checks: 'The principal fits inside the capital available above the reserve floor. Bridges are never partial.',
    source: '§4 rule 5, §6',
  },
  {
    id: 'R6_NO_DUPLICATE',
    label: 'No duplicate bridge for the same transaction',
    checks: 'The same card transaction has not already been decided.',
    source: '§4 rule 6',
  },
  {
    id: 'R7_RECEIVABLE_IN_TERM',
    label: 'An expected receivable is due before the term ends',
    checks: 'There is an invoice due inside the chosen term, so there is something to collect from.',
    source: '§4 rule 7',
  },
  {
    id: 'R8_REGIME_TIER',
    label: 'Tier allowed in current capital regime',
    checks: 'The customer’s tier clears the floor the current regime sets. Not one of the spec’s seven: §6 gates by tier as well, so the engine states it as its own rule.',
    source: '§6',
  },
];

const RULE_LABELS: Record<string, string> = Object.fromEntries(
  RULE_CATALOGUE.map((r) => [r.id, r.label]),
);

/** The label the engine puts on a rule result. */
export function ruleLabel(ruleId: string): string {
  return RULE_LABELS[ruleId] ?? ruleId;
}
