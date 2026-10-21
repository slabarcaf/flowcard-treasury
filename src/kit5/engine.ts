import type { Signals, Tier } from '../policy/types.js';
import type { CustomerDetail, FeedEvent, PipelineStep, PolicyCheck } from '../state/types.js';
import {
  assignTier,
  computeCreditLine,
  lendable,
  reserveFloor,
  deployable,
  capitalLevel,
  toBand,
  getDoor,
  hasNoGo,
  bridgeFee,
  BAND_LABELS,
} from '../policy/index.js';
import { AGENT_RATIONALES } from '../seed.js';
import type {
  DeclineEvent,
  Kit5State,
  RepaymentEvent,
  ScenarioCustomer,
  ScenarioEvent,
  ScenarioInputs,
} from './types.js';

// ---------------------------------------------------------------------------
// Kit 5 engine: each decline is decided the moment it arrives, against the
// capital left at that moment. Pure functions: old state in, new state out.
// ---------------------------------------------------------------------------

/**
 * Collect-by window when a request doesn't set its own. The customer's default term
 * is "as soon as money arrives"; this is the latest we wait before it is past due.
 * Assumption pending the team's call on collection timing (Source of Truth, Open 1).
 */
export const DEFAULT_TERM_DAYS = 30;

function fmt(n: number): string {
  return '$' + n.toLocaleString('en-US');
}

function minutes(time: string): number {
  const [h, m] = time.split(':').map(Number);
  return h * 60 + (m || 0);
}

export function sortEvents(events: ScenarioEvent[]): ScenarioEvent[] {
  return [...events].sort((a, b) => a.day - b.day || minutes(a.time) - minutes(b.time));
}

export function clockLabel(startDate: string, day: number): string {
  const d = new Date(`${startDate}T12:00:00Z`);
  d.setUTCDate(d.getUTCDate() + day);
  return d.toLocaleDateString('en-US', { weekday: 'short', month: 'short', day: 'numeric', timeZone: 'UTC' });
}

/** "Oct 19" — for the date columns. */
export function shortDate(startDate: string, day: number): string {
  const d = new Date(`${startDate}T12:00:00Z`);
  d.setUTCDate(d.getUTCDate() + day);
  return d.toLocaleDateString('en-US', { month: 'short', day: 'numeric', timeZone: 'UTC' });
}

function dueDayFor(ev: DeclineEvent): number {
  return ev.day + (ev.termDays ?? DEFAULT_TERM_DAYS);
}

function clone(s: Kit5State): Kit5State {
  return structuredClone(s);
}

function pushFeed(s: Kit5State, time: string, text: string, type: FeedEvent['type']) {
  s.feed.push({ id: `feed_${s.feed.length + 1}`, time, text, type });
}

function setCapital(s: Kit5State, lentOut: number) {
  const f = s.inputs.facility;
  s.lentOut = lentOut;
  s.deployableCapital = deployable(f, lentOut);
  s.band = toBand(capitalLevel(s.deployableCapital, lendable(f)));
}

function bandLabel(s: Kit5State): string {
  return BAND_LABELS[s.band];
}

function customerFor(s: Kit5State, key: string): ScenarioCustomer | undefined {
  return s.inputs.customers.find((c) => c.key === key);
}

function declineFor(s: Kit5State, requestId: string): DeclineEvent | undefined {
  return s.events.find((e): e is DeclineEvent => e.type === 'decline' && e.requestId === requestId);
}

// ---------------------------------------------------------------------------
// Initial state
// ---------------------------------------------------------------------------
export function initialKit5State(inputs: ScenarioInputs): Kit5State {
  const f = inputs.facility;
  const s: Kit5State = {
    phase: 'idle',
    lentOut: 0,
    deployableCapital: 0,
    floor: reserveFloor(f),
    band: 'a_lot',
    queue: [],
    feed: [],
    customerDetails: {},
    selectedCustomerId: null,
    processedCustomers: [],
    showCollection: false,
    collectionRows: [],
    demoClock: clockLabel(inputs.startDate, 0),
    inputs: structuredClone(inputs),
    events: sortEvents(inputs.events),
    cursor: 0,
    pause: null,
    day: 0,
    bridges: {},
  };
  setCapital(s, inputs.outstanding);
  return s;
}

export function start(prev: Kit5State): Kit5State {
  if (prev.phase !== 'idle') return prev;
  const s = clone(prev);
  s.phase = 'scanned';
  return s;
}

export function nextEvent(s: Kit5State): ScenarioEvent | undefined {
  return s.events[s.cursor];
}

/** True when the sequence can move on by itself (a decline is next and nothing is pending). */
export function canAutoAdvance(s: Kit5State): boolean {
  return s.phase !== 'idle' && !s.pause && nextEvent(s)?.type === 'decline';
}

// ---------------------------------------------------------------------------
// Underwriting pipeline for one request
// ---------------------------------------------------------------------------
interface Evaluation {
  precheck: PipelineStep[];
  perRequest: PipelineStep[];
  tier: Tier;
  creditLine: number;
  door: ReturnType<typeof getDoor>;
  eligible: boolean;
  ineligibleReason?: string;
  failures: string[];
}

function evaluate(s: Kit5State, c: ScenarioCustomer, signals: Signals, shortfall: number, tierFrom?: Tier): Evaluation {
  const f = s.inputs.facility;
  const precheck: PipelineStep[] = [];
  const perRequest: PipelineStep[] = [];
  const skip = (why: string): PipelineStep[] => [
    { label: 'Spend check', sublabel: 'Shortfall ≤ line', status: 'skip', tooltip: why },
    { label: 'Line check', sublabel: 'Within credit line', status: 'skip', tooltip: why },
    { label: 'Facility check', sublabel: 'Deployable ≥ shortfall', status: 'skip', tooltip: why },
    { label: 'Door / Approve', sublabel: 'Auto / Human / Closed', status: 'skip', tooltip: why },
  ];

  const enoughData = c.historyDays >= f.minHistoryDays;
  precheck.push({
    label: 'Enough data?',
    sublabel: `${f.minHistoryDays} days required`,
    status: enoughData ? 'pass' : 'fail',
    value: `${c.historyDays} days`,
    tooltip: `Customer must have at least ${f.minHistoryDays} days of history before they can receive a bridge.`,
  });
  if (!enoughData) {
    const why = 'Skipped — not enough history data to proceed.';
    precheck.push(
      { label: 'No-go rules', sublabel: '5 exclusion checks', status: 'skip', tooltip: why },
      { label: 'Four signals', sublabel: 'Inflow, concentration, buffer, track', status: 'skip', tooltip: why },
      { label: 'Tier', sublabel: 'A / B / C / none', status: 'skip', tooltip: why },
      { label: 'Credit line', sublabel: 'Tier multiplier × inflow', status: 'skip', tooltip: why },
    );
    const reason = `No line yet: ${c.historyDays} days of history, ${f.minHistoryDays} required (eligible in ${f.minHistoryDays - c.historyDays} days)`;
    return { precheck, perRequest: skip('Skipped — customer is ineligible.'), tier: 'none', creditLine: 0, door: 'closed', eligible: false, ineligibleReason: reason, failures: [reason] };
  }

  const noGo = hasNoGo(c.noGo);
  precheck.push({
    label: 'No-go rules',
    sublabel: '5 exclusion checks',
    status: noGo ? 'fail' : 'pass',
    value: noGo ? 'Blocked' : 'Clear',
    tooltip: 'Excluded industry, fraud flag, account frozen, past write-off, or one client above 70% of income. Any one blocks the customer.',
  });

  const tier = noGo ? 'none' : assignTier(signals);
  const vals = Object.values(signals);
  const g = vals.filter((v) => v === 'green').length;
  const y = vals.filter((v) => v === 'yellow').length;
  const r = vals.filter((v) => v === 'red').length;
  precheck.push({
    label: 'Four signals',
    sublabel: 'Inflow, concentration, buffer, track',
    status: 'pass',
    value: `${g}G ${y}Y${r ? ` ${r}R` : ''}`,
    tooltip: 'Inflow reliability, inflow concentration, cash buffer and track record, each rated green, yellow or red.',
  });
  precheck.push({
    label: 'Tier',
    sublabel: 'A / B / C / none',
    status: tier === 'none' ? 'fail' : 'pass',
    value: tierFrom && tierFrom !== tier ? `${tierFrom} → ${tier}` : `Tier ${tier}`,
    tooltip: 'All green or 1 yellow = A. 2 yellows = B. 3+ yellows or 1 red = C. Red on inflow reliability = no line.',
  });

  const creditLine = computeCreditLine(tier, c.monthlyInflow, f);
  precheck.push({
    label: 'Credit line',
    sublabel: 'Tier multiplier × inflow',
    status: creditLine > 0 ? 'pass' : 'fail',
    value: fmt(creditLine),
    tooltip: `Monthly inflow × tier multiplier (A 30%, B 20%, C 10%), capped at ${f.maxPerCustomerPct * 100}% of the facility.`,
  });

  if (tier === 'none') {
    const reason = noGo ? 'No line: a no-go rule applies' : 'No line: red on inflow reliability';
    return { precheck, perRequest: skip('Skipped — customer has no credit line.'), tier, creditLine: 0, door: 'closed', eligible: false, ineligibleReason: reason, failures: [reason] };
  }

  const failures: string[] = [];
  const lineOk = shortfall <= creditLine;
  perRequest.push({
    label: 'Spend check',
    sublabel: 'Shortfall ≤ credit line',
    status: lineOk ? 'pass' : 'fail',
    value: `${fmt(shortfall)} / ${fmt(creditLine)}`,
    tooltip: 'Full shortfall or nothing: a line smaller than the shortfall would still leave the payment failing.',
  });
  perRequest.push({
    label: 'Line check',
    sublabel: 'Within remaining line',
    status: lineOk ? 'pass' : 'fail',
    value: lineOk ? 'OK' : 'Exceeds',
    tooltip: 'The shortfall must fit within what is left of the customer’s credit line.',
  });
  if (!lineOk) failures.push(`${fmt(creditLine)} line does not cover the ${fmt(shortfall)} shortfall`);

  const facilityOk = shortfall <= s.deployableCapital;
  perRequest.push({
    label: 'Facility check',
    sublabel: 'Deployable ≥ shortfall',
    status: facilityOk ? 'pass' : 'fail',
    value: fmt(s.deployableCapital),
    tooltip: `Capital left above the ${fmt(s.floor)} reserve floor at the moment this request arrived. A bridge never takes the facility below the floor, and we never fund part of a request.`,
  });
  if (!facilityOk) {
    failures.push(`${fmt(shortfall)} exceeds the ${fmt(s.deployableCapital)} left above the floor; no partial bridge`);
  }

  const door = getDoor(s.band, tier);
  perRequest.push({
    label: 'Door / Approve',
    sublabel: 'Auto / Human / Closed',
    status: door === 'closed' ? 'fail' : 'pass',
    value: door === 'auto' ? 'Auto' : door === 'human' ? 'Human' : 'Closed',
    tooltip: `Tier ${tier} at capital level ${bandLabel(s)} (before this bridge).`,
  });
  if (door === 'closed') failures.push(`Tier ${tier} door is Closed at capital level ${bandLabel(s)}`);

  return { precheck, perRequest, tier, creditLine, door, eligible: true, failures };
}

// ---------------------------------------------------------------------------
// Decide one request (on arrival, or after the CRM re-rating)
// ---------------------------------------------------------------------------
function decide(s: Kit5State, ev: DeclineEvent, opts: { signals?: Signals; tierFrom?: Tier; holdForCrm?: boolean } = {}) {
  const c = customerFor(s, ev.customerKey);
  if (!c) return;
  const shortfall = Math.max(0, ev.paymentAmount - ev.walletBalance);
  const signals = opts.signals ?? c.signals;
  const e = evaluate(s, c, signals, shortfall, opts.tierFrom);
  const row = s.queue.find((q) => q.id === ev.requestId)!;
  const prev = s.customerDetails[ev.requestId];

  const detail: CustomerDetail = {
    customerId: ev.requestId,
    customerName: c.name,
    tier: e.tier,
    tierChange: opts.tierFrom && opts.tierFrom !== e.tier ? { from: opts.tierFrom, to: e.tier } : undefined,
    door: e.door,
    allocation: 0,
    status: '',
    rationale: '',
    policyChecks: [],
    precheck: e.precheck,
    perRequest: e.perRequest,
    showCrmNote: prev?.showCrmNote ?? false,
    showSignals: prev?.showSignals ?? false,
    bloomAgentPhase: prev?.bloomAgentPhase,
    showApproval: false,
    awaitingApproval: false,
  };

  row.tier = e.tier;
  row.door = e.door;

  if (!e.eligible) {
    detail.status = 'Ineligible';
    detail.ineligibleReason = e.ineligibleReason;
    row.status = 'Ineligible';
    row.eligible = false;
    row.ineligibleReason = e.ineligibleReason;
    pushFeed(s, ev.time, `${c.name}: ${e.ineligibleReason}`, 'info');
  } else if (opts.holdForCrm) {
    // Salesforce flow kept from GD: the agent flags a fresh note before the decision.
    detail.status = 'Evaluating';
    detail.rationale = c.rationale ?? '';
    detail.bloomAgentPhase = 'found';
    row.status = 'Evaluating';
    s.pause = { kind: 'crm', requestId: ev.requestId };
    pushFeed(s, ev.time, `Agent found a new Salesforce note on ${c.name}; decision waits for re-evaluation`, 'info');
  } else if (e.failures.length > 0) {
    detail.status = 'Declined';
    detail.rationale = c.crmSignalsAfter ? AGENT_RATIONALES.bloom_declined : e.failures[0];
    detail.policyChecks = e.failures.map((label) => ({ label: `${c.name}: ${label}`, passed: false }));
    row.status = 'Declined';
    pushFeed(s, ev.time, `${c.name}: declined. ${e.failures[0]}`, 'notification');
  } else {
    const checks: PolicyCheck[] = [
      { label: `${c.name}: Tier ${e.tier}, door ${e.door === 'auto' ? 'Auto' : 'Human'} at capital level ${bandLabel(s)}`, passed: true },
      { label: `${c.name}: ${fmt(shortfall)} within ${fmt(e.creditLine)} credit line`, passed: true },
      { label: `${c.name}: ${fmt(shortfall)} fits in ${fmt(s.deployableCapital)} above the floor`, passed: true },
    ];
    detail.allocation = shortfall;
    detail.rationale = c.rationale ?? '';
    detail.policyChecks = checks;
    row.allocation = shortfall;

    if (e.door === 'auto') {
      pushFeed(s, ev.time, `${c.name}: auto-approved, ${fmt(shortfall)} bridge funded`, 'info');
      fund(s, ev, c.name, shortfall, detail);
      s.pause = { kind: 'ack', requestId: ev.requestId };
    } else {
      const lendableAmt = lendable(s.inputs.facility);
      const after = s.deployableCapital - shortfall;
      const bandAfter = BAND_LABELS[toBand(capitalLevel(after, lendableAmt))];
      detail.status = 'Pending approval';
      detail.showApproval = true;
      detail.awaitingApproval = true;
      detail.approvalTerms = {
        amount: shortfall,
        currency: 'USD',
        to: `${c.name} (${c.id})`,
        covers: `Declined card transaction ${ev.requestId}`,
        term: `As soon as money arrives (customer default); collect by ${shortDate(s.inputs.startDate, dueDayFor(ev))}`,
        fee: `${(s.inputs.facility.feeRatePer30Days * 100).toFixed(1)}% per 30 days on daily outstanding`,
        evidence: `Tier ${e.tier}: door Human at capital level ${bandLabel(s)}`,
        capitalImpact: `Deployable ${fmt(s.deployableCapital)} → ${fmt(after)}, ${bandLabel(s) === bandAfter ? `band stays ${bandAfter}` : `band ${bandLabel(s)} → ${bandAfter}`}, floor intact`,
      };
      row.status = 'Pending approval';
      s.pause = { kind: 'human', requestId: ev.requestId };
      pushFeed(s, ev.time, `${c.name}: sent to credit officer (Tier ${e.tier}, capital level ${bandLabel(s)})`, 'info');
    }
  }

  row.rationale = detail.rationale;
  s.customerDetails[ev.requestId] = detail;
  if (!s.processedCustomers.includes(ev.requestId)) s.processedCustomers.push(ev.requestId);
}

function fund(s: Kit5State, ev: DeclineEvent, name: string, amount: number, detail: CustomerDetail) {
  detail.status = 'Funded';
  const row = s.queue.find((q) => q.id === ev.requestId);
  if (row) row.status = 'Funded';
  const dueDay = dueDayFor(ev);
  if (row) {
    row.fundedOn = shortDate(s.inputs.startDate, ev.day);
    row.dueOn = shortDate(s.inputs.startDate, dueDay);
  }
  setCapital(s, s.lentOut + amount);
  s.bridges[ev.requestId] = { customerName: name, amount, day: ev.day, dueDay };
  pushFeed(s, ev.time, `Transfer bridge-${ev.requestId}: ${fmt(amount)} → ${name} — SETTLED. Collect by ${shortDate(s.inputs.startDate, dueDay)}`, 'transfer');
}

// ---------------------------------------------------------------------------
// Process the next event in arrival order
// ---------------------------------------------------------------------------
export function step(prev: Kit5State): Kit5State {
  const ev = nextEvent(prev);
  if (prev.phase === 'idle' || prev.pause || !ev) return prev;
  const s = clone(prev);
  s.cursor += 1;
  if (ev.day !== s.day) {
    s.day = ev.day;
    s.demoClock = clockLabel(s.inputs.startDate, ev.day);
    pushFeed(s, ev.time, `+${ev.day} days — ${s.demoClock}`, 'info');
  }
  if (ev.type === 'decline') arrive(s, ev);
  else repay(s, ev);
  return s;
}

function arrive(s: Kit5State, ev: DeclineEvent) {
  const c = customerFor(s, ev.customerKey);
  if (!c) return;
  const shortfall = Math.max(0, ev.paymentAmount - ev.walletBalance);
  pushFeed(s, ev.time, `INSUFFICIENT_FUNDS: ${c.name} — ${ev.paymentDescription}`, 'decline');
  s.queue.push({
    id: ev.requestId,
    customerName: c.name,
    customerId: ev.requestId,
    paymentDescription: ev.paymentDescription,
    paymentAmount: ev.paymentAmount,
    shortfall,
    detectedAt: ev.time,
    tier: 'none',
    door: 'closed',
    allocation: 0,
    status: 'Detected',
    rationale: '',
    eligible: true,
  });
  s.selectedCustomerId = ev.requestId;
  decide(s, ev, { holdForCrm: !!c.crmSignalsAfter });
}

function repay(s: Kit5State, ev: RepaymentEvent) {
  const bridge = s.bridges[ev.requestId];
  if (!bridge) {
    pushFeed(s, ev.time, `Deposit ${fmt(ev.depositAmount)} received; no open bridge ${ev.requestId} to collect`, 'deposit');
    return;
  }
  const days = Math.max(0, ev.day - bridge.day);
  const daysLate = ev.day - bridge.dueDay;
  const fee = bridgeFee(bridge.amount, days, s.inputs.facility.feeRatePer30Days);
  pushFeed(s, ev.time, `Deposit ${fmt(ev.depositAmount)} received from ${bridge.customerName}${daysLate > 0 ? ` (${daysLate} days past due)` : ''}`, 'deposit');
  pushFeed(s, ev.time, `Fee charge: $${fee.toFixed(2)} (${days} days @ ${(s.inputs.facility.feeRatePer30Days * 100).toFixed(1)}%/30d)`, 'charge');
  pushFeed(s, ev.time, `Principal repayment: ${fmt(bridge.amount)} — capital back in the pool`, 'payment');

  setCapital(s, s.lentOut - bridge.amount);
  delete s.bridges[ev.requestId];

  const detail = s.customerDetails[ev.requestId];
  if (detail) {
    detail.status = 'Repaid';
    detail.collectionRows = [
      { label: 'Bridge fee', amount: fee, requestId: `fee-bridge-${ev.requestId}`, status: 'Collected' },
      { label: 'Principal', amount: bridge.amount, requestId: `repay-dep_${ev.requestId}`, status: 'Collected' },
    ];
  }
  const row = s.queue.find((q) => q.id === ev.requestId);
  if (row) {
    row.status = 'Repaid';
    row.repaidOn = shortDate(s.inputs.startDate, ev.day);
  }
  s.selectedCustomerId = ev.requestId;
}

// ---------------------------------------------------------------------------
// Presenter actions while the sequence is paused
// ---------------------------------------------------------------------------
export function revealCrm(prev: Kit5State): Kit5State {
  if (prev.pause?.kind !== 'crm') return prev;
  const detail = prev.customerDetails[prev.pause.requestId];
  if (detail?.bloomAgentPhase !== 'found') return prev;
  const s = clone(prev);
  const d = s.customerDetails[s.pause!.requestId];
  d.showCrmNote = true;
  d.showSignals = true;
  d.bloomAgentPhase = 'revealed';
  return s;
}

export function reEvaluateCrm(prev: Kit5State): Kit5State {
  if (prev.pause?.kind !== 'crm') return prev;
  const ev = declineFor(prev, prev.pause.requestId);
  const c = ev && customerFor(prev, ev.customerKey);
  if (!ev || !c?.crmSignalsAfter || prev.customerDetails[ev.requestId]?.bloomAgentPhase !== 'revealed') return prev;
  const s = clone(prev);
  s.pause = null;
  const tierFrom = assignTier(c.signals);
  const tierTo = assignTier(c.crmSignalsAfter);
  pushFeed(s, ev.time, `AI extracted 3 risk signals from the Salesforce note on ${c.name}`, 'info');
  pushFeed(s, ev.time, `${c.name} re-rated: Tier ${tierFrom} → Tier ${tierTo}`, 'info');
  decide(s, ev, { signals: c.crmSignalsAfter, tierFrom });
  s.customerDetails[ev.requestId].bloomAgentPhase = 'done';
  return s;
}

/** The presenter has seen the auto-approval; move on to the next request. */
export function acknowledge(prev: Kit5State): Kit5State {
  if (prev.pause?.kind !== 'ack') return prev;
  return { ...prev, pause: null };
}

export function approve(prev: Kit5State, requestId: string): Kit5State {
  if (prev.pause?.kind !== 'human' || prev.pause.requestId !== requestId) return prev;
  const ev = declineFor(prev, requestId);
  if (!ev) return prev;
  const s = clone(prev);
  const detail = s.customerDetails[requestId];
  s.pause = null;
  detail.awaitingApproval = false;
  pushFeed(s, ev.time, `Credit officer approved ${fmt(detail.allocation)} for ${detail.customerName}`, 'notification');
  pushFeed(s, ev.time, `${detail.customerName} accepted the bridge`, 'info');
  fund(s, ev, detail.customerName, detail.allocation, detail);
  return s;
}
