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

/** The `Organization-Id` every request carries; the seeded organization unless a test names one. */
let activeOrganization = '1';

/*
 * The seeded organization 1: demo-admin (user 1) owns it, demo-operator (user 2) is an admin,
 * demo-member (user 3) a member.
 */
describe('platform account browser routes', () => {
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

  it('mock ledger: owner writes, member sees only readable mailboxes, handover records events', async () => {
    process.env.MOCK_BFF_ENABLED = 'true';
    getSessionUser.mockResolvedValue(mockUser('demo-admin'));
    const route = await loadRoute();

    const open = await createMailbox(route, 'open', [
      { user_id: 1, permission: 'manage' },
      { user_id: 3, permission: 'view' },
    ]);
    const closed = await createMailbox(route, 'closed', [{ user_id: 1, permission: 'manage' }]);

    const blog = await createAccount(route, {
      platform: 'Medium',
      handle: 'blog',
      mailbox_id: open.id,
      owner_user_id: 2,
    });
    expect(blog.status).toBe(201);
    const blogBody = (await blog.json()).data;
    expect(blogBody).toMatchObject({
      status: 'active',
      mailbox_address: open.address,
      owner_user_id: 2,
    });
    const hidden = await createAccount(route, {
      platform: 'YouTube',
      handle: '@studio',
      mailbox_id: closed.id,
      owner_user_id: 2,
    });
    expect(hidden.status).toBe(201);
    const hiddenBody = (await hidden.json()).data;

    await expectError(
      await createAccount(route, { platform: 'youtube', handle: '@STUDIO' }),
      409,
      'MAILBOX.PLATFORM_ACCOUNT_TAKEN'
    );
    await expectError(
      await createAccount(route, { platform: 'X', handle: 'x', owner_user_id: 999 }),
      422,
      'COMMON.VALIDATION_FAILED'
    );
    await expectError(
      await createAccount(route, { platform: 'X', handle: 'x', status: 'paused' }),
      422,
      'COMMON.VALIDATION_FAILED'
    );
    await expectError(
      await createAccount(route, { platform: 'X', handle: 7 }),
      400,
      'COMMON.INVALID_INPUT'
    );

    // Search by the registration address.
    expect(await listHandles(route, `?q=${encodeURIComponent(closed.address)}`)).toEqual([
      '@studio',
    ]);
    await expectError(
      await route.listPlatformAccountsRoute(request('/api/platform-accounts?status=paused')),
      400,
      'COMMON.INVALID_INPUT'
    );

    // A member sees the account at a mailbox they may read, not the one they cannot.
    getSessionUser.mockResolvedValue(mockUser('demo-member'));
    expect(await listHandles(route, '')).toEqual(['blog']);
    expect(await listHandles(route, `?q=${encodeURIComponent(closed.address)}`)).toEqual([]);
    await expectError(
      await route.listPlatformAccountEventsRoute(
        request(`/api/platform-accounts/${hiddenBody.id}/events`),
        String(hiddenBody.id)
      ),
      404,
      'MAILBOX.PLATFORM_ACCOUNT_NOT_FOUND'
    );
    await expectError(
      await createAccount(route, { platform: 'X', handle: 'mine' }),
      403,
      'PERMISSION.DENIED'
    );
    await expectError(
      await handOver(route, { from_user_id: 2, to_user_id: 3 }),
      403,
      'PERMISSION.DENIED'
    );

    // The owner hands every account of user 2 to user 3; each move is an owner_changed event.
    getSessionUser.mockResolvedValue(mockUser('demo-admin'));
    const handover = await handOver(route, { from_user_id: 2, to_user_id: 3 });
    expect(handover.status).toBe(201);
    expect((await handover.json()).data).toEqual({
      from_user_id: 2,
      to_user_id: 3,
      account_ids: [blogBody.id, hiddenBody.id],
    });
    await expectError(
      await handOver(route, { from_user_id: 3, to_user_id: 3 }),
      422,
      'COMMON.VALIDATION_FAILED'
    );
    const events = await route.listPlatformAccountEventsRoute(
      request(`/api/platform-accounts/${blogBody.id}/events`),
      String(blogBody.id)
    );
    expect(events.status).toBe(200);
    expect(events.headers.get('cache-control')).toBe('private, no-store');
    const eventBody = await events.json();
    expect(eventBody.data.map((event: { kind: string }) => event.kind)).toEqual([
      'owner_changed',
      'created',
    ]);
    expect(eventBody.data[0]).toMatchObject({ from: '2', to: '3' });

    // The new owner now sees the account behind the closed mailbox, without its address.
    getSessionUser.mockResolvedValue(mockUser('demo-member'));
    const owned = await route.listPlatformAccountsRoute(request('/api/platform-accounts'));
    const ownedItems = (await owned.json()).data as { handle: string; mailbox_address: unknown }[];
    expect(ownedItems.map(item => [item.handle, item.mailbox_address])).toEqual([
      ['blog', open.address],
      ['@studio', null],
    ]);

    // Update replaces every field; delete answers 204 and the account is gone.
    getSessionUser.mockResolvedValue(mockUser('demo-admin'));
    const updated = await route.updatePlatformAccountRoute(
      write('PUT', `/api/platform-accounts/${blogBody.id}`, {
        platform: 'Medium',
        handle: 'blog',
        status: 'retired',
      }),
      String(blogBody.id)
    );
    expect(updated.status).toBe(200);
    expect((await updated.json()).data).toMatchObject({
      status: 'retired',
      mailbox_id: null,
      owner_user_id: null,
    });
    const removed = await route.deletePlatformAccountRoute(
      write('DELETE', `/api/platform-accounts/${blogBody.id}`),
      String(blogBody.id)
    );
    expect(removed.status).toBe(204);
    await expectError(
      await route.deletePlatformAccountRoute(
        write('DELETE', `/api/platform-accounts/${blogBody.id}`),
        String(blogBody.id)
      ),
      404,
      'MAILBOX.PLATFORM_ACCOUNT_NOT_FOUND'
    );
  });

  it('proxy ledger routes use the right upstream method, path, query, and body', async () => {
    process.env.API_ADAPTER_ENABLED = 'true';
    process.env.API_UPSTREAM_URL = 'https://api.example.com/v1';
    cookieStore.get.mockReturnValue({ value: 'A'.repeat(43) });
    fetchMock.mockImplementation(async (_url, init) =>
      init?.method === 'DELETE'
        ? new Response(null, { status: 204 })
        : Response.json({ code: 0, message: 'success', data: {} })
    );
    const route = await loadRoute();
    activeOrganization = '7';

    await route.listPlatformAccountsRoute(
      request('/api/platform-accounts?q=%20b%40acme%20&status=active&owner_user_id=4&extra=1')
    );
    await createAccount(route, { platform: 'X', handle: 'h', id: 9, mailbox_id: 3 });
    await route.updatePlatformAccountRoute(
      write('PUT', '/api/platform-accounts/5', { platform: 'X', handle: 'h', notes: 'n' }),
      '5'
    );
    await route.deletePlatformAccountRoute(write('DELETE', '/api/platform-accounts/5'), '5');
    await route.listPlatformAccountEventsRoute(
      request('/api/platform-accounts/5/events?page=2'),
      '5'
    );
    await handOver(route, { from_user_id: 4, to_user_id: 6 });

    const calls = fetchMock.mock.calls.map(([url, init]) => {
      const target = new URL(String(url));
      return `${init?.method ?? 'GET'} ${target.pathname}${target.search}`;
    });
    expect(calls).toEqual([
      'GET /v1/platform-accounts?page=1&per_page=15&q=b%40acme&owner_user_id=4&status=active',
      'POST /v1/platform-accounts',
      'PUT /v1/platform-accounts/5',
      'DELETE /v1/platform-accounts/5',
      'GET /v1/platform-accounts/5/events?page=2&per_page=15',
      'POST /v1/platform-account-handovers',
    ]);
    const bodies = fetchMock.mock.calls.map(([, init]) =>
      init?.body === undefined ? undefined : JSON.parse(String(init.body))
    );
    const blank = {
      profile_url: '',
      role_id: null,
      mailbox_id: null,
      owner_user_id: null,
      notes: '',
    };
    expect(bodies).toEqual([
      undefined,
      { ...blank, platform: 'X', handle: 'h', mailbox_id: 3 },
      { ...blank, platform: 'X', handle: 'h', notes: 'n' },
      undefined,
      undefined,
      { from_user_id: 4, to_user_id: 6 },
    ]);
    for (const [, init] of fetchMock.mock.calls) {
      expect(new Headers(init?.headers).get('organization-id')).toBe('7');
    }

    // Malformed ids, filters, and bodies never reach the API.
    fetchMock.mockClear();
    await expectError(
      await route.updatePlatformAccountRoute(
        write('PUT', '/api/platform-accounts/0', { platform: 'X', handle: 'h' }),
        '0'
      ),
      400,
      'COMMON.INVALID_INPUT'
    );
    await expectError(
      await route.listPlatformAccountsRoute(request('/api/platform-accounts?role_id=abc')),
      400,
      'COMMON.INVALID_INPUT'
    );
    await expectError(
      await route.listPlatformAccountsRoute(request(`/api/platform-accounts?q=${'x'.repeat(101)}`)),
      400,
      'COMMON.INVALID_INPUT'
    );
    await expectError(
      await createAccount(route, { platform: 'X', handle: 'h', notes: 'x'.repeat(9_000) }),
      413,
      'COMMON.REQUEST_TOO_LARGE'
    );
    await expectError(
      await handOver(route, { from_user_id: 'two', to_user_id: 3 }),
      422,
      'COMMON.VALIDATION_FAILED'
    );
    expect(fetchMock).not.toHaveBeenCalled();
  });
});

async function loadRoute(): Promise<MailboxRoute> {
  const route = await import('@/features/mailbox/server/mailbox-route');
  const store = await import('@/features/mailbox/server/mock-mailbox-store');
  store.mockMailboxStore.reset();
  const ledger = await import('@/features/mailbox/server/mock-platform-account-store');
  ledger.mockPlatformAccountStore.reset();
  return route;
}

async function createMailbox(
  route: MailboxRoute,
  localPart: string,
  members: { user_id: number; permission: string }[]
): Promise<{ id: number; address: string }> {
  const response = await route.createMailboxRoute(
    write('POST', '/api/mailboxes', { local_part: localPart, type: 'shared', members })
  );
  expect(response.status).toBe(201);
  return (await response.json()).data;
}

function createAccount(route: MailboxRoute, body: Record<string, unknown>) {
  return route.createPlatformAccountRoute(write('POST', '/api/platform-accounts', body));
}

function handOver(route: MailboxRoute, body: Record<string, unknown>) {
  return route.handOverPlatformAccountsRoute(
    write('POST', '/api/platform-account-handovers', body)
  );
}

async function listHandles(route: MailboxRoute, search: string): Promise<string[]> {
  const response = await route.listPlatformAccountsRoute(
    request(`/api/platform-accounts${search}`)
  );
  expect(response.status).toBe(200);
  expect(response.headers.get('cache-control')).toBe('private, no-store');
  return ((await response.json()).data as { handle: string }[]).map(item => item.handle);
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
