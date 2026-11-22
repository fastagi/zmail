import { beforeEach, describe, expect, it, vi } from 'vitest';

import { GoApiClient } from '@/server/api-adapter/go-api-client';
import { ApiErrorCode } from '@/http/codes';

const randomId = '01234567-89ab-cdef-0123-456789abcdef';

describe('GoApiClient', () => {
  let fetchMock: ReturnType<typeof vi.fn<typeof fetch>>;
  let client: GoApiClient;

  beforeEach(() => {
    fetchMock = vi.fn<typeof fetch>();
    client = new GoApiClient(
      {
        apiUrl: 'https://api.example.com/v1',
        maxResponseBytes: 1_024,
        timeoutMs: 5_000,
        clientIpHeader: 'x-real-client-ip',
      },
      {
        fetch: fetchMock,
        randomUUID: () => randomId,
      }
    );
  });

  it('waits a route its own timeout instead of the client one, within bounds', async () => {
    const short = new GoApiClient(
      { apiUrl: 'https://api.example.com/v1', maxResponseBytes: 1_024, timeoutMs: 20 },
      {
        // Answers after 80 ms unless the request is aborted first.
        fetch: (_url, init) =>
          new Promise<Response>((resolve, reject) => {
            const timer = setTimeout(() => resolve(successResponse({})), 80);
            init?.signal?.addEventListener('abort', () => {
              clearTimeout(timer);
              reject(new DOMException('aborted', 'TimeoutError'));
            });
          }),
        randomUUID: () => randomId,
      }
    );
    const base = { method: 'POST' as const, path: 'external-mail-credentials', incomingHeaders: new Headers() };
    const timedOut = await short.request(base);
    expect(timedOut).toMatchObject({ ok: false, error: { errorCode: ApiErrorCode.COMMON_TIMEOUT } });
    expect(await short.request({ ...base, timeoutMs: 1_000 })).toMatchObject({ ok: true });
    for (const timeoutMs of [0, 1.5, 120_001]) {
      await expect(short.request({ ...base, timeoutMs })).rejects.toThrow(/timeout/u);
    }
  });

  it('forwards only server-owned credentials and explicit organization context', async () => {
    fetchMock.mockResolvedValueOnce(successResponse({ organization_id: 42 }));
    const token = 'header.payload.signature';

    const result = await client.request({
      method: 'GET',
      path: 'organization-context',
      accessToken: token,
      organizationId: '42',
      incomingHeaders: new Headers({
        authorization: 'Bearer browser-controlled',
        cookie: 'browser=controlled',
        'organization-id': '99',
        'x-real-client-ip': '203.0.113.9',
        'x-request-id': 'req-org-1',
      }),
    });

    expect(result).toMatchObject({
      ok: true,
      data: {
        status: 200,
        envelope: {
          code: 0,
          data: { organization_id: 42 },
        },
      },
    });
    const [url, init] = fetchMock.mock.calls[0];
    expect(String(url)).toBe('https://api.example.com/v1/organization-context');
    const headers = new Headers(init?.headers);
    expect(headers.get('authorization')).toBe(`Bearer ${token}`);
    expect(headers.get('organization-id')).toBe('42');
    expect(headers.get('cookie')).toBeNull();
    expect(headers.get('x-forwarded-for')).toBe('203.0.113.9');
    expect(headers.get('x-request-id')).toBe('req-org-1');
  });

  it('preserves pagination metadata and allowlisted response headers', async () => {
    fetchMock.mockResolvedValueOnce(
      Response.json(
        {
          code: 0,
          message: 'success',
          data: [{ id: 42 }],
          meta: {
            current_page: 1,
            per_page: 15,
            total: 1,
            last_page: 1,
            from: 1,
            to: 1,
          },
          links: { first: '/v1/organizations?page=1', last: '', prev: null, next: null },
        },
        {
          headers: {
            'cache-control': 'public, max-age=60',
            etag: '"settings-test"',
            vary: 'Organization-Id',
            'x-ratelimit-remaining': '99',
            'set-cookie': 'must-not-pass=1',
          },
        }
      )
    );

    const result = await client.request({
      method: 'GET',
      path: 'organizations',
      incomingHeaders: new Headers(),
    });

    expect(result).toMatchObject({
      ok: true,
      data: {
        envelope: {
          data: [{ id: 42 }],
          meta: { total: 1 },
        },
        responseHeaders: {
          'cache-control': 'public, max-age=60',
          etag: '"settings-test"',
          vary: 'Organization-Id',
          'x-ratelimit-remaining': '99',
        },
      },
    });
    if (result.ok) {
      expect(result.data.responseHeaders).not.toHaveProperty('set-cookie');
    }
  });

  it('forwards explicit conditional validators and accepts bodyless 304', async () => {
    fetchMock.mockResolvedValueOnce(
      new Response(null, {
        status: 304,
        headers: {
          'cache-control': 'public, max-age=60',
          etag: '"settings-test"',
        },
      })
    );

    const result = await client.request({
      method: 'GET',
      path: 'settings/public',
      ifNoneMatch: '"settings-test"',
      incomingHeaders: new Headers(),
    });

    expect(result).toMatchObject({
      ok: true,
      data: {
        status: 304,
        envelope: null,
        responseHeaders: {
          'cache-control': 'public, max-age=60',
          etag: '"settings-test"',
        },
      },
    });
    const headers = new Headers(fetchMock.mock.calls[0][1]?.headers);
    expect(headers.get('if-none-match')).toBe('"settings-test"');
  });

  it('rejects unsafe conditional validators before fetch', async () => {
    await expect(
      client.request({
        method: 'PATCH',
        path: 'settings/user/localization.locale',
        ifMatch: '"setting-v1"\r\nx-injected: true',
        incomingHeaders: new Headers(),
      })
    ).rejects.toThrow('bounded visible ASCII');
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('rejects oversized upstream bodies before parsing JSON', async () => {
    fetchMock.mockResolvedValueOnce(
      new Response(JSON.stringify({ code: 0, data: 'a'.repeat(2_048) }), {
        headers: { 'content-type': 'application/json' },
      })
    );

    await expect(
      client.request({
        method: 'GET',
        path: 'organizations',
        incomingHeaders: new Headers({ 'x-request-id': 'req-large-1' }),
      })
    ).resolves.toMatchObject({
      ok: false,
      error: {
        cause: 'unavailable',
        status: 503,
        errorCode: ApiErrorCode.COMMON_SERVICE_UNAVAILABLE,
        requestId: 'req-large-1',
      },
    });
  });

  it('reads a binary success as bounded bytes and keeps only a safe disposition', async () => {
    fetchMock.mockResolvedValueOnce(
      new Response(new Uint8Array([0, 255, 10]), {
        headers: { 'content-disposition': "attachment; filename*=utf-8''r%C3%A9sum%C3%A9.pdf" },
      })
    );
    const result = await client.request({
      method: 'GET',
      path: 'files/1',
      incomingHeaders: new Headers(),
      responseType: 'binary',
      maxResponseBytes: 4_096,
    });
    expect(result).toMatchObject({
      ok: true,
      data: {
        status: 200,
        envelope: null,
        binary: { disposition: "attachment; filename*=utf-8''r%C3%A9sum%C3%A9.pdf" },
      },
    });
    expect(result.ok && result.data.binary?.bytes).toEqual(new Uint8Array([0, 255, 10]));
    expect(new Headers(fetchMock.mock.calls[0]?.[1]?.headers).get('accept')).toContain(
      'application/octet-stream'
    );

    fetchMock.mockResolvedValueOnce(
      new Response('x', { headers: { 'content-disposition': 'inline; filename="a.html"' } })
    );
    const inline = await client.request({
      method: 'GET',
      path: 'files/2',
      incomingHeaders: new Headers(),
      responseType: 'binary',
    });
    expect(inline.ok && inline.data.binary?.disposition).toBeNull();

    // The per-request limit replaces the client limit, and a larger body is refused.
    fetchMock.mockResolvedValueOnce(new Response(new Uint8Array(5_000)));
    await expect(
      client.request({
        method: 'GET',
        path: 'files/3',
        incomingHeaders: new Headers(),
        responseType: 'binary',
        maxResponseBytes: 4_096,
      })
    ).resolves.toMatchObject({ ok: false, error: { cause: 'unavailable' } });

    // An error is still read as a JSON envelope.
    fetchMock.mockResolvedValueOnce(
      Response.json(
        { code: 404, error_code: 'MAILBOX.MESSAGE_NOT_FOUND', message: 'missing' },
        { status: 404 }
      )
    );
    await expect(
      client.request({
        method: 'GET',
        path: 'files/4',
        incomingHeaders: new Headers(),
        responseType: 'binary',
      })
    ).resolves.toMatchObject({
      ok: false,
      error: { status: 404, errorCode: 'MAILBOX.MESSAGE_NOT_FOUND' },
    });
  });

  it('rejects absolute, traversal, query-bearing, and fragment-bearing paths', async () => {
    for (const path of [
      'https://evil.example/path',
      '../admin',
      '/organizations',
      'organizations?page=1',
      'organizations#fragment',
    ]) {
      expect(() =>
        client.request({
          method: 'GET',
          path,
          incomingHeaders: new Headers(),
        })
      ).toThrow('relative API path');
    }
    expect(fetchMock).not.toHaveBeenCalled();
  });
});

function successResponse(data: unknown): Response {
  return Response.json({ code: 0, message: 'success', data });
}
