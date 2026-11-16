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
  strictObject,
  string,
  union,
} from 'zod/mini';

/** Limits from `contracts/MAILBOXES.md`. */
export const MAILBOX_LOCAL_PART_MAX_LENGTH = 64;
export const MAILBOX_DISPLAY_NAME_MAX_LENGTH = 120;
export const MAILBOX_MAX_PER_USER = 10;
export const MAIL_MAX_RECIPIENTS = 50;
export const MAIL_ADDRESS_MAX_LENGTH = 320;
export const MAIL_SUBJECT_MAX_LENGTH = 998;
export const MAIL_BODY_MAX_BYTES = 256 * 1_024;
export const MAIL_MESSAGE_ID_MAX_LENGTH = 900;
export const MAIL_PAGE_MAX_SIZE = 100;

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

export const mailboxDomainSchema = strictObject({
  domain: string(),
  max_mailboxes: number().check(int()),
});
export const mailboxDomainListSchema = array(mailboxDomainSchema);

export const mailboxSchema = strictObject({
  id: safeIdSchema,
  address: string(),
  display_name: string(),
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
  created_at: timestampSchema,
};

export const mailMessageSummarySchema = strictObject(summaryShape);

export const mailMessageSchema = strictObject({
  ...summaryShape,
  text_body: string(),
  html_body: string(),
  message_id: string(),
  in_reply_to: string(),
});

export const mailMessagePageEnvelopeSchema = strictObject({
  code: literal(0),
  message: string(),
  data: array(mailMessageSummarySchema),
  meta: strictObject({
    current_page: positiveIntegerSchema,
    per_page: positiveIntegerSchema,
    total: nonnegativeIntegerSchema,
    last_page: positiveIntegerSchema,
    from: nonnegativeIntegerSchema,
    to: nonnegativeIntegerSchema,
  }),
  links: strictObject({
    first: string(),
    last: string(),
    prev: nullable(string()),
    next: nullable(string()),
  }),
});

/** Request bodies. Field rules beyond the types are checked by the normalizers below. */
export const createMailboxSchema = strictObject({
  local_part: string(),
  display_name: optional(string()),
});

export const sendMailMessageSchema = strictObject({
  to: array(string()),
  cc: optional(array(string())),
  subject: string(),
  text: optional(string()),
  html: optional(string()),
  in_reply_to: optional(string()),
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
export function normalizeMailMessage(draft: MailMessageDraft): NormalizedMailMessage | null {
  const seen = new Set<string>();
  const to = uniqueRecipients(draft.to, seen);
  if (to === null || to.length === 0) return null;
  const cc = uniqueRecipients(draft.cc ?? [], seen);
  if (cc === null || to.length + cc.length > MAIL_MAX_RECIPIENTS) return null;

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
