import 'server-only';

import {
  MAILBOX_MAX_PER_USER,
  normalizeDisplayName,
  normalizeLocalPart,
  normalizeMailMessage,
} from '@/features/mailbox/schemas';
import type {
  MailDirection,
  MailMessage,
  MailMessagePage,
  MailMessageSummary,
  Mailbox,
  MailboxDomain,
} from '@/features/mailbox/types';

/**
 * Development-only mailboxes, isolated per mock user. It mirrors the browser-facing rules of the
 * Go mailbox starter: address validation and reserved names, addresses that stay reserved after
 * a mailbox is deleted, the per-user mailbox limit, send validation, newest-first listings
 * without bodies, and read state. Nothing leaves the process: a "sent" message is only stored.
 */
export const MOCK_MAILBOX_DOMAIN = 'mail.example.com';

const maxMockUsers = 1_000;
const maxMockMessagesPerMailbox = 500;

interface MailboxState {
  mailbox: Mailbox;
  messages: Map<number, MailMessage>;
}

export type MockMailboxCreateResult =
  { ok: true; mailbox: Mailbox } | { ok: false; error: 'invalid' | 'reserved' | 'taken' | 'limit' };

export type MockMailMessageResult =
  { ok: true; message: MailMessage } | { ok: false; error: 'mailbox_not_found' | 'not_found' };

export type MockMailSendResult =
  { ok: true; message: MailMessage } | { ok: false; error: 'mailbox_not_found' | 'invalid' };

export type MockMailDeleteResult =
  { ok: true } | { ok: false; error: 'mailbox_not_found' | 'not_found' };

interface MockMailboxStoreDependencies {
  now: () => Date;
}

export class MockMailboxStore {
  private nextMailboxId = 1;
  private nextMessageId = 1;
  private users = new Map<string, Map<number, MailboxState>>();
  /** Every address that is or ever was a mailbox, across all users. */
  private claimedAddresses = new Set<string>();

  constructor(
    private readonly dependencies: MockMailboxStoreDependencies = { now: () => new Date() }
  ) {}

  domains(): MailboxDomain[] {
    return [{ domain: MOCK_MAILBOX_DOMAIN, max_mailboxes: MAILBOX_MAX_PER_USER }];
  }

  /** Oldest first, as the API lists them. */
  list(userKey: string): Mailbox[] {
    return [...this.mailboxesOf(userKey).values()]
      .map(state => state.mailbox)
      .sort((left, right) => left.id - right.id);
  }

  create(userKey: string, rawLocalPart: string, rawDisplayName: string): MockMailboxCreateResult {
    const localPart = normalizeLocalPart(rawLocalPart);
    if (!localPart.ok) return { ok: false, error: localPart.reason };
    const displayName = normalizeDisplayName(rawDisplayName);
    if (displayName === null) return { ok: false, error: 'invalid' };

    const address = `${localPart.value}@${MOCK_MAILBOX_DOMAIN}`;
    if (this.claimedAddresses.has(address)) return { ok: false, error: 'taken' };
    const mailboxes = this.mailboxesOf(userKey);
    if (mailboxes.size >= MAILBOX_MAX_PER_USER) return { ok: false, error: 'limit' };

    const mailbox: Mailbox = {
      id: this.nextMailboxId++,
      address,
      display_name: displayName,
      created_at: this.timestamp(),
    };
    const state: MailboxState = { mailbox, messages: new Map() };
    mailboxes.set(mailbox.id, state);
    this.claimedAddresses.add(address);
    this.store(state, this.welcomeMessage(mailbox));
    return { ok: true, mailbox };
  }

  /** Deletes the mailbox and its messages; the address stays claimed. */
  delete(userKey: string, mailboxId: number): boolean {
    return this.mailboxesOf(userKey).delete(mailboxId);
  }

  listMessages(
    userKey: string,
    mailboxId: number,
    direction: MailDirection | null,
    page: number,
    perPage: number
  ): MailMessagePage | null {
    const state = this.mailboxesOf(userKey).get(mailboxId);
    if (!state) return null;
    const all = [...state.messages.values()]
      .filter(message => direction === null || message.direction === direction)
      .sort((left, right) => right.id - left.id)
      .map(summaryOf);
    return paginated(all, page, perPage, mailboxId, direction);
  }

  getMessage(userKey: string, mailboxId: number, messageId: number): MockMailMessageResult {
    const state = this.mailboxesOf(userKey).get(mailboxId);
    if (!state) return { ok: false, error: 'mailbox_not_found' };
    const message = state.messages.get(messageId);
    return message ? { ok: true, message } : { ok: false, error: 'not_found' };
  }

  setRead(
    userKey: string,
    mailboxId: number,
    messageId: number,
    read: boolean
  ): MockMailMessageResult {
    const found = this.getMessage(userKey, mailboxId, messageId);
    if (!found.ok) return found;
    const message = { ...found.message, read };
    this.mailboxesOf(userKey).get(mailboxId)?.messages.set(messageId, message);
    return { ok: true, message };
  }

  deleteMessage(userKey: string, mailboxId: number, messageId: number): MockMailDeleteResult {
    const state = this.mailboxesOf(userKey).get(mailboxId);
    if (!state) return { ok: false, error: 'mailbox_not_found' };
    return state.messages.delete(messageId) ? { ok: true } : { ok: false, error: 'not_found' };
  }

  /** Validates like the API and stores the message as `sent`; the mock has no provider. */
  send(
    userKey: string,
    mailboxId: number,
    draft: Parameters<typeof normalizeMailMessage>[0]
  ): MockMailSendResult {
    const state = this.mailboxesOf(userKey).get(mailboxId);
    if (!state) return { ok: false, error: 'mailbox_not_found' };
    const normalized = normalizeMailMessage(draft);
    if (!normalized) return { ok: false, error: 'invalid' };

    const id = this.nextMessageId++;
    const message: MailMessage = {
      id,
      mailbox_id: mailboxId,
      direction: 'outbound',
      status: 'sent',
      from: { address: state.mailbox.address, name: state.mailbox.display_name },
      to: normalized.to,
      cc: normalized.cc,
      subject: normalized.subject,
      // The owner wrote it, so it starts out read.
      read: true,
      failure_code: '',
      created_at: this.timestamp(),
      text_body: normalized.text,
      html_body: normalized.html,
      message_id: `mock-${id}@${MOCK_MAILBOX_DOMAIN}`,
      in_reply_to: normalized.in_reply_to,
    };
    this.store(state, message);
    return { ok: true, message };
  }

  /** Test seam: forget every mailbox, message, and claimed address. */
  reset(): void {
    this.nextMailboxId = 1;
    this.nextMessageId = 1;
    this.users.clear();
    this.claimedAddresses.clear();
  }

  private store(state: MailboxState, message: MailMessage): void {
    if (state.messages.size >= maxMockMessagesPerMailbox) {
      const oldest = state.messages.keys().next().value;
      if (oldest !== undefined) state.messages.delete(oldest);
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
      created_at: this.timestamp(),
      text_body:
        'This message was created by the development mock. No mail is sent or received until the app uses the real API.',
      html_body:
        '<p>This message was created by the <strong>development mock</strong>. No mail is sent or received until the app uses the real API.</p>',
      message_id: `mock-${id}@${MOCK_MAILBOX_DOMAIN}`,
      in_reply_to: '',
    };
  }

  private mailboxesOf(userKey: string): Map<number, MailboxState> {
    const existing = this.users.get(userKey);
    if (existing) return existing;
    if (this.users.size >= maxMockUsers) {
      const oldest = this.users.keys().next().value;
      if (oldest !== undefined) this.users.delete(oldest);
    }
    const created = new Map<number, MailboxState>();
    this.users.set(userKey, created);
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
    created_at: message.created_at,
  };
}

function paginated(
  items: MailMessageSummary[],
  page: number,
  perPage: number,
  mailboxId: number,
  direction: MailDirection | null
): MailMessagePage {
  const total = items.length;
  const lastPage = Math.max(1, Math.ceil(total / perPage));
  const start = (page - 1) * perPage;
  const data = items.slice(start, start + perPage);
  const filter = direction === null ? '' : `direction=${direction}&`;
  const href = (target: number) =>
    `/api/mailboxes/${mailboxId}/messages?${filter}page=${target}&per_page=${perPage}`;
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
