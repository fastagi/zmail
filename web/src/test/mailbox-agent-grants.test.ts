import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { ApiErrorCode, ClientErrorCode } from '@/http/codes';
import type { AgentGrantFormValues } from '@/features/mailbox/utils/agent-grant-form';
import { messages } from '@/i18n/modules';
import zhHansMailbox from '@/i18n/modules/mailbox/zh-Hans';

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
const dayMilliseconds = 24 * 60 * 60 * 1_000;

describe('agent grant browser routes', () => {
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

  it('rejects cross-origin grant writes before authentication or body parsing', async () => {
    process.env.MOCK_BFF_ENABLED = 'true';
    const route = await loadRoute();
    const options = { body: '{', origin: 'https://evil.example' };
    const responses = await Promise.all([
      route.issueMailAgentGrantRoute(
        request('/api/mailboxes/1/agent-grants', { ...options, method: 'POST' }),
        '1'
      ),
      route.revokeMailAgentGrantRoute(
        request('/api/mailboxes/1/agent-grants/1', { ...options, method: 'DELETE' }),
        '1',
        '1'
      ),
    ]);
    for (const response of responses) expect(response.status).toBe(403);
    expect(getSessionUser).not.toHaveBeenCalled();
  });

  it('issues, lists, audits, drafts, and revokes in the mock with stable error codes', async () => {
    process.env.MOCK_BFF_ENABLED = 'true';
    const user = mockUser('demo-admin');
    getSessionUser.mockResolvedValue(user);
    const route = await loadRoute();
    const mailboxId = await createMailbox(route, 'agents');
    const { mockApiKeyStore } = await import('@/features/api-key/server/mock-api-key-store');
    const agentKey = mockApiKeyStore.create(user, {
      name: 'Support agent',
      scopes: ['mail:agent'],
    });
    const readKey = mockApiKeyStore.create(user, { name: 'Reader', scopes: ['mail:read'] });
    const issue = (body: Record<string, unknown>) =>
      route.issueMailAgentGrantRoute(
        request(`/api/mailboxes/${mailboxId}/agent-grants`, {
          method: 'POST',
          body: JSON.stringify(body),
        }),
        String(mailboxId)
      );
    const valid = {
      api_key_id: agentKey.api_key.id,
      capabilities: ['read', 'draft'],
      reveal_secrets: false,
      filters: { senders: ['Example.com'], label_ids: [] },
      expires_at: new Date(Date.now() + 7 * dayMilliseconds).toISOString(),
    };

    await expectStatus(
      await issue({ ...valid, capabilities: 'read' }),
      422,
      'COMMON.VALIDATION_FAILED'
    );
    await expectStatus(
      await issue({ ...valid, api_key_id: readKey.api_key.id }),
      422,
      'MAILBOX.AGENT_GRANT_INVALID'
    );
    await expectStatus(
      await issue({ ...valid, capabilities: ['draft'] }),
      422,
      'MAILBOX.AGENT_GRANT_INVALID'
    );
    await expectStatus(
      await issue({
        ...valid,
        expires_at: new Date(Date.now() + 91 * dayMilliseconds).toISOString(),
      }),
      422,
      'MAILBOX.AGENT_GRANT_INVALID'
    );

    const created = await issue(valid);
    expect(created.status).toBe(201);
    const grant = await data(created);
    expect(grant).toMatchObject({
      mailbox_id: mailboxId,
      api_key_name: 'Support agent',
      capabilities: ['read', 'draft'],
      filters: { senders: ['example.com'], label_ids: [] },
      status: 'active',
    });
    await expectStatus(await issue(valid), 409, 'MAILBOX.AGENT_GRANT_EXISTS');

    const listed = await data(
      await route.listMailAgentGrantsRoute(
        request(`/api/mailboxes/${mailboxId}/agent-grants`),
        String(mailboxId)
      )
    );
    expect(listed.map((item: { id: number }) => item.id)).toEqual([grant.id]);

    const activity = await data(
      await route.listMailAgentGrantActivityRoute(
        request(`/api/mailboxes/${mailboxId}/agent-grants/${grant.id}/activity`),
        String(mailboxId),
        String(grant.id)
      )
    );
    expect(activity.map((entry: { action: string }) => entry.action)).toEqual(['draft', 'read']);
    for (const entry of activity)
      expect(Object.keys(entry).sort()).toEqual(
        ['action', 'api_key_id', 'created_at', 'grant_id', 'id', 'message_id'].sort()
      );

    const drafts = await data(
      await route.listMailAgentDraftsRoute(
        request(`/api/mailboxes/${mailboxId}/agent-drafts`),
        String(mailboxId)
      )
    );
    expect(drafts).toHaveLength(1);
    expect(drafts[0]).toMatchObject({ grant_id: grant.id, status: 'pending' });

    const revoked = await route.revokeMailAgentGrantRoute(
      request(`/api/mailboxes/${mailboxId}/agent-grants/${grant.id}`, { method: 'DELETE' }),
      String(mailboxId),
      String(grant.id)
    );
    expect(revoked.status).toBe(204);
    // Revoking an ended grant answers 204 again; an unknown id is its own error.
    expect(
      (
        await route.revokeMailAgentGrantRoute(
          request(`/api/mailboxes/${mailboxId}/agent-grants/${grant.id}`, { method: 'DELETE' }),
          String(mailboxId),
          String(grant.id)
        )
      ).status
    ).toBe(204);
    await expectStatus(
      await route.revokeMailAgentGrantRoute(
        request(`/api/mailboxes/${mailboxId}/agent-grants/999`, { method: 'DELETE' }),
        String(mailboxId),
        '999'
      ),
      404,
      'MAILBOX.AGENT_GRANT_NOT_FOUND'
    );
    const after = await data(
      await route.listMailAgentGrantsRoute(
        request(`/api/mailboxes/${mailboxId}/agent-grants`),
        String(mailboxId)
      )
    );
    expect(after[0]).toMatchObject({ status: 'revoked', revoke_reason: 'revoked' });
    // The slot is free again once the grant ended.
    expect((await issue(valid)).status).toBe(201);
  });

  it('caps a mailbox at ten working grants', async () => {
    process.env.MOCK_BFF_ENABLED = 'true';
    const user = mockUser('demo-admin');
    getSessionUser.mockResolvedValue(user);
    const route = await loadRoute();
    const mailboxId = await createMailbox(route, 'busy');
    const { mockApiKeyStore } = await import('@/features/api-key/server/mock-api-key-store');
    const expiresAt = new Date(Date.now() + dayMilliseconds).toISOString();
    for (let index = 0; index <= 10; index += 1) {
      const key = mockApiKeyStore.create(user, { name: `Agent ${index}`, scopes: ['*'] });
      const response = await route.issueMailAgentGrantRoute(
        request(`/api/mailboxes/${mailboxId}/agent-grants`, {
          method: 'POST',
          body: JSON.stringify({
            api_key_id: key.api_key.id,
            capabilities: ['read'],
            expires_at: expiresAt,
          }),
        }),
        String(mailboxId)
      );
      if (index < 10) expect(response.status).toBe(201);
      else await expectStatus(response, 409, 'MAILBOX.AGENT_GRANT_LIMIT');
    }
  });

  it('forwards the agent grant routes to the API with only the documented fields', async () => {
    process.env.API_ADAPTER_ENABLED = 'true';
    process.env.API_UPSTREAM_URL = 'https://api.example.com/v1';
    cookieStore.get.mockReturnValue({ value: 'A'.repeat(43) });
    const route = await loadRoute();
    fetchMock.mockResolvedValueOnce(Response.json({ code: 0, message: 'success', data: {} }));
    const response = await route.issueMailAgentGrantRoute(
      request('/api/mailboxes/12/agent-grants', {
        method: 'POST',
        body: JSON.stringify({
          api_key_id: 4,
          capabilities: ['read'],
          expires_at: '2026-12-01T00:00:00Z',
          filters: { senders: [' Example.com '] },
          extra: true,
        }),
      }),
      '12'
    );
    expect(response.headers.get('cache-control')).toBe('private, no-store');
    let [url, init] = fetchMock.mock.calls[0] ?? [];
    expect(String(url)).toBe('https://api.example.com/v1/mailboxes/12/agent-grants');
    expect(init?.method).toBe('POST');
    expect(JSON.parse(String(init?.body))).toEqual({
      api_key_id: 4,
      capabilities: ['read'],
      reveal_secrets: false,
      filters: { senders: ['Example.com'], label_ids: [] },
      expires_at: '2026-12-01T00:00:00Z',
    });

    fetchMock.mockResolvedValueOnce(new Response(null, { status: 204 }));
    await route.revokeMailAgentGrantRoute(
      request('/api/mailboxes/12/agent-grants/7', { method: 'DELETE' }),
      '12',
      '7'
    );
    [url, init] = fetchMock.mock.calls[1] ?? [];
    expect(String(url)).toBe('https://api.example.com/v1/mailboxes/12/agent-grants/7');
    expect(init?.method).toBe('DELETE');

    fetchMock.mockResolvedValueOnce(Response.json({ code: 0, message: 'success', data: [] }));
    await route.listMailAgentDraftsRoute(
      request('/api/mailboxes/12/agent-drafts?page=2&per_page=500&x=1'),
      '12'
    );
    [url] = fetchMock.mock.calls[2] ?? [];
    expect(String(url)).toBe(
      'https://api.example.com/v1/mailboxes/12/agent-drafts?page=2&per_page=100'
    );
  });
});

describe('agent grant form rules', () => {
  it('checks the key, filters, senders, labels, expiry, and the ten-grant limit', async () => {
    const { validateAgentGrantForm, isSenderFilter, parseSenders } =
      await import('@/features/mailbox/utils/agent-grant-form');
    const base: AgentGrantFormValues = {
      apiKeyId: 3,
      scope: 'all',
      senders: '',
      labelIds: [],
      draft: false,
      revealSecrets: false,
      days: 30,
    };
    const context = { activeGrants: 0, activeKeyIds: new Set<number>(), maxGrants: 10 };
    expect(validateAgentGrantForm(base, context)).toEqual([]);
    const keys = (values: Partial<AgentGrantFormValues>, extra = {}) =>
      validateAgentGrantForm({ ...base, ...values }, { ...context, ...extra }).map(
        error => error.key
      );
    expect(keys({ apiKeyId: null })).toEqual(['agents.form.errors.key']);
    expect(keys({}, { activeKeyIds: new Set([3]) })).toEqual(['agents.form.errors.keyTaken']);
    expect(keys({}, { activeGrants: 10 })).toEqual(['agents.form.limitReached']);
    expect(keys({ scope: 'filtered' })).toEqual(['agents.form.errors.filtersEmpty']);
    expect(keys({ scope: 'filtered', senders: 'example.com, not a domain' })).toEqual([
      'agents.form.errors.senderInvalid',
    ]);
    const many = Array.from({ length: 21 }, (_, index) => `d${index}.example`).join('\n');
    expect(keys({ scope: 'filtered', senders: many })).toEqual([
      'agents.form.errors.sendersTooMany',
    ]);
    expect(
      keys({ scope: 'filtered', labelIds: Array.from({ length: 21 }, (_, index) => index + 1) })
    ).toEqual(['agents.form.errors.labelsTooMany']);
    expect(keys({ days: 91 })).toEqual(['agents.form.errors.expiry']);
    // Filters are ignored while the whole mailbox is chosen.
    expect(keys({ senders: 'nonsense' })).toEqual([]);

    expect(parseSenders(' A@Example.com,\nexample.com; a@example.com ')).toEqual([
      'a@example.com',
      'example.com',
    ]);
    expect(isSenderFilter('@example.com')).toBe(true);
    expect(isSenderFilter('billing@vendor.test')).toBe(true);
    expect(isSenderFilter('mail.example.co.uk')).toBe(true);
    expect(isSenderFilter('@bob@example.com')).toBe(false);
    expect(isSenderFilter('localhost')).toBe(false);
  });

  it('builds the request with read always on and filters only when filtering', async () => {
    const { agentGrantInput } = await import('@/features/mailbox/utils/agent-grant-form');
    const now = new Date('2026-11-20T00:00:00Z');
    const input = agentGrantInput(
      {
        apiKeyId: 3,
        scope: 'all',
        senders: 'example.com',
        labelIds: [4],
        draft: true,
        revealSecrets: true,
        days: 90,
      },
      now
    );
    expect(input).toEqual({
      api_key_id: 3,
      capabilities: ['read', 'draft'],
      reveal_secrets: true,
      filters: { senders: [], label_ids: [] },
      expires_at: new Date(now.getTime() + 90 * dayMilliseconds - 60_000).toISOString(),
    });
  });
});

describe('agent grant access and errors', () => {
  it('shows the tab to a personal owner and a shared manager only', async () => {
    const { canManageAgentGrants, isUsableAgentKey } =
      await import('@/features/mailbox/utils/agent-grant-access');
    const access = (overrides: Record<string, boolean>) => ({
      view: false,
      send: false,
      manage_members: false,
      administer: false,
      see_history: false,
      organize: false,
      ...overrides,
    });
    const box = (
      type: 'personal' | 'shared',
      origin: 'zmail' | 'connected',
      flags: Record<string, boolean>,
      awaiting = false
    ) => ({ type, origin, access: access(flags), awaiting_handover: awaiting });

    expect(
      canManageAgentGrants(box('personal', 'zmail', { view: true, send: true, organize: true }))
    ).toBe(true);
    expect(
      canManageAgentGrants(box('personal', 'connected', { view: true, see_history: true }))
    ).toBe(true);
    // An owner or admin of the organization on someone's personal mailbox.
    expect(
      canManageAgentGrants(box('personal', 'zmail', { administer: true, see_history: true }))
    ).toBe(false);
    expect(canManageAgentGrants(box('personal', 'zmail', { administer: true }, true))).toBe(false);
    expect(
      canManageAgentGrants(
        box('shared', 'zmail', { view: true, send: true, manage_members: true, organize: true })
      )
    ).toBe(true);
    // An owner or admin who is not a manage member.
    expect(
      canManageAgentGrants(box('shared', 'zmail', { view: true, send: true, manage_members: true }))
    ).toBe(false);
    expect(canManageAgentGrants(box('shared', 'zmail', { view: true, send: true }))).toBe(false);
    expect(
      canManageAgentGrants(box('shared', 'connected', { view: true, manage_members: true }))
    ).toBe(true);
    expect(canManageAgentGrants(box('shared', 'connected', { view: true }))).toBe(false);

    const now = new Date('2026-11-20T00:00:00Z');
    const key = { mode: 'live' as const, scopes: ['mail:agent'] };
    expect(isUsableAgentKey(key, now)).toBe(true);
    expect(isUsableAgentKey({ ...key, scopes: ['*'] }, now)).toBe(true);
    expect(isUsableAgentKey({ ...key, scopes: ['mail:read'] }, now)).toBe(false);
    expect(isUsableAgentKey({ ...key, mode: 'test' }, now)).toBe(false);
    expect(isUsableAgentKey({ ...key, revoked_at: '2026-11-19T00:00:00Z' }, now)).toBe(false);
    expect(isUsableAgentKey({ ...key, expires_at: '2026-11-19T00:00:00Z' }, now)).toBe(false);
  });

  it('maps every documented agent error code to its own copy', async () => {
    const { resolveAgentGrantErrorKey } =
      await import('@/features/mailbox/utils/agent-grant-error');
    // The same module instance as the resolver, whatever earlier tests reset.
    const { ApiError } = await import('@/http/request');
    const key = (code: string) => resolveAgentGrantErrorKey(new ApiError('x', code));
    expect(key(ApiErrorCode.MAILBOX_AGENT_GRANT_NOT_FOUND)).toBe('agents.errors.notFound');
    expect(key(ApiErrorCode.MAILBOX_AGENT_GRANT_INVALID)).toBe('agents.errors.invalid');
    expect(key(ApiErrorCode.MAILBOX_AGENT_GRANT_LIMIT)).toBe('agents.errors.limit');
    expect(key(ApiErrorCode.MAILBOX_AGENT_GRANT_EXISTS)).toBe('agents.errors.exists');
    expect(key(ApiErrorCode.PERMISSION_DENIED)).toBe('agents.errors.permissionDenied');
    expect(key(ApiErrorCode.MAILBOX_NOT_FOUND)).toBe('agents.errors.mailboxNotFound');
    expect(key(ApiErrorCode.COMMON_VALIDATION_FAILED)).toBe('agents.errors.validation');
    expect(key(ApiErrorCode.COMMON_RATE_LIMITED)).toBe('agents.errors.rateLimited');
    expect(key(ApiErrorCode.COMMON_SERVICE_UNAVAILABLE)).toBe('errors.unavailable');
    expect(key(ClientErrorCode.INVALID_RESPONSE)).toBe('errors.invalidResponse');
    expect(resolveAgentGrantErrorKey(new Error('boom'))).toBe('errors.generic');
  });

  it('has every agent key in both locales with the same arguments', () => {
    const en = messages.mailbox.agents as unknown as Record<string, unknown>;
    const zh = zhHansMailbox.agents as unknown as Record<string, unknown>;
    expect(leaves(zh)).toEqual(leaves(en));
    const enLeaves = flatten(en);
    const zhLeaves = flatten(zh);
    for (const [path, value] of Object.entries(enLeaves)) {
      expect(zhLeaves[path], path).toBeTruthy();
      expect(argumentsOf(zhLeaves[path] ?? ''), path).toEqual(argumentsOf(value));
    }
  });
});

describe('agent grant service', () => {
  it('rejects a grant of another mailbox and an activity row of another grant', async () => {
    vi.resetModules();
    const request = await import('@/http/request');
    const grant = {
      id: 7,
      mailbox_id: 3,
      api_key_id: 12,
      api_key_name: 'Support agent',
      api_key_prefix: 'sk_live_ab',
      key_user_id: 4,
      granted_by: 4,
      capabilities: ['read'],
      reveal_secrets: false,
      filters: { senders: [], label_ids: [] },
      status: 'active',
      expires_at: '2026-12-31T00:00:00Z',
      revoked_at: null,
      revoke_reason: null,
      created_at: '2026-12-01T09:00:00Z',
    };
    const envelope = (data: unknown[]) => ({
      code: 0,
      message: 'success',
      data,
      meta: { current_page: 1, per_page: 50, total: data.length, last_page: 1, from: 1, to: 1 },
      links: { first: '', last: '', prev: null, next: null },
    });
    const getEnvelope = vi.spyOn(request.default, 'getEnvelope');
    const get = vi.spyOn(request.default, 'get');
    const { agentGrantService } = await import('@/features/mailbox/services/agent-grant-service');

    getEnvelope.mockResolvedValueOnce(envelope([grant]) as never);
    await expect(agentGrantService.list(1, 3)).resolves.toMatchObject({ items: [grant] });
    expect(getEnvelope).toHaveBeenLastCalledWith('/mailboxes/3/agent-grants', {
      headers: { 'Organization-Id': '1' },
      params: { page: 1, per_page: 50 },
    });
    getEnvelope.mockResolvedValueOnce(envelope([{ ...grant, mailbox_id: 9 }]) as never);
    await expect(agentGrantService.list(1, 3)).rejects.toMatchObject({
      errorCode: ClientErrorCode.INVALID_RESPONSE,
    });
    get.mockResolvedValueOnce([
      {
        id: 1,
        grant_id: 8,
        api_key_id: 12,
        message_id: 5,
        action: 'read',
        created_at: grant.created_at,
      },
    ]);
    await expect(agentGrantService.activity(1, 3, 7)).rejects.toMatchObject({
      errorCode: ClientErrorCode.INVALID_RESPONSE,
    });
  });
});

function leaves(value: Record<string, unknown>): string[] {
  return Object.keys(flatten(value)).sort();
}

function flatten(value: Record<string, unknown>, prefix = ''): Record<string, string> {
  const result: Record<string, string> = {};
  for (const [key, child] of Object.entries(value)) {
    const path = prefix ? `${prefix}.${key}` : key;
    if (typeof child === 'string') result[path] = child;
    else Object.assign(result, flatten(child as Record<string, unknown>, path));
  }
  return result;
}

function argumentsOf(text: string): string[] {
  return [...new Set(Array.from(text.matchAll(/\{(\w+)/gu), match => match[1] ?? ''))].sort();
}

async function loadRoute(): Promise<MailboxRoute> {
  const route = await import('@/features/mailbox/server/mailbox-route');
  const store = await import('@/features/mailbox/server/mock-mailbox-store');
  store.mockMailboxStore.reset();
  const agents = await import('@/features/mailbox/server/mock-agent-grant-store');
  agents.mockAgentGrantStore.reset();
  return route;
}

/** A shared mailbox every current member manages; it starts with one welcome message. */
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
