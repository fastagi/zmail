import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const cookieStore = vi.hoisted(() => ({ get: vi.fn(), set: vi.fn() }));

vi.mock('next/headers', () => ({
  cookies: async () => cookieStore,
  headers: async () => new Headers(),
}));

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

function post(path: string, body: unknown, origin = 'https://app.example.com'): Request {
  const result = new Request(`https://app.example.com${path}`, {
    method: 'POST',
    body: JSON.stringify(body),
  });
  // Origin is a forbidden header in the Request constructor; set it afterwards like a browser.
  result.headers.set('content-type', 'application/json');
  result.headers.set('origin', origin);
  return result;
}

describe('password reset browser routes', () => {
  let fetchMock: ReturnType<typeof vi.fn<typeof fetch>>;

  beforeEach(() => {
    vi.resetModules();
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

  it('forwards a reset request and a confirmation to the API', async () => {
    process.env.API_ADAPTER_ENABLED = 'true';
    process.env.API_UPSTREAM_URL = 'https://api.example.com/v1';
    fetchMock.mockImplementation(async () =>
      Response.json({ code: 0, message: 'success', data: { message: 'ok' } })
    );
    const request = await import('@/app/api/auth/password-reset/route');
    const confirm = await import('@/app/api/auth/password-reset/confirm/route');

    const asked = await request.POST(
      post('/api/auth/password-reset', { email: 'ann@example.com' })
    );
    expect(asked.status).toBe(200);
    await expect(asked.json()).resolves.toMatchObject({ data: { success: true } });
    expect(asked.headers.get('cache-control')).toBe('private, no-store');

    const confirmed = await confirm.POST(
      post('/api/auth/password-reset/confirm', { token: ' tok-123 ', password: 'secret-1' })
    );
    expect(confirmed.status).toBe(200);

    const calls = fetchMock.mock.calls.map(([url, init]) => ({
      path: new URL(String(url)).pathname,
      body: JSON.parse(String(init?.body)),
    }));
    expect(calls).toEqual([
      { path: '/v1/password/reset', body: { email: 'ann@example.com' } },
      { path: '/v1/password/reset/confirm', body: { token: 'tok-123', new_password: 'secret-1' } },
    ]);
  });

  it('passes an invalid token error through', async () => {
    process.env.API_ADAPTER_ENABLED = 'true';
    process.env.API_UPSTREAM_URL = 'https://api.example.com/v1';
    fetchMock.mockImplementation(async () =>
      Response.json(
        { code: 401, error_code: 'AUTH.PASSWORD_RESET_TOKEN_INVALID', message: 'invalid' },
        { status: 401 }
      )
    );
    const confirm = await import('@/app/api/auth/password-reset/confirm/route');
    const response = await confirm.POST(
      post('/api/auth/password-reset/confirm', { token: 'nope', password: 'secret-1' })
    );
    expect(response.status).toBe(401);
    await expect(response.json()).resolves.toMatchObject({
      error_code: 'AUTH.PASSWORD_RESET_TOKEN_INVALID',
    });
  });

  it('rejects bad input, other origins, and short passwords before the API', async () => {
    process.env.API_ADAPTER_ENABLED = 'true';
    process.env.API_UPSTREAM_URL = 'https://api.example.com/v1';
    const request = await import('@/app/api/auth/password-reset/route');
    const confirm = await import('@/app/api/auth/password-reset/confirm/route');

    expect((await request.POST(post('/api/auth/password-reset', { email: 'nope' }))).status).toBe(
      422
    );
    expect(
      (
        await request.POST(
          post('/api/auth/password-reset', { email: 'a@example.com' }, 'https://evil.example')
        )
      ).status
    ).toBe(403);
    expect(
      (
        await confirm.POST(
          post('/api/auth/password-reset/confirm', { token: 'x', password: '123' })
        )
      ).status
    ).toBe(422);
    expect(fetchMock).not.toHaveBeenCalled();
  });
});
