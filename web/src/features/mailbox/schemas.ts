import {
  array,
  boolean,
  int,
  iso,
  literal,
  maximum,
  nonnegative,
  nullable,
  number,
  optional,
  positive,
  record,
  strictObject,
  string,
  looseObject,
  union,
  unknown,
} from 'zod/mini';

/** Limits from `contracts/MAILBOXES.md`. */
export const MAILBOX_LOCAL_PART_MAX_LENGTH = 64;
export const MAILBOX_DISPLAY_NAME_MAX_LENGTH = 120;
export const MAILBOX_MAX_PER_ORGANIZATION = 50;
/** Members of one shared mailbox. */
export const MAILBOX_MAX_MEMBERS = 100;
export const MAILBOX_ACCESS_EVENTS_MAX_PAGE_SIZE = 100;
export const MAIL_DOMAIN_NAME_MAX_LENGTH = 253;
export const MAIL_DOMAIN_MAX_PER_ORGANIZATION = 10;
export const MAIL_ZONE_MAX_PER_ORGANIZATION = 3;
export const MAIL_DNS_RECORD_NAME_MAX_LENGTH = 253;
export const MAIL_DNS_RECORD_CONTENT_MAX_LENGTH = 4_096;
export const MAIL_DNS_TXT_CONTENT_MAX_LENGTH = 2_048;
/** Cloudflare's DNS record identifier, as the record routes accept it. */
export const MAIL_DNS_RECORD_ID_PATTERN = /^[A-Za-z0-9]{1,64}$/u;
export const MAIL_MAX_RECIPIENTS = 50;
export const MAIL_ADDRESS_MAX_LENGTH = 320;
export const MAIL_SUBJECT_MAX_LENGTH = 998;
export const MAIL_BODY_MAX_BYTES = 256 * 1_024;
export const MAIL_MESSAGE_ID_MAX_LENGTH = 900;
export const MAIL_PAGE_MAX_SIZE = 100;
export const MAIL_LOG_QUERY_MAX_LENGTH = 200;
/** A mailbox search is at most this many characters as sent, then trimmed and not empty. */
export const MAIL_SEARCH_MAX_LENGTH = 200;
export const MAIL_FOLDER_NAME_MAX_LENGTH = 64;
export const MAIL_LABEL_NAME_MAX_LENGTH = 64;
export const MAIL_FOLDERS_MAX_PER_MAILBOX = 100;
export const MAIL_LABELS_MAX_PER_MAILBOX = 100;
/** The most messages one `message-updates` call or one `label_ids` set names. */
export const MAIL_MESSAGE_UPDATES_MAX_IDS = 100;
/** A thread lists at most its latest messages; a thread row names at most this many ids. */
export const MAIL_THREAD_MAX_MESSAGES = 100;
export const MAIL_TRASH_RETENTION_DAYS = 30;
export const MAIL_STATS_DEFAULT_DAYS = 7;
export const MAIL_STATS_MAX_DAYS = 90;
export const MAIL_MAX_ATTACHMENTS = 10;
/** Decoded size of all attachments of one message. */
export const MAIL_ATTACHMENTS_MAX_BYTES = 4 * 1_024 * 1_024;
export const MAIL_ATTACHMENT_NAME_MAX_LENGTH = 255;
export const MAIL_ATTACHMENT_TYPE_MAX_LENGTH = 127;
export const MAIL_TEMPLATE_NAME_MAX_LENGTH = 100;
export const MAIL_TEMPLATE_MAX_PER_ORGANIZATION = 200;
export const MAIL_TEMPLATE_MAX_VARIABLES = 100;
/** The visual editor's design document, as stored by the API. */
export const MAIL_TEMPLATE_DESIGN_MAX_BYTES = 256 * 1_024;
export const MAIL_TEMPLATE_VALUE_MAX_LENGTH = 2_000;
export const MAIL_TEMPLATE_TEST_MAX_RECIPIENTS = 5;
export const MAIL_CONTACT_NAME_MAX_LENGTH = 100;
export const MAIL_CONTACT_MAX_PROPERTIES = 50;
export const MAIL_CONTACT_PROPERTY_MAX_LENGTH = 500;
export const MAIL_CONTACT_LIST_NAME_MAX_LENGTH = 100;
export const MAIL_CONTACT_MAX_LISTS = 100;
export const MAIL_CONTACT_IMPORT_MAX_BYTES = 5 * 1_024 * 1_024;
export const MAIL_CONTACT_IMPORT_MAX_ROWS = 10_000;
/** Contact fields with a fixed meaning; they cannot be custom properties. */
export const MAIL_BROADCAST_NAME_MAX_LENGTH = 100;
export const MAIL_BROADCAST_TEST_MAX_PERCENT = 50;
export const MAIL_BROADCAST_TEST_WAIT_DEFAULT_MINUTES = 30;
export const MAIL_BROADCAST_TEST_WAIT_MIN_MINUTES = 5;
export const MAIL_BROADCAST_TEST_WAIT_MAX_MINUTES = 1_440;
export const MAIL_BROADCAST_LABEL_MAX_LENGTH = 50;
/** An unsubscribe token: organization id, contact id, and a 16-byte base64url signature. */
export const MAIL_UNSUBSCRIBE_TOKEN_PATTERN = /^[1-9]\d{0,9}\.[1-9]\d{0,9}\.[A-Za-z0-9_-]{22}$/u;
export const MAIL_CONTACT_RESERVED_FIELDS = ['email', 'first_name', 'last_name'] as const;
/** Filled from each contact on every broadcast message; a broadcast cannot set them. */
export const MAIL_BROADCAST_CONTACT_VARIABLES = [
  ...MAIL_CONTACT_RESERVED_FIELDS,
  'unsubscribe_url',
] as const;
/** A variable name: a letter, then letters, digits, or underscores. */
export const MAIL_TEMPLATE_VARIABLE_PATTERN = /^[A-Za-z][A-Za-z0-9_]{0,63}$/u;

const localPartPattern = /^[a-z0-9](?:[a-z0-9._-]{0,62}[a-z0-9])?$/u;
const recipientPattern = /^[^\s@<>"(),:;\\[\]]+@[^\s@<>"(),:;\\[\]]+\.[^\s@<>"(),:;\\[\]]+$/u;
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

const safeIdSchema = number().check(int(), positive(), maximum(Number.MAX_SAFE_INTEGER));
const timestampSchema = iso.datetime({ offset: true });
const positiveIntegerSchema = number().check(int(), positive());
const nonnegativeIntegerSchema = number().check(int(), nonnegative());

export const mailDirectionSchema = union([literal('inbound'), literal('outbound')]);
export const mailStatusSchema = union([
  literal('received'),
  literal('queued'),
  literal('sent'),
  literal('failed'),
]);

/** What happened to an outbound message after the provider took it; one event per recipient. */
export const mailEventTypeSchema = union([
  literal('accepted'),
  literal('delivered'),
  literal('deferred'),
  literal('bounced'),
  literal('failed'),
  literal('rejected'),
  literal('complained'),
]);
/** The most severe current state among the recipients; '' when no event is recorded. */
export const mailEventSummarySchema = union([literal(''), mailEventTypeSchema]);

export const mailboxDomainSchema = strictObject({
  domain: string(),
  // `shared` is the instance domain; `organization` a verified domain the organization connected.
  kind: union([literal('shared'), literal('organization')]),
  max_mailboxes: number().check(int()),
  // `cloudflare` delivers; `log` only records and is for development.
  provider: union([literal('cloudflare'), literal('log')]),
  inbound_enabled: boolean(),
  daily_send_limit: positiveIntegerSchema,
  // Broadcasts need the inbound secret and MAILBOX_PUBLIC_URL for their unsubscribe links.
  broadcasts_enabled: boolean(),
  // Instance-wide: whether owners and admins may connect their own domains.
  custom_domains_enabled: boolean(),
});
export const mailboxDomainListSchema = array(mailboxDomainSchema);

export const mailDomainRecordSchema = strictObject({
  purpose: union([literal('receiving'), literal('sending'), literal('dmarc')]),
  type: string(),
  name: string(),
  content: string(),
  priority: optional(nonnegativeIntegerSchema),
  status: union([literal('ok'), literal('missing'), literal('conflict')]),
  found: optional(string()),
});

/** `send_only` leaves the domain's MX with another mail service. */
export const mailDomainCapabilitySchema = union([literal('send_receive'), literal('send_only')]);

/** A Cloudflare zone Zmail created for an organization's apex. */
export const mailZoneSchema = strictObject({
  id: safeIdSchema,
  name: string(),
  // `pending` until the registrar delegates the apex to `name_servers`.
  status: union([literal('pending'), literal('active')]),
  name_servers: array(string()),
  original_name_servers: array(string()),
  activated_at: nullable(timestampSchema),
  last_checked_at: nullable(timestampSchema),
  created_at: timestampSchema,
});
export const mailZoneListSchema = array(mailZoneSchema);

export const mailDomainSchema = strictObject({
  id: safeIdSchema,
  name: string(),
  zone_name: string(),
  capability: mailDomainCapabilitySchema,
  // Null in a zone the operator manages.
  zone: nullable(mailZoneSchema),
  status: union([literal('pending'), literal('verified')]),
  records: array(mailDomainRecordSchema),
  // A setup runs in the background; read the domain again until this is false.
  setting_up: boolean(),
  // Cloudflare refused to look for the pending zone's name servers now; check again later.
  zone_check_limited: boolean(),
  last_error: optional(string()),
  checked_at: nullable(timestampSchema),
  verified_at: nullable(timestampSchema),
  created_at: timestampSchema,
});
export const mailDomainListSchema = array(mailDomainSchema);

export const mailDomainInputSchema = strictObject({
  name: string(),
  capability: optional(mailDomainCapabilitySchema),
});

export const mailDomainPreviewInputSchema = strictObject({ name: string() });
/** What connecting a domain would find; changes nothing. */
export const mailDomainPreviewSchema = strictObject({
  name: string(),
  apex: string(),
  existing_mx: array(string()),
  suggested_capability: mailDomainCapabilitySchema,
});

/** The record types the DNS editor writes. */
export const mailDnsRecordTypeSchema = union([
  literal('A'),
  literal('AAAA'),
  literal('CNAME'),
  literal('TXT'),
  literal('MX'),
]);
export const mailDnsLockedReasonSchema = union([
  literal('mail_receiving'),
  literal('mail_spf'),
  literal('mail_dmarc'),
  literal('mail_dkim'),
  literal('mail_sending'),
]);
export const mailDnsRecordSchema = strictObject({
  id: string(),
  // A zone may hold types the editor does not write.
  type: string(),
  name: string(),
  content: string(),
  // `1` is automatic.
  ttl: positiveIntegerSchema,
  priority: optional(nonnegativeIntegerSchema),
  proxied: boolean(),
  // Zmail mail relies on the record, so it cannot be changed or deleted.
  locked: boolean(),
  locked_reason: optional(mailDnsLockedReasonSchema),
});
export const mailDnsRecordListSchema = array(mailDnsRecordSchema);

export const mailDnsRecordInputSchema = strictObject({
  type: mailDnsRecordTypeSchema,
  name: string(),
  content: string(),
  ttl: optional(number()),
  priority: optional(number()),
  // The contract defaults it to false; the client always says.
  proxied: boolean(),
});
/** Any field of a record; an omitted field keeps its current value. */
export const mailDnsRecordPatchSchema = strictObject({
  type: optional(mailDnsRecordTypeSchema),
  name: optional(string()),
  content: optional(string()),
  ttl: optional(number()),
  priority: optional(number()),
  proxied: optional(boolean()),
});

export const mailboxTypeSchema = union([literal('personal'), literal('shared')]);
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
  type: mailboxTypeSchema,
  // Null for a shared mailbox or after the owner account is deleted; kept while a personal mailbox awaits handover.
  owner_user_id: nullable(safeIdSchema),
  awaiting_handover: boolean(),
  // Mail created before this instant is sealed; set once at handover.
  sealed_before: nullable(timestampSchema),
  webhooks_enabled: boolean(),
  access: mailboxAccessSchema,
  // The caller's own unread count; 0 without `view`.
  unread_count: nonnegativeIntegerSchema,
  created_at: timestampSchema,
  // The mail role the mailbox belongs to; null when ungrouped. Optional until every API emits it.
  role_id: optional(nullable(safeIdSchema)),
});
export const mailboxListSchema = array(mailboxSchema);

const mailAddressSchema = strictObject({ address: string(), name: string() });

/** Where a message is; `default` is the Inbox for inbound and Sent for outbound mail. */
export const mailPlacementSchema = union([
  literal('default'),
  literal('archive'),
  literal('folder'),
  literal('trash'),
]);
export const MAIL_PLACEMENTS = ['default', 'archive', 'folder', 'trash'] as const;

export const mailLabelColorSchema = union([
  literal('gray'),
  literal('red'),
  literal('orange'),
  literal('yellow'),
  literal('green'),
  literal('teal'),
  literal('blue'),
  literal('purple'),
  literal('pink'),
]);
export const MAIL_LABEL_COLORS = [
  'gray',
  'red',
  'orange',
  'yellow',
  'green',
  'teal',
  'blue',
  'purple',
  'pink',
] as const;

/** The fields every listing shares, the log included. */
const logSummaryShape = {
  id: safeIdSchema,
  mailbox_id: safeIdSchema,
  direction: mailDirectionSchema,
  status: mailStatusSchema,
  from: mailAddressSchema,
  to: array(string()),
  cc: array(string()),
  subject: string(),
  read: boolean(),
  failure_code: string(),
  // "send_limit" while the message waits for room in the organization's send limit.
  queued_reason: nullable(literal('send_limit')),
  last_event: mailEventSummarySchema,
  attachment_count: nonnegativeIntegerSchema,
  template_id: nullable(safeIdSchema),
  created_at: timestampSchema,
};

/** Placement, labels, and the thread are shared; `starred` and `read` are the caller's own. */
const organizeShape = {
  placement: mailPlacementSchema,
  folder_id: nullable(safeIdSchema),
  label_ids: array(safeIdSchema),
  starred: boolean(),
  thread_id: safeIdSchema,
  trashed_at: nullable(timestampSchema),
};

/** With `group=thread`: the thread's messages in the listed view only. */
export const mailThreadSummarySchema = strictObject({
  message_count: positiveIntegerSchema,
  message_ids: array(safeIdSchema),
  unread: boolean(),
  starred: boolean(),
  label_ids: array(safeIdSchema),
});

const summaryShape = { ...logSummaryShape, ...organizeShape };

/** An attached file as listed in a message; the bytes are downloaded separately. */
export const mailAttachmentSchema = strictObject({
  id: safeIdSchema,
  filename: string(),
  content_type: string(),
  size: nonnegativeIntegerSchema,
});

/** `recipient` is '' for a message-level event; `detail` holds short machine-readable tokens. */
export const mailEventSchema = strictObject({
  type: mailEventTypeSchema,
  recipient: string(),
  detail: string(),
  occurred_at: timestampSchema,
});

export const mailMessageSummarySchema = strictObject({
  ...summaryShape,
  thread: optional(mailThreadSummarySchema),
});

export const mailMessageSchema = strictObject({
  ...summaryShape,
  text_body: string(),
  html_body: string(),
  message_id: string(),
  in_reply_to: string(),
  // Oldest first; always empty for inbound messages.
  events: array(mailEventSchema),
  attachments: array(mailAttachmentSchema),
});

const pageMetaSchema = strictObject({
  current_page: positiveIntegerSchema,
  per_page: positiveIntegerSchema,
  total: nonnegativeIntegerSchema,
  last_page: positiveIntegerSchema,
  from: nonnegativeIntegerSchema,
  to: nonnegativeIntegerSchema,
});
const pageLinksSchema = strictObject({
  first: string(),
  last: string(),
  prev: nullable(string()),
  next: nullable(string()),
});

export const mailMessagePageEnvelopeSchema = strictObject({
  code: literal(0),
  message: string(),
  data: array(mailMessageSummarySchema),
  meta: pageMetaSchema,
  links: pageLinksSchema,
});

/** A log entry is a summary plus the address of the mailbox that owns it. */
export const mailLogEntrySchema = strictObject({ ...logSummaryShape, mailbox_address: string() });

export const mailLogPageEnvelopeSchema = strictObject({
  code: literal(0),
  message: string(),
  data: array(mailLogEntrySchema),
  meta: pageMetaSchema,
  links: pageLinksSchema,
});

export const INBOX_VIEWS = ['all', 'today', 'pending', 'sent'] as const;
/** The longest `tz` the unified inbox accepts. */
export const INBOX_TIME_ZONE_MAX_LENGTH = 64;
/** The longest cursor the API writes is about 56 characters; anything past this is not one. */
export const INBOX_CURSOR_MAX_LENGTH = 128;

/**
 * A unified inbox row: a listing summary with the owning mailbox's address, the mailbox's mail
 * role now (null without one), and a short text snippet. `thread` only with `group=thread`.
 */
export const inboxItemSchema = strictObject({
  ...summaryShape,
  mailbox_address: string(),
  role_id: nullable(safeIdSchema),
  snippet: string(),
  thread: optional(mailThreadSummarySchema),
});

const cursorPageMetaSchema = strictObject({
  per_page: positiveIntegerSchema,
  has_more: boolean(),
  next_cursor: nullable(string()),
});

export const inboxPageEnvelopeSchema = strictObject({
  code: literal(0),
  message: string(),
  data: array(inboxItemSchema),
  meta: cursorPageMetaSchema,
});

/**
 * The caller's own unified inbox counts over the mailboxes they read; `today` runs from
 * `today_starts_at` (inclusive) to `today_ends_at` (exclusive) in the requested zone.
 */
export const inboxCountsSchema = strictObject({
  views: strictObject({
    all_unread: nonnegativeIntegerSchema,
    today: nonnegativeIntegerSchema,
    today_unread: nonnegativeIntegerSchema,
    pending: nonnegativeIntegerSchema,
    sent_waiting: nonnegativeIntegerSchema,
  }),
  roles: array(strictObject({ role_id: safeIdSchema, unread: nonnegativeIntegerSchema })),
  ungrouped_unread: nonnegativeIntegerSchema,
  mailboxes: array(strictObject({ mailbox_id: safeIdSchema, unread: nonnegativeIntegerSchema })),
  today_starts_at: timestampSchema,
  today_ends_at: timestampSchema,
});

/** The most mail roles an organization holds. */
export const MAIL_ROLES_MAX_PER_ORGANIZATION = 50;
/** A role name is 1 to 64 characters after trimming, unique in the organization ignoring case. */
export const MAIL_ROLE_NAME_MAX_LENGTH = 64;
/** A role description is at most 280 characters after trimming, without control characters. */
export const MAIL_ROLE_DESCRIPTION_MAX_LENGTH = 280;

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

/** A thread's messages outside the trash, oldest first, with their bodies. */
export const mailThreadSchema = array(mailMessageSchema);

export const mailFolderSchema = strictObject({
  id: safeIdSchema,
  name: string(),
  created_at: timestampSchema,
});
export const mailFolderListSchema = array(mailFolderSchema);

export const mailLabelSchema = strictObject({
  id: safeIdSchema,
  name: string(),
  color: mailLabelColorSchema,
  created_at: timestampSchema,
});
export const mailLabelListSchema = array(mailLabelSchema);

/** The caller's own counts for a mailbox's navigation; the trash and sealed mail never count. */
export const mailMessageCountsSchema = strictObject({
  placements: strictObject({
    default_inbound_unread: nonnegativeIntegerSchema,
    archive_unread: nonnegativeIntegerSchema,
  }),
  folders: array(strictObject({ folder_id: safeIdSchema, unread: nonnegativeIntegerSchema })),
  starred: nonnegativeIntegerSchema,
});

export const mailMessageUpdatesResultSchema = strictObject({ updated: nonnegativeIntegerSchema });
export const mailTrashPurgeSchema = strictObject({ deleted: nonnegativeIntegerSchema });

/** One UTC calendar day of the statistics window. */
export const mailDayStatsSchema = strictObject({
  day: iso.date(),
  sent: nonnegativeIntegerSchema,
  failed: nonnegativeIntegerSchema,
  received: nonnegativeIntegerSchema,
});
export const mailDayStatsListSchema = array(mailDayStatsSchema);

const templateVariablesSchema = record(string(), string());

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

export const sendMailMessageSchema = strictObject({
  to: array(string()),
  cc: optional(array(string())),
  // Absent when `template` supplies the subject and bodies.
  subject: optional(string()),
  text: optional(string()),
  html: optional(string()),
  in_reply_to: optional(string()),
  attachments: optional(array(mailAttachmentInputSchema())),
  template: optional(
    strictObject({ id: safeIdSchema, variables: optional(templateVariablesSchema) })
  ),
});

/** A file to send; `content` is standard base64. */
function mailAttachmentInputSchema() {
  return strictObject({
    filename: string(),
    content_type: optional(string()),
    content: string(),
  });
}

/** The decoded byte length of standard base64, or null when the text is not base64. */
export function base64DecodedLength(content: string): number | null {
  if (
    content.length === 0 ||
    content.length % 4 !== 0 ||
    !/^[A-Za-z0-9+/]*={0,2}$/u.test(content)
  ) {
    return null;
  }
  const padding = content.endsWith('==') ? 2 : content.endsWith('=') ? 1 : 0;
  return (content.length / 4) * 3 - padding;
}

/**
 * The contract's limits for files to send: at most 10, each with a usable name and base64 content,
 * all together at most 4 MiB decoded. Returns false for files the API would answer with
 * `MAILBOX.MESSAGE_INVALID`.
 */
export function attachmentsWithinLimits(
  attachments: readonly { filename: string; content: string }[]
): boolean {
  if (attachments.length > MAIL_MAX_ATTACHMENTS) return false;
  let total = 0;
  for (const attachment of attachments) {
    const name = attachment.filename.replaceAll('\\', '/').split('/').pop()?.trim() ?? '';
    if (name === '' || name === '.' || name === '..') return false;
    const size = base64DecodedLength(attachment.content);
    if (size === null || size === 0) return false;
    total += size;
    if (total > MAIL_ATTACHMENTS_MAX_BYTES) return false;
  }
  return true;
}

export const mailTemplateSummarySchema = strictObject({
  id: safeIdSchema,
  name: string(),
  subject: string(),
  variables: array(string()),
  created_at: timestampSchema,
  updated_at: timestampSchema,
});
export const mailTemplateListSchema = array(mailTemplateSummarySchema);

/** The contract checks only the version; the editor validates the rest of the document. */
export const mailTemplateDesignSchema = looseObject({ version: number() });

export const mailTemplateSchema = strictObject({
  id: safeIdSchema,
  name: string(),
  subject: string(),
  variables: array(string()),
  created_at: timestampSchema,
  updated_at: timestampSchema,
  html_body: string(),
  text_body: string(),
  sample_variables: templateVariablesSchema,
  // The visual editor reads it with its own parser; the API stores it as given.
  design: nullable(mailTemplateDesignSchema),
});

export const mailTemplateInputSchema = strictObject({
  name: string(),
  subject: string(),
  html: optional(string()),
  text: optional(string()),
  sample_variables: optional(templateVariablesSchema),
  design: optional(nullable(mailTemplateDesignSchema)),
});

export const mailTemplateTestInputSchema = strictObject({
  mailbox_id: safeIdSchema,
  to: array(string()),
  variables: optional(templateVariablesSchema),
});

export const mailContactStatusSchema = union([
  literal('subscribed'),
  literal('unsubscribed'),
  literal('suppressed'),
]);

export const mailContactSchema = strictObject({
  id: safeIdSchema,
  email: string(),
  first_name: string(),
  last_name: string(),
  properties: record(string(), string()),
  status: mailContactStatusSchema,
  status_reason: string(),
  list_ids: array(safeIdSchema),
  created_at: timestampSchema,
  updated_at: timestampSchema,
});

export const mailContactPageEnvelopeSchema = strictObject({
  code: literal(0),
  message: string(),
  data: array(mailContactSchema),
  meta: pageMetaSchema,
  links: pageLinksSchema,
});

export const mailContactListSchema = strictObject({
  id: safeIdSchema,
  name: string(),
  contact_count: nonnegativeIntegerSchema,
  created_at: timestampSchema,
});
export const mailContactListListSchema = array(mailContactListSchema);

export const mailContactImportSkipReasonSchema = union([
  literal('invalid_email'),
  literal('duplicate'),
  literal('invalid_name'),
  literal('invalid_property'),
  literal('too_many_properties'),
]);

export const mailContactImportSchema = strictObject({
  rows: nonnegativeIntegerSchema,
  created: nonnegativeIntegerSchema,
  updated: nonnegativeIntegerSchema,
  skipped_count: nonnegativeIntegerSchema,
  skipped: array(
    strictObject({ line: positiveIntegerSchema, reason: mailContactImportSkipReasonSchema })
  ),
  ignored_columns: array(string()),
});

export const mailContactInputSchema = strictObject({
  email: optional(string()),
  first_name: optional(string()),
  last_name: optional(string()),
  properties: optional(record(string(), string())),
  status: optional(union([literal('subscribed'), literal('unsubscribed')])),
  list_ids: optional(array(safeIdSchema)),
});

export const mailContactImportInputSchema = strictObject({
  csv: string(),
  list_id: optional(safeIdSchema),
});

export const mailContactListInputSchema = strictObject({ name: string() });

export const mailBroadcastStatusSchema = union([
  literal('draft'),
  literal('scheduled'),
  literal('sending'),
  literal('paused'),
  literal('sent'),
  literal('canceled'),
]);

export const mailRecipientStatusSchema = union([
  literal('pending'),
  literal('sent'),
  literal('failed'),
  literal('skipped'),
  literal('canceled'),
]);

/**
 * What a caller without `view` on the sender mailbox, or of a sealed broadcast, knows after pausing
 * or cancelling: the API answers 204, so only the new status.
 */
export const mailBroadcastStatusOnlySchema = strictObject({
  id: safeIdSchema,
  status: mailBroadcastStatusSchema,
});

export const mailBroadcastSchema = strictObject({
  id: safeIdSchema,
  name: string(),
  mailbox_id: safeIdSchema,
  template_id: safeIdSchema,
  list_id: safeIdSchema,
  unsubscribe_label: string(),
  // Template values shared by every recipient; a contact's properties override them.
  variables: templateVariablesSchema,
  status: mailBroadcastStatusSchema,
  scheduled_at: nullable(timestampSchema),
  started_at: nullable(timestampSchema),
  finished_at: nullable(timestampSchema),
  stop_reason: string(),
  // Staged sending: test_percent of the recipients go first; the rest waits test_wait_minutes.
  test_percent: nonnegativeIntegerSchema,
  test_wait_minutes: nonnegativeIntegerSchema,
  current_wave: positiveIntegerSchema,
  resume_at: nullable(timestampSchema),
  // An owner resumed the broadcast after the health check paused it.
  safety_override: boolean(),
  counts: strictObject({
    total: nonnegativeIntegerSchema,
    pending: nonnegativeIntegerSchema,
    sent: nonnegativeIntegerSchema,
    failed: nonnegativeIntegerSchema,
    skipped: nonnegativeIntegerSchema,
    canceled: nonnegativeIntegerSchema,
    bounced: nonnegativeIntegerSchema,
    complained: nonnegativeIntegerSchema,
  }),
  created_at: timestampSchema,
  updated_at: timestampSchema,
});

export const mailBroadcastPageEnvelopeSchema = strictObject({
  code: literal(0),
  message: string(),
  data: array(mailBroadcastSchema),
  meta: pageMetaSchema,
  links: pageLinksSchema,
});

export const mailBroadcastRecipientSchema = strictObject({
  id: safeIdSchema,
  contact_id: safeIdSchema,
  email: string(),
  status: mailRecipientStatusSchema,
  message_id: nullable(safeIdSchema),
  failure_code: string(),
  updated_at: timestampSchema,
});

export const mailBroadcastRecipientPageEnvelopeSchema = strictObject({
  code: literal(0),
  message: string(),
  data: array(mailBroadcastRecipientSchema),
  meta: pageMetaSchema,
  links: pageLinksSchema,
});

export const mailBroadcastInputSchema = strictObject({
  name: string(),
  mailbox_id: safeIdSchema,
  template_id: safeIdSchema,
  list_id: safeIdSchema,
  unsubscribe_label: optional(string()),
  test_percent: optional(nonnegativeIntegerSchema),
  test_wait_minutes: optional(nonnegativeIntegerSchema),
  variables: optional(templateVariablesSchema),
});

export const mailBroadcastSendInputSchema = strictObject({
  scheduled_at: optional(nullable(string())),
});

/** At least one field; `folder_id` goes only with `placement` `folder`. */
export const updateMailMessageSchema = strictObject({
  read: optional(boolean()),
  starred: optional(boolean()),
  placement: optional(mailPlacementSchema),
  folder_id: optional(safeIdSchema),
  // The complete set of the message's labels, replacing the current one.
  label_ids: optional(array(safeIdSchema)),
});

/** One change for 1 to 100 messages; a label may not be both added and removed. */
export const updateMailMessagesSchema = strictObject({
  message_ids: array(safeIdSchema),
  read: optional(boolean()),
  starred: optional(boolean()),
  placement: optional(mailPlacementSchema),
  folder_id: optional(safeIdSchema),
  add_label_ids: optional(array(safeIdSchema)),
  remove_label_ids: optional(array(safeIdSchema)),
});

export const mailFolderInputSchema = strictObject({ name: string() });
export const mailLabelInputSchema = strictObject({ name: string(), color: mailLabelColorSchema });

export type NormalizedLocalPart =
  { ok: true; value: string } | { ok: false; reason: 'invalid' | 'reserved' };

/** Trims and lowercases a local part, then applies the contract's address rules. */
export function normalizeLocalPart(raw: string): NormalizedLocalPart {
  const value = raw.trim().toLowerCase();
  if (!localPartPattern.test(value) || value.includes('..')) {
    return { ok: false, reason: 'invalid' };
  }
  if (reservedLocalParts.has(value)) return { ok: false, reason: 'reserved' };
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

/** A bare lowercase address, or null for anything else (display-name forms included). */
export function normalizeRecipient(raw: string): string | null {
  const value = raw.trim();
  if (value === '' || value.length > MAIL_ADDRESS_MAX_LENGTH || !recipientPattern.test(value)) {
    return null;
  }
  return value.toLowerCase();
}

export interface NormalizedMailMessage {
  to: string[];
  cc: string[];
  subject: string;
  text: string;
  html: string;
  in_reply_to: string;
}

interface MailMessageDraft {
  to: readonly string[];
  cc?: readonly string[];
  subject: string;
  text?: string;
  html?: string;
  in_reply_to?: string;
}

/**
 * Applies the contract's send rules: 1 to 50 bare recipients across `to` and `cc` after
 * duplicates are removed, a single-line subject, at least one body, and an optional Message-ID.
 * Returns null for a message the API would answer with `MAILBOX.MESSAGE_INVALID`.
 */
/**
 * The contract's recipient rules alone: 1 to 50 bare addresses across `to` and `cc` after
 * duplicates are removed. Used when a template supplies the subject and body.
 */
export function normalizeRecipientLists(
  rawTo: readonly string[],
  rawCc: readonly string[] = []
): { to: string[]; cc: string[] } | null {
  const seen = new Set<string>();
  const to = uniqueRecipients(rawTo, seen);
  if (to === null || to.length === 0) return null;
  const cc = uniqueRecipients(rawCc, seen);
  if (cc === null || to.length + cc.length > MAIL_MAX_RECIPIENTS) return null;
  return { to, cc };
}

export function normalizeMailMessage(draft: MailMessageDraft): NormalizedMailMessage | null {
  const recipients = normalizeRecipientLists(draft.to, draft.cc ?? []);
  if (recipients === null) return null;
  const { to, cc } = recipients;

  const subject = draft.subject.trim();
  if (
    subject === '' ||
    Array.from(subject).length > MAIL_SUBJECT_MAX_LENGTH ||
    /[\r\n\u0000]/u.test(subject)
  ) {
    return null;
  }
  const text = draft.text ?? '';
  const html = draft.html ?? '';
  if (text.trim() === '' && html.trim() === '') return null;
  if (utf8Length(text) > MAIL_BODY_MAX_BYTES || utf8Length(html) > MAIL_BODY_MAX_BYTES) return null;

  const inReplyTo = normalizeMessageId(draft.in_reply_to ?? '');
  if (inReplyTo === null) return null;
  return { to, cc, subject, text, html, in_reply_to: inReplyTo };
}

/** Splits a recipient field on commas and line breaks, dropping blanks. */
export function splitRecipients(value: string): string[] {
  return value
    .split(/[,\r\n]+/u)
    .map(part => part.trim())
    .filter(Boolean);
}

function uniqueRecipients(raw: readonly string[], seen: Set<string>): string[] | null {
  const recipients: string[] = [];
  for (const candidate of raw) {
    const address = normalizeRecipient(candidate);
    if (address === null) return null;
    if (seen.has(address)) continue;
    seen.add(address);
    recipients.push(address);
  }
  return recipients;
}

/** Strips angle brackets; '' when absent, null when it is not a Message-ID. */
function normalizeMessageId(raw: string): string | null {
  const id = raw.trim().replace(/^</u, '').replace(/>$/u, '');
  if (id === '') return '';
  if (id.length > MAIL_MESSAGE_ID_MAX_LENGTH || /[\s<>]/u.test(id) || !id.includes('@'))
    return null;
  return id;
}

function utf8Length(value: string): number {
  return new TextEncoder().encode(value).length;
}
