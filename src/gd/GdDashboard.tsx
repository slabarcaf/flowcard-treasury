import type { ReactNode } from 'react';
import { useDashboard } from '../hooks/useDashboard.js';
import { CapitalBar } from '../components/CapitalBar.js';
import { BridgeQueue } from '../components/BridgeQueue.js';
import { ActivityFeed } from '../components/ActivityFeed.js';
import { FeedToast } from '../components/FeedToast.js';
import { Sidebar } from '../components/Sidebar.js';

/** GD's original demo, unchanged apart from the shared sidebar and the version toggle slot. */
export function GdDashboard({ versionToggle }: { versionToggle: ReactNode }) {
  const { state, scan, selectCustomer, bloomReveal, bloomReEvaluate, approve, collect, reset } = useDashboard();

  const totalEligibleShortfall = state.queue
    .filter((r) => r.eligible)
    .reduce((sum, r) => sum + r.shortfall, 0);

  return (
    <div className="min-h-screen bg-gray-50 text-gray-900 flex">
      <FeedToast events={state.feed} />
      <Sidebar />

      {/* Main area */}
      <div className="flex-1 flex flex-col min-h-0">
        {/* Top header bar */}
        <header className="bg-white border-b border-gray-200 px-6 py-3 flex items-center justify-between shrink-0">
          <div className="flex items-center gap-4">
            <h1 className="text-base font-semibold text-gray-900">Dashboard</h1>
            <span className="text-xs text-gray-400 font-mono">{state.demoClock}</span>
          </div>
          <div className="flex items-center gap-3">
            {versionToggle}
            {state.phase === 'idle' && (
              <button
                onClick={scan}
                className="px-4 py-2 text-sm font-medium bg-[#e04e2f] hover:bg-[#c9442a] active:bg-[#b33d25] text-white rounded-lg transition-colors cursor-pointer"
              >
                Scan Declines
              </button>
            )}
            {state.phase === 'funded' && !state.showCollection && (
              <button
                onClick={collect}
                className="px-4 py-2 text-sm font-medium bg-emerald-600 hover:bg-emerald-700 active:bg-emerald-800 text-white rounded-lg transition-colors cursor-pointer"
              >
                Simulate Collection
              </button>
            )}
            {state.phase !== 'idle' && (
              <button
                onClick={reset}
                className="px-3 py-2 text-sm font-medium text-gray-500 hover:text-gray-700 border border-gray-300 hover:border-gray-400 rounded-lg transition-colors cursor-pointer"
              >
                Reset
              </button>
            )}
            <div className="w-8 h-8 rounded-full bg-gray-100 border border-gray-200 flex items-center justify-center text-xs font-semibold text-gray-500">
              CO
            </div>
          </div>
        </header>

        {/* Content */}
        <div className="flex flex-1 min-h-0">
          {/* Main content */}
          <div className="flex-1 min-w-0 p-6 overflow-y-auto space-y-5">
            {/* Capital bar */}
            <CapitalBar
              lentOut={state.lentOut}
              deployable={state.deployableCapital}
              floor={state.floor}
              band={state.band}
            />

            {/* Bridge queue */}
            <BridgeQueue
              rows={state.queue}
              totalEligibleShortfall={totalEligibleShortfall}
              deployable={state.deployableCapital}
              customerDetails={state.customerDetails}
              selectedCustomerId={state.selectedCustomerId}
              processedCustomers={state.processedCustomers}
              showCollection={state.showCollection}
              collectionRows={state.collectionRows}
              onSelectCustomer={selectCustomer}
              onBloomReveal={bloomReveal}
              onBloomReEvaluate={bloomReEvaluate}
              onApprove={approve}
            />
          </div>

          {/* Activity feed */}
          <ActivityFeed events={state.feed} />
        </div>
      </div>
    </div>
  );
}

