import { useEffect, useRef, useState } from 'react';
import { OnboardingApiClient, OnboardingApiError, isAmbiguousSaveFailure } from '../services/onboarding/OnboardingApiClient.js';
import { SaveApplication } from '../services/onboarding/SaveApplication.js';
import type { ApplicationRecord, CompanyInput, CompanyRecord, DocumentUpload, OnboardingDetailsInput, ReadinessResult, SubmissionReceipt, SubmissionRecord } from '../services/onboarding/contracts.js';

interface PendingSubmissionAttempt {
  applicationId: string;
  expectedRevision: number;
  idempotencyKey: string;
  retryable: boolean;
}

export function useCompanyApplication() {
  const api = useRef(new OnboardingApiClient()).current;
  const operationRef = useRef(new SaveApplication());
  const operation = operationRef.current;
  const inFlight = useRef(false);
  const pendingSubmissionRef = useRef<PendingSubmissionAttempt | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<OnboardingApiError | null>(null);
  const [company, setCompany] = useState<CompanyRecord | null>(null);
  const [savedDraft, setSavedDraft] = useState<ApplicationRecord | null>(null);
  const [submissionReceipt, setSubmissionReceipt] = useState<SubmissionReceipt | null>(null);
  const [submissionRecords, setSubmissionRecords] = useState<SubmissionRecord[]>([]);
  const [submissionStatusError, setSubmissionStatusError] = useState<OnboardingApiError | null>(null);
  const [pendingSubmission, setPendingSubmission] = useState<PendingSubmissionAttempt | null>(null);
  const [readiness, setReadiness] = useState<ReadinessResult | null>(null);
  const [canRecover, setCanRecover] = useState(false);
  useEffect(() => {
    setCanRecover(error !== null && operationRef.current.application !== null &&
      pendingSubmission === null && pendingSubmissionRef.current === null && submissionReceipt === null &&
      (error.code === 'STORAGE_UNAVAILABLE' || isAmbiguousSaveFailure(error)));
  }, [error, pendingSubmission, submissionReceipt]);
  const [uncertain, setUncertain] = useState(false);
  const unresolved = uncertain && error?.code !== 'STORAGE_UNAVAILABLE';

  async function refreshSubmittedStatus(receipt: SubmissionReceipt) {
    const [record, submissions] = await Promise.all([
      api.getApplication(receipt.application_id),
      api.listSubmissions(receipt.application_id),
    ]);
    if (record.id !== receipt.application_id || record.company_id !== operation.company?.id) {
      throw new OnboardingApiError(0, 'INVALID_RESPONSE');
    }
    const submission = submissions.find(item => item.submission_id === receipt.submission_id);
    if (!submission || submission.company.id !== operation.company?.id) {
      throw new OnboardingApiError(0, 'INVALID_RESPONSE');
    }
    operation.application = record;
    setSavedDraft(record);
    setSubmissionRecords(submissions);
    setSubmissionStatusError(null);
  }

  async function persistDraft(input: CompanyInput, details: OnboardingDetailsInput, uploads: DocumentUpload[]): Promise<ApplicationRecord> {
    const result = await operation.run(api, input, details, uploads, setCompany, setSavedDraft);
    setSavedDraft(result);
    window.dispatchEvent(new Event('flowcard:onboarding-saved'));
    return result;
  }

  async function saveDraft(input: CompanyInput, details: OnboardingDetailsInput, uploads: DocumentUpload[] = []) {
    if (inFlight.current || unresolved || pendingSubmissionRef.current || submissionReceipt) return;
    inFlight.current = true;
    setBusy(true);
    setError(null);
    setReadiness(null);
    setUncertain(false);
    try {
      await persistDraft(input, details, uploads);
    } catch (caught) {
      const failure = caught instanceof OnboardingApiError ? caught : new OnboardingApiError(0, 'UNKNOWN_RESULT');
      setError(failure);
      // Never blindly repeat an ambiguous create or update request.
      setUncertain(isAmbiguousSaveFailure(failure));
    } finally {
      inFlight.current = false;
      setBusy(false);
    }
  }

  async function refresh() {
    if (!operation.application || inFlight.current) return;
    inFlight.current = true;
    setBusy(true);
    setSubmissionStatusError(null);
    try {
      if (submissionReceipt) {
        await refreshSubmittedStatus(submissionReceipt);
        return;
      }
      const record = await api.getApplication(operation.application.id);
      if (record.company_id !== operation.company?.id) throw new OnboardingApiError(0, 'INVALID_RESPONSE');
      operation.application = record;
      setSavedDraft(record);
    } catch (caught) {
      setSubmissionStatusError(caught instanceof OnboardingApiError ? caught : new OnboardingApiError(0, 'NETWORK_RESULT_UNKNOWN'));
    } finally {
      inFlight.current = false;
      setBusy(false);
    }
  }

  async function recoverSavedApplication(): Promise<boolean> {
    if (!operation.application || inFlight.current || pendingSubmissionRef.current || submissionReceipt) return false;
    inFlight.current = true;
    setBusy(true);
    try {
      const record = await api.getApplication(operation.application.id);
      if (record.company_id !== operation.company?.id) throw new OnboardingApiError(0, 'INVALID_RESPONSE');
      operation.application = record;
      setSavedDraft(record);
      setError(null);
      setUncertain(false);
      window.dispatchEvent(new Event('flowcard:onboarding-saved'));
      return true;
    } catch (caught) {
      setError(caught instanceof OnboardingApiError ? caught : new OnboardingApiError(0, 'NETWORK_RESULT_UNKNOWN'));
      return false;
    } finally {
      inFlight.current = false;
      setBusy(false);
    }
  }

  async function submitWithAttempt(attempt: PendingSubmissionAttempt) {
    try {
      const receipt = await api.submitApplication(attempt.applicationId, attempt.expectedRevision, attempt.idempotencyKey);
      setSubmissionReceipt(receipt);
      pendingSubmissionRef.current = null;
      setPendingSubmission(null);
      setError(null);
      setUncertain(false);
      window.dispatchEvent(new Event('flowcard:onboarding-saved'));

      try {
        await refreshSubmittedStatus(receipt);
      } catch (caught) {
        // The durable 202 receipt is retained even when this read-only refresh fails.
        setSubmissionStatusError(caught instanceof OnboardingApiError ? caught : new OnboardingApiError(0, 'NETWORK_RESULT_UNKNOWN'));
      }
    } catch (caught) {
      const failure = caught instanceof OnboardingApiError ? caught : new OnboardingApiError(0, 'NETWORK_RESULT_UNKNOWN');
      setError(failure);
      if (failure.status === 422) {
        setReadiness({ code: failure.code, message: failure.responseMessage, errors: failure.issues });
        pendingSubmissionRef.current = null;
        setPendingSubmission(null);
        setUncertain(false);
      } else if (failure.code === 'REVISION_CONFLICT' || failure.code === 'APPLICATION_STATE_CONFLICT') {
        pendingSubmissionRef.current = null;
        setPendingSubmission(null);
        setUncertain(false);
      } else {
        const retryable = failure.code !== 'IDEMPOTENCY_CONFLICT';
        const retained = { ...attempt, retryable };
        pendingSubmissionRef.current = retained;
        setPendingSubmission(retained);
        setUncertain(true);
      }
    }
  }

  async function verifyAndSubmit(input: CompanyInput, details: OnboardingDetailsInput, uploads: DocumentUpload[]) {
    const pendingAttempt = pendingSubmissionRef.current;
    if (pendingAttempt) {
      await retrySubmission();
      return;
    }
    if (inFlight.current || unresolved || submissionReceipt) return;
    inFlight.current = true;
    setBusy(true);
    setError(null);
    setReadiness(null);
    setUncertain(false);
    try {
      let draft: ApplicationRecord;
      try {
        draft = await persistDraft(input, details, uploads);
      } catch (caught) {
        const failure = caught instanceof OnboardingApiError ? caught : new OnboardingApiError(0, 'UNKNOWN_RESULT');
        setError(failure);
        setUncertain(isAmbiguousSaveFailure(failure));
        return;
      }

      if (!operation.company || draft.company_id !== operation.company.id) {
        setError(new OnboardingApiError(0, 'INVALID_RESPONSE'));
        return;
      }

      let result: ReadinessResult;
      try {
        result = await api.verify(draft.id);
      } catch (caught) {
        setError(caught instanceof OnboardingApiError ? caught : new OnboardingApiError(0, 'NETWORK_RESULT_UNKNOWN'));
        return;
      }
      setReadiness(result);
      if (result.code !== 'VALIDATION_PASSED') return;
      const verifiedRevision = result.application_revision;
      if (typeof verifiedRevision !== 'number' || !Number.isInteger(verifiedRevision) || verifiedRevision < 1) {
        setError(new OnboardingApiError(0, 'INVALID_RESPONSE'));
        return;
      }

      const attempt: PendingSubmissionAttempt = {
        applicationId: draft.id,
        expectedRevision: verifiedRevision,
        idempotencyKey: crypto.randomUUID(),
        retryable: true,
      };
      pendingSubmissionRef.current = attempt;
      setPendingSubmission(attempt);
      await submitWithAttempt(attempt);
    } finally {
      inFlight.current = false;
      setBusy(false);
    }
  }

  async function retrySubmission() {
    const attempt = pendingSubmissionRef.current;
    if (!attempt || !attempt.retryable || inFlight.current) return;
    inFlight.current = true;
    setBusy(true);
    setError(null);
    await submitWithAttempt(attempt);
    inFlight.current = false;
    setBusy(false);
  }

  return {
    saveDraft,
    verifyAndSubmit,
    retrySubmission,
    refresh,
    recoverSavedApplication,
    canRecover,
    busy,
    error,
    clearError: () => setError(null),
    clearReadiness: () => { setReadiness(null); setError(null); },
    company,
    savedCompanyInput: operation.savedCompanySnapshot,
    savedDraft,
    submissionReceipt,
    submissionRecords,
    submissionStatusError,
    pendingSubmission,
    readiness,
    uncertain: unresolved,
    isUploaded: (key: string) => operation.isUploaded(key),
  };
}
