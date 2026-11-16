import { useEffect, useRef, useState } from 'react';
import type { FormEvent, ReactNode } from 'react';
import { FinancialReadClient, FinancialReadError, cents } from '../../../../services/financial/client.js';
import type { Bridge, CaseDetail, CasePage, Changes, Collections, Offer, Operation, ReadScope, SourceMode } from '../../../../services/financial/client.js';
import { CommandRecovery, FinancialActions } from './FinancialActions.js';

type Selection = { kind: 'case' | 'offer' | 'bridge' | 'operation'; id: string; scope?: ReadScope };
type Detail = { kind: 'case'; value: CaseDetail } | { kind: 'offer'; value: Offer } | { kind: 'bridge'; value: Bridge; collections: Collections } | { kind: 'operation'; value: Operation };
type ReadState<T> = { busy: boolean; error: string; data: T | null };
const pending = <T,>(): ReadState<T> => ({ busy: true, error: '', data: null });
const failure = (error: unknown): string => error instanceof FinancialReadError ? error.message : 'The source could not be read. Refresh to try again.';
const display = (value: string | number | boolean | null | undefined): string => value === null || value === undefined ? 'Unavailable' : String(value);

function Card({ title, children }: { title: string; children: ReactNode }) {
  return <section className="rounded-xl border border-slate-700 p-5 space-y-4 min-w-0"><h2 className="text-xl font-semibold">{title}</h2>{children}</section>;
}
function Facts({ entries }: { entries: [string, string | number | boolean | null | undefined][] }) {
  return <dl className="grid grid-cols-1 sm:grid-cols-2 gap-x-8 gap-y-3 text-sm">{entries.map(([label, value]) => <div key={label}><dt className="text-slate-400">{label}</dt><dd className="break-words whitespace-pre-wrap">{display(value)}</dd></div>)}</dl>;
}
function Source({ mode }: { mode: SourceMode }) {
  return <p className="text-sm text-amber-200">{mode === 'DEMO' ? 'DEMO · synthetic source book' : 'SANDBOX · source environment'} · Saved Go backend records</p>;
}
function ErrorMessage({ state }: { state: { busy: boolean; error: string } }) {
  return <>{state.busy && <p role="status" className="text-slate-400">Reading saved records…</p>}{state.error && <p role="alert" className="text-amber-200">{state.error}</p>}</>;
}
const button = 'rounded border border-slate-600 px-3 py-2 text-sm disabled:opacity-40';

function Cases({ api, open }: { api: FinancialReadClient; open: (selection: Selection) => void }) {
  const [state, setState] = useState<ReadState<CasePage>>(pending);
  const generation = useRef(0);
  useEffect(() => {
    const abort = new AbortController();
    const request = ++generation.current;
    void api.cases(undefined, abort.signal).then(data => { if (generation.current === request) setState({ busy: false, error: '', data }); }).catch(error => { if (!abort.signal.aborted && generation.current === request) setState({ busy: false, error: failure(error), data: null }); });
    return () => { abort.abort(); };
  }, [api]);
  function load(cursor?: string) {
    const request = ++generation.current;
    setState(pending());
    void api.cases(cursor).then(data => { if (request === generation.current) setState({ busy: false, error: '', data }); }).catch(error => { if (request === generation.current) setState({ busy: false, error: failure(error), data: null }); });
  }
  return <Card title="Coordinator cases">
    <p className="text-sm text-slate-400">Saved assessments and their rule outcomes. Source mode and snapshot versions appear on detail.</p>
    <button type="button" className={button} disabled={state.busy} onClick={() => load()}>Refresh cases</button>
    <ErrorMessage state={state} />
    {state.data && <>
      {state.data.items.length === 0 && <p>No saved cases in this source.</p>}
      <ul className="space-y-3">{state.data.items.map(item => <li key={item.id} className="border-t border-slate-700 pt-3 space-y-2">
        <p>{item.kind} · {item.status} · {display(item.decision_status)}</p>
        <p className="text-xs break-all text-slate-400">Case {item.id} · Customer {display(item.customer_id)}</p>
        <p className="text-sm">Saved assessment: {display(item.assessment_id)} · Updated {item.updated_at}</p>
        <button type="button" className={button} onClick={() => open({ kind: 'case', id: item.id, scope: { customerID: item.customer_id ?? undefined } })}>Open case</button>
        {item.bridge_id && <button type="button" className={`${button} ml-2`} onClick={() => open({ kind: 'bridge', id: item.bridge_id!, scope: { customerID: item.customer_id ?? undefined } })}>Read linked bridge</button>}
      </li>)}</ul>
      {state.data.next_cursor && <button type="button" className={button} onClick={() => load(state.data!.next_cursor!)}>Next cases</button>}
    </>}
  </Card>;
}

function Servicing({ api, open }: { api: FinancialReadClient; open: (selection: Selection) => void }) {
  const [state, setState] = useState<ReadState<Changes>>(pending);
  const generation = useRef(0);
  useEffect(() => {
    const abort = new AbortController();
    const request = ++generation.current;
    void api.changes(0, abort.signal).then(data => { if (request === generation.current) setState({ busy: false, error: '', data }); }).catch(error => { if (!abort.signal.aborted && request === generation.current) setState({ busy: false, error: failure(error), data: null }); });
    return () => { abort.abort(); };
  }, [api]);
  function load(after = 0) {
    const request = ++generation.current;
    setState(pending());
    void api.changes(after).then(data => { if (request === generation.current) setState({ busy: false, error: '', data }); }).catch(error => { if (request === generation.current) setState({ busy: false, error: failure(error), data: null }); });
  }
  return <Card title="Funding and collection records">
    <p className="text-sm text-slate-400">Discover saved offers, bridges and operations from the committed change feed. Each page preserves source sequence and resource version.</p>
    <button type="button" className={button} disabled={state.busy} onClick={() => load()}>Read feed from start</button>
    <ErrorMessage state={state} />
    {state.data && <>
      <Source mode={state.data.mode} />
      <p className="text-sm">Last sequence on page: {state.data.next_after_seq}</p>
      {state.data.items.length === 0 && <p>No saved changes after this sequence.</p>}
      <ul className="space-y-3">{state.data.items.map(item => { const scope: ReadScope = { mode: state.data!.mode, customerID: item.customer_id, evaluationID: item.evaluation_id ?? undefined, originatingCaseID: item.originating_case_id ?? undefined }; return <li key={item.sequence} className="border-t border-slate-700 pt-3 space-y-2">
        <p>#{item.sequence} · {item.resource_type} · {item.change_type} · Resource version {item.resource_version}</p>
        <p className="text-xs break-all text-slate-400">{item.resource_id} · Customer {item.customer_id} · Recorded {item.recorded_at}</p>
        {item.bridge_version !== null && <p className="text-sm">Parent bridge version {item.bridge_version}</p>}
        <div className="flex flex-wrap gap-2">
          {(item.offer_id || item.resource_type === 'OFFER') && <button type="button" className={button} onClick={() => open({ kind: 'offer', id: item.offer_id ?? item.resource_id, scope })}>Read offer</button>}
          {item.bridge_id && <button type="button" className={button} onClick={() => open({ kind: 'bridge', id: item.bridge_id!, scope })}>Read bridge and collections</button>}
          {item.resource_type === 'OPERATION' && <button type="button" className={button} onClick={() => open({ kind: 'operation', id: item.resource_id })}>Read operation</button>}
          {item.originating_case_id && <button type="button" className={button} onClick={() => open({ kind: 'case', id: item.originating_case_id!, scope })}>Read originating case</button>}
        </div>
      </li>; })}</ul>
      {state.data.has_more && <button type="button" className={button} onClick={() => load(state.data!.next_after_seq)}>Next changes</button>}
    </>}
  </Card>;
}

function CaseRecord({ value }: { value: CaseDetail }) {
  const assessment = value.assessment;
  return <>
    <Source mode={value.mode} />
    <Facts entries={[
      ['Case ID', value.case_id], ['Case status', value.status], ['Stage', value.stage], ['Customer', value.customer_id],
      ['Bridge version', value.bridge_version], ['Refresh needed', value.refresh_needed], ['Updated', value.updated_at],
      ['Recommended next steps', value.next_actions.join(', ') || 'None reported'],
    ]} />
    {!assessment ? <p>Saved assessment unavailable for this case.</p> : <section className="space-y-4">
      <h3 className="font-semibold">Saved assessment · {assessment.status}</h3>
      <Facts entries={[
        ['Evaluation ID', assessment.id], ['Evaluated', assessment.evaluated_at], ['Policy', assessment.policy_id], ['Policy hash', assessment.policy_hash],
        ['Customer snapshot', assessment.customer_snapshot_id], ['Customer snapshot version', assessment.customer_snapshot_version],
        ['Facility snapshot', assessment.facility_snapshot_id], ['Facility snapshot version', assessment.facility_snapshot_version],
        ['Term days', assessment.term_days], ['Principal', cents(assessment.principal_cents)], ['Estimated fee', cents(assessment.estimated_fee_cents)], ['Total due', cents(assessment.total_due_cents)],
        ['Customer limit', cents(assessment.customer_limit_cents)], ['Existing exposure', cents(assessment.existing_exposure_cents)], ['Post-funding exposure', cents(assessment.post_funding_exposure_cents)], ['Available capital', cents(assessment.available_capital_cents)],
        ['Funding authorized', assessment.funding_authorized], ['Requires recheck', assessment.requires_recheck], ['Approval required', assessment.approval_required],
      ]} />
      <ul className="space-y-3">{assessment.rules.map(rule => <li key={rule.rule_id} className="border-t border-slate-700 pt-3">
        <p>{rule.rule_id} · {rule.status} · {rule.reason_code || 'No reason reported'}</p>
        <p className="text-sm break-words text-slate-400">Actual: {rule.actual === null || rule.actual === undefined ? 'Unavailable' : JSON.stringify(rule.actual)} · Threshold: {rule.threshold === null || rule.threshold === undefined ? 'Unavailable' : JSON.stringify(rule.threshold)}</p>
      </li>)}</ul>
    </section>}
    {value.investigation && <section className="space-y-2 border-t border-slate-700 pt-4"><h3 className="font-semibold">Investigation commentary</h3>
      <Facts entries={[
        ['Explanation mode', value.investigation.explanation_mode], ['Verified', value.investigation.verified], ['Current', value.investigation.current], ['Limitation', value.investigation.limitation],
      ]} />
      <p className="whitespace-pre-wrap">{value.investigation.summary}</p>
    </section>}
  </>;
}

function OfferRecord({ value }: { value: Offer }) {
  return <><p className="text-sm text-slate-400">Exact saved terms. Source consent label: {value.consent_label}</p><Facts entries={[
    ['Offer ID', value.id], ['State', value.state], ['Aggregate version', value.version], ['Immutable offer revision', value.offer_revision],
    ['Terms hash', value.terms_hash], ['Principal', cents(value.principal_cents)], ['Contract fee', cents(value.contract_fee_cents)], ['Total due', cents(value.total_due_cents)],
    ['Term days', value.term_days], ['Offered on', value.offered_on], ['Due on', value.due_on], ['Expires', value.expires_at],
    ['Disclosures', value.disclosures_version], ['Late fee convention', value.late_fee_convention], ['Originating case', value.originating_case_id], ['Evaluation', value.evaluation_id],
  ]} /></>;
}
function OperationRecord({ value }: { value: Operation }) {
  return <Facts entries={[
    ['Operation ID', value.id], ['Version', value.version], ['Kind', value.kind], ['State', value.state], ['Reserved amount', cents(value.reserved_amount_cents)],
    ['Verified settlement reference', value.last_verified_settlement_reference], ['Retry count', value.retry_count], ['Next check', value.next_check], ['Hold reason', value.hold_reason],
  ]} />;
}
function BridgeRecord({ value, collections, next, busy }: { value: Bridge; collections: Collections; next: () => void; busy: boolean }) {
  return <><Source mode={value.mode} /><Facts entries={[
    ['Bridge ID', value.id], ['Version', value.version], ['Source binding', value.source_binding_id], ['Response as of', value.as_of], ['Customer', value.customer_id], ['Account alias', value.account_ref],
    ['Lifecycle', value.lifecycle], ['Delinquency', value.delinquency], ['Days past due', value.days_past_due], ['Due on', value.due_on],
    ['Principal owed', cents(value.principal_owed_cents)], ['Contract fee owed', cents(value.contract_fee_owed_cents)], ['Late fee owed', cents(value.late_fee_owed_cents)],
    ['Debt as of', value.debt_as_of], ['Accrual current', value.accrual_current], ['Accrual current through', value.accrual_current_through],
    ['Last provider observation', value.last_provider_observed_at], ['Last attempt', value.last_attempt_id], ['Last attempt state', value.last_attempt_state], ['Next eligible retry', value.next_eligible_retry_at],
    ['Acceptance', value.acceptance_status], ['Approval', value.approval_status], ['Collection authority', value.collection_authority_status],
    ['Hold reasons', value.hold_reasons.join(', ') || 'None reported'], ['Recommended next steps', value.allowed_next_actions.join(', ') || 'None reported'],
  ]} /><h3 className="font-semibold">Operations</h3>
    {value.operations.length === 0 ? <p>No operations reported.</p> : value.operations.map(operation => <section className="border-t border-slate-700 pt-3" key={operation.id}><OperationRecord value={operation} /></section>)}
    <h3 className="font-semibold">Collection history · bridge version {collections.bridge_version}</h3>
    {collections.items.length === 0 ? <p>No collection attempts on this page.</p> : collections.items.map(item => <section key={item.id} className="border-t border-slate-700 pt-3 space-y-3"><Facts entries={[
      ['Attempt', item.id], ['Operation', item.operation_id], ['Deposit', item.deposit_id], ['State', item.state], ['Created', item.created_at], ['Settled at', item.settled_at],
      ['Reserved debit', cents(item.reserved_debit_cents)], ['Settled debit', cents(item.settled_debit_cents)], ['Provider fee', cents(item.provider_fee_cents)],
    ]} /><p className="text-sm">Settled allocations: {item.allocations.length === 0 ? 'None recorded' : item.allocations.map(row => `${row.allocation_index}: ${row.bucket} ${cents(row.amount_cents)}`).join('; ')}</p>
      <p className="text-sm text-slate-400">Planned allocations: {item.planned_allocations.length === 0 ? 'None recorded' : item.planned_allocations.map(row => `${row.allocation_index}: ${row.bucket} ${cents(row.amount_cents)}`).join('; ')}</p>
    </section>)}
    {collections.next_cursor && <button type="button" className={button} disabled={busy} onClick={next}>Next collection page</button>}
  </>;
}

/** Render only the backend's supplied values, versions, guards and limitations. */
export function FinancialSnapshot({ record, nextCollections, busy }: { record: Detail; nextCollections: () => void; busy: boolean }) {
  if (record.kind === 'case') return <CaseRecord value={record.value} />;
  if (record.kind === 'offer') return <OfferRecord value={record.value} />;
  if (record.kind === 'operation') return <OperationRecord value={record.value} />;
  return <BridgeRecord value={record.value} collections={record.collections} next={nextCollections} busy={busy} />;
}

export function FinancialRecordsScreen() {
  const [api] = useState(() => new FinancialReadClient());
  const [selected, setSelected] = useState<Selection | null>(null);
  const [state, setState] = useState<ReadState<Detail>>({ busy: false, error: '', data: null });
  const [kind, setKind] = useState<Selection['kind']>('offer');
  const [id, setID] = useState('');
  const generation = useRef(0);
  const activeRead = useRef<AbortController | null>(null);
  useEffect(() => () => { generation.current++; activeRead.current?.abort(); }, []);
  async function open(selection: Selection) {
    const request = ++generation.current;
    activeRead.current?.abort();
    const abort = new AbortController(); activeRead.current = abort;
    setSelected(selection); setState(pending());
    try {
      let data: Detail;
      if (selection.kind === 'case') data = { kind: 'case', value: await api.case(selection.id, abort.signal, selection.scope) };
      else if (selection.kind === 'offer') data = { kind: 'offer', value: await api.offer(selection.id, abort.signal, selection.scope) };
      else if (selection.kind === 'operation') data = { kind: 'operation', value: await api.operation(selection.id, abort.signal) };
      else {
        const bridge = await api.bridge(selection.id, abort.signal, selection.scope);
        data = { kind: 'bridge', value: bridge, collections: await api.collections(bridge.id, bridge.version, undefined, abort.signal) };
      }
      if (request === generation.current) setState({ busy: false, error: '', data });
    } catch (error) { if (!abort.signal.aborted && request === generation.current) setState({ busy: false, error: failure(error), data: null }); }
  }
  async function nextCollections() {
    const current = state.data;
    if (current?.kind !== 'bridge' || !current.collections.next_cursor || state.busy) return;
    const request = ++generation.current;
    setState(pending());
    try {
      const collections = await api.collections(current.value.id, current.value.version, current.collections.next_cursor);
      if (request === generation.current) setState({ busy: false, error: '', data: { ...current, collections } });
    } catch (error) { if (request === generation.current) setState({ busy: false, error: failure(error), data: null }); }
  }
  function lookup(event: FormEvent) { event.preventDefault(); void open({ kind, id }); }
  return <main className="p-5 space-y-6">
    <h1 className="text-2xl font-semibold">Financial records</h1>
    <p className="text-slate-400">Saved Coordinator and Funding records. Values and versions come from the connected Go services. Verified local DEMO bindings enable explicit actions; missing sources and facts remain explicit.</p>
    <div className="grid grid-cols-1 xl:grid-cols-2 gap-5"><Cases api={api} open={selection => void open(selection)} /><Servicing api={api} open={selection => void open(selection)} /></div>
    <Card title="Saved record detail">
      <form onSubmit={lookup} className="flex flex-wrap items-end gap-3">
        <label className="text-sm">Record type<select className="block bg-slate-900 border border-slate-600 rounded p-2" value={kind} onChange={event => setKind(event.target.value as Selection['kind'])}><option value="offer">Offer</option><option value="bridge">Bridge</option><option value="case">Case</option><option value="operation">Operation</option></select></label>
        <label className="text-sm flex-1">Saved record UUID<input className="block w-full bg-slate-900 border border-slate-600 rounded p-2" required value={id} onChange={event => setID(event.target.value)} /></label>
        <button type="submit" className={button}>Read saved record</button>
      </form>
      {selected && <button type="button" className={button} disabled={state.busy} onClick={() => void open(selected)}>Refresh selected record</button>}
      <ErrorMessage state={state} />
      {!selected && <p>Select a saved case/change or enter a known record UUID.</p>}
      {state.data && <FinancialSnapshot record={state.data} nextCollections={() => void nextCollections()} busy={state.busy} />}
      {state.data && <FinancialActions record={state.data} refresh={() => { if (selected) void open(selected); }} />}
    </Card>
    <CommandRecovery />
  </main>;
}
