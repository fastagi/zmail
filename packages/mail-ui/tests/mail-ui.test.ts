import assert from 'node:assert/strict';
import { readdirSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

import {
  detectProviderFromEmail,
  getProviderPreset,
  MAIL_UI_MAX_CONNECTED_ACCOUNTS,
  MAIL_UI_PROVIDER_PRESETS,
} from '../src/providers.ts';
import {
  buildReplyPayload,
  computeTotalUnread,
  computeUnreadByAccount,
  filterAccountsForMode,
  filterMessagesForMode,
  resolveReplySenderAccount,
} from '../src/selectors.ts';
import type { MailUiAccount, MailUiMessage } from '../src/types.ts';

function makeAccount(overrides: Partial<MailUiAccount> & { id: string; email: string }): MailUiAccount {
  return {
    id: overrides.id,
    email: overrides.email,
    displayName: overrides.displayName,
    providerId: overrides.providerId ?? 'gmail',
    kind: overrides.kind ?? 'external',
    mode: overrides.mode,
    unreadCount: overrides.unreadCount,
    syncStatus: overrides.syncStatus ?? 'synced',
    lastSyncedAt: overrides.lastSyncedAt ?? '2026-11-24T12:00:00Z',
    lastError: overrides.lastError ?? null,
    canSend: overrides.canSend ?? true,
  };
}

function makeMessage(
  overrides: Partial<MailUiMessage> & { id: string; accountId: string }
): MailUiMessage {
  return {
    id: overrides.id,
    threadId: overrides.threadId ?? `thr_${overrides.id}`,
    accountId: overrides.accountId,
    accountEmail: overrides.accountEmail,
    direction: overrides.direction ?? 'inbound',
    from: overrides.from ?? 'sender@example.com',
    fromName: overrides.fromName,
    to: overrides.to ?? ['user@gmail.com'],
    cc: overrides.cc,
    subject: overrides.subject ?? `Subject ${overrides.id}`,
    snippet: overrides.snippet ?? `Snippet for ${overrides.id}`,
    bodyText: overrides.bodyText ?? `Body for ${overrides.id}`,
    receivedAt: overrides.receivedAt ?? '2026-11-24T12:00:00Z',
    read: overrides.read ?? false,
    starred: overrides.starred ?? false,
    archived: overrides.archived ?? false,
  };
}

test('MAIL_UI_PROVIDER_PRESETS exposes all 13 unique presets', () => {
  assert.equal(MAIL_UI_PROVIDER_PRESETS.length, 13);
  assert.equal(MAIL_UI_MAX_CONNECTED_ACCOUNTS, 30);

  const ids = MAIL_UI_PROVIDER_PRESETS.map((preset) => preset.id);
  assert.equal(new Set(ids).size, 13);
  assert.deepEqual(ids, [
    'gmail',
    'outlook',
    'icloud',
    'qq',
    'netease163',
    'fastmail',
    'yahoo',
    'zoho',
    'exmail',
    'feishu',
    'aliyun',
    'sina',
    'custom',
  ]);
  assert.equal(getProviderPreset('unknown-provider').id, 'custom');
});

test('detectProviderFromEmail resolves all 12 known providers, domain overrides, and Custom IMAP fallback', () => {
  const cases: Array<{
    email: string;
    expectedId: string;
    expectedImapHost: string;
    expectedSmtpHost: string;
    matched: boolean;
  }> = [
    { email: 'Alice@Gmail.com', expectedId: 'gmail', expectedImapHost: 'imap.gmail.com', expectedSmtpHost: 'smtp.gmail.com', matched: true },
    { email: 'bob@outlook.com', expectedId: 'outlook', expectedImapHost: 'outlook.office365.com', expectedSmtpHost: 'smtp.office365.com', matched: true },
    { email: 'carol@icloud.com', expectedId: 'icloud', expectedImapHost: 'imap.mail.me.com', expectedSmtpHost: 'smtp.mail.me.com', matched: true },
    { email: 'dan@foxmail.com', expectedId: 'qq', expectedImapHost: 'imap.qq.com', expectedSmtpHost: 'smtp.qq.com', matched: true },
    { email: 'eve@163.com', expectedId: 'netease163', expectedImapHost: 'imap.163.com', expectedSmtpHost: 'smtp.163.com', matched: true },
    { email: 'eve@126.com', expectedId: 'netease163', expectedImapHost: 'imap.126.com', expectedSmtpHost: 'smtp.126.com', matched: true },
    { email: 'eve@yeah.net', expectedId: 'netease163', expectedImapHost: 'imap.yeah.net', expectedSmtpHost: 'smtp.yeah.net', matched: true },
    { email: 'frank@fastmail.fm', expectedId: 'fastmail', expectedImapHost: 'imap.fastmail.com', expectedSmtpHost: 'smtp.fastmail.com', matched: true },
    { email: 'grace@yahoo.com', expectedId: 'yahoo', expectedImapHost: 'imap.mail.yahoo.com', expectedSmtpHost: 'smtp.mail.yahoo.com', matched: true },
    { email: 'heidi@zohomail.com', expectedId: 'zoho', expectedImapHost: 'imap.zoho.com', expectedSmtpHost: 'smtp.zoho.com', matched: true },
    { email: 'ivan@exmail.qq.com', expectedId: 'exmail', expectedImapHost: 'imap.exmail.qq.com', expectedSmtpHost: 'smtp.exmail.qq.com', matched: true },
    { email: 'judy@larksuite.com', expectedId: 'feishu', expectedImapHost: 'imap.feishu.cn', expectedSmtpHost: 'smtp.feishu.cn', matched: true },
    { email: 'ken@aliyun.com', expectedId: 'aliyun', expectedImapHost: 'imap.aliyun.com', expectedSmtpHost: 'smtp.aliyun.com', matched: true },
    { email: 'li@sina.cn', expectedId: 'sina', expectedImapHost: 'imap.sina.com', expectedSmtpHost: 'smtp.sina.com', matched: true },
    { email: 'ops@acme.io', expectedId: 'custom', expectedImapHost: 'imap.acme.io', expectedSmtpHost: 'smtp.acme.io', matched: false },
  ];

  for (const tc of cases) {
    const detected = detectProviderFromEmail(tc.email);
    assert.ok(detected, `Expected detection for ${tc.email}`);
    assert.equal(detected.valid, true);
    assert.equal(detected.matched, tc.matched);
    assert.equal(detected.preset.id, tc.expectedId);
    assert.equal(detected.preset.imapHost, tc.expectedImapHost);
    assert.equal(detected.preset.smtpHost, tc.expectedSmtpHost);
  }

  assert.equal(detectProviderFromEmail('not-an-email'), null);
  assert.equal(detectProviderFromEmail('bad..user@gmail.com'), null);
  assert.equal(detectProviderFromEmail('   '), null);
});

test('filterAccountsForMode and filterMessagesForMode isolate work vs personal and support search & sorting', () => {
  const workAccount = makeAccount({ id: 'acc_work', email: 'ceo@zmail.app', kind: 'hosted', providerId: 'custom' });
  const personalGmail = makeAccount({ id: 'acc_gmail', email: 'me@gmail.com', kind: 'external', providerId: 'gmail' });
  const personalQQ = makeAccount({ id: 'acc_qq', email: 'me@qq.com', kind: 'external', providerId: 'qq' });
  const accounts = [workAccount, personalGmail, personalQQ];

  const messages = [
    makeMessage({ id: 'm1', accountId: 'acc_work', subject: 'Q4 Board Deck', receivedAt: '2026-11-24T09:00:00Z' }),
    makeMessage({ id: 'm2', accountId: 'acc_gmail', subject: 'Flight Receipt', receivedAt: '2026-11-24T10:00:00Z' }),
    makeMessage({ id: 'm3', accountId: 'acc_qq', subject: 'Flight Check-in', receivedAt: '2026-11-24T11:00:00Z' }),
    makeMessage({ id: 'm4', accountId: 'acc_gmail', subject: 'Archived Flight', archived: true, receivedAt: '2026-11-24T12:00:00Z' }),
  ];

  const personalAll = filterMessagesForMode(messages, accounts, 'personal');
  assert.deepEqual(personalAll.map((m) => m.id), ['m3', 'm2']);

  const workAll = filterMessagesForMode(messages, accounts, 'work');
  assert.deepEqual(workAll.map((m) => m.id), ['m1']);

  const gmailSearch = filterMessagesForMode(messages, accounts, 'personal', {
    selectedAccountId: 'acc_gmail',
    searchQuery: 'receipt',
  });
  assert.deepEqual(gmailSearch.map((m) => m.id), ['m2']);

  const withArchived = filterMessagesForMode(messages, accounts, 'personal', {
    includeArchived: true,
  });
  assert.deepEqual(withArchived.map((m) => m.id), ['m4', 'm3', 'm2']);

  // Verify 30-account cap
  const manyPersonal = Array.from({ length: 35 }, (_, idx) =>
    makeAccount({ id: `acc_${idx}`, email: `u${idx}@gmail.com`, kind: 'external' })
  );
  assert.equal(filterAccountsForMode(manyPersonal, 'personal').length, 30);
});

test('computeUnreadByAccount counts unread non-archived messages per account', () => {
  const accounts = [
    makeAccount({ id: 'a1', email: 'a1@gmail.com' }),
    makeAccount({ id: 'a2', email: 'a2@qq.com', providerId: 'qq' }),
    makeAccount({ id: 'a3', email: 'a3@163.com', providerId: 'netease163' }),
  ];
  const messages = [
    makeMessage({ id: '1', accountId: 'a1', read: false }),
    makeMessage({ id: '2', accountId: 'a1', read: false }),
    makeMessage({ id: '3', accountId: 'a1', read: true }),
    makeMessage({ id: '4', accountId: 'a2', read: false, archived: true }),
    makeMessage({ id: '5', accountId: 'a2', read: false }),
  ];

  const unread = computeUnreadByAccount(messages, accounts);
  assert.deepEqual(unread, { a1: 2, a2: 1, a3: 0 });
  assert.equal(computeTotalUnread(unread, accounts), 3);
});

test('resolveReplySenderAccount and buildReplyPayload always match the receiving account', () => {
  const gmail = makeAccount({ id: 'acc_gmail', email: 'me@gmail.com', providerId: 'gmail' });
  const qq = makeAccount({ id: 'acc_qq', email: 'me@qq.com', providerId: 'qq' });
  const accounts = [gmail, qq];

  const msgToQQ = makeMessage({
    id: 'm_qq',
    accountId: 'acc_qq',
    from: 'friend@example.com',
    to: ['me@qq.com', 'team@example.com'],
    subject: 'Dinner tonight?',
  });

  const sender = resolveReplySenderAccount(msgToQQ, accounts);
  assert.equal(sender?.id, 'acc_qq');
  assert.equal(sender?.email, 'me@qq.com');

  // Fallback by recipient address when accountId does not match
  const migratedMsg = makeMessage({
    id: 'm_migrated',
    accountId: 'stale_id',
    from: 'billing@vendor.com',
    to: ['Me <ME@GMAIL.COM>'],
    subject: 'Re: Invoice',
  });
  assert.equal(resolveReplySenderAccount(migratedMsg, accounts)?.id, 'acc_gmail');

  const replyAll = buildReplyPayload(msgToQQ, accounts, 'reply_all');
  assert.ok(replyAll);
  assert.equal(replyAll.fromAccountId, 'acc_qq');
  assert.equal(replyAll.fromEmail, 'me@qq.com');
  assert.deepEqual(replyAll.to, ['friend@example.com']);
  assert.deepEqual(replyAll.cc, ['team@example.com']);
  assert.equal(replyAll.subject, 'Re: Dinner tonight?');

  const forward = buildReplyPayload(migratedMsg, accounts, 'forward');
  assert.ok(forward);
  assert.equal(forward.fromAccountId, 'acc_gmail');
  assert.equal(forward.subject, 'Fwd: Re: Invoice');
});

test('packages/mail-ui has zero next/* or electron imports and every file stays under 400 lines', () => {
  const pkgRoot = fileURLToPath(new URL('..', import.meta.url));
  const filesToCheck: string[] = [];

  function walk(dir: string) {
    for (const entry of readdirSync(dir, { withFileTypes: true })) {
      const fullPath = join(dir, entry.name);
      if (entry.isDirectory()) {
        walk(fullPath);
      } else if (entry.name.endsWith('.ts') || entry.name.endsWith('.tsx')) {
        filesToCheck.push(fullPath);
      }
    }
  }

  walk(join(pkgRoot, 'src'));
  walk(join(pkgRoot, 'tests'));

  assert.ok(filesToCheck.length >= 6);
  for (const file of filesToCheck) {
    const content = readFileSync(file, 'utf8');
    const lineCount = content.split('\n').length;
    assert.ok(lineCount < 400, `${file} has ${lineCount} lines (must be < 400)`);
    if (file.includes('/src/')) {
      assert.doesNotMatch(
        content,
        /from\s+['"](?:next(?:\/[^'"]*)?|electron)['"]/u,
        `${file} must not import next/* or electron`
      );
    }
  }
});
