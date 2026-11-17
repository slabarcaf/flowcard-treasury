import { useCallback, useEffect, useRef, useState } from 'react';
import type { CSSProperties, FormEvent } from 'react';
import { OnboardingApiClient, OnboardingApiError } from '../../../../services/onboarding/OnboardingApiClient.js';
import type { ApplicationRecord } from '../../../../services/onboarding/contracts.js';
import { applicationStatus } from '../../../../services/onboarding/contracts.js';
import type { ApplicationsListState } from '../useBackOffice.js';
import { color, font, radius, space } from '../tokens.js';
import { Badge, Button, Card, CardHeader, ClickableTr, Table, Td, Th } from './primitives.js';
import { formatTimestamp } from './onboarding-format.js';
import { BrowserLoginForm } from '../../../../components/onboarding/BrowserLoginForm.js';
import { browserGatewayEnabled } from '../../../../services/browser-session/client.js';

const control: CSSProperties = {
  color: color.text,
  background: color.surfaceSubtle,
  padding: space.lg,
  border: `1px solid ${color.borderStrong}`,
  borderRadius: radius.md,
  fontFamily: font.sans,
};

export function OnboardingApplications({
  listState,
  onPageChange,
  onOpenApplication,
}: {
  listState: ApplicationsListState;
  onPageChange: (nextState: ApplicationsListState) => void;
  onOpenApplication: (applicationId: string) => void;
}) {
  const [api] = useState(() => new OnboardingApiClient());
  const [applications, setApplications] = useState<ApplicationRecord[]>([]);
  const [nextCursor, setNextCursor] = useState<string | undefined>();
  const [busy, setBusy] = useState(true);
  const [error, setError] = useState('');
  const [loadedCursor, setLoadedCursor] = useState<string | undefined>();
  const [loadedRefreshVersion, setLoadedRefreshVersion] = useState(-1);
  const [showConnection, setShowConnection] = useState(false);
  const [token, setToken] = useState('');
  const [refreshVersion, setRefreshVersion] = useState(0);
  const listRequest = useRef(0);
  const pageNumber = listState.previousCursors.length + 1;
  const canGoPrevious = listState.previousCursors.length > 0;
  const pageMatches = loadedCursor === listState.cursor && loadedRefreshVersion === refreshVersion;
  const pageBusy = busy || !pageMatches;
  const pageError = pageMatches ? error : '';
  const pageApplications = pageMatches ? applications : [];
  const pageNextCursor = pageMatches ? nextCursor : undefined;
  const canGoNext = Boolean(pageNextCursor);

  useEffect(() => {
    const request = ++listRequest.current;

    void api.listApplications(undefined, listState.cursor)
      .then(page => {
        if (listRequest.current !== request) return;
        setApplications(page.applications);
        setNextCursor(page.next_cursor || undefined);
        setError('');
        setLoadedCursor(listState.cursor);
        setLoadedRefreshVersion(refreshVersion);
      })
      .catch(caught => {
        if (listRequest.current !== request) return;
        setApplications([]);
        setNextCursor(undefined);
        setError(caught instanceof OnboardingApiError && caught.status === 401
          ? 'Sign in with an authorized internal session to view saved applications.'
          : 'Saved applications could not be loaded. Check the backend connection and try again.');
        setLoadedCursor(listState.cursor);
        setLoadedRefreshVersion(refreshVersion);
      })
      .finally(() => {
        if (listRequest.current === request) setBusy(false);
      });

    return () => {
      if (listRequest.current === request) listRequest.current += 1;
    };
  }, [api, listState.cursor, refreshVersion]);

  const refreshFirstPage = useCallback(() => {
    onPageChange({ previousCursors: [] });
    setRefreshVersion(version => version + 1);
  }, [onPageChange]);

  useEffect(() => {
    window.addEventListener('focus', refreshFirstPage);
    window.addEventListener('flowcard:onboarding-saved', refreshFirstPage);
    return () => {
      window.removeEventListener('focus', refreshFirstPage);
      window.removeEventListener('flowcard:onboarding-saved', refreshFirstPage);
    };
  }, [refreshFirstPage]);

  async function connect(event: FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError('');
    try {
      await api.establishOperatorSession(token);
      setToken('');
      setShowConnection(false);
      refreshFirstPage();
    } catch {
      setToken('');
      setError('Internal sign-in failed. Check the operator credential and backend configuration.');
      setBusy(false);
    }
  }

  function goNext() {
    if (!pageNextCursor || pageBusy) return;
    onPageChange({
      cursor: pageNextCursor,
      previousCursors: [...listState.previousCursors, listState.cursor],
    });
  }

  function goPrevious() {
    if (!canGoPrevious || pageBusy) return;
    const previousCursors = [...listState.previousCursors];
    const cursor = previousCursors.pop();
    onPageChange(cursor === undefined
      ? { previousCursors }
      : { cursor, previousCursors });
  }

  return (
    <Card>
      <CardHeader title="Onboarding applications" meta="Live backend records" />
      <p style={{ color: color.textMuted, marginTop: 0 }}>
        {browserGatewayEnabled ? 'Saved company onboarding records, including drafts.' : 'Applications across companies and statuses, including drafts. Demo portfolio customers remain in Customers.'}
      </p>
      <div style={{ display: 'flex', gap: space.lg, marginBottom: space.xl }}>
        <Button onClick={refreshFirstPage} disabled={pageBusy}>{pageBusy ? 'Loading…' : 'Refresh'}</Button>
        <Button onClick={() => setShowConnection(value => !value)}>{browserGatewayEnabled ? 'Sign in again' : 'Internal session'}</Button>
      </div>
      {showConnection && browserGatewayEnabled && <BrowserLoginForm onConnected={() => { setShowConnection(false); refreshFirstPage(); }} />}
      {showConnection && !browserGatewayEnabled && (
        <form onSubmit={connect} style={{ display: 'grid', gap: space.lg, marginBottom: space.xl }}>
          <label style={{ display: 'grid', gap: space.md }}>Operator credential
            <input
              type="password"
              required
              value={token}
              autoComplete="off"
              onChange={event => setToken(event.target.value)}
              style={control}
            />
          </label>
          <p style={{ color: color.textMuted, margin: 0 }}>
            For authorized team members and local integration testing. This is not a customer sign-in.
          </p>
          <button type="submit" disabled={busy || !token} style={{ ...control, cursor: 'pointer' }}>
            Connect
          </button>
        </form>
      )}
      {pageError && <p role="alert" style={{ color: color.critical }}>{pageError}</p>}
      {pageBusy && <p role="status">Loading applications…</p>}
      {!pageBusy && !pageError && pageApplications.length === 0 && (
        <p style={{ color: color.textMuted }}>
          {pageNumber === 1 ? 'No applications yet.' : 'No applications on this page.'}
        </p>
      )}
      {pageApplications.length > 0 && !pageError && (
        <div style={{ overflowX: 'auto' }}>
          <Table>
            <thead>
              <tr>
                <Th>Company</Th>
                <Th>Application ID</Th>
                <Th>Status</Th>
                <Th>Created</Th>
                <Th>Details</Th>
              </tr>
            </thead>
            <tbody>
              {pageApplications.map(application => (
                <ClickableTr key={application.id} onClick={() => onOpenApplication(application.id)}>
                  <Td><strong>{application.company?.legal_name || application.company_id}</strong></Td>
                  <Td><span style={{ overflowWrap: 'anywhere' }}>{application.id}</span></Td>
                  <Td><Badge>{applicationStatus(application.status)}</Badge></Td>
                  <Td>{formatTimestamp(application.created_at)}</Td>
                  <Td>
                    <button
                      type="button"
                      onClick={event => {
                        event.stopPropagation();
                        onOpenApplication(application.id);
                      }}
                      style={{
                        color: color.accent,
                        background: 'transparent',
                        border: 'none',
                        padding: 0,
                        font: 'inherit',
                        textDecoration: 'underline',
                        cursor: 'pointer',
                      }}
                    >
                      View application
                    </button>
                  </Td>
                </ClickableTr>
              ))}
            </tbody>
          </Table>
        </div>
      )}
      <nav aria-label="Applications pages" style={{ display: 'flex', alignItems: 'center', gap: space.lg, marginTop: space.xl }}>
        <Button onClick={goPrevious} disabled={pageBusy || !canGoPrevious}>Previous</Button>
        <span aria-live="polite">Page {pageNumber}</span>
        <Button onClick={goNext} disabled={pageBusy || !canGoNext}>Next</Button>
      </nav>
    </Card>
  );
}
