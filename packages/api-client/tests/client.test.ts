import assert from 'node:assert/strict';
import test from 'node:test';

import { createHttpClient } from '../src/client.ts';
import { createInboxClient } from '../src/inbox.ts';
import { ApiClientError, ClientErrorCode } from '../src/types.ts';

function jsonResponse(body: unknown, status = 200, headers: Record<string, string> = {}): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'content-type': 'application/json', ...headers },
  });
}

test('HttpClient sends Bearer token, Organization-Id, and X-Request-Id in bearer mode', async () => {
  let capturedUrl = '';
  let capturedInit: RequestInit | undefined;

  const client = createHttpClient({
    baseUrl: 'https://api.zmail.app/api/v1/',
    authMode: 'bearer',
    organizationId: 42,
    getAccessToken: async () => 'tok_secret_123',
    generateRequestId: () => 'req_fixed_1',
    fetch: async (input, init) => {
      capturedUrl = input;
      capturedInit = init;
      return jsonResponse({ code: 0, message: 'success', data: { ok: true } });
    },
  });

  const data = await client.get<{ ok: boolean }>('/health', { query: { detail: true } });

  assert.deepEqual(data, { ok: true });
  assert.equal(capturedUrl, 'https://api.zmail.app/api/v1/health?detail=true');
  assert.equal(capturedInit?.credentials, 'omit');

  const headers = new Headers(capturedInit?.headers);
  assert.equal(headers.get('authorization'), 'Bearer tok_secret_123');
  assert.equal(headers.get('organization-id'), '42');
  assert.equal(headers.get('x-request-id'), 'req_fixed_1');
});

test('HttpClient uses credentials include in cookie mode and parses canonical error envelopes', async () => {
  let capturedCredentials: RequestCredentials | undefined;

  const client = createHttpClient({
    baseUrl: '/api',
    authMode: 'cookie',
    fetch: async (_input, init) => {
      capturedCredentials = init?.credentials;
      return jsonResponse(
        {
          code: 422,
          error_code: 'COMMON.VALIDATION_FAILED',
          message: 'Validation failed',
          errors: { subject: ['is required'] },
          request_id: 'req_server_99',
        },
        422,
        { 'retry-after': '12' }
      );
    },
  });

  await assert.rejects(
    () => client.post('/mail-drafts', { subject: '' }),
    (error: unknown) => {
      assert.ok(error instanceof ApiClientError);
      assert.equal(error.status, 422);
      assert.equal(error.errorCode, 'COMMON.VALIDATION_FAILED');
      assert.equal(error.requestId, 'req_server_99');
      assert.deepEqual(error.fieldErrors, { subject: ['is required'] });
      assert.equal(error.retryAfterSeconds, 12);
      return true;
    }
  );
  assert.equal(capturedCredentials, 'include');
});

test('HttpClient enforces timeout and maps to CLIENT.TIMEOUT', async () => {
  const client = createHttpClient({
    baseUrl: 'https://api.zmail.app/api/v1',
    timeoutMs: 25,
    fetch: async (_input, init) =>
      new Promise<Response>((_resolve, reject) => {
        init?.signal?.addEventListener('abort', () => {
          reject(new Error('Aborted'));
        });
      }),
  });

  await assert.rejects(
    () => client.get('/slow'),
    (error: unknown) => {
      assert.ok(error instanceof ApiClientError);
      assert.equal(error.errorCode, ClientErrorCode.TIMEOUT);
      return true;
    }
  );
});

test('createInboxClient requests cursor pages cleanly', async () => {
  const client = createHttpClient({
    baseUrl: 'https://api.zmail.app/api/v1',
    authMode: 'bearer',
    fetch: async () =>
      jsonResponse({
        code: 0,
        message: 'success',
        data: [],
        meta: { per_page: 20, has_more: true, next_cursor: 'cur_next' },
      }),
  });

  const inbox = createInboxClient(client);
  const page = await inbox.listByCursor({ unread: true, per_page: 20 });

  assert.equal(page.meta.has_more, true);
  assert.equal(page.meta.next_cursor, 'cur_next');
});
