import { useState } from 'react';
import type { FormEvent } from 'react';
import { OnboardingApiClient } from '../../services/onboarding/OnboardingApiClient.js';

/** Explicit authorized local testing only. Never auto-issue or embed operator credentials. */
export function LocalSessionForm({ onConnected }: { onConnected: () => void }) {
  const [credential, setCredential] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  async function connect(event: FormEvent) {
    event.preventDefault();
    if (busy) return;
    setBusy(true); setError('');
    try {
      await new OnboardingApiClient().establishOperatorSession(credential);
      setCredential(''); onConnected();
    } catch { setCredential(''); setError('Sign-in failed. Check the local operator credential and try again.'); }
    finally { setBusy(false); }
  }
  return <form onSubmit={connect} className="mt-4 space-y-4 text-white">
    <p className="text-sm text-slate-300">Local integration testing requires a FlowCard operator session. Your form stays on this page. Do not use your Airwallex API key here.</p>
    <label className="block space-y-2 text-sm"><span>Local operator credential</span><input type="password" required autoComplete="off" disabled={busy} value={credential} onChange={event => setCredential(event.target.value)} className="revolut-input w-full" /></label>
    {error && <p role="alert" className="text-red-200 text-sm">{error}</p>}
    <button type="submit" disabled={busy || !credential} className="rounded-lg bg-white px-5 py-3 font-semibold text-slate-950 disabled:opacity-50">{busy ? 'Signing in…' : 'Sign in to retry submission'}</button>
  </form>;
}
