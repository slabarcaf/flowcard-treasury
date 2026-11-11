import { useMemo } from 'react';
import { industryOptions } from '../services/onboarding/industryReference.js';
import snapshot from '../services/onboarding/industry-categories.json';

/** Real exported reference data; requires no new endpoint in Ramil's backend. */
export function useIndustryOptions() {
  const options = useMemo(() => industryOptions(snapshot), []);
  return { options, loading: false, error: options.length === 0 };
}
