import { useState } from 'react';
import type { FormEvent } from 'react';
import { AuroraBackground } from './AuroraBackground.js';
import { OnboardingApiClient } from '../../services/onboarding/OnboardingApiClient.js';

export function LoginPage({ onLogin, onBack }: { onLogin: () => void; onBack: () => void }) {
  const [credential, setCredential] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  async function submit(event: FormEvent) {
    event.preventDefault();
    if (busy) return;
    setBusy(true); setError('');
    try {
      await new OnboardingApiClient().establishOperatorSession(credential);
      setCredential(''); onLogin();
    } catch { setCredential(''); setError('Sign-in failed. Check your operator credential and the backend connection.'); }
    finally { setBusy(false); }
  }
  return <div className="min-h-screen relative" style={{ backgroundColor: '#0D1421' }}>
    <AuroraBackground />
    <main className="relative z-10 min-h-screen flex items-center justify-center px-5 py-10 text-white">
      <div className="w-full max-w-md">
        <button type="button" onClick={onBack} className="revolut-link mb-8">Back</button>
        <h1 className="text-2xl font-bold mb-3">FlowCard team sign-in</h1>
        <p className="text-sm text-slate-400 mb-7">Use your authorized operator credential to access the treasury dashboard. This is not your Airwallex API key.</p>
        <form onSubmit={submit} className="space-y-5">
          <label className="block text-sm space-y-2"><span>Operator credential</span><input type="password" required autoComplete="off" value={credential} onChange={event => setCredential(event.target.value)} disabled={busy} className="revolut-input w-full" /></label>
          {error && <p role="alert" className="text-sm text-red-200">{error}</p>}
          <button type="submit" disabled={busy || !credential} className="revolut-btn-primary w-full disabled:opacity-40">{busy ? 'Signing in…' : 'Sign in'}</button>
        </form>
      </div>
    </main>
  </div>;
}
