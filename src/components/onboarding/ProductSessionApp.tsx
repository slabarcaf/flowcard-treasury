import { useEffect, useState } from 'react';
import { BrowserSessionClient, SessionError } from '../../services/browser-session/client.js';
import type { BrowserSession } from '../../services/browser-session/client.js';
import { BrowserSessionChanged } from '../../services/browser-session/context.js';
import { BrowserLoginForm } from './BrowserLoginForm.js';
import { AuroraBackground } from './AuroraBackground.js';
import { LandingPage } from './LandingPage.js';
import { BrowserGatewayApp } from './BrowserGatewayApp.js';
import { ConnectedBackOffice } from '../../features/bridge-capital/backoffice/ConnectedBackOffice.js';
import { ConnectedPortal } from '../../features/bridge-capital/portal/ConnectedPortal.js';
export function ProductSessionApp() {
  const [relogin, setRelogin] = useState(false);
  const [session, setSession] = useState<BrowserSession | null>();
  const [hash, setHash] = useState(window.location.hash);
  const [error, setError] = useState('');
  const [restoreVersion, setRestoreVersion] = useState(0);
  useEffect(() => {
    const change = () => setHash(window.location.hash);
    window.addEventListener('hashchange', change);
    return () => window.removeEventListener('hashchange', change);
  }, []);
  useEffect(() => {
    let active = true;
    void new BrowserSessionClient()
      .me()
      .then((value) => {
        if (active) setSession(value);
      })
      .catch((e) => {
        if (active) {
          if (e instanceof SessionError && e.status === 401) setSession(null);
          else setError(e.message);
        }
      });
    return () => {
      active = false;
    };
  }, [restoreVersion]);
  function authenticated(value: BrowserSession | null) {
    setRelogin(false);
    setSession(value);
    setError('');
    if (value && (hash === '#login' || hash === '' || hash === '#'))
      window.location.hash = value.user.role === 'OPERATOR' ? '#bridge-capital' : '#portal';
  }
  async function logout() {
    try {
      await new BrowserSessionClient().logout();
      setSession(null);
      window.location.hash = '#login';
    } catch (e) {
      if (e instanceof SessionError && e.status === 401) {
        setSession(null);
        window.location.hash = '#login';
      } else setError(e instanceof Error ? e.message : 'Sign-out unavailable');
    }
  }
  if (hash === '' || hash === '#')
    return (
      <LandingPage
        onGetStarted={() => (window.location.hash = '#onboarding')}
        onLogin={() => (window.location.hash = '#login')}
      />
    );
  const publicError = (
    <p role="alert" style={{ color: '#fbbf24' }}>
      {error}
    </p>
  );
  if (session === undefined)
    return (
      <div style={{ background: '#0D1421', minHeight: '100vh', color: 'white', padding: 40 }}>
        {error ? publicError : <p>Checking your session…</p>}
        <button
          onClick={() => {
            setError('');
            setRestoreVersion((x) => x + 1);
          }}
        >
          Check session again
        </button>
      </div>
    );
  if (!session)
    return (
      <BrowserSessionChanged value={(value) => authenticated(value)}>
        <div className="min-h-screen relative" style={{ backgroundColor: '#0D1421' }}>
          <AuroraBackground />
          <main className="relative z-10 min-h-screen flex items-center justify-center px-5 py-10 text-white">
            <div className="w-full max-w-md">
              <button
                type="button"
                className="revolut-link mb-8"
                onClick={() => (window.location.hash = '')}
              >
                Back
              </button>
              <h1 className="text-2xl font-bold mb-3">FlowCard sign-in</h1>
              <p className="text-sm text-slate-400 mb-7">
                Use your invited username and password to continue.
              </p>
              {error && publicError}
              <BrowserLoginForm />
            </div>
          </main>
        </div>
      </BrowserSessionChanged>
    );
  const operator = session.user.role === 'OPERATOR';
  const denied = operator
    ? hash === '#portal'
    : ['#bridge-capital', '#onboarding', '#developer'].includes(hash);
  if (denied)
    return (
      <div style={{ background: '#0D1421', minHeight: '100vh', color: 'white', padding: 40 }}>
        <h1>This page is for {operator ? 'customers' : 'operators'}</h1>
        <p>Your signed-in role determines which records you can access.</p>
        <button onClick={() => (window.location.hash = operator ? '#bridge-capital' : '#portal')}>
          Go to your workspace
        </button>
        <button onClick={() => void logout()}>Sign out</button>
      </div>
    );
  return (
    <BrowserSessionChanged value={(value) => authenticated(value)}>
      <div key={session.user.id}>
        {import.meta.env.VITE_SANDBOX_HISTORY === 'true' && (
          <div
            role="status"
            style={{ padding: '10px 20px', background: '#fff1cf', color: '#5b4108', fontSize: 13 }}
          >
            Sandbox history review · transfers disabled
          </div>
        )}
        {error && publicError}
        {relogin && (
          <div
            role="dialog"
            aria-label="Sign in again"
            style={{
              position: 'fixed',
              inset: 0,
              zIndex: 100,
              background: '#0d1421dd',
              display: 'grid',
              placeItems: 'center',
              color: 'white',
            }}
          >
            <section style={{ width: 360, padding: 24, background: '#171e2a', borderRadius: 20 }}>
              <h2>Sign in again</h2>
              <BrowserLoginForm />
              <button type="button" onClick={() => setRelogin(false)}>
                Close
              </button>
            </section>
          </div>
        )}
        {hash === '#developer' && operator ? (
          <>
            <div style={{ background: '#0D1421', color: 'white', padding: 16 }}>
              <button type="button" onClick={() => (window.location.hash = '#bridge-capital')}>
                Return to product
              </button>
              <span style={{ marginLeft: 20 }}>
                Developer interface · configured test environment
              </span>
            </div>
            <BrowserGatewayApp operatorOnly onSessionChanged={authenticated} />
          </>
        ) : operator ? (
          <ConnectedBackOffice
            key={hash === '#onboarding' ? 'onboarding' : 'office'}
            startOnboarding={hash === '#onboarding'}
            session={session}
            logout={() => void logout()}
            relogin={() => setRelogin(true)}
          />
        ) : (
          <ConnectedPortal
            session={session}
            logout={() => void logout()}
            relogin={() => setRelogin(true)}
          />
        )}
      </div>
    </BrowserSessionChanged>
  );
}
