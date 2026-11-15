import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it, vi } from 'vitest';
import { BrowserSessionClient, SessionError } from '../../src/services/browser-session/client.js';
import type { BrowserSession } from '../../src/services/browser-session/client.js';
import { BrowserAccess } from '../../src/components/onboarding/BrowserAccess.js';

const session: BrowserSession = { user: { id: 'owned-user-uuid', username: 'operator.one', role: 'OPERATOR' }, expires_at: '2026-11-16T20:00:00Z' };
const json = (value: unknown, status = 200) => new Response(JSON.stringify(value), { status, headers: { 'content-type': 'application/json' } });

describe('Go browser authentication boundary', () => {
  it('uses real username/password and cookie credentials for login, restore and logout without browser-supplied identity', async () => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValueOnce(json(session)).mockResolvedValueOnce(json(session)).mockResolvedValueOnce(new Response(null, { status: 204 }));
    const client = new BrowserSessionClient(fetcher);
    expect(await client.login('operator.one', '  synthetic password preserved  ')).toEqual(session);
    expect(await client.me()).toEqual(session);
    await client.logout();
    expect(fetcher.mock.calls.map(call => call[0])).toEqual(['/api/session/login', '/api/session/me', '/api/session/logout']);
    expect(JSON.parse(String(fetcher.mock.calls[0][1]?.body))).toEqual({ username: 'operator.one', password: '  synthetic password preserved  ' });
    for (const [, options] of fetcher.mock.calls) {
      expect(options?.credentials).toBe('include');
      expect(options?.cache).toBe('no-store');
      expect(options?.headers).not.toHaveProperty('Authorization');
    }
    expect(fetcher.mock.calls[2][1]?.body).toBeUndefined();
  });

  it('distinguishes expired credentials from unavailability without retrying uncertain requests', async () => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValueOnce(json({ error: 'unauthorized' }, 401)).mockResolvedValueOnce(json({ error: 'unavailable' }, 503)).mockRejectedValueOnce(new Error('private connection details'));
    const client = new BrowserSessionClient(fetcher);
    await expect(client.me()).rejects.toMatchObject({ status: 401 });
    await expect(client.logout()).rejects.toMatchObject({ status: 503 });
    await expect(client.login('operator.one', 'synthetic password')).rejects.toMatchObject({ status: 503 });
    expect(fetcher).toHaveBeenCalledTimes(3);
    expect(new SessionError(401).message).not.toEqual(new SessionError(503).message);
    expect(new SessionError(503).message).not.toContain('private');
  });

  it('refuses malformed restored identities instead of granting operator access', async () => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(json({ ...session, user: { ...session.user, role: 'ADMIN' } }));
    await expect(new BrowserSessionClient(fetcher).me()).rejects.toMatchObject({ status: 503 });
  });

  it('keeps operator route content unrendered for anonymous and unlinked customer sessions', () => {
    let protectedRenders = 0;
    function Protected() { protectedRenders++; return createElement('p', null, 'Operator company draft'); }
    for (const identity of [null, { ...session, user: { ...session.user, role: 'CUSTOMER' as const } }]) {
      const html = renderToStaticMarkup(<BrowserAccess session={identity}><Protected /></BrowserAccess>);
      expect(html).not.toContain('Operator company draft');
      expect(html).not.toContain('Local operator credential');
      if (identity) expect(html).toContain('Account not linked');
      else {
        expect(html).toContain('autoComplete="username"');
        expect(html).toContain('autoComplete="current-password"');
      }
    }
    expect(protectedRenders).toBe(0);
    expect(renderToStaticMarkup(<BrowserAccess session={session}><Protected /></BrowserAccess>)).toContain('Operator company draft');
    expect(protectedRenders).toBe(1);
  });
});
