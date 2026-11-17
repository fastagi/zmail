import 'server-only';

import {
  MAILBOX_MAX_PER_ORGANIZATION,
  MAIL_SUBJECT_MAX_LENGTH,
  MAIL_TEMPLATE_MAX_PER_ORGANIZATION,
  MAIL_TEMPLATE_MAX_VARIABLES,
  MAIL_TEMPLATE_NAME_MAX_LENGTH,
  MAIL_TEMPLATE_VALUE_MAX_LENGTH,
  MAIL_TEMPLATE_VARIABLE_PATTERN,
  MAIL_BODY_MAX_BYTES,
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
  MailTemplate,
  MailTemplateInput,
  MailTemplateSummary,
  SendMailMessageInput,
} from '@/features/mailbox/types';
import { renderTemplate, templateVariables } from '@/features/mailbox/utils/template-render';

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
  | { ok: true; message: MailMessage }
  | { ok: false; error: 'mailbox_not_found' | 'invalid' | 'template_not_found' }
  | { ok: false; error: 'variables_missing'; missing: string[] };

export type MockMailTemplateResult =
  | { ok: true; template: MailTemplate }
  | { ok: false; error: 'not_found' | 'invalid' | 'taken' | 'limit' };

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
  private nextTemplateId = 1;
  private templates = new Map<number, Map<number, MailTemplate>>();
  private organizations = new Map<number, Map<number, MailboxState>>();
  /** Every address that is or ever was a mailbox, across all organizations. */
  private claimedAddresses = new Set<string>();

  constructor(
    private readonly dependencies: MockMailboxStoreDependencies = { now: () => new Date() }
  ) {}

  /** The shared domain first, then the organization's verified domains. */
  domains(verifiedDomains: readonly string[] = []): MailboxDomain[] {
    const settings = {
      max_mailboxes: MAILBOX_MAX_PER_ORGANIZATION,
      provider: 'log' as const,
      inbound_enabled: false,
      daily_send_limit: MOCK_DAILY_SEND_LIMIT,
      broadcasts_enabled: true,
      custom_domains_enabled: true,
    };
    return [
      { domain: MOCK_MAILBOX_DOMAIN, kind: 'shared', ...settings },
      ...verifiedDomains.map(domain => ({ domain, kind: 'organization' as const, ...settings })),
    ];
  }

  /** Whether a live mailbox of any organization has an address on the domain. */
  usesDomain(name: string): boolean {
    for (const mailboxes of this.organizations.values()) {
      for (const state of mailboxes.values()) {
        if (state.mailbox.address.endsWith(`@${name}`)) return true;
      }
    }
    return false;
  }

  /** Oldest first, as the API lists them. */
  list(organizationId: number): Mailbox[] {
    return [...this.mailboxesOf(organizationId).values()]
      .map(state => ({ ...state.mailbox, unread_count: unreadCount(state) }))
      .sort((left, right) => left.id - right.id);
  }

  /** `addressDomain` is the shared domain or a verified domain the route already resolved. */
  create(
    organizationId: number,
    rawLocalPart: string,
    rawDisplayName: string,
    addressDomain: string = MOCK_MAILBOX_DOMAIN
  ): MockMailboxCreateResult {
    const localPart = normalizeLocalPart(rawLocalPart);
    if (!localPart.ok) return { ok: false, error: localPart.reason };
    const displayName = normalizeDisplayName(rawDisplayName);
    if (displayName === null) return { ok: false, error: 'invalid' };

    const address = `${localPart.value}@${addressDomain}`;
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

  listTemplates(organizationId: number): MailTemplateSummary[] {
    return [...this.templatesOf(organizationId).values()]
      .sort((a, b) => (a.name < b.name ? -1 : a.name > b.name ? 1 : a.id - b.id))
      .map(template => ({
        id: template.id,
        name: template.name,
        subject: template.subject,
        variables: template.variables,
        created_at: template.created_at,
        updated_at: template.updated_at,
      }));
  }

  /** True when the organization has the mailbox. */
  hasMailbox(organizationId: number, mailboxId: number): boolean {
    return this.mailboxesOf(organizationId).has(mailboxId);
  }

  getTemplate(organizationId: number, templateId: number): MockMailTemplateResult {
    const template = this.templatesOf(organizationId).get(templateId);
    return template ? { ok: true, template } : { ok: false, error: 'not_found' };
  }

  createTemplate(organizationId: number, input: MailTemplateInput): MockMailTemplateResult {
    const templates = this.templatesOf(organizationId);
    const built = buildTemplate(input);
    if (!built) return { ok: false, error: 'invalid' };
    if ([...templates.values()].some(existing => existing.name === built.name)) {
      return { ok: false, error: 'taken' };
    }
    if (templates.size >= MAIL_TEMPLATE_MAX_PER_ORGANIZATION) return { ok: false, error: 'limit' };
    const now = this.timestamp();
    const template: MailTemplate = {
      id: this.nextTemplateId++,
      ...built,
      created_at: now,
      updated_at: now,
    };
    templates.set(template.id, template);
    return { ok: true, template };
  }

  updateTemplate(
    organizationId: number,
    templateId: number,
    input: MailTemplateInput
  ): MockMailTemplateResult {
    const templates = this.templatesOf(organizationId);
    const existing = templates.get(templateId);
    if (!existing) return { ok: false, error: 'not_found' };
    const built = buildTemplate(input);
    if (!built) return { ok: false, error: 'invalid' };
    if (
      [...templates.values()].some(other => other.id !== templateId && other.name === built.name)
    ) {
      return { ok: false, error: 'taken' };
    }
    const template: MailTemplate = { ...existing, ...built, updated_at: this.timestamp() };
    templates.set(templateId, template);
    return { ok: true, template };
  }

  deleteTemplate(organizationId: number, templateId: number): boolean {
    return this.templatesOf(organizationId).delete(templateId);
  }

  /** Sends a template with its sample values, overridden by the given ones. */
  sendTemplateTest(
    organizationId: number,
    templateId: number,
    mailboxId: number,
    to: readonly string[],
    variables: Readonly<Record<string, string>>
  ): MockMailSendResult {
    const template = this.templatesOf(organizationId).get(templateId);
    if (!template) return { ok: false, error: 'template_not_found' };
    if (to.length === 0 || to.length > 5) return { ok: false, error: 'invalid' };
    return this.send(organizationId, mailboxId, {
      to: [...to],
      template: { id: templateId, variables: { ...template.sample_variables, ...variables } },
    });
  }

  /** Validates like the API and stores the message as `sent`; the mock has no provider. */
  send(organizationId: number, mailboxId: number, input: SendMailMessageInput): MockMailSendResult {
    const state = this.mailboxesOf(organizationId).get(mailboxId);
    if (!state) return { ok: false, error: 'mailbox_not_found' };
    let draft: Parameters<typeof normalizeMailMessage>[0] = {
      ...input,
      subject: input.subject ?? '',
    };
    let templateId: number | null = null;
    if (input.template) {
      if (input.subject !== undefined || input.text !== undefined || input.html !== undefined) {
        return { ok: false, error: 'invalid' };
      }
      const template = this.templatesOf(organizationId).get(input.template.id);
      if (!template) return { ok: false, error: 'template_not_found' };
      const values = cleanValues(input.template.variables ?? {});
      if (!values) return { ok: false, error: 'invalid' };
      // A real send uses only the given values; sample values are for previews and tests.
      const rendered = renderTemplate(
        { subject: template.subject, html: template.html_body, text: template.text_body },
        values
      );
      if (!rendered) return { ok: false, error: 'invalid' };
      if (rendered.missing.length > 0) {
        return { ok: false, error: 'variables_missing', missing: rendered.missing };
      }
      draft = { ...draft, subject: rendered.subject, text: rendered.text, html: rendered.html };
      templateId = template.id;
    }
    const normalized = normalizeMailMessage(draft);
    if (!normalized) return { ok: false, error: 'invalid' };
    const drafts = input.attachments ?? [];
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
      template_id: templateId,
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
    this.nextTemplateId = 1;
    this.templates.clear();
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
      template_id: null,
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

  private templatesOf(organizationId: number): Map<number, MailTemplate> {
    const existing = this.templates.get(organizationId);
    if (existing) return existing;
    const created = new Map<number, MailTemplate>();
    this.templates.set(organizationId, created);
    return created;
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
    template_id: message.template_id,
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

/** The contract's template rules; null for content the API answers with `MAILBOX.TEMPLATE_INVALID`. */
function buildTemplate(
  input: MailTemplateInput
): Omit<MailTemplate, 'id' | 'created_at' | 'updated_at'> | null {
  const name = input.name.trim().split(/\s+/u).filter(Boolean).join(' ');
  const subject = input.subject.trim();
  const html = input.html ?? '';
  const text = input.text ?? '';
  if (name === '' || Array.from(name).length > MAIL_TEMPLATE_NAME_MAX_LENGTH) return null;
  if (subject === '' || subject.length > MAIL_SUBJECT_MAX_LENGTH || /[\r\n]/u.test(subject)) {
    return null;
  }
  if (html.trim() === '' && text.trim() === '') return null;
  if (html.length > MAIL_BODY_MAX_BYTES || text.length > MAIL_BODY_MAX_BYTES) return null;
  const samples = cleanValues(input.sample_variables ?? {});
  const variables = templateVariables({ subject, html, text });
  if (!samples || !variables) return null;
  return {
    name,
    subject,
    html_body: html,
    text_body: text,
    sample_variables: samples,
    variables,
  };
}

function cleanValues(values: Readonly<Record<string, string>>): Record<string, string> | null {
  const entries = Object.entries(values);
  if (entries.length > MAIL_TEMPLATE_MAX_VARIABLES) return null;
  for (const [name, value] of entries) {
    if (
      !MAIL_TEMPLATE_VARIABLE_PATTERN.test(name) ||
      typeof value !== 'string' ||
      Array.from(value).length > MAIL_TEMPLATE_VALUE_MAX_LENGTH
    ) {
      return null;
    }
  }
  return { ...values };
}
