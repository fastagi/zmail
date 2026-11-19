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

type DeveloperRoute = typeof import('@/features/developer/server/developer-route');

const signupTemplate = {
  name: 'Sign-up code',
  subject: '{{app}} code: {{code}}',
  text: 'Your code is {{code}}. Reset at {{link}}.',
  kind: 'transactional' as const,
  key: 'signup-code',
  from_address: 'no-reply@mail.example.com',
  from_name: 'Acme',
  variable_definitions: [
    { name: 'code', secret: true, required: true },
    { name: 'app', secret: false, required: true },
    { name: 'link', secret: true, required: false },
  ],
  sample_variables: { app: 'Acme' },
};

describe('developer console browser route boundary', () => {
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
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
    vi.resetModules();
    for (const key of managedKeys) {
      delete process.env[key];
      if (originalEnv[key] !== undefined) process.env[key] = originalEnv[key];
    }
  });

  describe('mock backend', () => {
    beforeEach(() => {
      process.env.MOCK_BFF_ENABLED = 'true';
      getSessionUser.mockResolvedValue(mockUser('demo-admin'));
    });

    it('simulates a test-mode send to the caller and lists it masked in the test log only', async () => {
      const route = await loadRoute();

      const sent = await testSend(route, {
        template: 'signup-code',
        mode: 'test',
        variables: { code: '482913', link: 'https://u:p@reset.acme.com/t?code=1' },
      });
      expect(sent.status).toBe(202);
      expect(sent.headers.get('cache-control')).toContain('no-store');
      const message = (await sent.json()).data;
      // A test message never reaches a provider: it is simulated, with no provider call.
      expect(message).toMatchObject({
        mode: 'test',
        simulated: true,
        attempts: 0,
        template: 'signup-code',
        from: { address: 'no-reply@mail.example.com', name: 'Acme' },
        to: 'demo-admin@example.com',
        to_masked: 'de••@example.com',
        subject: 'Acme code: ••••••',
        variables: { app: 'Acme', code: '••••••', link: 'reset.acme.com/••••' },
        status: 'sent',
        queued_reason: null,
        failure_code: null,
        last_event: 'delivered',
      });
      expect(message.events.map((event: { type: string }) => event.type)).toEqual([
        'accepted',
        'delivered',
      ]);
      for (const event of message.events) expect(event.detail).toBe('simulated');
      expect(JSON.stringify(message)).not.toContain('482913');

      const live = await (
        await route.listTransactionalMailLogRoute(request('/api/transactional-mail-log'))
      ).json();
      expect(live.data).toEqual([]);
      expect(live.meta).toEqual({ next_cursor: null, total_24h: 0 });

      const test = await (
        await route.listTransactionalMailLogRoute(request('/api/transactional-mail-log?mode=test'))
      ).json();
      expect(test.meta).toEqual({ next_cursor: null, total_24h: 1 });
      expect(test.data).toHaveLength(1);
      expect(test.data[0]).toEqual({
        id: message.id,
        mode: 'test',
        simulated: true,
        template: 'signup-code',
        to_masked: 'de••@example.com',
        subject: 'Acme code: ••••••',
        status: 'sent',
        queued_reason: null,
        failure_code: null,
        last_event: 'delivered',
        idempotency_key: message.idempotency_key,
        created_at: message.created_at,
        sent_at: message.sent_at,
      });

      const entryResponse = await route.getTransactionalMailLogEntryRoute(
        request(`/api/transactional-mail-log/${message.id}`),
        String(message.id)
      );
      expect(entryResponse.status).toBe(200);
      const entry = (await entryResponse.json()).data;
      expect(entry).not.toHaveProperty('to');
      expect(entry).toMatchObject({
        from: { address: 'no-reply@mail.example.com', name: 'Acme' },
        variables: { app: 'Acme', code: '••••••', link: 'reset.acme.com/••••' },
        events: message.events,
      });
    });

    it('records a live send without simulating it', async () => {
      const route = await loadRoute();

      const sent = await testSend(route, {
        template: 'signup-code',
        mode: 'live',
        variables: { code: '1' },
      });
      expect(sent.status).toBe(202);
      expect((await sent.json()).data).toMatchObject({ mode: 'live', simulated: false });
      const live = await (
        await route.listTransactionalMailLogRoute(request('/api/transactional-mail-log'))
      ).json();
      expect(live.data).toHaveLength(1);
    });

    it('refuses the sixth test send of a user within a minute', async () => {
      vi.useFakeTimers({ toFake: ['Date'] });
      vi.setSystemTime(new Date('2026-11-19T10:00:10Z'));
      const route = await loadRoute();
      const body = { template: 'signup-code', mode: 'test', variables: { code: '1' } };

      for (let index = 0; index < 5; index += 1) {
        expect((await testSend(route, body)).status).toBe(202);
      }
      const refused = await testSend(route, body);
      expect(refused.status).toBe(429);
      expect(refused.headers.get('retry-after')).toBe('50');
      await expect(refused.json()).resolves.toMatchObject({ error_code: 'COMMON.RATE_LIMITED' });

      vi.setSystemTime(new Date('2026-11-19T10:01:00Z'));
      expect((await testSend(route, body)).status).toBe(202);
    });

    it('answers 503 MAILBOX.TRANSACTIONAL_DISABLED while the instance switch is off', async () => {
      const route = await loadRoute();
      const { mockTransactionalStore } =
        await import('@/features/developer/server/mock-transactional-store');
      mockTransactionalStore.setEnabled(false);

      const response = await testSend(route, { template: 'signup-code', mode: 'test' });
      expect(response.status).toBe(503);
      await expect(response.json()).resolves.toMatchObject({
        error_code: 'MAILBOX.TRANSACTIONAL_DISABLED',
      });
    });

    it('refuses unusable test sends with the contract error codes', async () => {
      const route = await loadRoute();
      const { mockMailboxStore } = await import('@/features/mailbox/server/mock-mailbox-store');
      mockMailboxStore.createTemplate(1, { name: 'Newsletter', subject: 'Hi', text: 'Hello' });

      const cases: [Record<string, unknown>, number, string][] = [
        [{ template: 'missing', mode: 'test' }, 404, 'MAILBOX.TEMPLATE_NOT_FOUND'],
        [{ template: 'Bad Key', mode: 'test' }, 422, 'COMMON.VALIDATION_FAILED'],
        [{ template: 'signup-code', mode: 'sandbox' }, 422, 'COMMON.VALIDATION_FAILED'],
        [
          { template: 'signup-code', mode: 'test', to: 'x@example.com' },
          422,
          'COMMON.VALIDATION_FAILED',
        ],
        [
          { template: 'signup-code', mode: 'test', variables: { code: '1', extra: 'x' } },
          422,
          'MAILBOX.MESSAGE_INVALID',
        ],
        [
          { template: 'signup-code', mode: 'test', variables: { code: '' } },
          422,
          'MAILBOX.TEMPLATE_VARIABLES_MISSING',
        ],
      ];
      for (const [body, status, errorCode] of cases) {
        const response = await testSend(route, body);
        expect(response.status, JSON.stringify(body)).toBe(status);
        await expect(response.json()).resolves.toMatchObject({ error_code: errorCode });
      }
      // Nothing refused was stored.
      const test = await (
        await route.listTransactionalMailLogRoute(request('/api/transactional-mail-log?mode=test'))
      ).json();
      expect(test.data).toEqual([]);
    });

    it('counts the last 24 hours per template when the log names one', async () => {
      const route = await loadRoute();
      const { mockMailboxStore } = await import('@/features/mailbox/server/mock-mailbox-store');
      const welcome = mockMailboxStore.createTemplate(1, {
        ...signupTemplate,
        name: 'Welcome',
        key: 'welcome',
      });
      expect(welcome.ok).toBe(true);
      await testSend(route, { template: 'signup-code', mode: 'test', variables: { code: '1' } });
      await testSend(route, { template: 'signup-code', mode: 'test', variables: { code: '2' } });
      await testSend(route, { template: 'welcome', mode: 'test', variables: { code: '3' } });

      const read = async (query: string) =>
        (
          await (
            await route.listTransactionalMailLogRoute(
              request(`/api/transactional-mail-log?${query}`)
            )
          ).json()
        ).meta.total_24h;
      expect(await read('mode=test&per_page=1')).toBe(3);
      expect(await read('mode=test&per_page=1&template=signup-code')).toBe(2);
      // Only the template narrows the count; the outcome does not.
      expect(await read('mode=test&per_page=1&template=welcome&outcome=problem')).toBe(1);
      expect(await read('mode=live&per_page=1&template=welcome')).toBe(0);
    });

    it('reflects the instance switch in the mock mailbox domains', async () => {
      await loadRoute();
      const { mockMailboxStore } = await import('@/features/mailbox/server/mock-mailbox-store');
      const { mockTransactionalStore } =
        await import('@/features/developer/server/mock-transactional-store');
      expect(mockMailboxStore.domains(['acme.example']).map(d => d.transactional_enabled)).toEqual([
        true,
        true,
      ]);
      mockTransactionalStore.setEnabled(false);
      expect(mockMailboxStore.domains(['acme.example']).map(d => d.transactional_enabled)).toEqual([
        false,
        false,
      ]);
    });

    it('masks secrets as the API does', async () => {
      const { maskSecret } = await import('@/features/developer/server/mock-transactional-store');
      const cases: [string, string][] = [
        ['https://u:p@reset.acme.com/t?code=1', 'reset.acme.com/••••'],
        ['  HTTPS://Reset.ACME.com:8443/t  ', 'reset.acme.com/••••'],
        ['http://[::1]:8080/x', '::1/••••'],
        ['http://bücher.example/x', 'bücher.example/••••'],
        ['reset.acme.com/t', '••••••'],
        ['ftp://files.acme.com/x', '••••••'],
        ['https:reset.acme.com', '••••••'],
        ['https:///path', '••••••'],
        ['https://acme.com:port/x', '••••••'],
        ['482913', '••••••'],
      ];
      for (const [value, masked] of cases) expect(maskSecret(value), value).toBe(masked);
    });

    it('filters and pages the log newest first', async () => {
      const route = await loadRoute();
      for (let index = 0; index < 3; index += 1) {
        await testSend(route, { template: 'signup-code', mode: 'test', variables: { code: '1' } });
      }

      const first = await (
        await route.listTransactionalMailLogRoute(
          request('/api/transactional-mail-log?mode=test&per_page=2&outcome=delivered')
        )
      ).json();
      expect(first.data.map((entry: { id: number }) => entry.id)).toEqual([3, 2]);
      expect(first.meta).toEqual({ next_cursor: '2', total_24h: 3 });
      const second = await (
        await route.listTransactionalMailLogRoute(
          request('/api/transactional-mail-log?mode=test&per_page=2&cursor=2')
        )
      ).json();
      expect(second.data.map((entry: { id: number }) => entry.id)).toEqual([1]);
      expect(second.meta.next_cursor).toBeNull();

      const problems = await (
        await route.listTransactionalMailLogRoute(
          request('/api/transactional-mail-log?mode=test&outcome=problem')
        )
      ).json();
      expect(problems.data).toEqual([]);
      const byRecipient = await (
        await route.listTransactionalMailLogRoute(
          request('/api/transactional-mail-log?mode=test&to=%20Demo-Admin@Example.COM%20')
        )
      ).json();
      expect(byRecipient.data).toHaveLength(3);
    });

    it('refuses unusable log filters and unknown entries', async () => {
      const route = await loadRoute();

      for (const query of [
        'mode=sandbox',
        'status=bounced',
        'last_event=accepted',
        'outcome=maybe',
        'template=Bad%20Key',
        'cursor=abc',
        'per_page=0',
        'per_page=101',
        'since=yesterday',
        'since=2026-01-01T00:00:00Z&until=2026-03-01T00:00:00Z',
        'since=2026-03-01T00:00:00Z&until=2026-03-01T00:00:00Z',
        `to=${'a'.repeat(321)}`,
      ]) {
        const response = await route.listTransactionalMailLogRoute(
          request(`/api/transactional-mail-log?${query}`)
        );
        expect(response.status, query).toBe(400);
        await expect(response.json()).resolves.toMatchObject({
          error_code: 'COMMON.INVALID_INPUT',
        });
      }

      const missing = await route.getTransactionalMailLogEntryRoute(
        request('/api/transactional-mail-log/99'),
        '99'
      );
      expect(missing.status).toBe(404);
      await expect(missing.json()).resolves.toMatchObject({
        error_code: 'MAILBOX.TRANSACTIONAL_MESSAGE_NOT_FOUND',
      });
      const malformed = await route.getTransactionalMailLogEntryRoute(
        request('/api/transactional-mail-log/x'),
        'x'
      );
      expect(malformed.status).toBe(400);
    });

    it('keeps one organization from reading another one log', async () => {
      const route = await loadRoute();
      const sent = (
        await (
          await testSend(route, { template: 'signup-code', mode: 'test', variables: { code: '1' } })
        ).json()
      ).data;

      const response = await route.getTransactionalMailLogEntryRoute(
        request(`/api/transactional-mail-log/${sent.id}`, { organization: '999' }),
        String(sent.id)
      );
      expect(response.status).toBe(404);
      await expect(response.json()).resolves.toMatchObject({
        error_code: 'ORGANIZATION.NOT_FOUND',
      });
    });

    it('shows an admin the three lanes in order, counting only live provider sends', async () => {
      const route = await loadRoute();
      // A live console send to a normal address reaches the (log) provider; a test one does not.
      await testSend(route, { template: 'signup-code', mode: 'live', variables: { code: '1' } });
      await testSend(route, { template: 'signup-code', mode: 'test', variables: { code: '2' } });

      const response = await route.listMailSendLanesRoute(request('/api/mail-send-lanes'));
      expect(response.status).toBe(200);
      expect(response.headers.get('cache-control')).toContain('no-store');
      const body = await response.json();
      expect(body.data.window_hours).toBe(24);
      expect(Number.isNaN(Date.parse(body.data.computed_at))).toBe(false);
      expect(body.data.lanes).toEqual([
        { lane: 'transactional', limit: 200, used: 1, waiting: 0 },
        { lane: 'broadcast', limit: 200, used: 0, waiting: 0 },
        { lane: 'personal', limit: 200, used: 0, waiting: 0 },
      ]);
    });

    it('lists the suppression list newest first in keyset pages', async () => {
      const route = await loadRoute();

      const first = await route.listMailSuppressionsRoute(
        request('/api/mail-suppressions?per_page=2')
      );
      expect(first.status).toBe(200);
      expect(first.headers.get('cache-control')).toContain('no-store');
      const firstBody = await first.json();
      expect(firstBody.data.map((entry: { email: string }) => entry.email)).toEqual([
        'full-box@example.org',
        'complainer@example.net',
      ]);
      expect(Object.keys(firstBody.data[0]).sort()).toEqual(
        ['created_at', 'email', 'id', 'reason', 'source'].sort()
      );
      expect(firstBody.meta).toEqual({ next_cursor: String(firstBody.data[1].id) });

      const second = await (
        await route.listMailSuppressionsRoute(
          request(`/api/mail-suppressions?per_page=2&cursor=${firstBody.meta.next_cursor}`)
        )
      ).json();
      expect(second.data.map((entry: { email: string }) => entry.email)).toEqual([
        'old-bounce@example.com',
      ]);
      expect(second.meta).toEqual({ next_cursor: null });
    });

    it('finds an exact address after trimming and lowercasing, and filters by reason', async () => {
      const route = await loadRoute();

      const exact = await (
        await route.listMailSuppressionsRoute(
          request('/api/mail-suppressions?email=%20Full-Box@Example.ORG%20')
        )
      ).json();
      expect(exact.data).toHaveLength(1);
      expect(exact.data[0]).toMatchObject({
        email: 'full-box@example.org',
        reason: 'bounced',
        source: 'transactional',
      });

      const partial = await (
        await route.listMailSuppressionsRoute(request('/api/mail-suppressions?email=full-box'))
      ).json();
      expect(partial.data).toEqual([]);

      const complained = await (
        await route.listMailSuppressionsRoute(request('/api/mail-suppressions?reason=complained'))
      ).json();
      expect(complained.data.map((entry: { email: string }) => entry.email)).toEqual([
        'complainer@example.net',
      ]);

      for (const query of ['reason=rejected', 'per_page=101', 'per_page=0', 'cursor=abc']) {
        const refused = await route.listMailSuppressionsRoute(
          request(`/api/mail-suppressions?${query}`)
        );
        expect(refused.status, query).toBe(400);
        await expect(refused.json()).resolves.toMatchObject({
          error_code: 'COMMON.INVALID_INPUT',
        });
      }
    });

    it('removes an entry with 204, and answers 204 again for one that is gone', async () => {
      const route = await loadRoute();
      const listed = await (
        await route.listMailSuppressionsRoute(request('/api/mail-suppressions'))
      ).json();
      const target = listed.data[0];

      for (let attempt = 0; attempt < 2; attempt += 1) {
        const removed = await removeSuppression(route, String(target.id));
        expect(removed.status).toBe(204);
        expect(await removed.text()).toBe('');
      }
      const unknown = await removeSuppression(route, '9999');
      expect(unknown.status).toBe(204);
      // Another organization's entry is never touched, and the answer discloses nothing.
      const foreign = await removeSuppression(route, String(listed.data[1].id), '999');
      expect(foreign.status).toBe(404);

      const after = await (
        await route.listMailSuppressionsRoute(request('/api/mail-suppressions'))
      ).json();
      expect(after.data.map((entry: { id: number }) => entry.id)).not.toContain(target.id);
      expect(after.data).toHaveLength(listed.data.length - 1);

      const malformed = await removeSuppression(route, 'x');
      expect(malformed.status).toBe(400);
    });

    it('refuses usage and the suppression list to a member', async () => {
      getSessionUser.mockResolvedValue(mockUser('demo-member'));
      const route = await loadRoute();

      for (const response of [
        await route.listMailSendLanesRoute(request('/api/mail-send-lanes')),
        await route.listMailSuppressionsRoute(request('/api/mail-suppressions')),
        await removeSuppression(route, '1'),
      ]) {
        expect(response.status).toBe(403);
        await expect(response.json()).resolves.toMatchObject({
          error_code: 'PERMISSION.DENIED',
        });
      }
    });

    it('rejects a cross-origin removal before authentication', async () => {
      const route = await loadRoute();
      getSessionUser.mockClear();
      const response = await route.removeMailSuppressionRoute(
        request('/api/mail-suppressions/1', {
          method: 'DELETE',
          origin: 'https://evil.example',
        }),
        '1'
      );
      expect(response.status).toBe(403);
      expect(getSessionUser).not.toHaveBeenCalled();
    });
  });

  it('stays unavailable when the mailbox feature is not selected', async () => {
    process.env.MOCK_BFF_ENABLED = 'true';
    process.env.NEXT_PUBLIC_OPTIONAL_FEATURES = 'organization';
    getSessionUser.mockResolvedValue(mockUser('demo-admin'));
    const route = await loadRoute();

    for (const response of [
      await route.listTransactionalMailLogRoute(request('/api/transactional-mail-log')),
      await testSend(route, { template: 'signup-code', mode: 'test' }),
    ]) {
      expect(response.status).toBe(503);
      await expect(response.json()).resolves.toMatchObject({
        error_code: 'COMMON.SERVICE_UNAVAILABLE',
      });
    }
    expect(getSessionUser).not.toHaveBeenCalled();
  });

  it('rejects cross-origin test sends before authentication or body parsing', async () => {
    process.env.MOCK_BFF_ENABLED = 'true';
    const route = await loadRoute();

    const response = await route.sendTransactionalTestRoute(
      request('/api/transactional-test-sends', {
        method: 'POST',
        body: '{',
        origin: 'https://evil.example',
      })
    );
    expect(response.status).toBe(403);
    expect(getSessionUser).not.toHaveBeenCalled();
  });

  it('requires a session', async () => {
    process.env.MOCK_BFF_ENABLED = 'true';
    getSessionUser.mockResolvedValue(null);
    const route = await loadRoute();

    const response = await route.listTransactionalMailLogRoute(
      request('/api/transactional-mail-log')
    );
    expect(response.status).toBe(401);
  });

  describe('go-api backend', () => {
    beforeEach(() => {
      process.env.API_ADAPTER_ENABLED = 'true';
      process.env.API_UPSTREAM_URL = 'https://api.example.com/v1';
      cookieStore.get.mockReturnValue({ value: 'A'.repeat(43) });
    });

    it('forwards the log with only the documented query parameters', async () => {
      fetchMock.mockImplementation(async () =>
        Response.json({
          code: 0,
          message: 'success',
          data: [],
          meta: { next_cursor: null, total_24h: 0 },
        })
      );
      const route = await loadRoute();

      await route.listTransactionalMailLogRoute(
        request('/api/transactional-mail-log', { organization: '7' })
      );
      await route.listTransactionalMailLogRoute(
        request(
          '/api/transactional-mail-log?mode=test&status=sent&last_event=bounced&outcome=problem&template=signup-code&to=%20LiLei@Example.COM%20&since=2026-11-12T00:00:00Z&until=2026-11-19T00:00:00Z&cursor=42&per_page=20&sort=id&user_id=9',
          { organization: '7' }
        )
      );
      await route.getTransactionalMailLogEntryRoute(
        request('/api/transactional-mail-log/42?mode=live', { organization: '7' }),
        '42'
      );

      const calls = fetchMock.mock.calls.map(([url, init]) => {
        const target = new URL(String(url));
        return `${init?.method ?? 'GET'} ${target.pathname}${target.search}`;
      });
      expect(calls).toEqual([
        'GET /v1/transactional-mail-log',
        'GET /v1/transactional-mail-log?mode=test&status=sent&last_event=bounced&outcome=problem&template=signup-code&to=lilei%40example.com&since=2026-11-12T00%3A00%3A00Z&until=2026-11-19T00%3A00%3A00Z&cursor=42&per_page=20',
        'GET /v1/transactional-mail-log/42',
      ]);
      for (const [, init] of fetchMock.mock.calls) {
        expect(new Headers(init?.headers).get('organization-id')).toBe('7');
      }
      expect(getSessionUser).not.toHaveBeenCalled();
    });

    it('forwards a test send with only its documented fields', async () => {
      fetchMock.mockResolvedValue(
        Response.json({ code: 0, message: 'success', data: { id: 1 } }, { status: 202 })
      );
      const route = await loadRoute();

      const response = await testSend(route, {
        template: 'signup-code',
        mode: 'live',
        variables: { code: '1' },
      });
      expect(response.status).toBe(202);
      const [url, init] = fetchMock.mock.calls[0];
      expect(`${init?.method} ${new URL(String(url)).pathname}`).toBe(
        'POST /v1/transactional-test-sends'
      );
      expect(JSON.parse(String(init?.body))).toEqual({
        template: 'signup-code',
        mode: 'live',
        variables: { code: '1' },
      });
    });

    it('passes upstream refusals through with their error code and Retry-After', async () => {
      const route = await loadRoute();
      for (const [status, errorCode] of [
        [403, 'PERMISSION.DENIED'],
        [429, 'COMMON.RATE_LIMITED'],
        [429, 'MAILBOX.RECIPIENT_RATE_LIMITED'],
        [503, 'MAILBOX.TRANSACTIONAL_DISABLED'],
      ] as const) {
        fetchMock.mockResolvedValueOnce(
          errorResponse(status, errorCode, status === 429 ? { 'retry-after': '7' } : {})
        );
        const response = await testSend(route, { template: 'signup-code', mode: 'test' });
        expect(response.status).toBe(status);
        if (status === 429) expect(response.headers.get('retry-after')).toBe('7');
        await expect(response.json()).resolves.toMatchObject({ error_code: errorCode });
      }
      fetchMock.mockResolvedValueOnce(errorResponse(403, 'PERMISSION.DENIED'));
      const read = await route.listTransactionalMailLogRoute(
        request('/api/transactional-mail-log')
      );
      expect(read.status).toBe(403);
      await expect(read.json()).resolves.toMatchObject({ error_code: 'PERMISSION.DENIED' });
    });

    it('forwards lane usage, the suppression list, and a removal', async () => {
      fetchMock
        .mockResolvedValueOnce(
          Response.json({
            code: 0,
            message: 'success',
            data: {
              lanes: [
                { lane: 'transactional', limit: 500, used: 37, waiting: 0 },
                { lane: 'broadcast', limit: 500, used: 500, waiting: 412 },
                { lane: 'personal', limit: 200, used: 12, waiting: 0 },
              ],
              window_hours: 24,
              computed_at: '2026-11-23T08:00:00Z',
            },
          })
        )
        .mockResolvedValueOnce(
          Response.json({ code: 0, message: 'success', data: [], meta: { next_cursor: null } })
        )
        .mockResolvedValueOnce(new Response(null, { status: 204 }));
      const route = await loadRoute();

      const lanes = await route.listMailSendLanesRoute(
        request('/api/mail-send-lanes?lane=broadcast', { organization: '7' })
      );
      expect(lanes.status).toBe(200);
      expect((await lanes.json()).data.lanes[1]).toEqual({
        lane: 'broadcast',
        limit: 500,
        used: 500,
        waiting: 412,
      });
      const list = await route.listMailSuppressionsRoute(
        request(
          '/api/mail-suppressions?email=%20LiLei@Example.COM%20&reason=bounced&cursor=42&per_page=20&sort=id',
          { organization: '7' }
        )
      );
      expect(list.status).toBe(200);
      const removed = await route.removeMailSuppressionRoute(
        request('/api/mail-suppressions/42', { method: 'DELETE', organization: '7' }),
        '42'
      );
      expect(removed.status).toBe(204);
      expect(removed.headers.get('cache-control')).toContain('no-store');

      const calls = fetchMock.mock.calls.map(([url, init]) => {
        const target = new URL(String(url));
        return `${init?.method ?? 'GET'} ${target.pathname}${target.search}`;
      });
      expect(calls).toEqual([
        'GET /v1/mail-send-lanes',
        'GET /v1/mail-suppressions?email=lilei%40example.com&reason=bounced&cursor=42&per_page=20',
        'DELETE /v1/mail-suppressions/42',
      ]);
      for (const [, init] of fetchMock.mock.calls) {
        expect(new Headers(init?.headers).get('organization-id')).toBe('7');
      }
      expect(getSessionUser).not.toHaveBeenCalled();
    });

    it('passes a member refusal of usage and the suppression list through', async () => {
      const route = await loadRoute();
      fetchMock
        .mockResolvedValueOnce(errorResponse(403, 'PERMISSION.DENIED'))
        .mockResolvedValueOnce(errorResponse(403, 'PERMISSION.DENIED'))
        .mockResolvedValueOnce(errorResponse(403, 'PERMISSION.DENIED'));

      for (const response of [
        await route.listMailSendLanesRoute(request('/api/mail-send-lanes')),
        await route.listMailSuppressionsRoute(request('/api/mail-suppressions')),
        await removeSuppression(route, '3'),
      ]) {
        expect(response.status).toBe(403);
        await expect(response.json()).resolves.toMatchObject({
          error_code: 'PERMISSION.DENIED',
        });
      }
    });

    it('checks the suppression query and id before calling the API', async () => {
      const route = await loadRoute();
      for (const query of ['reason=rejected', 'per_page=101', 'cursor=abc', 'email=x&email=y']) {
        const response = await route.listMailSuppressionsRoute(
          request(`/api/mail-suppressions?${query}`)
        );
        expect(response.status, query).toBe(400);
      }
      const malformed = await removeSuppression(route, '0');
      expect(malformed.status).toBe(400);
      expect(fetchMock).not.toHaveBeenCalled();
    });

    it('checks the query before calling the API', async () => {
      const route = await loadRoute();
      const response = await route.listTransactionalMailLogRoute(
        request('/api/transactional-mail-log?status=bounced')
      );
      expect(response.status).toBe(400);
      expect(fetchMock).not.toHaveBeenCalled();
    });
  });
});

async function loadRoute(): Promise<DeveloperRoute> {
  const route = await import('@/features/developer/server/developer-route');
  const { mockMailboxStore } = await import('@/features/mailbox/server/mock-mailbox-store');
  mockMailboxStore.reset();
  const { mockTransactionalStore } =
    await import('@/features/developer/server/mock-transactional-store');
  mockTransactionalStore.reset();
  if (process.env.API_ADAPTER_ENABLED !== 'true') {
    const created = mockMailboxStore.createTemplate(1, signupTemplate);
    expect(created.ok).toBe(true);
  }
  return route;
}

function testSend(route: DeveloperRoute, body: Record<string, unknown>) {
  return route.sendTransactionalTestRoute(
    request('/api/transactional-test-sends', { method: 'POST', body: JSON.stringify(body) })
  );
}

function removeSuppression(route: DeveloperRoute, id: string, organization?: string) {
  return route.removeMailSuppressionRoute(
    request(`/api/mail-suppressions/${id}`, {
      method: 'DELETE',
      ...(organization === undefined ? {} : { organization }),
    }),
    id
  );
}

function request(
  path: string,
  options: { method?: string; body?: string; origin?: string; organization?: string } = {}
): Request {
  const result = new Request(`https://app.example.com${path}`, {
    method: options.method ?? 'GET',
    body: options.body,
  });
  result.headers.set('organization-id', options.organization ?? '1');
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

function errorResponse(status: number, errorCode: string, headers: Record<string, string> = {}) {
  return Response.json(
    { code: status, error_code: errorCode, message: 'upstream detail' },
    { status, headers }
  );
}
