import { useState, useCallback, useEffect, useMemo } from 'react';
import type { Scenario, ScenarioInputs, Kit5State } from './types.js';
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

export function useKit5Dashboard() {
  const scenarios = useMemo<Scenario[]>(() => loadScenarios(), []);
  const [scenarioId, setScenarioId] = useState(scenarios[0].id);
  const scenario = scenarios.find((s) => s.id === scenarioId) ?? scenarios[0];
  const [inputs, setInputs] = useState<ScenarioInputs>(() => structuredClone(scenario.inputs));
  const [state, setState] = useState<Kit5State>(() => initialKit5State(scenario.inputs));

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
