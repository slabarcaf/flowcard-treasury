import { useState, useCallback, useRef } from 'react';
import type { DashboardState } from '../state/types.js';
import {
  initialState,
  scanDeclines,
  processCustomer,
  revealBloomCrm,
  reEvaluateBloom,
  fundCustomer,
  completeFunding,
  simulateCollection,
  reset,
} from '../state/machine.js';

export function useDashboard() {
  const [state, setState] = useState<DashboardState>(initialState);
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  const scan = useCallback(() => {
    setState((s) => (s.phase === 'idle' ? scanDeclines(s) : s));
  }, []);

  const selectCustomer = useCallback((customerId: string | null) => {
    setState((s) => {
      // Toggle selection
      if (s.selectedCustomerId === customerId) {
        return { ...s, selectedCustomerId: null };
      }
      // If not yet processed, run the pipeline
      if (customerId && !s.processedCustomers.includes(customerId)) {
        const processed = processCustomer(s, customerId);
        return { ...processed, selectedCustomerId: customerId };
      }
      return { ...s, selectedCustomerId: customerId };
    });
  }, []);

  const bloomReveal = useCallback(() => {
    setState((s) => revealBloomCrm(s));
  }, []);

  const bloomReEvaluate = useCallback(() => {
    setState((s) => reEvaluateBloom(s));
  }, []);

  const approve = useCallback((customerId: string) => {
    // Step 1: Mark approved + notification
    setState((s) => fundCustomer(s, customerId));

    // Step 2: After 1.5s delay, complete funding
    if (timerRef.current) clearTimeout(timerRef.current);
    timerRef.current = setTimeout(() => {
      setState((s) => completeFunding(s, customerId));
      timerRef.current = null;
    }, 3000);
  }, []);

  const collect = useCallback(() => {
    setState((s) => simulateCollection(s));
  }, []);

  const doReset = useCallback(() => {
    if (timerRef.current) {
      clearTimeout(timerRef.current);
      timerRef.current = null;
    }
    setState(reset());
  }, []);

  return { state, scan, selectCustomer, bloomReveal, bloomReEvaluate, approve, collect, reset: doReset };
}
