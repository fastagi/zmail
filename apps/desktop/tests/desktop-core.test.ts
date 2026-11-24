import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { describe, it } from 'node:test';
import {
  KeychainStore,
  KeychainUnavailableError,
  createFileKeychainBackend,
  createMemoryKeychainBackend,
  type SafeStorageAdapter,
} from '../src/main/keychain.ts';
import {
  LocalMailStore,
  createFileStorePersistence,
  tokenizeText,
} from '../src/main/local-store.ts';
import {
  createHardenedWindowOptions,
  isAllowedExternalOAuthUrl,
  parseOAuthDeepLink,
} from '../src/main/main.ts';
import { IPC_CHANNELS, type CachedMailMessage } from '../src/shared/ipc-channels.ts';

function createXorSafeStorage(available = true): SafeStorageAdapter {
  const key = 0x5a;
  return {
    isEncryptionAvailable: () => available,
    encryptString(plainText: string): Buffer {
      const raw = Buffer.from(plainText, 'utf8');
      const masked = Buffer.alloc(raw.length);
      for (let i = 0; i < raw.length; i += 1) {
        masked[i] = (raw[i] ?? 0) ^ key;
      }
      return masked;
    },
    decryptString(encrypted: Buffer): string {
      const out = Buffer.alloc(encrypted.length);
      for (let i = 0; i < encrypted.length; i += 1) {
        out[i] = (encrypted[i] ?? 0) ^ key;
      }
      return out.toString('utf8');
    },
  };
}

function makeMessage(overrides: Partial<CachedMailMessage> & { id: number; accountId: string }): CachedMailMessage {
  return {
    mailboxId: 1,
    threadId: overrides.id,
    from: 'sender@zmail.dev',
    to: ['alex@zmail.dev'],
    cc: [],
    subject: 'Default Subject',
    snippet: 'Default snippet',
    bodyText: 'Default body text',
    read: false,
    starred: false,
    placement: 'default',
    attachmentCount: 0,
    createdAt: '2026-11-24T12:00:00Z',
    ...overrides,
  };
}

describe('IPC channel contract', () => {
  it('defines unique channel names for every IPC endpoint', () => {
    const values = Object.values(IPC_CHANNELS);
    const unique = new Set(values);
    assert.equal(unique.size, values.length);
    for (const channel of values) {
      assert.match(channel, /^zmail:[a-z]+:[a-z-]+$/);
    }
  });
});

describe('KeychainStore (safeStorage wrapper)', () => {
  it('encrypts Bearer tokens and never writes plaintext to disk', () => {
    const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'zmail-keychain-'));
    try {
      const filePath = path.join(tmpDir, 'keychain.enc.json');
      const secretToken = 'zmail_live_bearer_token_987654321';
      const adapter = createXorSafeStorage(true);

      const store = new KeychainStore(adapter, createFileKeychainBackend(filePath));
      store.setToken('acct-work-1', secretToken);

      const rawDisk = fs.readFileSync(filePath, 'utf8');
      assert.equal(rawDisk.includes(secretToken), false, 'disk file must not contain plaintext token');
      assert.equal(fs.statSync(filePath).mode & 0o777, 0o600);

      const reloaded = new KeychainStore(adapter, createFileKeychainBackend(filePath));
      assert.equal(reloaded.getToken('acct-work-1'), secretToken);
      assert.deepEqual(reloaded.listAccountIds(), ['acct-work-1']);

      assert.equal(reloaded.deleteToken('acct-work-1'), true);
      assert.equal(reloaded.getToken('acct-work-1'), null);
      assert.deepEqual(reloaded.listAccountIds(), []);
    } finally {
      fs.rmSync(tmpDir, { recursive: true, force: true });
    }
  });

  it('refuses to persist tokens when OS encryption is unavailable', () => {
    const backend = createMemoryKeychainBackend();
    const store = new KeychainStore(createXorSafeStorage(false), backend);

    assert.throws(
      () => store.setToken('acct-work-1', 'secret-token'),
      KeychainUnavailableError
    );
    assert.equal(backend.read(), null);
  });
});

describe('LocalMailStore (offline cache, FTS, and delta cursors)', () => {
  it('ranks subject matches above body matches and supports multi-term AND + prefix + CJK search', () => {
    const store = new LocalMailStore();
    store.upsertMessages([
      makeMessage({
        id: 1,
        accountId: 'acct-work',
        from: 'ops@zmail.dev',
        subject: 'Quarterly Invoice Approval',
        snippet: 'Please review Q4 billing summary.',
        bodyText: 'Attached is the quarterly billing summary for Q4.',
        createdAt: '2026-11-24T10:00:00Z',
      }),
      makeMessage({
        id: 2,
        accountId: 'acct-work',
        from: 'finance@zmail.dev',
        subject: 'Weekly Sync Notes',
        snippet: 'Mentioned invoice during standup.',
        bodyText: 'We briefly mentioned invoice approval in the standup notes.',
        createdAt: '2026-11-24T11:00:00Z',
      }),
      makeMessage({
        id: 3,
        accountId: 'acct-personal',
        from: 'billing@service.cn',
        subject: '\u5341\u4e00\u6708\u7535\u5b50\u53d1\u7968\u901a\u77e5',
        snippet: '\u4e91\u5b58\u50a8\u53d1\u7968\u5df2\u5f00\u5177',
        bodyText: '\u60a8\u7684\u7535\u5b50\u53d1\u7968\u5df2\u5f00\u5177',
        createdAt: '2026-11-24T12:00:00Z',
      }),
    ]);

    const invoiceHits = store.search({ query: 'inv approv', accountId: 'acct-work' });
    assert.equal(invoiceHits.total, 2);
    assert.equal(invoiceHits.hits[0]?.message.id, 1);
    assert.ok((invoiceHits.hits[0]?.score ?? 0) > (invoiceHits.hits[1]?.score ?? 0));

    const cjkHits = store.search({ query: '\u7535\u5b50\u53d1\u7968', accountId: 'acct-personal' });
    assert.equal(cjkHits.total, 1);
    assert.equal(cjkHits.hits[0]?.message.id, 3);
  });

  it('cleans up stale index tokens on update and delete', () => {
    const store = new LocalMailStore();
    store.upsertMessages([
      makeMessage({
        id: 42,
        accountId: 'acct-work',
        subject: 'AlphaSecretCodename',
        bodyText: 'Initial draft',
      }),
    ]);
    assert.equal(store.search({ query: 'alphasecretcodename' }).total, 1);

    store.upsertMessages([
      makeMessage({
        id: 42,
        accountId: 'acct-work',
        subject: 'BetaPublicRelease',
        bodyText: 'Updated draft',
      }),
    ]);
    assert.equal(store.search({ query: 'alphasecretcodename' }).total, 0);
    assert.equal(store.search({ query: 'betapublicrelease' }).total, 1);

    assert.equal(store.deleteMessage('acct-work', 42), true);
    assert.equal(store.search({ query: 'betapublicrelease' }).total, 0);
  });

  it('supports 30 isolated accounts and persists delta cursors across restarts', () => {
    const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'zmail-store-'));
    try {
      const storePath = path.join(tmpDir, 'mail-cache.json');
      const store = new LocalMailStore(createFileStorePersistence(storePath));

      const batch: CachedMailMessage[] = [];
      for (let i = 1; i <= 30; i += 1) {
        const accountId = `acct-${i}`;
        batch.push(
          makeMessage({
            id: i,
            accountId,
            subject: `Welcome mailbox ${i}`,
            read: i % 2 === 0,
          })
        );
        store.setDeltaCursor({
          accountId,
          token: `cursor-${i}`,
          updatedAt: '2026-11-24T20:00:00Z',
        });
      }
      store.upsertMessages(batch);

      const reloaded = new LocalMailStore(createFileStorePersistence(storePath));
      const stats = reloaded.getStats();
      assert.equal(stats.accountCount, 30);
      assert.equal(stats.totalMessages, 30);
      assert.equal(stats.unreadMessages, 15);
      assert.equal(reloaded.getDeltaCursor('acct-30').token, 'cursor-30');

      reloaded.resetAccount('acct-30');
      assert.equal(reloaded.listMessages({ accountId: 'acct-30' }).length, 0);
      assert.equal(reloaded.getDeltaCursor('acct-30').token, null);
    } finally {
      fs.rmSync(tmpDir, { recursive: true, force: true });
    }
  });

  it('tokenizes email addresses into domain and local fragments', () => {
    const tokens = tokenizeText('release-bot@zmail.dev');
    assert.ok(tokens.includes('release'));
    assert.ok(tokens.includes('bot'));
    assert.ok(tokens.includes('zmail'));
  });
});

describe('Electron main security & deep-link helpers', () => {
  it('enforces hardened BrowserWindow webPreferences', () => {
    const opts = createHardenedWindowOptions('/app/dist/preload/index.js');
    assert.equal(opts.webPreferences.contextIsolation, true);
    assert.equal(opts.webPreferences.nodeIntegration, false);
    assert.equal(opts.webPreferences.sandbox, true);
    assert.equal(opts.webPreferences.webSecurity, true);
    assert.equal(opts.webPreferences.allowRunningInsecureContent, false);
  });

  it('parses valid zmail://oauth/callback deep links and rejects invalid schemes', () => {
    const valid = parseOAuthDeepLink('zmail://oauth/callback?code=abc123&state=xyz&account=acct-1');
    assert.deepEqual(valid, {
      code: 'abc123',
      state: 'xyz',
      account: 'acct-1',
      error: null,
      rawUrl: 'zmail://oauth/callback?code=abc123&state=xyz&account=acct-1',
    });

    assert.equal(parseOAuthDeepLink('https://evil.example/oauth/callback?code=abc'), null);
    assert.equal(parseOAuthDeepLink('zmail://other/path?code=abc'), null);
    assert.equal(parseOAuthDeepLink('not a url'), null);
  });

  it('allows only https and localhost http external OAuth URLs', () => {
    assert.equal(isAllowedExternalOAuthUrl('https://auth.zmail.dev/oauth/start'), true);
    assert.equal(isAllowedExternalOAuthUrl('http://127.0.0.1:8080/v1/auth/oauth/start'), true);
    assert.equal(isAllowedExternalOAuthUrl('http://localhost:8080/v1/auth/oauth/start'), true);
    assert.equal(isAllowedExternalOAuthUrl('http://insecure.example/oauth'), false);
    assert.equal(isAllowedExternalOAuthUrl('javascript:alert(1)'), false);
    assert.equal(isAllowedExternalOAuthUrl('file:///etc/passwd'), false);
  });
});
