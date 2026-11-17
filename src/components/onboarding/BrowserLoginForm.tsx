import { useContext, useState } from 'react';
import type { FormEvent } from 'react';
import { BrowserSessionClient, SessionError } from '../../services/browser-session/client.js';
import { BrowserSessionChanged } from '../../services/browser-session/context.js';

export function BrowserLoginForm({ onConnected }: { onConnected?: () => void }) {
  const authenticated = useContext(BrowserSessionChanged);
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  async function submit(event: FormEvent) {
    event.preventDefault();
    if (busy) return;
    setBusy(true); setError('');
    try {
      const session = await new BrowserSessionClient().login(username, password);
      authenticated(session);
      if (session.user.role === 'OPERATOR') onConnected?.();
    } catch (caught) { setError(caught instanceof SessionError ? caught.message : 'Sign-in failed. Please try again.'); }
    finally { setPassword(''); setBusy(false); }
  }
  return <form onSubmit={submit} className="space-y-4 text-white">
    <label className="block space-y-2 text-sm"><span>Username</span><input name="username" autoComplete="username" required value={username} onChange={event => setUsername(event.target.value)} disabled={busy} className="revolut-input w-full" /></label>
    <label className="block space-y-2 text-sm"><span>Password</span><input name="password" type="password" autoComplete="current-password" required value={password} onChange={event => setPassword(event.target.value)} disabled={busy} className="revolut-input w-full" /></label>
    {error && <p role="alert" className="text-red-200 text-sm">{error}</p>}
    <button type="submit" disabled={busy || !username || !password} className="revolut-btn-primary w-full disabled:opacity-40">{busy ? 'Signing in…' : 'Sign in'}</button>
  </form>;
}
