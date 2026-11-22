import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { afterEach, describe, expect, it, vi } from 'vitest';

import {
  externalMailService,
  isNavigableUrl,
} from '@/features/mailbox/services/external-mail-service';
import { ClientErrorCode } from '@/http/codes';
import request, { ApiError } from '@/http/request';

afterEach(() => {
  vi.restoreAllMocks();
});

describe('external mail service', () => {
  it.each([
    ['https://accounts.google.com/o/oauth2/v2/auth?state=s', true],
    ['http://localhost:3000/console/mailboxes/connected/callback?state=s', true],
    ['http://app.localhost/callback', true],
    ['http://127.0.0.1:3107/callback', true],
    ['http://[::1]:3107/callback', true],
    ['http://accounts.example.com/auth', false],
    ['http://localhost.evil.example/auth', false],
    ['http://127.0.0.2/auth', false],
    ['javascript:alert(1)', false],
    ['data:text/html,hi', false],
    ['//accounts.example.com/auth', false],
    ['not a url', false],
  ])('%s is navigable: %s', (url, expected) => {
    expect(isNavigableUrl(url)).toBe(expected);
  });

  it('refuses an authorization URL it would not navigate to', async () => {
    vi.spyOn(request, 'post').mockResolvedValue({
      authorization_url: 'http://accounts.example.com/auth?state=s',
      expires_at: '2026-11-21T10:10:00Z',
    });

    const failure = await externalMailService
      .start(7, { provider: 'gmail' })
      .catch((error: unknown) => error);
    expect(failure).toBeInstanceOf(ApiError);
    expect((failure as ApiError).errorCode).toBe(ClientErrorCode.INVALID_RESPONSE);
  });

  it('accepts an https authorization URL', async () => {
    const post = vi.spyOn(request, 'post').mockResolvedValue({
      authorization_url: 'https://accounts.example.com/auth?state=s',
      expires_at: '2026-11-21T10:10:00Z',
    });

    await expect(externalMailService.start(7, { provider: 'gmail', backfill_days: 30 })).resolves
      .toMatchObject({ authorization_url: 'https://accounts.example.com/auth?state=s' });
    expect(post).toHaveBeenCalledWith(
      '/external-mail-authorizations',
      { provider: 'gmail', backfill_days: 30 },
      { headers: { 'Organization-Id': '7' } }
    );
  });
});

describe('external mail service: app passwords, sync runs, purges, directory', () => {
  const connection = {
    id: 9,
    provider: 'imap',
    account_address: 'me@example.com',
    mailbox_id: 77,
    status: 'active',
    last_synced_at: null,
    last_error_code: null,
    created_at: '2026-11-21T10:00:00Z',
  };
  const input = {
    provider: 'imap',
    host: 'imap.example.com',
    port: 993,
    username: 'me@example.com',
    password: 'app-secret',
    backfill_days: 30,
  };

  it.each([
    [201, 'created'],
    [200, 'restored'],
  ] as const)('binds with an app password: %i is %s', async (status, outcome) => {
    const post = vi
      .spyOn(request, 'post')
      .mockResolvedValue({ status, data: { code: 0, message: 'success', data: connection } });
    const result = await externalMailService.bindCredentials(7, input);
    expect(result).toEqual({ outcome, connection });
    expect(post).toHaveBeenCalledWith('/external-mail-credentials', input, {
      headers: { 'Organization-Id': '7' },
      fastbuildResponseMode: 'response',
    });
    // The answer is all that is returned: never the request, which held the password.
    expect(JSON.stringify(result)).not.toContain('app-secret');
  });

  it.each([
    ['another port', { port: 143 }],
    ['an empty password', { password: '' }],
    ['a control character', { username: 'me\u0007' }],
    ['a backfill out of range', { backfill_days: 91 }],
    ['an invalid connector id', { provider: 'IMAP!' }],
  ])('refuses %s before any request', async (_name, change) => {
    const post = vi.spyOn(request, 'post');
    const failure = await externalMailService
      .bindCredentials(7, { ...input, ...change })
      .catch((error: unknown) => error);
    expect(failure).toBeInstanceOf(ApiError);
    expect(post).not.toHaveBeenCalled();
  });

  it('reads sync runs and refuses an unexpected shape', async () => {
    const run = {
      id: 1,
      started_at: '2026-11-20T08:00:00Z',
      finished_at: '2026-11-20T08:00:01Z',
      outcome: 'ok',
      fetched_count: 2,
      stored_count: 2,
      more: false,
      error_code: null,
      timed_out: false,
      throttled: false,
    };
    const get = vi.spyOn(request, 'get').mockResolvedValueOnce([run]);
    await expect(externalMailService.syncRuns(7, 3)).resolves.toEqual([run]);
    expect(get).toHaveBeenCalledWith('/external-mail-connections/3/sync-runs', {
      headers: { 'Organization-Id': '7' },
    });
    get.mockResolvedValueOnce([{ ...run, subject: 'leaked' }]);
    await expect(externalMailService.syncRuns(7, 3)).rejects.toBeInstanceOf(ApiError);
  });

  it('purges and reads the directory by cursor', async () => {
    const post = vi
      .spyOn(request, 'post')
      .mockResolvedValue({ deleted_messages: 5000, completed: false });
    await expect(externalMailService.purge(7, 3)).resolves.toEqual({
      deleted_messages: 5000,
      completed: false,
    });
    expect(post).toHaveBeenCalledWith('/external-mail-connections/3/purges', undefined, {
      headers: { 'Organization-Id': '7' },
    });

    const entry = {
      id: 3,
      provider: 'gmail',
      status: 'active',
      masked_account_address: 'st***@gmail.com',
      owner_user_id: 5,
      owner_display_name: 'stark',
      last_synced_at: null,
      created_at: '2026-11-19T08:00:00Z',
    };
    const getEnvelope = vi.spyOn(request, 'getEnvelope').mockResolvedValue({
      code: 0,
      message: 'success',
      data: [entry],
      meta: { per_page: 15, has_more: true, next_cursor: 'abc' },
    });
    await expect(externalMailService.directory(7, 'xyz')).resolves.toEqual({
      items: [entry],
      meta: { per_page: 15, has_more: true, next_cursor: 'abc' },
    });
    expect(getEnvelope).toHaveBeenCalledWith('/external-mail-connection-directory', {
      headers: { 'Organization-Id': '7' },
      params: { per_page: 15, cursor: 'xyz' },
    });
    // A directory row never carries the full address or message data.
    getEnvelope.mockResolvedValueOnce({
      code: 0,
      message: 'success',
      data: [{ ...entry, account_address: 'stark@gmail.com' }],
      meta: { per_page: 15, has_more: false, next_cursor: null },
    });
    await expect(externalMailService.directory(7)).rejects.toBeInstanceOf(ApiError);

    const del = vi.spyOn(request, 'delete').mockResolvedValue(undefined);
    await externalMailService.forceDisconnect(7, 3);
    expect(del).toHaveBeenCalledWith('/external-mail-connection-directory/3', {
      headers: { 'Organization-Id': '7' },
    });
  });
});

describe('callback page module graph', () => {
  const root = resolve(process.cwd(), 'src');
  const heavy = [
    '@/features/mailbox/hooks/use-mailbox',
    '@/features/mailbox/hooks/use-mail-organization',
    '@/features/mailbox/services/mailbox-service',
  ];

  /** The mailbox modules a file loads statically; `import type` and `import()` load nothing. */
  function staticMailboxImports(file: string): string[] {
    const source = readFileSync(file, 'utf8');
    return Array.from(
      source.matchAll(/^import\s+(?!type\b)[^;]*?from\s+'(@\/features\/mailbox\/[^']+)';/gmu),
      match => match[1]
    );
  }

  function fileOf(specifier: string): string {
    const base = resolve(root, specifier.replace('@/', ''));
    for (const candidate of [`${base}.ts`, `${base}.tsx`, `${base}/index.ts`]) {
      try {
        readFileSync(candidate);
        return candidate;
      } catch {
        // try the next extension
      }
    }
    throw new Error(`cannot resolve ${specifier}`);
  }

  it('loads neither the mailbox hooks nor the organization hook', () => {
    const seen = new Set<string>();
    const queue = ['@/features/mailbox/components/external-mail-callback'];
    while (queue.length > 0) {
      const specifier = queue.pop() as string;
      if (seen.has(specifier)) continue;
      seen.add(specifier);
      queue.push(...staticMailboxImports(fileOf(specifier)));
    }
    for (const specifier of heavy) expect([...seen]).not.toContain(specifier);
  });
});
