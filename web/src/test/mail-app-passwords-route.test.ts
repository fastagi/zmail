import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const cookieStore = vi.hoisted(() => ({ get: vi.fn(), set: vi.fn() }));
const getSessionUser = vi.hoisted(() => vi.fn());

vi.mock('next/headers', () => ({
  cookies: async () => cookieStore,
  headers: async () => new Headers(),
}));
vi.mock('@/features/auth/server/session', async importOriginal => {
  const original = await importOriginal<typeof import('@/features/auth/server/session')>();
  return { ...original, getSessionUser };
});

const originalEnv = { ...process.env };
const managedKeys = [
  'API_ADAPTER_ENABLED',
  'API_UPSTREAM_MAX_RESPONSE_BYTES',
  'API_UPSTREAM_TIMEOUT_MS',
  'API_UPSTREAM_URL',
  'MOCK_BFF_ENABLED',
  'NEXT_PUBLIC_API_URL',
  'NEXT_PUBLIC_APP_URL',
  'NEXT_PUBLIC_OPTIONAL_FEATURES',
] as const;

type MailboxRoute = typeof import('@/features/mailbox/server/mailbox-route');

describe('mail client password browser routes', () => {
  beforeEach(() => {
    vi.resetModules();
    vi.stubGlobal('fetch', vi.fn<typeof fetch>());
    getSessionUser.mockReset().mockResolvedValue({
      id: 'demo-admin',
      email: 'admin@example.com',
      name: 'Admin User',
    });
    process.env.NEXT_PUBLIC_API_URL = '/api';
    process.env.NEXT_PUBLIC_APP_URL = 'https://app.example.com';
    process.env.NEXT_PUBLIC_OPTIONAL_FEATURES = 'organization,mailbox';
    process.env.API_UPSTREAM_TIMEOUT_MS = '5000';
    process.env.API_UPSTREAM_MAX_RESPONSE_BYTES = '1048576';
    process.env.MOCK_BFF_ENABLED = 'true';
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.resetModules();
    for (const key of managedKeys) {
      delete process.env[key];
      if (originalEnv[key] !== undefined) process.env[key] = originalEnv[key];
    }
  });

  it('accepts the smtp scope and refuses repeated or unknown scopes', async () => {
    const route = await loadRoute();
    for (const scopes of [['imap', 'smtp'], ['smtp']]) {
      const created = await route.createMailAppPasswordRoute(
        request('/api/mail-app-passwords', {
          method: 'POST',
          body: JSON.stringify({ name: 'Phone', scopes, current_password: 'admin123' }),
        })
      );
      expect(created.status).toBe(201);
      expect((await created.json()).data).toMatchObject({ scopes });
    }
    for (const scopes of [['smtp', 'smtp'], ['pop3']]) {
      const refused = await route.createMailAppPasswordRoute(
        request('/api/mail-app-passwords', {
          method: 'POST',
          body: JSON.stringify({ name: 'Phone', scopes, current_password: 'admin123' }),
        })
      );
      expect(refused.status).toBe(422);
      expect((await refused.json()).errors).toMatchObject({ scopes: [expect.any(String)] });
    }
  });

  it('returns the secret once, lists without it, and revokes', async () => {
    const route = await loadRoute();
    const created = await route.createMailAppPasswordRoute(
      request('/api/mail-app-passwords', {
        method: 'POST',
        body: JSON.stringify({ name: ' Phone ', scopes: ['imap'], current_password: 'admin123' }),
      })
    );
    expect(created.status).toBe(201);
    expect(created.headers.get('cache-control')).toContain('no-store');
    const secret = (await created.json()).data;
    expect(secret).toMatchObject({ name: 'Phone', scopes: ['imap'] });
    expect(secret.secret).toMatch(/^[a-z]{4}( [a-z]{4}){3}$/u);

    const listed = await route.listMailAppPasswordsRoute(request('/api/mail-app-passwords'));
    const text = await listed.text();
    expect(text).not.toContain('"secret"');
    expect(text).not.toContain(secret.secret);
    expect(JSON.parse(text).data).toMatchObject({
      limit: 20,
      access: { enabled: true, reason: null },
      items: [{ id: secret.id, hint: secret.secret.replaceAll(' ', '').slice(12) }],
    });

    const revoked = await route.revokeMailAppPasswordRoute(
      request(`/api/mail-app-passwords/${secret.id}`, { method: 'DELETE' }),
      String(secret.id)
    );
    expect(revoked.status).toBe(204);
    const again = await route.revokeMailAppPasswordRoute(
      request(`/api/mail-app-passwords/${secret.id}`, { method: 'DELETE' }),
      String(secret.id)
    );
    expect(again.status).toBe(404);
  });

  it('refuses a wrong password as a field error, then rate limits', async () => {
    const route = await loadRoute();
    const attempt = () =>
      route.createMailAppPasswordRoute(
        request('/api/mail-app-passwords', {
          method: 'POST',
          body: JSON.stringify({ name: 'Phone', scopes: ['imap'], current_password: 'wrong' }),
        })
      );
    for (let index = 0; index < 5; index++) {
      const response = await attempt();
      expect(response.status).toBe(422);
      expect((await response.json()).errors.current_password).toBeDefined();
    }
    const limited = await attempt();
    expect(limited.status).toBe(429);
    expect(limited.headers.get('retry-after')).toMatch(/^\d+$/u);
  });

  it('lets an owner turn mail app access off, which stops creating', async () => {
    const route = await loadRoute();
    const off = await route.updateMailClientSettingsRoute(
      request('/api/mail-client-settings', {
        method: 'PUT',
        body: JSON.stringify({ access_enabled: false }),
      })
    );
    expect(off.status).toBe(200);
    expect((await off.json()).data.access_enabled).toBe(false);
    const created = await route.createMailAppPasswordRoute(
      request('/api/mail-app-passwords', {
        method: 'POST',
        body: JSON.stringify({ name: 'Phone', scopes: ['imap'], current_password: 'admin123' }),
      })
    );
    expect(created.status).toBe(403);
    expect((await created.json()).error_code).toBe('MAILBOX.MAIL_CLIENT_ACCESS_DISABLED');
    const listed = await route.listMailAppPasswordsRoute(request('/api/mail-app-passwords'));
    expect((await listed.json()).data.access).toEqual({
      enabled: false,
      reason: 'organization_disabled',
    });
  });
});

async function loadRoute(): Promise<MailboxRoute> {
  const route = await import('@/features/mailbox/server/mailbox-route');
  const store = await import('@/features/mailbox/server/mock-mail-app-password-store');
  store.mockMailAppPasswordStore.reset();
  return route;
}

function request(path: string, options: { method?: string; body?: string } = {}) {
  const result = new Request(`https://app.example.com${path}`, {
    method: options.method ?? 'GET',
    body: options.body,
  });
  result.headers.set('organization-id', '1');
  if (options.body) result.headers.set('content-type', 'application/json');
  if (options.method && options.method !== 'GET') {
    result.headers.set('origin', 'https://app.example.com');
    result.headers.set('sec-fetch-site', 'same-origin');
  }
  return result;
}
