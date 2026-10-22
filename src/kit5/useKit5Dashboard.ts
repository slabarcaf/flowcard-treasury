import { useState, useCallback, useEffect, useMemo } from 'react';
import type { Scenario, ScenarioInputs, Kit5State } from './types.js';
import type { LedgerProjectionResponse } from '../services/types.js';
import { BridgeApiClient } from '../services/http/BridgeApiClient.js';
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

export function useKit5Dashboard(apiBaseUrl = import.meta.env.VITE_BRIDGE_API_URL ?? '') {
  const scenarios = useMemo<Scenario[]>(() => loadScenarios(), []);
  const [scenarioId, setScenarioId] = useState(scenarios[0].id);
  const scenario = scenarios.find((s) => s.id === scenarioId) ?? scenarios[0];
  const [inputs, setInputs] = useState<ScenarioInputs>(() => structuredClone(scenario.inputs));
  const [state, setState] = useState<Kit5State>(() => initialKit5State(scenario.inputs));
  const [ledgerProjectionResult, setLedgerProjectionResult] = useState<{ url: string; data: LedgerProjectionResponse } | null>(null);
  const [ledgerProjectionFailure, setLedgerProjectionFailure] = useState<{ url: string; message: string } | null>(null);
  const [ledgerProjectionLoadedUrl, setLedgerProjectionLoadedUrl] = useState('');

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
    if (!canAutoAdvance(state)) return;
    const t = setTimeout(() => setState((s) => step(s)), ARRIVAL_MS);
    return () => clearTimeout(t);
  }, [state]);

  const selectScenario = useCallback((id: string) => {
    const next = scenarios.find((s) => s.id === id);
    if (!next) return;
    setScenarioId(id);
    setInputs(structuredClone(next.inputs));
    setState(initialKit5State(next.inputs));
  }, [scenarios]);

  const applyInputs = useCallback((next: ScenarioInputs) => {
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

  return {
    scenarios,
    scenario,
    inputs,
    state,
    ledgerApiEnabled: Boolean(apiBaseUrl),
    ledgerProjection,
    ledgerProjectionLoading,
    ledgerProjectionError,
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
  };
}
