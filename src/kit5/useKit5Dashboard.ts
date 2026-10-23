import { useState, useCallback, useEffect, useMemo, useRef } from 'react';
import type { Scenario, ScenarioInputs, Kit5State } from './types.js';
import type { Kit5PolicyDecisionSummary, Kit5ProviderTransaction, Kit5RepaymentDepositResponse, LedgerProjectionResponse } from '../services/types.js';
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

export function isFixtureCheckpointEventNext(
  scenario: Scenario,
  state: Kit5State,
): boolean {
  const checkpoint = scenario.providerCheckpoint;
  const next = state.events[state.cursor];
  return checkpoint?.provider === 'airwallex' && next?.type === 'decline' &&
    next.requestId === checkpoint.eventRequestId;
}

export function shouldPauseForFixtureCheckpoint(
  mode: 'fixture' | 'fixture-checkpoint' | 'sandbox-live',
  scenario: Scenario,
  state: Kit5State,
  resolution: FixtureCheckpointResolution,
): boolean {
  return mode === 'fixture-checkpoint' && resolution === 'awaiting' &&
    isFixtureCheckpointEventNext(scenario, state);
}

interface PersistedLiveRun {
  runId: string;
  customerIndex: number;
  action: LiveProviderActivity | null;
  history: LiveProviderActivity[];
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
  const [scenarioId, setScenarioId] = useState(scenarios[0].id);
  const scenario = scenarios.find((s) => s.id === scenarioId) ?? scenarios[0];
  const [initialLiveRun] = useState(() => loadLiveRun(scenarios[0].id));
  const [inputs, setInputs] = useState<ScenarioInputs>(() => structuredClone(scenario.inputs));
  const [state, setState] = useState<Kit5State>(() => initialKit5State(scenario.inputs));
  const [ledgerProjectionResult, setLedgerProjectionResult] = useState<{ url: string; data: LedgerProjectionResponse } | null>(null);
  const [ledgerProjectionFailure, setLedgerProjectionFailure] = useState<{ url: string; message: string } | null>(null);
  const [ledgerProjectionLoadedUrl, setLedgerProjectionLoadedUrl] = useState('');
  const [mode, setMode] = useState<'fixture' | 'fixture-checkpoint' | 'sandbox-live'>('fixture');
  const [fixtureCheckpointResolution, setFixtureCheckpointResolution] = useState<FixtureCheckpointResolution>('awaiting');
  const [liveRunId, setLiveRunId] = useState(initialLiveRun.runId);
  const [liveCustomerIndex, setLiveCustomerIndex] = useState(initialLiveRun.customerIndex);
  const [liveAction, setLiveAction] = useState<LiveProviderActivity | null>(initialLiveRun.action);
  const [liveHistory, setLiveHistory] = useState<LiveProviderActivity[]>(initialLiveRun.history);
  const [liveError, setLiveError] = useState<string | null>(null);
  const [livePending, setLivePending] = useState(false);
  const liveSubmissionRef = useRef(false);
  const liveGenerationRef = useRef(0);
  const [operatorAuthenticated, setOperatorAuthenticated] = useState(false);
  const [operatorApprovalAvailable, setOperatorApprovalAvailable] = useState(false);
  const liveScenarioId = `${scenario.id}:${liveRunId}`;

  useEffect(() => {
    persistLiveRun(scenario.id, {
      runId: liveRunId,
      customerIndex: liveCustomerIndex,
      action: liveAction,
      history: liveHistory,
    });
  }, [liveAction, liveCustomerIndex, liveHistory, liveRunId, scenario.id]);

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
    const next = scenarios.find((s) => s.id === id);
    if (!next) return;
    const liveRun = loadLiveRun(id);
    setScenarioId(id);
    if (id !== scenarioId) setFixtureCheckpointResolution('awaiting');
    liveGenerationRef.current += 1;
    liveSubmissionRef.current = false;
    setLiveRunId(liveRun.runId);
    setLiveCustomerIndex(liveRun.customerIndex);
    setLiveAction(liveRun.action);
    setLiveHistory(liveRun.history);
    setLiveError(null);
    setLivePending(false);
    setInputs(structuredClone(next.inputs));
    setState(initialKit5State(next.inputs));
  }, [scenarioId, scenarios]);

  const applyInputs = useCallback((next: ScenarioInputs) => {
    liveGenerationRef.current += 1;
    liveSubmissionRef.current = false;
    setLiveCustomerIndex(0);
    setLiveAction(null);
    setLiveHistory([]);
    setLiveError(null);
    setLivePending(false);
    setFixtureCheckpointResolution('awaiting');
    setInputs(structuredClone(next));
    setState(initialKit5State(next));
  }, []);

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

  const liveCustomers = sortEvents(inputs.events)
    .filter((event) => event.type === 'decline')
    .map((event) => ({ event, customer: inputs.customers.find((item) => item.key === event.customerKey) }))
    .filter((item): item is typeof item & { customer: NonNullable<typeof item.customer> } => Boolean(item.customer));
  const nextLiveCustomer = liveCustomers[liveCustomerIndex];
  const liveBlocked = livePending || (liveActivityNeedsReconciliation(liveAction) && liveAction?.status !== 'not_persisted');
  const liveComplete = liveCustomerIndex >= liveCustomers.length;

  const restartLiveSequence = useCallback(() => {
    if (liveBlocked || liveAction?.status === 'not_persisted' || liveSubmissionRef.current) return;
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
  }, [liveAction?.status, liveBlocked, liveHistory, scenario.id]);

  const selectMode = useCallback((next: 'fixture' | 'fixture-checkpoint' | 'sandbox-live') => {
    if (liveSubmissionRef.current || liveActivityNeedsReconciliation(liveAction)) return;
    setMode(next);
  }, [liveAction]);

  const confirmFixtureCheckpoint = useCallback(() => {
    if (!shouldPauseForFixtureCheckpoint(mode, scenario, state, fixtureCheckpointResolution)) return;
    setFixtureCheckpointResolution('confirmed');
  }, [fixtureCheckpointResolution, mode, scenario, state]);

  const continueFixtureOnly = useCallback(() => {
    if (!shouldPauseForFixtureCheckpoint(mode, scenario, state, fixtureCheckpointResolution)) return;
    setFixtureCheckpointResolution('fixture-only');
  }, [fixtureCheckpointResolution, mode, scenario, state]);

  const authenticateOperator = useCallback(async (token: string) => {
    if (!apiBaseUrl) throw new Error('Set VITE_BRIDGE_API_URL to enable operator login.');
    try {
      const session = await new BridgeApiClient(apiBaseUrl).createOperatorSession(token);
      setOperatorAuthenticated(true);
      setOperatorApprovalAvailable(session.approvalAvailable);
    } catch (error) {
      setLiveError(error instanceof Error ? error.message : 'Operator login failed.');
      throw error;
    }
  }, [apiBaseUrl]);

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
        actionId: `${liveScenarioId}:${nextLiveCustomer.event.requestId}`,
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
        attemptId: `${liveScenarioId}:${nextLiveCustomer.event.requestId}`,
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
    selectMode,
    submitNextCustomer,
    authenticateOperator,
    refreshLiveAction,
    refreshLiveHistoryActivity,
    approveLiveReview,
    createOrRefreshNovaRepaymentDeposit,
  };
}
