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

describe('AI triage browser routes', () => {
  let fetchMock: ReturnType<typeof vi.fn<typeof fetch>>;

  beforeEach(() => {
    vi.resetModules();
    fetchMock = vi.fn<typeof fetch>();
    vi.stubGlobal('fetch', fetchMock);
    cookieStore.get.mockReset();
    getSessionUser.mockReset();
    process.env.NEXT_PUBLIC_API_URL = '/api';
    process.env.NEXT_PUBLIC_APP_URL = 'https://app.example.com';
    process.env.NEXT_PUBLIC_OPTIONAL_FEATURES = 'organization,mailbox';
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

  it('sorts mock mail with the fake provider rules', async () => {
    const { fakeTriage } = await import('@/features/mailbox/server/mock-triage-store');
    expect(fakeTriage('Can you send the deck?').category).toBe('reply');
    expect(fakeTriage('\u80fd\u4e0d\u80fd\u4eca\u5929\u53d1\uff1f').category).toBe('reply');
    expect(fakeTriage('Please sign by 2026-12-01').category).toBe('deadline');
    expect(fakeTriage('\u8bf712\u67081\u65e5\u524d\u7b7e\u5b57').category).toBe('deadline');
    expect(fakeTriage('This week in review.')).toMatchObject({ category: 'fyi', source: 'ai' });
  });

  it('rejects cross-origin triage writes before authentication or body parsing', async () => {
    process.env.MOCK_BFF_ENABLED = 'true';
    const route = await loadRoute();
    const options = { body: '{', origin: 'https://evil.example' };

    const responses = await Promise.all([
      route.updateMailTriageSettingRoute(
        request('/api/mail-triage-setting', { ...options, method: 'PUT' })
      ),
      route.setMailboxTriageSettingRoute(
        request('/api/mailboxes/1/triage-setting', { ...options, method: 'PUT' }),
        '1'
      ),
      route.updateMailMessageTriageRoute(
        request('/api/mailboxes/1/messages/1/triage', { ...options, method: 'PUT' }),
        '1',
        '1'
      ),
      route.createMailReplyDraftRoute(
        request('/api/mailboxes/1/messages/1/reply-drafts', { ...options, method: 'POST' }),
        '1',
        '1'
      ),
    ]);

    for (const response of responses) expect(response.status).toBe(403);
    expect(getSessionUser).not.toHaveBeenCalled();
  });

  it('hides triage until the organization turns it on, then sorts, counts, and filters', async () => {
    process.env.MOCK_BFF_ENABLED = 'true';
    getSessionUser.mockResolvedValue(mockUser('demo-admin'));
    const route = await loadRoute();
    const mailbox = await createMailbox(route, 'triage');

    // Off by default: the setting says so, rows carry null, counts carry null.
    const setting = await data(
      await route.getMailTriageSettingRoute(request('/api/mail-triage-setting'))
    );
    expect(setting).toMatchObject({ available: true, enabled: false, updated_at: null });
    expect((await inboxItems(route))[0].triage).toBeNull();
    expect((await counts(route)).triage).toBeNull();
    // A triage filter while triage is off matches nothing.
    expect(await inboxItems(route, '?triage=fyi')).toEqual([]);

    await expectStatus(
      await route.updateMailTriageSettingRoute(
        request('/api/mail-triage-setting', {
          method: 'PUT',
          body: JSON.stringify({ enabled: true }),
        })
      ),
      422,
      'COMMON.VALIDATION_FAILED'
    );
    await turnOn(route);
    // The organization is on but the mailbox is not: still no result on its mail.
    expect((await inboxItems(route))[0].triage).toBeNull();
    expect((await counts(route)).triage).toEqual({ reply: 0, deadline: 0, fyi: 0, review: 0 });

    const switched = await data(
      await route.setMailboxTriageSettingRoute(
        request(`/api/mailboxes/${mailbox}/triage-setting`, {
          method: 'PUT',
          body: JSON.stringify({ enabled: true }),
        }),
        String(mailbox)
      )
    );
    expect(switched).toMatchObject({ id: mailbox, ai_triage_enabled: true });

    const [welcome] = await inboxItems(route);
    expect(welcome.triage).toMatchObject({ category: 'fyi', source: 'ai' });
    expect(welcome.triage.reason).not.toBe('');
    expect((await counts(route)).triage).toEqual({ reply: 0, deadline: 0, fyi: 1, review: 0 });
    expect((await inboxItems(route, '?triage=fyi')).map(item => item.id)).toEqual([welcome.id]);
    expect(await inboxItems(route, '?triage=reply,review')).toEqual([]);

    // Bad filters are refused before they reach a backend.
    for (const query of [
      '?triage=spam',
      '?triage=reply,reply',
      '?triage=reply&group=thread',
      '?triage=reply&view=sent',
      '?triage=reply&q=hello',
    ]) {
      expect(
        (await route.listInboxMessagesRoute(request(`/api/inbox-messages${query}`))).status
      ).toBe(400);
    }
  });

  it('lets a reader override and a sender draft, and keeps members off the switches', async () => {
    process.env.MOCK_BFF_ENABLED = 'true';
    getSessionUser.mockResolvedValue(mockUser('demo-admin'));
    const route = await loadRoute();
    const mailbox = await createMailbox(route, 'override');
    await turnOn(route);
    await route.setMailboxTriageSettingRoute(
      request(`/api/mailboxes/${mailbox}/triage-setting`, {
        method: 'PUT',
        body: JSON.stringify({ enabled: true }),
      }),
      String(mailbox)
    );
    const [welcome] = await inboxItems(route);
    const triagePath = `/api/mailboxes/${mailbox}/messages/${welcome.id}/triage`;
    const override = (body: Record<string, unknown>) =>
      route.updateMailMessageTriageRoute(
        request(triagePath, { method: 'PUT', body: JSON.stringify(body) }),
        String(mailbox),
        String(welcome.id)
      );

    expect(await data(await override({ category: 'reply' }))).toEqual({
      category: 'reply',
      reason: '',
      due_at: null,
      source: 'manual',
    });
    expect((await inboxItems(route, '?triage=reply'))[0]).toMatchObject({
      id: welcome.id,
      triage: { category: 'reply', source: 'manual' },
    });
    expect((await counts(route)).triage).toMatchObject({ reply: 1, fyi: 0 });
    await expectStatus(await override({ category: 'review' }), 422, 'COMMON.VALIDATION_FAILED');
    await expectStatus(
      await override({ category: 'fyi', due_at: '2026-12-01T00:00:00Z' }),
      422,
      'COMMON.VALIDATION_FAILED'
    );

    const draftOf = () =>
      route.createMailReplyDraftRoute(
        request(`/api/mailboxes/${mailbox}/messages/${welcome.id}/reply-drafts`, {
          method: 'POST',
        }),
        String(mailbox),
        String(welcome.id)
      );
    const draft = await data(await draftOf());
    expect(draft.body).toContain('Welcome to your mock mailbox');
    expect(draft.basis).toEqual([
      { kind: 'message', id: welcome.id, label: 'Welcome to your mock mailbox' },
    ]);

    // A member may not change the organization switches.
    getSessionUser.mockResolvedValue(mockUser('demo-member'));
    await expectStatus(
      await route.updateMailTriageSettingRoute(
        request('/api/mail-triage-setting', {
          method: 'PUT',
          body: JSON.stringify({ enabled: false, daily_message_cap: 10, draft_enabled: false }),
        })
      ),
      403,
      'PERMISSION.DENIED'
    );

    // With drafts off, a draft is refused as unavailable.
    getSessionUser.mockResolvedValue(mockUser('demo-admin'));
    await route.updateMailTriageSettingRoute(
      request('/api/mail-triage-setting', {
        method: 'PUT',
        body: JSON.stringify({ enabled: true, daily_message_cap: 10, draft_enabled: false }),
      })
    );
    await expectStatus(await draftOf(), 409, 'MAILBOX.TRIAGE_UNAVAILABLE');
  });

  it('forwards the triage routes to the API with only the documented fields', async () => {
    process.env.API_ADAPTER_ENABLED = 'true';
    process.env.API_UPSTREAM_URL = 'https://api.example.com/v1';
    cookieStore.get.mockReturnValue({ value: 'A'.repeat(43) });
    const route = await loadRoute();
    const ok = (payload: unknown) => Response.json({ code: 0, message: 'success', data: payload });

    fetchMock.mockResolvedValueOnce(
      ok({ category: 'deadline', reason: '', due_at: null, source: 'manual' })
    );
    const override = await route.updateMailMessageTriageRoute(
      request('/api/mailboxes/12/messages/34/triage', {
        method: 'PUT',
        body: JSON.stringify({ category: 'deadline', due_at: '2026-12-01T00:00:00Z', extra: 1 }),
      }),
      '12',
      '34'
    );
    expect(override.status).toBe(200);
    expect(override.headers.get('cache-control')).toBe('private, no-store');
    let [url, init] = fetchMock.mock.calls[0] ?? [];
    expect(String(url)).toBe('https://api.example.com/v1/mailboxes/12/messages/34/triage');
    expect(init?.method).toBe('PUT');
    expect(JSON.parse(String(init?.body))).toEqual({
      category: 'deadline',
      due_at: '2026-12-01T00:00:00Z',
    });

    fetchMock.mockResolvedValueOnce(ok({ body: 'Hi', basis: [] }));
    const draft = await route.createMailReplyDraftRoute(
      request('/api/mailboxes/12/messages/34/reply-drafts', { method: 'POST' }),
      '12',
      '34'
    );
    expect(draft.status).toBe(200);
    [url, init] = fetchMock.mock.calls[1] ?? [];
    expect(String(url)).toBe('https://api.example.com/v1/mailboxes/12/messages/34/reply-drafts');
    expect(init?.method).toBe('POST');
    expect(init?.body ?? null).toBeNull();

    fetchMock.mockResolvedValueOnce(
      Response.json(
        { code: 502, error_code: 'MAILBOX.TRIAGE_MODEL_FAILED', message: 'provider said no' },
        { status: 502 }
      )
    );
    await expectStatus(
      await route.createMailReplyDraftRoute(
        request('/api/mailboxes/12/messages/34/reply-drafts', { method: 'POST' }),
        '12',
        '34'
      ),
      502,
      'MAILBOX.TRIAGE_MODEL_FAILED'
    );

    fetchMock.mockResolvedValueOnce(ok({ data: [] }));
    await route.listInboxMessagesRoute(request('/api/inbox-messages?triage=reply,review'));
    [url] = fetchMock.mock.calls[3] ?? [];
    expect(new URL(String(url)).searchParams.get('triage')).toBe('reply,review');

    // A malformed body never leaves the BFF.
    const calls = fetchMock.mock.calls.length;
    await expectStatus(
      await route.updateMailTriageSettingRoute(
        request('/api/mail-triage-setting', {
          method: 'PUT',
          body: JSON.stringify({ enabled: true, daily_message_cap: 0, draft_enabled: true }),
        })
      ),
      422,
      'COMMON.VALIDATION_FAILED'
    );
    expect(fetchMock.mock.calls.length).toBe(calls);
  });
});

async function loadRoute(): Promise<MailboxRoute> {
  const route = await import('@/features/mailbox/server/mailbox-route');
  const store = await import('@/features/mailbox/server/mock-mailbox-store');
  store.mockMailboxStore.reset();
  const triage = await import('@/features/mailbox/server/mock-triage-store');
  triage.mockTriageStore.reset();
  return route;
}

/** A shared mailbox every current member manages; it starts with one unread welcome message. */
async function createMailbox(route: MailboxRoute, localPart: string): Promise<number> {
  const { mockOrganizationStore } =
    await import('@/features/organization/server/mock-organization-store');
  const members = mockOrganizationStore
    .memberUserIds(1)
    .map(userId => ({ user_id: userId, permission: 'manage' }));
  const response = await route.createMailboxRoute(
    request('/api/mailboxes', {
      method: 'POST',
      body: JSON.stringify({ local_part: localPart, type: 'shared', members }),
    })
  );
  return (await data(response)).id;
}

async function turnOn(route: MailboxRoute) {
  const response = await route.updateMailTriageSettingRoute(
    request('/api/mail-triage-setting', {
      method: 'PUT',
      body: JSON.stringify({ enabled: true, daily_message_cap: 100, draft_enabled: true }),
    })
  );
  expect(await data(response)).toMatchObject({ enabled: true, draft_enabled: true });
}

// eslint-disable-next-line @typescript-eslint/no-explicit-any
async function inboxItems(route: MailboxRoute, query = ''): Promise<any[]> {
  return data(await route.listInboxMessagesRoute(request(`/api/inbox-messages${query}`)));
}

// eslint-disable-next-line @typescript-eslint/no-explicit-any
async function counts(route: MailboxRoute): Promise<any> {
  return data(await route.getInboxMessageCountsRoute(request('/api/inbox-message-counts')));
}

// eslint-disable-next-line @typescript-eslint/no-explicit-any
async function data(response: Response): Promise<any> {
  expect(response.status).toBeLessThan(300);
  expect(response.headers.get('cache-control')).toBe('private, no-store');
  return (await response.json()).data;
}

async function expectStatus(response: Response, status: number, errorCode: string) {
  const body = await response.json();
  expect({ status: response.status, error_code: body.error_code }).toEqual({
    status,
    error_code: errorCode,
  });
}

function request(path: string, options: { method?: string; body?: string; origin?: string } = {}) {
  const result = new Request(`https://app.example.com${path}`, {
    method: options.method ?? 'GET',
    body: options.body,
  });
  result.headers.set('organization-id', '1');
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
