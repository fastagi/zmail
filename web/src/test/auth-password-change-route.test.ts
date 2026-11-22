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
  const result = new Request('https://app.example.com/api/auth/password', {
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
  return import('@/app/api/auth/password/route');
}

describe('PUT /api/auth/password', () => {
  let fetchMock: ReturnType<typeof vi.fn<typeof fetch>>;

  beforeEach(() => {
    vi.resetModules();
    cookieStore.get.mockReset();
    cookieStore.set.mockReset();
    mockSession.getSessionUser.mockReset();
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

    it('changes the password with the session credential and drops the revoked session', async () => {
      const token = opaqueCredential();
      cookieStore.get.mockReturnValue({ value: token });
      fetchMock.mockResolvedValue(
        Response.json({ code: 0, message: 'success', data: { message: 'changed' } })
      );

      const response = await (await route()).PUT(
        put({ currentPassword: 'old-secret', newPassword: 'new-secret' })
      );

      expect(response.status).toBe(200);
      await expect(response.json()).resolves.toMatchObject({ data: { success: true } });
      expect(response.headers.get('cache-control')).toBe('private, no-store');
      const [url, init] = fetchMock.mock.calls[0];
      expect(new URL(String(url)).pathname).toBe('/v1/users/password');
      expect(init?.method).toBe('PUT');
      expect(new Headers(init?.headers).get('authorization')).toBe(`Bearer ${token}`);
      expect(JSON.parse(String(init?.body))).toEqual({
        old_password: 'old-secret',
        new_password: 'new-secret',
      });
      expect(apiCookieCleared()).toBe(true);
    });

    it('keeps the session when the current password is wrong', async () => {
      cookieStore.get.mockReturnValue({ value: opaqueCredential() });
      fetchMock.mockResolvedValue(
        Response.json(
          {
            code: 401,
            error_code: 'AUTH.INVALID_CREDENTIALS',
            message: 'invalid username or password',
          },
          { status: 401 }
        )
      );

      const response = await (await route()).PUT(
        put({ currentPassword: 'wrong', newPassword: 'new-secret' })
      );

      expect(response.status).toBe(401);
      const body = await response.json();
      expect(body).toMatchObject({ error_code: 'AUTH.INVALID_CREDENTIALS' });
      expect(JSON.stringify(body)).not.toContain('invalid username or password');
      expect(apiCookieCleared()).toBe(false);
    });

    it('drops the cookie when the API no longer accepts the session', async () => {
      cookieStore.get.mockReturnValue({ value: opaqueCredential() });
      fetchMock.mockResolvedValue(
        Response.json(
          { code: 401, error_code: 'AUTH.UNAUTHORIZED', message: 'expired' },
          { status: 401 }
        )
      );

      const response = await (await route()).PUT(
        put({ currentPassword: 'old-secret', newPassword: 'new-secret' })
      );

      expect(response.status).toBe(401);
      await expect(response.json()).resolves.toMatchObject({ error_code: 'AUTH.UNAUTHORIZED' });
      expect(apiCookieCleared()).toBe(true);
    });

    it('maps the API new_password field error to newPassword', async () => {
      cookieStore.get.mockReturnValue({ value: opaqueCredential() });
      fetchMock.mockResolvedValue(
        Response.json(
          {
            code: 422,
            error_code: 'COMMON.VALIDATION_FAILED',
            message: 'bad',
            errors: { new_password: ['too weak'] },
          },
          { status: 422 }
        )
      );

      const response = await (await route()).PUT(
        put({ currentPassword: 'old-secret', newPassword: 'new-secret' })
      );

      expect(response.status).toBe(422);
      const body = await response.json();
      expect(Object.keys(body.errors)).toEqual(['newPassword']);
    });

    it('answers 401 without calling the API when there is no session', async () => {
      cookieStore.get.mockReturnValue(undefined);

      const response = await (await route()).PUT(
        put({ currentPassword: 'old-secret', newPassword: 'new-secret' })
      );

      expect(response.status).toBe(401);
      await expect(response.json()).resolves.toMatchObject({ error_code: 'AUTH.UNAUTHORIZED' });
      expect(fetchMock).not.toHaveBeenCalled();
    });

    it('rejects other origins and invalid or unchanged passwords before the API', async () => {
      cookieStore.get.mockReturnValue({ value: opaqueCredential() });
      const { PUT } = await route();

      expect(
        (
          await PUT(
            put(
              { currentPassword: 'old-secret', newPassword: 'new-secret' },
              'https://evil.example'
            )
          )
        ).status
      ).toBe(403);

      const short = await PUT(put({ currentPassword: 'old-secret', newPassword: '123' }));
      expect(short.status).toBe(422);
      expect((await short.json()).errors).toHaveProperty('newPassword');

      const long = await PUT(put({ currentPassword: 'old-secret', newPassword: 'x'.repeat(51) }));
      expect(long.status).toBe(422);

      const unchanged = await PUT(put({ currentPassword: 'same-secret', newPassword: 'same-secret' }));
      expect(unchanged.status).toBe(422);
      expect((await unchanged.json()).errors).toHaveProperty('newPassword');

      const missing = await PUT(put({ newPassword: 'new-secret' }));
      expect(missing.status).toBe(422);

      expect(fetchMock).not.toHaveBeenCalled();
    });
  });

  describe('with the development mock BFF', () => {
    beforeEach(() => {
      process.env.MOCK_BFF_ENABLED = 'true';
    });

    it('checks the current password and then ends the mock session', async () => {
      mockSession.getSessionUser.mockResolvedValue({
        id: 'demo-admin',
        email: 'admin@example.com',
        name: 'Admin User',
      });
      const { PUT } = await route();

      const wrong = await PUT(put({ currentPassword: 'nope', newPassword: 'new-secret' }));
      expect(wrong.status).toBe(401);
      await expect(wrong.json()).resolves.toMatchObject({ error_code: 'AUTH.INVALID_CREDENTIALS' });
      expect(mockSession.clearSessionCookie).not.toHaveBeenCalled();

      const changed = await PUT(put({ currentPassword: 'admin123', newPassword: 'new-secret' }));
      expect(changed.status).toBe(200);
      await expect(changed.json()).resolves.toMatchObject({ data: { success: true } });
      expect(mockSession.clearSessionCookie).toHaveBeenCalled();
      expect(fetchMock).not.toHaveBeenCalled();
    });

    it('answers 401 AUTH.UNAUTHORIZED without a mock session', async () => {
      mockSession.getSessionUser.mockResolvedValue(null);

      const response = await (await route()).PUT(
        put({ currentPassword: 'admin123', newPassword: 'new-secret' })
      );

      expect(response.status).toBe(401);
      await expect(response.json()).resolves.toMatchObject({ error_code: 'AUTH.UNAUTHORIZED' });
    });
  });
});
