import { array, boolean, iso, literal, nullable, optional, strictObject, string, union } from 'zod/mini';

import {
  cursorPageMetaSchema,
  mailAddressSchema,
  mailDirectionSchema,
  mailEventSchema,
  mailEventSummarySchema,
  mailLabelColorSchema,
  mailSourceSchema,
  mailStatusSchema,
  nonnegativeIntegerSchema,
  pageLinksSchema,
  pageMetaSchema,
  positiveIntegerSchema,
  safeIdSchema,
  templateVariablesSchema,
  timestampSchema,
} from './shared-schemas';

export const MAIL_MAX_RECIPIENTS = 50;
export const MAIL_ADDRESS_MAX_LENGTH = 320;
export const MAIL_SUBJECT_MAX_LENGTH = 998;
export const MAIL_BODY_MAX_BYTES = 256 * 1_024;
export const MAIL_MESSAGE_ID_MAX_LENGTH = 900;
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

const recipientPattern = /^[^\s@<>"(),:;\\[\]]+@[^\s@<>"(),:;\\[\]]+\.[^\s@<>"(),:;\\[\]]+$/u;

/** AI triage categories; `review` means the model could not decide (`contracts/MAILBOXES.md`). */
export const MAIL_TRIAGE_CATEGORIES = ['reply', 'deadline', 'fyi', 'review'] as const;
export const mailTriageCategorySchema = literal(MAIL_TRIAGE_CATEGORIES);

/** A message's triage result, shared by every reader; `reason` is empty for a manual decision. */
export const mailTriageSchema = strictObject({
  category: mailTriageCategorySchema,
  reason: string(),
  due_at: nullable(timestampSchema),
  source: literal(['ai', 'manual']),
});

/** The caller's unread inbound mail by triage category. */
export const inboxTriageCountsSchema = strictObject({
  reply: nonnegativeIntegerSchema,
  deadline: nonnegativeIntegerSchema,
  fyi: nonnegativeIntegerSchema,
  review: nonnegativeIntegerSchema,
});

/** Where a message is; `default` is the Inbox for inbound and Sent for outbound mail. */
export const mailPlacementSchema = union([
  literal('default'),
  literal('archive'),
  literal('folder'),
  literal('trash'),
]);
export const MAIL_PLACEMENTS = ['default', 'archive', 'folder', 'trash'] as const;

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
  // "send_limit" or "scheduled" while the message waits to send.
  queued_reason: nullable(union([literal('send_limit'), literal('scheduled')])),
  scheduled_at: optional(nullable(timestampSchema)),
  snoozed_until: optional(nullable(timestampSchema)),
  last_event: mailEventSummarySchema,
  attachment_count: nonnegativeIntegerSchema,
  // Where the message came from; optional until every API emits it, and missing reads as `zmail`.
  source: optional(mailSourceSchema),
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

export const mailMessageSummarySchema = strictObject({
  ...summaryShape,
  thread: optional(mailThreadSummarySchema),
});

export const mailMessageSchema = strictObject({
  ...summaryShape,
  text_body: string(),
  html_body: string(),
  // Blind copies: only on the detail the sending member reads; absent everywhere else.
  bcc: optional(array(string())),
  // True when codes or sign-in links of an inbound message were masked; absent from older APIs.
  secrets_masked: optional(boolean()),
  message_id: string(),
  in_reply_to: string(),
  // Oldest first; always empty for inbound messages.
  events: array(mailEventSchema),
  attachments: array(mailAttachmentSchema),
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

export const INBOX_VIEWS = ['all', 'today', 'pending', 'snoozed', 'sent'] as const;
/** Shared-conversation assignment filters of the unified inbox; see `contracts/MAILBOXES.md`. */
export const INBOX_ASSIGNMENTS = ['mine', 'unassigned', 'closed'] as const;
/** The longest `tz` the unified inbox accepts. */
export const INBOX_TIME_ZONE_MAX_LENGTH = 64;
/** The longest cursor the API writes is about 56 characters; anything past this is not one. */
export const INBOX_CURSOR_MAX_LENGTH = 128;
/** Date ranges of the unified inbox list: the past week, month, or year, or older than a year. */
export const INBOX_DATE_RANGES = ['week', 'month', 'year', 'older'] as const;

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
  // The AI triage result; null while the organization's triage is off. Optional until every API emits it.
  triage: optional(nullable(mailTriageSchema)),
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
    snoozed: optional(nonnegativeIntegerSchema),
  }),
  roles: array(strictObject({ role_id: safeIdSchema, unread: nonnegativeIntegerSchema })),
  ungrouped_unread: nonnegativeIntegerSchema,
  mailboxes: array(strictObject({ mailbox_id: safeIdSchema, unread: nonnegativeIntegerSchema })),
  today_starts_at: timestampSchema,
  today_ends_at: timestampSchema,
  // Unread mail by AI triage category; null while the organization's triage is off, which hides it.
  triage: optional(nullable(inboxTriageCountsSchema)),
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
  scheduled_at: optional(timestampSchema),
});

/** A file to send; `content` is standard base64. */
function mailAttachmentInputSchema() {
  return strictObject({
    filename: string(),
    content_type: optional(string()),
    content: string(),
  });
}

/** At least one field; `folder_id` goes only with `placement` `folder`. */
export const updateMailMessageSchema = strictObject({
  read: optional(boolean()),
  starred: optional(boolean()),
  placement: optional(mailPlacementSchema),
  folder_id: optional(safeIdSchema),
  // The complete set of the message's labels, replacing the current one.
  label_ids: optional(array(safeIdSchema)),
  snoozed_until: optional(timestampSchema),
  clear_snooze: optional(boolean()),
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
  snoozed_until: optional(timestampSchema),
  clear_snooze: optional(boolean()),
});

export const mailFolderInputSchema = strictObject({ name: string() });
export const mailLabelInputSchema = strictObject({ name: string(), color: mailLabelColorSchema });

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

/**
 * Applies the contract's send rules: 1 to 50 bare recipients across `to` and `cc` after
 * duplicates are removed, a single-line subject, at least one body, and an optional Message-ID.
 * Returns null for a message the API would answer with `MAILBOX.MESSAGE_INVALID`.
 */
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

/** A calendar day `YYYY-MM-DD` the unified inbox accepts as `since` or `until`: 1970 or later. */
export function isInboxDay(value: string): boolean {
  if (!/^\d{4}-\d{2}-\d{2}$/u.test(value)) return false;
  const [year, month, day] = value.split('-').map(Number);
  const date = new Date(Date.UTC(year, month - 1, day));
  return (
    year >= 1970 &&
    date.getUTCFullYear() === year &&
    date.getUTCMonth() === month - 1 &&
    date.getUTCDate() === day
  );
}
