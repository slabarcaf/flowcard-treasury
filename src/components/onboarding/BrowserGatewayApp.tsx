import { useEffect, useState } from 'react';
import { BrowserSessionClient, SessionError } from '../../services/browser-session/client.js';
import type { BrowserSession } from '../../services/browser-session/client.js';
import { BrowserLoginForm } from './BrowserLoginForm.js';
import { BrowserAccess } from './BrowserAccess.js';
import { FinancialRecordsScreen } from '../../features/bridge-capital/backoffice/screens/FinancialRecordsScreen.js';
import { CustomerFinancialWorkspace } from '../../features/bridge-capital/portal/screens/CustomerFinancialWorkspace.js';
import { BrowserSessionChanged } from '../../services/browser-session/context.js';
import { OnboardingWizard } from './OnboardingWizard.js';
import { ApplicationsScreen } from '../../features/bridge-capital/backoffice/screens/ApplicationsScreen.js';
import { ApplicationDetailScreen } from '../../features/bridge-capital/backoffice/screens/ApplicationDetailScreen.js';
import type { View } from '../../features/bridge-capital/backoffice/useBackOffice.js';

const initialView = (): View => ({ screen: 'applications', listState: { previousCursors: [] } });

export function BrowserGatewayApp({operatorOnly=false,onSessionChanged}:{operatorOnly?:boolean;onSessionChanged?:(session:BrowserSession|null)=>void} = {}) {
  const [session, setSession] = useState<BrowserSession | null>();
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(true);
  const [relogin, setRelogin] = useState(false);
  const [draft, setDraft] = useState(window.location.hash === '#onboarding');
  const [financial, setFinancial] = useState(['#bridge-capital', '#financial-records'].includes(window.location.hash));
  const [view, setView] = useState<View>(initialView);
  async function restore() {
    setError(''); setBusy(true);
    try { setSession(await new BrowserSessionClient().me()); }
    catch (caught) {
      if (caught instanceof SessionError && caught.status === 401) setSession(null);
      else setError('Sign-in service is unavailable. Try checking your session again.');
    } finally { setBusy(false); }
  }
  useEffect(() => {
    let active = true;
    void new BrowserSessionClient().me().then(value => { if (active) setSession(value); }).catch(caught => {
      if (!active) return;
      if (caught instanceof SessionError && caught.status === 401) setSession(null);
      else setError('Sign-in service is unavailable. Try checking your session again.');
    }).finally(() => { if (active) setBusy(false); });
    return () => { active = false; };
  }, []);
  useEffect(() => {
    const navigate = () => { setDraft(window.location.hash === '#onboarding'); setFinancial(['#bridge-capital', '#financial-records'].includes(window.location.hash)); setView(initialView()); };
    window.addEventListener('hashchange', navigate);
    return () => window.removeEventListener('hashchange', navigate);
  }, []);
  async function logout() {
    setBusy(true); setError('');
    try { await new BrowserSessionClient().logout(); setSession(null); onSessionChanged?.(null); setView(initialView()); setDraft(false); setFinancial(false); }
    catch (caught) {
      if (caught instanceof SessionError && caught.status === 401) { setSession(null); setView(initialView()); setDraft(false); setFinancial(false); }
      else setError('Sign-out could not be confirmed. Your session remains displayed. Try again when the service is available.');
    } finally { setBusy(false); }
  }
  function go(next: View) {
    if (next.screen === 'applications' || next.screen === 'application') setView(next);
  }
  return <BrowserSessionChanged value={value => {
    if (!session || session.user.id !== value.user.id) {
      setDraft(window.location.hash === '#onboarding');
      setFinancial(['#bridge-capital', '#financial-records'].includes(window.location.hash));
      setView(initialView());
    }
    setSession(value); onSessionChanged?.(value); setRelogin(false);
  }}>
    <div className="min-h-screen bg-[#0D1421] text-white">
      <header className="flex flex-wrap justify-between gap-4 p-5 border-b border-slate-700">
        <p>FlowCard · Go workspace</p>
        {session && <div className="flex gap-4"><span>{session.user.username} · {session.user.role}</span><button type="button" onClick={() => setRelogin(value => !value)} disabled={busy}>Sign in again</button><button type="button" onClick={() => void logout()} disabled={busy}>Sign out</button></div>}
      </header>
      {error && <p role="alert" className="p-5 text-amber-200">{error}</p>}
      {session && relogin && <div className="max-w-md mx-auto p-5"><p className="mb-4">Sign in to restore your session. Your draft remains on this page.</p><BrowserLoginForm /></div>}
      {session === undefined ? <main className="max-w-md mx-auto p-8"><p>{busy ? 'Checking session…' : 'Session not available.'}</p><button type="button" disabled={busy} onClick={() => void restore()}>Check session</button></main>
        : <BrowserAccess session={session} customer={operatorOnly?<main className="p-8">This developer interface requires an operator session.</main>:<CustomerFinancialWorkspace key={session?.user.id} />}><section key={session?.user.id}>
          <nav className="flex gap-5 p-5"><button type="button" onClick={() => { setFinancial(false); setDraft(false); setView(initialView()); }}>Applications</button><button type="button" onClick={() => { setFinancial(false); setDraft(true); }}>New company draft</button><button type="button" onClick={() => { setDraft(false); setFinancial(true); }}>Financial records</button></nav>
          <p className="px-5 text-sm text-slate-400">Onboarding and saved financial records. Financial commands require verified bindings. Saving a draft does not submit it to the provider.</p>
          {financial ? <FinancialRecordsScreen /> : draft ? <OnboardingWizard onComplete={() => { setDraft(false); setView(initialView()); }} />
            : view.screen === 'application' ? <ApplicationDetailScreen applicationId={view.applicationId} listState={view.listState} go={go} />
            : <ApplicationsScreen listState={view.screen === 'applications' ? view.listState : { previousCursors: [] }} go={go} />}
        </section></BrowserAccess>}
    </div>
  </BrowserSessionChanged>;
}
