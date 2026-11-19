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

/** The `Organization-Id` every request of a test carries unless it names another. */
let activeOrganization = '1';

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
    process.env.NEXT_PUBLIC_OPTIONAL_FEATURES = 'organization,mailbox';
    process.env.API_UPSTREAM_TIMEOUT_MS = '5000';
    activeOrganization = '1';
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

  it('is selected only together with the organization feature', () => {
    expect(() => parseOptionalWebFeatures('mailbox')).toThrow(
      'Optional Web feature "mailbox" requires "organization"'
    );
    expect(parseOptionalWebFeatures('organization,mailbox')).toEqual(['organization', 'mailbox']);
  });

  it('stays unavailable when the feature is not selected', async () => {
    process.env.MOCK_BFF_ENABLED = 'true';
    getSessionUser.mockResolvedValue(mockUser('demo-admin'));

    // Neither feature, and the organization feature without the mailbox feature.
    for (const features of ['', 'organization']) {
      vi.resetModules();
      process.env.NEXT_PUBLIC_OPTIONAL_FEATURES = features;
      const route = await loadRoute();

      const read = await route.listMailboxesRoute(request('/api/mailboxes'));
      const write = await create(route, { local_part: 'hello' });

      for (const response of [read, write]) {
        expect(response.status).toBe(503);
        await expect(response.json()).resolves.toMatchObject({
          error_code: 'COMMON.SERVICE_UNAVAILABLE',
        });
      }
    }
    expect(getSessionUser).not.toHaveBeenCalled();
  });

  it('stays unavailable in production without the adapter or an explicit mock', async () => {
    const route = await loadRoute();
    const base = {
      mailboxFeatureEnabled: true,
      organizationFeatureEnabled: true,
      adapterEnabled: false,
      nodeEnv: 'production' as const,
    };

    expect(route.resolveMailboxRoute({ ...base, mockBffEnabled: false }).available).toBe(false);
    expect(
      route.resolveMailboxRoute({ ...base, adapterEnabled: true, mockBffEnabled: false })
    ).toEqual({ available: true, backend: 'go-api' });
    expect(
      route.resolveMailboxRoute({
        ...base,
        mailboxFeatureEnabled: false,
        adapterEnabled: true,
        mockBffEnabled: true,
      }).available
    ).toBe(false);
    // The mailbox feature needs the organization feature, whatever the backend.
    expect(
      route.resolveMailboxRoute({
        ...base,
        organizationFeatureEnabled: false,
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
      data: [
        {
          domain: 'mail.example.com',
          kind: 'shared',
          max_mailboxes: 50,
          provider: 'log',
          inbound_enabled: false,
          daily_send_limit: 200,
          broadcasts_enabled: true,
          custom_domains_enabled: true,
        },
      ],
    });

    const created = await create(route, {
      local_part: '  Hello.Team ',
      display_name: ' Hello   Team ',
    });
    expect(created.status).toBe(201);
    expect(created.headers.get('cache-control')).toBe('private, no-store');
    expect(created.headers.get('vary')).toContain('Cookie');
    expect(created.headers.get('vary')).toContain('Organization-Id');
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
    // Another organization cannot take the address either.
    activeOrganization = await secondOrganization();
    await expectError(await create(route, { local_part: 'box0' }), 409, 'MAILBOX.ADDRESS_TAKEN');

    activeOrganization = '1';
    for (let index = 1; index <= 50; index += 1) {
      expect((await create(route, { local_part: `box${index}` })).status).toBe(201);
    }
    await expectError(await create(route, { local_part: 'box51' }), 409, 'MAILBOX.LIMIT_REACHED');

    // The limit is per organization: a full one leaves the other free to create.
    activeOrganization = await secondOrganization();
    expect((await create(route, { local_part: 'elsewhere' })).status).toBe(201);
  });

  it("reports another organization's mailbox and messages as missing", async () => {
    process.env.MOCK_BFF_ENABLED = 'true';
    getSessionUser.mockResolvedValue(mockUser('demo-admin'));
    const route = await loadRoute();
    const mailbox = (await (await create(route, { local_part: 'private' })).json()).data;
    const path = `/api/mailboxes/${mailbox.id}/messages`;

    // The same user, acting in an organization that does not own the mailbox.
    activeOrganization = await secondOrganization();
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

    // Nothing was changed from the other organization.
    activeOrganization = '1';
    const list = await (await route.listMailboxesRoute(request('/api/mailboxes'))).json();
    expect(list.data.map((item: { id: number }) => item.id)).toEqual([mailbox.id]);
    const messages = await (
      await route.listMailMessagesRoute(request(path), String(mailbox.id))
    ).json();
    expect(messages.meta.total).toBe(1);
  });

  it('requires a well-formed organization header after authentication', async () => {
    process.env.MOCK_BFF_ENABLED = 'true';
    getSessionUser.mockResolvedValue(mockUser('demo-admin'));
    const route = await loadRoute();
    const calls = (organization: string | string[] | null) => [
      () => route.listMailboxDomainsRoute(request('/api/mailbox-domains', { organization })),
      () => route.listMailboxesRoute(request('/api/mailboxes', { organization })),
      () => route.listMailLogRoute(request('/api/mail-messages', { organization })),
      () => route.listMailMessageStatsRoute(request('/api/mail-message-stats', { organization })),
      () =>
        route.listMailMessagesRoute(request('/api/mailboxes/1/messages', { organization }), '1'),
      () =>
        route.getMailMessageRoute(
          request('/api/mailboxes/1/messages/1', { organization }),
          '1',
          '1'
        ),
      () =>
        route.createMailboxRoute(
          request('/api/mailboxes', { method: 'POST', body: '{"local_part":"x"}', organization })
        ),
      () =>
        route.deleteMailboxRoute(
          request('/api/mailboxes/1', { method: 'DELETE', organization }),
          '1'
        ),
      () =>
        route.sendMailMessageRoute(
          request('/api/mailboxes/1/messages', { method: 'POST', body: '{}', organization }),
          '1'
        ),
      () =>
        route.updateMailMessageRoute(
          request('/api/mailboxes/1/messages/1', { method: 'PUT', body: '{}', organization }),
          '1',
          '1'
        ),
      () =>
        route.deleteMailMessageRoute(
          request('/api/mailboxes/1/messages/1', { method: 'DELETE', organization }),
          '1',
          '1'
        ),
    ];

    for (const call of calls(null)) {
      const response = await call();
      await expectError(response, 400, 'ORGANIZATION.CONTEXT_REQUIRED');
      expect(response.headers.get('vary')).toContain('Organization-Id');
    }
    for (const organization of ['', '0', '-1', '1.5', 'abc', '01', ['1', '1'], ['1', '2']]) {
      for (const call of calls(organization)) {
        await expectError(await call(), 400, 'ORGANIZATION.CONTEXT_INVALID');
      }
    }
    // An organization that does not exist and one the caller does not belong to look the same.
    const foreign = await secondOrganization('demo-operator');
    getSessionUser.mockResolvedValue(mockUser('demo-member'));
    for (const organization of ['999', foreign]) {
      for (const call of calls(organization)) {
        await expectError(await call(), 404, 'ORGANIZATION.NOT_FOUND');
      }
    }

    // A signed-out caller learns nothing about the header.
    getSessionUser.mockResolvedValue(null);
    for (const call of [...calls(null), ...calls('abc'), ...calls('999')]) {
      await expectError(await call(), 401, 'AUTH.UNAUTHORIZED');
    }
  });

  it('lets only owners and admins create and delete mailboxes', async () => {
    process.env.MOCK_BFF_ENABLED = 'true';
    const route = await loadRoute();
    const remove = (id: number) =>
      route.deleteMailboxRoute(request(`/api/mailboxes/${id}`, { method: 'DELETE' }), String(id));
    const listed = async () =>
      (await (await route.listMailboxesRoute(request('/api/mailboxes'))).json()).data.map(
        (item: { address: string }) => item.address
      );

    getSessionUser.mockResolvedValue(mockUser('demo-admin'));
    const owned = (await (await create(route, { local_part: 'owner-box' })).json()).data;
    getSessionUser.mockResolvedValue(mockUser('demo-operator'));
    const managed = await create(route, { local_part: 'admin-box' });
    expect(managed.status).toBe(201);
    const adminBox = (await managed.json()).data;

    // A member is refused before the body or the mailbox is looked at, and changes nothing.
    getSessionUser.mockResolvedValue(mockUser('demo-member'));
    await expectError(await create(route, { local_part: 'member-box' }), 403, 'PERMISSION.DENIED');
    await expectError(await create(route, {}), 403, 'PERMISSION.DENIED');
    // Deleting needs `administer`: a member with access is refused, an unknown mailbox is missing.
    await expectError(await remove(owned.id), 403, 'PERMISSION.DENIED');
    await expectError(await remove(999), 404, 'MAILBOX.NOT_FOUND');
    expect(await listed()).toEqual(['owner-box@mail.example.com', 'admin-box@mail.example.com']);

    // As a `manage` member the member still reads, sends, marks, and deletes messages.
    const path = `/api/mailboxes/${owned.id}/messages`;
    const inbox = await (
      await route.listMailMessagesRoute(request(`${path}?direction=inbound`), String(owned.id))
    ).json();
    const welcomeId = String(inbox.data[0].id);
    const sent = await send(route, owned.id, {
      to: ['bob@example.com'],
      subject: 'From a member',
      text: 'Body',
    });
    expect(sent.status).toBe(201);
    const marked = await route.updateMailMessageRoute(
      request(`${path}/${welcomeId}`, { method: 'PUT', body: JSON.stringify({ read: true }) }),
      String(owned.id),
      welcomeId
    );
    expect(marked.status).toBe(200);
    expect((await route.listMailLogRoute(request('/api/mail-messages'))).status).toBe(200);
    expect((await route.listMailMessageStatsRoute(request('/api/mail-message-stats'))).status).toBe(
      200
    );
    expect((await route.listMailboxDomainsRoute(request('/api/mailbox-domains'))).status).toBe(200);

    // Read state is per person: what the member read is still unread for the owner.
    getSessionUser.mockResolvedValue(mockUser('demo-admin'));
    const seen = await route.getMailMessageRoute(
      request(`${path}/${welcomeId}`),
      String(owned.id),
      welcomeId
    );
    await expect(seen.json()).resolves.toMatchObject({ data: { read: false } });

    getSessionUser.mockResolvedValue(mockUser('demo-member'));
    const removedMessage = await route.deleteMailMessageRoute(
      request(`${path}/${welcomeId}`, { method: 'DELETE' }),
      String(owned.id),
      welcomeId
    );
    expect(removedMessage.status).toBe(204);

    // An admin deletes a mailbox the owner created, and the owner one the admin created.
    getSessionUser.mockResolvedValue(mockUser('demo-operator'));
    expect((await remove(owned.id)).status).toBe(204);
    getSessionUser.mockResolvedValue(mockUser('demo-admin'));
    expect((await remove(adminBox.id)).status).toBe(204);
    expect(await listed()).toEqual([]);
  });

  it('keeps the mailboxes, log, and statistics of two organizations apart', async () => {
    process.env.MOCK_BFF_ENABLED = 'true';
    getSessionUser.mockResolvedValue(mockUser('demo-admin'));
    const route = await loadRoute();
    const second = await secondOrganization();
    const addresses = async () =>
      (await (await route.listMailboxesRoute(request('/api/mailboxes'))).json()).data.map(
        (item: { address: string }) => item.address
      );
    const logTotal = async () =>
      (await (await route.listMailLogRoute(request('/api/mail-messages'))).json()).meta.total;
    const received = async () =>
      (
        await (await route.listMailMessageStatsRoute(request('/api/mail-message-stats'))).json()
      ).data.reduce((sum: number, day: { received: number }) => sum + day.received, 0);

    const first = (await (await create(route, { local_part: 'first' })).json()).data;
    await send(route, first.id, { to: ['bob@example.com'], subject: 'One', text: 'Body' });
    activeOrganization = second;
    const other = (await (await create(route, { local_part: 'second' })).json()).data;

    expect(await addresses()).toEqual(['second@mail.example.com']);
    expect(await logTotal()).toBe(1);
    expect(await received()).toBe(1);
    await expectError(
      await send(route, first.id, { to: ['bob@example.com'], subject: 'Two', text: 'Body' }),
      404,
      'MAILBOX.NOT_FOUND'
    );

    activeOrganization = '1';
    expect(await addresses()).toEqual(['first@mail.example.com']);
    expect(await logTotal()).toBe(2);
    await expectError(
      await route.listMailMessagesRoute(
        request(`/api/mailboxes/${other.id}/messages`),
        String(other.id)
      ),
      404,
      'MAILBOX.NOT_FOUND'
    );

    // A member of the first organization only does not reach the second one at all.
    getSessionUser.mockResolvedValue(mockUser('demo-member'));
    expect(await addresses()).toEqual(['first@mail.example.com']);
    await expectError(
      await route.listMailboxesRoute(request('/api/mailboxes', { organization: second })),
      404,
      'ORGANIZATION.NOT_FOUND'
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
    for (let index = 0; index < 50; index += 1) {
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
    // Inbound mail has no delivery events; listings carry the summary and never the events.
    expect(inbox.data[0].last_event).toBe('');
    expect(inbox.data[0]).not.toHaveProperty('events');
    const welcomeId = String(inbox.data[0].id);

    const read = await route.getMailMessageRoute(request(`${path}/${welcomeId}`), id, welcomeId);
    expect(read.status).toBe(200);
    const welcome = (await read.json()).data;
    expect(welcome.text_body).not.toBe('');
    expect(welcome).toMatchObject({ last_event: '', events: [] });
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
      queued_reason: null,
      in_reply_to: welcome.message_id,
      last_event: 'delivered',
    });
    // The mock provider accepts the message and delivers to every recipient at once.
    expect(sent.events).toEqual([
      { type: 'accepted', recipient: '', detail: '', occurred_at: sent.created_at },
      { type: 'delivered', recipient: 'bob@example.com', detail: '', occurred_at: sent.created_at },
      {
        type: 'delivered',
        recipient: 'carol@example.com',
        detail: '',
        occurred_at: sent.created_at,
      },
    ]);
    const stored = (
      await (
        await route.getMailMessageRoute(request(`${path}/${sent.id}`), id, String(sent.id))
      ).json()
    ).data;
    expect(stored.events).toEqual(sent.events);
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
    expect(page.data[0].last_event).toBe('delivered');
    expect(page.data[0]).not.toHaveProperty('events');
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

  it('stores attachments in the mock and serves them only as opaque downloads', async () => {
    process.env.MOCK_BFF_ENABLED = 'true';
    getSessionUser.mockResolvedValue(mockUser('demo-admin'));
    const route = await loadRoute();
    const mailbox = (await (await create(route, { local_part: 'hello' })).json()).data;
    const html = '<script>alert(1)</script>';
    const sent = await send(route, mailbox.id, {
      to: ['bob@example.com'],
      subject: 'Files',
      text: 'See attached.',
      attachments: [
        { filename: '../résumé.html', content_type: 'text/html', content: base64(html) },
        { filename: 'data.bin', content: base64('x') },
      ],
    });
    expect(sent.status).toBe(201);
    const message = (await sent.json()).data;
    expect(message.attachment_count).toBe(2);
    expect(message.attachments).toEqual([
      { id: expect.any(Number), filename: 'résumé.html', content_type: 'text/html', size: 25 },
      {
        id: expect.any(Number),
        filename: 'data.bin',
        content_type: 'application/octet-stream',
        size: 1,
      },
    ]);
    const outbound = await (
      await route.listMailMessagesRoute(
        request(`/api/mailboxes/${mailbox.id}/messages?direction=outbound`),
        String(mailbox.id)
      )
    ).json();
    expect(outbound.data[0]).toMatchObject({ attachment_count: 2 });
    expect(outbound.data[0].attachments).toBeUndefined();

    const path = `/api/mailboxes/${mailbox.id}/messages/${message.id}/attachments`;
    const file = message.attachments[0];
    const download = await route.downloadMailAttachmentRoute(
      request(`${path}/${file.id}`),
      String(mailbox.id),
      String(message.id),
      String(file.id)
    );
    expect(download.status).toBe(200);
    expect(await download.text()).toBe(html);
    expect(download.headers.get('content-type')).toBe('application/octet-stream');
    expect(download.headers.get('x-content-type-options')).toBe('nosniff');
    expect(download.headers.get('cache-control')).toBe('private, no-store');
    expect(download.headers.get('content-disposition')).toBe(
      `attachment; filename*=utf-8''${encodeURIComponent('résumé.html')}`
    );

    const missing = await route.downloadMailAttachmentRoute(
      request(`${path}/999`),
      String(mailbox.id),
      String(message.id),
      '999'
    );
    await expectError(missing, 404, 'MAILBOX.MESSAGE_NOT_FOUND');
    const malformed = await route.downloadMailAttachmentRoute(
      request(`${path}/abc`),
      String(mailbox.id),
      String(message.id),
      'abc'
    );
    expect(malformed.status).toBe(400);

    activeOrganization = await secondOrganization();
    const stranger = await route.downloadMailAttachmentRoute(
      request(`${path}/${file.id}`),
      String(mailbox.id),
      String(message.id),
      String(file.id)
    );
    await expectError(stranger, 404, 'MAILBOX.NOT_FOUND');
  });

  it('validates attachments like the API', async () => {
    process.env.MOCK_BFF_ENABLED = 'true';
    getSessionUser.mockResolvedValue(mockUser('demo-admin'));
    const route = await loadRoute();
    const mailbox = (await (await create(route, { local_part: 'hello' })).json()).data;
    const valid = { to: ['bob@example.com'], subject: 'Hi', text: 'Body' };
    const half = base64('x'.repeat(2 * 1_024 * 1_024 + 1));

    for (const attachments of [
      Array.from({ length: 11 }, () => ({ filename: 'a.txt', content: base64('a') })),
      [{ filename: 'a.txt', content: 'not base64!' }],
      [{ filename: ' / ', content: base64('a') }],
      [
        { filename: 'a.bin', content: half },
        { filename: 'b.bin', content: half },
      ],
    ]) {
      const response = await send(route, mailbox.id, { ...valid, attachments });
      const body = await response.json();
      expect([response.status, body.error_code]).toEqual(
        attachments.length > 10
          ? [422, 'COMMON.VALIDATION_FAILED']
          : [422, 'MAILBOX.MESSAGE_INVALID']
      );
    }
    for (const attachments of [
      'a.txt',
      [{ filename: 'a.txt' }],
      [{ filename: '', content: base64('a') }],
      [{ filename: 'a.txt', content: base64('a'), path: '/etc' }],
    ]) {
      await expectError(
        await send(route, mailbox.id, { ...valid, attachments }),
        422,
        'COMMON.VALIDATION_FAILED'
      );
    }
  });

  it('forwards an attachment download as opaque bytes from the API', async () => {
    process.env.API_ADAPTER_ENABLED = 'true';
    process.env.API_UPSTREAM_URL = 'https://api.example.com/v1';
    cookieStore.get.mockReturnValue({ value: 'A'.repeat(43) });
    fetchMock.mockResolvedValueOnce(
      new Response(new Uint8Array([0, 1, 2, 255]), {
        headers: {
          'content-type': 'text/html',
          'content-disposition': "attachment; filename*=utf-8''a.pdf",
        },
      })
    );
    const route = await loadRoute();

    const response = await route.downloadMailAttachmentRoute(
      request('/api/mailboxes/12/messages/34/attachments/56'),
      '12',
      '34',
      '56'
    );

    expect(response.status).toBe(200);
    expect(new Uint8Array(await response.arrayBuffer())).toEqual(new Uint8Array([0, 1, 2, 255]));
    expect(response.headers.get('content-type')).toBe('application/octet-stream');
    expect(response.headers.get('content-disposition')).toBe("attachment; filename*=utf-8''a.pdf");
    expect(response.headers.get('x-content-type-options')).toBe('nosniff');
    const [url, init] = fetchMock.mock.calls[0] ?? [];
    expect(String(url)).toBe('https://api.example.com/v1/mailboxes/12/messages/34/attachments/56');
    expect(new Headers(init?.headers).get('organization-id')).toBe('1');

    fetchMock.mockResolvedValueOnce(
      Response.json(
        { code: 404, error_code: 'MAILBOX.MESSAGE_NOT_FOUND', message: 'missing' },
        { status: 404 }
      )
    );
    await expectError(
      await route.downloadMailAttachmentRoute(
        request('/api/mailboxes/12/messages/34/attachments/57'),
        '12',
        '34',
        '57'
      ),
      404,
      'MAILBOX.MESSAGE_NOT_FOUND'
    );
  });

  it('keeps templates per organization in the mock and sends with them', async () => {
    process.env.MOCK_BFF_ENABLED = 'true';
    getSessionUser.mockResolvedValue(mockUser('demo-admin'));
    const route = await loadRoute();
    const mailbox = (await (await create(route, { local_part: 'hello' })).json()).data;
    const template = {
      name: ' Welcome   email ',
      subject: 'Welcome, {{first_name|friend}}',
      html: '<p>Hi {{first_name|friend}}, code {{code}}</p>',
      text: 'Code {{code}}',
      sample_variables: { first_name: 'Ann', code: '1' },
    };

    const created = await route.createMailTemplateRoute(
      request('/api/mail-templates', { method: 'POST', body: JSON.stringify(template) })
    );
    expect(created.status).toBe(201);
    const stored = (await created.json()).data;
    expect(stored).toMatchObject({
      name: 'Welcome email',
      variables: ['code', 'first_name'],
      sample_variables: { first_name: 'Ann', code: '1' },
      design: null,
    });
    const designed = await route.createMailTemplateRoute(
      request('/api/mail-templates', {
        method: 'POST',
        body: JSON.stringify({ ...template, name: 'Designed', design: { version: 1, blocks: [] } }),
      })
    );
    expect(designed.status).toBe(201);
    const designedTemplate = (await designed.json()).data;
    expect(designedTemplate.design).toEqual({ version: 1, blocks: [] });
    await route.deleteMailTemplateRoute(
      request(`/api/mail-templates/${designedTemplate.id}`, { method: 'DELETE' }),
      String(designedTemplate.id)
    );
    const duplicate = await route.createMailTemplateRoute(
      request('/api/mail-templates', { method: 'POST', body: JSON.stringify(template) })
    );
    await expectError(duplicate, 409, 'MAILBOX.TEMPLATE_NAME_TAKEN');
    const broken = await route.createMailTemplateRoute(
      request('/api/mail-templates', {
        method: 'POST',
        body: JSON.stringify({ ...template, name: 'Broken', html: '<p>{{first name}}</p>' }),
      })
    );
    await expectError(broken, 422, 'MAILBOX.TEMPLATE_INVALID');

    const list = await (await route.listMailTemplatesRoute(request('/api/mail-templates'))).json();
    expect(list.data).toEqual([expect.objectContaining({ id: stored.id, name: 'Welcome email' })]);
    expect(list.data[0].html_body).toBeUndefined();

    // A real send uses only the given values: first_name falls back, code is required.
    const missing = await send(route, mailbox.id, {
      to: ['bob@example.com'],
      template: { id: stored.id },
    });
    const missingBody = await missing.json();
    expect([missing.status, missingBody.error_code, missingBody.errors]).toEqual([
      422,
      'MAILBOX.TEMPLATE_VARIABLES_MISSING',
      { 'template.variables.code': ['is required'] },
    ]);
    const sent = await send(route, mailbox.id, {
      to: ['bob@example.com'],
      template: { id: stored.id, variables: { code: '<9>' } },
    });
    expect(sent.status).toBe(201);
    expect((await sent.json()).data).toMatchObject({
      subject: 'Welcome, friend',
      html_body: '<p>Hi friend, code &lt;9&gt;</p>',
      text_body: 'Code <9>',
      template_id: stored.id,
    });
    await expectError(
      await send(route, mailbox.id, {
        to: ['bob@example.com'],
        subject: 'Both',
        template: { id: stored.id, variables: { code: '1' } },
      }),
      422,
      'MAILBOX.MESSAGE_INVALID'
    );

    const test = await route.sendMailTemplateTestRoute(
      request(`/api/mail-templates/${stored.id}/tests`, {
        method: 'POST',
        body: JSON.stringify({ mailbox_id: mailbox.id, to: ['qa@example.com'] }),
      }),
      String(stored.id)
    );
    expect(test.status).toBe(201);
    expect((await test.json()).data).toMatchObject({
      subject: 'Welcome, Ann',
      text_body: 'Code 1',
    });

    const updated = await route.updateMailTemplateRoute(
      request(`/api/mail-templates/${stored.id}`, {
        method: 'PUT',
        body: JSON.stringify({ ...template, subject: 'Changed' }),
      }),
      String(stored.id)
    );
    expect((await updated.json()).data.subject).toBe('Changed');

    activeOrganization = await secondOrganization();
    await expectError(
      await route.getMailTemplateRoute(
        request(`/api/mail-templates/${stored.id}`),
        String(stored.id)
      ),
      404,
      'MAILBOX.TEMPLATE_NOT_FOUND'
    );
    activeOrganization = '1';

    const removed = await route.deleteMailTemplateRoute(
      request(`/api/mail-templates/${stored.id}`, { method: 'DELETE' }),
      String(stored.id)
    );
    expect(removed.status).toBe(204);
    await expectError(
      await route.deleteMailTemplateRoute(
        request(`/api/mail-templates/${stored.id}`, { method: 'DELETE' }),
        String(stored.id)
      ),
      404,
      'MAILBOX.TEMPLATE_NOT_FOUND'
    );
  });

  it('rejects malformed template bodies before they reach the backend', async () => {
    process.env.MOCK_BFF_ENABLED = 'true';
    getSessionUser.mockResolvedValue(mockUser('demo-admin'));
    const route = await loadRoute();
    const valid = { name: 'A', subject: 'S', html: '<p>x</p>' };
    for (const body of [
      { ...valid, name: '' },
      { ...valid, subject: 7 },
      { ...valid, sample_variables: { 'bad-name': 'x' } },
      { ...valid, sample_variables: { ok: 1 } },
      { ...valid, extra: true },
      { ...valid, design: [1] },
      { ...valid, design: { blocks: [] } },
      { ...valid, design: { version: 0 } },
      { ...valid, design: { version: 1, pad: 'x'.repeat(256 * 1_024) } },
    ]) {
      await expectError(
        await route.createMailTemplateRoute(
          request('/api/mail-templates', { method: 'POST', body: JSON.stringify(body) })
        ),
        422,
        'COMMON.VALIDATION_FAILED'
      );
    }
    for (const body of [
      { to: ['qa@example.com'] },
      { mailbox_id: 1, to: [] },
      { mailbox_id: 1, to: Array.from({ length: 6 }, (_, index) => `q${index}@example.com`) },
    ]) {
      await expectError(
        await route.sendMailTemplateTestRoute(
          request('/api/mail-templates/1/tests', { method: 'POST', body: JSON.stringify(body) }),
          '1'
        ),
        422,
        'COMMON.VALIDATION_FAILED'
      );
    }
    await expectError(
      await send(route, 1, { to: ['bob@example.com'], template: { id: 'one' } }),
      422,
      'COMMON.VALIDATION_FAILED'
    );
  });

  it('forwards template routes to the fixed API paths', async () => {
    process.env.API_ADAPTER_ENABLED = 'true';
    process.env.API_UPSTREAM_URL = 'https://api.example.com/v1';
    cookieStore.get.mockReturnValue({ value: 'A'.repeat(43) });
    fetchMock.mockImplementation(async () => Response.json({ code: 0, message: 'ok', data: [] }));
    const route = await loadRoute();
    const body = JSON.stringify({ name: 'A', subject: 'S', text: 'x' });

    await route.listMailTemplatesRoute(request('/api/mail-templates'));
    await route.createMailTemplateRoute(request('/api/mail-templates', { method: 'POST', body }));
    await route.getMailTemplateRoute(request('/api/mail-templates/3'), '3');
    await route.updateMailTemplateRoute(
      request('/api/mail-templates/3', { method: 'PUT', body }),
      '3'
    );
    await route.deleteMailTemplateRoute(
      request('/api/mail-templates/3', { method: 'DELETE' }),
      '3'
    );
    await route.sendMailTemplateTestRoute(
      request('/api/mail-templates/3/tests', {
        method: 'POST',
        body: JSON.stringify({ mailbox_id: 2, to: ['qa@example.com'] }),
      }),
      '3'
    );

    expect(
      fetchMock.mock.calls.map(([url, init]) => `${init?.method} ${new URL(String(url)).pathname}`)
    ).toEqual([
      'GET /v1/mail-templates',
      'POST /v1/mail-templates',
      'GET /v1/mail-templates/3',
      'PUT /v1/mail-templates/3',
      'DELETE /v1/mail-templates/3',
      'POST /v1/mail-templates/3/tests',
    ]);
    for (const [, init] of fetchMock.mock.calls) {
      expect(new Headers(init?.headers).get('organization-id')).toBe('1');
    }
  });

  it('keeps contacts and lists per organization in the mock', async () => {
    process.env.MOCK_BFF_ENABLED = 'true';
    getSessionUser.mockResolvedValue(mockUser('demo-admin'));
    const route = await loadRoute();
    const post = (path: string, body: unknown, method = 'POST') =>
      request(path, { method, body: JSON.stringify(body) });

    const vip = (
      await (
        await route.saveMailContactListRoute(post('/api/mail-contact-lists', { name: ' VIP ' }))
      ).json()
    ).data;
    expect(vip).toMatchObject({ name: 'VIP', contact_count: 0 });
    await expectError(
      await route.saveMailContactListRoute(post('/api/mail-contact-lists', { name: 'VIP' })),
      409,
      'MAILBOX.CONTACT_LIST_NAME_TAKEN'
    );

    const created = await route.createMailContactRoute(
      post('/api/mail-contacts', {
        email: ' Ann@Example.com ',
        first_name: ' Ann ',
        properties: { plan: 'pro' },
        list_ids: [vip.id],
      })
    );
    expect(created.status).toBe(201);
    const ann = (await created.json()).data;
    expect(ann).toMatchObject({
      email: 'ann@example.com',
      first_name: 'Ann',
      status: 'subscribed',
      list_ids: [vip.id],
    });
    await expectError(
      await route.createMailContactRoute(post('/api/mail-contacts', { email: 'ann@example.com' })),
      409,
      'MAILBOX.CONTACT_EXISTS'
    );
    await expectError(
      await route.createMailContactRoute(
        post('/api/mail-contacts', { email: 'Bob <b@example.com>' })
      ),
      422,
      'MAILBOX.CONTACT_INVALID'
    );
    await expectError(
      await route.createMailContactRoute(
        post('/api/mail-contacts', { email: 'c@example.com', list_ids: [999] })
      ),
      404,
      'MAILBOX.CONTACT_LIST_NOT_FOUND'
    );

    const updated = await route.updateMailContactRoute(
      post(
        `/api/mail-contacts/${ann.id}`,
        {
          email: 'x@example.com',
          first_name: 'Ann',
          status: 'unsubscribed',
          list_ids: [],
        },
        'PUT'
      ),
      String(ann.id)
    );
    expect((await updated.json()).data).toMatchObject({
      email: 'ann@example.com',
      status: 'unsubscribed',
      status_reason: 'manual',
      list_ids: [],
    });

    const imported = await route.importMailContactsRoute(
      post('/api/mail-contact-imports', {
        csv: 'Email,First Name,Plan,Phone #\nann@example.com,,team,1\nbad,,,\nbob@example.com,Bob,,\n',
        list_id: vip.id,
      })
    );
    expect((await imported.json()).data).toEqual({
      rows: 3,
      created: 1,
      updated: 1,
      skipped_count: 1,
      skipped: [{ line: 3, reason: 'invalid_email' }],
      ignored_columns: ['Phone #'],
    });
    await expectError(
      await route.importMailContactsRoute(
        post('/api/mail-contact-imports', { csv: 'name\nAnn\n' })
      ),
      422,
      'MAILBOX.CONTACT_IMPORT_INVALID'
    );

    const list = async (query: string) =>
      (
        await (await route.listMailContactsRoute(request(`/api/mail-contacts${query}`))).json()
      ).data.map((contact: { email: string }) => contact.email);
    expect(await list('')).toEqual(['bob@example.com', 'ann@example.com']);
    expect(await list(`?list_id=${vip.id}`)).toEqual(['bob@example.com', 'ann@example.com']);
    expect(await list('?status=unsubscribed')).toEqual(['ann@example.com']);
    expect(await list('?q=BOB')).toEqual(['bob@example.com']);
    const annAfter = (
      await (await route.listMailContactsRoute(request('/api/mail-contacts?q=ann'))).json()
    ).data[0];
    // The import kept the status and the first name, and merged the property.
    expect(annAfter).toMatchObject({
      status: 'unsubscribed',
      first_name: 'Ann',
      properties: { plan: 'team' },
    });
    await expectError(
      await route.listMailContactsRoute(request('/api/mail-contacts?status=bounced')),
      400,
      'COMMON.INVALID_INPUT'
    );

    const lists = (
      await (await route.listMailContactListsRoute(request('/api/mail-contact-lists'))).json()
    ).data;
    expect(lists).toEqual([expect.objectContaining({ id: vip.id, contact_count: 2 })]);

    activeOrganization = await secondOrganization();
    expect(await list('')).toEqual([]);
    await expectError(
      await route.deleteMailContactRoute(
        request(`/api/mail-contacts/${ann.id}`, { method: 'DELETE' }),
        String(ann.id)
      ),
      404,
      'MAILBOX.CONTACT_NOT_FOUND'
    );
    activeOrganization = '1';

    const removedList = await route.deleteMailContactListRoute(
      request(`/api/mail-contact-lists/${vip.id}`, { method: 'DELETE' }),
      String(vip.id)
    );
    expect(removedList.status).toBe(204);
    expect(await list('')).toHaveLength(2);
  });

  it('connects, verifies, and removes organization domains in the mock', async () => {
    process.env.MOCK_BFF_ENABLED = 'true';
    getSessionUser.mockResolvedValue(mockUser('demo-admin'));
    const route = await loadRoute();
    const connect = (name: unknown) =>
      route.connectMailDomainRoute(
        request('/api/mail-domains', { method: 'POST', body: JSON.stringify({ name }) })
      );
    const refresh = (id: number, action: 'setups' | 'checks') =>
      route.refreshMailDomainRoute(
        request(`/api/mail-domains/${id}/${action}`, { method: 'POST' }),
        String(id),
        action
      );
    const removeDomain = (id: number) =>
      route.deleteMailDomainRoute(
        request(`/api/mail-domains/${id}`, { method: 'DELETE' }),
        String(id)
      );

    const connected = await connect(' News.Example.org. ');
    expect(connected.status).toBe(201);
    const domain = (await connected.json()).data;
    // example.org is no zone the operator manages: the domain waits for a new managed zone.
    expect(domain).toMatchObject({
      name: 'news.example.org',
      status: 'pending',
      zone_name: 'example.org',
      capability: 'send_receive',
      zone: { name: 'example.org', status: 'pending' },
      zone_check_limited: false,
      records: [],
    });
    await expectError(await refresh(domain.id, 'setups'), 409, 'MAILBOX.ZONE_PENDING');

    // In a zone the operator manages, the setup runs at once and sending records still propagate.
    const operatorZone = (await (await connect('news.example.com')).json()).data;
    expect(operatorZone).toMatchObject({ zone_name: 'example.com', zone: null, status: 'pending' });
    expect(
      operatorZone.records.some((record: { status: string }) => record.status === 'missing')
    ).toBe(true);
    expect((await removeDomain(operatorZone.id)).status).toBe(204);

    await expectError(await connect('news.example.org'), 409, 'MAILBOX.DOMAIN_TAKEN');
    await expectError(await connect('mail.example.com'), 409, 'MAILBOX.DOMAIN_TAKEN');
    await expectError(await connect('not a domain'), 422, 'MAILBOX.DOMAIN_INVALID');
    await expectError(await connect('mail.example.invalid'), 422, 'MAILBOX.DOMAIN_ZONE_NOT_FOUND');
    await expectError(await connect(42), 422, 'COMMON.VALIDATION_FAILED');

    // A pending domain is not offered for mailboxes and refuses one.
    const offered = async () =>
      (
        await (await route.listMailboxDomainsRoute(request('/api/mailbox-domains'))).json()
      ).data.map((item: { domain: string; kind: string }) => `${item.kind}:${item.domain}`);
    expect(await offered()).toEqual(['shared:mail.example.com']);
    await expectError(
      await create(route, { local_part: 'hello', domain: 'news.example.org' }),
      422,
      'MAILBOX.DOMAIN_NOT_VERIFIED'
    );

    // The first check finds the delegation, activates the zone, and runs the setup.
    const checked = await refresh(domain.id, 'checks');
    expect((await checked.json()).data).toMatchObject({
      status: 'verified',
      zone: { status: 'active' },
    });
    expect(await offered()).toEqual(['shared:mail.example.com', 'organization:news.example.org']);
    const mailbox = await create(route, { local_part: 'hello', domain: 'news.example.org' });
    expect(mailbox.status).toBe(201);
    const created = (await mailbox.json()).data;
    expect(created.address).toBe('hello@news.example.org');

    // Another organization neither sees nor uses the domain.
    activeOrganization = await secondOrganization();
    expect(
      (await (await route.listMailDomainsRoute(request('/api/mail-domains'))).json()).data
    ).toEqual([]);
    await expectError(
      await create(route, { local_part: 'x', domain: 'news.example.org' }),
      404,
      'MAILBOX.DOMAIN_NOT_FOUND'
    );
    await expectError(await refresh(domain.id, 'setups'), 404, 'MAILBOX.DOMAIN_NOT_FOUND');
    activeOrganization = '1';

    await expectError(await removeDomain(domain.id), 409, 'MAILBOX.DOMAIN_IN_USE');
    await route.deleteMailboxRoute(
      request(`/api/mailboxes/${created.id}`, { method: 'DELETE' }),
      String(created.id)
    );
    expect((await removeDomain(domain.id)).status).toBe(204);
    expect(await offered()).toEqual(['shared:mail.example.com']);

    // Members read domains but never change them.
    getSessionUser.mockResolvedValue(mockUser('demo-member'));
    expect((await route.listMailDomainsRoute(request('/api/mail-domains'))).status).toBe(200);
    await expectError(await connect('other.example.org'), 403, 'PERMISSION.DENIED');
  });

  it('forwards domain routes to the fixed API paths', async () => {
    process.env.API_ADAPTER_ENABLED = 'true';
    process.env.API_UPSTREAM_URL = 'https://api.example.com/v1';
    cookieStore.get.mockReturnValue({ value: 'A'.repeat(43) });
    fetchMock.mockImplementation(async () => Response.json({ code: 0, message: 'ok', data: [] }));
    const route = await loadRoute();

    await route.listMailDomainsRoute(request('/api/mail-domains'));
    await route.connectMailDomainRoute(
      request('/api/mail-domains', {
        method: 'POST',
        body: JSON.stringify({ name: 'example.org' }),
      })
    );
    await route.getMailDomainRoute(request('/api/mail-domains/3'), '3');
    await route.refreshMailDomainRoute(
      request('/api/mail-domains/3/setups', { method: 'POST' }),
      '3',
      'setups'
    );
    await route.refreshMailDomainRoute(
      request('/api/mail-domains/3/checks', { method: 'POST' }),
      '3',
      'checks'
    );
    await route.deleteMailDomainRoute(request('/api/mail-domains/3', { method: 'DELETE' }), '3');
    await create(route, { local_part: 'hello', domain: 'example.org' });

    expect(
      fetchMock.mock.calls.map(([url, init]) => `${init?.method} ${new URL(String(url)).pathname}`)
    ).toEqual([
      'GET /v1/mail-domains',
      'POST /v1/mail-domains',
      'GET /v1/mail-domains/3',
      'POST /v1/mail-domains/3/setups',
      'POST /v1/mail-domains/3/checks',
      'DELETE /v1/mail-domains/3',
      'POST /v1/mailboxes',
    ]);
    expect(JSON.parse(String(fetchMock.mock.calls[1]?.[1]?.body))).toEqual({ name: 'example.org' });
    expect(JSON.parse(String(fetchMock.mock.calls[6]?.[1]?.body))).toEqual({
      local_part: 'hello',
      domain: 'example.org',
    });
  });

  it('creates a pending managed zone for an unknown apex and activates it on the first check', async () => {
    process.env.MOCK_BFF_ENABLED = 'true';
    getSessionUser.mockResolvedValue(mockUser('demo-admin'));
    const route = await loadRoute();

    const connected = await connectDomain(route, { name: 'example.org' });
    expect(connected.status).toBe(201);
    const domain = (await connected.json()).data;
    expect(domain.zone).toMatchObject({
      name: 'example.org',
      status: 'pending',
      name_servers: ['ada.ns.cloudflare.com', 'bob.ns.cloudflare.com'],
      activated_at: null,
    });
    const zoneId: number = domain.zone.id;

    const zones = await route.listMailZonesRoute(request('/api/mail-zones'));
    expect(zones.status).toBe(200);
    expect((await zones.json()).data).toEqual([domain.zone]);
    const zone = await route.getMailZoneRoute(request(`/api/mail-zones/${zoneId}`), String(zoneId));
    expect((await zone.json()).data).toEqual(domain.zone);

    // Cloudflare imported what it found for the apex; nothing is Zmail's yet.
    const imported = (await (await listRecords(route, zoneId)).json()).data;
    expect(
      imported.map((record: Record<string, unknown>) => [
        record.type,
        record.name,
        record.content,
        record.locked,
      ])
    ).toEqual([
      ['A', 'example.org', '192.0.2.10', false],
      ['CNAME', 'www.example.org', 'example.com', false],
    ]);

    await expectError(await receiving(route, domain.id), 409, 'MAILBOX.ZONE_PENDING');

    // A second domain under the apex joins the same zone.
    const sibling = (await (await connectDomain(route, { name: 'mail.example.org' })).json()).data;
    expect(sibling.zone.id).toBe(zoneId);
    await expectError(await deleteZone(route, zoneId), 409, 'MAILBOX.ZONE_IN_USE');

    const checked = (await (await checkDomain(route, domain.id)).json()).data;
    expect(checked).toMatchObject({ status: 'verified', zone_check_limited: false });
    // The zone turning active sets up every other pending domain in it too.
    const domains = (await (await route.listMailDomainsRoute(request('/api/mail-domains'))).json())
      .data as Array<{ id: number; status: string; records: unknown[] }>;
    const siblingNow = domains.find(item => item.id === sibling.id);
    expect(siblingNow).toMatchObject({ status: 'verified' });
    expect(siblingNow?.records.length).toBeGreaterThan(0);
    expect(checked.zone).toMatchObject({ id: zoneId, status: 'active' });
    expect(checked.zone.activated_at).not.toBeNull();

    // The setup added the mail records, which are now locked.
    const records = (await (await listRecords(route, zoneId)).json()).data as Array<{
      type: string;
      name: string;
      locked: boolean;
      locked_reason?: string;
    }>;
    expect(records.filter(record => record.type === 'MX' && record.name === 'example.org')).toEqual(
      expect.arrayContaining([
        expect.objectContaining({ locked: true, locked_reason: 'mail_receiving' }),
      ])
    );
    expect(records.find(record => record.name === '_dmarc.example.org')).toMatchObject({
      locked: true,
      locked_reason: 'mail_dmarc',
    });
    expect(records.find(record => record.type === 'A')).toMatchObject({ locked: false });

    // Another organization neither sees the zone nor takes its apex.
    activeOrganization = await secondOrganization();
    expect(
      (await (await route.listMailZonesRoute(request('/api/mail-zones'))).json()).data
    ).toEqual([]);
    await expectError(
      await route.getMailZoneRoute(request(`/api/mail-zones/${zoneId}`), String(zoneId)),
      404,
      'MAILBOX.ZONE_NOT_FOUND'
    );
    await expectError(await listRecords(route, zoneId), 404, 'MAILBOX.ZONE_NOT_FOUND');
    await expectError(
      await connectDomain(route, { name: 'shop.example.org' }),
      409,
      'MAILBOX.DOMAIN_TAKEN'
    );
  });

  it('limits managed zones and removes a pending one once no domain lives in it', async () => {
    process.env.MOCK_BFF_ENABLED = 'true';
    getSessionUser.mockResolvedValue(mockUser('demo-admin'));
    const route = await loadRoute();

    const ids: Array<{ domain: number; zone: number }> = [];
    for (const name of ['one.example', 'two.example', 'three.example']) {
      const domain = (await (await connectDomain(route, { name })).json()).data;
      ids.push({ domain: domain.id, zone: domain.zone.id });
    }
    await expectError(
      await connectDomain(route, { name: 'four.example' }),
      422,
      'MAILBOX.ZONE_LIMIT_REACHED'
    );

    const [first, second] = ids;
    await route.deleteMailDomainRoute(
      request(`/api/mail-domains/${first.domain}`, { method: 'DELETE' }),
      String(first.domain)
    );
    expect((await deleteZone(route, first.zone)).status).toBe(204);
    await expectError(await deleteZone(route, first.zone), 404, 'MAILBOX.ZONE_NOT_FOUND');

    // An active zone stays while the registrar may still delegate to Cloudflare.
    await checkDomain(route, second.domain);
    await route.deleteMailDomainRoute(
      request(`/api/mail-domains/${second.domain}`, { method: 'DELETE' }),
      String(second.domain)
    );
    await expectError(await deleteZone(route, second.zone), 409, 'MAILBOX.ZONE_STILL_DELEGATED');

    getSessionUser.mockResolvedValue(mockUser('demo-member'));
    expect((await route.listMailZonesRoute(request('/api/mail-zones'))).status).toBe(200);
    await expectError(await deleteZone(route, second.zone), 403, 'PERMISSION.DENIED');
  });

  it('edits managed zone DNS records in the mock and keeps mail records locked', async () => {
    process.env.MOCK_BFF_ENABLED = 'true';
    getSessionUser.mockResolvedValue(mockUser('demo-admin'));
    const route = await loadRoute();
    const domain = (await (await connectDomain(route, { name: 'example.org' })).json()).data;
    const zoneId: number = domain.zone.id;
    await checkDomain(route, domain.id);

    const created = await createRecord(route, zoneId, {
      type: 'A',
      name: 'app',
      content: '192.0.2.20',
      proxied: true,
    });
    expect(created.status).toBe(201);
    const record = (await created.json()).data;
    expect(record).toMatchObject({
      type: 'A',
      name: 'app.example.org',
      content: '192.0.2.20',
      ttl: 1,
      proxied: true,
      locked: false,
    });
    expect(record.id).toMatch(/^[A-Za-z0-9]{1,64}$/u);

    const updated = await updateRecord(route, zoneId, record.id, {
      content: '192.0.2.21',
      ttl: 300,
    });
    expect(updated.status).toBe(200);
    expect((await updated.json()).data).toMatchObject({
      id: record.id,
      content: '192.0.2.21',
      ttl: 300,
      proxied: true,
    });

    const invalid = await createRecord(route, zoneId, { type: 'A', name: 'bad', content: 'nope' });
    expect(invalid.status).toBe(422);
    await expect(invalid.json()).resolves.toMatchObject({
      error_code: 'MAILBOX.DNS_RECORD_INVALID',
      errors: { content: [expect.any(String)] },
    });
    await expectError(
      await createRecord(route, zoneId, { type: 'MX', name: '@', content: 'mx.other.example' }),
      422,
      'MAILBOX.DNS_RECORD_INVALID'
    );
    // Email Routing owns the MX of a receiving domain, and DMARC belongs to Zmail mail.
    await expectError(
      await createRecord(route, zoneId, {
        type: 'MX',
        name: '@',
        content: 'mx.other.example',
        priority: 5,
      }),
      409,
      'MAILBOX.DNS_RECORD_LOCKED'
    );
    await expectError(
      await createRecord(route, zoneId, {
        type: 'TXT',
        name: '_dmarc',
        content: 'v=DMARC1; p=reject',
      }),
      409,
      'MAILBOX.DNS_RECORD_LOCKED'
    );

    const records = (await (await listRecords(route, zoneId)).json()).data as Array<{
      id: string;
      locked: boolean;
    }>;
    const locked = records.find(item => item.locked);
    expect(locked).toBeDefined();
    await expectError(
      await updateRecord(route, zoneId, locked!.id, { ttl: 300 }),
      409,
      'MAILBOX.DNS_RECORD_LOCKED'
    );
    await expectError(
      await deleteRecord(route, zoneId, locked!.id),
      409,
      'MAILBOX.DNS_RECORD_LOCKED'
    );

    expect((await deleteRecord(route, zoneId, record.id)).status).toBe(204);
    await expectError(
      await deleteRecord(route, zoneId, record.id),
      404,
      'MAILBOX.DNS_RECORD_NOT_FOUND'
    );
    await expectError(
      await updateRecord(route, zoneId, 'missing1', { ttl: 300 }),
      404,
      'MAILBOX.DNS_RECORD_NOT_FOUND'
    );

    // Shape checks come before the store, and record IDs are Cloudflare's alphanumerics.
    await expectError(
      await createRecord(route, zoneId, { type: 'A', name: 'x', content: 1 }),
      422,
      'COMMON.VALIDATION_FAILED'
    );
    await expectError(
      await updateRecord(route, zoneId, record.id, { proxied: 'yes' }),
      422,
      'COMMON.VALIDATION_FAILED'
    );
    await expectError(await deleteRecord(route, zoneId, 'bad-id'), 400, 'COMMON.INVALID_INPUT');

    // Members read the records but never change them.
    getSessionUser.mockResolvedValue(mockUser('demo-member'));
    expect((await listRecords(route, zoneId)).status).toBe(200);
    await expectError(
      await createRecord(route, zoneId, { type: 'A', name: 'x', content: '192.0.2.1' }),
      403,
      'PERMISSION.DENIED'
    );
  });

  it('previews a domain and enables receiving on a send-only domain in the mock', async () => {
    process.env.MOCK_BFF_ENABLED = 'true';
    getSessionUser.mockResolvedValue(mockUser('demo-member'));
    const route = await loadRoute();

    // An apex usually has mail elsewhere; a fresh subdomain has none. Members may preview.
    const apex = await preview(route, { name: ' Example.ORG. ' });
    expect(apex.status).toBe(200);
    expect((await apex.json()).data).toEqual({
      name: 'example.org',
      apex: 'example.org',
      existing_mx: ['mx1.provider.example', 'mx2.provider.example'],
      suggested_capability: 'send_only',
    });
    expect((await (await preview(route, { name: 'mail.example.org' })).json()).data).toEqual({
      name: 'mail.example.org',
      apex: 'example.org',
      existing_mx: [],
      suggested_capability: 'send_receive',
    });
    await expectError(
      await preview(route, { name: 'not a domain' }),
      422,
      'MAILBOX.DOMAIN_INVALID'
    );
    await expectError(
      await preview(route, { name: 'mail.example.com' }),
      409,
      'MAILBOX.DOMAIN_TAKEN'
    );
    await expectError(await preview(route, { name: 7 }), 422, 'COMMON.VALIDATION_FAILED');

    getSessionUser.mockResolvedValue(mockUser('demo-admin'));
    await expectError(
      await connectDomain(route, { name: 'news.example.com', capability: 'receive_only' }),
      422,
      'COMMON.VALIDATION_FAILED'
    );
    const sendOnly = (
      await (
        await connectDomain(route, { name: 'news.example.com', capability: 'send_only' })
      ).json()
    ).data;
    expect(sendOnly.capability).toBe('send_only');
    expect(sendOnly.records.some((item: { purpose: string }) => item.purpose === 'receiving')).toBe(
      false
    );

    const enabled = await receiving(route, sendOnly.id);
    expect(enabled.status).toBe(200);
    const upgraded = (await enabled.json()).data;
    expect(upgraded.capability).toBe('send_receive');
    expect(upgraded.records.some((item: { purpose: string }) => item.purpose === 'receiving')).toBe(
      true
    );
    // Already receiving: answered unchanged.
    expect((await (await receiving(route, sendOnly.id)).json()).data).toMatchObject({
      capability: 'send_receive',
    });
    await expectError(await receiving(route, 999), 404, 'MAILBOX.DOMAIN_NOT_FOUND');

    getSessionUser.mockResolvedValue(mockUser('demo-member'));
    await expectError(await receiving(route, sendOnly.id), 403, 'PERMISSION.DENIED');
  });

  it('forwards managed zone, preview, and receiving routes to the fixed API paths', async () => {
    process.env.API_ADAPTER_ENABLED = 'true';
    process.env.API_UPSTREAM_URL = 'https://api.example.com/v1';
    cookieStore.get.mockReturnValue({ value: 'A'.repeat(43) });
    fetchMock.mockImplementation(async () => Response.json({ code: 0, message: 'ok', data: [] }));
    const route = await loadRoute();

    await preview(route, { name: 'example.org' });
    await connectDomain(route, { name: 'example.org', capability: 'send_only' });
    await receiving(route, 3);
    await route.listMailZonesRoute(request('/api/mail-zones'));
    await route.getMailZoneRoute(request('/api/mail-zones/4'), '4');
    await deleteZone(route, 4);
    await listRecords(route, 4);
    await createRecord(route, 4, {
      type: 'MX',
      name: '@',
      content: 'mx.example.net',
      priority: 10,
    });
    await updateRecord(route, 4, 'abc123', { content: '192.0.2.2', proxied: false });
    await deleteRecord(route, 4, 'abc123');

    expect(
      fetchMock.mock.calls.map(([url, init]) => `${init?.method} ${new URL(String(url)).pathname}`)
    ).toEqual([
      'POST /v1/mail-domain-previews',
      'POST /v1/mail-domains',
      'POST /v1/mail-domains/3/receiving',
      'GET /v1/mail-zones',
      'GET /v1/mail-zones/4',
      'DELETE /v1/mail-zones/4',
      'GET /v1/mail-zones/4/dns-records',
      'POST /v1/mail-zones/4/dns-records',
      'PATCH /v1/mail-zones/4/dns-records/abc123',
      'DELETE /v1/mail-zones/4/dns-records/abc123',
    ]);
    const bodies = fetchMock.mock.calls.map(([, init]) =>
      init?.body ? JSON.parse(String(init.body)) : undefined
    );
    expect(bodies[0]).toEqual({ name: 'example.org' });
    expect(bodies[1]).toEqual({ name: 'example.org', capability: 'send_only' });
    expect(bodies[2]).toBeUndefined();
    expect(bodies[7]).toEqual({ type: 'MX', name: '@', content: 'mx.example.net', priority: 10 });
    expect(bodies[8]).toEqual({ content: '192.0.2.2', proxied: false });

    // Malformed input and IDs never reach the API.
    fetchMock.mockClear();
    await expectError(await deleteRecord(route, 4, 'a/b'), 400, 'COMMON.INVALID_INPUT');
    await expectError(await listRecords(route, 0), 400, 'COMMON.INVALID_INPUT');
    await expectError(
      await createRecord(route, 4, { type: 'A', name: 'x', content: '192.0.2.1', ttl: '60' }),
      422,
      'COMMON.VALIDATION_FAILED'
    );
    await expectError(await preview(route, {}), 422, 'COMMON.VALIDATION_FAILED');
    const crossOrigin = await route.createMailDnsRecordRoute(
      request('/api/mail-zones/4/dns-records', {
        method: 'POST',
        body: '{',
        origin: 'https://evil.example',
      }),
      '4'
    );
    expect(crossOrigin.status).toBe(403);
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('passes managed zone and DNS record failures from the API through unchanged', async () => {
    process.env.API_ADAPTER_ENABLED = 'true';
    process.env.API_UPSTREAM_URL = 'https://api.example.com/v1';
    cookieStore.get.mockReturnValue({ value: 'A'.repeat(43) });
    const upstream = (status: number, errorCode: string, extra: Record<string, unknown> = {}) =>
      Response.json(
        {
          code: status,
          error_code: errorCode,
          message: 'Unprocessable',
          error: 'cloudflare said no',
          ...extra,
        },
        { status }
      );
    fetchMock
      .mockResolvedValueOnce(upstream(409, 'MAILBOX.DNS_RECORD_LOCKED'))
      .mockResolvedValueOnce(
        upstream(422, 'MAILBOX.DNS_RECORD_INVALID', { errors: { content: ['not an IPv4'] } })
      )
      .mockResolvedValueOnce(upstream(422, 'MAILBOX.DNS_RECORD_REJECTED'))
      .mockResolvedValueOnce(upstream(404, 'MAILBOX.DNS_RECORD_NOT_FOUND'))
      .mockResolvedValueOnce(upstream(404, 'MAILBOX.ZONE_NOT_FOUND'))
      .mockResolvedValueOnce(upstream(409, 'MAILBOX.ZONE_STILL_DELEGATED'))
      .mockResolvedValueOnce(upstream(409, 'MAILBOX.ZONE_PENDING'))
      .mockResolvedValueOnce(upstream(422, 'MAILBOX.ZONE_LIMIT_REACHED'))
      .mockResolvedValueOnce(
        Response.json({ code: 0, message: 'ok', data: { id: 3, zone_check_limited: true } })
      );
    const route = await loadRoute();

    await expectError(
      await updateRecord(route, 4, 'abc123', { ttl: 300 }),
      409,
      'MAILBOX.DNS_RECORD_LOCKED'
    );
    const invalid = await createRecord(route, 4, { type: 'A', name: 'x', content: 'nope' });
    expect(invalid.status).toBe(422);
    const invalidBody = await invalid.json();
    expect(invalidBody).toMatchObject({
      error_code: 'MAILBOX.DNS_RECORD_INVALID',
      errors: { content: [expect.any(String)] },
    });
    const rejected = await createRecord(route, 4, { type: 'A', name: 'x', content: '192.0.2.1' });
    expect(rejected.status).toBe(422);
    const rejectedBody = await rejected.json();
    expect(rejectedBody.error_code).toBe('MAILBOX.DNS_RECORD_REJECTED');
    // Cloudflare's reason is the one upstream text the browser receives.
    expect(rejectedBody.message).toBe('cloudflare said no');
    expect(rejectedBody.error).toBe('cloudflare said no');
    await expectError(await deleteRecord(route, 4, 'abc123'), 404, 'MAILBOX.DNS_RECORD_NOT_FOUND');
    await expectError(await listRecords(route, 4), 404, 'MAILBOX.ZONE_NOT_FOUND');
    await expectError(await deleteZone(route, 4), 409, 'MAILBOX.ZONE_STILL_DELEGATED');
    await expectError(await receiving(route, 3), 409, 'MAILBOX.ZONE_PENDING');
    await expectError(
      await connectDomain(route, { name: 'example.org' }),
      422,
      'MAILBOX.ZONE_LIMIT_REACHED'
    );
    // A rate-limited zone check is a flag on the answer, not an error.
    const limited = await checkDomain(route, 3);
    expect(limited.status).toBe(200);
    expect((await limited.json()).data).toMatchObject({ zone_check_limited: true });
  });

  it('forwards upstream error text only for a rejected DNS record', async () => {
    process.env.API_ADAPTER_ENABLED = 'true';
    process.env.API_UPSTREAM_URL = 'https://api.example.com/v1';
    cookieStore.get.mockReturnValue({ value: 'A'.repeat(43) });
    const upstream = (status: number, errorCode: string) =>
      Response.json(
        { code: status, error_code: errorCode, message: 'secret', error: 'provider internals' },
        { status }
      );
    fetchMock
      .mockResolvedValueOnce(upstream(409, 'MAILBOX.DNS_RECORD_LOCKED'))
      .mockResolvedValueOnce(upstream(502, 'MAILBOX.DOMAIN_PROVIDER_FAILED'))
      .mockResolvedValueOnce(upstream(422, 'MAILBOX.DNS_RECORD_INVALID'));
    const route = await loadRoute();

    for (const response of [
      await updateRecord(route, 4, 'abc123', { ttl: 300 }),
      await createRecord(route, 4, { type: 'A', name: 'x', content: '192.0.2.1' }),
      await createRecord(route, 4, { type: 'A', name: 'x', content: '192.0.2.1' }),
    ]) {
      const text = await response.text();
      expect(text).not.toContain('provider internals');
      expect(text).not.toContain('secret');
    }
  });

  it('forwards contact routes to the fixed API paths', async () => {
    process.env.API_ADAPTER_ENABLED = 'true';
    process.env.API_UPSTREAM_URL = 'https://api.example.com/v1';
    cookieStore.get.mockReturnValue({ value: 'A'.repeat(43) });
    fetchMock.mockImplementation(async () => Response.json({ code: 0, message: 'ok', data: [] }));
    const route = await loadRoute();
    const body = (value: unknown) => JSON.stringify(value);

    await route.listMailContactsRoute(
      request('/api/mail-contacts?q=ann&status=subscribed&list_id=4&page=2')
    );
    await route.createMailContactRoute(
      request('/api/mail-contacts', { method: 'POST', body: body({ email: 'a@example.com' }) })
    );
    await route.updateMailContactRoute(
      request('/api/mail-contacts/3', { method: 'PUT', body: body({ first_name: 'A' }) }),
      '3'
    );
    await route.deleteMailContactRoute(request('/api/mail-contacts/3', { method: 'DELETE' }), '3');
    await route.importMailContactsRoute(
      request('/api/mail-contact-imports', {
        method: 'POST',
        body: body({ csv: 'email\n', list_id: 4 }),
      })
    );
    await route.listMailContactListsRoute(request('/api/mail-contact-lists'));
    await route.saveMailContactListRoute(
      request('/api/mail-contact-lists', { method: 'POST', body: body({ name: 'A' }) })
    );
    await route.saveMailContactListRoute(
      request('/api/mail-contact-lists/4', { method: 'PUT', body: body({ name: 'B' }) }),
      '4'
    );
    await route.deleteMailContactListRoute(
      request('/api/mail-contact-lists/4', { method: 'DELETE' }),
      '4'
    );

    expect(
      fetchMock.mock.calls.map(([url, init]) => {
        const parsed = new URL(String(url));
        return `${init?.method} ${parsed.pathname}${parsed.search}`;
      })
    ).toEqual([
      'GET /v1/mail-contacts?q=ann&status=subscribed&list_id=4&page=2&per_page=15',
      'POST /v1/mail-contacts',
      'PUT /v1/mail-contacts/3',
      'DELETE /v1/mail-contacts/3',
      'POST /v1/mail-contact-imports',
      'GET /v1/mail-contact-lists',
      'POST /v1/mail-contact-lists',
      'PUT /v1/mail-contact-lists/4',
      'DELETE /v1/mail-contact-lists/4',
    ]);
  });

  it('rejects malformed contact bodies before they reach the backend', async () => {
    process.env.MOCK_BFF_ENABLED = 'true';
    getSessionUser.mockResolvedValue(mockUser('demo-admin'));
    const route = await loadRoute();
    for (const body of [
      { email: 7 },
      { email: 'a@example.com', status: 'suppressed' },
      { email: 'a@example.com', list_ids: ['1'] },
      { email: 'a@example.com', properties: { plan: 1 } },
      { email: 'a@example.com', extra: true },
    ]) {
      await expectError(
        await route.createMailContactRoute(
          request('/api/mail-contacts', { method: 'POST', body: JSON.stringify(body) })
        ),
        422,
        'COMMON.VALIDATION_FAILED'
      );
    }
    for (const body of [{}, { csv: '' }, { csv: 'email', list_id: 'x' }]) {
      await expectError(
        await route.importMailContactsRoute(
          request('/api/mail-contact-imports', { method: 'POST', body: JSON.stringify(body) })
        ),
        422,
        'COMMON.VALIDATION_FAILED'
      );
    }
  });

  it('sends a test group first and lets only owners and admins control a broadcast', async () => {
    process.env.MOCK_BFF_ENABLED = 'true';
    getSessionUser.mockResolvedValue(mockUser('demo-admin'));
    const route = await loadRoute();
    const json = (path: string, body: unknown, method = 'POST') =>
      request(path, { method, body: JSON.stringify(body) });

    const mailbox = (await (await create(route, { local_part: 'launch' })).json()).data;
    const template = (
      await (
        await route.createMailTemplateRoute(
          json('/api/mail-templates', { name: 'Launch', subject: 'New', text: 'Hello' })
        )
      ).json()
    ).data;
    const list = (
      await (
        await route.saveMailContactListRoute(json('/api/mail-contact-lists', { name: 'All' }))
      ).json()
    ).data;
    for (let index = 0; index < 10; index += 1) {
      await route.createMailContactRoute(
        json('/api/mail-contacts', { email: `user${index}@example.com`, list_ids: [list.id] })
      );
    }
    const draft = (
      await (
        await route.saveMailBroadcastRoute(
          json('/api/mail-broadcasts', {
            name: 'Staged',
            mailbox_id: mailbox.id,
            template_id: template.id,
            list_id: list.id,
            test_percent: 20,
            test_wait_minutes: 45,
          })
        )
      ).json()
    ).data;
    expect(draft).toMatchObject({ test_percent: 20, test_wait_minutes: 45 });
    await expectError(
      await route.saveMailBroadcastRoute(
        json('/api/mail-broadcasts', {
          name: 'Too big',
          mailbox_id: mailbox.id,
          template_id: template.id,
          list_id: list.id,
          test_percent: 60,
        })
      ),
      422,
      'COMMON.VALIDATION_FAILED'
    );

    // A member drafts but never starts, pauses, resumes, or cancels.
    getSessionUser.mockResolvedValue(mockUser('demo-member'));
    for (const action of ['pauses', 'resumptions', 'cancellations'] as const) {
      await expectError(
        await route.controlMailBroadcastRoute(
          json(`/api/mail-broadcasts/${draft.id}/${action}`, {}),
          String(draft.id),
          action
        ),
        403,
        'PERMISSION.DENIED'
      );
    }
    await expectError(
      await route.sendMailBroadcastRoute(
        json(`/api/mail-broadcasts/${draft.id}/sends`, {}),
        String(draft.id)
      ),
      403,
      'PERMISSION.DENIED'
    );

    getSessionUser.mockResolvedValue(mockUser('demo-admin'));
    const started = (
      await (
        await route.sendMailBroadcastRoute(
          json(`/api/mail-broadcasts/${draft.id}/sends`, {}),
          String(draft.id)
        )
      ).json()
    ).data;
    expect(started).toMatchObject({
      status: 'paused',
      stop_reason: 'test_wave_complete',
      current_wave: 1,
      counts: { total: 10, sent: 2, pending: 8 },
    });
    expect(started.resume_at).not.toBeNull();
    const resumed = (
      await (
        await route.controlMailBroadcastRoute(
          json(`/api/mail-broadcasts/${draft.id}/resumptions`, {}),
          String(draft.id),
          'resumptions'
        )
      ).json()
    ).data;
    expect(resumed).toMatchObject({
      status: 'sent',
      current_wave: 2,
      counts: { sent: 10, pending: 0, bounced: 0 },
    });
    await expectError(
      await route.controlMailBroadcastRoute(
        json(`/api/mail-broadcasts/${draft.id}/pauses`, {}),
        String(draft.id),
        'pauses'
      ),
      409,
      'MAILBOX.BROADCAST_NOT_EDITABLE'
    );
  });

  it('runs a broadcast in the mock and unsubscribes through its link', async () => {
    process.env.MOCK_BFF_ENABLED = 'true';
    getSessionUser.mockResolvedValue(mockUser('demo-admin'));
    const route = await loadRoute();
    const { unsubscribeRoute } = await import('@/features/mailbox/server/unsubscribe-route');
    const json = (path: string, body: unknown, method = 'POST') =>
      request(path, { method, body: JSON.stringify(body) });

    const mailbox = (await (await create(route, { local_part: 'news' })).json()).data;
    const template = (
      await (
        await route.createMailTemplateRoute(
          json('/api/mail-templates', {
            name: 'News',
            subject: 'Hi {{first_name|there}}',
            text: 'Plan {{plan}}. Leave: {{unsubscribe_url}}',
          })
        )
      ).json()
    ).data;
    const list = (
      await (
        await route.saveMailContactListRoute(json('/api/mail-contact-lists', { name: 'All' }))
      ).json()
    ).data;
    for (const [email, plan] of [
      ['ann@example.com', 'pro'],
      ['bob@example.com', ''],
    ]) {
      await route.createMailContactRoute(
        json('/api/mail-contacts', {
          email,
          first_name: email.slice(0, 3),
          properties: plan ? { plan } : {},
          list_ids: [list.id],
        })
      );
    }

    const draft = (
      await (
        await route.saveMailBroadcastRoute(
          json('/api/mail-broadcasts', {
            name: 'Launch',
            mailbox_id: mailbox.id,
            template_id: template.id,
            list_id: list.id,
          })
        )
      ).json()
    ).data;
    expect(draft).toMatchObject({ status: 'draft', counts: { total: 0 } });

    const sent = await route.sendMailBroadcastRoute(
      json(`/api/mail-broadcasts/${draft.id}/sends`, {}),
      String(draft.id)
    );
    // Bob has no plan and the template has no fallback for it, so his message fails.
    expect((await sent.json()).data).toMatchObject({
      status: 'sent',
      counts: { total: 2, sent: 1, failed: 1 },
    });
    const failed = await (
      await route.listMailBroadcastRecipientsRoute(
        request(`/api/mail-broadcasts/${draft.id}/recipients?status=failed`),
        String(draft.id)
      )
    ).json();
    expect(failed.data).toEqual([
      expect.objectContaining({
        email: 'bob@example.com',
        failure_code: 'template.variables_missing',
      }),
    ]);
    await expectError(
      await route.sendMailBroadcastRoute(
        json(`/api/mail-broadcasts/${draft.id}/sends`, {}),
        String(draft.id)
      ),
      409,
      'MAILBOX.BROADCAST_NOT_EDITABLE'
    );

    const outbound = await (
      await route.listMailMessagesRoute(
        request(`/api/mailboxes/${mailbox.id}/messages?direction=outbound`),
        String(mailbox.id)
      )
    ).json();
    const message = (
      await (
        await route.getMailMessageRoute(
          request(`/api/mailboxes/${mailbox.id}/messages/${outbound.data[0].id}`),
          String(mailbox.id),
          String(outbound.data[0].id)
        )
      ).json()
    ).data;
    const token = /\/unsubscribe\/([^\s]+)/u.exec(message.text_body)?.[1] ?? '';
    expect(message.text_body).toContain('Plan pro.');

    // The public route needs no session and no same-origin browser.
    getSessionUser.mockResolvedValue(null);
    const external = new Request(`https://app.example.com/api/mail-unsubscribes/${token}`, {
      method: 'POST',
      body: 'List-Unsubscribe=One-Click',
      headers: {
        'content-type': 'application/x-www-form-urlencoded',
        origin: 'https://mail.google.com',
      },
    });
    const unsubscribed = await unsubscribeRoute(external, token);
    expect(unsubscribed.status).toBe(204);
    for (const bad of ['1.2', `${token}x`, '1.2.' + 'a'.repeat(22)]) {
      const response = await unsubscribeRoute(
        new Request('https://app.example.com/', { method: 'POST' }),
        bad
      );
      expect(response.status).toBe(404);
    }

    getSessionUser.mockResolvedValue(mockUser('demo-admin'));
    const ann = await (
      await route.listMailContactsRoute(request('/api/mail-contacts?q=ann'))
    ).json();
    expect(ann.data[0]).toMatchObject({
      status: 'unsubscribed',
      status_reason: 'unsubscribe_link',
    });

    const scheduled = (
      await (
        await route.saveMailBroadcastRoute(
          json('/api/mail-broadcasts', {
            name: 'Later',
            mailbox_id: mailbox.id,
            template_id: template.id,
            list_id: list.id,
          })
        )
      ).json()
    ).data;
    const later = await route.sendMailBroadcastRoute(
      json(`/api/mail-broadcasts/${scheduled.id}/sends`, {
        scheduled_at: new Date(Date.now() + 3_600_000).toISOString(),
      }),
      String(scheduled.id)
    );
    expect((await later.json()).data).toMatchObject({ status: 'scheduled', counts: { total: 1 } });
    await expectError(
      await route.deleteMailBroadcastRoute(
        request(`/api/mail-broadcasts/${scheduled.id}`, { method: 'DELETE' }),
        String(scheduled.id)
      ),
      409,
      'MAILBOX.BROADCAST_NOT_EDITABLE'
    );
    const canceled = await route.controlMailBroadcastRoute(
      json(`/api/mail-broadcasts/${scheduled.id}/cancellations`, {}),
      String(scheduled.id),
      'cancellations'
    );
    expect((await canceled.json()).data).toMatchObject({
      status: 'canceled',
      counts: { canceled: 1 },
    });

    const list2 = await (
      await route.listMailBroadcastsRoute(request('/api/mail-broadcasts'))
    ).json();
    expect(list2.data.map((item: { name: string }) => item.name)).toEqual(['Later', 'Launch']);
  });

  it('fills shared broadcast values, lets contact fields win, and refuses reserved names', async () => {
    process.env.MOCK_BFF_ENABLED = 'true';
    getSessionUser.mockResolvedValue(mockUser('demo-admin'));
    const route = await loadRoute();
    const json = (path: string, body: unknown) =>
      request(path, { method: 'POST', body: JSON.stringify(body) });
    const data = async (response: Response) => (await response.json()).data;

    const mailbox = await data(await create(route, { local_part: 'promo' }));
    const template = await data(
      await route.createMailTemplateRoute(
        json('/api/mail-templates', { name: 'Sale', subject: 'Use {{code}}', text: 'Sale' })
      )
    );
    const list = await data(
      await route.saveMailContactListRoute(json('/api/mail-contact-lists', { name: 'Sale' }))
    );
    for (const [email, code] of [
      ['ann@example.com', 'ANN50'],
      ['bob@example.com', ''],
    ]) {
      await route.createMailContactRoute(
        json('/api/mail-contacts', {
          email,
          properties: code ? { code } : {},
          list_ids: [list.id],
        })
      );
    }
    const base = {
      name: 'Sale',
      mailbox_id: mailbox.id,
      template_id: template.id,
      list_id: list.id,
    };
    for (const variables of [{ first_name: 'x' }, { unsubscribe_url: 'x' }, { '1x': 'x' }, []]) {
      await expectError(
        await route.saveMailBroadcastRoute(json('/api/mail-broadcasts', { ...base, variables })),
        422,
        'COMMON.VALIDATION_FAILED'
      );
    }
    const draft = await data(
      await route.saveMailBroadcastRoute(
        json('/api/mail-broadcasts', { ...base, variables: { code: 'NOV20' } })
      )
    );
    expect(draft.variables).toEqual({ code: 'NOV20' });
    const sent = await data(
      await route.sendMailBroadcastRoute(
        json(`/api/mail-broadcasts/${draft.id}/sends`, {}),
        String(draft.id)
      )
    );
    // Bob has no code of his own, so the shared one fills it instead of failing him.
    expect(sent).toMatchObject({ status: 'sent', counts: { total: 2, sent: 2, failed: 0 } });
    const messages = await (
      await route.listMailMessagesRoute(
        request(`/api/mailboxes/${mailbox.id}/messages?direction=outbound`),
        String(mailbox.id)
      )
    ).json();
    const subjects = Object.fromEntries(
      messages.data.map((message: { to: string[]; subject: string }) => [
        message.to[0],
        message.subject,
      ])
    );
    expect(subjects).toEqual({ 'ann@example.com': 'Use ANN50', 'bob@example.com': 'Use NOV20' });
  });

  it('forwards broadcast routes and the public unsubscribe route to the fixed API paths', async () => {
    process.env.API_ADAPTER_ENABLED = 'true';
    process.env.API_UPSTREAM_URL = 'https://api.example.com/v1';
    cookieStore.get.mockReturnValue({ value: 'A'.repeat(43) });
    fetchMock.mockImplementation(async () => Response.json({ code: 0, message: 'ok', data: [] }));
    const route = await loadRoute();
    const { unsubscribeRoute } = await import('@/features/mailbox/server/unsubscribe-route');
    const body = JSON.stringify({ name: 'A', mailbox_id: 1, template_id: 2, list_id: 3 });

    await route.listMailBroadcastsRoute(request('/api/mail-broadcasts?page=2'));
    await route.saveMailBroadcastRoute(request('/api/mail-broadcasts', { method: 'POST', body }));
    await route.saveMailBroadcastRoute(
      request('/api/mail-broadcasts/5', { method: 'PUT', body }),
      '5'
    );
    await route.getMailBroadcastRoute(request('/api/mail-broadcasts/5'), '5');
    await route.sendMailBroadcastRoute(
      request('/api/mail-broadcasts/5/sends', {
        method: 'POST',
        body: JSON.stringify({ scheduled_at: '2030-01-01T10:00:00+08:00' }),
      }),
      '5'
    );
    await route.controlMailBroadcastRoute(
      request('/api/mail-broadcasts/5/cancellations', { method: 'POST', body: '{}' }),
      '5',
      'cancellations'
    );
    await route.controlMailBroadcastRoute(
      request('/api/mail-broadcasts/5/pauses', { method: 'POST' }),
      '5',
      'pauses'
    );
    await route.controlMailBroadcastRoute(
      request('/api/mail-broadcasts/5/resumptions', { method: 'POST' }),
      '5',
      'resumptions'
    );
    await route.listMailBroadcastRecipientsRoute(
      request('/api/mail-broadcasts/5/recipients?status=failed'),
      '5'
    );
    await route.deleteMailBroadcastRoute(
      request('/api/mail-broadcasts/5', { method: 'DELETE' }),
      '5'
    );
    fetchMock.mockImplementationOnce(async () => new Response(null, { status: 204 }));
    const token = `1.2.${'a'.repeat(22)}`;
    const unsubscribed = await unsubscribeRoute(
      new Request(`https://app.example.com/api/mail-unsubscribes/${token}`, { method: 'POST' }),
      token
    );
    expect(unsubscribed.status).toBe(204);

    const calls = fetchMock.mock.calls.map(([url, init]) => {
      const parsed = new URL(String(url));
      return `${init?.method} ${parsed.pathname}${parsed.search}`;
    });
    expect(calls).toEqual([
      'GET /v1/mail-broadcasts?page=2&per_page=15',
      'POST /v1/mail-broadcasts',
      'PUT /v1/mail-broadcasts/5',
      'GET /v1/mail-broadcasts/5',
      'POST /v1/mail-broadcasts/5/sends',
      'POST /v1/mail-broadcasts/5/cancellations',
      'POST /v1/mail-broadcasts/5/pauses',
      'POST /v1/mail-broadcasts/5/resumptions',
      'GET /v1/mail-broadcasts/5/recipients?page=1&per_page=15&status=failed',
      'DELETE /v1/mail-broadcasts/5',
      `POST /v1/mail-unsubscribes/${token}`,
    ]);
    // The schedule is forwarded in UTC, and the public call carries no credentials.
    expect(JSON.parse(String(fetchMock.mock.calls[4]?.[1]?.body))).toEqual({
      scheduled_at: '2030-01-01T02:00:00.000Z',
    });
    const publicHeaders = new Headers(fetchMock.mock.calls[10]?.[1]?.headers);
    expect(publicHeaders.get('authorization')).toBeNull();
    expect(publicHeaders.get('organization-id')).toBeNull();
  });

  it('forwards only the fixed production paths, methods, and whitelisted query', async () => {
    process.env.API_ADAPTER_ENABLED = 'true';
    process.env.API_UPSTREAM_URL = 'https://api.example.com/v1';
    cookieStore.get.mockReturnValue({ value: 'A'.repeat(43) });
    fetchMock.mockImplementation(async () =>
      Response.json({ code: 0, message: 'success', data: [] })
    );
    const route = await loadRoute();
    activeOrganization = '7';

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
    // The API verifies membership; the adapter only carries the organization on every call.
    expect(fetchMock.mock.calls).toHaveLength(10);
    for (const [, init] of fetchMock.mock.calls) {
      expect(new Headers(init?.headers).get('organization-id')).toBe('7');
    }
    expect(getSessionUser).not.toHaveBeenCalled();
  });

  it('forwards a member write and returns the decision of the API', async () => {
    process.env.API_ADAPTER_ENABLED = 'true';
    process.env.API_UPSTREAM_URL = 'https://api.example.com/v1';
    cookieStore.get.mockReturnValue({ value: 'A'.repeat(43) });
    fetchMock.mockImplementation(async () =>
      Response.json(
        { code: 403, error_code: 'PERMISSION.DENIED', message: 'members cannot' },
        { status: 403 }
      )
    );
    const route = await loadRoute();

    const created = await create(route, { local_part: 'hello' });
    const removed = await route.deleteMailboxRoute(
      request('/api/mailboxes/12', { method: 'DELETE' }),
      '12'
    );

    for (const response of [created, removed]) {
      await expectError(response, 403, 'PERMISSION.DENIED');
    }
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it('rejects a missing or malformed organization header before it reaches the API', async () => {
    process.env.API_ADAPTER_ENABLED = 'true';
    process.env.API_UPSTREAM_URL = 'https://api.example.com/v1';
    cookieStore.get.mockReturnValue({ value: 'A'.repeat(43) });
    const route = await loadRoute();

    await expectError(
      await route.listMailboxesRoute(request('/api/mailboxes', { organization: null })),
      400,
      'ORGANIZATION.CONTEXT_REQUIRED'
    );
    await expectError(
      await route.createMailboxRoute(
        request('/api/mailboxes', { method: 'POST', body: '{}', organization: null })
      ),
      400,
      'ORGANIZATION.CONTEXT_REQUIRED'
    );
    for (const organization of ['abc', '0', ['7', '7'], ['7', '8']]) {
      await expectError(
        await route.listMailLogRoute(request('/api/mail-messages', { organization })),
        400,
        'ORGANIZATION.CONTEXT_INVALID'
      );
      await expectError(
        await route.deleteMailboxRoute(
          request('/api/mailboxes/12', { method: 'DELETE', organization }),
          '12'
        ),
        400,
        'ORGANIZATION.CONTEXT_INVALID'
      );
    }
    expect(fetchMock).not.toHaveBeenCalled();
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

  it('guards the log and the statistics like every other read', async () => {
    process.env.MOCK_BFF_ENABLED = 'true';
    process.env.NEXT_PUBLIC_OPTIONAL_FEATURES = '';
    getSessionUser.mockResolvedValue(mockUser('demo-admin'));
    let route = await loadRoute();

    for (const response of [
      await route.listMailLogRoute(request('/api/mail-messages')),
      await route.listMailMessageStatsRoute(request('/api/mail-message-stats')),
    ]) {
      expect(response.status).toBe(503);
      await expect(response.json()).resolves.toMatchObject({
        error_code: 'COMMON.SERVICE_UNAVAILABLE',
      });
    }
    expect(getSessionUser).not.toHaveBeenCalled();

    vi.resetModules();
    process.env.NEXT_PUBLIC_OPTIONAL_FEATURES = 'organization,mailbox';
    getSessionUser.mockResolvedValue(null);
    route = await loadRoute();
    // Authentication comes before validation: a signed-out caller learns nothing about the query.
    for (const response of [
      await route.listMailLogRoute(request('/api/mail-messages?direction=spam')),
      await route.listMailMessageStatsRoute(request('/api/mail-message-stats?days=0')),
    ]) {
      await expectError(response, 401, 'AUTH.UNAUTHORIZED');
    }
  });

  it('lists the log across mailboxes, newest first, with the owning address', async () => {
    process.env.MOCK_BFF_ENABLED = 'true';
    getSessionUser.mockResolvedValue(mockUser('demo-admin'));
    const route = await loadRoute();
    const hello = (await (await create(route, { local_part: 'hello' })).json()).data;
    const sales = (await (await create(route, { local_part: 'sales' })).json()).data;
    const offer = (
      await (
        await send(route, hello.id, {
          to: ['Bob@Example.com'],
          subject: '100% off_today',
          text: 'Body',
        })
      ).json()
    ).data;
    const invoice = (
      await (
        await send(route, sales.id, {
          to: ['carol@example.org', 'dave@example.com'],
          subject: 'Invoice 1000 offXtoday',
          html: '<p>x</p>',
        })
      ).json()
    ).data;
    const log = async (query = '') =>
      (await route.listMailLogRoute(request(`/api/mail-messages${query}`))).json();
    const ids = async (query: string) =>
      (await log(query)).data.map((item: { id: number }) => item.id);

    const response = await route.listMailLogRoute(request('/api/mail-messages'));
    expect(response.status).toBe(200);
    expect(response.headers.get('cache-control')).toBe('private, no-store');
    expect(response.headers.get('vary')).toContain('Cookie');
    expect(response.headers.get('vary')).toContain('Organization-Id');
    const everything = await response.json();
    // Two welcome messages and two sends, newest first.
    expect(everything.data.map((item: { id: number }) => item.id)).toEqual([
      invoice.id,
      offer.id,
      invoice.id - 2,
      offer.id - 2,
    ]);
    expect(everything.meta).toMatchObject({ current_page: 1, per_page: 15, total: 4 });
    // Log entries carry the delivery summary and never the events.
    expect(everything.data.map((item: { last_event: string }) => item.last_event)).toEqual([
      'delivered',
      'delivered',
      '',
      '',
    ]);
    for (const item of everything.data) expect(item).not.toHaveProperty('events');
    expect(everything.data[0]).toMatchObject({
      mailbox_id: sales.id,
      mailbox_address: 'sales@mail.example.com',
      direction: 'outbound',
      status: 'sent',
    });
    expect(everything.data[1].mailbox_address).toBe('hello@mail.example.com');
    for (const entry of everything.data) {
      expect(entry).not.toHaveProperty('text_body');
      expect(entry).not.toHaveProperty('html_body');
    }

    expect(await ids('?direction=outbound')).toEqual([invoice.id, offer.id]);
    expect(await ids('?direction=inbound')).toEqual([invoice.id - 2, offer.id - 2]);
    expect(await ids('?status=received')).toEqual([invoice.id - 2, offer.id - 2]);
    expect(await ids('?status=sent')).toEqual([invoice.id, offer.id]);
    expect(await ids('?status=failed')).toEqual([]);
    expect(await ids('?status=queued')).toEqual([]);
    expect(await ids(`?mailbox_id=${hello.id}`)).toEqual([offer.id, offer.id - 2]);
    expect(await ids(`?mailbox_id=${sales.id}&direction=outbound&status=sent`)).toEqual([
      invoice.id,
    ]);
    // An empty value does not filter, as in the API.
    expect(await ids('?direction=&status=&mailbox_id=&q=')).toHaveLength(4);

    // The search covers the subject, the sender address, and every recipient address.
    expect(await ids('?q=INVOICE')).toEqual([invoice.id]);
    expect(await ids('?q=%20invoice%20')).toEqual([invoice.id]);
    expect(await ids('?q=BOB%40example')).toEqual([offer.id]);
    expect(await ids('?q=dave%40')).toEqual([invoice.id]);
    expect(await ids('?q=sales%40mail.example.com')).toEqual([invoice.id, invoice.id - 2]);
    expect(await ids('?q=nothing-like-this')).toEqual([]);
    // `%` and `_` are ordinary characters, not wildcards.
    expect(await ids('?q=100%25')).toEqual([offer.id]);
    expect(await ids('?q=off_today')).toEqual([offer.id]);
    expect(await ids('?q=%25')).toEqual([offer.id]);
    expect(await ids('?q=_')).toEqual([offer.id]);

    const page = await log('?direction=outbound&q=today&per_page=1&page=2');
    expect(page.data.map((item: { id: number }) => item.id)).toEqual([offer.id]);
    expect(page.meta).toMatchObject({ current_page: 2, per_page: 1, total: 2, last_page: 2 });
    expect(page.links.prev).toBe('/api/mail-messages?direction=outbound&q=today&page=1&per_page=1');
    expect(page.links.next).toBeNull();

    // A deleted mailbox takes its messages out of the log.
    await route.deleteMailboxRoute(
      request(`/api/mailboxes/${sales.id}`, { method: 'DELETE' }),
      String(sales.id)
    );
    expect(await ids('')).toEqual([offer.id, offer.id - 2]);

    // Another member of the organization sees the same log.
    getSessionUser.mockResolvedValue(mockUser('demo-member'));
    expect(await ids('')).toEqual([offer.id, offer.id - 2]);

    // Another organization sees none of it, even when naming the mailbox.
    getSessionUser.mockResolvedValue(mockUser('demo-admin'));
    activeOrganization = await secondOrganization();
    expect(await ids('')).toEqual([]);
    expect(await ids(`?mailbox_id=${hello.id}`)).toEqual([]);
  });

  it('rejects an invalid log or statistics query with invalid input', async () => {
    process.env.MOCK_BFF_ENABLED = 'true';
    getSessionUser.mockResolvedValue(mockUser('demo-admin'));
    const route = await loadRoute();

    for (const query of [
      '?direction=all',
      '?direction=inbound&direction=outbound',
      '?direction=inbound&direction=inbound',
      '?status=bounced',
      '?status=sent&status=failed',
      '?mailbox_id=0',
      '?mailbox_id=-1',
      '?mailbox_id=abc',
      '?mailbox_id=1.5',
      '?mailbox_id=1&mailbox_id=2',
      `?q=${'a'.repeat(201)}`,
      '?q=a&q=b',
    ]) {
      await expectError(
        await route.listMailLogRoute(request(`/api/mail-messages${query}`)),
        400,
        'COMMON.INVALID_INPUT'
      );
    }
    // 200 characters is the limit, counted after trimming.
    const longest = await route.listMailLogRoute(
      request(`/api/mail-messages?q=%20${'a'.repeat(200)}%20`)
    );
    expect(longest.status).toBe(200);

    for (const query of [
      '?days=0',
      '?days=91',
      '?days=-7',
      '?days=abc',
      '?days=1.5',
      '?days=',
      '?days=7&days=7',
    ]) {
      await expectError(
        await route.listMailMessageStatsRoute(request(`/api/mail-message-stats${query}`)),
        400,
        'COMMON.INVALID_INPUT'
      );
    }
  });

  it('counts mail per UTC day over the window, empty days included', async () => {
    vi.useFakeTimers({ toFake: ['Date'] });
    try {
      process.env.MOCK_BFF_ENABLED = 'true';
      getSessionUser.mockResolvedValue(mockUser('demo-admin'));
      const route = await loadRoute();
      const stats = async (query = '') => {
        const response = await route.listMailMessageStatsRoute(
          request(`/api/mail-message-stats${query}`)
        );
        expect(response.status).toBe(200);
        expect(response.headers.get('cache-control')).toBe('private, no-store');
        return (await response.json()).data;
      };
      const empty = { sent: 0, failed: 0, received: 0 };

      // Late on the 14th in UTC: the welcome message arrives.
      vi.setSystemTime(new Date('2026-11-14T23:59:30Z'));
      const mailbox = (await (await create(route, { local_part: 'hello' })).json()).data;
      // Just after midnight on the 16th: two messages go out.
      vi.setSystemTime(new Date('2026-11-16T00:00:05Z'));
      for (const subject of ['One', 'Two']) {
        await send(route, mailbox.id, { to: ['bob@example.com'], subject, text: 'Body' });
      }

      expect(await stats('?days=3')).toEqual([
        { day: '2026-11-14', sent: 0, failed: 0, received: 1 },
        { day: '2026-11-15', ...empty },
        { day: '2026-11-16', sent: 2, failed: 0, received: 0 },
      ]);
      expect(await stats('?days=1')).toEqual([
        { day: '2026-11-16', sent: 2, failed: 0, received: 0 },
      ]);
      // The window defaults to a week, oldest day first.
      const week = await stats();
      expect(week.map((entry: { day: string }) => entry.day)).toEqual([
        '2026-11-10',
        '2026-11-11',
        '2026-11-12',
        '2026-11-13',
        '2026-11-14',
        '2026-11-15',
        '2026-11-16',
      ]);
      expect(week.slice(0, 4)).toEqual(
        ['2026-11-10', '2026-11-11', '2026-11-12', '2026-11-13'].map(day => ({ day, ...empty }))
      );
      expect(await stats('?days=90')).toHaveLength(90);

      // Another member of the organization sees the same counts.
      getSessionUser.mockResolvedValue(mockUser('demo-member'));
      expect(await stats('?days=1')).toEqual([
        { day: '2026-11-16', sent: 2, failed: 0, received: 0 },
      ]);

      // Another organization has the same window with nothing in it.
      getSessionUser.mockResolvedValue(mockUser('demo-admin'));
      activeOrganization = await secondOrganization();
      const theirs = await stats();
      expect(theirs).toHaveLength(7);
      expect(theirs.every((entry: typeof empty) => entry.sent + entry.received === 0)).toBe(true);
    } finally {
      vi.useRealTimers();
    }
  });

  it('forwards the log and the statistics with only the whitelisted query', async () => {
    process.env.API_ADAPTER_ENABLED = 'true';
    process.env.API_UPSTREAM_URL = 'https://api.example.com/v1';
    cookieStore.get.mockReturnValue({ value: 'A'.repeat(43) });
    fetchMock.mockImplementation(async () =>
      Response.json({ code: 0, message: 'success', data: [] })
    );
    const route = await loadRoute();
    activeOrganization = '7';

    await route.listMailLogRoute(request('/api/mail-messages'));
    await route.listMailLogRoute(
      request(
        '/api/mail-messages?q=%20100%25%20off%20&status=failed&user_id=9&mailbox_id=12&direction=outbound&per_page=500&page=3&sort=subject'
      )
    );
    await route.listMailLogRoute(request('/api/mail-messages?direction=&q=%20&page=x'));
    await route.listMailMessageStatsRoute(request('/api/mail-message-stats?user_id=9'));
    await route.listMailMessageStatsRoute(request('/api/mail-message-stats?days=30&since=2020'));

    const calls = fetchMock.mock.calls.map(([url, init]) => {
      const target = new URL(String(url));
      return `${init?.method ?? 'GET'} ${target.pathname}${target.search}`;
    });
    expect(calls).toEqual([
      'GET /v1/mail-messages?page=1&per_page=15',
      'GET /v1/mail-messages?page=3&per_page=100&direction=outbound&status=failed&mailbox_id=12&q=100%25+off',
      'GET /v1/mail-messages?page=1&per_page=15',
      'GET /v1/mail-message-stats?days=7',
      'GET /v1/mail-message-stats?days=30',
    ]);
    for (const [, init] of fetchMock.mock.calls) {
      expect(new Headers(init?.headers).get('organization-id')).toBe('7');
    }
    expect(getSessionUser).not.toHaveBeenCalled();
  });

  it('rejects an invalid log or statistics query before it reaches the API', async () => {
    process.env.API_ADAPTER_ENABLED = 'true';
    process.env.API_UPSTREAM_URL = 'https://api.example.com/v1';
    cookieStore.get.mockReturnValue({ value: 'A'.repeat(43) });
    const route = await loadRoute();

    for (const query of [
      '?direction=all',
      '?status=bounced',
      '?mailbox_id=0',
      `?q=${'a'.repeat(201)}`,
      '?status=sent&status=sent',
    ]) {
      await expectError(
        await route.listMailLogRoute(request(`/api/mail-messages${query}`)),
        400,
        'COMMON.INVALID_INPUT'
      );
    }
    for (const query of ['?days=0', '?days=91', '?days=week']) {
      await expectError(
        await route.listMailMessageStatsRoute(request(`/api/mail-message-stats${query}`)),
        400,
        'COMMON.INVALID_INPUT'
      );
    }
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
  describe('mailbox access in the mock', () => {
    // Seeded organization 1: user 1 demo-admin (owner), 2 demo-operator (admin), 3 demo-member.
    const as = (user: 'demo-admin' | 'demo-operator' | 'demo-member') =>
      getSessionUser.mockResolvedValue(mockUser(user));

    beforeEach(() => {
      process.env.MOCK_BFF_ENABLED = 'true';
    });

    const listed = async (route: MailboxRoute) =>
      (await (await route.listMailboxesRoute(request('/api/mailboxes'))).json()).data as {
        id: number;
        address: string;
        type: string;
        owner_user_id: number | null;
        awaiting_handover: boolean;
        sealed_before: string | null;
        webhooks_enabled: boolean;
        unread_count: number;
        access: Record<string, boolean>;
      }[];
    const inbox = (route: MailboxRoute, id: number) =>
      route.listMailMessagesRoute(
        request(`/api/mailboxes/${id}/messages?direction=inbound`),
        String(id)
      );
    const members = (route: MailboxRoute, id: number) =>
      route.listMailboxMembersRoute(request(`/api/mailboxes/${id}/members`), String(id));
    const setMember = (route: MailboxRoute, id: number, userId: number, permission: unknown) =>
      route.setMailboxMemberRoute(
        request(`/api/mailboxes/${id}/members/${userId}`, {
          method: 'PUT',
          body: JSON.stringify({ permission }),
        }),
        String(id),
        String(userId)
      );
    const removeMember = (route: MailboxRoute, id: number, userId: number) =>
      route.removeMailboxMemberRoute(
        request(`/api/mailboxes/${id}/members/${userId}`, { method: 'DELETE' }),
        String(id),
        String(userId)
      );
    const events = (route: MailboxRoute, id: number, query = '') =>
      route.listMailboxAccessEventsRoute(
        request(`/api/mailboxes/${id}/access-events${query}`),
        String(id)
      );
    const handOver = (route: MailboxRoute, id: number, successor: unknown) =>
      route.handOverMailboxRoute(
        request(`/api/mailboxes/${id}/handovers`, {
          method: 'POST',
          body: JSON.stringify({ successor_user_id: successor }),
        }),
        String(id)
      );
    const deleteSealed = (route: MailboxRoute, id: number, address: unknown) =>
      route.deleteMailboxSealedMessagesRoute(
        request(`/api/mailboxes/${id}/sealed-messages`, {
          method: 'DELETE',
          body: JSON.stringify({ confirm_address: address }),
        }),
        String(id)
      );
    const webhooks = (route: MailboxRoute, id: number, enabled: unknown) =>
      route.setMailboxWebhookSettingRoute(
        request(`/api/mailboxes/${id}/webhook-setting`, {
          method: 'PUT',
          body: JSON.stringify({ enabled }),
        }),
        String(id)
      );
    const personal = async (route: MailboxRoute, localPart: string, owner: number) =>
      (
        await (
          await create(route, { local_part: localPart, type: 'personal', owner_user_id: owner })
        ).json()
      ).data as { id: number; address: string };
    const shared = async (
      route: MailboxRoute,
      localPart: string,
      list: { user_id: number; permission: string }[]
    ) =>
      (await (await create(route, { local_part: localPart, type: 'shared', members: list })).json())
        .data as { id: number; address: string };

    it('creates personal and shared mailboxes and rejects invalid access', async () => {
      const route = await loadRoute();
      as('demo-admin');

      const own = await create(route, { local_part: 'riley', type: 'personal', owner_user_id: 3 });
      expect(own.status).toBe(201);
      await expect(own.json()).resolves.toMatchObject({
        data: {
          type: 'personal',
          owner_user_id: 3,
          awaiting_handover: false,
          sealed_before: null,
          webhooks_enabled: false,
          unread_count: 0,
          // An admin administers but does not read a personal mailbox.
          access: {
            view: false,
            send: false,
            manage_members: false,
            administer: true,
            see_history: true,
          },
        },
      });
      const team = await create(route, {
        local_part: 'team',
        type: 'shared',
        members: [{ user_id: 3, permission: 'view' }],
      });
      expect(team.status).toBe(201);
      await expect(team.json()).resolves.toMatchObject({
        data: {
          type: 'shared',
          owner_user_id: null,
          access: { view: false, send: false, manage_members: true, administer: true },
        },
      });

      for (const body of [
        // `type` is named so the helper adds no default access; JSON drops the undefined value.
        { local_part: 'a1', type: undefined },
        { local_part: 'a2', type: 'group', owner_user_id: 3 },
        { local_part: 'a3', type: 'personal' },
        { local_part: 'a4', type: 'personal', owner_user_id: 99 },
        { local_part: 'a5', type: 'personal', owner_user_id: 3, members: [] },
        { local_part: 'a6', type: 'shared', members: [] },
        {
          local_part: 'a7',
          type: 'shared',
          owner_user_id: 3,
          members: [{ user_id: 3, permission: 'view' }],
        },
        {
          local_part: 'a8',
          type: 'shared',
          members: [
            { user_id: 3, permission: 'view' },
            { user_id: 3, permission: 'send' },
          ],
        },
        { local_part: 'a9', type: 'shared', members: [{ user_id: 3, permission: 'owner' }] },
        { local_part: 'a10', type: 'shared', members: [{ user_id: 99, permission: 'view' }] },
      ]) {
        await expectError(await create(route, body), 422, 'MAILBOX.ACCESS_INVALID');
      }
      expect((await listed(route)).map(item => item.address)).toEqual([
        'riley@mail.example.com',
        'team@mail.example.com',
      ]);

      // A member may not create one, with or without access in the body.
      as('demo-member');
      await expectError(
        await create(route, { local_part: 'mine', type: 'personal', owner_user_id: 3 }),
        403,
        'PERMISSION.DENIED'
      );
    });

    it('answers 404 without any access and 403 without the needed one', async () => {
      const route = await loadRoute();
      as('demo-admin');
      const rileyBox = await personal(route, 'riley', 3);
      const viewBox = await shared(route, 'viewers', [{ user_id: 3, permission: 'view' }]);
      const hidden = await shared(route, 'hidden', [{ user_id: 2, permission: 'send' }]);

      // The member sees only what they may access, with what they may do there.
      as('demo-member');
      const seen = await listed(route);
      expect(seen.map(item => [item.address, item.access])).toEqual([
        [
          rileyBox.address,
          { view: true, send: true, manage_members: false, administer: false, see_history: true },
        ],
        [
          viewBox.address,
          { view: true, send: false, manage_members: false, administer: false, see_history: false },
        ],
      ]);
      expect((await inbox(route, rileyBox.id)).status).toBe(200);
      expect((await inbox(route, viewBox.id)).status).toBe(200);
      await expectError(
        await send(route, viewBox.id, { to: ['bob@example.com'], subject: 'Hi', text: 'Body' }),
        403,
        'PERMISSION.DENIED'
      );
      const viewInbox = await (await inbox(route, viewBox.id)).json();
      const welcome = String(viewInbox.data[0].id);
      await expectError(
        await route.deleteMailMessageRoute(
          request(`/api/mailboxes/${viewBox.id}/messages/${welcome}`, { method: 'DELETE' }),
          String(viewBox.id),
          welcome
        ),
        403,
        'PERMISSION.DENIED'
      );
      // Marking read needs view only.
      const marked = await route.updateMailMessageRoute(
        request(`/api/mailboxes/${viewBox.id}/messages/${welcome}`, {
          method: 'PUT',
          body: JSON.stringify({ read: true }),
        }),
        String(viewBox.id),
        welcome
      );
      expect(marked.status).toBe(200);
      await expectError(await inbox(route, hidden.id), 404, 'MAILBOX.NOT_FOUND');
      await expectError(await members(route, hidden.id), 404, 'MAILBOX.NOT_FOUND');
      await expectError(await events(route, hidden.id), 404, 'MAILBOX.NOT_FOUND');
      await expectError(await webhooks(route, hidden.id, true), 404, 'MAILBOX.NOT_FOUND');
      await expectError(
        await route.deleteMailboxRoute(
          request(`/api/mailboxes/${hidden.id}`, { method: 'DELETE' }),
          String(hidden.id)
        ),
        404,
        'MAILBOX.NOT_FOUND'
      );
      await expectError(
        await route.deleteMailboxRoute(
          request(`/api/mailboxes/${viewBox.id}`, { method: 'DELETE' }),
          String(viewBox.id)
        ),
        403,
        'PERMISSION.DENIED'
      );
      await expectError(await members(route, viewBox.id), 403, 'PERMISSION.DENIED');
      await expectError(await events(route, viewBox.id), 403, 'PERMISSION.DENIED');
      // The personal owner reads its history but has no members to manage.
      expect((await events(route, rileyBox.id)).status).toBe(200);
      await expectError(await members(route, rileyBox.id), 409, 'MAILBOX.ACCESS_CONFLICT');

      // An admin who is not a member sees every mailbox but reads none of them.
      as('demo-operator');
      expect((await listed(route)).map(item => item.address)).toEqual([
        rileyBox.address,
        viewBox.address,
        hidden.address,
      ]);
      await expectError(await inbox(route, rileyBox.id), 403, 'PERMISSION.DENIED');
      await expectError(await inbox(route, viewBox.id), 403, 'PERMISSION.DENIED');
      expect((await inbox(route, hidden.id)).status).toBe(200);
      // The log and statistics only cover mailboxes the caller reads.
      const log = await (await route.listMailLogRoute(request('/api/mail-messages'))).json();
      expect(
        new Set(log.data.map((item: { mailbox_address: string }) => item.mailbox_address))
      ).toEqual(new Set([hidden.address]));
    });

    it('keeps read state per person', async () => {
      const route = await loadRoute();
      as('demo-admin');
      const box = await shared(route, 'desk', [
        { user_id: 1, permission: 'send' },
        { user_id: 3, permission: 'send' },
      ]);
      const unread = async () =>
        (await listed(route)).find(item => item.id === box.id)?.unread_count;
      expect(await unread()).toBe(1);

      as('demo-member');
      expect(await unread()).toBe(1);
      const welcome = String((await (await inbox(route, box.id)).json()).data[0].id);
      await route.updateMailMessageRoute(
        request(`/api/mailboxes/${box.id}/messages/${welcome}`, {
          method: 'PUT',
          body: JSON.stringify({ read: true }),
        }),
        String(box.id),
        welcome
      );
      expect(await unread()).toBe(0);
      await expect((await inbox(route, box.id)).json()).resolves.toMatchObject({
        data: [{ read: true }],
      });

      as('demo-admin');
      expect(await unread()).toBe(1);
      await expect((await inbox(route, box.id)).json()).resolves.toMatchObject({
        data: [{ read: false }],
      });
      // Without view the count is 0.
      as('demo-operator');
      expect(await unread()).toBe(0);

      // Outbound mail is read for everyone, and marking it unread changes nothing.
      as('demo-member');
      const sent = (
        await (
          await send(route, box.id, { to: ['bob@example.com'], subject: 'Out', text: 'Body' })
        ).json()
      ).data;
      const unmarked = await route.updateMailMessageRoute(
        request(`/api/mailboxes/${box.id}/messages/${sent.id}`, {
          method: 'PUT',
          body: JSON.stringify({ read: false }),
        }),
        String(box.id),
        String(sent.id)
      );
      await expect(unmarked.json()).resolves.toMatchObject({ data: { read: true } });
      as('demo-admin');
      expect(await unread()).toBe(1);
    });

    it('lists, sets, and removes members and records the history', async () => {
      const route = await loadRoute();
      as('demo-admin');
      const box = await shared(route, 'support-team', [{ user_id: 3, permission: 'manage' }]);

      // A manage member runs the members, including granting manage.
      as('demo-member');
      const set = await setMember(route, box.id, 2, 'view');
      expect(set.status).toBe(200);
      await expect(set.json()).resolves.toMatchObject({
        data: { user_id: 2, permission: 'view', granted_by: 3 },
      });
      expect((await setMember(route, box.id, 2, 'manage')).status).toBe(200);
      await expectError(await setMember(route, box.id, 2, 'owner'), 422, 'MAILBOX.ACCESS_INVALID');
      await expectError(await setMember(route, box.id, 99, 'view'), 422, 'MAILBOX.ACCESS_INVALID');
      const listing = await (await members(route, box.id)).json();
      expect(
        listing.data.map((item: { user_id: number; permission: string }) => [
          item.user_id,
          item.permission,
        ])
      ).toEqual([
        [2, 'manage'],
        [3, 'manage'],
      ]);
      expect((await removeMember(route, box.id, 2)).status).toBe(204);
      await expectError(await removeMember(route, box.id, 2), 404, 'MAILBOX.NOT_FOUND');

      const history = await (await events(route, box.id, '?page=1&per_page=2')).json();
      expect(history.meta).toMatchObject({ current_page: 1, per_page: 2, total: 5, last_page: 3 });
      expect(history.data.map((item: { type: string }) => item.type)).toEqual([
        'member_removed',
        'member_set',
      ]);
      expect(history.data[0]).toMatchObject({ actor_user_id: 3, subject_user_id: 2, detail: {} });
      expect(history.data[1]).toMatchObject({
        subject_user_id: 2,
        detail: { permission: 'manage' },
      });
      const oldest = await (await events(route, box.id, '?page=3&per_page=2')).json();
      expect(oldest.data.map((item: { type: string }) => item.type)).toEqual(['created']);
      expect(oldest.data[0]).toMatchObject({ actor_user_id: 1, detail: { type: 'shared' } });

      // A personal mailbox has no members, for an admin too.
      as('demo-admin');
      const own = await personal(route, 'riley', 3);
      for (const response of [
        await members(route, own.id),
        await setMember(route, own.id, 1, 'view'),
        await removeMember(route, own.id, 3),
        await webhooks(route, own.id, true),
      ]) {
        await expectError(response, 409, 'MAILBOX.ACCESS_CONFLICT');
      }
    });

    it('hands a personal mailbox over and seals the earlier mail', async () => {
      const route = await loadRoute();
      as('demo-admin');
      const box = await personal(route, 'riley', 3);

      // The owner may not hand over their own mailbox.
      as('demo-member');
      await expectError(await handOver(route, box.id, 2), 403, 'PERMISSION.DENIED');
      await expectError(await deleteSealed(route, box.id, box.address), 403, 'PERMISSION.DENIED');

      as('demo-admin');
      await expectError(await handOver(route, box.id, 99), 422, 'MAILBOX.ACCESS_INVALID');
      await expectError(
        await deleteSealed(route, box.id, box.address),
        409,
        'MAILBOX.ACCESS_CONFLICT'
      );
      const handed = await handOver(route, box.id, 2);
      expect(handed.status).toBe(201);
      const after = (await handed.json()).data;
      expect(after).toMatchObject({
        type: 'shared',
        owner_user_id: null,
        awaiting_handover: false,
        access: { view: false, manage_members: true, administer: true },
      });
      expect(after.sealed_before).toEqual(expect.any(String));
      await expectError(await handOver(route, box.id, 2), 409, 'MAILBOX.ACCESS_CONFLICT');

      // The former owner keeps nothing.
      as('demo-member');
      expect((await listed(route)).map(item => item.id)).not.toContain(box.id);
      await expectError(await inbox(route, box.id), 404, 'MAILBOX.NOT_FOUND');

      // The successor sends from it but sees nothing from before the handover.
      as('demo-operator');
      const successorInbox = await (await inbox(route, box.id)).json();
      expect(successorInbox.data).toEqual([]);
      expect(
        (await send(route, box.id, { to: ['bob@example.com'], subject: 'New', text: 'Body' }))
          .status
      ).toBe(201);
      const listing = await (await members(route, box.id)).json();
      expect(listing.data).toMatchObject([{ user_id: 2, permission: 'send', granted_by: 1 }]);
      const history = await (await events(route, box.id)).json();
      expect(history.data[0]).toMatchObject({
        type: 'handed_over',
        actor_user_id: 1,
        subject_user_id: 2,
        detail: { former_owner_user_id: 3, sealed_before: after.sealed_before },
      });

      // Deleting the sealed history needs the exact address, ignoring case.
      await expectError(
        await deleteSealed(route, box.id, 'someone@mail.example.com'),
        422,
        'MAILBOX.ACCESS_INVALID'
      );
      const deleted = await deleteSealed(route, box.id, box.address.toUpperCase());
      expect(deleted.status).toBe(200);
      await expect(deleted.json()).resolves.toMatchObject({
        data: { deleted_messages: 1, deleted_broadcasts: 0 },
      });
      const latest = await (await events(route, box.id)).json();
      expect(latest.data[0]).toMatchObject({
        type: 'sealed_deleted',
        subject_user_id: null,
        detail: { deleted_messages: 1, deleted_broadcasts: 0 },
      });
      // The boundary stays and the new mail is untouched.
      const sent = await (
        await route.listMailMessagesRoute(
          request(`/api/mailboxes/${box.id}/messages?direction=outbound`),
          String(box.id)
        )
      ).json();
      expect(sent.data).toHaveLength(1);
      expect((await listed(route)).find(item => item.id === box.id)?.sealed_before).toBe(
        after.sealed_before
      );
    });

    it('shows a personal mailbox whose owner left as awaiting handover', async () => {
      const route = await loadRoute();
      as('demo-admin');
      const box = await personal(route, 'riley', 3);
      const { mockOrganizationStore } =
        await import('@/features/organization/server/mock-organization-store');
      expect(mockOrganizationStore.removeMember(mockUser('demo-admin'), 1, 3)).toBe(true);

      const item = (await listed(route)).find(entry => entry.id === box.id);
      expect(item).toMatchObject({ type: 'personal', awaiting_handover: true });
      expect((await handOver(route, box.id, 2)).status).toBe(201);
      expect((await listed(route)).find(entry => entry.id === box.id)?.awaiting_handover).toBe(
        false
      );
    });

    it('switches organization webhooks of a shared mailbox and records changes only', async () => {
      const route = await loadRoute();
      as('demo-admin');
      const box = await shared(route, 'events', [
        { user_id: 3, permission: 'send' },
        { user_id: 2, permission: 'manage' },
      ]);
      expect((await listed(route))[0].webhooks_enabled).toBe(false);

      as('demo-member');
      await expectError(await webhooks(route, box.id, true), 403, 'PERMISSION.DENIED');
      as('demo-operator');
      const on = await webhooks(route, box.id, true);
      expect(on.status).toBe(200);
      await expect(on.json()).resolves.toMatchObject({ data: { webhooks_enabled: true } });
      expect((await webhooks(route, box.id, true)).status).toBe(200);
      await expectError(await webhooks(route, box.id, 'yes'), 422, 'COMMON.VALIDATION_FAILED');
      const history = await (await events(route, box.id)).json();
      expect(
        history.data
          .filter((item: { type: string }) => item.type === 'webhooks_changed')
          .map((item: { actor_user_id: number; detail: unknown }) => [
            item.actor_user_id,
            item.detail,
          ])
      ).toEqual([[2, { enabled: true }]]);
    });
  });

  it('forwards the access routes to the fixed API paths', async () => {
    process.env.API_ADAPTER_ENABLED = 'true';
    process.env.API_UPSTREAM_URL = 'https://api.example.com/v1';
    cookieStore.get.mockReturnValue({ value: 'A'.repeat(43) });
    fetchMock.mockImplementation(async () => Response.json({ code: 0, message: 'ok', data: [] }));
    const route = await loadRoute();

    await route.listMailboxMembersRoute(request('/api/mailboxes/4/members'), '4');
    await route.setMailboxMemberRoute(
      request('/api/mailboxes/4/members/9', {
        method: 'PUT',
        body: JSON.stringify({ permission: 'view', extra: 1 }),
      }),
      '4',
      '9'
    );
    await route.removeMailboxMemberRoute(
      request('/api/mailboxes/4/members/9', { method: 'DELETE' }),
      '4',
      '9'
    );
    await route.listMailboxAccessEventsRoute(
      request('/api/mailboxes/4/access-events?page=2&per_page=500&evil=1'),
      '4'
    );
    await route.handOverMailboxRoute(
      request('/api/mailboxes/4/handovers', {
        method: 'POST',
        body: JSON.stringify({ successor_user_id: 9 }),
      }),
      '4'
    );
    await route.deleteMailboxSealedMessagesRoute(
      request('/api/mailboxes/4/sealed-messages', {
        method: 'DELETE',
        body: JSON.stringify({ confirm_address: 'a@mail.example.com' }),
      }),
      '4'
    );
    await route.setMailboxWebhookSettingRoute(
      request('/api/mailboxes/4/webhook-setting', {
        method: 'PUT',
        body: JSON.stringify({ enabled: false }),
      }),
      '4'
    );
    await create(route, {
      local_part: 'team',
      type: 'shared',
      members: [{ user_id: 9, permission: 'send' }],
    });

    const calls = fetchMock.mock.calls.map(([url, init]) => {
      const parsed = new URL(String(url));
      return `${init?.method} ${parsed.pathname}${parsed.search}`;
    });
    expect(calls).toEqual([
      'GET /v1/mailboxes/4/members',
      'PUT /v1/mailboxes/4/members/9',
      'DELETE /v1/mailboxes/4/members/9',
      'GET /v1/mailboxes/4/access-events?page=2&per_page=100',
      'POST /v1/mailboxes/4/handovers',
      'DELETE /v1/mailboxes/4/sealed-messages',
      'PUT /v1/mailboxes/4/webhook-setting',
      'POST /v1/mailboxes',
    ]);
    const bodies = fetchMock.mock.calls.map(([, init]) =>
      init?.body === undefined ? undefined : JSON.parse(String(init.body))
    );
    expect(bodies).toEqual([
      undefined,
      { permission: 'view' },
      undefined,
      undefined,
      { successor_user_id: 9 },
      { confirm_address: 'a@mail.example.com' },
      { enabled: false },
      { local_part: 'team', type: 'shared', members: [{ user_id: 9, permission: 'send' }] },
    ]);
    for (const [, init] of fetchMock.mock.calls) {
      expect(new Headers(init?.headers).get('organization-id')).toBe('1');
    }
  });

  it('rejects malformed access bodies and ids before they reach the API', async () => {
    process.env.API_ADAPTER_ENABLED = 'true';
    process.env.API_UPSTREAM_URL = 'https://api.example.com/v1';
    cookieStore.get.mockReturnValue({ value: 'A'.repeat(43) });
    const route = await loadRoute();

    const put = (body: unknown, userId = '9') =>
      route.setMailboxMemberRoute(
        request(`/api/mailboxes/4/members/${userId}`, {
          method: 'PUT',
          body: JSON.stringify(body),
        }),
        '4',
        userId
      );
    await expectError(await put({}), 422, 'COMMON.VALIDATION_FAILED');
    await expectError(await put({ permission: 7 }), 422, 'COMMON.VALIDATION_FAILED');
    await expectError(await put({ permission: 'view' }, 'abc'), 400, 'COMMON.INVALID_INPUT');
    await expectError(
      await route.handOverMailboxRoute(
        request('/api/mailboxes/4/handovers', {
          method: 'POST',
          body: JSON.stringify({ successor_user_id: '9' }),
        }),
        '4'
      ),
      422,
      'COMMON.VALIDATION_FAILED'
    );
    await expectError(
      await route.deleteMailboxSealedMessagesRoute(
        request('/api/mailboxes/4/sealed-messages', { method: 'DELETE', body: JSON.stringify({}) }),
        '4'
      ),
      422,
      'COMMON.VALIDATION_FAILED'
    );
    await expectError(
      await route.setMailboxWebhookSettingRoute(
        request('/api/mailboxes/4/webhook-setting', {
          method: 'PUT',
          body: JSON.stringify({ enabled: 'true' }),
        }),
        '4'
      ),
      422,
      'COMMON.VALIDATION_FAILED'
    );
    await expectError(
      await create(route, { local_part: 'x', type: 'shared', members: 'everyone' }),
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
  const contacts = await import('@/features/mailbox/server/mock-contact-store');
  contacts.mockContactStore.reset();
  const broadcasts = await import('@/features/mailbox/server/mock-broadcast-store');
  broadcasts.mockBroadcastStore.reset();
  return route;
}

/**
 * Creates a mailbox. In mock mode a body without `type` becomes a shared mailbox every current
 * member of the active organization manages, so tests about other rules read and send as before.
 */
async function create(route: MailboxRoute, body: Record<string, unknown>) {
  let payload = body;
  if (process.env.API_ADAPTER_ENABLED !== 'true' && !('type' in body) && 'local_part' in body) {
    const { mockOrganizationStore } =
      await import('@/features/organization/server/mock-organization-store');
    const members = mockOrganizationStore
      .memberUserIds(Number(activeOrganization))
      .map(userId => ({ user_id: userId, permission: 'manage' }));
    payload = { ...body, type: 'shared', members };
  }
  return route.createMailboxRoute(
    request('/api/mailboxes', { method: 'POST', body: JSON.stringify(payload) })
  );
}

function send(route: MailboxRoute, mailboxId: number, body: Record<string, unknown>) {
  return route.sendMailMessageRoute(
    request(`/api/mailboxes/${mailboxId}/messages`, { method: 'POST', body: JSON.stringify(body) }),
    String(mailboxId)
  );
}

function connectDomain(route: MailboxRoute, body: Record<string, unknown>) {
  return route.connectMailDomainRoute(
    request('/api/mail-domains', { method: 'POST', body: JSON.stringify(body) })
  );
}

function checkDomain(route: MailboxRoute, domainId: number) {
  return route.refreshMailDomainRoute(
    request(`/api/mail-domains/${domainId}/checks`, { method: 'POST' }),
    String(domainId),
    'checks'
  );
}

function receiving(route: MailboxRoute, domainId: number) {
  return route.enableMailDomainReceivingRoute(
    request(`/api/mail-domains/${domainId}/receiving`, { method: 'POST' }),
    String(domainId)
  );
}

function preview(route: MailboxRoute, body: Record<string, unknown>) {
  return route.previewMailDomainRoute(
    request('/api/mail-domain-previews', { method: 'POST', body: JSON.stringify(body) })
  );
}

function deleteZone(route: MailboxRoute, zoneId: number) {
  return route.deleteMailZoneRoute(
    request(`/api/mail-zones/${zoneId}`, { method: 'DELETE' }),
    String(zoneId)
  );
}

function listRecords(route: MailboxRoute, zoneId: number) {
  return route.listMailDnsRecordsRoute(
    request(`/api/mail-zones/${zoneId}/dns-records`),
    String(zoneId)
  );
}

function createRecord(route: MailboxRoute, zoneId: number, body: Record<string, unknown>) {
  return route.createMailDnsRecordRoute(
    request(`/api/mail-zones/${zoneId}/dns-records`, {
      method: 'POST',
      body: JSON.stringify(body),
    }),
    String(zoneId)
  );
}

function updateRecord(
  route: MailboxRoute,
  zoneId: number,
  recordId: string,
  body: Record<string, unknown>
) {
  return route.updateMailDnsRecordRoute(
    request(`/api/mail-zones/${zoneId}/dns-records/${recordId}`, {
      method: 'PATCH',
      body: JSON.stringify(body),
    }),
    String(zoneId),
    recordId
  );
}

function deleteRecord(route: MailboxRoute, zoneId: number, recordId: string) {
  return route.deleteMailDnsRecordRoute(
    request(`/api/mail-zones/${zoneId}/dns-records/${encodeURIComponent(recordId)}`, {
      method: 'DELETE',
    }),
    String(zoneId),
    recordId
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

/**
 * A second organization owned by `owner`, next to the seeded one (id 1: demo-admin is its owner,
 * demo-operator an admin, demo-member a member). Created once per test; returns its id.
 */
async function secondOrganization(owner = 'demo-admin'): Promise<string> {
  const { mockOrganizationStore } =
    await import('@/features/organization/server/mock-organization-store');
  const actor = mockUser(owner);
  const existing = mockOrganizationStore
    .list(actor, 1, 100)
    .items.find(item => item.slug === 'second');
  if (existing) return String(existing.id);
  const created = mockOrganizationStore.create(actor, { name: 'Second', slug: 'second' });
  if (typeof created === 'string') throw new Error(created);
  return String(created.id);
}

/**
 * `organization` is the `Organization-Id` header: the active organization by default, `null` for
 * no header, and an array for a repeated header.
 */
function request(
  path: string,
  options: {
    method?: string;
    body?: string;
    origin?: string;
    organization?: string | string[] | null;
  } = {}
): Request {
  const result = new Request(`https://app.example.com${path}`, {
    method: options.method ?? 'GET',
    body: options.body,
  });
  const organization =
    options.organization === undefined ? activeOrganization : options.organization;
  for (const value of organization === null ? [] : [organization].flat()) {
    result.headers.append('organization-id', value);
  }
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

function base64(value: string): string {
  return Buffer.from(value).toString('base64');
}
