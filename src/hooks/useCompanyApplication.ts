import { useEffect, useRef, useState } from 'react';
import { OnboardingApiClient, OnboardingApiError, isAmbiguousSaveFailure } from '../services/onboarding/OnboardingApiClient.js';
import { SaveApplication } from '../services/onboarding/SaveApplication.js';
import type { ApplicationRecord, CompanyInput, CompanyRecord, DocumentUpload, OnboardingDetailsInput } from '../services/onboarding/contracts.js';

export function useCompanyApplication() {
  const api = useRef(new OnboardingApiClient()).current;
  const operationRef = useRef(new SaveApplication());
  const operation = operationRef.current;
  const inFlight = useRef(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<OnboardingApiError | null>(null);
  const [company, setCompany] = useState<CompanyRecord | null>(null);
  const [application, setApplication] = useState<ApplicationRecord | null>(null);
  const [canRecover, setCanRecover] = useState(false);
  useEffect(() => { setCanRecover(error !== null && operationRef.current.application !== null); }, [error]);
  const [uncertain, setUncertain] = useState(false);

  const unresolved = uncertain && error?.code !== 'STORAGE_UNAVAILABLE';

  async function save(input: CompanyInput, details: OnboardingDetailsInput, uploads: DocumentUpload[] = []) {
    if (inFlight.current || unresolved) return;
    setUncertain(false);
    inFlight.current = true;
    setBusy(true);
    setError(null);
    try {
      const result = await operation.run(api, input, details, uploads, setCompany);
      setApplication(result);
      window.dispatchEvent(new Event('flowcard:onboarding-saved'));
    } catch (caught) {
      const failure = caught instanceof OnboardingApiError ? caught : new OnboardingApiError(0, 'UNKNOWN_RESULT');
      setError(failure);
      // Creation has no client idempotency contract. Never blindly repeat an ambiguous POST.
      setUncertain(isAmbiguousSaveFailure(failure));
    } finally { inFlight.current = false; setBusy(false); }
  }
  async function recoverSavedApplication(): Promise<boolean> {
    if (!operation.application || inFlight.current) return false;
    inFlight.current = true; setBusy(true);
    try {
      // Read-only reconciliation: never repeat a company/application/file POST.
      const record = await api.getApplication(operation.application.id);
      if (record.company_id !== operation.company?.id) throw new OnboardingApiError(0, 'INVALID_RESPONSE');
      operation.application = record;
      setApplication(record); setError(null); setUncertain(false);
      window.dispatchEvent(new Event('flowcard:onboarding-saved'));
      return true;
    } catch (caught) {
      setError(caught instanceof OnboardingApiError ? caught : new OnboardingApiError(0, 'NETWORK_RESULT_UNKNOWN'));
      return false;
    } finally { inFlight.current = false; setBusy(false); }
  }
  async function refresh() {
    if (!operation.application || inFlight.current) return;
    inFlight.current = true;
    setBusy(true);
    setError(null);
    try {
      operation.application = await api.getApplication(operation.application.id);
      setApplication(operation.application);
    } catch (caught) {
      setError(caught instanceof OnboardingApiError ? caught : new OnboardingApiError(0, 'NETWORK_RESULT_UNKNOWN'));
    } finally { inFlight.current = false; setBusy(false); }
  }
  return { save, refresh, recoverSavedApplication, canRecover, busy, error, clearError: () => setError(null), company, application, uncertain: unresolved, isUploaded: (key: string) => operation.isUploaded(key) };
}
