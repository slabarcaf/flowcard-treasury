import type { DashboardState, QueueRow, FeedEvent, PolicyCheck, PipelineStep, CollectionRow } from './types.js';
import {
  FACILITY_CONFIG,
  CUSTOMERS,
  DECLINED_TRANSACTIONS,
  SEEDED_OUTSTANDING,
  BLOOM_SIGNALS_AFTER_CRM,
  AGENT_RATIONALES,
  NOVA_COLLECTION,
  NOVA_APPROVAL_TERMS,
  DEMO_APPROVED_BRIDGE_CAPS,
  demoPolicyReadiness,
} from '../seed.js';
import {
  assignTier,
  lendable,
  reserveFloor,
  deployable,
  capitalLevel,
  toBand,
  hasNoGo,
  evaluateDemoPolicy,
} from '../policy/index.js';
import { bridgeFee } from '../policy/fees.js';
import type { DemoPolicyDecision } from '../policy/index.js';
import type { CustomerProfile, DemoPolicyReadiness, FacilityConfig, Band } from '../policy/types.js';

const LENDABLE = lendable(FACILITY_CONFIG);
const FLOOR = reserveFloor(FACILITY_CONFIG);

/** Policy adapter used by the GD demo engine and shared decision fixtures. */
export function evaluateDashboardCustomerPolicy(input: {
  profile: CustomerProfile; facility: FacilityConfig; readiness: DemoPolicyReadiness;
  shortfall: number; outstanding: number; band: Band; deployableCapital: number;
}): DemoPolicyDecision {
  return evaluateDemoPolicy(input);
}

// ---------------------------------------------------------------------------
// Initial (idle) state
// ---------------------------------------------------------------------------
export function initialState(): DashboardState {
  const dep = deployable(FACILITY_CONFIG, SEEDED_OUTSTANDING);
  return {
    phase: 'idle',
    lentOut: SEEDED_OUTSTANDING,
    deployableCapital: dep,
    floor: FLOOR,
    band: toBand(capitalLevel(dep, LENDABLE)),
    queue: [],
    feed: [],
    customerDetails: {},
    selectedCustomerId: null,
    processedCustomers: [],
    showCollection: false,
    collectionRows: [],
    demoClock: 'Mon, Oct 19',
  };
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------
let feedIdCounter = 0;
function feedEvent(time: string, text: string, type: FeedEvent['type']): FeedEvent {
  return { id: `feed_${++feedIdCounter}`, time, text, type };
}

function fmt(n: number): string {
  return '$' + n.toLocaleString('en-US');
}

// Map customer ID to seed key
function customerKey(customerId: string): string | undefined {
  for (const [key, profile] of Object.entries(CUSTOMERS)) {
    if (profile.id === customerId) return key;
  }
  return undefined;
}

// ---------------------------------------------------------------------------
// scanDeclines — creates raw "Detected" rows, no decisions yet
// ---------------------------------------------------------------------------
export function scanDeclines(prev: DashboardState): DashboardState {
  const state: DashboardState = JSON.parse(JSON.stringify(prev));
  state.phase = 'scanned';
  feedIdCounter = 0;

  const queue: QueueRow[] = [];
  const feed: FeedEvent[] = [];

  for (const txn of DECLINED_TRANSACTIONS) {
    feed.push(
      feedEvent(txn.detectedAt, `INSUFFICIENT_FUNDS: ${txn.customerName} — ${txn.paymentDescription}`, 'decline'),
    );

    queue.push({
      id: txn.transactionId,
      customerName: txn.customerName,
      customerId: txn.customerId,
      paymentDescription: txn.paymentDescription,
      paymentAmount: txn.paymentAmount,
      shortfall: txn.shortfall,
      detectedAt: txn.detectedAt,
      tier: 'none',
      door: 'closed',
      allocation: 0,
      status: 'Detected',
      rationale: '',
      eligible: true,
    });
  }

  state.queue = queue;
  state.feed = feed;
  state.customerDetails = {};
  state.selectedCustomerId = null;
  state.processedCustomers = [];
  state.showCollection = false;
  state.collectionRows = [];

  return state;
}

// ---------------------------------------------------------------------------
// processCustomer — runs full pipeline for one customer, builds steps
// ---------------------------------------------------------------------------
export function processCustomer(prev: DashboardState, customerId: string): DashboardState {
  const state: DashboardState = JSON.parse(JSON.stringify(prev));

  // Already processed?
  if (state.processedCustomers.includes(customerId)) return state;

  const key = customerKey(customerId);
  if (!key) return state;

  const profile = CUSTOMERS[key];
  const txn = DECLINED_TRANSACTIONS.find((t) => t.customerId === customerId);
  if (!txn) return state;

  const readiness = demoPolicyReadiness(profile.id);
  const facility = { ...FACILITY_CONFIG, approvedBridgeCap: DEMO_APPROVED_BRIDGE_CAPS[profile.id] };
  const sharedDecision = evaluateDashboardCustomerPolicy({
    profile, facility, readiness, shortfall: txn.shortfall, outstanding: 0,
    band: state.band, deployableCapital: state.deployableCapital,
  });

  if (sharedDecision.status === 'blocked' || sharedDecision.status === 'ineligible') {
    const reason = sharedDecision.reasons.join(' ');
    const skipped = (label: string): PipelineStep => ({ label, sublabel: 'Skipped by deterministic policy gate', status: 'skip', tooltip: reason });
    const precheck: PipelineStep[] = [
      { label: 'Salesforce snapshot', sublabel: 'Fresh complete snapshot', status: readiness.snapshot === 'fresh' ? 'pass' : 'fail', value: readiness.snapshot, tooltip: reason },
      { label: 'Evidence readiness', sublabel: 'Qualified or valid no-signal', status: ['ready', 'no_signal'].includes(readiness.evidence) ? 'pass' : 'fail', value: readiness.evidence, tooltip: reason },
      skipped('Daily precheck'),
    ];
    const perRequest = ['Spend check', 'Line check', 'Facility check', 'Door / Approve'].map(skipped);
    const rowIdx = state.queue.findIndex((r) => r.customerId === customerId);
    const status = sharedDecision.status === 'blocked' ? 'Blocked' : 'Ineligible';
    if (rowIdx >= 0) {
      state.queue[rowIdx].status = status;
      state.queue[rowIdx].eligible = false;
      state.queue[rowIdx].ineligibleReason = reason;
      state.queue[rowIdx].reasonCodes = sharedDecision.reasonCodes;
      state.queue[rowIdx].reasonTexts = sharedDecision.reasons;
    }
    state.customerDetails[customerId] = {
      customerId, customerName: profile.name, tier: 'none', door: 'closed', allocation: 0,
      creditLine: 0, reasonCodes: sharedDecision.reasonCodes, reasonTexts: sharedDecision.reasons,
      status, rationale: '', policyChecks: [], precheck, perRequest, showCrmNote: false,
      showSignals: false, showApproval: false, awaitingApproval: false, ineligibleReason: reason,
    };
    state.processedCustomers = [...state.processedCustomers, customerId];
    return state;
  }

  const precheck: PipelineStep[] = [];
  const perRequest: PipelineStep[] = [];
  let policyChecks: PolicyCheck[] = [];

  // ---- Daily precheck pipeline ----

  // Step 1: Enough data?
  const hasEnoughData = profile.historyDays >= FACILITY_CONFIG.minHistoryDays;
  precheck.push({
    label: 'Enough data?',
    sublabel: `${FACILITY_CONFIG.minHistoryDays} days required`,
    status: hasEnoughData ? 'pass' : 'fail',
    value: `${profile.historyDays} days`,
    tooltip: `Customer must have at least ${FACILITY_CONFIG.minHistoryDays} days of transaction history on the platform before they can receive a bridge. This ensures we have enough data to assess risk.`,
  });

  if (!hasEnoughData) {
    // Short-circuit: skip remaining steps
    precheck.push(
      { label: 'No-go rules', sublabel: '5 exclusion checks', status: 'skip', tooltip: 'Skipped — not enough history data to proceed.' },
      { label: 'Four signals', sublabel: 'Inflow, concentration, buffer, track', status: 'skip', tooltip: 'Skipped — not enough history data to proceed.' },
      { label: 'Tier', sublabel: 'A / B / C / none', status: 'skip', tooltip: 'Skipped — not enough history data to proceed.' },
      { label: 'Credit line', sublabel: 'Tier multiplier × inflow', status: 'skip', tooltip: 'Skipped — not enough history data to proceed.' },
    );

    // No per-request steps either
    perRequest.push(
      { label: 'Spend check', sublabel: 'Shortfall ≤ line', status: 'skip', tooltip: 'Skipped — customer is ineligible.' },
      { label: 'Line check', sublabel: 'Within credit line', status: 'skip', tooltip: 'Skipped — customer is ineligible.' },
      { label: 'Facility check', sublabel: 'Deployable ≥ shortfall', status: 'skip', tooltip: 'Skipped — customer is ineligible.' },
      { label: 'Door / Approve', sublabel: 'Auto / Human / Closed', status: 'skip', tooltip: 'Skipped — customer is ineligible.' },
    );

    // Build queue row + detail for ineligible customer
    const rowIdx = state.queue.findIndex((r) => r.customerId === customerId);
    if (rowIdx >= 0) {
      state.queue[rowIdx].status = 'Ineligible';
      state.queue[rowIdx].eligible = false;
      state.queue[rowIdx].ineligibleReason = `No line: ${profile.historyDays} days of history (${FACILITY_CONFIG.minHistoryDays} required)`;
    }

    state.customerDetails[customerId] = {
      customerId,
      customerName: profile.name,
      tier: 'none',
      door: 'closed',
      allocation: 0,
      status: 'Ineligible',
      rationale: '',
      policyChecks: [],
      precheck,
      perRequest,
      showCrmNote: false,
      showSignals: false,
      showApproval: false,
      awaitingApproval: false,
      ineligibleReason: `No line: ${profile.historyDays} days of history (${FACILITY_CONFIG.minHistoryDays} required)`,
    };

    state.processedCustomers = [...state.processedCustomers, customerId];
    return state;
  }

  // Step 2: No-go rules
  const noGo = hasNoGo(profile.noGo);
  precheck.push({
    label: 'No-go rules',
    sublabel: '5 exclusion checks',
    status: noGo ? 'fail' : 'pass',
    value: noGo ? 'Blocked' : 'Clear',
    tooltip: 'Hard exclusion checks: excluded industry, fraud flag, account frozen, past write-off, or mono-client (>70% concentration). Any one blocks the customer.',
  });

  // Step 3: Four signals — use initial signals only (Bloom CRM re-rating happens later)
  const signals = profile.signals;
  const initialTier = sharedDecision.tier;
  const finalTier = initialTier;
  const showCrmNote = false;
  const showSignals = false;

  // Count signal colors for display
  const sigVals = Object.values(signals);
  const greens = sigVals.filter((s) => s === 'green').length;
  const yellows = sigVals.filter((s) => s === 'yellow').length;

  precheck.push({
    label: 'Four signals',
    sublabel: 'Inflow, concentration, buffer, track',
    status: 'pass',
    value: `${greens}G ${yellows}Y`,
    tooltip: 'Four risk signals are evaluated: inflow reliability, inflow concentration, cash buffer, and track record. Each is rated green (low risk), yellow (moderate), or red (high). Red on inflow reliability means no credit line.',
  });

  // Step 4: Tier
  precheck.push({
    label: 'Tier',
    sublabel: 'A / B / C / none',
    status: finalTier === 'none' ? 'fail' : 'pass',
    value: `Tier ${finalTier}`,
    tooltip: 'Tier is derived from signals. All green or 1 yellow = Tier A. 2 yellows = Tier B. 3+ yellows or 1 red = Tier C. Red on inflow reliability = no tier. Higher tiers get larger credit lines and easier approval.',
  });

  // Step 5: Credit line
  const creditLine = sharedDecision.creditLine;
  precheck.push({
    label: 'Credit line',
    sublabel: 'Tier multiplier × inflow',
    status: creditLine > 0 ? 'pass' : 'fail',
    value: fmt(creditLine),
    tooltip: `Credit line = monthly inflow × tier multiplier (A: 30%, B: 20%, C: 10%), capped at ${FACILITY_CONFIG.maxPerCustomerPct * 100}% of facility size. This is the maximum bridge amount the customer can receive.`,
  });

  // ---- Per-request pipeline ----
  const shortfall = txn.shortfall;

  // Step 1: Spend check — shortfall within credit line
  const spendOk = !sharedDecision.reasonCodes.includes('CREDIT_LINE_TOO_SMALL');
  perRequest.push({
    label: 'Spend check',
    sublabel: 'Shortfall ≤ credit line',
    status: spendOk ? 'pass' : 'fail',
    value: `${fmt(shortfall)} / ${fmt(creditLine)}`,
    tooltip: 'Does the payment shortfall fit within the customer\'s credit line? No partial bridges allowed — if the line is smaller than the shortfall, the payment would still fail.',
  });

  // Step 2: Line check — within remaining credit line (no prior outstanding for demo)
  const lineOk = spendOk; // same in demo since no prior outstanding
  perRequest.push({
    label: 'Line check',
    sublabel: 'Within remaining line',
    status: lineOk ? 'pass' : 'fail',
    value: lineOk ? 'OK' : 'Exceeds',
    tooltip: 'Checks whether the shortfall fits within the customer\'s remaining credit line after subtracting any existing outstanding bridges. Prevents over-exposure to a single customer.',
  });

  // Step 3: Facility check — deployable capital >= shortfall
  const facilityOk = !sharedDecision.reasonCodes.includes('INSUFFICIENT_DEPLOYABLE_CAPITAL');
  perRequest.push({
    label: 'Facility check',
    sublabel: 'Deployable ≥ shortfall',
    status: facilityOk ? 'pass' : 'fail',
    value: fmt(state.deployableCapital),
    tooltip: 'Is there enough deployable capital in the facility to fund this bridge? Deployable = facility size minus reserve floor minus already lent out.',
  });

  // Step 4: Door / Approve
  const door = sharedDecision.door;
  const doorOk = door !== 'closed';
  const allPerRequestOk = sharedDecision.status === 'approved' || sharedDecision.status === 'human_review';

  perRequest.push({
    label: 'Door / Approve',
    sublabel: 'Auto / Human / Closed',
    status: doorOk ? 'pass' : 'fail',
    value: door === 'auto' ? 'Auto' : door === 'human' ? 'Human' : 'Closed',
    tooltip: 'The approval path depends on the customer\'s tier and the current capital band. Auto = instant funding. Human = credit officer must approve. Closed = no bridge available at this capital level for this tier.',
  });

  // ---- Determine outcome ----
  const allocation = sharedDecision.allocation;
  let status: string;
  let rationale: string;
  let showApproval = false;
  let awaitingApproval = false;

  if (sharedDecision.status === 'declined' || !allPerRequestOk) {
    status = 'Declined';
    rationale = key === 'bloom' ? AGENT_RATIONALES.bloom_declined : '';
    policyChecks = [];

    if (key === 'bloom') {
      policyChecks = [
        { label: `Bloom: Tier ${finalTier} door is Closed in band ${state.band.replace('_', ' ')}`, passed: false },
        { label: `Bloom: ${fmt(creditLine)} does not cover the ${fmt(shortfall)} shortfall`, passed: false },
      ];
    }
  } else if (door === 'auto' && key === 'bloom') {
    // Bloom: show initial pipeline as passing, but don't fund — CRM re-evaluation pending
    status = 'Evaluating';
    rationale = AGENT_RATIONALES.bloom_initial;
    policyChecks = [];
  } else if (door === 'auto') {
    status = 'Funded';
    rationale = AGENT_RATIONALES[key as keyof typeof AGENT_RATIONALES] ?? '';
    policyChecks = [
      { label: `${profile.name}: Tier ${finalTier}, door Auto in band ${state.band.replace('_', ' ')}`, passed: true },
      { label: `${profile.name}: ${fmt(allocation)} within ${fmt(creditLine)} credit line`, passed: true },
      { label: `${profile.name}: Deployable capital sufficient`, passed: true },
    ];

    // Auto-fund: update capital
    state.lentOut += allocation;
    state.deployableCapital = deployable(FACILITY_CONFIG, state.lentOut);
    state.band = toBand(capitalLevel(state.deployableCapital, LENDABLE));

    state.feed.push(
      feedEvent('9:15', `Transfer bridge-${txn.transactionId}: ${fmt(allocation)} → ${profile.name} — SETTLED`, 'transfer'),
    );
  } else {
    // Human door
    status = 'Pending approval';
    rationale = AGENT_RATIONALES[`${key}_revised` as keyof typeof AGENT_RATIONALES]
      ?? AGENT_RATIONALES[key as keyof typeof AGENT_RATIONALES]
      ?? '';
    showApproval = true;
    awaitingApproval = true;
    policyChecks = [
      { label: `${profile.name}: Tier ${finalTier}, door Human in band ${state.band.replace('_', ' ')}`, passed: true },
      { label: `${profile.name}: ${fmt(allocation)} within ${fmt(creditLine)} credit line`, passed: true },
      { label: `${profile.name}: Deployable capital sufficient`, passed: true },
    ];
  }

  // Update queue row
  const rowIdx = state.queue.findIndex((r) => r.customerId === customerId);
  if (rowIdx >= 0) {
    state.queue[rowIdx].tier = finalTier;
    state.queue[rowIdx].door = door;
    state.queue[rowIdx].allocation = allocation;
    state.queue[rowIdx].status = status;
    state.queue[rowIdx].rationale = rationale;
    if (!allPerRequestOk && key !== 'bloom') {
      state.queue[rowIdx].eligible = false;
    }
  }

  // Build detail
  state.customerDetails[customerId] = {
    customerId,
    customerName: profile.name,
    tier: finalTier,
    tierChange: undefined,
    door,
    allocation,
    creditLine,
    reasonCodes: sharedDecision.reasonCodes,
    reasonTexts: sharedDecision.reasons,
    status,
    rationale,
    policyChecks,
    precheck,
    perRequest,
    showCrmNote,
    showSignals,
    ...(key === 'bloom' ? { bloomAgentPhase: 'found' as const } : {}),
    showApproval,
    awaitingApproval,
    ...(showApproval ? { approvalTerms: {
      ...NOVA_APPROVAL_TERMS,
      amount: allocation,
      creditLine,
      door: 'human' as const,
      reviewRequirement: sharedDecision.reasonCodes.includes('CREDIT_REVIEW_WATCH')
        ? 'Salesforce credit review is on watch; a human decision is required.'
        : 'A credit officer must review and approve these exact terms.',
      to: `${profile.name} (${profile.id})`,
      covers: `Declined card transaction ${txn.transactionId}`,
    } } : {}),
  };

  state.processedCustomers = [...state.processedCustomers, customerId];

  // If auto-funded and this is first funding, add plan event (skip Bloom — CRM re-eval pending)
  if (door === 'auto' && key !== 'bloom') {
    state.feed.push(
      feedEvent('9:14', `${profile.name}: auto-approved, ${fmt(allocation)} bridge funded`, 'info'),
    );
  }

  return state;
}

// ---------------------------------------------------------------------------
// revealBloomCrm — step 1: show CRM note + signals, phase → 'revealed'
// ---------------------------------------------------------------------------
export function revealBloomCrm(prev: DashboardState): DashboardState {
  const state: DashboardState = JSON.parse(JSON.stringify(prev));
  const customerId = CUSTOMERS['bloom'].id;
  const detail = state.customerDetails[customerId];
  if (!detail || detail.bloomAgentPhase !== 'found') return state;

  detail.showCrmNote = true;
  detail.showSignals = true;
  detail.bloomAgentPhase = 'revealed';

  return state;
}

// ---------------------------------------------------------------------------
// reEvaluateBloom — step 2: CRM re-rating, rebuilds pipeline with post-CRM signals
// ---------------------------------------------------------------------------
export function reEvaluateBloom(prev: DashboardState): DashboardState {
  const state: DashboardState = JSON.parse(JSON.stringify(prev));

  const customerId = CUSTOMERS['bloom'].id;
  const detail = state.customerDetails[customerId];
  if (!detail || detail.bloomAgentPhase !== 'revealed') return state;

  const profile = CUSTOMERS['bloom'];
  const txn = DECLINED_TRANSACTIONS.find((t) => t.customerId === customerId);
  if (!txn) return state;

  // Original tier from initial signals
  const initialTier = assignTier(profile.signals);

  // Re-rate with CRM signals
  const signals = BLOOM_SIGNALS_AFTER_CRM;

  const sigVals = Object.values(signals);
  const greens = sigVals.filter((s) => s === 'green').length;
  const yellows = sigVals.filter((s) => s === 'yellow').length;

  const shortfall = txn.shortfall;
  const sharedDecision = evaluateDashboardCustomerPolicy({
    profile: { ...profile, signals },
    facility: { ...FACILITY_CONFIG, approvedBridgeCap: DEMO_APPROVED_BRIDGE_CAPS[profile.id] },
    readiness: demoPolicyReadiness(profile.id), shortfall, outstanding: 0,
    band: state.band, deployableCapital: state.deployableCapital,
  });
  const finalTier = sharedDecision.tier;
  const creditLine = sharedDecision.creditLine;
  const door = sharedDecision.door;

  // Rebuild precheck with final signals
  const precheck: PipelineStep[] = [
    {
      label: 'Enough data?',
      sublabel: `${FACILITY_CONFIG.minHistoryDays} days required`,
      status: 'pass',
      value: `${profile.historyDays} days`,
      tooltip: `Customer must have at least ${FACILITY_CONFIG.minHistoryDays} days of transaction history on the platform before they can receive a bridge. This ensures we have enough data to assess risk.`,
    },
    {
      label: 'No-go rules',
      sublabel: '5 exclusion checks',
      status: 'pass',
      value: 'Clear',
      tooltip: 'Hard exclusion checks: excluded industry, fraud flag, account frozen, past write-off, or mono-client (>70% concentration). Any one blocks the customer.',
    },
    {
      label: 'Four signals',
      sublabel: 'Inflow, concentration, buffer, track',
      status: 'pass',
      value: `${greens}G ${yellows}Y`,
      tooltip: 'Four risk signals are evaluated: inflow reliability, inflow concentration, cash buffer, and track record. Each is rated green (low risk), yellow (moderate), or red (high). Red on inflow reliability means no credit line.',
    },
    {
      label: 'Tier',
      sublabel: 'A / B / C / none',
      status: finalTier === 'none' ? 'fail' : 'pass',
      value: `${initialTier} → ${finalTier}`,
      tooltip: 'Tier is derived from signals. All green or 1 yellow = Tier A. 2 yellows = Tier B. 3+ yellows or 1 red = Tier C. Red on inflow reliability = no tier. Higher tiers get larger credit lines and easier approval.',
    },
    {
      label: 'Credit line',
      sublabel: 'Tier multiplier × inflow',
      status: creditLine > 0 ? 'pass' : 'fail',
      value: fmt(creditLine),
      tooltip: `Credit line = monthly inflow × tier multiplier (A: 30%, B: 20%, C: 10%), capped at ${FACILITY_CONFIG.maxPerCustomerPct * 100}% of facility size. This is the maximum bridge amount the customer can receive.`,
    },
  ];

  // Rebuild per-request with final values
  const spendOk = !sharedDecision.reasonCodes.includes('CREDIT_LINE_TOO_SMALL');
  const lineOk = spendOk;
  const facilityOk = !sharedDecision.reasonCodes.includes('INSUFFICIENT_DEPLOYABLE_CAPITAL');
  const doorOk = door !== 'closed';
  const perRequest: PipelineStep[] = [
    {
      label: 'Spend check',
      sublabel: 'Shortfall ≤ credit line',
      status: spendOk ? 'pass' : 'fail',
      value: `${fmt(shortfall)} / ${fmt(creditLine)}`,
      tooltip: 'Does the payment shortfall fit within the customer\'s credit line? No partial bridges allowed — if the line is smaller than the shortfall, the payment would still fail.',
    },
    {
      label: 'Line check',
      sublabel: 'Within remaining line',
      status: lineOk ? 'pass' : 'fail',
      value: lineOk ? 'OK' : 'Exceeds',
      tooltip: 'Checks whether the shortfall fits within the customer\'s remaining credit line after subtracting any existing outstanding bridges. Prevents over-exposure to a single customer.',
    },
    {
      label: 'Facility check',
      sublabel: 'Deployable ≥ shortfall',
      status: facilityOk ? 'pass' : 'fail',
      value: fmt(state.deployableCapital),
      tooltip: 'Is there enough deployable capital in the facility to fund this bridge? Deployable = facility size minus reserve floor minus already lent out.',
    },
    {
      label: 'Door / Approve',
      sublabel: 'Auto / Human / Closed',
      status: doorOk ? 'pass' : 'fail',
      value: door === 'auto' ? 'Auto' : door === 'human' ? 'Human' : 'Closed',
      tooltip: 'The approval path depends on the customer\'s tier and the current capital band. Auto = instant funding. Human = credit officer must approve. Closed = no bridge available at this capital level for this tier.',
    },
  ];

  const allocation = sharedDecision.allocation;

  // Policy checks for the declined outcome
  const policyChecks: PolicyCheck[] = [
    { label: `Bloom: Tier ${finalTier} door is Closed in band ${state.band.replace('_', ' ')}`, passed: false },
    { label: `Bloom: ${fmt(creditLine)} does not cover the ${fmt(shortfall)} shortfall`, passed: false },
  ];

  // Feed events
  state.feed.push(feedEvent('9:13', 'CRM note found: Jamie Chen, Account Manager on Bloom & Co.', 'info'));
  state.feed.push(feedEvent('9:13', 'AI extracted 3 risk signals from CRM note', 'info'));
  state.feed.push(feedEvent('9:13', `Bloom & Co. re-rated: Tier ${initialTier} → Tier ${finalTier} (3 yellow signals)`, 'info'));

  // Update detail
  detail.tier = finalTier;
  detail.tierChange = { from: initialTier, to: finalTier };
  detail.door = door;
  detail.allocation = allocation;
  detail.creditLine = creditLine;
  detail.reasonCodes = sharedDecision.reasonCodes;
  detail.reasonTexts = sharedDecision.reasons;
  detail.status = 'Declined';
  detail.rationale = AGENT_RATIONALES.bloom_declined;
  detail.policyChecks = policyChecks;
  detail.precheck = precheck;
  detail.perRequest = perRequest;
  detail.showCrmNote = true;
  detail.showSignals = true;
  detail.bloomAgentPhase = 'done';

  // Update queue row
  const rowIdx = state.queue.findIndex((r) => r.customerId === customerId);
  if (rowIdx >= 0) {
    state.queue[rowIdx].tier = finalTier;
    state.queue[rowIdx].door = door;
    state.queue[rowIdx].allocation = allocation;
    state.queue[rowIdx].status = 'Declined';
    state.queue[rowIdx].rationale = AGENT_RATIONALES.bloom_declined;
    state.queue[rowIdx].reasonCodes = sharedDecision.reasonCodes;
    state.queue[rowIdx].reasonTexts = sharedDecision.reasons;
  }

  return state;
}

// ---------------------------------------------------------------------------
// fundCustomer — marks as "Approved", adds notification, does NOT update capital
// ---------------------------------------------------------------------------
export function fundCustomer(prev: DashboardState, customerId: string): DashboardState {
  const state: DashboardState = JSON.parse(JSON.stringify(prev));
  const detail = state.customerDetails[customerId];
  if (!detail || !detail.awaitingApproval) return state;

  // Mark as approved (not yet funded)
  detail.status = 'Approved';
  detail.awaitingApproval = false;

  const rowIdx = state.queue.findIndex((r) => r.customerId === customerId);
  if (rowIdx >= 0) {
    state.queue[rowIdx].status = 'Approved';
  }

  // Add notification feed event
  state.feed.push(
    feedEvent('9:16', `Notification sent to ${detail.customerName}: bridge of ${fmt(detail.allocation)} approved and ready to accept`, 'notification'),
  );

  return state;
}

// ---------------------------------------------------------------------------
// completeFunding — updates status to "Funded", updates capital, adds acceptance
// ---------------------------------------------------------------------------
export function completeFunding(prev: DashboardState, customerId: string): DashboardState {
  const state: DashboardState = JSON.parse(JSON.stringify(prev));
  const detail = state.customerDetails[customerId];
  if (!detail || detail.status !== 'Approved') return state;

  const allocation = detail.allocation;

  // Update capital
  state.lentOut += allocation;
  state.deployableCapital = deployable(FACILITY_CONFIG, state.lentOut);
  state.band = toBand(capitalLevel(state.deployableCapital, LENDABLE));

  // Update status
  detail.status = 'Funded';
  const rowIdx = state.queue.findIndex((r) => r.customerId === customerId);
  if (rowIdx >= 0) {
    state.queue[rowIdx].status = 'Funded';
  }

  // Acceptance feed event
  state.feed.push(
    feedEvent('9:16', `${detail.customerName} accepted the bridge`, 'info'),
  );
  state.feed.push(
    feedEvent('9:16', `Transfer bridge-${state.queue[rowIdx]?.id}: ${fmt(allocation)} → ${detail.customerName} — SETTLED`, 'transfer'),
  );

  state.phase = 'funded';

  return state;
}

// ---------------------------------------------------------------------------
// simulateCollection — advances clock 9 days, deposits, collects fee + principal
// ---------------------------------------------------------------------------
export function simulateCollection(prev: DashboardState): DashboardState {
  const state: DashboardState = JSON.parse(JSON.stringify(prev));

  const col = NOVA_COLLECTION;
  const fee = bridgeFee(col.principalAmount, col.daysOutstanding, FACILITY_CONFIG.feeRatePer30Days);

  state.demoClock = 'Wed, Oct 28';

  // Feed events
  state.feed.push(
    feedEvent('10:00', `+9 days — ${state.demoClock}`, 'info'),
  );
  state.feed.push(
    feedEvent('10:01', `Deposit ${fmt(col.depositAmount)} received from Nova Agency`, 'deposit'),
  );
  state.feed.push(
    feedEvent('10:01', `Fee charge: ${fmt(fee)} (${col.daysOutstanding} days @ 1.5%/30d)`, 'charge'),
  );
  state.feed.push(
    feedEvent('10:01', `Principal repayment: ${fmt(col.principalAmount)}`, 'payment'),
  );

  // Return capital
  state.lentOut -= col.principalAmount;
  state.deployableCapital = deployable(FACILITY_CONFIG, state.lentOut);
  state.band = toBand(capitalLevel(state.deployableCapital, LENDABLE));

  // Collection rows
  const rows: CollectionRow[] = [
    { label: 'Bridge fee', amount: fee, requestId: col.feeRequestId, status: 'Collected' },
    { label: 'Principal', amount: col.principalAmount, requestId: col.principalRequestId, status: 'Collected' },
  ];

  state.showCollection = true;
  state.collectionRows = rows;

  return state;
}

// ---------------------------------------------------------------------------
// reset — back to idle
// ---------------------------------------------------------------------------
export function reset(): DashboardState {
  feedIdCounter = 0;
  return initialState();
}
