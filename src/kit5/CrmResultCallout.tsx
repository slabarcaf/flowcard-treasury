import type { SignalRating } from '../policy/types.js';
import type { CrmChange } from './types.js';

const RATING_CLS: Record<SignalRating, string> = {
  green: 'text-emerald-700',
  yellow: 'text-amber-700',
  red: 'text-red-700',
};

const DOOR_LABEL = { auto: 'Auto', human: 'Human', closed: 'Closed' } as const;

function fmt(n: number): string {
  return '$' + n.toLocaleString('en-US');
}

function Row({ label, from, to, worse }: { label: string; from: string; to: string; worse?: boolean }) {
  return (
    <div className="contents">
      <span className="text-gray-500">{label}</span>
      <span className="font-mono">
        <span className="text-gray-500">{from}</span>
        <span className="text-gray-400"> → </span>
        <span className={worse ? 'text-red-700 font-semibold' : 'text-gray-900 font-semibold'}>{to}</span>
      </span>
    </div>
  );
}

/** Shown after a CRM re-evaluation: what the Salesforce note changed, before → after. */
export function CrmResultCallout({ change, onContinue }: { change: CrmChange; onContinue: () => void }) {
  const declined = change.status === 'Declined' || change.status === 'Ineligible';
  return (
    <div className={`rounded-xl border px-5 py-4 animate-panel-in ${declined ? 'bg-rose-50 border-rose-200' : 'bg-amber-50 border-amber-200'}`} role="status">
      <div className="flex items-start justify-between gap-4">
        <div className="min-w-0">
          <div className={`text-sm font-semibold ${declined ? 'text-rose-800' : 'text-amber-800'}`}>
            Salesforce note changed {change.customerName}: {change.status.toLowerCase()}
          </div>
          <div className="text-xs text-gray-600 mt-0.5">
            Without the note, this request would have been {change.outcomeBefore}.
          </div>
        </div>
        <button
          onClick={onContinue}
          className="shrink-0 px-4 py-2 text-sm font-medium bg-gray-900 hover:bg-gray-800 active:bg-black text-white rounded-lg transition-colors cursor-pointer"
        >
          Continue
        </button>
      </div>

      <div className="grid grid-cols-1 md:grid-cols-2 gap-4 mt-3">
        <div className="bg-white/70 rounded-lg border border-white px-4 py-3">
          <div className="text-[11px] font-semibold text-gray-500 uppercase tracking-wider mb-2">What changed</div>
          <div className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1.5 text-xs">
            {change.signals.map((s) => (
              <div key={s.label} className="contents">
                <span className="text-gray-500">{s.label}</span>
                <span className="font-mono">
                  <span className={RATING_CLS[s.from]}>{s.from}</span>
                  <span className="text-gray-400"> → </span>
                  <span className={`${RATING_CLS[s.to]} font-semibold`}>{s.to}</span>
                </span>
              </div>
            ))}
            <Row label="Tier" from={change.tierFrom} to={change.tierTo} worse={change.tierTo !== change.tierFrom} />
            <Row label="Credit line" from={fmt(change.lineFrom)} to={fmt(change.lineTo)} worse={change.lineTo < change.lineFrom} />
            <Row
              label={`Door at ${change.bandLabel}`}
              from={DOOR_LABEL[change.doorFrom]}
              to={DOOR_LABEL[change.doorTo]}
              worse={change.doorTo === 'closed'}
            />
          </div>
        </div>

        <div className="bg-white/70 rounded-lg border border-white px-4 py-3">
          <div className="text-[11px] font-semibold text-gray-500 uppercase tracking-wider mb-2">Why the decision is {change.status.toLowerCase()}</div>
          {change.reasons.length > 0 ? (
            <ul className="text-xs text-gray-800 space-y-1.5 list-disc pl-4">
              {change.reasons.map((r) => <li key={r}>{r}</li>)}
            </ul>
          ) : (
            <p className="text-xs text-gray-800">The request still passes every check.</p>
          )}
        </div>
      </div>
    </div>
  );
}
