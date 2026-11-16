import { useEffect, useState } from 'react';
import { FinancialReadClient, cents } from '../../../../services/financial/client.js';
import type { Bridge, Offer } from '../../../../services/financial/client.js';
import { FinancialCommandClient } from '../../../../services/financial/commands.js';
import type { ServicingSession } from '../../../../services/financial/commands.js';
import { CommandRecovery, FinancialActions } from '../../backoffice/screens/FinancialActions.js';

export function CustomerFinancialWorkspace() {
  const [api] = useState(() => new FinancialCommandClient());
  const [reader] = useState(() => new FinancialReadClient());
  const [binding, setBinding] = useState<ServicingSession | null>(null);
  const [ids, setIDs] = useState<string[]>([]);
  const [offer, setOffer] = useState<Offer | null>(null);
  const [bridge, setBridge] = useState<Bridge | null>(null);
  const [bridgeID, setBridgeID] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(true);
  useEffect(() => {
    let active = true;
    void api.session().then(async status => { if (!active) return; setBinding(status); if (status.linked && status.role === 'CUSTOMER') { const offers = await api.offers(); if (active) setIDs(offers); } }).catch(caught => { if (active) setError(caught instanceof Error ? caught.message : 'Servicing is unavailable.'); }).finally(() => { if (active) setBusy(false); });
    return () => { active = false; };
  }, [api]);
  async function open(id: string) {
    setBusy(true); setError(''); setOffer(null);
    try { const value = await reader.offer(id); if (value.source_mode !== 'DEMO' || value.customer_id !== binding?.customer_id) throw new Error('Saved offer does not match this verified DEMO customer.'); setOffer(value); }
    catch (caught) { setError(caught instanceof Error ? caught.message : 'Offer read unavailable.'); } finally { setBusy(false); }
  }
  async function refresh() { if (offer) await open(offer.id); }
  async function readBridge() {
    setBusy(true); setError(''); setBridge(null);
    try { const value = await reader.bridge(bridgeID); if (value.mode !== 'DEMO' || value.customer_id !== binding?.customer_id) throw new Error('Bridge does not match this verified DEMO customer.'); setBridge(value); }
    catch (caught) { setError(caught instanceof Error ? caught.message : 'Bridge read unavailable.'); } finally { setBusy(false); }
  }
  return <main className="max-w-3xl mx-auto p-6 space-y-5">
    {busy && <p role="status">Reading verified servicing records…</p>}
    {error && <p role="alert" className="text-amber-200">{error}</p>}
    {binding && !binding.linked && <><h1 className="text-2xl">Account not linked</h1><p>Your customer login is active. A servicing account has not been linked to this user. Financial and operator onboarding access is unavailable.</p></>}
    {binding?.linked && binding.role === 'CUSTOMER' && <><h1 className="text-2xl">Your DEMO financing</h1><p className="text-amber-200">Synthetic local book · Verified customer {binding.customer_id}</p>
      <p className="text-sm text-slate-400">Latest 50 offer IDs published for your customer by verified create receipts. Current terms come from the owning Go service.</p>
      {ids.length === 0 && <p>No indexed offers for this customer.</p>}
      <div className="flex flex-wrap gap-3">{ids.map(id => <button key={id} type="button" className="rounded border border-slate-600 p-2 text-xs" disabled={busy} onClick={() => void open(id)}>Read offer {id}</button>)}</div>
      {offer && <section key={`${offer.id}:${offer.version}`} className="rounded border border-slate-700 p-4 space-y-3"><h2 className="text-xl">Saved offer · {offer.state}</h2><p>Principal {cents(offer.principal_cents)} · Contract fee {cents(offer.contract_fee_cents)} · Total due {cents(offer.total_due_cents)}</p><p>Due {offer.due_on} · Term {offer.term_days} days · Version {offer.version} · Revision {offer.offer_revision}</p><FinancialActions record={{ kind: 'offer', value: offer }} refresh={() => void refresh()} /></section>}
      <section className="rounded border border-slate-700 p-4 space-y-3"><h2 className="text-xl">Read your saved bridge</h2><label className="block text-sm">Bridge UUID<input className="block w-full bg-slate-900 border border-slate-600 p-2" value={bridgeID} onChange={event => setBridgeID(event.target.value)} /></label><button type="button" className="rounded border border-slate-600 p-2" disabled={busy || !bridgeID} onClick={() => void readBridge()}>Read current bridge status</button>
        {bridge && <><p>{bridge.lifecycle} · Version {bridge.version} · Response as of {bridge.as_of}</p><p>Principal owed {cents(bridge.principal_owed_cents)} · Contract fee owed {cents(bridge.contract_fee_owed_cents)} · Late fee owed {cents(bridge.late_fee_owed_cents)}</p><p>Debt as of {bridge.debt_as_of ?? 'Unavailable'} · Accrual current {String(bridge.accrual_current ?? 'Unavailable')}</p></>}
      </section><CommandRecovery />
    </>}
  </main>;
}
