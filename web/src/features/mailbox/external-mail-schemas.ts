import {
  array,
  boolean,
  enum as zodEnum,
  literal,
  looseObject,
  nonnegative,
  nullable,
  number,
  optional,
  regex,
  string,
  strictObject,
  type infer as Infer,
} from 'zod/mini';

import { safeIdSchema, timestampSchema } from '@/features/mailbox/schemas';

/**
 * External mailboxes (`contracts/MAILBOXES.md`, External Mailboxes): a member binds an outside
 * account read-only through a connector, and its mail is copied into an `external` mailbox only
 * they read.
 */

/** How far back a new binding first reads; the contract allows 1 to 90 and defaults to 14. */
export const EXTERNAL_MAIL_BACKFILL_CHOICES = [14, 30, 90] as const;
export type ExternalMailBackfillDays = (typeof EXTERNAL_MAIL_BACKFILL_CHOICES)[number];
export const EXTERNAL_MAIL_DEFAULT_BACKFILL_DAYS: ExternalMailBackfillDays = 14;

/** Connector ids match this pattern; new connectors add ids without a contract change. */
export const EXTERNAL_MAIL_CONNECTOR_ID_PATTERN = /^[a-z][a-z0-9_]{1,15}$/u;

export const externalMailConnectorIdSchema = string().check(
  regex(EXTERNAL_MAIL_CONNECTOR_ID_PATTERN)
);

/**
 * One connector of the instance. `auth_kind` is read as any text so a later kind (an app password,
 * say) arrives intact and is offered as "coming soon" rather than failing the whole list.
 */
export const externalMailConnectorSchema = looseObject({
  id: externalMailConnectorIdSchema,
  auth_kind: string(),
  capabilities: looseObject({ read: boolean(), send: optional(boolean()) }),
});
export const externalMailConnectorListSchema = array(externalMailConnectorSchema);

export const EXTERNAL_MAIL_CONNECTION_STATUSES = ['active', 'needs_reconnect', 'revoked'] as const;
export const externalMailConnectionStatusSchema = zodEnum(EXTERNAL_MAIL_CONNECTION_STATUSES);

/**
 * One of the caller's bound accounts, or a shared connection they manage; it never carries a token.
 * `shared` is optional so an API from before shared connected mailboxes still parses.
 */
export const externalMailConnectionSchema = strictObject({
  id: safeIdSchema,
  provider: externalMailConnectorIdSchema,
  account_address: string(),
  mailbox_id: safeIdSchema,
  shared: optional(boolean()),
  /** The tenant the mailbox was assigned from; optional so an API from before tenants still parses. */
  tenant_id: optional(nullable(safeIdSchema)),
  status: externalMailConnectionStatusSchema,
  last_synced_at: nullable(timestampSchema),
  last_error_code: nullable(string()),
  created_at: timestampSchema,
});
export const externalMailConnectionListSchema = array(externalMailConnectionSchema);

/** The pending sign-in: the provider page to send the browser to, valid for 10 minutes. */
export const externalMailAuthorizationSchema = strictObject({
  authorization_url: string(),
  expires_at: timestampSchema,
});

export const externalMailAuthorizationEnvelopeSchema = strictObject({
  code: literal(0),
  message: string(),
  data: externalMailAuthorizationSchema,
});

/** Connectors that bind with an app password (`auth_kind: "password"`) and never redirect. */
export const EXTERNAL_MAIL_PASSWORD_AUTH_KIND = 'password';
/**
 * The API signs in to an IMAP server within 45 seconds before it answers, so the BFF waits longer
 * than that for this one route, and the browser a little longer than the BFF.
 */
export const EXTERNAL_MAIL_CREDENTIALS_UPSTREAM_TIMEOUT_MS = 60_000;
export const EXTERNAL_MAIL_CREDENTIALS_REQUEST_TIMEOUT_MS = 65_000;
/** Implicit-TLS IMAP; the contract accepts this port only. */
export const EXTERNAL_MAIL_IMAP_PORT = 993;
/**
 * Well-known IMAP and SMTP servers, offered as one-click presets in the app password form. The
 * labels are brand names and stay untranslated.
 */
export const EXTERNAL_MAIL_SERVER_PRESETS = [
  { id: 'qq', label: 'QQ', imap: 'imap.qq.com', smtp: 'smtp.qq.com', smtpPort: 465 },
  { id: 'netease163', label: '163', imap: 'imap.163.com', smtp: 'smtp.163.com', smtpPort: 465 },
  { id: 'netease126', label: '126', imap: 'imap.126.com', smtp: 'smtp.126.com', smtpPort: 465 },
  {
    id: 'exmail',
    label: 'Tencent Exmail',
    imap: 'imap.exmail.qq.com',
    smtp: 'smtp.exmail.qq.com',
    smtpPort: 465,
  },
  {
    id: 'aliyun',
    label: 'Aliyun',
    imap: 'imap.aliyun.com',
    smtp: 'smtp.aliyun.com',
    smtpPort: 465,
  },
  { id: 'gmail', label: 'Gmail', imap: 'imap.gmail.com', smtp: 'smtp.gmail.com', smtpPort: 465 },
  {
    id: 'icloud',
    label: 'iCloud',
    imap: 'imap.mail.me.com',
    smtp: 'smtp.mail.me.com',
    smtpPort: 587,
  },
  {
    id: 'yahoo',
    label: 'Yahoo',
    imap: 'imap.mail.yahoo.com',
    smtp: 'smtp.mail.yahoo.com',
    smtpPort: 465,
  },
  {
    id: 'fastmail',
    label: 'Fastmail',
    imap: 'imap.fastmail.com',
    smtp: 'smtp.fastmail.com',
    smtpPort: 465,
  },
] as const satisfies readonly {
  id: string;
  label: string;
  imap: string;
  smtp: string;
  smtpPort: 465 | 587;
}[];

/** SMTP submission: implicit TLS on 465 (the default) or STARTTLS on 587. */
export const EXTERNAL_MAIL_SMTP_PORTS = [465, 587] as const;
export type ExternalMailSmtpPort = (typeof EXTERNAL_MAIL_SMTP_PORTS)[number];
export const EXTERNAL_MAIL_DEFAULT_SMTP_PORT: ExternalMailSmtpPort = 465;
/** Bounds the form checks before sending; the server stays the authority. */
export const EXTERNAL_MAIL_HOST_MAX_LENGTH = 253;
export const EXTERNAL_MAIL_USERNAME_MAX_LENGTH = 320;
export const EXTERNAL_MAIL_PASSWORD_MAX_LENGTH = 1_024;

export const EXTERNAL_MAIL_SYNC_OUTCOMES = [
  'ok',
  'auth_failed',
  'fetch_failed',
  'cursor_expired',
  'provider_unavailable',
  'dropped',
] as const;

const countSchema = number().check(nonnegative());

/** One fetch of a connection; it never carries message data. */
export const externalMailSyncRunSchema = strictObject({
  id: safeIdSchema,
  started_at: timestampSchema,
  finished_at: timestampSchema,
  outcome: zodEnum(EXTERNAL_MAIL_SYNC_OUTCOMES),
  fetched_count: countSchema,
  stored_count: countSchema,
  more: boolean(),
  error_code: nullable(string()),
  timed_out: boolean(),
  throttled: boolean(),
});
export const externalMailSyncRunListSchema = array(externalMailSyncRunSchema);

/** What one purge request deleted; `completed` once the mailbox and connection are gone. */
export const externalMailPurgeSchema = strictObject({
  deleted_messages: countSchema,
  completed: boolean(),
});

/**
 * One row of the owners' and admins' connection directory: never mail. A member's own account
 * shows only its masked address; a shared connection is the organization's team account and also
 * shows its full address and mailbox. The shared fields are optional so an API from before shared
 * connected mailboxes still parses.
 */
export const externalMailDirectoryEntrySchema = strictObject({
  id: safeIdSchema,
  provider: externalMailConnectorIdSchema,
  status: externalMailConnectionStatusSchema,
  shared: optional(boolean()),
  tenant_id: optional(nullable(safeIdSchema)),
  account_address: optional(nullable(string())),
  mailbox_id: optional(nullable(safeIdSchema)),
  masked_account_address: string(),
  owner_user_id: nullable(safeIdSchema),
  owner_display_name: nullable(string()),
  last_synced_at: nullable(timestampSchema),
  created_at: timestampSchema,
});

export const externalMailDirectoryPageEnvelopeSchema = strictObject({
  code: literal(0),
  message: string(),
  data: array(externalMailDirectoryEntrySchema),
  meta: strictObject({
    per_page: number(),
    has_more: boolean(),
    next_cursor: nullable(string()),
  }),
});

export const EXTERNAL_MAIL_DIRECTORY_PAGE_SIZE = 15;
export const EXTERNAL_MAIL_DIRECTORY_MAX_PAGE_SIZE = 100;
export const EXTERNAL_MAIL_DIRECTORY_CURSOR_MAX_LENGTH = 512;

export type ExternalMailConnector = Infer<typeof externalMailConnectorSchema>;
export type ExternalMailConnection = Infer<typeof externalMailConnectionSchema>;
export type ExternalMailConnectionStatus = Infer<typeof externalMailConnectionStatusSchema>;
export type ExternalMailAuthorization = Infer<typeof externalMailAuthorizationSchema>;
export type ExternalMailSyncRun = Infer<typeof externalMailSyncRunSchema>;
export type ExternalMailSyncOutcome = ExternalMailSyncRun['outcome'];
export type ExternalMailPurge = Infer<typeof externalMailPurgeSchema>;
export type ExternalMailDirectoryEntry = Infer<typeof externalMailDirectoryEntrySchema>;
export interface ExternalMailDirectoryPage {
  items: ExternalMailDirectoryEntry[];
  meta: Infer<typeof externalMailDirectoryPageEnvelopeSchema>['meta'];
}

/**
 * Binds an account with an app password. The password lives only in this request: it is never
 * logged, cached, or kept by the browser.
 */
export interface BindExternalMailCredentialsInput {
  provider: string;
  /** Restores that connection (the caller's own, or a shared one they manage). */
  connection_id?: number;
  host: string;
  port: number;
  username: string;
  password: string;
  /** The outgoing server verified with the same login; the connected mailbox then sends through it. */
  smtp_host?: string;
  smtp_port?: ExternalMailSmtpPort;
  backfill_days?: number;
}

/** Start a new binding with a connector, or reconnect one of the caller's connections. */
export type StartExternalMailAuthorizationInput =
  { provider: string; backfill_days?: number } | { connection_id: number };

export interface CompleteExternalMailAuthorizationInput {
  state: string;
  code: string;
}

/** `created` is a new mailbox (`201`); `restored` is the caller's own account again (`200`). */
export interface ExternalMailCompletion {
  outcome: 'created' | 'restored';
  connection: ExternalMailConnection;
}
