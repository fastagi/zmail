import type { MailAttachment, MailMessage } from '@/features/mailbox/types';

export type ComposeMode = 'compose' | 'reply' | 'forward';

export interface ComposeDraft {
  to: string;
  cc?: string;
  subject: string;
  body?: string;
  inReplyTo: string;
  reply: boolean;
  mode?: ComposeMode;
  /**
   * The files of the message being forwarded and where they are stored: the composer shows them
   * and, on send, reads each one and attaches it again.
   */
  forwarded?: { mailboxId: number; messageId: number; attachments: readonly MailAttachment[] };
}

export interface QuotedReplyCopy {
  prefixSubject: (subject: string) => string;
  quotedHeader: (params: { date: string; sender: string }) => string;
  formatDate: (isoDate: string) => string;
}

export interface ForwardDraftCopy {
  prefixSubject: (subject: string) => string;
  forwardedHeader: string;
  formatDate: (isoDate: string) => string;
  labels: {
    from: string;
    date: string;
    subject: string;
    to: string;
    cc: string;
  };
}

export const emptyDraft: ComposeDraft = {
  to: '',
  cc: '',
  subject: '',
  body: '',
  inReplyTo: '',
  reply: false,
  mode: 'compose',
};

function formatSender(from: MailMessage['from']): string {
  return from.name ? `${from.name} <${from.address}>` : from.address;
}

function quoteTextBody(text: string, header: string): string {
  if (text.trim() === '') return '';
  const quoted = text
    .replace(/\r\n/gu, '\n')
    .split('\n')
    .map(line => (line === '' ? '>' : `> ${line}`))
    .join('\n');
  return `\n\n${header}\n${quoted}`;
}

/** A reply goes to the sender, keeps an existing `Re:` prefix, and names the message it answers. */
export function replyDraft(
  message: MailMessage,
  prefixSubject: (subject: string) => string
): ComposeDraft {
  return {
    to: message.from.address,
    subject: /^re:/iu.test(message.subject) ? message.subject : prefixSubject(message.subject),
    inReplyTo: message.message_id,
    reply: true,
    mode: 'reply',
  };
}

/** A reply that quotes the plain-text body of the message it answers. */
export function quotedReplyDraft(message: MailMessage, copy: QuotedReplyCopy): ComposeDraft {
  const base = replyDraft(message, copy.prefixSubject);
  const header = copy.quotedHeader({
    date: copy.formatDate(message.created_at),
    sender: formatSender(message.from),
  });
  return {
    ...base,
    cc: '',
    body: quoteTextBody(message.text_body, header),
    mode: 'reply',
  };
}

/**
 * Computes deduplicated lowercase `to` and `cc` lists for Reply All, excluding the current
 * `mailboxAddress` case-insensitively and keeping the sender in `to`.
 */
export function replyAllRecipients(
  message: MailMessage,
  mailboxAddress = ''
): { to: string[]; cc: string[] } {
  const self = mailboxAddress.trim().toLowerCase();
  const seen = new Set<string>();
  if (self !== '') seen.add(self);

  const sender = message.from.address.trim().toLowerCase();
  const to: string[] = [];
  if (sender !== '' && !seen.has(sender)) {
    seen.add(sender);
    to.push(sender);
  }

  const cc: string[] = [];
  for (const raw of [...message.to, ...message.cc]) {
    const address = raw.trim().toLowerCase();
    if (address === '' || seen.has(address)) continue;
    seen.add(address);
    if (to.length === 0) {
      to.push(address);
    } else {
      cc.push(address);
    }
  }
  if (to.length === 0 && sender !== '') {
    to.push(sender);
  }
  return { to, cc };
}

/**
 * Replies to the sender and every other unique recipient except `mailboxAddress`, quoting the
 * original plain-text body.
 */
export function replyAllDraft(
  message: MailMessage,
  mailboxAddress: string,
  copy: QuotedReplyCopy
): ComposeDraft {
  const quoted = quotedReplyDraft(message, copy);
  const recipients = replyAllRecipients(message, mailboxAddress);
  return {
    ...quoted,
    to: recipients.to.join(', '),
    cc: recipients.cc.join(', '),
  };
}

/** Forwards a message with a `Fwd:` subject prefix, header block, and plain-text body. */
export function forwardDraft(message: MailMessage, copy: ForwardDraftCopy): ComposeDraft {
  const subject = /^fwd?:/iu.test(message.subject)
    ? message.subject
    : copy.prefixSubject(message.subject);
  const headerLines = [
    copy.forwardedHeader,
    `${copy.labels.from}: ${formatSender(message.from)}`,
    `${copy.labels.date}: ${copy.formatDate(message.created_at)}`,
    `${copy.labels.subject}: ${message.subject}`,
    `${copy.labels.to}: ${message.to.join(', ')}`,
    ...(message.cc.length > 0 ? [`${copy.labels.cc}: ${message.cc.join(', ')}`] : []),
  ];
  const text = message.text_body.replace(/\r\n/gu, '\n');
  const body =
    text.trim() === ''
      ? `\n\n${headerLines.join('\n')}`
      : `\n\n${headerLines.join('\n')}\n\n${text}`;
  return {
    to: '',
    cc: '',
    subject,
    body,
    inReplyTo: '',
    reply: false,
    mode: 'forward',
    ...(message.attachments.length > 0
      ? {
          forwarded: {
            mailboxId: message.mailbox_id,
            messageId: message.id,
            attachments: message.attachments,
          },
        }
      : {}),
  };
}
