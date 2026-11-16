import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { parseOptionalWebFeatures } from '@/config/optional-features';

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

describe('mailbox browser route boundary', () => {
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
    process.env.NEXT_PUBLIC_OPTIONAL_FEATURES = 'mailbox';
    process.env.API_UPSTREAM_TIMEOUT_MS = '5000';
    process.env.API_UPSTREAM_MAX_RESPONSE_BYTES = '1048576';
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.resetModules();
    for (const key of managedKeys) {
      delete process.env[key];
      if (originalEnv[key] !== undefined) process.env[key] = originalEnv[key];
    }
  });

  it('is selected on its own, without an organization', () => {
    expect(parseOptionalWebFeatures('mailbox')).toEqual(['mailbox']);
  });

  it('stays unavailable when the feature is not selected', async () => {
    process.env.MOCK_BFF_ENABLED = 'true';
    process.env.NEXT_PUBLIC_OPTIONAL_FEATURES = '';
    getSessionUser.mockResolvedValue(mockUser('demo-admin'));
    const route = await loadRoute();

    const read = await route.listMailboxesRoute(request('/api/mailboxes'));
    const write = await create(route, { local_part: 'hello' });

    for (const response of [read, write]) {
      expect(response.status).toBe(503);
      await expect(response.json()).resolves.toMatchObject({
        error_code: 'COMMON.SERVICE_UNAVAILABLE',
      });
    }
    expect(getSessionUser).not.toHaveBeenCalled();
  });

  it('stays unavailable in production without the adapter or an explicit mock', async () => {
    const route = await loadRoute();
    const base = { featureEnabled: true, adapterEnabled: false, nodeEnv: 'production' as const };

    expect(route.resolveMailboxRoute({ ...base, mockBffEnabled: false }).available).toBe(false);
    expect(
      route.resolveMailboxRoute({ ...base, adapterEnabled: true, mockBffEnabled: false })
    ).toEqual({ available: true, backend: 'go-api' });
    expect(
      route.resolveMailboxRoute({
        ...base,
        featureEnabled: false,
        adapterEnabled: true,
        mockBffEnabled: true,
      }).available
    ).toBe(false);
  });

  it('rejects cross-origin writes before authentication or body parsing', async () => {
    process.env.MOCK_BFF_ENABLED = 'true';
    const route = await loadRoute();
    const options = { body: '{', origin: 'https://evil.example' };

    const responses = await Promise.all([
      route.createMailboxRoute(request('/api/mailboxes', { ...options, method: 'POST' })),
      route.deleteMailboxRoute(request('/api/mailboxes/1', { ...options, method: 'DELETE' }), '1'),
      route.sendMailMessageRoute(
        request('/api/mailboxes/1/messages', { ...options, method: 'POST' }),
        '1'
      ),
      route.updateMailMessageRoute(
        request('/api/mailboxes/1/messages/1', { ...options, method: 'PUT' }),
        '1',
        '1'
      ),
      route.deleteMailMessageRoute(
        request('/api/mailboxes/1/messages/1', { ...options, method: 'DELETE' }),
        '1',
        '1'
      ),
    ]);

    for (const response of responses) {
      expect(response.status).toBe(403);
      expect(response.headers.get('cache-control')).toBe('private, no-store');
    }
    expect(getSessionUser).not.toHaveBeenCalled();
  });

  it('rejects unauthenticated reads and writes', async () => {
    process.env.MOCK_BFF_ENABLED = 'true';
    getSessionUser.mockResolvedValue(null);
    const route = await loadRoute();

    const responses = [
      await route.listMailboxDomainsRoute(request('/api/mailbox-domains')),
      await route.listMailboxesRoute(request('/api/mailboxes')),
      await route.listMailMessagesRoute(request('/api/mailboxes/1/messages'), '1'),
      await route.getMailMessageRoute(request('/api/mailboxes/1/messages/1'), '1', '1'),
      await create(route, { local_part: 'hello' }),
      await send(route, 1, { to: ['bob@example.com'], subject: 'Hi', text: 'Body' }),
    ];

    for (const response of responses) {
      expect(response.status).toBe(401);
      expect(response.headers.get('cache-control')).toBe('private, no-store');
      await expect(response.json()).resolves.toMatchObject({ error_code: 'AUTH.UNAUTHORIZED' });
    }
  });

  it('creates mailboxes on the mock domain and applies the address rules', async () => {
    process.env.MOCK_BFF_ENABLED = 'true';
    getSessionUser.mockResolvedValue(mockUser('demo-admin'));
    const route = await loadRoute();

    const domains = await route.listMailboxDomainsRoute(request('/api/mailbox-domains'));
    await expect(domains.json()).resolves.toMatchObject({
      code: 0,
      data: [{ domain: 'mail.example.com', max_mailboxes: 10 }],
    });

    const created = await create(route, {
      local_part: '  Hello.Team ',
      display_name: ' Hello   Team ',
    });
    expect(created.status).toBe(201);
    expect(created.headers.get('cache-control')).toBe('private, no-store');
    expect(created.headers.get('vary')).toContain('Cookie');
    const mailbox = (await created.json()).data;
    expect(mailbox).toMatchObject({
      address: 'hello.team@mail.example.com',
      display_name: 'Hello Team',
    });
    const list = await route.listMailboxesRoute(request('/api/mailboxes'));
    await expect(list.json()).resolves.toMatchObject({ data: [{ id: mailbox.id }] });

    await expectError(await create(route, { local_part: 'a..b' }), 422, 'MAILBOX.ADDRESS_INVALID');
    await expectError(await create(route, { local_part: '-a' }), 422, 'MAILBOX.ADDRESS_INVALID');
    await expectError(
      await create(route, { local_part: 'ok', display_name: 'Bad <name>' }),
      422,
      'MAILBOX.ADDRESS_INVALID'
    );
    await expectError(
      await create(route, { local_part: 'Postmaster' }),
      422,
      'MAILBOX.ADDRESS_RESERVED'
    );
    await expectError(
      await create(route, { local_part: 'HELLO.team' }),
      409,
      'MAILBOX.ADDRESS_TAKEN'
    );
    await expectError(await create(route, {}), 422, 'COMMON.VALIDATION_FAILED');
    await expectError(
      await create(route, { local_part: 'a'.repeat(65) }),
      422,
      'COMMON.VALIDATION_FAILED'
    );
    const malformed = await route.createMailboxRoute(
      request('/api/mailboxes', { method: 'POST', body: '{' })
    );
    await expectError(malformed, 400, 'COMMON.INVALID_INPUT');
  });

  it('keeps a deleted address reserved and enforces the mailbox limit', async () => {
    process.env.MOCK_BFF_ENABLED = 'true';
    getSessionUser.mockResolvedValue(mockUser('demo-admin'));
    const route = await loadRoute();
    const first = (await (await create(route, { local_part: 'box0' })).json()).data;

    const removed = await route.deleteMailboxRoute(
      request(`/api/mailboxes/${first.id}`, { method: 'DELETE' }),
      String(first.id)
    );
    expect(removed.status).toBe(204);
    await expectError(await create(route, { local_part: 'box0' }), 409, 'MAILBOX.ADDRESS_TAKEN');
    await expectError(
      await route.deleteMailboxRoute(
        request(`/api/mailboxes/${first.id}`, { method: 'DELETE' }),
        String(first.id)
      ),
      404,
      'MAILBOX.NOT_FOUND'
    );
    // Another user cannot take the address either.
    getSessionUser.mockResolvedValue(mockUser('demo-member'));
    await expectError(await create(route, { local_part: 'box0' }), 409, 'MAILBOX.ADDRESS_TAKEN');

    getSessionUser.mockResolvedValue(mockUser('demo-admin'));
    for (let index = 1; index <= 10; index += 1) {
      expect((await create(route, { local_part: `box${index}` })).status).toBe(201);
    }
    await expectError(await create(route, { local_part: 'box11' }), 409, 'MAILBOX.LIMIT_REACHED');
  });

  it("reports another user's mailbox and messages as missing", async () => {
    process.env.MOCK_BFF_ENABLED = 'true';
    getSessionUser.mockResolvedValue(mockUser('demo-admin'));
    const route = await loadRoute();
    const mailbox = (await (await create(route, { local_part: 'private' })).json()).data;
    const path = `/api/mailboxes/${mailbox.id}/messages`;

    getSessionUser.mockResolvedValue(mockUser('demo-member'));
    await expect(
      (await route.listMailboxesRoute(request('/api/mailboxes'))).json()
    ).resolves.toMatchObject({
      data: [],
    });
    await expectError(
      await route.listMailMessagesRoute(request(path), String(mailbox.id)),
      404,
      'MAILBOX.NOT_FOUND'
    );
    await expectError(
      await route.getMailMessageRoute(request(`${path}/1`), String(mailbox.id), '1'),
      404,
      'MAILBOX.NOT_FOUND'
    );
    await expectError(
      await send(route, mailbox.id, { to: ['bob@example.com'], subject: 'Hi', text: 'Body' }),
      404,
      'MAILBOX.NOT_FOUND'
    );
    await expectError(
      await route.deleteMailboxRoute(
        request(`/api/mailboxes/${mailbox.id}`, { method: 'DELETE' }),
        String(mailbox.id)
      ),
      404,
      'MAILBOX.NOT_FOUND'
    );
  });

  it('counts unread inbound messages per mailbox and follows the read state', async () => {
    process.env.MOCK_BFF_ENABLED = 'true';
    getSessionUser.mockResolvedValue(mockUser('demo-admin'));
    const route = await loadRoute();
    const created = (await (await create(route, { local_part: 'hello' })).json()).data;
    // A create response reports 0; the listing counts the unread welcome message.
    expect(created.unread_count).toBe(0);
    const id = String(created.id);
    const path = `/api/mailboxes/${id}/messages`;
    const unreadOf = async () =>
      (await (await route.listMailboxesRoute(request('/api/mailboxes'))).json()).data[0]
        .unread_count;
    expect(await unreadOf()).toBe(1);

    // Sending never adds unread mail.
    await route.sendMailMessageRoute(
      request(path, {
        method: 'POST',
        body: JSON.stringify({ to: ['bob@example.com'], subject: 'Hi', text: 'Body' }),
      }),
      id
    );
    expect(await unreadOf()).toBe(1);

    const inbox = await (
      await route.listMailMessagesRoute(request(`${path}?direction=inbound`), id)
    ).json();
    const welcomeId = String(inbox.data[0].id);
    const setRead = (read: boolean) =>
      route.updateMailMessageRoute(
        request(`${path}/${welcomeId}`, { method: 'PUT', body: JSON.stringify({ read }) }),
        id,
        welcomeId
      );
    await setRead(true);
    expect(await unreadOf()).toBe(0);
    await setRead(false);
    expect(await unreadOf()).toBe(1);
  });

  it('reports the mailbox limit before a taken address, as the API does', async () => {
    process.env.MOCK_BFF_ENABLED = 'true';
    getSessionUser.mockResolvedValue(mockUser('demo-admin'));
    const route = await loadRoute();
    for (let index = 0; index < 10; index += 1) {
      expect((await create(route, { local_part: `full${index}` })).status).toBe(201);
    }
    await expectError(await create(route, { local_part: 'full0' }), 409, 'MAILBOX.LIMIT_REACHED');
  });

  it('lists newest first without bodies, tracks read state, and deletes messages', async () => {
    process.env.MOCK_BFF_ENABLED = 'true';
    getSessionUser.mockResolvedValue(mockUser('demo-admin'));
    const route = await loadRoute();
    const mailbox = (
      await (await create(route, { local_part: 'hello', display_name: 'Hello' })).json()
    ).data;
    const id = String(mailbox.id);
    const path = `/api/mailboxes/${id}/messages`;

    const inbox = await (
      await route.listMailMessagesRoute(request(`${path}?direction=inbound`), id)
    ).json();
    expect(inbox.data).toHaveLength(1);
    expect(inbox.data[0]).toMatchObject({ direction: 'inbound', status: 'received', read: false });
    expect(inbox.data[0]).not.toHaveProperty('text_body');
    expect(inbox.data[0]).not.toHaveProperty('html_body');
    const welcomeId = String(inbox.data[0].id);

    const read = await route.getMailMessageRoute(request(`${path}/${welcomeId}`), id, welcomeId);
    expect(read.status).toBe(200);
    const welcome = (await read.json()).data;
    expect(welcome.text_body).not.toBe('');
    expect(welcome.read).toBe(false);

    const marked = await route.updateMailMessageRoute(
      request(`${path}/${welcomeId}`, { method: 'PUT', body: JSON.stringify({ read: true }) }),
      id,
      welcomeId
    );
    expect(marked.status).toBe(200);
    await expect(marked.json()).resolves.toMatchObject({ data: { read: true } });
    await expectError(
      await route.updateMailMessageRoute(
        request(`${path}/${welcomeId}`, { method: 'PUT', body: '{}' }),
        id,
        welcomeId
      ),
      422,
      'COMMON.VALIDATION_FAILED'
    );

    const first = await send(route, mailbox.id, {
      to: ['Bob@Example.com', 'bob@example.com'],
      cc: ['carol@example.com'],
      subject: ' First ',
      text: 'Plain body',
      in_reply_to: `<${welcome.message_id}>`,
    });
    expect(first.status).toBe(201);
    const sent = (await first.json()).data;
    expect(sent).toMatchObject({
      direction: 'outbound',
      status: 'sent',
      from: { address: 'hello@mail.example.com', name: 'Hello' },
      to: ['bob@example.com'],
      cc: ['carol@example.com'],
      subject: 'First',
      read: true,
      failure_code: '',
      in_reply_to: welcome.message_id,
    });
    const second = (
      await (
        await send(route, mailbox.id, {
          to: ['bob@example.com'],
          subject: 'Second',
          html: '<p>x</p>',
        })
      ).json()
    ).data;

    const page = await (
      await route.listMailMessagesRoute(request(`${path}?direction=outbound&per_page=1`), id)
    ).json();
    expect(page.data.map((item: { id: number }) => item.id)).toEqual([second.id]);
    expect(page.meta).toMatchObject({ current_page: 1, per_page: 1, total: 2, last_page: 2 });
    expect(page.links.next).toContain('direction=outbound&page=2');
    const next = await (
      await route.listMailMessagesRoute(request(`${path}?direction=outbound&per_page=1&page=2`), id)
    ).json();
    expect(next.data.map((item: { id: number }) => item.id)).toEqual([sent.id]);
    const everything = await (await route.listMailMessagesRoute(request(path), id)).json();
    expect(everything.meta.total).toBe(3);

    await expectError(
      await route.listMailMessagesRoute(request(`${path}?direction=spam`), id),
      400,
      'COMMON.INVALID_INPUT'
    );
    await expectError(
      await route.getMailMessageRoute(request(`${path}/abc`), id, 'abc'),
      400,
      'COMMON.INVALID_INPUT'
    );
    await expectError(
      await route.listMailMessagesRoute(request('/api/mailboxes/0/messages'), '0'),
      400,
      'COMMON.INVALID_INPUT'
    );

    const removed = await route.deleteMailMessageRoute(
      request(`${path}/${sent.id}`, { method: 'DELETE' }),
      id,
      String(sent.id)
    );
    expect(removed.status).toBe(204);
    await expectError(
      await route.getMailMessageRoute(request(`${path}/${sent.id}`), id, String(sent.id)),
      404,
      'MAILBOX.MESSAGE_NOT_FOUND'
    );
    await expectError(
      await route.deleteMailMessageRoute(
        request(`${path}/${sent.id}`, { method: 'DELETE' }),
        id,
        String(sent.id)
      ),
      404,
      'MAILBOX.MESSAGE_NOT_FOUND'
    );
  });

  it('validates a send like the API', async () => {
    process.env.MOCK_BFF_ENABLED = 'true';
    getSessionUser.mockResolvedValue(mockUser('demo-admin'));
    const route = await loadRoute();
    const mailbox = (await (await create(route, { local_part: 'hello' })).json()).data;
    const valid = { to: ['bob@example.com'], subject: 'Hi', text: 'Body' };

    for (const invalid of [
      { ...valid, to: ['Bob <bob@example.com>'] },
      { ...valid, to: ['bob@localhost'] },
      { ...valid, subject: 'two\nlines' },
      { ...valid, subject: '   ' },
      { ...valid, text: '   ' },
      { to: valid.to, subject: 'Hi' },
      { ...valid, in_reply_to: 'no-at-sign' },
      {
        ...valid,
        to: Array.from({ length: 30 }, (_, index) => `to${index}@example.com`),
        cc: Array.from({ length: 21 }, (_, index) => `cc${index}@example.com`),
      },
    ]) {
      await expectError(await send(route, mailbox.id, invalid), 422, 'MAILBOX.MESSAGE_INVALID');
    }
    for (const malformed of [
      { subject: 'Hi', text: 'Body' },
      { ...valid, to: [] },
      { ...valid, to: 'bob@example.com' },
      { ...valid, subject: 's'.repeat(999) },
      { ...valid, cc: [1] },
    ]) {
      await expectError(await send(route, mailbox.id, malformed), 422, 'COMMON.VALIDATION_FAILED');
    }

    const outbound = await (
      await route.listMailMessagesRoute(
        request(`/api/mailboxes/${mailbox.id}/messages?direction=outbound`),
        String(mailbox.id)
      )
    ).json();
    expect(outbound.data).toEqual([]);
  });

  it('forwards only the fixed production paths, methods, and whitelisted query', async () => {
    process.env.API_ADAPTER_ENABLED = 'true';
    process.env.API_UPSTREAM_URL = 'https://api.example.com/v1';
    cookieStore.get.mockReturnValue({ value: 'A'.repeat(43) });
    fetchMock.mockImplementation(async () =>
      Response.json({ code: 0, message: 'success', data: [] })
    );
    const route = await loadRoute();

    await route.listMailboxDomainsRoute(request('/api/mailbox-domains?x=1'));
    await route.listMailboxesRoute(request('/api/mailboxes?x=1'));
    await create(route, { local_part: 'hello', display_name: 'Hello', owner_id: 9 });
    await route.deleteMailboxRoute(request('/api/mailboxes/12', { method: 'DELETE' }), '12');
    await route.listMailMessagesRoute(
      request('/api/mailboxes/12/messages?direction=outbound&page=2&per_page=500&user_id=9'),
      '12'
    );
    await route.listMailMessagesRoute(request('/api/mailboxes/12/messages'), '12');
    await send(route, 12, { to: ['bob@example.com'], subject: 'Hi', text: 'Body', from: 'x@y.z' });
    await route.getMailMessageRoute(request('/api/mailboxes/12/messages/34'), '12', '34');
    await route.updateMailMessageRoute(
      request('/api/mailboxes/12/messages/34', {
        method: 'PUT',
        body: JSON.stringify({ read: false }),
      }),
      '12',
      '34'
    );
    await route.deleteMailMessageRoute(
      request('/api/mailboxes/12/messages/34', { method: 'DELETE' }),
      '12',
      '34'
    );

    const calls = fetchMock.mock.calls.map(([url, init]) => {
      const target = new URL(String(url));
      return `${init?.method ?? 'GET'} ${target.pathname}${target.search}`;
    });
    expect(calls).toEqual([
      'GET /v1/mailbox-domains',
      'GET /v1/mailboxes',
      'POST /v1/mailboxes',
      'DELETE /v1/mailboxes/12',
      'GET /v1/mailboxes/12/messages?page=2&per_page=100&direction=outbound',
      'GET /v1/mailboxes/12/messages?page=1&per_page=15',
      'POST /v1/mailboxes/12/messages',
      'GET /v1/mailboxes/12/messages/34',
      'PUT /v1/mailboxes/12/messages/34',
      'DELETE /v1/mailboxes/12/messages/34',
    ]);
    expect(JSON.parse(String(fetchMock.mock.calls[2][1]?.body))).toEqual({
      local_part: 'hello',
      display_name: 'Hello',
    });
    expect(JSON.parse(String(fetchMock.mock.calls[6][1]?.body))).toEqual({
      to: ['bob@example.com'],
      subject: 'Hi',
      text: 'Body',
    });
    expect(JSON.parse(String(fetchMock.mock.calls[8][1]?.body))).toEqual({ read: false });
    for (const [, init] of fetchMock.mock.calls) {
      expect(new Headers(init?.headers).has('organization-id')).toBe(false);
    }
    expect(getSessionUser).not.toHaveBeenCalled();
  });

  it('rejects malformed input before it reaches the API', async () => {
    process.env.API_ADAPTER_ENABLED = 'true';
    process.env.API_UPSTREAM_URL = 'https://api.example.com/v1';
    cookieStore.get.mockReturnValue({ value: 'A'.repeat(43) });
    const route = await loadRoute();

    await expectError(
      await route.getMailMessageRoute(request('/api/mailboxes/1/messages/x'), '1', '../x'),
      400,
      'COMMON.INVALID_INPUT'
    );
    await expectError(
      await route.listMailMessagesRoute(request('/api/mailboxes/1/messages?direction=all'), '1'),
      400,
      'COMMON.INVALID_INPUT'
    );
    await expectError(
      await send(route, 1, { to: [], subject: 'Hi' }),
      422,
      'COMMON.VALIDATION_FAILED'
    );
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('keeps the delivery failure code and status from the API', async () => {
    process.env.API_ADAPTER_ENABLED = 'true';
    process.env.API_UPSTREAM_URL = 'https://api.example.com/v1';
    cookieStore.get.mockReturnValue({ value: 'A'.repeat(43) });
    fetchMock.mockResolvedValueOnce(
      Response.json(
        { code: 502, error_code: 'MAILBOX.DELIVERY_FAILED', message: 'provider said no' },
        { status: 502 }
      )
    );
    const route = await loadRoute();

    const response = await send(route, 12, {
      to: ['bob@example.com'],
      subject: 'Hi',
      text: 'Body',
    });

    expect(response.status).toBe(502);
    const body = await response.json();
    expect(body.error_code).toBe('MAILBOX.DELIVERY_FAILED');
    expect(body.message).not.toContain('provider said no');
  });
});

async function loadRoute(): Promise<MailboxRoute> {
  const route = await import('@/features/mailbox/server/mailbox-route');
  const store = await import('@/features/mailbox/server/mock-mailbox-store');
  store.mockMailboxStore.reset();
  return route;
}

function create(route: MailboxRoute, body: Record<string, unknown>) {
  return route.createMailboxRoute(
    request('/api/mailboxes', { method: 'POST', body: JSON.stringify(body) })
  );
}

function send(route: MailboxRoute, mailboxId: number, body: Record<string, unknown>) {
  return route.sendMailMessageRoute(
    request(`/api/mailboxes/${mailboxId}/messages`, { method: 'POST', body: JSON.stringify(body) }),
    String(mailboxId)
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

function request(
  path: string,
  options: { method?: string; body?: string; origin?: string } = {}
): Request {
  const result = new Request(`https://app.example.com${path}`, {
    method: options.method ?? 'GET',
    body: options.body,
  });
  if (options.body) result.headers.set('content-type', 'application/json');
  if (options.method && options.method !== 'GET') {
    result.headers.set('origin', options.origin ?? 'https://app.example.com');
    result.headers.set('sec-fetch-site', options.origin ? 'cross-site' : 'same-origin');
  }
  return result;
}

function mockUser(id: string) {
  return { id, email: `${id}@example.com`, name: id };
}
