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
type ExternalStore = typeof import('@/features/mailbox/server/mock-external-mail-store');

let activeOrganization = '1';

/* The seeded organization 1: demo-admin (user 1) owns it, demo-member (user 3) is a member. */
describe('external mail browser routes', () => {
  let fetchMock: ReturnType<typeof vi.fn<typeof fetch>>;

  beforeEach(() => {
    vi.resetModules();
    fetchMock = vi.fn<typeof fetch>();
    vi.stubGlobal('fetch', fetchMock);
    cookieStore.get.mockReset();
    cookieStore.set.mockReset();
    getSessionUser.mockReset();
    process.env.NEXT_PUBLIC_API_URL = '/api';
    process.env.NEXT_PUBLIC_APP_URL = 'https://app.example.com';
    process.env.NEXT_PUBLIC_OPTIONAL_FEATURES = 'organization,mailbox';
    process.env.API_UPSTREAM_TIMEOUT_MS = '5000';
    process.env.API_UPSTREAM_MAX_RESPONSE_BYTES = '1048576';
    activeOrganization = '1';
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.resetModules();
    for (const key of managedKeys) {
      delete process.env[key];
      if (originalEnv[key] !== undefined) process.env[key] = originalEnv[key];
    }
  });

  it('mock connect flow: connectors, start, 201 then 200, private mailbox, revoke', async () => {
    process.env.MOCK_BFF_ENABLED = 'true';
    getSessionUser.mockResolvedValue(mockUser('demo-member'));
    const { route, store } = await load();

    const connectors = await route.listExternalMailConnectorsRoute(
      request('/api/external-mail-connectors')
    );
    expect(connectors.status).toBe(200);
    expect(connectors.headers.get('cache-control')).toBe('private, no-store');
    expect((await connectors.json()).data.map((item: { id: string }) => item.id)).toEqual([
      'gmail',
      'outlook',
    ]);

    const started = await start(route, { provider: 'gmail', backfill_days: 30 });
    expect(started.status).toBe(201);
    const { authorization_url: url, expires_at: expiresAt } = (await started.json()).data;
    const target = new URL(url);
    expect(target.origin).toBe('https://app.example.com');
    expect(target.pathname).toBe('/console/mailboxes/connected/callback');
    expect(Date.parse(expiresAt)).toBeGreaterThan(Date.now());
    const state = target.searchParams.get('state') ?? '';
    const code = target.searchParams.get('code') ?? '';

    const created = await complete(route, { state, code });
    expect(created.status).toBe(201);
    const connection = (await created.json()).data;
    expect(connection).toMatchObject({
      provider: 'gmail',
      account_address: 'member@gmail.com',
      status: 'active',
      last_error_code: null,
    });

    // The state is single use.
    await expectError(
      await complete(route, { state, code }),
      404,
      'MAILBOX.EXTERNAL_AUTHORIZATION_NOT_FOUND'
    );

    // The external mailbox is listed for its owner, read-only.
    const mailboxes = await route.listMailboxesRoute(request('/api/mailboxes'));
    const external = (await mailboxes.json()).data.find(
      (item: { id: number }) => item.id === connection.mailbox_id
    );
    expect(external).toMatchObject({ type: 'personal', origin: 'connected', owner_user_id: 3 });
    expect(external.access).toMatchObject({ view: true, send: false, organize: false });

    // Synced copies carry the connector as `source` in every listing, so the source badge shows;
    // Zmail's own mail says `zmail`.
    const copies = await route.listMailMessagesRoute(
      request(`/api/mailboxes/${connection.mailbox_id}/messages`),
      String(connection.mailbox_id)
    );
    const copyItems: { source?: string }[] = (await copies.json()).data;
    expect(copyItems.length).toBeGreaterThan(0);
    expect(copyItems.map(item => item.source)).toEqual(copyItems.map(() => 'gmail'));
    const unified = await route.listInboxMessagesRoute(request('/api/inbox-messages?view=all'));
    const unifiedItems: { mailbox_id: number; source?: string }[] = (await unified.json()).data;
    const synced = unifiedItems.filter(item => item.mailbox_id === connection.mailbox_id);
    expect(synced.length).toBeGreaterThan(0);
    for (const item of synced) expect(item.source).toBe('gmail');
    for (const item of unifiedItems.filter(row => row.mailbox_id !== connection.mailbox_id)) {
      expect(item.source).toBe('zmail');
    }

    // Reconnecting a connection that needs it restores the same one (200).
    store.mockExternalMailStore.markNeedsReconnect(connection.id);
    const listed = await route.listExternalMailConnectionsRoute(
      request('/api/external-mail-connections')
    );
    expect((await listed.json()).data[0]).toMatchObject({
      id: connection.id,
      status: 'needs_reconnect',
      last_error_code: 'external.auth_failed',
    });
    const again = new URL(
      (await (await start(route, { connection_id: connection.id })).json()).data.authorization_url
    );
    const restored = await complete(route, {
      state: again.searchParams.get('state') ?? '',
      code: again.searchParams.get('code') ?? '',
    });
    expect(restored.status).toBe(200);
    expect((await restored.json()).data).toMatchObject({
      id: connection.id,
      mailbox_id: connection.mailbox_id,
      status: 'active',
    });
    // Revoked and bound again: the same mailbox, and no second copy of the synced mail.
    await route.revokeExternalMailConnectionRoute(
      write('DELETE', `/api/external-mail-connections/${connection.id}`),
      String(connection.id)
    );
    const rebind = new URL(
      (await (await start(route, { provider: 'gmail' })).json()).data.authorization_url
    );
    const rebound = await complete(route, {
      state: rebind.searchParams.get('state') ?? '',
      code: rebind.searchParams.get('code') ?? '',
    });
    expect(rebound.status).toBe(200);
    const messages = await route.listMailMessagesRoute(
      request(`/api/mailboxes/${connection.mailbox_id}/messages`),
      String(connection.mailbox_id)
    );
    expect((await messages.json()).data).toHaveLength(3);

    // A refused code.
    const refused = new URL(
      (await (await start(route, { provider: 'outlook' })).json()).data.authorization_url
    );
    await expectError(
      await complete(route, { state: refused.searchParams.get('state') ?? '', code: 'nope' }),
      422,
      'MAILBOX.EXTERNAL_AUTHORIZATION_FAILED'
    );

    const revoked = await route.revokeExternalMailConnectionRoute(
      write('DELETE', `/api/external-mail-connections/${connection.id}`),
      String(connection.id)
    );
    expect(revoked.status).toBe(204);
    const afterRevoke = await route.listExternalMailConnectionsRoute(
      request('/api/external-mail-connections')
    );
    expect((await afterRevoke.json()).data[0].status).toBe('revoked');
  });

  it('mock start and completion reject bad input and other members', async () => {
    process.env.MOCK_BFF_ENABLED = 'true';
    getSessionUser.mockResolvedValue(mockUser('demo-member'));
    const { route } = await load();

    await expectError(await start(route, {}), 422, 'MAILBOX.EXTERNAL_AUTHORIZATION_INVALID');
    await expectError(
      await start(route, { provider: 'Gmail!' }),
      422,
      'COMMON.VALIDATION_FAILED'
    );
    await expectError(
      await start(route, { provider: 'gmail', backfill_days: 91 }),
      422,
      'COMMON.VALIDATION_FAILED'
    );
    await expectError(
      await start(route, { provider: 'yahoo' }),
      404,
      'MAILBOX.EXTERNAL_PROVIDER_UNAVAILABLE'
    );
    await expectError(
      await start(route, { connection_id: 99 }),
      404,
      'MAILBOX.EXTERNAL_CONNECTION_NOT_FOUND'
    );
    await expectError(await complete(route, { state: '' }), 422, 'COMMON.VALIDATION_FAILED');

    const url = new URL(
      (await (await start(route, { provider: 'gmail' })).json()).data.authorization_url
    );
    // Another member cannot finish this member's sign-in.
    getSessionUser.mockResolvedValue(mockUser('demo-admin'));
    await expectError(
      await complete(route, {
        state: url.searchParams.get('state') ?? '',
        code: url.searchParams.get('code') ?? '',
      }),
      404,
      'MAILBOX.EXTERNAL_AUTHORIZATION_NOT_FOUND'
    );

    // Mutations need the same origin.
    const crossSite = write('POST', '/api/external-mail-authorizations', { provider: 'gmail' });
    crossSite.headers.set('origin', 'https://evil.example');
    crossSite.headers.set('sec-fetch-site', 'cross-site');
    expect((await route.startExternalMailAuthorizationRoute(crossSite)).status).toBe(403);
  });

  it('proxy routes use the right upstream method, path, body, and keep the status', async () => {
    process.env.API_ADAPTER_ENABLED = 'true';
    process.env.API_UPSTREAM_URL = 'https://api.example.com/v1';
    cookieStore.get.mockReturnValue({ value: 'A'.repeat(43) });
    fetchMock.mockImplementation(async (_url, init) =>
      init?.method === 'DELETE'
        ? new Response(null, { status: 204 })
        : Response.json({ code: 0, message: 'success', data: {} }, { status: 201 })
    );
    const { route } = await load();
    activeOrganization = '7';

    await route.listExternalMailConnectorsRoute(request('/api/external-mail-connectors'));
    await route.listExternalMailConnectionsRoute(request('/api/external-mail-connections'));
    await start(route, { provider: 'gmail', backfill_days: 30, extra: true });
    await start(route, { connection_id: 4 });
    const completed = await complete(route, { state: 's', code: 'c' });
    expect(completed.status).toBe(201);
    await route.revokeExternalMailConnectionRoute(
      write('DELETE', '/api/external-mail-connections/4'),
      '4'
    );

    expect(
      fetchMock.mock.calls.map(([url, init]) => {
        const target = new URL(String(url));
        return `${init?.method ?? 'GET'} ${target.pathname}`;
      })
    ).toEqual([
      'GET /v1/external-mail-connectors',
      'GET /v1/external-mail-connections',
      'POST /v1/external-mail-authorizations',
      'POST /v1/external-mail-authorizations',
      'POST /v1/external-mail-authorizations/completions',
      'DELETE /v1/external-mail-connections/4',
    ]);
    expect(
      fetchMock.mock.calls.map(([, init]) =>
        init?.body === undefined ? undefined : JSON.parse(String(init.body))
      )
    ).toEqual([
      undefined,
      undefined,
      { provider: 'gmail', backfill_days: 30 },
      { connection_id: 4 },
      { state: 's', code: 'c' },
      undefined,
    ]);
    for (const [, init] of fetchMock.mock.calls) {
      expect(new Headers(init?.headers).get('organization-id')).toBe('7');
    }

    fetchMock.mockClear();
    await expectError(
      await route.revokeExternalMailConnectionRoute(
        write('DELETE', '/api/external-mail-connections/0'),
        '0'
      ),
      400,
      'COMMON.INVALID_INPUT'
    );
    await expectError(
      await complete(route, { state: 'x'.repeat(3_000), code: 'c' }),
      422,
      'COMMON.VALIDATION_FAILED'
    );
    expect(fetchMock).not.toHaveBeenCalled();
  });
});

async function load(): Promise<{ route: MailboxRoute; store: ExternalStore }> {
  const route = await import('@/features/mailbox/server/mailbox-route');
  const mailboxStore = await import('@/features/mailbox/server/mock-mailbox-store');
  mailboxStore.mockMailboxStore.reset();
  const store = await import('@/features/mailbox/server/mock-external-mail-store');
  store.mockExternalMailStore.reset();
  return { route, store };
}

function start(route: MailboxRoute, body: Record<string, unknown>) {
  return route.startExternalMailAuthorizationRoute(
    write('POST', '/api/external-mail-authorizations', body)
  );
}

function complete(route: MailboxRoute, body: Record<string, unknown>) {
  return route.completeExternalMailAuthorizationRoute(
    write('POST', '/api/external-mail-authorizations/completions', body)
  );
}

async function expectError(response: Response, status: number, errorCode: string) {
  const body = await response.json();
  expect({ status: response.status, error_code: body.error_code }).toEqual({
    status,
    error_code: errorCode,
  });
  expect(response.headers.get('cache-control')).toBe('private, no-store');
}

function write(method: string, path: string, body?: unknown): Request {
  return request(path, { method, ...(body === undefined ? {} : { body: JSON.stringify(body) }) });
}

function request(path: string, options: { method?: string; body?: string } = {}): Request {
  const result = new Request(`https://app.example.com${path}`, {
    method: options.method ?? 'GET',
    body: options.body,
  });
  result.headers.set('organization-id', activeOrganization);
  if (options.body) result.headers.set('content-type', 'application/json');
  if (options.method && options.method !== 'GET') {
    result.headers.set('origin', 'https://app.example.com');
    result.headers.set('sec-fetch-site', 'same-origin');
  }
  return result;
}

function mockUser(id: string) {
  return { id, email: `${id}@example.com`, name: id };
}
