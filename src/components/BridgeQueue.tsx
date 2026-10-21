import { Fragment } from 'react';
import type { QueueRow, CustomerDetail, CollectionRow } from '../state/types.js';
import { CustomerDetailPanel } from './CustomerDetail.js';

interface Props {
  rows: QueueRow[];
  totalEligibleShortfall: number;
  deployable: number;
  customerDetails: Record<string, CustomerDetail>;
  selectedCustomerId: string | null;
  processedCustomers: string[];
  showCollection: boolean;
  collectionRows: CollectionRow[];
  onSelectCustomer: (customerId: string) => void;
  onBloomReveal: () => void;
  onBloomReEvaluate: () => void;
  onApprove: (customerId: string) => void;
  /** Kit 5: show funded and collect-by dates under each status. */
  showDates?: boolean;
}

function fmt(n: number): string {
  return '$' + n.toLocaleString('en-US');
}

const TIER_COLORS: Record<string, string> = {
  A: 'text-emerald-600',
  B: 'text-amber-600',
  C: 'text-red-600',
  none: 'text-gray-400',
};

const DOOR_LABELS: Record<string, { text: string; cls: string }> = {
  auto: { text: 'Auto', cls: 'text-emerald-600' },
  human: { text: 'Human', cls: 'text-amber-600' },
  closed: { text: 'Closed', cls: 'text-red-600' },
};

const STATUS_COLORS: Record<string, string> = {
  Detected: 'text-sky-600',
  Planned: 'text-sky-600',
  'Pending approval': 'text-amber-600',
  Approved: 'text-amber-600',
  Evaluating: 'text-sky-600',
  Funded: 'text-emerald-600',
  Repaid: 'text-emerald-600',
  Declined: 'text-red-600',
  Ineligible: 'text-red-500',
  '—': 'text-gray-400',
};

export function BridgeQueue({
  rows,
  totalEligibleShortfall,
  deployable,
  customerDetails,
  selectedCustomerId,
  processedCustomers,
  showCollection,
  collectionRows,
  onSelectCustomer,
  onBloomReveal,
  onBloomReEvaluate,
  onApprove,
  showDates = false,
}: Props) {
  // Kit 5 adds dates under the status; tighter cells keep the table inside the card.
  const pad = showDates ? 'px-2.5' : 'px-4';

  if (rows.length === 0) {
    return (
      <div className="bg-white rounded-xl border border-gray-200 p-10 flex items-center justify-center text-gray-400 text-sm">
        No declines in queue
      </div>
    );
  }

  return (
    <div className="bg-white rounded-xl border border-gray-200 overflow-x-hidden overflow-y-visible">
      {/* Banner */}
      {totalEligibleShortfall > 0 && (
        <div className="bg-gray-50 border-b border-gray-200 px-5 py-3 flex items-center justify-between">
          <span className="text-sm text-gray-600">
            Eligible shortfalls{' '}
            <span className="font-mono font-semibold text-gray-900">
              {fmt(totalEligibleShortfall)}
            </span>
          </span>
          <span className="text-sm text-gray-500">
            Deployable{' '}
            <span className="font-mono font-semibold text-amber-600">
              {fmt(deployable)}
            </span>
          </span>
        </div>
      )}

      {/* Table */}
      <table className="w-full text-sm">
        <thead>
          <tr className="bg-gray-50 text-gray-500 text-xs uppercase tracking-wider border-b border-gray-200">
            <th className={`text-left ${showDates ? 'px-3' : 'px-5'} py-3 font-medium`}>Customer</th>
            <th className={`text-left ${pad} py-3 font-medium`}>Payment</th>
            <th className={`text-right ${pad} py-3 font-medium`}>Shortfall</th>
            <th className={`text-center ${pad} py-3 font-medium`}>Detected</th>
            <th className={`text-center ${pad} py-3 font-medium`}>Tier</th>
            <th className={`text-center ${pad} py-3 font-medium`}>Door</th>
            <th className={`text-right ${pad} py-3 font-medium`}>Allocation</th>
            <th className={`text-left ${pad} py-3 font-medium`}>Status</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((row) => {
            const isSelected = row.customerId === selectedCustomerId;
            const detail = customerDetails[row.customerId];
            const isProcessed = processedCustomers.includes(row.customerId);

            return (
              <Fragment key={row.id}>
                <tr
                  onClick={() => onSelectCustomer(row.customerId)}
                  className={`border-b border-gray-100 transition-colors duration-150 cursor-pointer ${
                    !row.eligible && isProcessed ? 'opacity-45' : ''
                  } ${isSelected ? 'bg-orange-50/60' : 'hover:bg-gray-50'}`}
                >
                  <td className={`${showDates ? 'px-3' : 'px-5'} py-3.5`}>
                    <div className="text-gray-900 font-medium">{row.customerName}</div>
                    {!row.eligible && isProcessed && (
                      <div className="text-xs text-gray-400 mt-0.5">
                        {row.ineligibleReason}
                      </div>
                    )}
                  </td>
                  <td className={`${pad} py-3.5 text-gray-600`}>{row.paymentDescription}</td>
                  <td className={`${pad} py-3.5 text-right font-mono text-red-600`}>
                    {fmt(row.shortfall)}
                  </td>
                  <td className={`${pad} py-3.5 text-center text-gray-400 font-mono text-xs`}>
                    {row.detectedAt}
                  </td>
                  {/* Tier — show "—" before processed */}
                  <td
                    className={`${pad} py-3.5 text-center font-semibold ${
                      isProcessed ? (TIER_COLORS[row.tier] ?? 'text-gray-400') : 'text-gray-400'
                    }`}
                  >
                    {isProcessed ? (row.tier === 'none' ? '—' : row.tier) : '—'}
                  </td>
                  {/* Door — show "—" before processed */}
                  <td
                    className={`${pad} py-3.5 text-center text-xs font-medium ${
                      isProcessed ? (DOOR_LABELS[row.door]?.cls ?? 'text-gray-400') : 'text-gray-400'
                    }`}
                  >
                    {isProcessed ? (DOOR_LABELS[row.door]?.text ?? '—') : '—'}
                  </td>
                  {/* Allocation — show "—" before processed */}
                  <td className={`${pad} py-3.5 text-right font-mono text-gray-900`}>
                    {isProcessed ? (row.allocation > 0 ? fmt(row.allocation) : '—') : '—'}
                  </td>
                  {/* Status */}
                  <td
                    className={`${pad} py-3.5 text-xs font-medium ${
                      STATUS_COLORS[row.status] ?? 'text-gray-400'
                    }`}
                  >
                    {row.status}
                    {showDates && row.fundedOn && (
                      <div className="mt-0.5 font-normal text-[11px] leading-4 whitespace-nowrap">
                        <div className="text-gray-500">Funded <span className="font-mono">{row.fundedOn}</span></div>
                        {row.repaidOn ? (
                          <div className="text-emerald-600">Repaid <span className="font-mono">{row.repaidOn}</span></div>
                        ) : (
                          <div className="text-gray-500">Collect by <span className="font-mono">{row.dueOn}</span></div>
                        )}
                      </div>
                    )}
                  </td>
                </tr>
                {isSelected && detail && (
                  <tr>
                    <td colSpan={8} className="p-0">
                      <CustomerDetailPanel
                        detail={detail}
                        onBloomReveal={onBloomReveal}
                        onBloomReEvaluate={onBloomReEvaluate}
                        onApprove={onApprove}
                        showCollection={showCollection}
                        collectionRows={collectionRows}
                      />
                    </td>
                  </tr>
                )}
              </Fragment>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
