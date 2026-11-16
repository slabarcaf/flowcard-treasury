import { useEffect, useState } from 'react';
import { FinancialCommandClient, FinancialCommandError } from '../../../../services/financial/commands.js';
import type { ActionRecord, CommandReceipt, SavedCommand, ServicingSession } from '../../../../services/financial/commands.js';
import { cents } from '../../../../services/financial/client.js';

const button = 'rounded border border-slate-600 px-3 py-2 text-sm disabled:opacity-40';
const message = (error: unknown) => error instanceof FinancialCommandError ? error.message : 'The command outcome could not be confirmed. Check saved history.';

export function CommandRecovery() {
  const [api] = useState(() => new FinancialCommandClient());
  const [history, setHistory] = useState<SavedCommand[]>([]);
  const [enabled, setEnabled] = useState(false);
  useEffect(() => { let active = true; void api.session().then(value => { if (active) setEnabled(value.enabled && value.linked); }).catch(() => { if (active) setEnabled(true); }); return () => { active = false; }; }, [api]);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  async function refresh() { setBusy(true); setError(''); try { setHistory(await api.history()); } catch (caught) { setError(message(caught)); } finally { setBusy(false); } }
  async function retry(command: SavedCommand) {
    setBusy(true); setError('');
    try { await api.retry(command); setHistory(await api.history()); } catch (caught) { setError(message(caught)); } finally { setBusy(false); }
  }
  if (!enabled) return null;
  return <section className="rounded border border-slate-700 p-4 space-y-3"><h2 className="font-semibold">Command recovery</h2>
    <p className="text-sm text-slate-400">Up to 50 commands for this verified user and binding, unknown outcomes first. A received reservation/queue receipt is not settlement. Unknown outcomes retain their original payload and key.</p>
    <button type="button" className={button} disabled={busy} onClick={() => void refresh()}>Refresh command history</button>
    {error && <p role="alert" className="text-amber-200">{error}</p>}
    {history.map(command => <article key={command.id} className="border-t border-slate-700 pt-3 space-y-2"><p>{command.state} · HTTP {command.status ?? 'unknown'}</p><p className="text-xs break-all">{command.path} · Command {command.id}</p>
      {command.receipt && <pre className="whitespace-pre-wrap break-all text-xs">{JSON.stringify(command.receipt, null, 2)}</pre>}
      {command.state === 'UNKNOWN' && <button type="button" className={button} disabled={busy} onClick={() => void retry(command)}>Retry same saved command and key</button>}
    </article>)}
  </section>;
}

export function FinancialActions({ record, refresh }: { record: ActionRecord; refresh: () => void }) {
  const [api] = useState(() => new FinancialCommandClient());
  const [binding, setBinding] = useState<ServicingSession | null>(null);
  const [error, setError] = useState('');
  const [reason, setReason] = useState('REVIEWED');
  const [accepted, setAccepted] = useState(false);
  const [busy, setBusy] = useState(false);
  const [receipt, setReceipt] = useState<CommandReceipt | null>(null);
  const [attempted, setAttempted] = useState(false);
  const [unresolved, setUnresolved] = useState(false);
  useEffect(() => { let active = true; void api.session().then(async value => {
      if (value.enabled && value.linked) {
        const commands = await api.history();
        const pending = commands.some(command => command.state === 'UNKNOWN' && (record.kind === 'case' ? command.payload.originating_case_id === record.value.case_id : command.path.includes(record.value.id)));
        if (active) setUnresolved(pending);
      }
      if (active) setBinding(value);
    }).catch(caught => { if (active) setError(message(caught)); }); return () => { active = false; }; }, [api, record]);
  async function run(action: () => Promise<CommandReceipt>) {
    if (busy || attempted || unresolved) return;
    setAttempted(true);
    setBusy(true); setError('');
    try { setReceipt(await action()); } catch (caught) { setError(message(caught)); } finally { setBusy(false); }
  }
  const linked = binding?.enabled && binding.linked && binding.mode === 'DEMO';
  return <section className="border-t border-slate-700 pt-4 space-y-4">
    {!binding && !error && <p>Checking verified servicing binding…</p>}
    {binding && !linked && <p className="text-sm text-slate-400">Financial commands require a verified local DEMO binding. Read-only access remains separate.</p>}
    {error && <p role="alert" className="text-amber-200">{error}</p>}
    {unresolved && <p role="alert" className="text-amber-200">An earlier command for this record has an unknown outcome. Recover the same saved command and key before a new action.</p>}
    {linked && <><h3 className="font-semibold">Explicit DEMO action</h3><p className="text-sm text-amber-200">Synthetic local financing only. The service rechecks all policy, versions, terms and authority.</p>
      {binding.role === 'OPERATOR' && record.kind === 'case' && record.value.assessment && <button type="button" className={button} disabled={busy || attempted || unresolved} onClick={() => void run(() => api.create(record.value))}>Create DEMO offer from this saved assessment</button>}
      {record.kind === 'offer' && <>
        <p className="text-sm">Offer {record.value.id} · immutable revision {record.value.offer_revision} · aggregate version {record.value.version}</p>
        <p className="text-sm">Principal {cents(record.value.principal_cents)} · Contract fee {cents(record.value.contract_fee_cents)} · Total due {cents(record.value.total_due_cents)} · Due {record.value.due_on}</p>
        <p className="text-xs break-all">Exact terms hash {record.value.terms_hash} · Disclosures {record.value.disclosures_version} · Waterfall version {record.value.waterfall_version ?? 'Unavailable'} · Late fee convention {record.value.late_fee_convention} · Term {record.value.term_days} days · Expires {record.value.expires_at}</p>
        {binding.role === 'CUSTOMER' && binding.customer_id === record.value.customer_id && <>
          <label className="block text-sm"><input type="checkbox" checked={accepted} onChange={event => setAccepted(event.target.checked)} disabled={busy || attempted || unresolved} /> I accept these exact DEMO terms, fee, total due and repayment date for this offer and terms hash.</label>
          <button type="button" className={button} disabled={!accepted || busy || attempted || unresolved} onClick={() => void run(() => api.accept(record.value))}>Accept these DEMO terms</button>
        </>}
        {binding.role === 'OPERATOR' && <>
          <label className="block text-sm">Approval reason<input className="block bg-slate-900 border border-slate-600 rounded p-2" value={reason} onChange={event => setReason(event.target.value)} disabled={busy || attempted || unresolved} /></label>
          <div className="flex gap-3"><button type="button" className={button} disabled={busy || attempted || unresolved || !reason} onClick={() => void run(() => api.approve(record.value, reason))}>Approve these DEMO terms</button><button type="button" className={button} disabled={busy || attempted || unresolved} onClick={() => void run(() => api.fund(record.value))}>Request DEMO funding</button></div>
        </>}
      </>}
      {binding.role === 'OPERATOR' && (record.kind === 'bridge' || record.kind === 'operation' && record.value.state === 'HELD') && <>
        <label className="block text-sm">Review reason<input className="block bg-slate-900 border border-slate-600 rounded p-2" value={reason} onChange={event => setReason(event.target.value)} disabled={busy || attempted || unresolved} /></label>
        <button type="button" className={button} disabled={busy || attempted || unresolved || !reason} onClick={() => void run(() => record.kind === 'bridge' ? api.review(record.value, reason) : api.reconcile(record.value, reason))}>{record.kind === 'bridge' ? 'Request DEMO collection review' : 'Request DEMO reconciliation'}</button>
      </>}
    </>}
    {attempted && !receipt && <button type="button" className={button} disabled={busy} onClick={refresh}>Refresh saved state and recovery history</button>}
    {receipt && <section role="status" className="space-y-2"><p>Command received · HTTP {receipt.status} · {receipt.commandID}</p><p className="text-sm">A reserved or queued receipt does not establish settled funds. Read current saved state.</p><pre className="whitespace-pre-wrap break-all text-xs">{JSON.stringify(receipt.receipt, null, 2)}</pre><button type="button" className={button} onClick={refresh}>Refresh saved record before another action</button></section>}
  </section>;
}
