export const browserGatewayEnabled = import.meta.env.VITE_BROWSER_GATEWAY_ENABLED === 'true';

export interface BrowserSession {
  user: { id: string; username: string; role: 'OPERATOR' | 'CUSTOMER' };
  expires_at: string;
}

export class SessionError extends Error {
  readonly status: number;
  constructor(status: number) {
    super(status === 401 ? 'Sign-in failed or your session has expired. Check your username and password.'
      : status === 429 ? 'Too many sign-in attempts. Wait a minute before trying again.'
      : 'Sign-in service is unavailable. Your entries remain on this page. Try again when it is available.');
    this.status = status;
  }
}

export class BrowserSessionClient {
  private readonly fetchImpl: typeof fetch;
  constructor(fetchImpl: typeof fetch = globalThis.fetch.bind(globalThis)) { this.fetchImpl = fetchImpl; }

  private async request(method: string, path: string, credentials?: { username: string; password: string }): Promise<BrowserSession | null> {
    let response: Response;
    try {
      response = await this.fetchImpl(`/api/session/${path}`, {
        method, credentials: 'include', cache: 'no-store', signal: AbortSignal.timeout(10_000),
        headers: { accept: 'application/json', ...(credentials ? { 'content-type': 'application/json' } : {}) },
        ...(credentials ? { body: JSON.stringify(credentials) } : {}),
      });
    } catch { throw new SessionError(503); }
    if (!response.ok) throw new SessionError(response.status);
    if (response.status === 204) return null;
    let body: BrowserSession;
    try { body = await response.json(); } catch { throw new SessionError(503); }
    if (!body?.user || typeof body.user.id !== 'string' || typeof body.user.username !== 'string' ||
        !['OPERATOR', 'CUSTOMER'].includes(body.user.role) || !Number.isFinite(Date.parse(body.expires_at))) throw new SessionError(503);
    return body;
  }
  async login(username: string, password: string): Promise<BrowserSession> {
    const session = await this.request('POST', 'login', { username, password });
    if (!session) throw new SessionError(503);
    return session;
  }
  me(): Promise<BrowserSession | null> { return this.request('GET', 'me'); }
  async logout(): Promise<void> { await this.request('POST', 'logout'); }
}

/** UI routing only. All actual permission checks remain on the gateway. */
export function operatorSession(session: BrowserSession | null | undefined): boolean {
  return session?.user.role === 'OPERATOR';
}
