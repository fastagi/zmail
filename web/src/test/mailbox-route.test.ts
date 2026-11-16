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
          max_mailboxes: 50,
          provider: 'log',
          inbound_enabled: false,
          daily_send_limit: 200,
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
    await expectError(await remove(owned.id), 403, 'PERMISSION.DENIED');
    await expectError(await remove(999), 403, 'PERMISSION.DENIED');
    expect(await listed()).toEqual(['owner-box@mail.example.com', 'admin-box@mail.example.com']);

    // The member still reads, sends, marks, and deletes messages in every mailbox.
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

    // Read state is shared: what the member read is read for the owner too.
    getSessionUser.mockResolvedValue(mockUser('demo-admin'));
    const seen = await route.getMailMessageRoute(
      request(`${path}/${welcomeId}`),
      String(owned.id),
      welcomeId
    );
    await expect(seen.json()).resolves.toMatchObject({ data: { read: true } });

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
