import { useState, useCallback, useEffect, useMemo, useRef } from 'react';
import type { Scenario, ScenarioInputs, Kit5State } from './types.js';
import type { Kit5ProviderTransaction, LedgerProjectionResponse } from '../services/types.js';
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
} from './engine.js';

/** Pause between arrivals while the sequence plays itself. */
const ARRIVAL_MS = 1400;

interface LiveProviderActivity {
  status: string;
  attemptId?: string;
  customerId?: string;
  lifecycleId?: string | null;
  result?: Kit5ProviderTransaction;
  amount?: number;
  customerName?: string;
}

export function useKit5Dashboard(apiBaseUrl = import.meta.env.VITE_BRIDGE_API_URL ?? '') {
  const scenarios = useMemo<Scenario[]>(() => loadScenarios(), []);
  const [scenarioId, setScenarioId] = useState(scenarios[0].id);
  const scenario = scenarios.find((s) => s.id === scenarioId) ?? scenarios[0];
  const [inputs, setInputs] = useState<ScenarioInputs>(() => structuredClone(scenario.inputs));
  const [state, setState] = useState<Kit5State>(() => initialKit5State(scenario.inputs));
  const [ledgerProjectionResult, setLedgerProjectionResult] = useState<{ url: string; data: LedgerProjectionResponse } | null>(null);
  const [ledgerProjectionFailure, setLedgerProjectionFailure] = useState<{ url: string; message: string } | null>(null);
  const [ledgerProjectionLoadedUrl, setLedgerProjectionLoadedUrl] = useState('');
  const [mode, setMode] = useState<'fixture' | 'sandbox-live'>('fixture');
  const [liveCustomerIndex, setLiveCustomerIndex] = useState(0);
  const [liveAction, setLiveAction] = useState<LiveProviderActivity | null>(null);
  const [liveHistory, setLiveHistory] = useState<LiveProviderActivity[]>([]);
  const [liveError, setLiveError] = useState<string | null>(null);
  const [livePending, setLivePending] = useState(false);
  const liveSubmissionRef = useRef(false);
  const liveGenerationRef = useRef(0);
  const [operatorAuthenticated, setOperatorAuthenticated] = useState(false);

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
  useEffect(() => {
    if (mode !== 'fixture' || !canAutoAdvance(state)) return;
    const t = setTimeout(() => setState((s) => step(s)), ARRIVAL_MS);
    return () => clearTimeout(t);
  }, [mode, state]);

  const selectScenario = useCallback((id: string) => {
    const next = scenarios.find((s) => s.id === id);
    if (!next) return;
    setScenarioId(id);
    liveGenerationRef.current += 1;
    liveSubmissionRef.current = false;
    setLiveCustomerIndex(0);
    setLiveAction(null);
    setLiveHistory([]);
    setLiveError(null);
    setLivePending(false);
    setInputs(structuredClone(next.inputs));
    setState(initialKit5State(next.inputs));
  }, [scenarios]);

  const applyInputs = useCallback((next: ScenarioInputs) => {
    liveGenerationRef.current += 1;
    liveSubmissionRef.current = false;
    setLiveCustomerIndex(0);
    setLiveAction(null);
    setLiveHistory([]);
    setLiveError(null);
    setLivePending(false);
    setInputs(structuredClone(next));
    setState(initialKit5State(next));
  }, []);

  const scan = useCallback(() => setState((s) => start(s)), []);
  const advance = useCallback(() => setState((s) => step(s)), []);
  const reset = useCallback(() => setState(initialKit5State(inputs)), [inputs]);

  const selectCustomer = useCallback((requestId: string | null) => {
    setState((s) => ({ ...s, selectedCustomerId: s.selectedCustomerId === requestId ? null : requestId }));
  }, []);

  const crmReveal = useCallback(() => setState((s) => revealCrm(s)), []);
  const crmReEvaluate = useCallback(() => setState((s) => reEvaluateCrm(s)), []);
  const approve = useCallback((requestId: string) => setState((s) => approveRequest(s, requestId)), []);
  const continueAfterAuto = useCallback(() => setState((s) => acknowledge(s)), []);

  const liveCustomers = scenario.inputs.events
    .filter((event) => event.type === 'decline')
    .map((event) => ({ event, customer: scenario.inputs.customers.find((item) => item.key === event.customerKey) }))
    .filter((item): item is typeof item & { customer: NonNullable<typeof item.customer> } => Boolean(item.customer));
  const nextLiveCustomer = liveCustomers[liveCustomerIndex];
  const liveBlocked = livePending || (liveAction !== null && liveAction.status !== 'completed' && liveAction.status !== 'not_persisted');

  const selectMode = useCallback((next: 'fixture' | 'sandbox-live') => {
    if (liveSubmissionRef.current || (liveAction !== null && liveAction.status !== 'completed')) return;
    setMode(next);
    setLiveCustomerIndex(0);
    setLiveAction(null);
    setLiveHistory([]);
    setLiveError(null);
    setLivePending(false);
  }, [liveAction]);

  const authenticateOperator = useCallback(async (token: string) => {
    if (!apiBaseUrl) throw new Error('Set VITE_BRIDGE_API_URL to enable operator login.');
    try {
      await new BridgeApiClient(apiBaseUrl).createOperatorSession(token);
      setOperatorAuthenticated(true);
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
        scenarioId: scenario.id,
        customerId: nextLiveCustomer.customer.id,
        actionId: `${scenario.id}:${nextLiveCustomer.event.requestId}`,
      });
      if (generation !== liveGenerationRef.current) return;
      if (response.status === 'rejected') {
        setLiveError(`Provider action rejected: ${response.reason}`);
        return;
      }
      if (response.status !== 'completed') {
        setLiveAction({
          status: response.status,
          attemptId: response.attemptId,
          customerId: nextLiveCustomer.customer.id,
          lifecycleId: response.lifecycleId,
          amount: response.attemptedAmount ? response.attemptedAmount.amountCents / 100 : undefined,
          customerName: nextLiveCustomer.customer.name,
        });
        setLiveError(`Provider action ${response.status}; reconcile this attempt before continuing.`);
        return;
      }
      const activity = {
        status: response.status,
        attemptId: response.attemptId,
        customerId: nextLiveCustomer.customer.id,
        result: response.result,
        amount: response.attemptedAmount ? response.attemptedAmount.amountCents / 100 : undefined,
        customerName: nextLiveCustomer.customer.name,
      } satisfies LiveProviderActivity;
      setLiveAction(activity);
      setLiveHistory((history) => [...history, activity]);
      setLiveCustomerIndex((index) => index + 1);
    } catch (error) {
      if (generation !== liveGenerationRef.current) return;
      setLiveAction({
        status: 'unresolved',
        attemptId: `${scenario.id}:${nextLiveCustomer.event.requestId}`,
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
  }, [apiBaseUrl, liveBlocked, mode, nextLiveCustomer, operatorAuthenticated, scenario.id]);

  const refreshLiveAction = useCallback(async () => {
    if (!apiBaseUrl || !operatorAuthenticated || !liveAction?.attemptId || !liveAction.customerId || livePending) return;
    const generation = liveGenerationRef.current;
    setLivePending(true);
    setLiveError(null);
    try {
      const response = await new BridgeApiClient(apiBaseUrl).refreshAttempt({
        scenarioId: scenario.id,
        customerId: liveAction.customerId,
        actionId: liveAction.attemptId,
      });
      if (generation !== liveGenerationRef.current) return;
      const activity = {
        ...liveAction,
        status: response.status,
        lifecycleId: response.status === 'completed' ? response.result.cardTransactionLifecycleId : response.lifecycleId,
        amount: response.attemptedAmount ? response.attemptedAmount.amountCents / 100 : liveAction.amount,
        result: response.status === 'completed' ? response.result : undefined,
      } satisfies LiveProviderActivity;
      setLiveAction(activity);
      if (response.status === 'completed' && !liveHistory.some((entry) => entry.attemptId === activity.attemptId)) {
        setLiveHistory((history) => [...history, activity]);
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
  }, [apiBaseUrl, liveAction, liveHistory, livePending, operatorAuthenticated, scenario.id]);

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
    liveAction,
    liveHistory,
    liveError,
    livePending,
    operatorAuthenticated,
    liveBlocked,
    nextLiveCustomer,
    liveComplete: liveCustomerIndex >= liveCustomers.length,
    selectScenario,
    applyInputs,
    scan,
    advance,
    reset,
    selectCustomer,
    crmReveal,
    crmReEvaluate,
    approve,
    continueAfterAuto,
    selectMode,
    submitNextCustomer,
    authenticateOperator,
    refreshLiveAction,
  };
}
