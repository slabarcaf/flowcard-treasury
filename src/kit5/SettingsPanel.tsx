import { useState } from 'react';
import type { SignalRating, Signals } from '../policy/types.js';
import type { ScenarioInputs } from './types.js';
import { DEFAULT_TERM_DAYS } from './engine.js';

interface Props {
  inputs: ScenarioInputs;
  defaults: ScenarioInputs;
  onApply: (next: ScenarioInputs) => void;
  onClose: () => void;
}

const SIGNALS: { key: keyof Signals; label: string }[] = [
  { key: 'inflowReliability', label: 'Reliability' },
  { key: 'inflowConcentration', label: 'Concentration' },
  { key: 'cashBuffer', label: 'Cash buffer' },
  { key: 'trackRecord', label: 'Track record' },
];

const RATINGS: SignalRating[] = ['green', 'yellow', 'red'];

const inputCls = 'w-full text-sm font-mono text-gray-900 bg-white border border-gray-300 rounded-md px-2 py-1';

function NumberField({ label, value, onChange, suffix }: {
  label: string;
  value: number;
  onChange: (v: number) => void;
  suffix?: string;
}) {
  return (
    <label className="block">
      <span className="text-[11px] text-gray-500">{label}{suffix ? ` (${suffix})` : ''}</span>
      <input
        type="number"
        className={inputCls}
        value={Number.isFinite(value) ? value : ''}
        onChange={(e) => onChange(e.target.value === '' ? 0 : Number(e.target.value))}
      />
    </label>
  );
}

/** Rounds away floating-point noise from percentage conversions (0.2 * 100 = 20.000000000000004). */
function pct(fraction: number): number {
  return Math.round(fraction * 10_000) / 100;
}

/**
 * Every input the engine uses. Closed by default; the backend will fill these
 * values later, and the panel stays as a way to try "what if" variations.
 */
export function SettingsPanel({ inputs, defaults, onApply, onClose }: Props) {
  const [draft, setDraft] = useState<ScenarioInputs>(() => structuredClone(inputs));
  const f = draft.facility;

  const update = (fn: (d: ScenarioInputs) => void) => {
    setDraft((d) => {
      const next = structuredClone(d);
      fn(next);
      return next;
    });
  };

  const lendable = f.facilitySize * (1 - f.reserveFloorPct);

  return (
    <div className="fixed inset-0 z-50 flex justify-end">
      <div className="absolute inset-0 bg-black/20" onClick={onClose} />
      <aside className="relative w-full max-w-xl h-full bg-white shadow-xl flex flex-col">
        <div className="px-5 py-4 border-b border-gray-200 flex items-center justify-between">
          <div>
            <h2 className="text-sm font-semibold text-gray-900">Scenario inputs</h2>
            <p className="text-xs text-gray-500">These will come from the backend. Applying restarts the scenario.</p>
          </div>
          <button onClick={onClose} className="text-gray-400 hover:text-gray-600 text-xl leading-none cursor-pointer" aria-label="Close">×</button>
        </div>

        <div className="flex-1 overflow-y-auto px-5 py-4 space-y-6">
          <section>
            <h3 className="text-xs font-semibold text-gray-500 uppercase tracking-wider mb-2">Facility</h3>
            <div className="grid grid-cols-2 sm:grid-cols-3 gap-3">
              <NumberField label="Facility size" suffix="$" value={f.facilitySize} onChange={(v) => update((d) => { d.facility.facilitySize = v; })} />
              <NumberField label="Reserve floor" suffix="%" value={pct(f.reserveFloorPct)} onChange={(v) => update((d) => { d.facility.reserveFloorPct = v / 100; })} />
              <NumberField label="Already lent out" suffix="$" value={draft.outstanding} onChange={(v) => update((d) => { d.outstanding = v; })} />
              <NumberField label="Max per customer" suffix="% of facility" value={pct(f.maxPerCustomerPct)} onChange={(v) => update((d) => { d.facility.maxPerCustomerPct = v / 100; })} />
              <NumberField label="Fee per 30 days" suffix="%" value={pct(f.feeRatePer30Days)} onChange={(v) => update((d) => { d.facility.feeRatePer30Days = v / 100; })} />
              <NumberField label="History required" suffix="days" value={f.minHistoryDays} onChange={(v) => update((d) => { d.facility.minHistoryDays = v; })} />
            </div>
            <p className="text-xs text-gray-500 mt-2 font-mono">
              Lendable ${lendable.toLocaleString('en-US')} · Deployable at start ${Math.max(0, lendable - draft.outstanding).toLocaleString('en-US')}
            </p>
          </section>

          <section>
            <h3 className="text-xs font-semibold text-gray-500 uppercase tracking-wider mb-2">Customers</h3>
            <div className="space-y-3">
              {draft.customers.map((c, i) => (
                <div key={c.key} className="border border-gray-200 rounded-lg p-3">
                  <div className="text-sm font-medium text-gray-900 mb-2">{c.name}</div>
                  <div className="grid grid-cols-2 gap-3 mb-2">
                    <NumberField label="Monthly inflow" suffix="$" value={c.monthlyInflow} onChange={(v) => update((d) => { d.customers[i].monthlyInflow = v; })} />
                    <NumberField label="History" suffix="days" value={c.historyDays} onChange={(v) => update((d) => { d.customers[i].historyDays = v; })} />
                  </div>
                  <div className="grid grid-cols-2 sm:grid-cols-4 gap-2">
                    {SIGNALS.map((sig) => (
                      <label key={sig.key} className="block">
                        <span className="text-[11px] text-gray-500">{sig.label}</span>
                        <select
                          className={inputCls}
                          value={c.signals[sig.key]}
                          onChange={(e) => update((d) => { d.customers[i].signals[sig.key] = e.target.value as SignalRating; })}
                        >
                          {RATINGS.map((r) => <option key={r} value={r}>{r}</option>)}
                        </select>
                      </label>
                    ))}
                  </div>
                  {c.crmSignalsAfter && (
                    <p className="text-[11px] text-gray-400 mt-2">Salesforce note re-rating is fixed for this customer.</p>
                  )}
                </div>
              ))}
            </div>
          </section>

          <section>
            <h3 className="text-xs font-semibold text-gray-500 uppercase tracking-wider mb-1">Requests and repayments</h3>
            <p className="text-xs text-gray-500 mb-2">Processed in day + time order. Change a time to change who arrives first.</p>
            <div className="space-y-2">
              {draft.events.map((ev, i) => {
                const name = ev.type === 'decline'
                  ? draft.customers.find((c) => c.key === ev.customerKey)?.name
                  : `Repayment of ${ev.requestId}`;
                return (
                  <div key={`${ev.type}-${ev.requestId}`} className="border border-gray-200 rounded-lg p-3">
                    <div className="text-sm text-gray-900 mb-2">
                      <span className="font-medium">{name}</span>
                      {ev.type === 'decline' && (
                        <span className="text-gray-500"> · shortfall ${Math.max(0, ev.paymentAmount - ev.walletBalance).toLocaleString('en-US')}</span>
                      )}
                    </div>
                    <div className="grid grid-cols-2 sm:grid-cols-5 gap-2">
                      <NumberField label="Day" value={ev.day} onChange={(v) => update((d) => { d.events[i].day = v; })} />
                      <label className="block">
                        <span className="text-[11px] text-gray-500">Time</span>
                        <input
                          className={inputCls}
                          value={ev.time}
                          onChange={(e) => update((d) => { d.events[i].time = e.target.value; })}
                        />
                      </label>
                      {ev.type === 'decline' ? (
                        <>
                          <NumberField label="Payment" suffix="$" value={ev.paymentAmount} onChange={(v) => update((d) => { const e = d.events[i]; if (e.type === 'decline') e.paymentAmount = v; })} />
                          <NumberField label="Wallet" suffix="$" value={ev.walletBalance} onChange={(v) => update((d) => { const e = d.events[i]; if (e.type === 'decline') e.walletBalance = v; })} />
                          <NumberField label="Collect within" suffix="days" value={ev.termDays ?? DEFAULT_TERM_DAYS} onChange={(v) => update((d) => { const e = d.events[i]; if (e.type === 'decline') e.termDays = v; })} />
                        </>
                      ) : (
                        <NumberField label="Deposit" suffix="$" value={ev.depositAmount} onChange={(v) => update((d) => { const e = d.events[i]; if (e.type === 'repayment') e.depositAmount = v; })} />
                      )}
                    </div>
                  </div>
                );
              })}
            </div>
          </section>
        </div>

        <div className="px-5 py-3 border-t border-gray-200 flex items-center justify-between gap-3">
          <button
            onClick={() => setDraft(structuredClone(defaults))}
            className="text-sm text-gray-500 hover:text-gray-700 cursor-pointer"
          >
            Reset to scenario defaults
          </button>
          <div className="flex gap-2">
            <button onClick={onClose} className="px-3 py-2 text-sm text-gray-600 border border-gray-300 rounded-lg cursor-pointer">Cancel</button>
            <button
              onClick={() => onApply(draft)}
              className="px-4 py-2 text-sm font-medium bg-[#e04e2f] hover:bg-[#c9442a] text-white rounded-lg cursor-pointer"
            >
              Apply and restart
            </button>
          </div>
        </div>
      </aside>
    </div>
  );
}
