import type { CustomerDetail as Detail, CollectionRow } from '../state/types.js';
import { UnderwritingPipeline } from './UnderwritingPipeline.js';
import { SalesforceNote } from './SalesforceNote.js';
import { SignalExtraction } from './SignalExtraction.js';
import { PolicyChecks } from './PolicyChecks.js';
import { ApprovalCard } from './ApprovalCard.js';
import { CollectionWaterfall } from './CollectionWaterfall.js';

interface Props {
  detail: Detail;
  onBloomReveal: () => void;
  onBloomReEvaluate: () => void;
  onApprove: (customerId: string) => void;
  showCollection: boolean;
  collectionRows: CollectionRow[];
}

function AgentBanner({ phase, onReveal, onReEvaluate }: {
  phase: Detail['bloomAgentPhase'];
  onReveal: () => void;
  onReEvaluate: () => void;
}) {
  if (!phase || phase === 'done') return null;

  if (phase === 'found') {
    return (
      <div className="flex items-center justify-between bg-amber-50 border border-amber-200 rounded-lg px-4 py-3 animate-panel-in">
        <div className="flex items-center gap-2">
          <span className="relative flex h-2.5 w-2.5">
            <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-amber-400 opacity-75" />
            <span className="relative inline-flex rounded-full h-2.5 w-2.5 bg-amber-500" />
          </span>
          <span className="text-sm text-amber-800 font-medium">Agent found new information from CRM</span>
        </div>
        <button
          onClick={onReveal}
          className="px-3 py-1.5 text-xs font-semibold bg-amber-600 hover:bg-amber-700 active:bg-amber-800 text-white rounded-md transition-colors cursor-pointer"
        >
          Re-evaluate
        </button>
      </div>
    );
  }

  if (phase === 'revealed') {
    return (
      <div className="flex items-center justify-between bg-rose-50 border border-rose-200 rounded-lg px-4 py-3 animate-panel-in">
        <div className="flex items-center gap-2">
          <span className="relative flex h-2.5 w-2.5">
            <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-rose-400 opacity-75" />
            <span className="relative inline-flex rounded-full h-2.5 w-2.5 bg-rose-500" />
          </span>
          <span className="text-sm text-rose-800 font-medium">Agent extracted 3 risk signals — ready to re-evaluate</span>
        </div>
        <button
          onClick={onReEvaluate}
          className="px-3 py-1.5 text-xs font-semibold bg-rose-600 hover:bg-rose-700 active:bg-rose-800 text-white rounded-md transition-colors cursor-pointer"
        >
          Re-evaluate
        </button>
      </div>
    );
  }

  return null;
}

export function CustomerDetailPanel({ detail, onBloomReveal, onBloomReEvaluate, onApprove, showCollection, collectionRows }: Props) {
  return (
    <div className="bg-gray-50 border-t border-gray-200 px-6 py-5 space-y-4 animate-panel-in">
      {/* Underwriting pipeline flowchart */}
      <UnderwritingPipeline precheck={detail.precheck} perRequest={detail.perRequest} />

      {/* Ineligible reason */}
      {detail.ineligibleReason && (
        <div className="text-sm text-red-700 bg-red-50 border border-red-200 rounded-lg px-4 py-3">
          {detail.ineligibleReason}
        </div>
      )}

      {/* Bloom: agent status banner */}
      {detail.bloomAgentPhase && (
        <AgentBanner
          phase={detail.bloomAgentPhase}
          onReveal={onBloomReveal}
          onReEvaluate={onBloomReEvaluate}
        />
      )}

      {/* Bloom: CRM note + signals */}
      {detail.showCrmNote && <SalesforceNote visible />}
      {detail.showSignals && <SignalExtraction visible />}

      {/* Policy checks */}
      {detail.policyChecks.length > 0 && (
        <PolicyChecks visible checks={detail.policyChecks} />
      )}

      {/* Nova: Approval card */}
      {detail.showApproval && (
        <ApprovalCard
          visible
          terms={detail.approvalTerms}
          awaitingApproval={detail.awaitingApproval}
          onApprove={() => onApprove(detail.customerId)}
        />
      )}

      {/* Collection waterfall for funded customers */}
      {detail.collectionRows ? (
        <CollectionWaterfall visible rows={detail.collectionRows} />
      ) : detail.status === 'Funded' && showCollection && (
        <CollectionWaterfall visible={showCollection} rows={collectionRows} />
      )}
    </div>
  );
}
