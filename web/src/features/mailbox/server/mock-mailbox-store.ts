import 'server-only';

import {
  MAILBOX_MAX_PER_ORGANIZATION,
  attachmentsWithinLimits,
  normalizeDisplayName,
  normalizeLocalPart,
  normalizeMailMessage,
} from '@/features/mailbox/schemas';
import type {
  MailAttachment,
  MailDayStats,
  MailDirection,
  MailEvent,
  MailLogEntry,
  MailLogFilters,
  MailLogPage,
  MailMessage,
  MailMessagePage,
  MailMessageSummary,
  Mailbox,
  MailboxDomain,
  SendMailMessageInput,
} from '@/features/mailbox/types';

/**
 * Development-only mailboxes, isolated per organization. It mirrors the browser-facing rules of
 * the Go mailbox starter: address validation and reserved names, addresses that stay reserved
 * after a mailbox is deleted, the per-organization mailbox limit, send validation, newest-first
 * listings without bodies, read state shared by the organization, the cross-mailbox log, and
 * per-day counts. Membership and roles are decided by the route before it calls the store.
 * Nothing leaves the process: a "sent" message is only stored, which is what the `log` provider
 * means.
 */
export const MOCK_MAILBOX_DOMAIN = 'mail.example.com';
export const MOCK_DAILY_SEND_LIMIT = 200;

const dayMilliseconds = 24 * 60 * 60 * 1_000;

const maxMockOrganizations = 1_000;
const maxMockMessagesPerMailbox = 500;

interface MailboxState {
  mailbox: Mailbox;
  messages: Map<number, MailMessage>;
  /** File content by message, then by attachment id. */
  files: Map<number, Map<number, { filename: string; content: Uint8Array }>>;
}

export type MockMailboxCreateResult =
  { ok: true; mailbox: Mailbox } | { ok: false; error: 'invalid' | 'reserved' | 'taken' | 'limit' };

export type MockMailMessageResult =
  { ok: true; message: MailMessage } | { ok: false; error: 'mailbox_not_found' | 'not_found' };

export type MockMailSendResult =
  { ok: true; message: MailMessage } | { ok: false; error: 'mailbox_not_found' | 'invalid' };

export type MockMailAttachmentResult =
  | { ok: true; filename: string; content: Uint8Array }
  | { ok: false; error: 'mailbox_not_found' | 'not_found' };

export type MockMailDeleteResult =
  { ok: true } | { ok: false; error: 'mailbox_not_found' | 'not_found' };

interface MockMailboxStoreDependencies {
  now: () => Date;
}

export class MockMailboxStore {
  private nextMailboxId = 1;
  private nextMessageId = 1;
  private nextAttachmentId = 1;
  private organizations = new Map<number, Map<number, MailboxState>>();
  /** Every address that is or ever was a mailbox, across all organizations. */
  private claimedAddresses = new Set<string>();

  constructor(
    private readonly dependencies: MockMailboxStoreDependencies = { now: () => new Date() }
  ) {}

  domains(): MailboxDomain[] {
    return [
      {
        domain: MOCK_MAILBOX_DOMAIN,
        max_mailboxes: MAILBOX_MAX_PER_ORGANIZATION,
        provider: 'log',
        inbound_enabled: false,
        daily_send_limit: MOCK_DAILY_SEND_LIMIT,
      },
    ];
  }

  /** Oldest first, as the API lists them. */
  list(organizationId: number): Mailbox[] {
    return [...this.mailboxesOf(organizationId).values()]
      .map(state => ({ ...state.mailbox, unread_count: unreadCount(state) }))
      .sort((left, right) => left.id - right.id);
  }

  create(
    organizationId: number,
    rawLocalPart: string,
    rawDisplayName: string
  ): MockMailboxCreateResult {
    const localPart = normalizeLocalPart(rawLocalPart);
    if (!localPart.ok) return { ok: false, error: localPart.reason };
    const displayName = normalizeDisplayName(rawDisplayName);
    if (displayName === null) return { ok: false, error: 'invalid' };

    const address = `${localPart.value}@${MOCK_MAILBOX_DOMAIN}`;
    // The API checks the organization's limit before the address, so an organization at the
    // limit learns about the limit even when the address is also taken.
    const mailboxes = this.mailboxesOf(organizationId);
    if (mailboxes.size >= MAILBOX_MAX_PER_ORGANIZATION) return { ok: false, error: 'limit' };
    if (this.claimedAddresses.has(address)) return { ok: false, error: 'taken' };

    const mailbox: Mailbox = {
      id: this.nextMailboxId++,
      address,
      display_name: displayName,
      // As in the API, a create response reports 0; listings count unread inbound messages.
      unread_count: 0,
      created_at: this.timestamp(),
    };
    const state: MailboxState = { mailbox, messages: new Map(), files: new Map() };
    mailboxes.set(mailbox.id, state);
    this.claimedAddresses.add(address);
    this.store(state, this.welcomeMessage(mailbox));
    return { ok: true, mailbox };
  }

  /** Deletes the mailbox and its messages; the address stays claimed. */
  delete(organizationId: number, mailboxId: number): boolean {
    return this.mailboxesOf(organizationId).delete(mailboxId);
  }

  listMessages(
    organizationId: number,
    mailboxId: number,
    direction: MailDirection | null,
    page: number,
    perPage: number
  ): MailMessagePage | null {
    const state = this.mailboxesOf(organizationId).get(mailboxId);
    if (!state) return null;
    const all = [...state.messages.values()]
      .filter(message => direction === null || message.direction === direction)
      .sort((left, right) => right.id - left.id)
      .map(summaryOf);
    const filter = direction === null ? '' : `direction=${direction}&`;
    return paginated(all, page, perPage, `/api/mailboxes/${mailboxId}/messages?${filter}`);
  }

  /**
   * Every mailbox of the organization, newest first, without bodies. The search is a case-insensitive
   * substring match on the subject, the sender address, and each recipient address, so `%` and
   * `_` only match themselves. Another organization's mailbox matches nothing.
   */
  listLog(
    organizationId: number,
    filters: MailLogFilters,
    page: number,
    perPage: number
  ): MailLogPage {
    const needle = filters.q?.trim().toLowerCase() ?? '';
    const entries: MailLogEntry[] = [];
    for (const state of this.mailboxesOf(organizationId).values()) {
      if (filters.mailboxId !== undefined && state.mailbox.id !== filters.mailboxId) continue;
      for (const message of state.messages.values()) {
        if (filters.direction && message.direction !== filters.direction) continue;
        if (filters.status && message.status !== filters.status) continue;
        if (needle !== '' && !matchesSearch(message, needle)) continue;
        entries.push({ ...summaryOf(message), mailbox_address: state.mailbox.address });
      }
    }
    entries.sort((left, right) => right.id - left.id);

    const query = new URLSearchParams();
    if (filters.direction) query.set('direction', filters.direction);
    if (filters.status) query.set('status', filters.status);
    if (filters.mailboxId !== undefined) query.set('mailbox_id', String(filters.mailboxId));
    if (needle !== '') query.set('q', filters.q?.trim() ?? '');
    const prefix = query.size > 0 ? `${query.toString()}&` : '';
    return paginated(entries, page, perPage, `/api/mail-messages?${prefix}`);
  }

  /**
   * One entry per UTC day for the last `days` days ending today, oldest first, empty days
   * included. `sent` and `failed` count outbound messages by final status; `received` counts
   * inbound messages.
   */
  stats(organizationId: number, days: number): MailDayStats[] {
    const now = this.dependencies.now();
    const today = Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), now.getUTCDate());
    const entries = Array.from({ length: days }, (_, index): MailDayStats => ({
      day: dayOf(today - (days - 1 - index) * dayMilliseconds),
      sent: 0,
      failed: 0,
      received: 0,
    }));
    const byDay = new Map(entries.map(entry => [entry.day, entry]));
    for (const state of this.mailboxesOf(organizationId).values()) {
      for (const message of state.messages.values()) {
        const entry = byDay.get(message.created_at.slice(0, 10));
        if (!entry) continue;
        if (message.direction === 'inbound') entry.received += 1;
        else if (message.status === 'sent') entry.sent += 1;
        else if (message.status === 'failed') entry.failed += 1;
      }
    }
    return entries;
  }

  getMessage(organizationId: number, mailboxId: number, messageId: number): MockMailMessageResult {
    const state = this.mailboxesOf(organizationId).get(mailboxId);
    if (!state) return { ok: false, error: 'mailbox_not_found' };
    const message = state.messages.get(messageId);
    return message ? { ok: true, message } : { ok: false, error: 'not_found' };
  }

  setRead(
    organizationId: number,
    mailboxId: number,
    messageId: number,
    read: boolean
  ): MockMailMessageResult {
    const found = this.getMessage(organizationId, mailboxId, messageId);
    if (!found.ok) return found;
    const message = { ...found.message, read };
    this.mailboxesOf(organizationId).get(mailboxId)?.messages.set(messageId, message);
    return { ok: true, message };
  }

  deleteMessage(
    organizationId: number,
    mailboxId: number,
    messageId: number
  ): MockMailDeleteResult {
    const state = this.mailboxesOf(organizationId).get(mailboxId);
    if (!state) return { ok: false, error: 'mailbox_not_found' };
    state.files.delete(messageId);
    return state.messages.delete(messageId) ? { ok: true } : { ok: false, error: 'not_found' };
  }

  attachment(
    organizationId: number,
    mailboxId: number,
    messageId: number,
    attachmentId: number
  ): MockMailAttachmentResult {
    const state = this.mailboxesOf(organizationId).get(mailboxId);
    if (!state) return { ok: false, error: 'mailbox_not_found' };
    const file = state.files.get(messageId)?.get(attachmentId);
    return file ? { ok: true, ...file } : { ok: false, error: 'not_found' };
  }

  /** Validates like the API and stores the message as `sent`; the mock has no provider. */
  send(
    organizationId: number,
    mailboxId: number,
    draft: Parameters<typeof normalizeMailMessage>[0] & Pick<SendMailMessageInput, 'attachments'>
  ): MockMailSendResult {
    const state = this.mailboxesOf(organizationId).get(mailboxId);
    if (!state) return { ok: false, error: 'mailbox_not_found' };
    const normalized = normalizeMailMessage(draft);
    if (!normalized) return { ok: false, error: 'invalid' };
    const drafts = draft.attachments ?? [];
    if (!attachmentsWithinLimits(drafts)) return { ok: false, error: 'invalid' };

    const id = this.nextMessageId++;
    const files = new Map<number, { filename: string; content: Uint8Array }>();
    const attachments = drafts.map((file): MailAttachment => {
      const attachmentId = this.nextAttachmentId++;
      const content = Uint8Array.from(Buffer.from(file.content, 'base64'));
      const filename = file.filename.replaceAll('\\', '/').split('/').pop()?.trim() ?? '';
      files.set(attachmentId, { filename, content });
      return {
        id: attachmentId,
        filename,
        content_type: /^[\w.+-]+\/[\w.+-]+$/u.test(file.content_type ?? '')
          ? (file.content_type as string)
          : 'application/octet-stream',
        size: content.byteLength,
      };
    });
    const sentAt = this.timestamp();
    // As the API's send call does: one message-level `accepted`, then what the provider
    // reported for each recipient. The mock's provider delivers to everyone at once.
    const events: MailEvent[] = [
      { type: 'accepted', recipient: '', detail: '', occurred_at: sentAt },
      ...[...normalized.to, ...normalized.cc].map((recipient): MailEvent => ({
        type: 'delivered',
        recipient,
        detail: '',
        occurred_at: sentAt,
      })),
    ];
    const message: MailMessage = {
      id,
      mailbox_id: mailboxId,
      direction: 'outbound',
      status: 'sent',
      from: { address: state.mailbox.address, name: state.mailbox.display_name },
      to: normalized.to,
      cc: normalized.cc,
      subject: normalized.subject,
      // A member wrote it, so it starts out read.
      read: true,
      failure_code: '',
      last_event: 'delivered',
      attachment_count: attachments.length,
      created_at: sentAt,
      text_body: normalized.text,
      html_body: normalized.html,
      message_id: `mock-${id}@${MOCK_MAILBOX_DOMAIN}`,
      in_reply_to: normalized.in_reply_to,
      events,
      attachments,
    };
    this.store(state, message);
    if (files.size > 0) state.files.set(id, files);
    return { ok: true, message };
  }

  /** Test seam: forget every mailbox, message, and claimed address. */
  reset(): void {
    this.nextMailboxId = 1;
    this.nextMessageId = 1;
    this.nextAttachmentId = 1;
    this.organizations.clear();
    this.claimedAddresses.clear();
  }

  private store(state: MailboxState, message: MailMessage): void {
    if (state.messages.size >= maxMockMessagesPerMailbox) {
      const oldest = state.messages.keys().next().value;
      if (oldest !== undefined) {
        state.messages.delete(oldest);
        state.files.delete(oldest);
      }
    }
    state.messages.set(message.id, message);
  }

  /** One unread inbound message, so a new mock inbox shows the read flow and the HTML sandbox. */
  private welcomeMessage(mailbox: Mailbox): MailMessage {
    const id = this.nextMessageId++;
    return {
      id,
      mailbox_id: mailbox.id,
      direction: 'inbound',
      status: 'received',
      from: { address: `hello@${MOCK_MAILBOX_DOMAIN}`, name: 'Mock Mail' },
      to: [mailbox.address],
      cc: [],
      subject: 'Welcome to your mock mailbox',
      read: false,
      failure_code: '',
      last_event: '',
      attachment_count: 0,
      created_at: this.timestamp(),
      text_body:
        'This message was created by the development mock. No mail is sent or received until the app uses the real API.',
      html_body:
        '<p>This message was created by the <strong>development mock</strong>. No mail is sent or received until the app uses the real API.</p>',
      message_id: `mock-${id}@${MOCK_MAILBOX_DOMAIN}`,
      in_reply_to: '',
      events: [],
      attachments: [],
    };
  }

  private mailboxesOf(organizationId: number): Map<number, MailboxState> {
    const existing = this.organizations.get(organizationId);
    if (existing) return existing;
    if (this.organizations.size >= maxMockOrganizations) {
      const oldest = this.organizations.keys().next().value;
      if (oldest !== undefined) this.organizations.delete(oldest);
    }
    const created = new Map<number, MailboxState>();
    this.organizations.set(organizationId, created);
    return created;
  }

  private timestamp(): string {
    return this.dependencies.now().toISOString();
  }
}

export const mockMailboxStore = new MockMailboxStore();

function summaryOf(message: MailMessage): MailMessageSummary {
  return {
    id: message.id,
    mailbox_id: message.mailbox_id,
    direction: message.direction,
    status: message.status,
    from: message.from,
    to: message.to,
    cc: message.cc,
    subject: message.subject,
    read: message.read,
    failure_code: message.failure_code,
    last_event: message.last_event,
    attachment_count: message.attachment_count,
    created_at: message.created_at,
  };
}

function matchesSearch(message: MailMessage, needle: string): boolean {
  return (
    message.subject.toLowerCase().includes(needle) ||
    message.from.address.toLowerCase().includes(needle) ||
    message.to.some(address => address.toLowerCase().includes(needle))
  );
}

/** The UTC calendar day of a timestamp, as `YYYY-MM-DD`. */
function dayOf(milliseconds: number): string {
  return new Date(milliseconds).toISOString().slice(0, 10);
}

/** `hrefPrefix` is the listing path with its filters, ready for `page=` to be appended. */
function paginated<T>(
  items: T[],
  page: number,
  perPage: number,
  hrefPrefix: string
): { items: T[]; meta: MailMessagePage['meta']; links: MailMessagePage['links'] } {
  const total = items.length;
  const lastPage = Math.max(1, Math.ceil(total / perPage));
  const start = (page - 1) * perPage;
  const data = items.slice(start, start + perPage);
  const href = (target: number) => `${hrefPrefix}page=${target}&per_page=${perPage}`;
  return {
    items: data,
    meta: {
      current_page: page,
      per_page: perPage,
      total,
      last_page: lastPage,
      from: data.length ? start + 1 : 0,
      to: data.length ? start + data.length : 0,
    },
    links: {
      first: href(1),
      last: href(lastPage),
      prev: page > 1 ? href(page - 1) : null,
      next: page < lastPage ? href(page + 1) : null,
    },
  };
}

function unreadCount(state: MailboxState): number {
  let unread = 0;
  for (const message of state.messages.values()) {
    if (message.direction === 'inbound' && !message.read) unread += 1;
  }
  return unread;
}
