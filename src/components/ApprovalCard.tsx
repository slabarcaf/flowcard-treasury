import { NOVA_APPROVAL_TERMS } from '../seed.js';
import type { ApprovalTerms } from '../state/types.js';

interface Props {
  visible: boolean;
  terms?: ApprovalTerms;
  awaitingApproval: boolean;
  onApprove: () => void;
}

function fmt(n: number): string {
  return '$' + n.toLocaleString('en-US');
}

export function ApprovalCard({ visible, terms, awaitingApproval, onApprove }: Props) {
  if (!visible) return null;

  const t = terms ?? NOVA_APPROVAL_TERMS;

  return (
    <div className="bg-white border border-amber-300 rounded-lg p-5 animate-panel-in">
      <div className="flex items-center justify-between mb-4">
        <h4 className="text-xs font-semibold text-amber-700 uppercase tracking-wider">
          Credit Officer Approval
        </h4>
        {awaitingApproval && (
          <span className="text-[10px] bg-amber-100 text-amber-700 px-2 py-0.5 rounded-full animate-pulse font-medium">
            Awaiting
          </span>
        )}
      </div>

      <div className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-2 text-sm mb-5">
        <span className="text-gray-500">Amount</span>
        <span className="font-mono text-gray-900 font-semibold text-lg">
          {fmt(t.amount)} {t.currency}
        </span>

        <span className="text-gray-500">To</span>
        <span className="text-gray-800">{t.to}</span>

        <span className="text-gray-500">Covers</span>
        <span className="text-gray-600 text-xs">{t.covers}</span>

        <span className="text-gray-500">Term</span>
        <span className="text-gray-600 text-xs">{t.term}</span>

        <span className="text-gray-500">Fee</span>
        <span className="text-gray-600 text-xs">{t.fee}</span>

        <span className="text-gray-500">Evidence</span>
        <span className="text-gray-600 text-xs">{t.evidence}</span>

        <span className="text-gray-500">Capital impact</span>
        <span className="text-amber-700 text-xs font-mono">{t.capitalImpact}</span>
      </div>

      {awaitingApproval && (
        <button
          onClick={onApprove}
          className="w-full bg-[#e04e2f] hover:bg-[#c9442a] active:bg-[#b33d25] text-white text-sm font-medium py-2.5 rounded-lg transition-colors cursor-pointer"
        >
          Approve Bridge
        </button>
      )}

      {!awaitingApproval && (
        <div className="text-center text-emerald-600 text-sm font-medium py-2">
          Approved
        </div>
      )}
    </div>
  );
}
