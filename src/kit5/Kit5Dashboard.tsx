import { useState, type ReactNode } from 'react';
import { CapitalBar } from '../components/CapitalBar.js';
import { BridgeQueue } from '../components/BridgeQueue.js';
import { ActivityFeed } from '../components/ActivityFeed.js';
import { FeedToast } from '../components/FeedToast.js';
import { Sidebar } from '../components/Sidebar.js';
import { useKit5Dashboard } from './useKit5Dashboard.js';
import { nextEvent } from './engine.js';
import { SettingsPanel } from './SettingsPanel.js';

/** Kit 5 version: 20% reserve floor, requests decided one at a time as they arrive. */
export function Kit5Dashboard({ versionToggle }: { versionToggle: ReactNode }) {
  const k = useKit5Dashboard();
  const { state } = k;
  const [settingsOpen, setSettingsOpen] = useState(false);

  const upcoming = nextEvent(state);
  const awaitingCollection = state.phase !== 'idle' && !state.pause && upcoming?.type === 'repayment';
  const finished = state.phase !== 'idle' && !state.pause && !upcoming;

  const totalEligibleShortfall = state.queue
    .filter((r) => r.eligible)
    .reduce((sum, r) => sum + r.shortfall, 0);

  let statusLine = k.scenario.summary;
  if (state.pause?.kind === 'human') statusLine = 'Waiting on the credit officer. The next request is decided after this one.';
  else if (state.pause?.kind === 'crm') statusLine = 'Agent found new Salesforce information. Re-evaluate to decide this request.';
  else if (awaitingCollection) statusLine = 'All requests for the day are decided. Simulate the next collection to move the clock forward.';
  else if (finished) statusLine = 'Scenario complete.';

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
            {state.phase === 'idle' && (
              <button
                onClick={k.scan}
                className="px-4 py-2 text-sm font-medium bg-[#e04e2f] hover:bg-[#c9442a] active:bg-[#b33d25] text-white rounded-lg transition-colors cursor-pointer"
              >
                Scan Declines
              </button>
            )}
            {awaitingCollection && (
              <button
                onClick={k.advance}
                className="px-4 py-2 text-sm font-medium bg-emerald-600 hover:bg-emerald-700 active:bg-emerald-800 text-white rounded-lg transition-colors cursor-pointer"
              >
                Simulate Collection
              </button>
            )}
            {state.phase !== 'idle' && (
              <button
                onClick={k.reset}
                className="px-3 py-2 text-sm font-medium text-gray-500 hover:text-gray-700 border border-gray-300 hover:border-gray-400 rounded-lg transition-colors cursor-pointer"
              >
                Reset
              </button>
            )}
            <button
              onClick={() => setSettingsOpen(true)}
              aria-label="Scenario inputs"
              title="Scenario inputs"
              className="w-8 h-8 rounded-lg border border-gray-200 hover:border-gray-300 text-gray-500 hover:text-gray-700 flex items-center justify-center cursor-pointer"
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

            <CapitalBar
              lentOut={state.lentOut}
              deployable={state.deployableCapital}
              floor={state.floor}
              band={state.band}
            />

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
            />
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
