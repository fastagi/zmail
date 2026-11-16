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
  union,
} from 'zod/mini';

/** Limits from `contracts/MAILBOXES.md`. */
export const MAILBOX_LOCAL_PART_MAX_LENGTH = 64;
export const MAILBOX_DISPLAY_NAME_MAX_LENGTH = 120;
export const MAILBOX_MAX_PER_ORGANIZATION = 50;
export const MAIL_MAX_RECIPIENTS = 50;
export const MAIL_ADDRESS_MAX_LENGTH = 320;
export const MAIL_SUBJECT_MAX_LENGTH = 998;
export const MAIL_BODY_MAX_BYTES = 256 * 1_024;
export const MAIL_MESSAGE_ID_MAX_LENGTH = 900;
export const MAIL_PAGE_MAX_SIZE = 100;
export const MAIL_LOG_QUERY_MAX_LENGTH = 200;
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
export const MAIL_TEMPLATE_VALUE_MAX_LENGTH = 2_000;
export const MAIL_TEMPLATE_TEST_MAX_RECIPIENTS = 5;
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
  max_mailboxes: number().check(int()),
  // `cloudflare` delivers; `log` only records and is for development.
  provider: union([literal('cloudflare'), literal('log')]),
  inbound_enabled: boolean(),
  daily_send_limit: positiveIntegerSchema,
});
export const mailboxDomainListSchema = array(mailboxDomainSchema);

export const mailboxSchema = strictObject({
  id: safeIdSchema,
  address: string(),
  display_name: string(),
  unread_count: nonnegativeIntegerSchema,
  created_at: timestampSchema,
});
export const mailboxListSchema = array(mailboxSchema);

const mailAddressSchema = strictObject({ address: string(), name: string() });

const summaryShape = {
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
  last_event: mailEventSummarySchema,
  attachment_count: nonnegativeIntegerSchema,
  template_id: nullable(safeIdSchema),
  created_at: timestampSchema,
};

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

export const mailMessageSummarySchema = strictObject(summaryShape);

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
export const mailLogEntrySchema = strictObject({ ...summaryShape, mailbox_address: string() });

export const mailLogPageEnvelopeSchema = strictObject({
  code: literal(0),
  message: string(),
  data: array(mailLogEntrySchema),
  meta: pageMetaSchema,
  links: pageLinksSchema,
});

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
export const createMailboxSchema = strictObject({
  local_part: string(),
  display_name: optional(string()),
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
});

export const mailTemplateInputSchema = strictObject({
  name: string(),
  subject: string(),
  html: optional(string()),
  text: optional(string()),
  sample_variables: optional(templateVariablesSchema),
});

export const mailTemplateTestInputSchema = strictObject({
  mailbox_id: safeIdSchema,
  to: array(string()),
  variables: optional(templateVariablesSchema),
});

export const updateMailMessageSchema = strictObject({ read: boolean() });

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
