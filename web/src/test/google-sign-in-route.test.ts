import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const cookieStore = vi.hoisted(() => ({
  get: vi.fn(),
  set: vi.fn(),
}));
const requestHeaders = vi.hoisted(() => new Headers({ 'x-forwarded-for': '203.0.113.20' }));

vi.mock('next/headers', () => ({
  cookies: async () => cookieStore,
  headers: async () => requestHeaders,
}));

const ENV_KEYS = [
  'API_ADAPTER_ENABLED',
  'API_UPSTREAM_TIMEOUT_MS',
  'API_UPSTREAM_MAX_RESPONSE_BYTES',
  'API_UPSTREAM_URL',
  'API_CLIENT_IP_HEADER',
  'MOCK_BFF_ENABLED',
  'NEXT_PHASE',
  'NEXT_PUBLIC_API_URL',
  'NEXT_PUBLIC_APP_URL',
] as const;
const originalEnv = { ...process.env };
const STATE = 'Zs6f1m2KkTn0wq3rJx8yBv5cD7eA9hGu4iLpQoRsTt0';
const GOOGLE_URL = `https://accounts.google.com/o/oauth2/v2/auth?client_id=x&state=${STATE}`;

describe('Google sign-in BFF routes', () => {
  let fetchMock: ReturnType<typeof vi.fn<typeof fetch>>;

  beforeEach(() => {
    vi.resetModules();
    fetchMock = vi.fn<typeof fetch>();
    vi.stubGlobal('fetch', fetchMock);
    cookieStore.get.mockReset();
    cookieStore.set.mockReset();
    process.env.API_ADAPTER_ENABLED = 'true';
    process.env.API_UPSTREAM_TIMEOUT_MS = '5000';
    process.env.API_UPSTREAM_MAX_RESPONSE_BYTES = '1048576';
    process.env.API_UPSTREAM_URL = 'https://api.example.com/v1';
    process.env.API_CLIENT_IP_HEADER = 'x-forwarded-for';
    process.env.NEXT_PUBLIC_API_URL = '/api';
    process.env.NEXT_PUBLIC_APP_URL = 'https://app.example.com';
    delete process.env.NEXT_PHASE;
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.resetModules();
    for (const key of ENV_KEYS) {
      delete process.env[key];
      if (originalEnv[key] !== undefined) process.env[key] = originalEnv[key];
    }
  });

  async function start(query = '') {
    const { GET } = await import('@/app/api/auth/google/route');
    return GET(new Request(`https://app.example.com/api/auth/google${query}`));
  }

  async function callback(query: string, cookieState: string | null = STATE) {
    cookieStore.get.mockImplementation((name: string) =>
      name === 'zmail_google_state' && cookieState !== null ? { value: cookieState } : undefined
    );
    const { GET } = await import('@/app/api/auth/google/callback/route');
    return GET(new Request(`https://app.example.com/api/auth/google/callback${query}`));
  }

  it('starts at Google with the registered callback and binds the state to the browser', async () => {
    fetchMock.mockResolvedValueOnce(
      Response.json(
        { code: 0, message: 'success', data: { authorization_url: GOOGLE_URL, state: STATE, expires_in: 600 } },
        { status: 201 }
      )
    );

    const response = await start('?returnUrl=%2Fconsole%2Fadmin%2Fdomains');

    expect(response.status).toBe(303);
    expect(response.headers.get('location')).toBe(GOOGLE_URL);
    expect(response.headers.get('cache-control')).toContain('no-store');
    const [url, init] = fetchMock.mock.calls[0] ?? [];
    expect(String(url)).toBe('https://api.example.com/v1/auth/google/authorizations');
    expect(JSON.parse(String(init?.body))).toEqual({
      redirect_uri: 'https://app.example.com/api/auth/google/callback',
      return_url: '/console/admin/domains',
    });
    const cookie = response.headers.get('set-cookie') ?? '';
    expect(cookie).toContain(`zmail_google_state=${STATE}`);
    expect(cookie).toMatch(/HttpOnly/i);
    expect(cookie).toMatch(/SameSite=lax/i);
    expect(cookie).toMatch(/Max-Age=600/i);
  });

  it('never sends an off-site return URL upstream', async () => {
    fetchMock.mockResolvedValueOnce(
      Response.json(
        { code: 0, message: 'success', data: { authorization_url: GOOGLE_URL, state: STATE, expires_in: 600 } },
        { status: 201 }
      )
    );
    await start('?returnUrl=%2F%2Fevil.example%2Fsteal');
    const [, init] = fetchMock.mock.calls[0] ?? [];
    expect(JSON.parse(String(init?.body)).return_url).toBe('/console');
  });

  it('goes back to sign-in when the API has no Google client', async () => {
    fetchMock.mockResolvedValueOnce(
      Response.json(
        { code: 503, message: 'not configured', error_code: 'AUTH.GOOGLE_NOT_CONFIGURED' },
        { status: 503 }
      )
    );
    const response = await start('?returnUrl=%2Fconsole%2Finbox');
    expect(response.status).toBe(303);
    expect(response.headers.get('location')).toBe(
      'https://app.example.com/login?googleError=AUTH.GOOGLE_NOT_CONFIGURED&returnUrl=%2Fconsole%2Finbox'
    );
  });

  it('refuses a callback whose state is not this browser’s, without calling the API', async () => {
    const response = await callback(`?state=${STATE}&code=abc`, 'Xs6f1m2KkTn0wq3rJx8yBv5cD7eA9hGu4iLpQoRsTt0');
    expect(response.headers.get('location')).toBe(
      'https://app.example.com/login?googleError=AUTH.GOOGLE_STATE_INVALID'
    );
    expect(fetchMock).not.toHaveBeenCalled();
    expect(cookieStore.set).toHaveBeenCalledWith(expect.objectContaining({ name: 'zmail_google_state', maxAge: 0 }));

    const noCookie = await callback(`?state=${STATE}&code=abc`, null);
    expect(noCookie.headers.get('location')).toContain('googleError=AUTH.GOOGLE_STATE_INVALID');
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('reports a cancelled consent', async () => {
    const response = await callback(`?error=access_denied&state=${STATE}`);
    expect(response.headers.get('location')).toBe('https://app.example.com/login?googleError=cancelled');
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('signs in, sets the session cookie, and lands on the stored return URL', async () => {
    fetchMock.mockResolvedValueOnce(googleSessionResponse('/console/admin/domains'));

    const response = await callback(`?state=${STATE}&code=4%2F0Ab-code`);

    expect(response.status).toBe(303);
    expect(response.headers.get('location')).toBe('https://app.example.com/console/admin/domains');
    const [url, init] = fetchMock.mock.calls[0] ?? [];
    expect(String(url)).toBe('https://api.example.com/v1/auth/google/sessions');
    expect(JSON.parse(String(init?.body))).toEqual({ state: STATE, code: '4/0Ab-code' });
    expect(cookieStore.set).toHaveBeenCalledWith(
      expect.objectContaining({ name: 'fastbuild_auth', value: opaqueCredential(), httpOnly: true })
    );
    expect(cookieStore.set).toHaveBeenCalledWith(expect.objectContaining({ name: 'zmail_google_state', maxAge: 0 }));
  });

  it('ignores an off-site return URL even from the API', async () => {
    fetchMock.mockResolvedValueOnce(googleSessionResponse('//evil.example/steal'));
    const response = await callback(`?state=${STATE}&code=abc`);
    expect(response.headers.get('location')).toBe('https://app.example.com/console');
  });

  it('passes the API refusal code to the sign-in page', async () => {
    fetchMock.mockResolvedValueOnce(
      Response.json(
        { code: 403, message: 'unverified', error_code: 'AUTH.GOOGLE_EMAIL_UNVERIFIED' },
        { status: 403 }
      )
    );
    const response = await callback(`?state=${STATE}&code=abc`);
    expect(response.headers.get('location')).toBe(
      'https://app.example.com/login?googleError=AUTH.GOOGLE_EMAIL_UNVERIFIED'
    );
    expect(cookieStore.set).not.toHaveBeenCalledWith(expect.objectContaining({ name: 'fastbuild_auth' }));
  });

  it('shows the button only when the API reports a Google client', async () => {
    fetchMock.mockResolvedValueOnce(
      Response.json({ code: 0, message: 'success', data: { google: { enabled: true } } })
    );
    const googleSignIn = await import('@/features/auth/server/google-sign-in');
    expect(await googleSignIn.isGoogleSignInAvailable(1_000)).toBe(true);
    expect(await googleSignIn.isGoogleSignInAvailable(2_000)).toBe(true);
    expect(fetchMock).toHaveBeenCalledTimes(1);

    googleSignIn.resetGoogleSignInAvailabilityCache();
    fetchMock.mockRejectedValueOnce(new Error('down'));
    expect(await googleSignIn.isGoogleSignInAvailable(3_000)).toBe(false);

    googleSignIn.resetGoogleSignInAvailabilityCache();
    fetchMock.mockResolvedValueOnce(
      Response.json({ code: 0, message: 'success', data: { google: { enabled: false } } })
    );
    expect(await googleSignIn.isGoogleSignInAvailable(4_000)).toBe(false);
  });

  it('hides the button without the production adapter', async () => {
    process.env.API_ADAPTER_ENABLED = 'false';
    const googleSignIn = await import('@/features/auth/server/google-sign-in');
    expect(await googleSignIn.isGoogleSignInAvailable()).toBe(false);
    expect(fetchMock).not.toHaveBeenCalled();
  });
});

function googleSessionResponse(returnUrl: string): Response {
  return Response.json({
    code: 0,
    message: 'success',
    data: {
      access_token: opaqueCredential(),
      token_type: 'Bearer',
      expires_in: 600,
      return_url: returnUrl,
      user: { id: 42, username: 'user_abc', email: 'ada@example.com', nickname: 'Ada', status: 1 },
    },
  });
}

function opaqueCredential(): string {
  return Buffer.from('0123456789abcdef0123456789abcdef').toString('base64url');
}
