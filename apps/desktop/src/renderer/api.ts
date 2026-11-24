import type { CachedMailMessage, MailPlacement } from '../shared/ipc-channels.ts';

export interface DesktopAccountConfig {
  id: string;
  email: string;
  displayName: string;
  workspace: 'personal' | 'work';
  providerId?: string;
  apiBaseUrl: string;
  unreadCount: number;
}

export const DEFAULT_API_BASE = 'http://127.0.0.1:8080';

export const INITIAL_ACCOUNTS: DesktopAccountConfig[] = [
  {
    id: 'acct-work-primary',
    email: 'alex@zmail.dev',
    displayName: 'Alex (Work)',
    workspace: 'work',
    apiBaseUrl: DEFAULT_API_BASE,
    unreadCount: 2,
  },
  {
    id: 'acct-personal-main',
    email: 'alex.personal@zmail.dev',
    displayName: 'Alex (Personal)',
    workspace: 'personal',
    apiBaseUrl: DEFAULT_API_BASE,
    unreadCount: 1,
  },
];

export const SEED_MESSAGES: CachedMailMessage[] = [
  {
    id: 1001,
    accountId: 'acct-work-primary',
    mailboxId: 1,
    threadId: 501,
    from: 'release-bot@zmail.dev',
    to: ['alex@zmail.dev'],
    cc: [],
    subject: 'Zmail Desktop v0.1 signed build ready',
    snippet: 'Direct Go /v1 sync, OS Keychain storage, and offline search are ready for QA.',
    bodyText:
      'Direct Go /v1 sync, OS Keychain storage, and offline full-text search are ready for desktop QA across macOS, Windows, and Linux.',
    read: false,
    starred: true,
    placement: 'default',
    attachmentCount: 1,
    createdAt: '2026-11-24T19:15:00Z',
  },
  {
    id: 1002,
    accountId: 'acct-work-primary',
    mailboxId: 1,
    threadId: 502,
    from: 'design@zmail.dev',
    to: ['alex@zmail.dev'],
    cc: ['product@zmail.dev'],
    subject: 'Personal / Work 3-pane MailMaster layout tokens',
    snippet: 'Updated spacing and keyboard focus indicators for 30-account sidebar.',
    bodyText:
      'We updated the 3-pane layout tokens and keyboard focus indicators so switching across up to 30 Personal and Work accounts stays crisp.',
    read: false,
    starred: false,
    placement: 'default',
    attachmentCount: 0,
    createdAt: '2026-11-24T17:40:00Z',
  },
  {
    id: 2001,
    accountId: 'acct-personal-main',
    mailboxId: 2,
    threadId: 601,
    from: 'receipts@cloud.example',
    to: ['alex.personal@zmail.dev'],
    cc: [],
    subject: 'Monthly backup receipt - November',
    snippet: 'Your encrypted archive completed without warnings.',
    bodyText: 'Your encrypted monthly backup completed without warnings. Total size: 4.2 GB.',
    read: false,
    starred: false,
    placement: 'default',
    attachmentCount: 1,
    createdAt: '2026-11-24T15:05:00Z',
  },
];

interface RawInboxRecord {
  id: number;
  mailbox_id?: number;
  thread_id?: number;
  from: string;
  to?: string[];
  cc?: string[];
  subject: string;
  snippet?: string;
  body_text?: string;
  read?: boolean;
  starred?: boolean;
  placement?: MailPlacement;
  attachment_count?: number;
  created_at: string;
}

interface DirectV1SyncResult {
  messages: CachedMailMessage[];
  nextCursor: string | null;
}

function normalizeBaseUrl(baseUrl: string): string {
  return baseUrl.replace(/\/+$/, '');
}

function toCachedMessage(accountId: string, raw: RawInboxRecord): CachedMailMessage {
  return {
    id: raw.id,
    accountId,
    mailboxId: raw.mailbox_id ?? 1,
    threadId: raw.thread_id ?? raw.id,
    from: raw.from,
    to: Array.isArray(raw.to) ? raw.to : [],
    cc: Array.isArray(raw.cc) ? raw.cc : [],
    subject: raw.subject || '(No Subject)',
    snippet: raw.snippet ?? (raw.body_text ?? '').slice(0, 140),
    bodyText: raw.body_text ?? raw.snippet ?? '',
    read: Boolean(raw.read),
    starred: Boolean(raw.starred),
    placement: raw.placement ?? 'default',
    attachmentCount: raw.attachment_count ?? 0,
    createdAt: raw.created_at,
  };
}

/**
 * Direct Go `/v1` API client using OS-keychain Bearer Token (bypasses Next.js BFF).
 */
export async function syncInboxFromV1(params: {
  accountId: string;
  apiBaseUrl: string;
  bearerToken: string;
  cursor: string | null;
}): Promise<DirectV1SyncResult> {
  const base = normalizeBaseUrl(params.apiBaseUrl);
  const url = new URL(`${base}/v1/inbox`);
  url.searchParams.set('per_page', '50');
  if (params.cursor) {
    url.searchParams.set('cursor', params.cursor);
  }

  const response = await fetch(url.toString(), {
    method: 'GET',
    headers: {
      Authorization: `Bearer ${params.bearerToken}`,
      Accept: 'application/json',
    },
  });

  if (!response.ok) {
    throw new Error(`Zmail /v1 sync failed with HTTP ${response.status}`);
  }

  const payload = (await response.json()) as {
    data?: RawInboxRecord[] | { items?: RawInboxRecord[]; next_cursor?: string | null };
    meta?: { next_cursor?: string | null };
  };

  const rows = Array.isArray(payload.data)
    ? payload.data
    : Array.isArray(payload.data?.items)
      ? payload.data.items
      : [];

  const nextCursor =
    payload.meta?.next_cursor ??
    (!Array.isArray(payload.data) ? (payload.data?.next_cursor ?? null) : null);

  return {
    messages: rows.map((row) => toCachedMessage(params.accountId, row)),
    nextCursor,
  };
}

export async function patchMessageOnV1(params: {
  apiBaseUrl: string;
  bearerToken: string;
  messageId: number;
  patch: { read?: boolean; starred?: boolean; placement?: MailPlacement };
}): Promise<void> {
  const base = normalizeBaseUrl(params.apiBaseUrl);
  const response = await fetch(`${base}/v1/inbox/messages/${params.messageId}`, {
    method: 'PATCH',
    headers: {
      Authorization: `Bearer ${params.bearerToken}`,
      'Content-Type': 'application/json',
      Accept: 'application/json',
    },
    body: JSON.stringify(params.patch),
  });
  if (!response.ok) {
    throw new Error(`Zmail /v1 message update failed with HTTP ${response.status}`);
  }
}

export async function sendMessageOnV1(params: {
  apiBaseUrl: string;
  bearerToken: string;
  to: string[];
  subject: string;
  bodyText: string;
}): Promise<void> {
  const base = normalizeBaseUrl(params.apiBaseUrl);
  const response = await fetch(`${base}/v1/inbox/messages`, {
    method: 'POST',
    headers: {
      Authorization: `Bearer ${params.bearerToken}`,
      'Content-Type': 'application/json',
      Accept: 'application/json',
    },
    body: JSON.stringify({
      to: params.to,
      subject: params.subject,
      body_text: params.bodyText,
    }),
  });
  if (!response.ok) {
    throw new Error(`Zmail /v1 send failed with HTTP ${response.status}`);
  }
}
