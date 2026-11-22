import { randomBytes } from 'node:crypto';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const cookieStore = vi.hoisted(() => ({ get: vi.fn(), set: vi.fn() }));
const mockSession = vi.hoisted(() => ({
  getSessionUser: vi.fn(),
  clearSessionCookie: vi.fn(async () => undefined),
  setSessionCookie: vi.fn(async () => undefined),
}));

vi.mock('next/headers', () => ({
  cookies: async () => cookieStore,
  headers: async () => new Headers(),
}));
vi.mock('@/features/auth/server/session', () => mockSession);

const managedKeys = [
  'API_ADAPTER_ENABLED',
  'API_UPSTREAM_TIMEOUT_MS',
  'API_UPSTREAM_MAX_RESPONSE_BYTES',
  'API_UPSTREAM_URL',
  'MOCK_BFF_ENABLED',
  'NEXT_PUBLIC_API_URL',
  'NEXT_PUBLIC_APP_URL',
] as const;
const originalEnv = { ...process.env };

function put(body: unknown, origin = 'https://app.example.com'): Request {
  const result = new Request('https://app.example.com/api/auth/me', {
    method: 'PUT',
    body: JSON.stringify(body),
  });
  result.headers.set('content-type', 'application/json');
  result.headers.set('origin', origin);
  return result;
}

function opaqueCredential(): string {
  return randomBytes(32).toString('base64url');
}

/** True when the route expired the API session cookie (an empty value with max-age 0). */
function apiCookieCleared(): boolean {
  return cookieStore.set.mock.calls.some(
    ([cookie]) => typeof cookie === 'object' && cookie.value === '' && cookie.maxAge === 0
  );
}

async function route() {
  return import('@/app/api/auth/me/route');
}

describe('PUT /api/auth/me', () => {
  let fetchMock: ReturnType<typeof vi.fn<typeof fetch>>;

  beforeEach(() => {
    vi.resetModules();
    cookieStore.get.mockReset();
    cookieStore.set.mockReset();
    mockSession.getSessionUser.mockReset();
    mockSession.setSessionCookie.mockClear();
    mockSession.clearSessionCookie.mockClear();
    fetchMock = vi.fn<typeof fetch>();
    vi.stubGlobal('fetch', fetchMock);
    process.env.NEXT_PUBLIC_API_URL = '/api';
    process.env.NEXT_PUBLIC_APP_URL = 'https://app.example.com';
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

  describe('with the Go API adapter', () => {
    beforeEach(() => {
      process.env.API_ADAPTER_ENABLED = 'true';
      process.env.API_UPSTREAM_URL = 'https://api.example.com/v1';
    });

    it('sends only the nickname and answers with the renamed user', async () => {
      const token = opaqueCredential();
      cookieStore.get.mockReturnValue({ value: token });
      fetchMock.mockResolvedValue(
        Response.json({
          code: 0,
          message: 'success',
          data: { id: 7, username: 'u7', email: 'ada@example.com', nickname: 'Ada L', status: 1 },
        })
      );

      const response = await (await route()).PUT(put({ name: '  Ada L  ' }));

      expect(response.status).toBe(200);
      await expect(response.json()).resolves.toMatchObject({
        data: { user: { id: '7', email: 'ada@example.com', name: 'Ada L' } },
      });
      expect(response.headers.get('cache-control')).toBe('private, no-store');
      const [url, init] = fetchMock.mock.calls[0];
      expect(new URL(String(url)).pathname).toBe('/v1/users/profile');
      expect(init?.method).toBe('PUT');
      expect(new Headers(init?.headers).get('authorization')).toBe(`Bearer ${token}`);
      expect(JSON.parse(String(init?.body))).toEqual({ nickname: 'Ada L' });
    });

    it('drops the cookie when the API no longer accepts the session', async () => {
      cookieStore.get.mockReturnValue({ value: opaqueCredential() });
      fetchMock.mockResolvedValue(
        Response.json(
          { code: 401, error_code: 'AUTH.UNAUTHORIZED', message: 'expired' },
          { status: 401 }
        )
      );

      const response = await (await route()).PUT(put({ name: 'Ada' }));

      expect(response.status).toBe(401);
      expect(apiCookieCleared()).toBe(true);
    });

    it('rejects a blank or overlong name before calling the API', async () => {
      cookieStore.get.mockReturnValue({ value: opaqueCredential() });
      const { PUT } = await route();

      expect((await PUT(put({ name: '   ' }))).status).toBe(422);
      expect((await PUT(put({ name: 'x'.repeat(51) }))).status).toBe(422);
      expect(fetchMock).not.toHaveBeenCalled();
    });

    it('refuses a cross-origin rename', async () => {
      cookieStore.get.mockReturnValue({ value: opaqueCredential() });

      const response = await (await route()).PUT(put({ name: 'Ada' }, 'https://evil.example'));

      expect(response.status).toBe(403);
      expect(fetchMock).not.toHaveBeenCalled();
    });
  });

  describe('with the development mock BFF', () => {
    beforeEach(() => {
      process.env.MOCK_BFF_ENABLED = 'true';
    });

    it('renames the mock session user', async () => {
      mockSession.getSessionUser.mockResolvedValue({
        id: 'demo-admin',
        email: 'admin@example.com',
        name: 'Admin User',
      });

      const response = await (await route()).PUT(put({ name: 'Ada' }));

      expect(response.status).toBe(200);
      const renamed = { id: 'demo-admin', email: 'admin@example.com', name: 'Ada' };
      await expect(response.json()).resolves.toMatchObject({ data: { user: renamed } });
      expect(mockSession.setSessionCookie).toHaveBeenCalledWith(renamed);
      expect(fetchMock).not.toHaveBeenCalled();
    });

    it('answers 401 AUTH.UNAUTHORIZED without a mock session', async () => {
      mockSession.getSessionUser.mockResolvedValue(null);

      const response = await (await route()).PUT(put({ name: 'Ada' }));

      expect(response.status).toBe(401);
      await expect(response.json()).resolves.toMatchObject({ error_code: 'AUTH.UNAUTHORIZED' });
    });
  });
});
