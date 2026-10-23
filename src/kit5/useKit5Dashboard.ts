import { useState, useCallback, useEffect, useMemo, useRef } from 'react';
import type { Scenario, ScenarioInputs, Kit5State } from './types.js';
import type { Kit5FixtureCheckpointLifecycleRequest, Kit5FixtureCheckpointRequest, Kit5FixtureCheckpointResponse, Kit5PolicyDecisionSummary, Kit5ProviderTransaction, Kit5RepaymentDepositResponse, LedgerProjectionResponse } from '../services/types.js';
import { BridgeApiClient, BridgeApiError } from '../services/http/BridgeApiClient.js';
import { loadScenarios } from './scenarios.js';
import {
  initialKit5State,
  start,
  step,
  canAutoAdvance,
  revealCrm,
  reEvaluateCrm,
  approve as approveRequest,
  acknowledge,
  sortEvents,
} from './engine.js';

/** Pause between arrivals while the sequence plays itself. */
const ARRIVAL_MS = 1400;
const LIVE_RUN_STORAGE_PREFIX = 'flowcard-kit5-live-run:';
const FIXTURE_CHECKPOINT_STORAGE_PREFIX = 'flowcard-kit5-fixture-checkpoint:';
const LIVE_RUN_ID_PATTERN = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;

export interface LiveProviderActivity {
  scenarioId?: string;
  status: string;
  attemptId?: string;
  customerId?: string;
  lifecycleId?: string | null;
  result?: Kit5ProviderTransaction;
  amount?: number;
  customerName?: string;
  policyStatus?: string;
  policyError?: string | null;
  policyDecisionId?: string | null;
  bridgeId?: string | null;
  retryLifecycleId?: string | null;
  retryResult?: { requestId?: string; processResult: 'APPROVED' | 'DECLINED'; failureReason: string | null; cardTransactionEventId: string; cardTransactionId: string; cardTransactionLifecycleId: string } | null;
  retryError?: string | null;
  policyDecision?: Kit5PolicyDecisionSummary | null;
  humanApproved?: boolean;
  repaymentDeposit?: Kit5RepaymentDepositResponse | null;
  repaymentDepositError?: string | null;
}

export type FixtureCheckpointResolution = 'awaiting' | 'confirmed' | 'fixture-only';

export type FixtureCheckpointControl = 'run-attempt' | 'reconcile-attempt' | 'resolve-lifecycle' | 'continue-fixture' | 'continue-fixture-only';

export interface FixtureCheckpointActivity {
  scenarioId: string;
  customerId: string;
  actionId: string;
  status: Kit5FixtureCheckpointResponse['status'] | 'unresolved';
  attemptId?: string;
  lifecycleId?: string | null;
  attemptedAmount?: { amountCents: number; currency: string } | null;
  result?: Kit5ProviderTransaction;
  reason?: string;
}

export function isFixtureCheckpointEventNext(
  scenario: Scenario,
  state: Kit5State,
): boolean {
  const checkpoint = scenario.providerCheckpoint;
  const next = state.events[state.cursor];
  return checkpoint?.provider === 'airwallex' && fixtureCheckpointTermsMatch(scenario, state.inputs) && next?.type === 'decline' &&
    next.requestId === checkpoint.eventRequestId;
}

export function fixtureCheckpointTermsMatch(scenario: Scenario, inputs: ScenarioInputs): boolean {
  const checkpoint = scenario.providerCheckpoint;
  if (!checkpoint) return false;
  const expected = scenario.inputs.events.find((event) => event.type === 'decline' && event.requestId === checkpoint.eventRequestId);
  const configured = inputs.events.find((event) => event.type === 'decline' && event.requestId === checkpoint.eventRequestId);
  if (!expected || !configured || expected.type !== 'decline' || configured.type !== 'decline') return false;
  const expectedCustomer = scenario.inputs.customers.find((customer) => customer.key === checkpoint.customerKey);
  const configuredCustomer = inputs.customers.find((customer) => customer.key === checkpoint.customerKey);
  return expectedCustomer?.id === configuredCustomer?.id &&
    expected.customerKey === configured.customerKey && expected.day === configured.day &&
    expected.time === configured.time && expected.paymentDescription === configured.paymentDescription &&
    expected.paymentAmount === configured.paymentAmount && expected.walletBalance === configured.walletBalance;
}

export function shouldPauseForFixtureCheckpoint(
  mode: 'fixture' | 'fixture-checkpoint' | 'sandbox-live',
  scenario: Scenario,
  state: Kit5State,
  resolution: FixtureCheckpointResolution,
): boolean {
  return mode === 'fixture-checkpoint' && state.phase !== 'idle' && resolution === 'awaiting' &&
    isFixtureCheckpointEventNext(scenario, state);
}

interface PersistedLiveRun {
  runId: string;
  customerIndex: number;
  action: LiveProviderActivity | null;
  history: LiveProviderActivity[];
}

interface PersistedFixtureCheckpointRun {
  runId: string;
  activity: FixtureCheckpointActivity | null;
  resolution: FixtureCheckpointResolution;
  enabled: boolean;
  identityKnown: boolean;
  identityPersisted: boolean;
}

const SELECTED_SCENARIO_STORAGE_KEY = 'flowcard-kit5-selected-scenario';

function newFixtureCheckpointRun(): Omit<PersistedFixtureCheckpointRun, 'identityKnown' | 'identityPersisted'> {
  return { runId: globalThis.crypto.randomUUID(), activity: null, resolution: 'awaiting', enabled: false };
}

function fixtureCheckpointRunIdFromScenarioId(scenarioId: string, baseScenarioId: string): string | null {
  const prefix = `${baseScenarioId}:`;
  if (!scenarioId.startsWith(prefix)) return null;
  const runId = scenarioId.slice(prefix.length);
  return LIVE_RUN_ID_PATTERN.test(runId) ? runId : null;
}

function shouldRestoreFixtureCheckpointMode(run: PersistedFixtureCheckpointRun): boolean {
  return run.enabled || fixtureCheckpointNeedsReconciliation(run.activity) ||
    (run.activity !== null && run.resolution === 'awaiting');
}

function isFixtureCheckpointActivity(value: unknown): value is FixtureCheckpointActivity {
  if (typeof value !== 'object' || value === null) return false;
  const activity = value as Record<string, unknown>;
  if (typeof activity.scenarioId !== 'string' || typeof activity.customerId !== 'string' ||
      typeof activity.actionId !== 'string' ||
      !['completed', 'in_progress', 'ambiguous', 'failed', 'rejected', 'unresolved'].includes(String(activity.status))) return false;
  if (activity.result === undefined) return true;
  if (typeof activity.result !== 'object' || activity.result === null) return false;
  const result = activity.result as Record<string, unknown>;
  return (result.processResult === 'APPROVED' || result.processResult === 'DECLINED') &&
    (typeof result.failureReason === 'string' || result.failureReason === null) &&
    typeof result.cardTransactionEventId === 'string' &&
    typeof result.cardTransactionId === 'string' &&
    typeof result.cardTransactionLifecycleId === 'string';
}

function persistFixtureCheckpointRun(scenarioId: string, run: PersistedFixtureCheckpointRun): boolean {
  try {
    window.sessionStorage.setItem(`${FIXTURE_CHECKPOINT_STORAGE_PREFIX}${scenarioId}`, JSON.stringify({
      runId: run.runId,
      activity: run.activity,
      resolution: run.resolution,
      enabled: run.enabled,
    }));
    return true;
  } catch {
    return false;
  }
}

function loadFixtureCheckpointRun(scenarioId: string): PersistedFixtureCheckpointRun {
  let stored: string | null;
  try {
    stored = window.sessionStorage.getItem(`${FIXTURE_CHECKPOINT_STORAGE_PREFIX}${scenarioId}`);
  } catch {
    return { ...newFixtureCheckpointRun(), identityKnown: false, identityPersisted: false };
  }
  if (stored) {
    try {
      const parsed = JSON.parse(stored) as Partial<PersistedFixtureCheckpointRun>;
      if (typeof parsed.runId === 'string' && LIVE_RUN_ID_PATTERN.test(parsed.runId) &&
          (parsed.activity === null || isFixtureCheckpointActivity(parsed.activity)) &&
          (parsed.resolution === 'awaiting' || parsed.resolution === 'confirmed' || parsed.resolution === 'fixture-only')) {
        const activity = parsed.activity ?? null;
        const enabled = typeof parsed.enabled === 'boolean'
          ? parsed.enabled
          : activity !== null || parsed.resolution !== 'awaiting';
        return { runId: parsed.runId, activity, resolution: parsed.resolution, enabled, identityKnown: true, identityPersisted: true };
      }
      return { ...newFixtureCheckpointRun(), identityKnown: false, identityPersisted: false };
    } catch {
      return { ...newFixtureCheckpointRun(), identityKnown: false, identityPersisted: false };
    }
  }
  const run: PersistedFixtureCheckpointRun = {
    ...newFixtureCheckpointRun(),
    identityKnown: true,
    identityPersisted: false,
  };
  run.identityPersisted = persistFixtureCheckpointRun(scenarioId, run);
  return run;
}

function loadSelectedScenarioId(scenarios: readonly Scenario[]): string {
  try {
    const selected = window.sessionStorage.getItem(SELECTED_SCENARIO_STORAGE_KEY);
    return selected && scenarios.some((scenario) => scenario.id === selected) ? selected : scenarios[0]!.id;
  } catch {
    return scenarios[0]!.id;
  }
}

function persistSelectedScenarioId(scenarioId: string): void {
  try {
    window.sessionStorage.setItem(SELECTED_SCENARIO_STORAGE_KEY, scenarioId);
  } catch {
    // Checkpoint submission separately requires a durable run identity.
  }
}

export function isFixtureCheckpointProviderConfirmed(activity: FixtureCheckpointActivity | null): boolean {
  return activity?.status === 'completed' && activity.result?.processResult === 'DECLINED' &&
    activity.result.failureReason === 'INSUFFICIENT_FUNDS';
}

export function fixtureCheckpointNeedsReconciliation(activity: FixtureCheckpointActivity | null): boolean {
  return activity !== null && activity !== undefined && ['in_progress', 'ambiguous', 'unresolved'].includes(activity.status);
}

/** The small set of actions allowed while the fixture sequence is paused at its provider checkpoint. */
export function fixtureCheckpointControls(input: {
  mode: 'fixture' | 'fixture-checkpoint' | 'sandbox-live';
  paused: boolean;
  pending: boolean;
  resolution: FixtureCheckpointResolution;
  activity: FixtureCheckpointActivity | null;
}): FixtureCheckpointControl | null {
  if (input.mode !== 'fixture-checkpoint' || !input.paused || input.pending || input.resolution !== 'awaiting') return null;
  if (fixtureCheckpointNeedsReconciliation(input.activity)) {
    return input.activity?.lifecycleId ? 'reconcile-attempt' : 'resolve-lifecycle';
  }
  if (isFixtureCheckpointProviderConfirmed(input.activity)) return 'continue-fixture';
  if (input.activity) return 'continue-fixture-only';
  return 'run-attempt';
}

export function fixtureCheckpointReceipt(activity: FixtureCheckpointActivity | null) {
  return {
    providerResult: activity?.result?.processResult ?? 'Unavailable',
    attemptStatus: activity?.status ?? 'not_started',
    failureReason: activity?.result ? activity.result.failureReason ?? 'None returned' : 'Unavailable',
    amountCents: activity?.attemptedAmount?.amountCents ?? null,
    currency: activity?.attemptedAmount?.currency ?? null,
    eventId: activity?.result?.cardTransactionEventId ?? 'Unavailable',
    transactionId: activity?.result?.cardTransactionId ?? 'Unavailable',
    lifecycleId: activity?.result?.cardTransactionLifecycleId ?? activity?.lifecycleId ?? 'Unavailable',
  };
}

export function fixtureCheckpointRecoveryState(
  activity: FixtureCheckpointActivity | null,
  resolution: FixtureCheckpointResolution,
  pending: boolean,
  paused: boolean,
): string {
  if (pending) return 'Waiting for Airwallex. The scenario clock and fixture decision are paused.';
  if (activity && fixtureCheckpointNeedsReconciliation(activity)) {
    return activity.lifecycleId
      ? 'Reconciliation required. Refresh the same Airwallex lifecycle before continuing.'
      : 'Airwallex returned no lifecycle ID. Find this attempt in the provider dashboard, then verify its lifecycle ID here. Do not run another provider attempt.';
  }
  if (resolution === 'confirmed') return 'The provider confirmed DECLINED / INSUFFICIENT_FUNDS. The presenter resumed the fixture sequence.';
  if (resolution === 'fixture-only') return 'The presenter chose fixture-only continuation. The provider result did not determine the fixture decision.';
  if (!paused) return 'The fixture sequence is running toward Bloom’s checkpoint.';
  if (isFixtureCheckpointProviderConfirmed(activity)) return 'Provider decline confirmed. Presenter continuation is required before the fixture event runs.';
  if (activity?.status === 'completed') return 'Provider result recorded. It did not confirm DECLINED / INSUFFICIENT_FUNDS. Fixture-only continuation is available.';
  if (activity) return 'The provider attempt did not return a completed receipt. Fixture-only continuation is available.';
  return 'Waiting for the presenter to run the Airwallex Sandbox attempt.';
}

function isLiveProviderActivity(value: unknown): value is LiveProviderActivity {
  return typeof value === 'object' && value !== null &&
    typeof (value as { status?: unknown }).status === 'string';
}

function persistLiveRun(scenarioId: string, run: PersistedLiveRun): void {
  try {
    window.sessionStorage.setItem(`${LIVE_RUN_STORAGE_PREFIX}${scenarioId}`, JSON.stringify(run));
  } catch {
    // Live actions still use unique in-memory run IDs when browser storage is unavailable.
  }
}

function loadLiveRun(scenarioId: string): PersistedLiveRun {
  try {
    const stored = window.sessionStorage.getItem(`${LIVE_RUN_STORAGE_PREFIX}${scenarioId}`);
    if (stored) {
      const parsed = JSON.parse(stored) as Partial<PersistedLiveRun>;
      if (typeof parsed.runId === 'string' && LIVE_RUN_ID_PATTERN.test(parsed.runId) &&
          typeof parsed.customerIndex === 'number' && Number.isSafeInteger(parsed.customerIndex) && parsed.customerIndex >= 0) {
        const runScenarioId = `${scenarioId}:${parsed.runId}`;
        const action = isLiveProviderActivity(parsed.action)
          ? { ...parsed.action, scenarioId: parsed.action.scenarioId ?? runScenarioId }
          : null;
        const history = Array.isArray(parsed.history)
          ? parsed.history.filter(isLiveProviderActivity).map((activity) => ({
            ...activity,
            scenarioId: activity.scenarioId ?? runScenarioId,
          }))
          : [];
        return { runId: parsed.runId, customerIndex: parsed.customerIndex, action, history };
      }
    }
  } catch {
    // Ignore an unavailable or malformed browser snapshot and begin a new run.
  }
  const run = { runId: globalThis.crypto.randomUUID(), customerIndex: 0, action: null, history: [] };
  persistLiveRun(scenarioId, run);
  return run;
}

export function upsertLiveHistory(history: readonly LiveProviderActivity[], activity: LiveProviderActivity): LiveProviderActivity[] {
  const index = history.findIndex((entry) => entry.attemptId === activity.attemptId);
  if (index < 0) return [...history, activity];
  return history.map((entry, entryIndex) => entryIndex === index ? { ...entry, ...activity } : entry);
}

export function liveActivityNeedsReconciliation(activity: LiveProviderActivity | null): boolean {
  if (!activity) return false;
  return activity.status !== 'completed' || ['evaluating', 'pending_review', 'reserving', 'reserved', 'settled', 'retry_submitting', 'retry_ambiguous'].includes(activity.policyStatus ?? '');
}

export function canApproveKit5Review(authenticated: boolean, policyStatus: string | undefined): boolean {
  return authenticated && policyStatus === 'pending_review';
}

export function canApproveKit5Decision(
  authenticated: boolean,
  policyStatus: string | undefined,
  decision: Kit5PolicyDecisionSummary | null | undefined,
): boolean {
  return canApproveKit5Review(authenticated, policyStatus) && decision?.outcome === 'human_review' &&
    Number.isSafeInteger(decision.amountCents) && decision.amountCents > 0 &&
    Number.isSafeInteger(decision.creditLineCents) && decision.creditLineCents >= 0 &&
    Boolean(decision.tier && decision.reasonCodes.length > 0);
}

export function canCreateNovaRepaymentDeposit(
  authenticated: boolean,
  activity: LiveProviderActivity | null | undefined,
): boolean {
  return authenticated && activity?.customerName === 'Nova Agency' && activity.status === 'completed' &&
    activity.result?.processResult === 'DECLINED' && activity.result.failureReason === 'INSUFFICIENT_FUNDS' &&
    activity.humanApproved === true && activity.policyStatus === 'retry_completed' && Boolean(activity.bridgeId) &&
    Boolean(activity.retryResult?.cardTransactionLifecycleId) && !activity.retryError;
}

export function useKit5Dashboard(apiBaseUrl = import.meta.env.VITE_BRIDGE_API_URL ?? '') {
  const scenarios = useMemo<Scenario[]>(() => loadScenarios(), []);
  const [scenarioId, setScenarioId] = useState(() => loadSelectedScenarioId(scenarios));
  const scenario = scenarios.find((s) => s.id === scenarioId) ?? scenarios[0];
  const [initialLiveRun] = useState(() => loadLiveRun(scenario.id));
  const [initialFixtureCheckpointRun] = useState(() => loadFixtureCheckpointRun(scenario.id));
  const [inputs, setInputs] = useState<ScenarioInputs>(() => structuredClone(scenario.inputs));
  const [state, setState] = useState<Kit5State>(() => initialKit5State(scenario.inputs));
  const [ledgerProjectionResult, setLedgerProjectionResult] = useState<{ url: string; data: LedgerProjectionResponse } | null>(null);
  const [ledgerProjectionFailure, setLedgerProjectionFailure] = useState<{ url: string; message: string } | null>(null);
  const [ledgerProjectionLoadedUrl, setLedgerProjectionLoadedUrl] = useState('');
  const [mode, setMode] = useState<'fixture' | 'fixture-checkpoint' | 'sandbox-live'>(() =>
    scenario.providerCheckpoint && shouldRestoreFixtureCheckpointMode(initialFixtureCheckpointRun) ? 'fixture-checkpoint' : 'fixture');
  const [fixtureCheckpointRunId, setFixtureCheckpointRunId] = useState(initialFixtureCheckpointRun.runId);
  const [fixtureCheckpointActivity, setFixtureCheckpointActivity] = useState<FixtureCheckpointActivity | null>(initialFixtureCheckpointRun.activity);
  const [fixtureCheckpointResolution, setFixtureCheckpointResolution] = useState<FixtureCheckpointResolution>(initialFixtureCheckpointRun.resolution);
  const [fixtureCheckpointIdentityKnown, setFixtureCheckpointIdentityKnown] = useState(initialFixtureCheckpointRun.identityKnown);
  const [fixtureCheckpointIdentityPersisted, setFixtureCheckpointIdentityPersisted] = useState(initialFixtureCheckpointRun.identityPersisted);
  const [fixtureCheckpointStartNew, setFixtureCheckpointStartNew] = useState(false);
  const [fixtureCheckpointError, setFixtureCheckpointError] = useState<string | null>(null);
  const [fixtureCheckpointPending, setFixtureCheckpointPending] = useState(false);
  const [liveRunId, setLiveRunId] = useState(initialLiveRun.runId);
  const [liveCustomerIndex, setLiveCustomerIndex] = useState(initialLiveRun.customerIndex);
  const [liveAction, setLiveAction] = useState<LiveProviderActivity | null>(initialLiveRun.action);
  const [liveHistory, setLiveHistory] = useState<LiveProviderActivity[]>(initialLiveRun.history);
  const [liveError, setLiveError] = useState<string | null>(null);
  const [livePending, setLivePending] = useState(false);
  const liveSubmissionRef = useRef(false);
  const fixtureCheckpointSubmissionRef = useRef(false);
  const liveGenerationRef = useRef(0);
  const [operatorAuthenticated, setOperatorAuthenticated] = useState(false);
  const [operatorApprovalAvailable, setOperatorApprovalAvailable] = useState(false);
  const liveScenarioId = `${scenario.id}:${liveRunId}`;
  const fixtureCheckpointScenarioId = `${scenario.id}:${fixtureCheckpointRunId}`;
  const fixtureCheckpointCustomer = scenario.providerCheckpoint
    ? scenario.inputs.customers.find((customer) => customer.key === scenario.providerCheckpoint?.customerKey)
    : undefined;
  const fixtureCheckpointRequest = useMemo<Kit5FixtureCheckpointRequest | null>(() => scenario.providerCheckpoint && fixtureCheckpointCustomer
    ? {
      scenarioId: fixtureCheckpointScenarioId,
      customerId: fixtureCheckpointCustomer.id,
      actionId: `${fixtureCheckpointScenarioId}:${scenario.providerCheckpoint.eventRequestId}`,
    }
    : null, [fixtureCheckpointCustomer, fixtureCheckpointScenarioId, scenario.providerCheckpoint]);
  const fixtureCheckpointTermsCompatible = fixtureCheckpointTermsMatch(scenario, inputs);

  useEffect(() => {
    persistLiveRun(scenario.id, {
      runId: liveRunId,
      customerIndex: liveCustomerIndex,
      action: liveAction,
      history: liveHistory,
    });
  }, [liveAction, liveCustomerIndex, liveHistory, liveRunId, scenario.id]);

  useEffect(() => {
    if (!fixtureCheckpointIdentityKnown) return;
    persistFixtureCheckpointRun(scenario.id, {
      runId: fixtureCheckpointRunId,
      activity: fixtureCheckpointActivity,
      resolution: fixtureCheckpointResolution,
      enabled: mode === 'fixture-checkpoint',
      identityKnown: fixtureCheckpointIdentityKnown,
      identityPersisted: fixtureCheckpointIdentityPersisted,
    });
  }, [fixtureCheckpointActivity, fixtureCheckpointIdentityKnown, fixtureCheckpointIdentityPersisted, fixtureCheckpointResolution, fixtureCheckpointRunId, mode, scenario.id]);

  // Optional read-only server projection. It is deliberately separate from the
  // deterministic scenario state, which remains the demo's source of behavior.
  useEffect(() => {
    if (!apiBaseUrl) return;
    let active = true;
    void new BridgeApiClient(apiBaseUrl).getLedgerProjection()
      .then((projection) => {
        if (active) setLedgerProjectionResult({ url: apiBaseUrl, data: projection });
      })
      .catch((error: unknown) => {
        if (active) setLedgerProjectionFailure({
          url: apiBaseUrl,
          message: error instanceof Error ? error.message : 'Ledger projection unavailable',
        });
      })
      .finally(() => {
        if (active) setLedgerProjectionLoadedUrl(apiBaseUrl);
      });
    return () => { active = false; };
  }, [apiBaseUrl]);

  const ledgerProjection = ledgerProjectionResult && ledgerProjectionResult.url === apiBaseUrl
    ? ledgerProjectionResult.data
    : null;
  const ledgerProjectionError = ledgerProjectionFailure && ledgerProjectionFailure.url === apiBaseUrl
    ? ledgerProjectionFailure.message
    : null;
  const ledgerProjectionLoading = Boolean(apiBaseUrl) && ledgerProjectionLoadedUrl !== apiBaseUrl;

  // Declines arrive one at a time; the sequence stops for the CRM step, a
  // credit officer, or a repayment (which the presenter triggers).
  const fixtureCheckpointPaused = shouldPauseForFixtureCheckpoint(mode, scenario, state, fixtureCheckpointResolution);

  useEffect(() => {
    if ((mode !== 'fixture' && mode !== 'fixture-checkpoint') || !canAutoAdvance(state) || fixtureCheckpointPaused) return;
    const t = setTimeout(() => setState((s) => (
      shouldPauseForFixtureCheckpoint(mode, scenario, s, fixtureCheckpointResolution) ? s : step(s)
    )), ARRIVAL_MS);
    return () => clearTimeout(t);
  }, [fixtureCheckpointPaused, fixtureCheckpointResolution, mode, scenario, state]);

  const selectScenario = useCallback((id: string) => {
    if (fixtureCheckpointPending || fixtureCheckpointSubmissionRef.current ||
        fixtureCheckpointNeedsReconciliation(fixtureCheckpointActivity) ||
        (fixtureCheckpointActivity !== null && fixtureCheckpointResolution === 'awaiting') ||
        (fixtureCheckpointPaused && id !== scenario.id)) return;
    const next = scenarios.find((s) => s.id === id);
    if (!next) return;
    const liveRun = loadLiveRun(id);
    const checkpointRun = loadFixtureCheckpointRun(id);
    setScenarioId(id);
    liveGenerationRef.current += 1;
    liveSubmissionRef.current = false;
    fixtureCheckpointSubmissionRef.current = false;
    setFixtureCheckpointRunId(checkpointRun.runId);
    setFixtureCheckpointActivity(checkpointRun.activity);
    setFixtureCheckpointStartNew(false);
    setFixtureCheckpointResolution(checkpointRun.resolution);
    setFixtureCheckpointIdentityKnown(checkpointRun.identityKnown);
    setFixtureCheckpointIdentityPersisted(checkpointRun.identityPersisted);
    setFixtureCheckpointError(null);
    setFixtureCheckpointPending(false);
    setLiveRunId(liveRun.runId);
    setLiveCustomerIndex(liveRun.customerIndex);
    setLiveAction(liveRun.action);
    setLiveHistory(liveRun.history);
    setLiveError(null);
    setLivePending(false);
    setInputs(structuredClone(next.inputs));
    setState(initialKit5State(next.inputs));
    persistSelectedScenarioId(id);
    if (next.providerCheckpoint && shouldRestoreFixtureCheckpointMode(checkpointRun)) {
      setMode('fixture-checkpoint');
    } else if (mode === 'fixture-checkpoint') {
      setMode('fixture');
    }
  }, [fixtureCheckpointActivity, fixtureCheckpointPaused, fixtureCheckpointPending, fixtureCheckpointResolution, mode, scenario.id, scenarios]);

  const applyInputs = useCallback((next: ScenarioInputs) => {
    if (fixtureCheckpointPending || fixtureCheckpointSubmissionRef.current ||
        fixtureCheckpointNeedsReconciliation(fixtureCheckpointActivity) ||
        (fixtureCheckpointActivity !== null && fixtureCheckpointResolution === 'awaiting')) return;
    liveGenerationRef.current += 1;
    liveSubmissionRef.current = false;
    fixtureCheckpointSubmissionRef.current = false;
    const nextCheckpointRun = {
      ...newFixtureCheckpointRun(),
      enabled: mode === 'fixture-checkpoint' && fixtureCheckpointTermsMatch(scenario, next),
      identityKnown: fixtureCheckpointIdentityKnown,
      identityPersisted: false,
    };
    nextCheckpointRun.identityPersisted = fixtureCheckpointIdentityKnown &&
      persistFixtureCheckpointRun(scenario.id, nextCheckpointRun);
    setFixtureCheckpointRunId(nextCheckpointRun.runId);
    setFixtureCheckpointIdentityPersisted(nextCheckpointRun.identityPersisted);
    setFixtureCheckpointActivity(null);
    setFixtureCheckpointStartNew(false);
    setLiveCustomerIndex(0);
    setLiveAction(null);
    setLiveHistory([]);
    setLiveError(null);
    setLivePending(false);
    setFixtureCheckpointResolution('awaiting');
    setFixtureCheckpointError(null);
    setFixtureCheckpointPending(false);
    setInputs(structuredClone(next));
    setState(initialKit5State(next));
    if (mode === 'fixture-checkpoint' && !fixtureCheckpointTermsMatch(scenario, next)) setMode('fixture');
  }, [fixtureCheckpointActivity, fixtureCheckpointIdentityKnown, fixtureCheckpointPending, fixtureCheckpointResolution, mode, scenario]);

  const scan = useCallback(() => setState((s) => start(s)), []);
  const advance = useCallback(() => setState((s) => (
    shouldPauseForFixtureCheckpoint(mode, scenario, s, fixtureCheckpointResolution) ? s : step(s)
  )), [fixtureCheckpointResolution, mode, scenario]);
  const reset = useCallback(() => setState(initialKit5State(inputs)), [inputs]);

  const selectCustomer = useCallback((requestId: string | null) => {
    setState((s) => ({ ...s, selectedCustomerId: s.selectedCustomerId === requestId ? null : requestId }));
  }, []);

  const crmReveal = useCallback(() => setState((s) => revealCrm(s)), []);
  const crmReEvaluate = useCallback(() => setState((s) => reEvaluateCrm(s)), []);
  const approve = useCallback((requestId: string) => setState((s) => approveRequest(s, requestId)), []);
  const continueAfterAuto = useCallback(() => setState((s) => acknowledge(s)), []);

  const recoverFixtureCheckpointFromServer = useCallback(async () => {
    if (!apiBaseUrl) return;
    const recovered = await new BridgeApiClient(apiBaseUrl).recoverFixtureCheckpoint();
    if (recovered.status === 'not_found') return;
    const checkpointScenario = scenarios.find((candidate) =>
      fixtureCheckpointRunIdFromScenarioId(recovered.scenarioId, candidate.id) !== null && candidate.providerCheckpoint);
    const recoveredRunId = checkpointScenario
      ? fixtureCheckpointRunIdFromScenarioId(recovered.scenarioId, checkpointScenario.id)
      : null;
    if (!checkpointScenario || !recoveredRunId) {
      setFixtureCheckpointError('The server has a saved checkpoint, but its scenario identity is not recognized. No new provider attempt will be submitted.');
      return;
    }
    const recoveredActivity: FixtureCheckpointActivity = {
      scenarioId: recovered.scenarioId,
      customerId: recovered.customerId,
      actionId: recovered.actionId,
      status: recovered.status,
      attemptId: recovered.attemptId,
      lifecycleId: recovered.lifecycleId,
      attemptedAmount: recovered.attemptedAmount,
      result: recovered.result,
    };
    const recoveredInputs = structuredClone(checkpointScenario.inputs);
    liveGenerationRef.current += 1;
    setScenarioId(checkpointScenario.id);
    persistSelectedScenarioId(checkpointScenario.id);
    setInputs(recoveredInputs);
    setState(start(initialKit5State(recoveredInputs)));
    setFixtureCheckpointRunId(recoveredRunId);
    setFixtureCheckpointActivity(recoveredActivity);
    setFixtureCheckpointResolution('awaiting');
    setFixtureCheckpointStartNew(false);
    setFixtureCheckpointIdentityKnown(true);
    setMode('fixture-checkpoint');
    const persisted = persistFixtureCheckpointRun(checkpointScenario.id, {
      runId: recoveredRunId,
      activity: recoveredActivity,
      resolution: 'awaiting',
      enabled: true,
      identityKnown: true,
      identityPersisted: true,
    });
    setFixtureCheckpointIdentityPersisted(persisted);
  }, [apiBaseUrl, scenarios]);

  const fixtureCheckpointProviderConfirmed = isFixtureCheckpointProviderConfirmed(fixtureCheckpointActivity);
  const fixtureCheckpointUnresolved = fixtureCheckpointNeedsReconciliation(fixtureCheckpointActivity);

  const liveCustomers = sortEvents(inputs.events)
    .filter((event) => event.type === 'decline')
    .map((event) => ({ event, customer: inputs.customers.find((item) => item.key === event.customerKey) }))
    .filter((item): item is typeof item & { customer: NonNullable<typeof item.customer> } => Boolean(item.customer));
  const nextLiveCustomer = liveCustomers[liveCustomerIndex];
  const liveBlocked = livePending || (liveActivityNeedsReconciliation(liveAction) && liveAction?.status !== 'not_persisted');
  const liveComplete = liveCustomerIndex >= liveCustomers.length;

  const restartLiveSequence = useCallback(() => {
    if (liveBlocked || liveAction?.status === 'not_persisted' || liveSubmissionRef.current ||
        fixtureCheckpointPending || fixtureCheckpointSubmissionRef.current ||
        fixtureCheckpointNeedsReconciliation(fixtureCheckpointActivity)) return;
    liveGenerationRef.current += 1;
    liveSubmissionRef.current = false;
    const nextRun: PersistedLiveRun = {
      runId: globalThis.crypto.randomUUID(),
      customerIndex: 0,
      action: null,
      history: liveHistory,
    };
    persistLiveRun(scenario.id, nextRun);
    setLiveRunId(nextRun.runId);
    setLiveCustomerIndex(nextRun.customerIndex);
    setLiveAction(nextRun.action);
    setLiveHistory(nextRun.history);
    setLiveError(null);
    setLivePending(false);
  }, [fixtureCheckpointActivity, fixtureCheckpointPending, liveAction?.status, liveBlocked, liveHistory, scenario.id]);

  const selectMode = useCallback((next: 'fixture' | 'fixture-checkpoint' | 'sandbox-live') => {
    if (liveSubmissionRef.current || liveActivityNeedsReconciliation(liveAction) ||
        fixtureCheckpointSubmissionRef.current || fixtureCheckpointNeedsReconciliation(fixtureCheckpointActivity) ||
        (fixtureCheckpointPaused && next !== 'fixture-checkpoint') ||
        (fixtureCheckpointActivity !== null && fixtureCheckpointResolution === 'awaiting')) return;
    if (next === 'fixture-checkpoint' && (!scenario.providerCheckpoint || !fixtureCheckpointTermsCompatible)) return;
    setMode(next);
    const persisted = fixtureCheckpointIdentityKnown && persistFixtureCheckpointRun(scenario.id, {
      runId: fixtureCheckpointRunId,
      activity: fixtureCheckpointActivity,
      resolution: fixtureCheckpointResolution,
      enabled: next === 'fixture-checkpoint',
      identityKnown: fixtureCheckpointIdentityKnown,
      identityPersisted: fixtureCheckpointIdentityPersisted,
    });
    if (persisted) setFixtureCheckpointIdentityPersisted(true);
  }, [fixtureCheckpointActivity, fixtureCheckpointIdentityKnown, fixtureCheckpointIdentityPersisted, fixtureCheckpointPaused, fixtureCheckpointResolution, fixtureCheckpointRunId, fixtureCheckpointTermsCompatible, liveAction, scenario.id, scenario.providerCheckpoint]);

  const confirmFixtureCheckpoint = useCallback(() => {
    if (!shouldPauseForFixtureCheckpoint(mode, scenario, state, fixtureCheckpointResolution) ||
        !isFixtureCheckpointProviderConfirmed(fixtureCheckpointActivity)) return;
    setFixtureCheckpointResolution('confirmed');
    setFixtureCheckpointError(null);
  }, [fixtureCheckpointActivity, fixtureCheckpointResolution, mode, scenario, state]);

  const continueFixtureOnly = useCallback(() => {
    if (!shouldPauseForFixtureCheckpoint(mode, scenario, state, fixtureCheckpointResolution) ||
        fixtureCheckpointNeedsReconciliation(fixtureCheckpointActivity)) return;
    setFixtureCheckpointResolution('fixture-only');
  }, [fixtureCheckpointActivity, fixtureCheckpointResolution, mode, scenario, state]);

  const submitFixtureCheckpoint = useCallback(async () => {
    if (mode !== 'fixture-checkpoint' || !apiBaseUrl || !operatorAuthenticated || !fixtureCheckpointRequest ||
        !fixtureCheckpointPaused || fixtureCheckpointResolution !== 'awaiting' || fixtureCheckpointPending ||
        fixtureCheckpointSubmissionRef.current || fixtureCheckpointActivity?.status === 'completed' ||
        !fixtureCheckpointTermsCompatible) return;
    if (!fixtureCheckpointIdentityKnown) {
      setFixtureCheckpointError('Checkpoint recovery data could not be read from session storage. Keep this run blocked until the prior provider attempt can be reconciled.');
      return;
    }
    if (!fixtureCheckpointIdentityPersisted) {
      const persisted = persistFixtureCheckpointRun(scenario.id, {
        runId: fixtureCheckpointRunId,
        activity: fixtureCheckpointActivity,
        resolution: fixtureCheckpointResolution,
        enabled: true,
        identityKnown: true,
        identityPersisted: false,
      });
      if (!persisted) {
        setFixtureCheckpointError('Session storage is unavailable. The Airwallex checkpoint was not submitted because its identity must survive a reload.');
        return;
      }
      setFixtureCheckpointIdentityPersisted(true);
    }
    const generation = liveGenerationRef.current;
    fixtureCheckpointSubmissionRef.current = true;
    setFixtureCheckpointPending(true);
    setFixtureCheckpointError(null);
    try {
      const response = await new BridgeApiClient(apiBaseUrl).fixtureCheckpoint({
        ...fixtureCheckpointRequest,
        ...(fixtureCheckpointStartNew ? { startNew: true } : {}),
      });
      if (generation !== liveGenerationRef.current) return;
      if (response.status === 'rejected') {
        setFixtureCheckpointActivity({
          ...fixtureCheckpointRequest,
          status: 'rejected',
          attemptId: fixtureCheckpointRequest.actionId,
          reason: response.reason,
        });
        setFixtureCheckpointError(`Airwallex checkpoint rejected: ${response.reason}`);
        return;
      }
      const activity: FixtureCheckpointActivity = {
        scenarioId: response.scenarioId ?? fixtureCheckpointRequest.scenarioId,
        customerId: response.customerId ?? fixtureCheckpointRequest.customerId,
        actionId: response.actionId ?? fixtureCheckpointRequest.actionId,
        status: response.status,
        attemptId: response.attemptId,
        lifecycleId: response.lifecycleId,
        attemptedAmount: response.attemptedAmount,
        result: response.result,
      };
      const adoptedRunId = response.scenarioId
        ? fixtureCheckpointRunIdFromScenarioId(response.scenarioId, scenario.id)
        : fixtureCheckpointRunId;
      if (!adoptedRunId) {
        setFixtureCheckpointError('The server returned an invalid checkpoint recovery identity. No new provider attempt will be submitted.');
        return;
      }
      setFixtureCheckpointRunId(adoptedRunId);
      setFixtureCheckpointStartNew(false);
      setFixtureCheckpointIdentityKnown(true);
      setFixtureCheckpointIdentityPersisted(true);
      setMode('fixture-checkpoint');
      setFixtureCheckpointActivity(activity);
      if (response.status === 'completed') {
        setFixtureCheckpointError(isFixtureCheckpointProviderConfirmed(activity)
          ? null
          : `Airwallex returned ${response.result.processResult}${response.result.failureReason ? ` / ${response.result.failureReason}` : ''}; this does not confirm an insufficient-funds decline.`);
      } else if (response.status === 'failed') {
        setFixtureCheckpointError('The provider attempt failed before returning a transaction result. Fixture-only continuation is available.');
      } else {
        setFixtureCheckpointError(`Airwallex checkpoint status is ${response.status}; reconcile the same attempt before continuing.`);
      }
    } catch (error) {
      if (generation !== liveGenerationRef.current) return;
      setFixtureCheckpointActivity({
        ...fixtureCheckpointRequest,
        status: 'unresolved',
        attemptId: fixtureCheckpointRequest.actionId,
      });
      setFixtureCheckpointError(error instanceof Error
        ? `${error.message}. The result is unresolved. Find the provider lifecycle ID and verify it before starting another attempt.`
        : 'Airwallex checkpoint result is unresolved. Find the provider lifecycle ID and verify it before starting another attempt.');
    } finally {
      if (generation === liveGenerationRef.current) {
        fixtureCheckpointSubmissionRef.current = false;
        setFixtureCheckpointPending(false);
      }
    }
  }, [apiBaseUrl, fixtureCheckpointActivity, fixtureCheckpointIdentityKnown, fixtureCheckpointIdentityPersisted, fixtureCheckpointPaused, fixtureCheckpointPending, fixtureCheckpointRequest, fixtureCheckpointResolution, fixtureCheckpointRunId, fixtureCheckpointStartNew, fixtureCheckpointTermsCompatible, mode, operatorAuthenticated, scenario.id]);

  const runFixtureCheckpoint = useCallback(async () => {
    if (fixtureCheckpointActivity?.status === 'completed') return;
    await submitFixtureCheckpoint();
  }, [fixtureCheckpointActivity?.status, submitFixtureCheckpoint]);

  const reconcileFixtureCheckpoint = useCallback(async () => {
    if (!fixtureCheckpointNeedsReconciliation(fixtureCheckpointActivity)) return;
    // Replaying the same provider-only POST with the saved identity lets the
    // server reconcile its durable attempt without entering bridge recovery.
    await submitFixtureCheckpoint();
  }, [fixtureCheckpointActivity, submitFixtureCheckpoint]);

  const reconcileFixtureCheckpointLifecycle = useCallback(async (lifecycleId: string) => {
    const normalizedLifecycleId = lifecycleId.trim();
    if (!/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(normalizedLifecycleId) ||
        !fixtureCheckpointRequest || !operatorAuthenticated || !apiBaseUrl ||
        !fixtureCheckpointIdentityKnown || !fixtureCheckpointNeedsReconciliation(fixtureCheckpointActivity) || fixtureCheckpointPending ||
        fixtureCheckpointSubmissionRef.current) return;
    const generation = liveGenerationRef.current;
    fixtureCheckpointSubmissionRef.current = true;
    setFixtureCheckpointPending(true);
    setFixtureCheckpointError(null);
    try {
      const response = await new BridgeApiClient(apiBaseUrl).reconcileFixtureCheckpointLifecycle({
        ...fixtureCheckpointRequest,
        lifecycleId: normalizedLifecycleId,
      } satisfies Kit5FixtureCheckpointLifecycleRequest);
      if (generation !== liveGenerationRef.current) return;
      if (response.status === 'rejected') {
        setFixtureCheckpointError(`Airwallex lifecycle was not accepted: ${response.reason}. The original attempt remains unresolved.`);
        return;
      }
      const activity: FixtureCheckpointActivity = {
        ...fixtureCheckpointRequest,
        status: response.status,
        attemptId: response.attemptId,
        lifecycleId: response.lifecycleId,
        attemptedAmount: response.attemptedAmount,
        result: response.result,
      };
      setFixtureCheckpointActivity(activity);
      setFixtureCheckpointError(response.status === 'completed'
        ? (isFixtureCheckpointProviderConfirmed(activity) ? null : 'Lifecycle verified, but the result does not confirm DECLINED / INSUFFICIENT_FUNDS.')
        : 'Airwallex did not return a verifiable lifecycle result. The original attempt remains unresolved; no new provider attempt was created.');
    } catch (error) {
      if (generation !== liveGenerationRef.current) return;
      setFixtureCheckpointError(error instanceof Error ? error.message : 'Airwallex lifecycle could not be verified.');
    } finally {
      if (generation === liveGenerationRef.current) {
        fixtureCheckpointSubmissionRef.current = false;
        setFixtureCheckpointPending(false);
      }
    }
  }, [apiBaseUrl, fixtureCheckpointActivity, fixtureCheckpointIdentityKnown, fixtureCheckpointPending, fixtureCheckpointRequest, operatorAuthenticated]);

  const startNewFixtureCheckpointRun = useCallback(() => {
    if (fixtureCheckpointPending || fixtureCheckpointSubmissionRef.current ||
        !fixtureCheckpointActivity || fixtureCheckpointResolution === 'awaiting' ||
        fixtureCheckpointNeedsReconciliation(fixtureCheckpointActivity) || !fixtureCheckpointIdentityKnown) return;
    liveGenerationRef.current += 1;
    const nextRun: PersistedFixtureCheckpointRun = {
      runId: globalThis.crypto.randomUUID(),
      activity: null,
      resolution: 'awaiting',
      enabled: mode === 'fixture-checkpoint',
      identityKnown: true,
      identityPersisted: false,
    };
    nextRun.identityPersisted = persistFixtureCheckpointRun(scenario.id, nextRun);
    if (!nextRun.identityPersisted) {
      setFixtureCheckpointError('Session storage is unavailable. A new Airwallex run was not started because its identity must survive a reload.');
      return;
    }
    setFixtureCheckpointRunId(nextRun.runId);
    setFixtureCheckpointIdentityKnown(true);
    setFixtureCheckpointIdentityPersisted(true);
    setFixtureCheckpointActivity(null);
    setFixtureCheckpointStartNew(true);
    setFixtureCheckpointResolution('awaiting');
    setFixtureCheckpointError(null);
    setFixtureCheckpointPending(false);
    setState(initialKit5State(inputs));
  }, [fixtureCheckpointActivity, fixtureCheckpointIdentityKnown, fixtureCheckpointPending, fixtureCheckpointResolution, inputs, mode, scenario.id]);

  const authenticateOperator = useCallback(async (token: string) => {
    if (!apiBaseUrl) throw new Error('Set VITE_BRIDGE_API_URL to enable operator login.');
    try {
      const session = await new BridgeApiClient(apiBaseUrl).createOperatorSession(token);
      setOperatorAuthenticated(true);
      setOperatorApprovalAvailable(session.approvalAvailable);
      if (scenario.providerCheckpoint && mode === 'fixture-checkpoint' &&
          (!fixtureCheckpointActivity || fixtureCheckpointNeedsReconciliation(fixtureCheckpointActivity))) {
        try {
          await recoverFixtureCheckpointFromServer();
        } catch (error) {
          setFixtureCheckpointError(error instanceof Error
            ? `Could not recover the saved Airwallex checkpoint: ${error.message}`
            : 'Could not recover the saved Airwallex checkpoint.');
        }
      }
    } catch (error) {
      setLiveError(error instanceof Error ? error.message : 'Operator login failed.');
      throw error;
    }
  }, [apiBaseUrl, fixtureCheckpointActivity, mode, recoverFixtureCheckpointFromServer, scenario.providerCheckpoint]);

  const submitNextCustomer = useCallback(async () => {
    if (mode !== 'sandbox-live' || !apiBaseUrl || !operatorAuthenticated || !nextLiveCustomer || liveBlocked || liveSubmissionRef.current) return;
    const generation = liveGenerationRef.current;
    liveSubmissionRef.current = true;
    setLivePending(true);
    setLiveError(null);
    try {
      const response = await new BridgeApiClient(apiBaseUrl).nextCustomer({
        scenarioId: liveScenarioId,
        customerId: nextLiveCustomer.customer.id,
        actionId: `${liveScenarioId}:live:${nextLiveCustomer.event.requestId}`,
      });
      if (generation !== liveGenerationRef.current) return;
      if (response.status === 'rejected') {
        setLiveError(`Provider action rejected: ${response.reason}`);
        return;
      }
      if (response.status !== 'completed') {
        const activity = {
          scenarioId: liveScenarioId,
          status: response.status,
          attemptId: response.attemptId,
          customerId: nextLiveCustomer.customer.id,
          lifecycleId: response.lifecycleId,
          amount: response.attemptedAmount ? response.attemptedAmount.amountCents / 100 : undefined,
          customerName: nextLiveCustomer.customer.name,
          result: response.result,
          policyStatus: response.policyStatus,
          policyError: response.policyError,
          policyDecisionId: response.policyDecisionId,
          bridgeId: response.bridgeId,
          retryLifecycleId: response.retryLifecycleId,
          retryResult: response.retryResult,
          retryError: response.retryError,
          policyDecision: response.policyDecision,
          humanApproved: response.humanApproved,
        } satisfies LiveProviderActivity;
        setLiveAction(activity);
        if (activity.result) setLiveHistory((history) => upsertLiveHistory(history, activity));
        setLiveError(`Provider action ${response.status}; reconcile this attempt before continuing.`);
        return;
      }
      const activity = {
        scenarioId: liveScenarioId,
        status: response.status,
        attemptId: response.attemptId,
        customerId: nextLiveCustomer.customer.id,
        result: response.result,
        policyStatus: response.policyStatus,
        policyError: response.policyError,
        policyDecisionId: response.policyDecisionId,
        bridgeId: response.bridgeId,
        retryLifecycleId: response.retryLifecycleId,
        retryResult: response.retryResult,
        retryError: response.retryError,
        policyDecision: response.policyDecision,
        humanApproved: response.humanApproved,
        amount: response.attemptedAmount ? response.attemptedAmount.amountCents / 100 : undefined,
        customerName: nextLiveCustomer.customer.name,
      } satisfies LiveProviderActivity;
      setLiveAction(activity);
      setLiveHistory((history) => upsertLiveHistory(history, activity));
      setLiveCustomerIndex((index) => index + 1);
    } catch (error) {
      if (generation !== liveGenerationRef.current) return;
      setLiveAction({
        status: 'unresolved',
        scenarioId: liveScenarioId,
        attemptId: `${liveScenarioId}:live:${nextLiveCustomer.event.requestId}`,
        customerId: nextLiveCustomer.customer.id,
        customerName: nextLiveCustomer.customer.name,
      });
      setLiveError(error instanceof Error ? error.message : 'Provider action failed; reconcile before continuing.');
    } finally {
      if (generation === liveGenerationRef.current) {
        liveSubmissionRef.current = false;
        setLivePending(false);
      }
    }
  }, [apiBaseUrl, liveBlocked, liveScenarioId, mode, nextLiveCustomer, operatorAuthenticated]);

  const refreshLiveAction = useCallback(async () => {
    if (!apiBaseUrl || !operatorAuthenticated || !liveAction?.attemptId || !liveAction.customerId || livePending) return;
    const generation = liveGenerationRef.current;
    setLivePending(true);
    setLiveError(null);
    try {
      const response = await new BridgeApiClient(apiBaseUrl).refreshAttempt({
        scenarioId: liveAction.scenarioId ?? liveScenarioId,
        customerId: liveAction.customerId,
        actionId: liveAction.attemptId,
      });
      if (generation !== liveGenerationRef.current) return;
      const activity = {
        ...liveAction,
        status: response.status,
        lifecycleId: response.status === 'completed' ? response.result.cardTransactionLifecycleId : response.lifecycleId,
        amount: response.attemptedAmount ? response.attemptedAmount.amountCents / 100 : liveAction.amount,
        result: response.result ?? liveAction.result,
        policyStatus: response.policyStatus,
        policyError: response.policyError,
        policyDecisionId: response.policyDecisionId,
        bridgeId: response.bridgeId,
        retryLifecycleId: response.retryLifecycleId,
        retryResult: response.retryResult,
        retryError: response.retryError,
        policyDecision: response.policyDecision,
        humanApproved: response.humanApproved,
      } satisfies LiveProviderActivity;
      setLiveAction(activity);
      const priorHistory = liveHistory.find((entry) => entry.attemptId === activity.attemptId);
      if (activity.result) setLiveHistory((history) => upsertLiveHistory(history, activity));
      if (response.status === 'completed' && priorHistory?.status !== 'completed') {
        setLiveCustomerIndex((index) => index + 1);
      } else if (response.status !== 'completed') {
        setLiveError(`Provider action ${response.status}; refresh again before continuing.`);
      }
    } catch (error) {
      if (generation === liveGenerationRef.current) {
        if (error instanceof BridgeApiError && error.status === 404) {
          setLiveAction((action) => action?.attemptId === liveAction.attemptId
            ? { ...action, status: 'not_persisted' }
            : action);
          setLiveError('No durable attempt exists. Retry this same customer and action safely.');
        } else {
          setLiveError(error instanceof Error ? error.message : 'Status refresh failed.');
        }
      }
    } finally {
      if (generation === liveGenerationRef.current) setLivePending(false);
    }
  }, [apiBaseUrl, liveAction, liveHistory, livePending, liveScenarioId, operatorAuthenticated]);

  const refreshLiveHistoryActivity = useCallback(async (activity: LiveProviderActivity) => {
    if (!apiBaseUrl || !operatorAuthenticated || !activity.attemptId || !activity.customerId || livePending) return;
    const generation = liveGenerationRef.current;
    setLivePending(true);
    setLiveError(null);
    try {
      const response = await new BridgeApiClient(apiBaseUrl).refreshAttempt({
        scenarioId: activity.scenarioId ?? liveScenarioId, customerId: activity.customerId, actionId: activity.attemptId,
      });
      if (generation !== liveGenerationRef.current) return;
      const updated = { ...activity, status: response.status, lifecycleId: response.lifecycleId ?? activity.lifecycleId,
        result: response.result ?? activity.result,
        amount: response.attemptedAmount ? response.attemptedAmount.amountCents / 100 : activity.amount,
        policyStatus: response.policyStatus, policyDecisionId: response.policyDecisionId,
        policyError: response.policyError,
        policyDecision: response.policyDecision,
        bridgeId: response.bridgeId, retryLifecycleId: response.retryLifecycleId,
        retryResult: response.retryResult, retryError: response.retryError } satisfies LiveProviderActivity;
      setLiveHistory((history) => upsertLiveHistory(history, updated));
      if (liveAction?.attemptId === updated.attemptId) {
        setLiveAction(updated);
        if (response.status === 'completed' && activity.status !== 'completed') setLiveCustomerIndex((index) => index + 1);
      }
      if (response.status !== 'completed') setLiveError(`Provider action ${response.status}; refresh again before continuing.`);
    } catch (error) {
      if (generation === liveGenerationRef.current) setLiveError(error instanceof Error ? error.message : 'Status refresh failed.');
    } finally {
      if (generation === liveGenerationRef.current) setLivePending(false);
    }
  }, [apiBaseUrl, liveAction, livePending, liveScenarioId, operatorAuthenticated]);

  const approveLiveReview = useCallback(async (reason: string, target?: LiveProviderActivity) => {
    const approvalTarget = target ?? liveAction;
    if (!apiBaseUrl || !operatorAuthenticated || !approvalTarget?.attemptId || !approvalTarget.customerId ||
        !canApproveKit5Review(operatorAuthenticated, approvalTarget.policyStatus) || livePending) return;
    const approvalScenarioId = approvalTarget.scenarioId ?? liveScenarioId;
    const generation = liveGenerationRef.current;
    setLivePending(true);
    setLiveError(null);
    try {
      const response = await new BridgeApiClient(apiBaseUrl).approveHumanReview({
        scenarioId: approvalScenarioId, customerId: approvalTarget.customerId, actionId: approvalTarget.attemptId, reason,
      });
      if (generation !== liveGenerationRef.current) return;
      const status = await new BridgeApiClient(apiBaseUrl).refreshAttempt({
        scenarioId: approvalScenarioId, customerId: approvalTarget.customerId, actionId: approvalTarget.attemptId,
      });
      if (generation !== liveGenerationRef.current) return;
      const activity = { ...approvalTarget, status: status.status, result: status.result ?? approvalTarget.result,
        policyStatus: status.policyStatus, policyDecisionId: status.policyDecisionId,
        policyError: status.policyError,
        policyDecision: status.policyDecision,
        humanApproved: status.humanApproved,
        bridgeId: status.bridgeId, retryLifecycleId: status.retryLifecycleId,
        retryResult: status.retryResult, retryError: status.retryError } satisfies LiveProviderActivity;
      if (liveAction?.attemptId === activity.attemptId) setLiveAction(activity);
      if (activity.result) setLiveHistory((history) => upsertLiveHistory(history, activity));
      if (response.status === 'pending') setLiveError('Approval is recorded. Waiting for the signed transfer settlement event.');
      if (response.status === 'blocked') setLiveError('Approval was blocked because the current policy terms changed or are unavailable.');
    } catch (error) {
      if (generation === liveGenerationRef.current) setLiveError(error instanceof Error ? error.message : 'Approval failed.');
    } finally {
      if (generation === liveGenerationRef.current) setLivePending(false);
    }
  }, [apiBaseUrl, liveAction, livePending, liveScenarioId, operatorAuthenticated]);

  const createOrRefreshNovaRepaymentDeposit = useCallback(async (target: LiveProviderActivity) => {
    if (!apiBaseUrl || !operatorAuthenticated || !target.customerId || !target.attemptId ||
        !canCreateNovaRepaymentDeposit(operatorAuthenticated, target) || livePending) return;
    const targetScenarioId = target.scenarioId ?? liveScenarioId;
    const generation = liveGenerationRef.current;
    setLivePending(true);
    setLiveError(null);
    try {
      const deposit = await new BridgeApiClient(apiBaseUrl).createOrRefreshNovaRepaymentDeposit({
        scenarioId: targetScenarioId, customerId: target.customerId, actionId: target.attemptId,
      });
      if (generation !== liveGenerationRef.current) return;
      const updated = { ...target, repaymentDeposit: deposit, repaymentDepositError: deposit.error };
      setLiveHistory((history) => upsertLiveHistory(history, updated));
      if (liveAction?.attemptId === updated.attemptId) setLiveAction(updated);
      if (deposit.status === 'ambiguous' || deposit.status === 'blocked') {
        setLiveError(deposit.error ?? `Nova deposit status is ${deposit.status}; refresh after reconciliation.`);
      }
    } catch (error) {
      if (generation !== liveGenerationRef.current) return;
      const message = error instanceof Error ? error.message : 'Nova deposit request failed.';
      const updated = { ...target, repaymentDepositError: message };
      setLiveHistory((history) => upsertLiveHistory(history, updated));
      if (liveAction?.attemptId === updated.attemptId) setLiveAction(updated);
      setLiveError(message.includes('mapping')
        ? 'Nova deposit is blocked because an active connected-account and USD Global Account mapping has not been verified.'
        : message);
    } finally {
      if (generation === liveGenerationRef.current) setLivePending(false);
    }
  }, [apiBaseUrl, liveAction, livePending, liveScenarioId, operatorAuthenticated]);

  return {
    scenarios,
    scenario,
    inputs,
    state,
    ledgerApiEnabled: Boolean(apiBaseUrl),
    ledgerProjection,
    ledgerProjectionLoading,
    ledgerProjectionError,
    mode,
    fixtureCheckpointResolution,
    fixtureCheckpointPaused,
    fixtureCheckpointRunId,
    fixtureCheckpointScenarioId,
    fixtureCheckpointActivity,
    fixtureCheckpointTermsCompatible,
    fixtureCheckpointIdentityKnown,
    fixtureCheckpointIdentityPersisted,
    fixtureCheckpointProviderConfirmed,
    fixtureCheckpointUnresolved,
    fixtureCheckpointPending,
    fixtureCheckpointError,
    liveAction,
    liveHistory,
    liveError,
    livePending,
    operatorAuthenticated,
    operatorApprovalAvailable,
    liveBlocked,
    nextLiveCustomer,
    liveComplete,
    selectScenario,
    applyInputs,
    scan,
    advance,
    reset,
    restartLiveSequence,
    selectCustomer,
    crmReveal,
    crmReEvaluate,
    approve,
    continueAfterAuto,
    confirmFixtureCheckpoint,
    continueFixtureOnly,
    runFixtureCheckpoint,
    reconcileFixtureCheckpoint,
    reconcileFixtureCheckpointLifecycle,
    startNewFixtureCheckpointRun,
    selectMode,
    submitNextCustomer,
    authenticateOperator,
    refreshLiveAction,
    refreshLiveHistoryActivity,
    approveLiveReview,
    createOrRefreshNovaRepaymentDeposit,
  };
}
