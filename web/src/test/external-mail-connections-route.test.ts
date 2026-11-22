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
describe('external mail credentials, sync runs, purges, and directory routes', () => {
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

  it('mock app password binding: every outcome, without keeping the password', async () => {
    process.env.MOCK_BFF_ENABLED = 'true';
    getSessionUser.mockResolvedValue(mockUser('demo-member'));
    const { route, store } = await load();
    const secret = 'app-secret-42';

    const created = await bind(route, {
      provider: 'imap',
      host: 'IMAP.Example.com',
      username: 'riley',
      password: secret,
      backfill_days: 30,
    });
    expect(created.status).toBe(201);
    expect(created.headers.get('cache-control')).toBe('private, no-store');
    const text = await created.text();
    expect(text).not.toContain(secret);
    const connection = JSON.parse(text).data;
    expect(connection).toMatchObject({
      provider: 'imap',
      account_address: 'riley@imap.example.com',
      status: 'active',
    });
    // Nothing the store holds or answers carries the password.
    expect(JSON.stringify(store.mockExternalMailStore)).not.toContain(secret);

    const restored = await bind(route, {
      provider: 'imap',
      host: 'imap.example.com',
      port: 993,
      username: 'Riley',
      password: 'another',
    });
    expect(restored.status).toBe(200);
    expect((await restored.json()).data.id).toBe(connection.id);

    await expectError(
      await bind(route, { provider: 'imap', host: 'unreachable.example', username: 'a', password: 'b' }),
      422,
      'MAILBOX.EXTERNAL_SERVER_UNREACHABLE'
    );
    await expectError(
      await bind(route, {
        provider: 'imap',
        host: 'imap.example.com',
        username: 'a',
        password: store.MOCK_IMAP_REFUSED_PASSWORD,
      }),
      422,
      'MAILBOX.EXTERNAL_AUTHORIZATION_FAILED'
    );
    await expectError(
      await bind(route, { provider: 'gmail', host: 'imap.example.com', username: 'a', password: 'b' }),
      404,
      'MAILBOX.EXTERNAL_PROVIDER_UNAVAILABLE'
    );
    await expectError(
      await bind(route, { provider: 'imap', host: 'imap.example.com', port: 143, username: 'a', password: 'b' }),
      422,
      'COMMON.VALIDATION_FAILED'
    );
    await expectError(
      await bind(route, { provider: 'imap', host: 'imap.example.com', username: 'a\u0000', password: 'b' }),
      422,
      'COMMON.VALIDATION_FAILED'
    );

    // Another member binding the same account is refused.
    getSessionUser.mockResolvedValue(mockUser('demo-operator'));
    await expectError(
      await bind(route, { provider: 'imap', host: 'imap.example.com', username: 'riley', password: 'x' }),
      409,
      'MAILBOX.EXTERNAL_ACCOUNT_TAKEN'
    );

    // Ten attempts in 15 minutes, then 429 with Retry-After.
    getSessionUser.mockResolvedValue(mockUser('demo-member'));
    let limited: Response | null = null;
    for (let attempt = 0; attempt < 12 && !limited; attempt += 1) {
      const response = await bind(route, {
        provider: 'imap',
        host: 'unreachable.example',
        username: 'a',
        password: 'b',
      });
      if (response.status === 429) limited = response;
    }
    expect(limited).not.toBeNull();
    const retryAfter = Number(limited?.headers.get('retry-after'));
    expect(retryAfter).toBeGreaterThan(0);
    expect(retryAfter).toBeLessThanOrEqual(15 * 60);
    expect((await limited?.json()).error_code).toBe('COMMON.RATE_LIMITED');
  });

  it('mock app passwords: 10 live connections, then the limit once the rate window passes', async () => {
    const { store } = await load();
    const boxes = { create: () => 1, sync: () => undefined };
    let clock = Date.parse('2026-11-21T10:00:00Z');
    const mock = new store.MockExternalMailStore({
      now: () => new Date(clock),
      randomState: () => 's',
    });
    const attempt = (username: string) =>
      mock.bindCredentials(1, 3, { provider: 'imap', host: 'h.example', username, password: 'p' }, boxes);
    for (let index = 0; index < 10; index += 1) expect(attempt(`u${index}`).ok).toBe(true);
    expect(attempt('u10')).toEqual({ ok: false, error: 'rate_limited', retryAfterSeconds: 900 });
    clock += 16 * 60 * 1_000;
    expect(attempt('u10')).toEqual({ ok: false, error: 'connection_limit' });
  });

  it('mock sync runs, purge, and its 409, for the owner only', async () => {
    process.env.MOCK_BFF_ENABLED = 'true';
    getSessionUser.mockResolvedValue(mockUser('demo-member'));
    const { route } = await load();
    const created = await bind(route, {
      provider: 'imap',
      host: 'imap.example.com',
      username: 'riley@example.com',
      password: 'p',
    });
    const connection = (await created.json()).data;
    const id = String(connection.id);

    const runs = await route.listExternalMailSyncRunsRoute(
      request(`/api/external-mail-connections/${id}/sync-runs`),
      id
    );
    expect(runs.status).toBe(200);
    expect(runs.headers.get('cache-control')).toBe('private, no-store');
    const [latest] = (await runs.json()).data;
    expect(latest).toMatchObject({ outcome: 'ok', more: true, error_code: null });
    expect(Object.keys(latest).sort()).toEqual(
      [
        'error_code',
        'fetched_count',
        'finished_at',
        'id',
        'more',
        'outcome',
        'started_at',
        'stored_count',
        'throttled',
        'timed_out',
      ].sort()
    );

    await expectError(
      await route.purgeExternalMailCopiesRoute(
        write('POST', `/api/external-mail-connections/${id}/purges`),
        id
      ),
      409,
      'MAILBOX.EXTERNAL_CONNECTION_NOT_REVOKED'
    );

    // The owner and admin roles get 404 on another member's runs and purge.
    getSessionUser.mockResolvedValue(mockUser('demo-admin'));
    await expectError(
      await route.listExternalMailSyncRunsRoute(
        request(`/api/external-mail-connections/${id}/sync-runs`),
        id
      ),
      404,
      'MAILBOX.EXTERNAL_CONNECTION_NOT_FOUND'
    );
    await expectError(
      await route.purgeExternalMailCopiesRoute(
        write('POST', `/api/external-mail-connections/${id}/purges`),
        id
      ),
      404,
      'MAILBOX.EXTERNAL_CONNECTION_NOT_FOUND'
    );

    getSessionUser.mockResolvedValue(mockUser('demo-member'));
    await route.revokeExternalMailConnectionRoute(
      write('DELETE', `/api/external-mail-connections/${id}`),
      id
    );
    const purged = await route.purgeExternalMailCopiesRoute(
      write('POST', `/api/external-mail-connections/${id}/purges`),
      id
    );
    expect(purged.status).toBe(200);
    expect((await purged.json()).data).toEqual({ deleted_messages: 3, completed: true });
    const mailboxes = await route.listMailboxesRoute(request('/api/mailboxes'));
    expect(
      (await mailboxes.json()).data.some(
        (item: { id: number }) => item.id === connection.mailbox_id
      )
    ).toBe(false);
    await expectError(
      await route.purgeExternalMailCopiesRoute(
        write('POST', `/api/external-mail-connections/${id}/purges`),
        id
      ),
      404,
      'MAILBOX.EXTERNAL_CONNECTION_NOT_FOUND'
    );
  });

  it('mock connection directory: managers only, masked, keyset pages, forced disconnect', async () => {
    process.env.MOCK_BFF_ENABLED = 'true';
    const { route, store } = await load();
    const boxes = { create: () => 1, sync: () => undefined };
    for (let index = 0; index < 4; index += 1) {
      store.mockExternalMailStore.bindCredentials(
        1,
        3,
        { provider: 'imap', host: 'example.com', username: `person${index}`, password: 'p' },
        boxes
      );
    }

    getSessionUser.mockResolvedValue(mockUser('demo-member'));
    await expectError(
      await route.listExternalMailConnectionDirectoryRoute(
        request('/api/external-mail-connection-directory')
      ),
      403,
      'PERMISSION.DENIED'
    );
    await expectError(
      await route.forceDisconnectExternalMailConnectionRoute(
        write('DELETE', '/api/external-mail-connection-directory/1'),
        '1'
      ),
      403,
      'PERMISSION.DENIED'
    );

    getSessionUser.mockResolvedValue(mockUser('demo-operator'));
    await expectError(
      await route.listExternalMailConnectionDirectoryRoute(
        request('/api/external-mail-connection-directory?page=2')
      ),
      400,
      'COMMON.INVALID_INPUT'
    );
    const first = await route.listExternalMailConnectionDirectoryRoute(
      request('/api/external-mail-connection-directory?per_page=3')
    );
    expect(first.status).toBe(200);
    expect(first.headers.get('cache-control')).toBe('private, no-store');
    const firstBody = await first.json();
    expect(firstBody.data.map((item: { id: number }) => item.id)).toEqual([4, 3, 2]);
    expect(firstBody.data[0]).toEqual({
      id: 4,
      provider: 'imap',
      status: 'active',
      masked_account_address: 'pe***@example.com',
      owner_user_id: 3,
      owner_display_name: 'Riley Chen',
      last_synced_at: expect.any(String),
      created_at: expect.any(String),
    });
    expect(JSON.stringify(firstBody)).not.toContain('person3@');
    expect(firstBody.meta).toMatchObject({ per_page: 3, has_more: true });
    const second = await route.listExternalMailConnectionDirectoryRoute(
      request(
        `/api/external-mail-connection-directory?per_page=3&cursor=${encodeURIComponent(firstBody.meta.next_cursor)}`
      )
    );
    const secondBody = await second.json();
    expect(secondBody.data.map((item: { id: number }) => item.id)).toEqual([1]);
    expect(secondBody.meta).toMatchObject({ has_more: false, next_cursor: null });

    const disconnected = await route.forceDisconnectExternalMailConnectionRoute(
      write('DELETE', '/api/external-mail-connection-directory/4'),
      '4'
    );
    expect(disconnected.status).toBe(204);
    expect(store.mockExternalMailStore.list(1, 3).find(item => item.id === 4)?.status).toBe('revoked');
    await expectError(
      await route.forceDisconnectExternalMailConnectionRoute(
        write('DELETE', '/api/external-mail-connection-directory/99'),
        '99'
      ),
      404,
      'MAILBOX.EXTERNAL_CONNECTION_NOT_FOUND'
    );
  });

  it('masks short local parts to one character', async () => {
    const { store } = await load();
    expect(store.maskAddress('a@x.io')).toBe('a***@x.io');
    expect(store.maskAddress('ab@x.io')).toBe('a***@x.io');
    expect(store.maskAddress('stark@gmail.com')).toBe('st***@gmail.com');
  });

  it('proxy routes forward method, path, body, query, and Retry-After', async () => {
    process.env.API_ADAPTER_ENABLED = 'true';
    process.env.API_UPSTREAM_URL = 'https://api.example.com/v1';
    cookieStore.get.mockReturnValue({ value: 'A'.repeat(43) });
    fetchMock.mockImplementation(async (url, init) => {
      const path = new URL(String(url)).pathname;
      if (path === '/v1/external-mail-credentials') {
        return Response.json(
          { code: 429, message: 'slow down', error_code: 'COMMON.RATE_LIMITED' },
          { status: 429, headers: { 'retry-after': '120' } }
        );
      }
      if (init?.method === 'DELETE') return new Response(null, { status: 204 });
      return Response.json({ code: 0, message: 'success', data: [] }, { status: 200 });
    });
    const { route } = await load();
    activeOrganization = '7';

    const limited = await bind(route, {
      provider: 'imap',
      host: 'imap.example.com',
      username: 'me',
      password: 'secret',
      backfill_days: 30,
      extra: true,
    });
    expect(limited.status).toBe(429);
    expect(limited.headers.get('retry-after')).toBe('120');
    await route.listExternalMailSyncRunsRoute(
      request('/api/external-mail-connections/4/sync-runs'),
      '4'
    );
    await route.purgeExternalMailCopiesRoute(
      write('POST', '/api/external-mail-connections/4/purges'),
      '4'
    );
    await route.listExternalMailConnectionDirectoryRoute(
      request('/api/external-mail-connection-directory?per_page=15&cursor=abc')
    );
    await route.forceDisconnectExternalMailConnectionRoute(
      write('DELETE', '/api/external-mail-connection-directory/4'),
      '4'
    );

    expect(
      fetchMock.mock.calls.map(([url, init]) => {
        const target = new URL(String(url));
        return `${init?.method ?? 'GET'} ${target.pathname}${target.search}`;
      })
    ).toEqual([
      'POST /v1/external-mail-credentials',
      'GET /v1/external-mail-connections/4/sync-runs',
      'POST /v1/external-mail-connections/4/purges',
      'GET /v1/external-mail-connection-directory?per_page=15&cursor=abc',
      'DELETE /v1/external-mail-connection-directory/4',
    ]);
    expect(JSON.parse(String(fetchMock.mock.calls[0]?.[1]?.body))).toEqual({
      provider: 'imap',
      host: 'imap.example.com',
      port: 993,
      username: 'me',
      password: 'secret',
      backfill_days: 30,
    });
    for (const [, init] of fetchMock.mock.calls) {
      expect(new Headers(init?.headers).get('organization-id')).toBe('7');
    }

    fetchMock.mockClear();
    await expectError(
      await route.listExternalMailSyncRunsRoute(
        request('/api/external-mail-connections/x/sync-runs'),
        'x'
      ),
      400,
      'COMMON.INVALID_INPUT'
    );
    await expectError(
      await route.listExternalMailConnectionDirectoryRoute(
        request('/api/external-mail-connection-directory?per_page=500')
      ),
      422,
      'COMMON.VALIDATION_FAILED'
    );
    expect(fetchMock).not.toHaveBeenCalled();
  });
});

function bind(route: MailboxRoute, body: Record<string, unknown>) {
  return route.bindExternalMailCredentialsRoute(
    write('POST', '/api/external-mail-credentials', body)
  );
}

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
