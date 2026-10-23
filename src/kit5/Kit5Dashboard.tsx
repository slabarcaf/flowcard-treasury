import { useState, type FormEvent, type ReactNode } from 'react';
import { CapitalBar } from '../components/CapitalBar.js';
import { BridgeQueue } from '../components/BridgeQueue.js';
import { ActivityFeed } from '../components/ActivityFeed.js';
import { FeedToast } from '../components/FeedToast.js';
import { Sidebar } from '../components/Sidebar.js';
import {
  canApproveKit5Decision,
  canCreateNovaRepaymentDeposit,
  fixtureCheckpointControls,
  fixtureCheckpointReceipt,
  fixtureCheckpointRecoveryState,
  useKit5Dashboard,
} from './useKit5Dashboard.js';
import { nextEvent, shortDate } from './engine.js';
import { SettingsPanel } from './SettingsPanel.js';
import { formatCollectionCharge } from './live-format.js';

/** Kit 5 version: 20% reserve floor, requests decided one at a time as they arrive. */
export function Kit5Dashboard({ versionToggle }: { versionToggle: ReactNode }) {
  const k = useKit5Dashboard();
  const { state } = k;
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [operatorToken, setOperatorToken] = useState('');

  const upcoming = nextEvent(state);
  const awaitingCollection = state.phase !== 'idle' && !state.pause && upcoming?.type === 'repayment';
  const finished = state.phase !== 'idle' && !state.pause && !upcoming;

  const totalEligibleShortfall = state.queue
    .filter((r) => r.eligible)
    .reduce((sum, r) => sum + r.shortfall, 0);

  const autoApproved = state.pause?.kind === 'ack' ? state.queue.find((r) => r.id === state.pause!.requestId) : undefined;
  const autoDetail = autoApproved ? state.customerDetails[autoApproved.id] : undefined;
  const outstanding = Object.values(state.bridges);
  const checkpointRequestId = k.scenario.providerCheckpoint?.eventRequestId;
  const checkpointDecision = checkpointRequestId
    ? state.queue.find((row) => row.id === checkpointRequestId)
    : undefined;
  const checkpointReceipt = fixtureCheckpointReceipt(k.fixtureCheckpointActivity);
  const checkpointRecoveryState = fixtureCheckpointRecoveryState(
    k.fixtureCheckpointActivity,
    k.fixtureCheckpointResolution,
    k.fixtureCheckpointPending,
    k.fixtureCheckpointPaused,
  );
  const checkpointControls = fixtureCheckpointControls({
    mode: k.mode,
    paused: k.fixtureCheckpointPaused,
    pending: k.fixtureCheckpointPending,
    resolution: k.fixtureCheckpointResolution,
    activity: k.fixtureCheckpointActivity,
  });
  const fixtureMode = k.mode !== 'sandbox-live';
  const checkpointChoiceRequired = k.mode === 'fixture-checkpoint' &&
    k.fixtureCheckpointActivity !== null && k.fixtureCheckpointResolution === 'awaiting';

  let statusLine = k.scenario.summary;
  if (autoApproved) statusLine = `${autoApproved.customerName} was auto-approved. Continue when you're ready for the next request.`;
  else if (state.pause?.kind === 'human') statusLine = 'Waiting on the credit officer. The next request is decided after this one.';
  else if (state.pause?.kind === 'crm') statusLine = 'Agent found new Salesforce information. Re-evaluate to decide this request.';
  else if (awaitingCollection) statusLine = 'All requests for the day are decided. Simulate the next collection to move the clock forward.';
  else if (k.mode === 'fixture-checkpoint' && k.fixtureCheckpointPaused) statusLine = 'Bloom’s event is paused for the Airwallex checkpoint. The fixture clock and decision will wait for presenter input.';
  else if (finished) {
    statusLine = outstanding.length === 0
      ? 'Scenario complete. Every bridge has been repaid.'
      : `Scenario complete. Still outstanding: ${outstanding
          .map((b) => `${b.customerName} $${b.amount.toLocaleString('en-US')}, collect by ${shortDate(state.inputs.startDate, b.dueDay)}`)
          .join('; ')}.`;
  }

  return (
    <div className="min-h-screen bg-gray-50 text-gray-900 flex">
      <FeedToast events={state.feed} />
      <Sidebar />

      <div className="flex-1 flex flex-col min-h-0 min-w-0">
        <header className="bg-white border-b border-gray-200 px-6 py-3 flex items-center justify-between gap-4 shrink-0">
          <div className="flex items-center gap-4 min-w-0">
            <h1 className="text-base font-semibold text-gray-900">Dashboard</h1>
            <span className="text-xs text-gray-400 font-mono whitespace-nowrap">{state.demoClock}</span>
            <label className="flex items-center gap-2 text-xs text-gray-500">
              Scenario
              <select
                value={k.scenario.id}
                disabled={k.fixtureCheckpointPending || k.fixtureCheckpointUnresolved || checkpointChoiceRequired}
                onChange={(e) => k.selectScenario(e.target.value)}
                className="text-sm text-gray-900 bg-white border border-gray-300 rounded-md px-2 py-1.5 cursor-pointer"
              >
                {k.scenarios.map((s) => (
                  <option key={s.id} value={s.id}>{s.name}</option>
                ))}
              </select>
            </label>
          </div>
          <div className="flex items-center gap-3">
            {versionToggle}
            <label className="flex items-center gap-2 text-xs text-gray-500">
              Mode
              <select
                aria-label="Demo mode"
                value={k.mode}
                disabled={k.liveBlocked || k.fixtureCheckpointPending || k.fixtureCheckpointUnresolved || checkpointChoiceRequired}
                onChange={(event) => k.selectMode(event.target.value as 'fixture' | 'fixture-checkpoint' | 'sandbox-live')}
                className="text-sm text-gray-900 bg-white border border-gray-300 rounded-md px-2 py-1.5 cursor-pointer"
              >
                <option value="fixture">Fixture</option>
                <option value="fixture-checkpoint" disabled={!k.scenario.providerCheckpoint}>Fixture + Airwallex checkpoint</option>
                <option value="sandbox-live">Sandbox live</option>
              </select>
            </label>
            {k.mode === 'sandbox-live' && (
              <>
                <button
                  type="button"
                  onClick={() => void k.submitNextCustomer()}
                  disabled={!k.operatorAuthenticated || k.livePending || k.liveBlocked || !k.nextLiveCustomer || !k.ledgerApiEnabled}
                  className="px-4 py-2 text-sm font-medium bg-[#e04e2f] hover:bg-[#c9442a] disabled:bg-gray-300 disabled:cursor-not-allowed text-white rounded-lg transition-colors cursor-pointer"
                >
                  {k.livePending ? 'Submitting…' : k.liveComplete ? 'Sequence complete' : k.liveAction?.status === 'not_persisted' ? 'Retry same customer' : 'Next customer'}
                </button>
                <button
                  type="button"
                  onClick={() => {
                    const confirmed = window.confirm('Start a fresh Sandbox live sequence? Previous provider attempts will remain recorded. Each Next customer click will create a new Sandbox transaction.');
                    if (confirmed) k.restartLiveSequence();
                  }}
                  disabled={k.livePending || k.liveBlocked || k.liveAction?.status === 'not_persisted'}
                  title="Start a new live run. Previous attempts remain recorded."
                  className="px-3 py-2 text-sm font-medium text-gray-600 border border-gray-300 hover:border-gray-400 disabled:opacity-50 disabled:cursor-not-allowed rounded-lg transition-colors cursor-pointer"
                >
                  Restart sequence
                </button>
              </>
            )}
            {fixtureMode && state.phase === 'idle' && (
              <button
                onClick={k.scan}
                className="px-4 py-2 text-sm font-medium bg-[#e04e2f] hover:bg-[#c9442a] active:bg-[#b33d25] text-white rounded-lg transition-colors cursor-pointer"
              >
                Scan Declines
              </button>
            )}
            {fixtureMode && awaitingCollection && (
              <button
                onClick={k.advance}
                className="px-4 py-2 text-sm font-medium bg-emerald-600 hover:bg-emerald-700 active:bg-emerald-800 text-white rounded-lg transition-colors cursor-pointer"
              >
                Simulate Collection
              </button>
            )}
            {fixtureMode && state.phase !== 'idle' && (
              <button
                onClick={k.reset}
                disabled={checkpointChoiceRequired}
                className="px-3 py-2 text-sm font-medium text-gray-500 hover:text-gray-700 border border-gray-300 hover:border-gray-400 disabled:opacity-50 disabled:cursor-not-allowed rounded-lg transition-colors cursor-pointer"
              >
                Reset
              </button>
            )}
            <button
              onClick={() => setSettingsOpen(true)}
              disabled={checkpointChoiceRequired}
              aria-label="Scenario inputs"
              title="Scenario inputs"
              className="w-8 h-8 rounded-lg border border-gray-200 hover:border-gray-300 text-gray-500 hover:text-gray-700 disabled:opacity-50 disabled:cursor-not-allowed flex items-center justify-center cursor-pointer"
            >
              <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}><path strokeLinecap="round" strokeLinejoin="round" d="M10.5 6h9.75M10.5 6a1.5 1.5 0 11-3 0m3 0a1.5 1.5 0 10-3 0M3.75 6H7.5m3 12h9.75m-9.75 0a1.5 1.5 0 01-3 0m3 0a1.5 1.5 0 00-3 0m-3.75 0H7.5m9-6h3.75m-3.75 0a1.5 1.5 0 01-3 0m3 0a1.5 1.5 0 00-3 0m-9.75 0h9.75" /></svg>
            </button>
            <div className="w-8 h-8 rounded-full bg-gray-100 border border-gray-200 flex items-center justify-center text-xs font-semibold text-gray-500">
              CO
            </div>
          </div>
        </header>

        <div className="flex flex-1 min-h-0">
          <div className="flex-1 min-w-0 p-6 overflow-y-auto space-y-5">
            <div className="text-sm text-gray-600 bg-white border border-gray-200 rounded-xl px-5 py-3">
              <span className="font-medium text-gray-900">{k.scenario.name}.</span> {statusLine}
              {JSON.stringify(k.inputs) !== JSON.stringify(k.scenario.inputs) && (
                <span className="ml-2 text-[11px] font-medium text-amber-700 bg-amber-50 border border-amber-200 rounded-full px-2 py-0.5">
                  Edited inputs
                </span>
              )}
            </div>

            {k.mode === 'fixture-checkpoint' && (
              <section aria-label="Fixture and Airwallex checkpoint" className="bg-white border border-gray-200 rounded-xl px-5 py-4 space-y-3">
                <div>
                  <h2 className="text-sm font-semibold text-gray-900">Fixture + Airwallex checkpoint</h2>
                  <p className="text-sm text-gray-600 mt-1">
                    The Airwallex receipt records one Sandbox attempt. The fixture engine makes and displays its policy decision separately.
                  </p>
                </div>
                {!k.ledgerApiEnabled && <p role="alert" className="text-sm text-amber-700">Set VITE_BRIDGE_API_URL to enable the Airwallex checkpoint API.</p>}
                {!k.operatorAuthenticated && (
                  <form
                    className="flex items-end gap-2"
                    onSubmit={(event: FormEvent<HTMLFormElement>) => {
                      event.preventDefault();
                      void k.authenticateOperator(operatorToken)
                        .then(() => setOperatorToken(''))
                        .catch(() => {});
                    }}
                  >
                    <label className="flex flex-col gap-1 text-xs text-gray-600">
                      Operator credential
                      <input
                        type="password"
                        autoComplete="current-password"
                        value={operatorToken}
                        onChange={(event) => setOperatorToken(event.target.value)}
                        className="border border-gray-300 rounded-md px-2 py-1.5 text-sm"
                      />
                    </label>
                    <button type="submit" disabled={!operatorToken || !k.ledgerApiEnabled || k.fixtureCheckpointPending} className="px-3 py-1.5 text-sm border rounded-md disabled:opacity-50">Authorize</button>
                  </form>
                )}
                {k.liveError && !k.operatorAuthenticated && <p role="alert" className="text-sm text-red-700">{k.liveError}</p>}
                {k.fixtureCheckpointPaused && !k.fixtureCheckpointActivity && (
                  <p className="text-sm text-gray-600">Bloom’s $9,000 supplier payment is waiting at the provider checkpoint. The scenario clock and fixture decision remain paused.</p>
                )}
                {k.fixtureCheckpointPending && <p role="status" className="text-sm text-sky-700">Airwallex request in progress. The scenario clock and fixture decision remain paused.</p>}
                {k.fixtureCheckpointError && <p role="alert" className="text-sm text-amber-800">{k.fixtureCheckpointError}</p>}
                {k.fixtureCheckpointPaused && (
                  <div className="flex flex-wrap gap-2">
                    {checkpointControls.includes('run-attempt') && (
                      <button
                        type="button"
                        onClick={() => void k.runFixtureCheckpoint()}
                        disabled={!k.operatorAuthenticated || !k.ledgerApiEnabled || k.fixtureCheckpointPending}
                        className="px-4 py-2 text-sm font-medium bg-[#e04e2f] hover:bg-[#c9442a] disabled:bg-gray-300 disabled:cursor-not-allowed text-white rounded-lg transition-colors cursor-pointer"
                      >
                        Run Airwallex Sandbox attempt
                      </button>
                    )}
                    {checkpointControls.includes('reconcile-attempt') && (
                      <button
                        type="button"
                        onClick={() => void k.reconcileFixtureCheckpoint()}
                        disabled={!k.operatorAuthenticated || !k.ledgerApiEnabled || k.fixtureCheckpointPending}
                        className="px-4 py-2 text-sm font-medium border border-sky-300 text-sky-800 hover:bg-sky-50 disabled:opacity-50 disabled:cursor-not-allowed rounded-lg transition-colors cursor-pointer"
                      >
                        Reconcile Airwallex checkpoint
                      </button>
                    )}
                    {checkpointControls.includes('continue-fixture') && (
                      <button
                        type="button"
                        onClick={k.confirmFixtureCheckpoint}
                        className="px-4 py-2 text-sm font-medium bg-emerald-600 hover:bg-emerald-700 text-white rounded-lg transition-colors cursor-pointer"
                      >
                        Continue fixture sequence
                      </button>
                    )}
                    {checkpointControls.includes('continue-fixture-only') && (
                      <button
                        type="button"
                        onClick={k.continueFixtureOnly}
                        className="px-4 py-2 text-sm font-medium border border-amber-300 text-amber-900 hover:bg-amber-50 rounded-lg transition-colors cursor-pointer"
                      >
                        Continue fixture-only
                      </button>
                    )}
                  </div>
                )}
                <p role="status" className="text-sm text-gray-700">Recovery state: {checkpointRecoveryState}</p>

                <section aria-label="Airwallex provider receipt" className="border-t border-gray-100 pt-3">
                  <h3 className="text-xs font-semibold uppercase tracking-wide text-gray-500">Airwallex provider receipt</h3>
                  <dl className="grid grid-cols-[max-content_1fr] gap-x-3 gap-y-1 mt-2 text-xs text-gray-600">
                    <dt>Provider result</dt><dd>{checkpointReceipt.providerResult}</dd>
                    <dt>Attempt status</dt><dd>{checkpointReceipt.attemptStatus}</dd>
                    <dt>Server-resolved amount</dt><dd>{checkpointReceipt.amountCents === null ? 'Unavailable' : `$${(checkpointReceipt.amountCents / 100).toLocaleString('en-US')} ${checkpointReceipt.currency}`}</dd>
                    <dt>Failure reason</dt><dd>{checkpointReceipt.failureReason}</dd>
                    <dt>Provider event ID</dt><dd className="font-mono break-all">{checkpointReceipt.eventId}</dd>
                    <dt>Provider transaction ID</dt><dd className="font-mono break-all">{checkpointReceipt.transactionId}</dd>
                    <dt>Lifecycle ID</dt><dd className="font-mono break-all">{checkpointReceipt.lifecycleId}</dd>
                    {k.fixtureCheckpointActivity?.attemptId && <><dt>Attempt ID</dt><dd className="font-mono break-all">{k.fixtureCheckpointActivity.attemptId}</dd></>}
                    {k.fixtureCheckpointActivity?.reason && <><dt>Server response</dt><dd>{k.fixtureCheckpointActivity.reason}</dd></>}
                  </dl>
                </section>

                <section aria-label="Fixture policy decision" className="border-t border-gray-100 pt-3">
                  <h3 className="text-xs font-semibold uppercase tracking-wide text-gray-500">Fixture policy decision</h3>
                  <dl className="grid grid-cols-[max-content_1fr] gap-x-3 gap-y-1 mt-2 text-xs text-gray-600">
                    <dt>Bloom decision</dt><dd>{checkpointDecision?.status ?? 'Waiting for fixture continuation'}</dd>
                    <dt>Fixture shortfall</dt><dd>{checkpointDecision ? `$${checkpointDecision.shortfall.toLocaleString('en-US')}` : 'Not calculated yet'}</dd>
                    <dt>Fixture allocation</dt><dd>{checkpointDecision ? `$${checkpointDecision.allocation.toLocaleString('en-US')}` : 'Not decided yet'}</dd>
                  </dl>
                </section>
              </section>
            )}

            {k.mode === 'sandbox-live' && (
              <section aria-label="Sandbox live provider activity" className="bg-white border border-gray-200 rounded-xl px-5 py-4 space-y-2">
                <h2 className="text-sm font-semibold text-gray-900">Sandbox live provider activity</h2>
                {!k.ledgerApiEnabled && <p role="alert" className="text-sm text-amber-700">Set VITE_BRIDGE_API_URL to enable the sandbox-live API.</p>}
                {!k.operatorAuthenticated && (
                  <form
                    className="flex items-end gap-2"
                    onSubmit={(event: FormEvent<HTMLFormElement>) => {
                      event.preventDefault();
                      void k.authenticateOperator(operatorToken)
                        .then(() => setOperatorToken(''))
                        .catch(() => {});
                    }}
                  >
                    <label className="flex flex-col gap-1 text-xs text-gray-600">
                      Operator credential
                      <input
                        type="password"
                        autoComplete="current-password"
                        value={operatorToken}
                        onChange={(event) => setOperatorToken(event.target.value)}
                        className="border border-gray-300 rounded-md px-2 py-1.5 text-sm"
                      />
                    </label>
                    <button type="submit" disabled={!operatorToken || k.livePending} className="px-3 py-1.5 text-sm border rounded-md disabled:opacity-50">Authorize</button>
                  </form>
                )}
                {k.nextLiveCustomer && !k.liveBlocked && (
                  <p className="text-sm text-gray-600">
                    Next: {k.nextLiveCustomer.customer.name}, ${k.nextLiveCustomer.event.paymentAmount.toLocaleString('en-US')} {k.nextLiveCustomer.event.paymentDescription}
                  </p>
                )}
                {k.livePending && <p role="status" className="text-sm text-sky-700">Waiting for the provider result…</p>}
                {k.liveError && <p role="alert" className="text-sm text-red-700">{k.liveError}</p>}
                {k.liveHistory.filter((activity) => activity.attemptId !== k.liveAction?.attemptId).map((activity) => (
                  <div key={activity.attemptId} className="border-t border-gray-100 pt-2">
                  <dl className="grid grid-cols-[max-content_1fr] gap-x-3 gap-y-1 text-xs text-gray-600">
                    <dt>Customer</dt><dd>{activity.customerName}</dd>
                    <dt>Transaction status</dt><dd>{activity.result?.processResult ?? activity.status}</dd>
                    <dt>Scenario amount attempted</dt><dd>{activity.amount === undefined ? 'Unavailable' : `$${activity.amount.toLocaleString('en-US')} USD (provider response does not include amount)`}</dd>
                    <dt>Failure reason</dt><dd>{activity.result?.failureReason ?? 'None returned'}</dd>
                    <dt>Provider event ID</dt><dd className="font-mono break-all">{activity.result?.cardTransactionEventId ?? 'Unavailable'}</dd>
                    <dt>Provider transaction ID</dt><dd className="font-mono break-all">{activity.result?.cardTransactionId ?? 'Unavailable'}</dd>
                    <dt>Lifecycle ID</dt><dd className="font-mono break-all">{activity.result?.cardTransactionLifecycleId ?? activity.lifecycleId ?? 'Unavailable'}</dd>
                    <dt>Policy status</dt><dd>{activity.policyStatus ?? 'Pending evaluation'}</dd>
                    {activity.policyError && <><dt>Policy recovery</dt><dd>{activity.policyError}</dd></>}
                    {activity.policyDecision && <>
                      <dt>Proposed bridge</dt><dd>${(activity.policyDecision.amountCents / 100).toLocaleString('en-US')} USD</dd>
                      <dt>Credit line</dt><dd>${(activity.policyDecision.creditLineCents / 100).toLocaleString('en-US')} USD</dd>
                      <dt>Tier / capital door</dt><dd>{activity.policyDecision.tier} / {activity.policyDecision.capitalBand ?? 'unavailable'} / {activity.policyDecision.door ?? 'unavailable'}</dd>
                      <dt>Policy reasons</dt><dd>{activity.policyDecision.reasonCodes.join(', ') || activity.policyDecision.rationale}</dd>
                    </>}
                    {activity.bridgeId && <><dt>Bridge ID</dt><dd className="font-mono break-all">{activity.bridgeId}</dd></>}
                    {activity.retryLifecycleId && <><dt>Card retry lifecycle</dt><dd className="font-mono break-all">{activity.retryLifecycleId}</dd></>}
                    {activity.retryResult && <><dt>Card retry result</dt><dd>{activity.retryResult.processResult} ({activity.retryResult.failureReason ?? 'no failure reason'})</dd></>}
                    {activity.retryError && <><dt>Card retry status</dt><dd>{activity.retryError}</dd></>}
                    {activity.repaymentDeposit && <>
                      <dt>Nova deposit status</dt><dd>{activity.repaymentDeposit.status} (provider: {activity.repaymentDeposit.providerStatus ?? 'unavailable'})</dd>
                      <dt>Nova provider deposit ID</dt><dd className="font-mono break-all">{activity.repaymentDeposit.depositId ?? 'Not returned'}</dd>
                      <dt>Fee collection</dt><dd>{formatCollectionCharge(activity.repaymentDeposit.collection?.fee)}</dd>
                      <dt>Principal collection</dt><dd>{formatCollectionCharge(activity.repaymentDeposit.collection?.principal)}</dd>
                    </>}
                    {activity.repaymentDepositError && <><dt>Nova deposit</dt><dd role="alert">{activity.repaymentDepositError}</dd></>}
                  </dl>
                  {(activity.status !== 'completed' || ['evaluating', 'pending_review', 'blocked', 'reserving', 'reserved', 'settled', 'retry_submitting', 'retry_ambiguous'].includes(activity.policyStatus ?? '')) && (
                    <button type="button" onClick={() => void k.refreshLiveHistoryActivity(activity)} disabled={k.livePending} className="mr-2 mt-2 px-3 py-1.5 text-sm border rounded-md disabled:opacity-50">
                      Refresh policy and provider status
                    </button>
                  )}
                  {canApproveKit5Decision(k.operatorAuthenticated && k.operatorApprovalAvailable, activity.policyStatus, activity.policyDecision) && (
                    <button type="button" onClick={() => {
                      const reason = window.prompt('Reason for approving the saved policy terms');
                      if (reason?.trim()) void k.approveLiveReview(reason.trim(), activity);
                    }} disabled={k.livePending} className="mt-2 px-3 py-1.5 text-sm border rounded-md disabled:opacity-50">
                      Approve saved terms
                    </button>
                  )}
                  {k.mode === 'sandbox-live' && canCreateNovaRepaymentDeposit(k.operatorAuthenticated, activity) && (
                    <button type="button" onClick={() => void k.createOrRefreshNovaRepaymentDeposit(activity)} disabled={k.livePending || !k.ledgerApiEnabled} className="ml-2 mt-2 px-3 py-1.5 text-sm border rounded-md disabled:opacity-50">
                      {activity.repaymentDeposit ? 'Refresh Nova $8,000 deposit and collection' : 'Create Nova $8,000 USD deposit'}
                    </button>
                  )}
                  </div>
                ))}
                {k.liveAction && (k.liveAction.status !== 'completed' || ['evaluating', 'pending_review', 'blocked', 'reserving', 'reserved', 'settled', 'retry_submitting', 'retry_ambiguous'].includes(k.liveAction.policyStatus ?? '')) && (
                  <dl className="grid grid-cols-[max-content_1fr] gap-x-3 gap-y-1 text-xs text-gray-600">
                    <dt>Customer</dt><dd>{k.liveAction.customerName}</dd>
                    <dt>Transaction status</dt><dd>{k.liveAction.result?.processResult ?? k.liveAction.status}</dd>
                    <dt>Scenario amount attempted</dt><dd>{k.liveAction.amount === undefined ? 'Unavailable' : `$${k.liveAction.amount.toLocaleString('en-US')} USD (provider response does not include amount)`}</dd>
                    <dt>Failure reason</dt><dd>{k.liveAction.result?.failureReason ?? 'None returned'}</dd>
                    <dt>Provider event ID</dt><dd className="font-mono break-all">{k.liveAction.result?.cardTransactionEventId ?? 'Unavailable'}</dd>
                    <dt>Provider transaction ID</dt><dd className="font-mono break-all">{k.liveAction.result?.cardTransactionId ?? 'Unavailable'}</dd>
                    <dt>Lifecycle ID</dt><dd className="font-mono break-all">{k.liveAction.result?.cardTransactionLifecycleId ?? k.liveAction.lifecycleId ?? 'Unavailable'}</dd>
                    <dt>Policy decision</dt><dd>{k.liveAction.policyStatus ?? 'Pending evaluation'}</dd>
                    {k.liveAction.policyError && <><dt>Policy recovery</dt><dd>{k.liveAction.policyError}</dd></>}
                    {k.liveAction.policyDecision && <>
                      <dt>Proposed bridge</dt><dd>${(k.liveAction.policyDecision.amountCents / 100).toLocaleString('en-US')} USD</dd>
                      <dt>Credit line</dt><dd>${(k.liveAction.policyDecision.creditLineCents / 100).toLocaleString('en-US')} USD</dd>
                      <dt>Tier / capital door</dt><dd>{k.liveAction.policyDecision.tier} / {k.liveAction.policyDecision.capitalBand ?? 'unavailable'} / {k.liveAction.policyDecision.door ?? 'unavailable'}</dd>
                      <dt>Policy reasons</dt><dd>{k.liveAction.policyDecision.reasonCodes.join(', ') || k.liveAction.policyDecision.rationale}</dd>
                    </>}
                    {k.liveAction.bridgeId && <><dt>Bridge ID</dt><dd className="font-mono break-all">{k.liveAction.bridgeId}</dd></>}
                    {k.liveAction.retryLifecycleId && <><dt>Card retry lifecycle</dt><dd className="font-mono break-all">{k.liveAction.retryLifecycleId}</dd></>}
                    {k.liveAction.retryResult && <><dt>Card retry result</dt><dd>{k.liveAction.retryResult.processResult} ({k.liveAction.retryResult.failureReason ?? 'no failure reason'})</dd></>}
                    {k.liveAction.retryError && <><dt>Card retry status</dt><dd>{k.liveAction.retryError}</dd></>}
                  </dl>
                )}
                {canApproveKit5Decision(k.operatorAuthenticated && k.operatorApprovalAvailable,
                  k.liveAction?.policyStatus, k.liveAction?.policyDecision) && k.liveAction?.attemptId && (
                  <button type="button" onClick={() => {
                    const reason = window.prompt('Reason for approving the saved policy terms');
                    if (reason?.trim()) void k.approveLiveReview(reason.trim());
                  }} disabled={k.livePending} className="px-3 py-1.5 text-sm border rounded-md disabled:opacity-50">
                    {k.livePending ? 'Submitting approval…' : 'Approve saved terms'}
                  </button>
                )}
                {k.liveAction && (k.liveAction.status !== 'completed' || ['evaluating', 'pending_review', 'blocked', 'reserving', 'reserved', 'settled', 'retry_submitting', 'retry_ambiguous'].includes(k.liveAction.policyStatus ?? '')) && k.liveAction.attemptId && (
                  <button type="button" onClick={() => void k.refreshLiveAction()} disabled={k.livePending} className="px-3 py-1.5 text-sm border rounded-md disabled:opacity-50">
                    {k.livePending ? 'Refreshing…' : 'Refresh provider status'}
                  </button>
                )}
                {k.liveComplete && <p role="status" className="text-sm text-gray-500">All customer actions have provider results. Restart starts a new run and keeps these provider attempts recorded.</p>}
              </section>
            )}

            <CapitalBar
              lentOut={state.lentOut}
              deployable={state.deployableCapital}
              floor={state.floor}
              band={state.band}
            />

            {fixtureMode && autoApproved && (
              <div className="flex items-center justify-between gap-4 bg-emerald-50 border border-emerald-200 rounded-xl px-5 py-4 animate-panel-in" role="status">
                <div className="min-w-0">
                  <div className="text-sm font-semibold text-emerald-800">
                    Auto-approved: {autoApproved.customerName}, ${autoApproved.allocation.toLocaleString('en-US')} funded
                  </div>
                  <div className="text-xs text-emerald-700 mt-1">
                    {autoDetail?.policyChecks[0]?.label.replace(`${autoApproved.customerName}: `, '')}.
                    {' '}Deployable ${(state.deployableCapital + autoApproved.allocation).toLocaleString('en-US')} → ${state.deployableCapital.toLocaleString('en-US')}.
                    {' '}Funded {autoApproved.fundedOn}, collect by {autoApproved.dueOn}.
                  </div>
                </div>
                <button
                  onClick={k.continueAfterAuto}
                  className="shrink-0 px-4 py-2 text-sm font-medium bg-emerald-600 hover:bg-emerald-700 active:bg-emerald-800 text-white rounded-lg transition-colors cursor-pointer"
                >
                  Continue
                </button>
              </div>
            )}

            {fixtureMode ? (
              <>
                {k.mode === 'fixture-checkpoint' && <h2 className="text-sm font-semibold text-gray-700">Fixture policy outcomes</h2>}
                <BridgeQueue
                  rows={state.queue}
                  totalEligibleShortfall={totalEligibleShortfall}
                  deployable={state.deployableCapital}
                  customerDetails={state.customerDetails}
                  selectedCustomerId={state.selectedCustomerId}
                  processedCustomers={state.processedCustomers}
                  showCollection={state.showCollection}
                  collectionRows={state.collectionRows}
                  onSelectCustomer={k.selectCustomer}
                  onBloomReveal={k.crmReveal}
                  onBloomReEvaluate={k.crmReEvaluate}
                  onApprove={k.approve}
                  showDates
                />
              </>
            ) : (
              <div className="bg-white border border-gray-200 rounded-xl px-5 py-4 text-sm text-gray-500">
                Fixture decisions are paused in sandbox-live mode. Provider outcomes are shown above.
              </div>
            )}
          </div>

          <ActivityFeed events={state.feed} />
        </div>
      </div>

      {settingsOpen && (
        <SettingsPanel
          inputs={k.inputs}
          defaults={k.scenario.inputs}
          onApply={(next) => {
            k.applyInputs(next);
            setSettingsOpen(false);
          }}
          onClose={() => setSettingsOpen(false)}
        />
      )}
    </div>
  );
}
