import type { ReactNode } from 'react';
import type { BrowserSession } from '../../services/browser-session/client.js';
import { operatorSession } from '../../services/browser-session/client.js';
import { BrowserLoginForm } from './BrowserLoginForm.js';

/** The same role boundary wraps every route, including direct hash navigation. */
export function BrowserAccess({ session, children, customer }: { session: BrowserSession | null; children: ReactNode; customer?: ReactNode }) {
  if (session === null) return <main className="max-w-md mx-auto p-8 space-y-5"><h1 className="text-2xl">FlowCard sign-in</h1><p>Use your invited username and password.</p><BrowserLoginForm /></main>;
  if (!operatorSession(session)) return customer ?? <main className="max-w-xl mx-auto p-8 space-y-4"><h1 className="text-2xl">Account not linked</h1><p>Your customer login is active. A servicing account has not been linked to this user. Financial and operator onboarding access is unavailable.</p></main>;
  return children;
}
