import {
  _default,
  array,
  boolean,
  literal,
  nullable,
  optional,
  record,
  strictObject,
  string,
  union,
  unknown,
} from 'zod/mini';

import {
  mailLabelColorSchema,
  nonnegativeIntegerSchema,
  pageLinksSchema,
  pageMetaSchema,
  positiveIntegerSchema,
  safeIdSchema,
  timestampSchema,
} from './shared-schemas';

/** Limits from `contracts/MAILBOXES.md`. */
export const MAILBOX_LOCAL_PART_MAX_LENGTH = 64;
export const MAILBOX_DISPLAY_NAME_MAX_LENGTH = 120;
export const MAILBOX_MAX_PER_ORGANIZATION = 50;
/** Members of one shared mailbox. */
export const MAILBOX_MAX_MEMBERS = 100;
export const MAILBOX_ACCESS_EVENTS_MAX_PAGE_SIZE = 100;
/** The administration directory, import, and export of `contracts/MAILBOXES.md`. */
export const MAILBOX_DIRECTORY_QUERY_MAX_LENGTH = 120;
export const MAILBOX_DIRECTORY_DEFAULT_PAGE_SIZE = 15;
export const MAILBOX_DIRECTORY_MAX_PAGE_SIZE = 100;
export const MAILBOX_DIRECTORY_CURSOR_MAX_LENGTH = 128;
export const MAILBOX_IMPORT_MAX_ROWS = 500;
export const MAILBOX_IMPORT_ADDRESS_MAX_LENGTH = 320;
export const MAILBOX_IMPORT_DISPLAY_NAME_MAX_LENGTH = 480;
export const MAILBOX_IMPORT_MEMBERS_MAX_LENGTH = 8192;
/** Roster import (`POST /v1/member-imports`) bounds, as the contract states them. */
export const MEMBER_IMPORT_MAX_ROWS = 500;
export const MEMBER_IMPORT_EMAIL_MAX_LENGTH = 320;
export const MEMBER_IMPORT_NAME_MAX_LENGTH = 480;
export const MEMBER_IMPORT_ROLE_MAX_LENGTH = 16;
export const MEMBER_IMPORT_MAILBOX_MAX_LENGTH = 320;
export const MEMBER_IMPORT_DOMAIN_MAX_LENGTH = 253;
/** 5000 rows of 100 members each fit well within it. */
export const MAILBOX_EXPORT_MAX_BYTES = 24 * 1024 * 1024;
export const MAILBOX_LIST_SCOPES = ['usable'] as const;

/** The most mail roles an organization holds. */
export const MAIL_ROLES_MAX_PER_ORGANIZATION = 50;
/** A role name is 1 to 64 characters after trimming, unique in the organization ignoring case. */
export const MAIL_ROLE_NAME_MAX_LENGTH = 64;
/** A role description is at most 280 characters after trimming, without control characters. */
export const MAIL_ROLE_DESCRIPTION_MAX_LENGTH = 280;

const localPartPattern = /^[a-z0-9](?:[a-z0-9._-]{0,62}[a-z0-9])?$/u;
const reservedLocalParts = new Set([
  'abuse',
  'admin',
  'administrator',
  'hostmaster',
  'mailer-daemon',
  'no-reply',
  'noreply',
  'postmaster',
  'root',
  'security',
  'support',
  'webmaster',
]);

/** The types a mailbox can be created, filtered, or exported as. */
export const mailboxTypeSchema = union([literal('personal'), literal('shared')]);
/** Every type the API reports: who may use the mailbox, independent of its origin. */
export const mailboxKindSchema = mailboxTypeSchema;
/** Where a mailbox's mail comes from; `connected` holds read-only copies of a bound outside account. */
export const mailboxOriginSchema = union([literal('zmail'), literal('connected')]);

/** Each level includes the previous one. */
export const mailboxPermissionSchema = union([literal('view'), literal('send'), literal('manage')]);
export const MAILBOX_PERMISSIONS = ['view', 'send', 'manage'] as const;

/** What the caller may do with a mailbox; controls are shown or hidden from it. */
export const mailboxAccessSchema = strictObject({
  view: boolean(),
  send: boolean(),
  manage_members: boolean(),
  administer: boolean(),
  see_history: boolean(),
  // Delete mail for good, empty the trash, and arrange folders and labels.
  organize: boolean(),
});

export const mailboxSchema = strictObject({
  id: safeIdSchema,
  address: string(),
  display_name: string(),
  type: mailboxKindSchema,
  // `connected` mailboxes are read-only copies of an outside account. Optional until every API emits it.
  origin: optional(mailboxOriginSchema),
  // Null for a shared mailbox or after the owner account is deleted; kept while a personal mailbox awaits handover.
  owner_user_id: nullable(safeIdSchema),
  awaiting_handover: boolean(),
  // Mail created before this instant is sealed; set once at handover.
  sealed_before: nullable(timestampSchema),
  webhooks_enabled: boolean(),
  // The mailbox's AI triage switch. Optional until every API emits it.
  ai_triage_enabled: optional(boolean()),
  access: mailboxAccessSchema,
  // The caller's own unread count; 0 without `view`.
  unread_count: nonnegativeIntegerSchema,
  created_at: timestampSchema,
  // The mail role the mailbox belongs to; null when ungrouped. Optional until every API emits it.
  role_id: optional(nullable(safeIdSchema)),
});
export const mailboxListSchema = array(mailboxSchema);

/**
 * An organization-level grouping of mailboxes for navigation; it grants nothing. `mailbox_ids` are
 * the role's mailboxes the caller can see, ascending; `owner_user_id` is for display only.
 */
export const mailRoleSchema = strictObject({
  id: safeIdSchema,
  name: string(),
  color: mailLabelColorSchema,
  description: string(),
  owner_user_id: nullable(safeIdSchema),
  mailbox_ids: array(safeIdSchema),
  created_at: timestampSchema,
  updated_at: timestampSchema,
});
export const mailRoleListSchema = array(mailRoleSchema);

/** Every field of a role; an omitted one takes its default (gray, empty, no owner). */
export const mailRoleInputSchema = strictObject({
  name: string(),
  color: optional(mailLabelColorSchema),
  description: optional(string()),
  owner_user_id: optional(nullable(safeIdSchema)),
});

/** Request bodies. Field rules beyond the types are checked by the normalizers below. */
export const mailboxMemberInputSchema = strictObject({
  user_id: safeIdSchema,
  permission: mailboxPermissionSchema,
});

export const createMailboxSchema = strictObject({
  local_part: string(),
  display_name: optional(string()),
  // A verified domain of the organization; absent means the shared domain.
  domain: optional(string()),
  type: mailboxTypeSchema,
  // Personal mailboxes only.
  owner_user_id: optional(safeIdSchema),
  // Shared mailboxes only: 1 to 100 different users.
  members: optional(array(mailboxMemberInputSchema)),
});

export const mailboxMemberSchema = strictObject({
  user_id: safeIdSchema,
  permission: mailboxPermissionSchema,
  granted_by: safeIdSchema,
  created_at: timestampSchema,
});
export const mailboxMemberListSchema = array(mailboxMemberSchema);

export const mailboxAccessEventTypeSchema = union([
  literal('created'),
  literal('member_set'),
  literal('member_removed'),
  literal('owner_left'),
  literal('owner_returned'),
  literal('handed_over'),
  literal('sealed_deleted'),
  literal('webhooks_changed'),
  literal('renamed'),
  literal('connection_bound'),
  literal('connection_reconnected'),
  literal('connection_revoked'),
  literal('connection_force_disconnected'),
  literal('connection_revoked_on_leave'),
  literal('agent_granted'),
  literal('agent_revoked'),
  literal('owner_assigned'),
]);

export const mailboxAccessEventSchema = strictObject({
  id: safeIdSchema,
  type: mailboxAccessEventTypeSchema,
  // Null for a change Zmail made itself.
  actor_user_id: nullable(safeIdSchema),
  subject_user_id: nullable(safeIdSchema),
  // Ids, permissions, timestamps, and counts; clients ignore keys they do not know.
  detail: record(string(), unknown()),
  created_at: timestampSchema,
});

export const mailboxAccessEventPageEnvelopeSchema = strictObject({
  code: literal(0),
  message: string(),
  data: array(mailboxAccessEventSchema),
  meta: pageMetaSchema,
  links: pageLinksSchema,
});

export const mailboxSealedDeletionSchema = strictObject({
  deleted_messages: nonnegativeIntegerSchema,
  deleted_broadcasts: nonnegativeIntegerSchema,
});

export type NormalizedLocalPart =
  | { ok: true; value: string }
  | { ok: false; reason: 'invalid' | 'reserved' };

/**
 * Trims and lowercases a local part, then applies the contract's address rules. Role addresses
 * are reserved only on the shared domain; an organization operates its own domain.
 */
export function normalizeLocalPart(
  raw: string,
  { sharedDomain = true }: { sharedDomain?: boolean } = {}
): NormalizedLocalPart {
  const value = raw.trim().toLowerCase();
  if (!localPartPattern.test(value) || value.includes('..')) {
    return { ok: false, reason: 'invalid' };
  }
  if (sharedDomain && reservedLocalParts.has(value)) return { ok: false, reason: 'reserved' };
  return { ok: true, value };
}

/** Collapses whitespace; null when the name is too long or holds `<`, `>`, or `"`. */
export function normalizeDisplayName(raw: string): string | null {
  const value = raw.trim().split(/\s+/u).filter(Boolean).join(' ');
  if (Array.from(value).length > MAILBOX_DISPLAY_NAME_MAX_LENGTH || /[<>"]/u.test(value)) {
    return null;
  }
  return value;
}

/** One mailbox of the administration directory, with its member count and personal-lane sends. */
export const mailboxDirectoryEntrySchema = strictObject({
  id: safeIdSchema,
  address: string(),
  display_name: string(),
  type: mailboxKindSchema,
  owner_user_id: nullable(safeIdSchema),
  awaiting_handover: boolean(),
  created_at: timestampSchema,
  member_count: nonnegativeIntegerSchema,
  sent_last_24h: nonnegativeIntegerSchema,
});

export const mailboxDirectoryMetaSchema = strictObject({
  per_page: positiveIntegerSchema,
  has_more: boolean(),
  next_cursor: nullable(string()),
  daily_send_limit: positiveIntegerSchema,
  sent_last_24h: nonnegativeIntegerSchema,
  mailbox_count: nonnegativeIntegerSchema,
  max_mailboxes: positiveIntegerSchema,
});

export const mailboxDirectoryPageEnvelopeSchema = strictObject({
  code: literal(0),
  message: string(),
  data: array(mailboxDirectoryEntrySchema),
  meta: mailboxDirectoryMetaSchema,
});

export const mailboxImportRowStatusSchema = union([
  literal('valid'),
  literal('created'),
  literal('failed'),
]);

export const mailboxImportRowResultSchema = strictObject({
  row: positiveIntegerSchema,
  address: string(),
  status: mailboxImportRowStatusSchema,
  mailbox_id: nullable(safeIdSchema),
  error_code: nullable(string()),
  missing_emails: array(string()),
});

export const mailboxImportResultSchema = strictObject({
  dry_run: boolean(),
  succeeded: nonnegativeIntegerSchema,
  failed: nonnegativeIntegerSchema,
  rows: array(mailboxImportRowResultSchema),
});

export const memberImportRowStatusSchema = union([
  literal('valid'),
  literal('invited'),
  literal('created'),
  literal('skipped'),
  literal('failed'),
]);

export const memberImportSkipReasonSchema = union([
  literal('already_member'),
  literal('already_invited'),
  literal('mailbox_taken'),
]);

export const memberImportRowResultSchema = strictObject({
  row: positiveIntegerSchema,
  email: string(),
  address: string(),
  status: memberImportRowStatusSchema,
  skip_reason: nullable(memberImportSkipReasonSchema),
  error_code: nullable(string()),
  mailbox_id: nullable(safeIdSchema),
  invitation_id: nullable(safeIdSchema),
  email_send_status: nullable(
    union([literal('accepted_by_provider'), literal('failed'), literal('not_configured')])
  ),
  // An API from before the welcome mail does not report these; read them as "no welcome".
  welcome_mail_status: _default(
    nullable(
      union([
        literal('queued'),
        literal('suppressed'),
        literal('already_sent'),
        literal('disabled'),
        literal('not_configured'),
        literal('failed'),
      ])
    ),
    null
  ),
  welcome_mail_error_code: _default(nullable(string()), null),
});

export const memberImportResultSchema = strictObject({
  dry_run: boolean(),
  total: nonnegativeIntegerSchema,
  valid: nonnegativeIntegerSchema,
  invited: nonnegativeIntegerSchema,
  created: nonnegativeIntegerSchema,
  skipped: nonnegativeIntegerSchema,
  failed: nonnegativeIntegerSchema,
  welcome_mails_queued: _default(nonnegativeIntegerSchema, 0),
  welcome_mails_failed: _default(nonnegativeIntegerSchema, 0),
  rows: array(memberImportRowResultSchema),
});
