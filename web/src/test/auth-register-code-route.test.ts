import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const cookieStore = vi.hoisted(() => ({ get: vi.fn(), set: vi.fn(), delete: vi.fn() }));

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
  result.headers.set('content-type', 'application/json');
  result.headers.set('origin', origin);
  return result;
}

describe('registration code browser routes', () => {
  let fetchMock: ReturnType<typeof vi.fn<typeof fetch>>;

  beforeEach(() => {
    vi.resetModules();
    fetchMock = vi.fn<typeof fetch>();
    vi.stubGlobal('fetch', fetchMock);
    process.env.NEXT_PUBLIC_API_URL = '/api';
    process.env.NEXT_PUBLIC_APP_URL = 'https://app.example.com';
    process.env.API_UPSTREAM_TIMEOUT_MS = '5000';
    process.env.API_UPSTREAM_MAX_RESPONSE_BYTES = '1048576';
    process.env.API_ADAPTER_ENABLED = 'true';
    process.env.API_UPSTREAM_URL = 'https://api.example.com/v1';
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.resetModules();
    for (const key of managedKeys) {
      delete process.env[key];
      if (originalEnv[key] !== undefined) process.env[key] = originalEnv[key];
    }
  });

  it('asks the API to mail a code and answers without saying whether the address is taken', async () => {
    fetchMock.mockImplementation(async () =>
      Response.json({ code: 0, message: 'accepted', data: { message: 'ok' } }, { status: 202 })
    );
    const route = await import('@/app/api/auth/register/email-code/route');

    const response = await route.POST(
      post('/api/auth/register/email-code', { email: 'ann@example.com' })
    );

    expect(response.status).toBe(200);
    await expect(response.json()).resolves.toMatchObject({ data: { success: true } });
    expect(response.headers.get('cache-control')).toBe('private, no-store');
    const [url, init] = fetchMock.mock.calls[0];
    expect(new URL(String(url)).pathname).toBe('/v1/register/email-codes');
    expect(JSON.parse(String(init?.body))).toEqual({ email: 'ann@example.com' });
  });

  it('rejects other origins and bad addresses before the API', async () => {
    const route = await import('@/app/api/auth/register/email-code/route');

    expect(
      (
        await route.POST(
          post('/api/auth/register/email-code', { email: 'a@example.com' }, 'https://evil.example')
        )
      ).status
    ).toBe(403);
    expect(
      (await route.POST(post('/api/auth/register/email-code', { email: 'nope' }))).status
    ).toBe(422);
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('forwards the code with the registration and passes a code error through', async () => {
    fetchMock.mockImplementation(async () =>
      Response.json(
        { code: 400, error_code: 'AUTH.REGISTRATION_EMAIL_CODE_INVALID', message: 'invalid' },
        { status: 400 }
      )
    );
    const route = await import('@/app/api/auth/register/route');

    const response = await route.POST(
      post('/api/auth/register', {
        name: 'Ann',
        email: 'ann@example.com',
        password: 'secret-123',
        code: '042917',
      })
    );

    expect(response.status).toBe(400);
    await expect(response.json()).resolves.toMatchObject({
      error_code: 'AUTH.REGISTRATION_EMAIL_CODE_INVALID',
    });
    const [url, init] = fetchMock.mock.calls[0];
    expect(new URL(String(url)).pathname).toBe('/v1/register');
    expect(JSON.parse(String(init?.body))).toMatchObject({
      email: 'ann@example.com',
      email_code: '042917',
    });
  });

  it('requires a six-digit code before the API', async () => {
    const route = await import('@/app/api/auth/register/route');

    for (const code of [undefined, '12345', '12345a']) {
      const response = await route.POST(
        post('/api/auth/register', {
          name: 'Ann',
          email: 'ann@example.com',
          password: 'secret-123',
          ...(code === undefined ? {} : { code }),
        })
      );
      expect(response.status).toBe(422);
    }
    expect(fetchMock).not.toHaveBeenCalled();
  });
});
