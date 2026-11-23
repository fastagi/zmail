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

// A real-looking P-256 point (65 bytes) and auth secret (16 bytes), base64url.
const p256dh = `B${'A'.repeat(86)}`;
const auth = 'A'.repeat(22);
const endpoint = 'https://fcm.googleapis.com/fcm/send/device-1';

async function loadRoute() {
  const route = await import('@/features/notification/server/notification-route');
  const { mockNotificationStore } = await import(
    '@/features/notification/server/mock-notification-store'
  );
  return { route, store: mockNotificationStore };
}

describe('web push browser routes', () => {
  let fetchMock: ReturnType<typeof vi.fn<typeof fetch>>;

  beforeEach(() => {
    vi.resetModules();
    fetchMock = vi.fn<typeof fetch>();
    vi.stubGlobal('fetch', fetchMock);
    cookieStore.get.mockReset();
    getSessionUser.mockReset();
    process.env.NEXT_PUBLIC_API_URL = '/api';
    process.env.NEXT_PUBLIC_APP_URL = 'https://app.example.com';
    process.env.NEXT_PUBLIC_OPTIONAL_FEATURES = 'notification';
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

  it('reports push as not configured in the mock and refuses to register', async () => {
    process.env.MOCK_BFF_ENABLED = 'true';
    getSessionUser.mockResolvedValue(mockUser('demo'));
    const { route } = await loadRoute();

    const config = await route.getWebPushConfigRoute(request('/api/web-push-config'));
    await expect(config.json()).resolves.toMatchObject({
      data: { enabled: false, public_key: '' },
    });

    const registered = await route.registerWebPushSubscriptionRoute(
      request('/api/web-push-subscriptions', { method: 'PUT', body: subscriptionBody() })
    );
    expect(registered.status).toBe(503);
    await expect(registered.json()).resolves.toMatchObject({
      error_code: 'NOTIFICATION.WEB_PUSH_DISABLED',
    });
  });

  it('registers idempotently, lists newest first, and never records a delivery', async () => {
    process.env.MOCK_BFF_ENABLED = 'true';
    getSessionUser.mockResolvedValue(mockUser('demo'));
    const { route, store } = await loadRoute();
    store.configureWebPush('BPublicKey_-');

    const first = await (
      await route.registerWebPushSubscriptionRoute(
        request('/api/web-push-subscriptions', { method: 'PUT', body: subscriptionBody() })
      )
    ).json();
    const again = await (
      await route.registerWebPushSubscriptionRoute(
        request('/api/web-push-subscriptions', {
          method: 'PUT',
          body: subscriptionBody({ device_label: 'Firefox · Linux' }),
        })
      )
    ).json();
    await route.registerWebPushSubscriptionRoute(
      request('/api/web-push-subscriptions', {
        method: 'PUT',
        body: subscriptionBody({ endpoint: 'https://web.push.apple.com/abc' }),
      })
    );

    expect(again.data.id).toBe(first.data.id);
    expect(first.data).not.toHaveProperty('endpoint');
    const listed = await (
      await route.listWebPushSubscriptionsRoute(request('/api/web-push-subscriptions'))
    ).json();
    expect(listed.data.map((item: { id: number }) => item.id)).toEqual([2, 1]);
    expect(listed.data[1]).toMatchObject({
      device_label: 'Firefox · Linux',
      last_success_at: null,
    });

    const removed = await route.removeWebPushSubscriptionRoute(
      request('/api/web-push-subscriptions/1', { method: 'DELETE' }),
      '1'
    );
    expect(removed.status).toBe(204);
  });

  it('keeps devices private to their owner and maps invalid input to stable codes', async () => {
    process.env.MOCK_BFF_ENABLED = 'true';
    getSessionUser.mockResolvedValue(mockUser('alice'));
    const { route, store } = await loadRoute();
    store.configureWebPush('BPublicKey');
    const created = await (
      await route.registerWebPushSubscriptionRoute(
        request('/api/web-push-subscriptions', { method: 'PUT', body: subscriptionBody() })
      )
    ).json();

    getSessionUser.mockResolvedValue(mockUser('bob'));
    const foreign = await route.removeWebPushSubscriptionRoute(
      request(`/api/web-push-subscriptions/${created.data.id}`, { method: 'DELETE' }),
      String(created.data.id)
    );
    expect(foreign.status).toBe(404);
    await expect(foreign.json()).resolves.toMatchObject({
      error_code: 'NOTIFICATION.WEB_PUSH_SUBSCRIPTION_NOT_FOUND',
    });
    const bobList = await (
      await route.listWebPushSubscriptionsRoute(request('/api/web-push-subscriptions'))
    ).json();
    expect(bobList.data).toEqual([]);

    const badEndpoint = await route.registerWebPushSubscriptionRoute(
      request('/api/web-push-subscriptions', {
        method: 'PUT',
        body: subscriptionBody({ endpoint: 'https://evil.example/push' }),
      })
    );
    expect(badEndpoint.status).toBe(422);
    await expect(badEndpoint.json()).resolves.toMatchObject({
      error_code: 'NOTIFICATION.WEB_PUSH_ENDPOINT_INVALID',
    });

    const badKeys = await route.registerWebPushSubscriptionRoute(
      request('/api/web-push-subscriptions', {
        method: 'PUT',
        body: JSON.stringify({ endpoint, keys: { p256dh: 'short', auth } }),
      })
    );
    expect(badKeys.status).toBe(422);
    await expect(badKeys.json()).resolves.toMatchObject({
      error_code: 'NOTIFICATION.WEB_PUSH_KEYS_INVALID',
    });

    const badShape = await route.registerWebPushSubscriptionRoute(
      request('/api/web-push-subscriptions', {
        method: 'PUT',
        body: JSON.stringify({ endpoint, keys: { p256dh, auth }, extra: true }),
      })
    );
    expect(badShape.status).toBe(422);
    await expect(badShape.json()).resolves.toMatchObject({
      error_code: 'COMMON.VALIDATION_FAILED',
    });

    const badId = await route.removeWebPushSubscriptionRoute(
      request('/api/web-push-subscriptions/abc', { method: 'DELETE' }),
      'abc'
    );
    expect(badId.status).toBe(404);
  });

  it('rejects cross-origin writes before authentication or body parsing', async () => {
    process.env.MOCK_BFF_ENABLED = 'true';
    const { route } = await loadRoute();

    const put = await route.registerWebPushSubscriptionRoute(
      request('/api/web-push-subscriptions', {
        method: 'PUT',
        origin: 'https://evil.example',
        body: '{',
      })
    );
    const remove = await route.removeWebPushSubscriptionRoute(
      request('/api/web-push-subscriptions/1', {
        method: 'DELETE',
        origin: 'https://evil.example',
      }),
      '1'
    );

    expect(put.status).toBe(403);
    expect(remove.status).toBe(403);
    expect(getSessionUser).not.toHaveBeenCalled();
  });

  it('requires a session and the notification feature', async () => {
    process.env.MOCK_BFF_ENABLED = 'true';
    getSessionUser.mockResolvedValue(null);
    let { route } = await loadRoute();
    expect((await route.getWebPushConfigRoute(request('/api/web-push-config'))).status).toBe(401);

    vi.resetModules();
    process.env.NEXT_PUBLIC_OPTIONAL_FEATURES = '';
    ({ route } = await loadRoute());
    const disabled = await route.listWebPushSubscriptionsRoute(
      request('/api/web-push-subscriptions')
    );
    expect(disabled.status).toBe(503);
    await expect(disabled.json()).resolves.toMatchObject({
      error_code: 'COMMON.SERVICE_UNAVAILABLE',
    });
  });

  it('forwards fixed upstream paths and keeps an omitted push preference omitted', async () => {
    process.env.API_ADAPTER_ENABLED = 'true';
    process.env.API_UPSTREAM_URL = 'https://api.example.com/v1';
    cookieStore.get.mockReturnValue({ value: 'A'.repeat(43) });
    const subscription = {
      id: 7,
      device_label: 'Chrome · macOS',
      created_at: '2026-11-20T10:00:00Z',
      last_success_at: null,
    };
    fetchMock
      .mockResolvedValueOnce(envelope({ enabled: true, public_key: 'BKey' }))
      .mockResolvedValueOnce(envelope([subscription]))
      .mockResolvedValueOnce(envelope(subscription))
      .mockResolvedValueOnce(new Response(null, { status: 204 }))
      .mockResolvedValueOnce(
        envelope({ in_app_enabled: true, email_enabled: false, web_push_enabled: false })
      )
      .mockResolvedValueOnce(
        Response.json(
          {
            code: 503,
            message: 'disabled',
            error_code: 'NOTIFICATION.WEB_PUSH_DISABLED',
          },
          { status: 503 }
        )
      );
    const { route } = await loadRoute();

    await route.getWebPushConfigRoute(request('/api/web-push-config'));
    await route.listWebPushSubscriptionsRoute(request('/api/web-push-subscriptions?x=1'));
    await route.registerWebPushSubscriptionRoute(
      request('/api/web-push-subscriptions', { method: 'PUT', body: subscriptionBody() })
    );
    const removed = await route.removeWebPushSubscriptionRoute(
      request('/api/web-push-subscriptions/7', { method: 'DELETE' }),
      '7'
    );
    await route.replaceNotificationPreferenceRoute(
      request('/api/notification-preferences', {
        method: 'PUT',
        body: JSON.stringify({ in_app_enabled: true, email_enabled: false }),
      })
    );
    const disabled = await route.registerWebPushSubscriptionRoute(
      request('/api/web-push-subscriptions', { method: 'PUT', body: subscriptionBody() })
    );

    const urls = fetchMock.mock.calls.map(call => String(call[0]));
    expect(urls.slice(0, 5)).toEqual([
      'https://api.example.com/v1/web-push-config',
      'https://api.example.com/v1/web-push-subscriptions',
      'https://api.example.com/v1/web-push-subscriptions',
      'https://api.example.com/v1/web-push-subscriptions/7',
      'https://api.example.com/v1/notification-preferences',
    ]);
    expect(fetchMock.mock.calls[3][1]?.method).toBe('DELETE');
    expect(removed.status).toBe(204);
    expect(JSON.parse(String(fetchMock.mock.calls[2][1]?.body))).toEqual({
      endpoint,
      keys: { p256dh, auth },
      device_label: 'Chrome · macOS',
    });
    expect(JSON.parse(String(fetchMock.mock.calls[4][1]?.body))).toEqual({
      in_app_enabled: true,
      email_enabled: false,
    });
    expect(disabled.status).toBe(503);
    await expect(disabled.json()).resolves.toMatchObject({
      error_code: 'NOTIFICATION.WEB_PUSH_DISABLED',
    });
  });
});

function subscriptionBody(overrides: Record<string, unknown> = {}): string {
  return JSON.stringify({
    endpoint,
    keys: { p256dh, auth },
    device_label: 'Chrome · macOS',
    ...overrides,
  });
}

function envelope(data: unknown): Response {
  return Response.json({ code: 0, message: 'success', data });
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
  if (options.origin) {
    result.headers.set('origin', options.origin);
    result.headers.set('sec-fetch-site', 'cross-site');
  }
  return result;
}

function mockUser(id: string) {
  return { id, email: `${id}@example.com`, name: id };
}
