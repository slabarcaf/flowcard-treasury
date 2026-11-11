/**
 * The demo control panel behind the Admin screen.
 *
 * The policy config is a set of plain objects that every engine function reads
 * at call time, so a threshold can be changed in place and the next decision
 * uses it. That is what makes this screen possible — and it is also why it is
 * a demo tool and not an ops screen: there is no audit trail, no approval, and
 * nothing is persisted.
 *
 * Nothing here decides anything. It only moves the numbers the engine reads.
 */
import {
  DELINQUENCY,
  HISTORY,
  PRICING,
  REGIME_RULES,
  REGIME_THRESHOLDS,
  TIER_LIMIT_PCT,
} from '../policy/policy.config.js';

/**
 * Lets this one module write a config object the rest of the app reads as
 * frozen. `as const` gives those objects literal types; this widens the numbers
 * back so they can be set, without loosening the config for anyone else.
 */
type Editable<T> = { -readonly [K in keyof T]: T[K] extends number ? number : T[K] };

const pricing = PRICING as Editable<typeof PRICING>;
const history = HISTORY as Editable<typeof HISTORY>;
const regimeThresholds = REGIME_THRESHOLDS as Editable<typeof REGIME_THRESHOLDS>;
const watch = DELINQUENCY.watch as Editable<typeof DELINQUENCY.watch>;
const delinquent = DELINQUENCY.delinquent as Editable<typeof DELINQUENCY.delinquent>;
const serious = DELINQUENCY.serious as Editable<typeof DELINQUENCY.serious>;
const defaulted = DELINQUENCY.defaulted as Editable<typeof DELINQUENCY.defaulted>;

export type FieldKind = 'money' | 'percent' | 'days' | 'months';

export interface ConfigField {
  id: string;
  group: string;
  label: string;
  hint: string;
  kind: FieldKind;
  get: () => number;
  set: (value: number) => void;
}

/**
 * The late-repayment bands are contiguous: moving the end of one band moves the
 * start of the next, so the ladder never leaves a gap.
 */
function setWatchTo(value: number) {
  watch.to = value;
  delinquent.from = value + 1;
}

function setDelinquentTo(value: number) {
  delinquent.to = value;
  serious.from = value + 1;
}

function setSeriousTo(value: number) {
  serious.to = value;
  defaulted.from = value + 1;
}

export const CONFIG_FIELDS: ConfigField[] = [
  {
    id: 'pricing.serviceFee',
    group: 'Pricing',
    label: 'Service fee',
    hint: 'Flat, charged once per bridge',
    kind: 'money',
    get: () => pricing.serviceFee,
    set: (v) => { pricing.serviceFee = v; },
  },
  {
    id: 'pricing.monthlyRate',
    group: 'Pricing',
    label: 'Monthly rate',
    hint: 'Prorated daily over the chosen term',
    kind: 'percent',
    get: () => pricing.monthlyRate,
    set: (v) => { pricing.monthlyRate = v; },
  },
  {
    id: 'limits.A',
    group: 'Tier limits',
    label: 'Tier A limit',
    hint: 'Share of monthly inflows',
    kind: 'percent',
    get: () => TIER_LIMIT_PCT.A,
    set: (v) => { TIER_LIMIT_PCT.A = v; },
  },
  {
    id: 'limits.B',
    group: 'Tier limits',
    label: 'Tier B limit',
    hint: 'Share of monthly inflows',
    kind: 'percent',
    get: () => TIER_LIMIT_PCT.B,
    set: (v) => { TIER_LIMIT_PCT.B = v; },
  },
  {
    id: 'limits.C',
    group: 'Tier limits',
    label: 'Tier C limit',
    hint: 'Share of monthly inflows',
    kind: 'percent',
    get: () => TIER_LIMIT_PCT.C,
    set: (v) => { TIER_LIMIT_PCT.C = v; },
  },
  {
    id: 'history.minTenureMonths',
    group: 'Eligibility',
    label: 'Minimum tenure',
    hint: 'Below this there is no line at all',
    kind: 'months',
    get: () => history.minTenureMonths,
    set: (v) => { history.minTenureMonths = v; },
  },
  {
    id: 'history.minBalanceDays',
    group: 'Eligibility',
    label: 'Minimum balance history',
    hint: 'Days of end-of-day balances required',
    kind: 'days',
    get: () => history.minBalanceDays,
    set: (v) => { history.minBalanceDays = v; },
  },
  {
    id: 'regime.abundantAbove',
    group: 'Capital regimes',
    label: 'Abundant above',
    hint: 'Capital above the floor, as a share of the facility',
    kind: 'percent',
    get: () => regimeThresholds.abundantAbove,
    set: (v) => { regimeThresholds.abundantAbove = v; },
  },
  {
    id: 'regime.normalAbove',
    group: 'Capital regimes',
    label: 'Normal above',
    hint: 'Below Abundant, above this, capital is Normal',
    kind: 'percent',
    get: () => regimeThresholds.normalAbove,
    set: (v) => { regimeThresholds.normalAbove = v; },
  },
  {
    id: 'regime.tightAbove',
    group: 'Capital regimes',
    label: 'Tight above',
    hint: 'Below this, capital is Critical',
    kind: 'percent',
    get: () => regimeThresholds.tightAbove,
    set: (v) => { regimeThresholds.tightAbove = v; },
  },
  {
    id: 'regime.tightHumanApproval',
    group: 'Capital regimes',
    label: 'Approval needed above, in Tight',
    hint: 'Bridges over this need a person while capital is Tight',
    kind: 'money',
    get: () => REGIME_RULES.TIGHT.humanApprovalAbove ?? 0,
    set: (v) => { REGIME_RULES.TIGHT.humanApprovalAbove = v; },
  },
  {
    id: 'dpd.watch',
    group: 'Late repayment',
    label: 'Watch ends at',
    hint: 'Days past due; Delinquent starts the day after',
    kind: 'days',
    get: () => watch.to,
    set: setWatchTo,
  },
  {
    id: 'dpd.delinquent',
    group: 'Late repayment',
    label: 'Delinquent ends at',
    hint: 'Days past due; Serious starts the day after',
    kind: 'days',
    get: () => delinquent.to,
    set: setDelinquentTo,
  },
  {
    id: 'dpd.serious',
    group: 'Late repayment',
    label: 'Serious ends at',
    hint: 'Days past due; a default is recorded after this',
    kind: 'days',
    get: () => serious.to,
    set: setSeriousTo,
  },
];

/** What the spec says, captured before anything on this screen can change it. */
const SPEC_DEFAULTS: Record<string, number> = Object.fromEntries(
  CONFIG_FIELDS.map((f) => [f.id, f.get()]),
);

export function specDefault(fieldId: string): number {
  return SPEC_DEFAULTS[fieldId];
}

export function isModified(field: ConfigField): boolean {
  return field.get() !== SPEC_DEFAULTS[field.id];
}

export function modifiedCount(): number {
  return CONFIG_FIELDS.filter(isModified).length;
}

/** Put every threshold back to the value in docs/bridge-capital-spec.md. */
export function resetPolicyConfig(): void {
  for (const field of CONFIG_FIELDS) field.set(SPEC_DEFAULTS[field.id]);
}

/** The groups, in the order the screen shows them. */
export const CONFIG_GROUPS: string[] = [
  ...new Set(CONFIG_FIELDS.map((f) => f.group)),
];
