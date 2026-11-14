import { useCallback, useEffect, useRef, useState } from 'react';
import type { ApplicationsListState, View } from '../useBackOffice.js';
import { OnboardingApiClient, OnboardingApiError } from '../../../../services/onboarding/OnboardingApiClient.js';
import type { ApplicationRecord, CompanyRecord, SubmissionRecord } from '../../../../services/onboarding/contracts.js';
import { color, space } from '../tokens.js';
import { Button, Card } from '../components/primitives.js';
import { OnboardingApplicationDetails } from '../components/OnboardingApplicationDetails.js';

export function ApplicationDetailScreen({
  applicationId,
  listState,
  go,
}: {
  applicationId: string;
  listState: ApplicationsListState;
  go: (view: View) => void;
}) {
  const api = useRef(new OnboardingApiClient()).current;
  const requestGeneration = useRef(0);
  const [recordId, setRecordId] = useState<string | null>(null);
  const [settledId, setSettledId] = useState<string | null>(null);
  const [application, setApplication] = useState<ApplicationRecord | null>(null);
  const [liveDraftCompany, setLiveDraftCompany] = useState<CompanyRecord | undefined>();
  const [companyBusy, setCompanyBusy] = useState(false);
  const [submissions, setSubmissions] = useState<SubmissionRecord[]>([]);
  const [busy, setBusy] = useState(true);
  const [historyBusy, setHistoryBusy] = useState(false);
  const [detailError, setDetailError] = useState('');
  const [companyError, setCompanyError] = useState('');
  const [historyError, setHistoryError] = useState('');

  const loadHistory = useCallback(async (id: string, request: number) => {
    setHistoryBusy(true);
    setHistoryError('');
    setSubmissions([]);
    try {
      const history = await api.listSubmissions(id);
      if (requestGeneration.current !== request) return;
      setSubmissions(history);
    } catch (caught) {
      if (requestGeneration.current !== request) return;
      setHistoryError(historyErrorMessage(caught));
    } finally {
      if (requestGeneration.current === request) setHistoryBusy(false);
    }
  }, [api]);

  const loadDraftCompany = useCallback(async (record: ApplicationRecord, request: number) => {
    setLiveDraftCompany(undefined);
    setCompanyError('');
    if (record.status !== 'DRAFT' || record.company) {
      setCompanyBusy(false);
      return;
    }
    setCompanyBusy(true);
    try {
      const company = await api.getCompany(record.company_id);
      if (requestGeneration.current === request) setLiveDraftCompany(company);
    } catch {
      if (requestGeneration.current === request) {
        setCompanyError('Current company details could not be loaded. Refresh the application to retry.');
      }
    } finally {
      if (requestGeneration.current === request) setCompanyBusy(false);
    }
  }, [api]);

  const loadApplication = useCallback(async () => {
    const request = ++requestGeneration.current;
    try {
      const record = await api.getApplication(applicationId);
      if (requestGeneration.current !== request) return;
      setApplication(record);
      setRecordId(applicationId);
      setSettledId(applicationId);
      setDetailError('');
      setLiveDraftCompany(undefined);
      setCompanyError('');
      setCompanyBusy(record.status === 'DRAFT' && !record.company);
      await loadHistory(applicationId, request);
      if (requestGeneration.current !== request) return;
      await loadDraftCompany(record, request);
    } catch (caught) {
      if (requestGeneration.current !== request) return;
      setSettledId(applicationId);
      setDetailError(applicationErrorMessage(caught));
    } finally {
      if (requestGeneration.current === request) setBusy(false);
    }
  }, [api, applicationId, loadDraftCompany, loadHistory]);

  const refreshApplication = useCallback(() => {
    setBusy(true);
    setDetailError('');
    void loadApplication();
  }, [loadApplication]);

  useEffect(() => {
    void loadApplication();

    return () => {
      requestGeneration.current += 1;
    };
  }, [loadApplication]);

  const currentApplication = recordId === applicationId ? application : null;

  const generationAtRender = requestGeneration.current;
  const handleUpdated = useCallback((updated: ApplicationRecord) => {
    if (updated.id !== applicationId || recordId !== applicationId ||
        requestGeneration.current !== generationAtRender) return;
    const request = ++requestGeneration.current;
    setBusy(false);
    setApplication(updated);
    setDetailError('');
    void loadHistory(applicationId, request);
    void loadDraftCompany(updated, request);
  }, [applicationId, generationAtRender, loadDraftCompany, loadHistory, recordId]);

  function backToApplications() {
    go({ screen: 'applications', listState: copyListState(listState) });
  }

  function retryHistory() {
    void loadHistory(applicationId, requestGeneration.current);
  }

  return (
    <div style={{ display: 'grid', gap: space.xl }}>
      <div>
        <Button onClick={backToApplications}>Back to applications</Button>
      </div>
      {currentApplication ? (
        <>
          {detailError && (
            <div role="alert" style={{ display: 'grid', justifyItems: 'start', gap: space.md }}>
              <p style={{ color: color.critical, margin: 0 }}>{detailError}</p>
              <Button onClick={() => void refreshApplication()} disabled={busy}>Retry</Button>
            </div>
          )}
          <OnboardingApplicationDetails
            application={currentApplication}
            liveDraftCompany={liveDraftCompany}
            companyError={companyError}
            companyBusy={companyBusy}
            submissions={submissions}
            historyBusy={historyBusy}
            historyError={historyError}
            onUpdated={handleUpdated}
            onRefresh={() => void refreshApplication()}
            onRetryHistory={retryHistory}
            refreshBusy={busy}
          />
        </>
      ) : settledId === applicationId && detailError ? (
        <Card>
          <p role="alert" style={{ color: color.critical }}>{detailError}</p>
          <div style={{ display: 'flex', gap: space.lg }}>
            <Button onClick={() => void refreshApplication()} disabled={busy}>Retry</Button>
          </div>
        </Card>
      ) : (
        <p role="status">Loading application</p>
      )}
    </div>
  );
}

function copyListState(state: ApplicationsListState): ApplicationsListState {
  return state.cursor === undefined
    ? { previousCursors: [...state.previousCursors] }
    : { cursor: state.cursor, previousCursors: [...state.previousCursors] };
}

function applicationErrorMessage(error: unknown): string {
  if (error instanceof OnboardingApiError) {
    const status = error.status > 0 ? `HTTP ${error.status}` : 'Network error';
    return `Application details could not be loaded (${status}, ${error.code}). ${error.message}`;
  }
  return 'Application details could not be loaded. Check the backend connection and try again.';
}

function historyErrorMessage(error: unknown): string {
  if (error instanceof OnboardingApiError) {
    const status = error.status > 0 ? `HTTP ${error.status}` : 'network error';
    return `Submission history could not be loaded (${status}, ${error.code}). ${error.message}`;
  }
  return 'Submission history could not be loaded. Check the backend connection and try again.';
}
