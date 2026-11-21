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
      source.matchAll(/^import\s+(?!type\b)[^;]*?from\s+'(@\/features\/mailbox\/[^']+)';/gmsu),
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
    for (const module of heavy) expect([...seen]).not.toContain(module);
  });
});
