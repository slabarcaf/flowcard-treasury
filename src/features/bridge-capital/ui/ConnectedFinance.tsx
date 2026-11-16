import { useEffect, useRef, useState } from 'react';
import type { BrowserSession } from '../../../services/browser-session/client.js';
import type { Bridge, Collections, Offer } from '../../../services/financial/client.js';
import { cents } from '../../../services/financial/client.js';
import { FinancialCommandError } from '../../../services/financial/commands.js';
import type { CommandReceipt } from '../../../services/financial/commands.js';
import { useProductQuery } from './useProductQuery.js';
import { Badge, Button, Card, CardHeader, Row } from '../backoffice/components/primitives.js';
import { color, space } from '../backoffice/tokens.js';
import { financeRead, financeCommand, human, stateTone } from './finance.js';

export function ReadNotice({
  busy,
  error,
  refresh,
}: {
  busy: boolean;
  error: string;
  refresh: () => void;
}) {
  return (
    <div style={{ display: 'flex', gap: space.md, alignItems: 'center', flexWrap: 'wrap' }}>
      {busy && <span role="status">Updating saved records…</span>}
      {error && (
        <p role="alert" style={{ color: color.warning }}>
          {error} Displayed records may be stale.
        </p>
      )}
      <Button onClick={refresh} disabled={busy}>
        Refresh
      </Button>
    </div>
  );
}
export function SourceNote({ mode }: { mode?: string }) {
  return (
    <p style={{ fontSize: 12, color: color.textMuted, margin: '10px 0' }}>
      {mode === 'DEMO'
        ? 'DEMO · synthetic financial records and local provider · real Go processing'
        : mode === 'SANDBOX'
          ? 'Airwallex sandbox test funds · synthetic financial history and receivables'
          : 'Financial source not yet confirmed'}
    </p>
  );
}

export function Recovery({ changed }: { changed?: () => void }) {
  const history = useProductQuery('command-history', () => financeCommand.history(), 5000);
  const [busy, setBusy] = useState('');
  const [error, setError] = useState('');
  return (
    <Card>
      <CardHeader title="Saved actions" meta="Your session's durable command history" />
      <ReadNotice {...history} />
      {history.data?.length === 0 && <p>No actions yet.</p>}
      {error && <p role="alert">{error}</p>}
      {history.data?.slice(0, 10).map((command) => (
        <div key={command.id} style={{ padding: '12px 0', borderTop: `1px solid ${color.border}` }}>
          <Badge tone={command.state === 'UNKNOWN' ? 'warning' : 'neutral'}>
            {human(command.state)}
          </Badge>
          <span style={{ marginLeft: 12, fontSize: 13 }}>
            {command.path.endsWith('/acceptances')
              ? 'Accept terms'
              : command.path.endsWith('/approvals')
                ? 'Approve terms'
                : command.path.endsWith('/fund')
                  ? 'Request funding'
                  : command.path === '/api/offers'
                    ? 'Create offer'
                    : human(command.path.split('/').at(-1) ?? 'Action')}
          </span>
          {!!command.receipt?.error && (
            <p style={{ fontSize: 13 }}>
              {typeof command.receipt.error === 'string'
                ? command.receipt.error
                : JSON.stringify(command.receipt.error)}
            </p>
          )}
          {command.state === 'UNKNOWN' && (
            <div style={{ marginTop: 10 }}>
              <p>
                The outcome is unconfirmed. Check the current record, then retry the saved request
                and key.
              </p>
              <Button
                disabled={!!busy}
                onClick={() => {
                  setBusy(command.id);
                  setError('');
                  void financeCommand
                    .retry(command)
                    .then(() => {
                      history.refresh();
                      changed?.();
                    })
                    .catch((e) => setError(e instanceof Error ? e.message : 'Recovery unavailable'))
                    .finally(() => setBusy(''));
                }}
              >
                Retry same saved action
              </Button>
            </div>
          )}
        </div>
      ))}
    </Card>
  );
}

export function OfferCard({
  id,
  session,
  openBridge,
  changed,
}: {
  id: string;
  session: BrowserSession;
  openBridge: (id: string) => void;
  changed?: () => void;
}) {
  const offer = useProductQuery('offer:' + id, () => financeRead.offer(id), 3000);
  const history = useProductQuery('offer-history:' + id, () => financeCommand.history(), 5000);
  const [unknownID, setUnknownID] = useState('');
  const inFlight = useRef(false);
  const [expired, setExpired] = useState(false);
  const expiresAt = offer.data?.expires_at;
  useEffect(() => {
    if (!expiresAt) return;
    const delay = Math.max(0, Date.parse(expiresAt) - Date.now());
    const timer = setTimeout(() => setExpired(true), delay);
    return () => clearTimeout(timer);
  }, [expiresAt]);
  const [accepted, setAccepted] = useState(false);
  const [busy, setBusy] = useState(false);
  const [attempt, setAttempt] = useState<number | null>(null);
  const [unknown, setUnknown] = useState(false);
  const [error, setError] = useState('');
  const [receipt, setReceipt] = useState<CommandReceipt | null>(null);
  async function run(action: (value: Offer) => Promise<CommandReceipt>) {
    if (
      !offer.data ||
      inFlight.current ||
      busy ||
      blockedUnknown ||
      durableUnknown ||
      expired ||
      !!history.error ||
      attempt === offer.data.version
    )
      return;
    inFlight.current = true;
    setBusy(true);
    setAttempt(offer.data.version);
    setError('');
    try {
      const value = await action(offer.data);
      setReceipt(value);
      offer.refresh();
      changed?.();
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Action unavailable');
      if (!(e instanceof FinancialCommandError) || e.status >= 500) {
        setUnknown(true);
        if (e instanceof FinancialCommandError) setUnknownID(e.commandID);
      }
      history.refresh();
    } finally {
      inFlight.current = false;
      setBusy(false);
    }
  }
  const confirmed =
    history.data?.some((command) => command.id === unknownID && command.state !== 'UNKNOWN') ??
    false;
  const blockedUnknown = unknown && !confirmed;
  const durableUnknown =
    history.data?.some(
      (command) => command.state === 'UNKNOWN' && command.path.includes('/' + id + '/')
    ) ?? true;
  const value = offer.data;
  const disabled =
    busy ||
    offer.busy ||
    !!offer.error ||
    !!history.error ||
    expired ||
    blockedUnknown ||
    durableUnknown ||
    (!!value && attempt === value.version);
  const bridge = receipt?.receipt.bridge_id;
  return (
    <Card>
      <CardHeader
        title="Bridge offer"
        meta={value && <Badge tone={stateTone(value.state)}>{human(value.state)}</Badge>}
      />
      <ReadNotice {...offer} />
      {expired && offer.data?.state !== 'CONSUMED' && (
        <p role="status">
          This saved offer has expired. Request a fresh assessment and offer before a new action.
        </p>
      )}
      {value && (
        <>
          <div style={{ marginTop: 20 }}>
            <Row label="We cover" value={cents(value.principal_cents)} />
            <Row label="Fixed fee" value={cents(value.contract_fee_cents)} />
            <Row label="Total to repay" value={cents(value.total_due_cents)} />
            <Row label="Term" value={`${value.term_days} days`} />
            <Row label="Due" value={value.due_on} />
            <Row label="Offer expires" value={new Date(value.expires_at).toLocaleString()} />
          </div>
          <details style={{ fontSize: 12, color: color.textMuted, margin: '16px 0' }}>
            <summary>Saved terms reference</summary>
            <p style={{ overflowWrap: 'anywhere' }}>
              {value.id} · revision {value.offer_revision} · version {value.version}
              <br />
              Hash {value.terms_hash}
              <br />
              Disclosures {value.disclosures_version} · {value.consent_label}
            </p>
          </details>
          {session.user.role === 'CUSTOMER' ? (
            <>
              {value.state === 'AWAITING_ACCEPTANCE' && (
                <>
                  <label
                    style={{ display: 'flex', gap: 12, alignItems: 'start', margin: '20px 0' }}
                  >
                    <input
                      type="checkbox"
                      checked={accepted}
                      disabled={disabled}
                      onChange={(e) => setAccepted(e.target.checked)}
                    />
                    I accept these exact {value.source_mode === 'SANDBOX' ? 'sandbox test' : 'DEMO'}{' '}
                    terms: {cents(value.principal_cents)} principal,{' '}
                    {cents(value.contract_fee_cents)} fee, {cents(value.total_due_cents)} total, due{' '}
                    {value.due_on}.
                  </label>
                  <Button
                    variant="primary"
                    disabled={disabled || !accepted}
                    onClick={() => void run((v) => financeCommand.accept(v))}
                  >
                    Accept these terms
                  </Button>
                </>
              )}
              {['AWAITING_APPROVAL', 'READY'].includes(value.state) && (
                <p role="status">
                  Your acceptance is saved. Waiting for operator processing. Funding is confirmed
                  only after settlement.
                </p>
              )}
            </>
          ) : (
            <div style={{ display: 'flex', gap: 12, flexWrap: 'wrap', marginTop: 20 }}>
              <Button
                disabled={disabled || value.state !== 'AWAITING_APPROVAL'}
                onClick={() => void run((v) => financeCommand.approve(v, 'REVIEWED'))}
              >
                Approve exact terms
              </Button>
              <Button
                variant="primary"
                disabled={disabled || value.state !== 'READY'}
                onClick={() => void run((v) => financeCommand.fund(v))}
              >
                Request funding
              </Button>
            </div>
          )}
          {error && (
            <p role="alert" style={{ color: color.warning }}>
              {error}
            </p>
          )}
          {error && !blockedUnknown && !durableUnknown && (
            <Button
              disabled={offer.busy}
              onClick={() => {
                void financeRead
                  .offer(id)
                  .then(() => {
                    offer.refresh();
                    setAttempt(null);
                    setAccepted(false);
                    setUnknown(false);
                    setError('');
                  })
                  .catch((e) => setError(e.message));
              }}
            >
              Review refreshed terms before a new attempt
            </Button>
          )}
          {receipt && (
            <p role="status">
              {receipt.status === 202
                ? 'Request queued. Funds are not yet confirmed.'
                : 'Saved action received.'}
            </p>
          )}
          {typeof bridge === 'string' && (
            <Button onClick={() => openBridge(bridge)}>View bridge progress</Button>
          )}
          {blockedUnknown && (
            <p>
              Use Saved actions below to recover the original request. Do not start another attempt.
            </p>
          )}
        </>
      )}
    </Card>
  );
}

export function BridgeCard({ id, operator = false }: { id: string; operator?: boolean }) {
  const detail = useProductQuery('bridge:' + id, () => financeRead.bridge(id), 3000);
  const [page, setPage] = useState<{ version: number; cursor: string }>();
  const version = detail.data?.version;
  const cursor = page?.version === version ? page?.cursor : undefined;
  const collections = useProductQuery(
    'collections:' + id + ':' + version + ':' + cursor,
    () =>
      version === undefined ? Promise.resolve(null) : financeRead.collections(id, version, cursor),
    3000
  );
  const actionHistory = useProductQuery(
    'bridge-actions:' + id,
    () => financeCommand.history(),
    5000
  );
  const [attemptedActions, setAttemptedActions] = useState<string[]>([]);
  const actionFlight = useRef(false);
  const [actionError, setActionError] = useState('');
  const [acting, setActing] = useState(false);
  const value = detail.data;
  const unresolvedActions =
    actionHistory.data?.some(
      (c) =>
        c.state === 'UNKNOWN' &&
        (c.path.includes('/' + id + '/') ||
          detail.data?.operations.some((op) => c.path.includes('/' + op.id + '/')))
    ) ?? true;
  async function review(v: Bridge) {
    const key = 'review:' + v.version;
    if (actionFlight.current || unresolvedActions || attemptedActions.includes(key)) return;
    actionFlight.current = true;
    setAttemptedActions((a) => [...a, key]);
    setActing(true);
    setActionError('');
    try {
      await financeCommand.review(v, 'REVIEWED');
      detail.refresh();
    } catch (e) {
      setActionError(e instanceof Error ? e.message : 'Review unavailable');
    } finally {
      actionFlight.current = false;
      actionHistory.refresh();
      setActing(false);
    }
  }
  const history = collections.data as Collections | null;
  return (
    <div style={{ display: 'grid', gap: 20 }}>
      <Card>
        <CardHeader
          title={operator ? 'Bridge overview' : 'Your bridge'}
          meta={value && <Badge tone={stateTone(value.lifecycle)}>{human(value.lifecycle)}</Badge>}
        />
        <ReadNotice {...detail} />
        {value && (
          <>
            <Row label="Principal still owed" value={cents(value.principal_owed_cents)} />
            <Row label="Contract fee still owed" value={cents(value.contract_fee_owed_cents)} />
            <Row label="Late fee still owed" value={cents(value.late_fee_owed_cents)} />
            <Row label="Due" value={value.due_on ?? 'Unavailable'} />
            <Row label="Delinquency" value={human(value.delinquency)} />
            <Row label="Debt observed" value={value.debt_as_of ?? 'Unavailable'} />
            <Row
              label="Accrual current"
              value={value.accrual_current === null ? 'Unknown' : String(value.accrual_current)}
            />
            {value.hold_reasons.length > 0 && (
              <p role="alert" style={{ color: color.warning }}>
                On hold: {value.hold_reasons.map(human).join(', ')}
              </p>
            )}
            {value.lifecycle === 'REPAID' && (
              <p style={{ color: color.positive }}>
                Repaid in full. No principal or fees remain owed.
              </p>
            )}
            {value.lifecycle !== 'REPAID' && (
              <p>
                Eligible settled deposits are collected fee-first. This view reflects the servicing
                ledger.
              </p>
            )}
            <div style={{ fontSize: 12, color: color.textMuted, overflowWrap: 'anywhere' }}>
              Bridge {id} · version {value.version} · as of {value.as_of}
            </div>
            {operator && (
              <Button
                disabled={
                  acting ||
                  unresolvedActions ||
                  !!actionHistory.error ||
                  attemptedActions.includes('review:' + value.version) ||
                  detail.busy ||
                  !!detail.error ||
                  !['ACTIVE', 'COLLECTING', 'REOPENED_HELD'].includes(value.lifecycle)
                }
                onClick={() => void review(value)}
              >
                Request collection review
              </Button>
            )}
            {actionError && <p role="alert">{actionError}</p>}
          </>
        )}
      </Card>
      <Card>
        <CardHeader title="Funding and collection progress" />
        {value?.operations.map((op) => (
          <div key={op.id} style={{ padding: '12px 0', borderTop: `1px solid ${color.border}` }}>
            <Row
              label={human(op.kind)}
              value={<Badge tone={stateTone(op.state)}>{human(op.state)}</Badge>}
            />
            {op.hold_reason && <p>{human(op.hold_reason)}</p>}
            {operator && op.state === 'HELD' && (
              <Button
                disabled={
                  acting ||
                  unresolvedActions ||
                  !!actionHistory.error ||
                  attemptedActions.includes(op.id + ':' + op.version)
                }
                onClick={() => {
                  if (actionFlight.current) return;
                  actionFlight.current = true;
                  setAttemptedActions((a) => [...a, op.id + ':' + op.version]);
                  setActing(true);
                  void financeCommand
                    .reconcile(op, 'REVIEWED')
                    .then(detail.refresh)
                    .catch((e) => setActionError(e.message))
                    .finally(() => {
                      actionFlight.current = false;
                      actionHistory.refresh();
                      setActing(false);
                    });
                }}
              >
                Reconcile saved operation
              </Button>
            )}
          </div>
        ))}
        {collections.error && (
          <p role="alert">{collections.error} Refresh the bridge before loading history again.</p>
        )}
        {history &&
          history.bridge_version === version &&
          history.items.map((item) => (
            <div
              key={item.id}
              style={{ padding: '12px 0', borderTop: `1px solid ${color.border}` }}
            >
              <Row
                label="Deposit collection"
                value={<Badge tone={stateTone(item.state)}>{human(item.state)}</Badge>}
              />
              <Row label="Settled debit" value={cents(item.settled_debit_cents)} />
              {item.allocations.map((a) => (
                <Row
                  key={a.allocation_index}
                  label={human(a.bucket)}
                  value={cents(a.amount_cents)}
                />
              ))}
              <small>{item.settled_at ?? 'Settlement pending'}</small>
            </div>
          ))}
        {history?.items.length === 0 && <p>No settled collections on this page.</p>}
        {history?.next_cursor && (
          <Button
            onClick={() =>
              setPage(
                history.next_cursor && version
                  ? { version, cursor: history.next_cursor }
                  : undefined
              )
            }
          >
            More collections
          </Button>
        )}
        {cursor && <Button onClick={() => setPage(undefined)}>Newest collections</Button>}
      </Card>
    </div>
  );
}
