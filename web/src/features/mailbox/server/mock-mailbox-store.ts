import 'server-only';

import {
  MAILBOX_MAX_MEMBERS,
  MAILBOX_MAX_PER_ORGANIZATION,
  MAILBOX_PERMISSIONS,
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
  CreateMailboxInput,
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
  MailboxAccess,
  MailboxAccessEvent,
  MailboxAccessEventPage,
  MailboxAccessEventType,
  MailboxDomain,
  MailboxMember,
  MailboxPermission,
  MailboxSealedDeletion,
  MailboxType,
  MailTemplate,
  MailTemplateInput,
  MailTemplateSummary,
  SendMailMessageInput,
} from '@/features/mailbox/types';
import { renderTemplate, templateVariables } from '@/features/mailbox/utils/template-render';
import { mockOrganizationStore } from '@/features/organization/server/mock-organization-store';
import type { OrganizationRole } from '@/features/organization/types';

/**
 * Development-only mailboxes, isolated per organization. It mirrors the browser-facing rules of
 * the Go mailbox starter: address validation and reserved names, addresses that stay reserved
 * after a mailbox is deleted, the per-organization mailbox limit, send validation, newest-first
 * listings without bodies, the cross-mailbox log, and per-day counts. Access follows
 * `contracts/MAILBOXES.md`: personal and shared mailboxes, member permissions, 404 without any
 * access and 403 without the needed one, per-person read state, handover with a seal boundary,
 * the webhook switch, and the access history. Organization membership comes from the
 * organization mock; a departure is noticed on the next access to the mailbox. Nothing leaves the
 * process: a "sent" message is only stored, which is what the `log` provider means.
 */
export const MOCK_MAILBOX_DOMAIN = 'mail.example.com';
export const MOCK_DAILY_SEND_LIMIT = 200;

const dayMilliseconds = 24 * 60 * 60 * 1_000;

const maxMockOrganizations = 1_000;
const maxMockMessagesPerMailbox = 500;
const maxMockAccessEventsPerMailbox = 1_000;

/** Who acts: the user and their role in the organization of the request. */
export interface MailboxViewer {
  userId: number;
  role: OrganizationRole;
}

interface MailboxAccessState {
  type: MailboxType;
  ownerUserId: number | null;
  /** Set while a personal mailbox's owner is not a member of the organization. */
  ownerLeft: { at: string; throughMessageId: number } | null;
  sealedBefore: string | null;
  /**
   * Messages with an id up to this one were created before the seal. The mock seals by id, so a
   * message stored in the same millisecond as the handover is still sealed.
   */
  sealedThroughMessageId: number;
  webhooksEnabled: boolean;
  members: Map<number, MailboxMember>;
  events: MailboxAccessEvent[];
}

interface StoredMailbox {
  id: number;
  address: string;
  display_name: string;
  created_at: string;
}

interface MailboxState {
  mailbox: StoredMailbox;
  access: MailboxAccessState;
  messages: Map<number, MailMessage>;
  /** Who read each inbound message, by user id. */
  reads: Map<number, Set<number>>;
  /** File content by message, then by attachment id. */
  files: Map<number, Map<number, { filename: string; content: Uint8Array }>>;
}

export type MockMailboxCreateResult =
  | { ok: true; mailbox: Mailbox }
  | { ok: false; error: 'invalid' | 'reserved' | 'taken' | 'limit' | 'access_invalid' };

/** No access at all reads as a missing mailbox; some access but not the needed one is `forbidden`. */
type MockAccessDenied = 'mailbox_not_found' | 'forbidden';

export type MockMailMessageResult =
  { ok: true; message: MailMessage } | { ok: false; error: MockAccessDenied | 'not_found' };

export type MockMailMessagePageResult =
  { ok: true; page: MailMessagePage } | { ok: false; error: MockAccessDenied };

export type MockMailSendResult =
  | { ok: true; message: MailMessage }
  | { ok: false; error: MockAccessDenied | 'invalid' | 'template_not_found' }
  | { ok: false; error: 'variables_missing'; missing: string[] };

export type MockMailTemplateResult =
  | { ok: true; template: MailTemplate }
  | { ok: false; error: 'not_found' | 'invalid' | 'taken' | 'limit' };

export type MockMailAttachmentResult =
  | { ok: true; filename: string; content: Uint8Array }
  | { ok: false; error: MockAccessDenied | 'not_found' };

export type MockMailDeleteResult =
  { ok: true } | { ok: false; error: MockAccessDenied | 'not_found' };

/**
 * Failures of the access routes: `conflict` is `MAILBOX.ACCESS_CONFLICT`, `invalid` is
 * `MAILBOX.ACCESS_INVALID`, and `member_not_found` an absent member (`MAILBOX.NOT_FOUND`).
 */
export type MockAccessError = MockAccessDenied | 'conflict' | 'invalid' | 'member_not_found';
export type MockAccessResult<T> = { ok: true; value: T } | { ok: false; error: MockAccessError };

interface MockMailboxStoreDependencies {
  now: () => Date;
  /** The membership id of a current member of the organization, or null. */
  membershipOf: (organizationId: number, userId: number) => number | null;
}

const defaultDependencies: MockMailboxStoreDependencies = {
  now: () => new Date(),
  membershipOf: (organizationId, userId) =>
    mockOrganizationStore.membershipIdOf(organizationId, userId),
};

/** What a viewer may do with a mailbox: the union of role, personal ownership, and member row. */
export function mailboxAccessFor(
  access: Pick<MailboxAccessState, 'type' | 'ownerUserId' | 'members'>,
  viewer: MailboxViewer
): MailboxAccess {
  const admin = viewer.role === 'owner' || viewer.role === 'admin';
  const result: MailboxAccess = {
    view: false,
    send: false,
    manage_members: admin && access.type === 'shared',
    administer: admin,
    see_history: admin,
  };
  if (access.type === 'personal' && access.ownerUserId === viewer.userId) {
    result.view = true;
    result.send = true;
    result.see_history = true;
  }
  const member = access.type === 'shared' ? access.members.get(viewer.userId) : undefined;
  if (member) {
    result.view = true;
    if (member.permission !== 'view') result.send = true;
    if (member.permission === 'manage') {
      result.manage_members = true;
      result.see_history = true;
    }
  }
  return result;
}

function anyAccess(access: MailboxAccess): boolean {
  return Object.values(access).some(Boolean);
}

function isPermission(value: unknown): value is MailboxPermission {
  return (MAILBOX_PERMISSIONS as readonly unknown[]).includes(value);
}

export class MockMailboxStore {
  private nextMailboxId = 1;
  private nextMessageId = 1;
  private nextAttachmentId = 1;
  private nextTemplateId = 1;
  private nextAccessEventId = 1;
  private templates = new Map<number, Map<number, MailTemplate>>();
  private organizations = new Map<number, Map<number, MailboxState>>();
  /** Every address that is or ever was a mailbox, across all organizations. */
  private claimedAddresses = new Set<string>();
  private readonly dependencies: MockMailboxStoreDependencies;

  constructor(dependencies: Partial<MockMailboxStoreDependencies> = {}) {
    this.dependencies = { ...defaultDependencies, ...dependencies };
  }

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

  /** Mailboxes the viewer has any access to, oldest first, as the API lists them. */
  list(organizationId: number, viewer: MailboxViewer): Mailbox[] {
    return this.statesOf(organizationId)
      .flatMap(state => {
        const access = mailboxAccessFor(state.access, viewer);
        return anyAccess(access) ? [this.view(state, viewer, access)] : [];
      })
      .sort((left, right) => left.id - right.id);
  }

  /** `addressDomain` is the shared domain or a verified domain the route already resolved. */
  create(
    organizationId: number,
    viewer: MailboxViewer,
    input: Omit<CreateMailboxInput, 'type'> & { type?: unknown },
    addressDomain: string = MOCK_MAILBOX_DOMAIN
  ): MockMailboxCreateResult {
    const localPart = normalizeLocalPart(input.local_part);
    if (!localPart.ok) return { ok: false, error: localPart.reason };
    const displayName = normalizeDisplayName(input.display_name ?? '');
    if (displayName === null) return { ok: false, error: 'invalid' };
    const access = this.initialAccess(organizationId, input);
    if (!access) return { ok: false, error: 'access_invalid' };

    const address = `${localPart.value}@${addressDomain}`;
    // The API checks the organization's limit before the address, so an organization at the
    // limit learns about the limit even when the address is also taken.
    const mailboxes = this.mailboxesOf(organizationId);
    if (mailboxes.size >= MAILBOX_MAX_PER_ORGANIZATION) return { ok: false, error: 'limit' };
    if (this.claimedAddresses.has(address)) return { ok: false, error: 'taken' };

    const mailbox: StoredMailbox = {
      id: this.nextMailboxId++,
      address,
      display_name: displayName,
      created_at: this.timestamp(),
    };
    const state: MailboxState = {
      mailbox,
      access,
      messages: new Map(),
      reads: new Map(),
      files: new Map(),
    };
    for (const member of access.members.values()) member.granted_by = viewer.userId;
    this.record(state, 'created', viewer.userId, access.ownerUserId, { type: access.type });
    for (const member of access.members.values()) {
      this.record(state, 'member_set', viewer.userId, member.user_id, {
        membership_id: this.dependencies.membershipOf(organizationId, member.user_id),
        permission: member.permission,
      });
    }
    mailboxes.set(mailbox.id, state);
    this.claimedAddresses.add(address);
    this.store(state, this.welcomeMessage(mailbox));
    // As in the API, a create response reports 0 unread.
    return { ok: true, mailbox: { ...this.view(state, viewer), unread_count: 0 } };
  }

  /** Deletes the mailbox and its messages; the address stays claimed. Needs `administer`. */
  delete(organizationId: number, mailboxId: number, viewer: MailboxViewer): MockMailDeleteResult {
    const found = this.authorize(organizationId, mailboxId, viewer, 'administer');
    if (!found.ok) return found;
    this.mailboxesOf(organizationId).delete(mailboxId);
    return { ok: true };
  }

  listMessages(
    organizationId: number,
    mailboxId: number,
    viewer: MailboxViewer,
    direction: MailDirection | null,
    page: number,
    perPage: number
  ): MockMailMessagePageResult {
    const found = this.authorize(organizationId, mailboxId, viewer, 'view');
    if (!found.ok) return found;
    const all = this.visibleMessages(found.state)
      .filter(message => direction === null || message.direction === direction)
      .sort((left, right) => right.id - left.id)
      .map(message => summaryOf(this.forViewer(found.state, message, viewer)));
    const filter = direction === null ? '' : `direction=${direction}&`;
    return {
      ok: true,
      page: paginated(all, page, perPage, `/api/mailboxes/${mailboxId}/messages?${filter}`),
    };
  }

  /**
   * The mailboxes the viewer reads, newest first, without bodies or sealed mail. The search is a
   * case-insensitive substring match on the subject, the sender address, and each recipient
   * address, so `%` and `_` only match themselves. Another organization's mailbox matches nothing.
   */
  listLog(
    organizationId: number,
    viewer: MailboxViewer,
    filters: MailLogFilters,
    page: number,
    perPage: number
  ): MailLogPage {
    const needle = filters.q?.trim().toLowerCase() ?? '';
    const entries: MailLogEntry[] = [];
    for (const state of this.readableStates(organizationId, viewer)) {
      if (filters.mailboxId !== undefined && state.mailbox.id !== filters.mailboxId) continue;
      for (const message of this.visibleMessages(state)) {
        if (filters.direction && message.direction !== filters.direction) continue;
        if (filters.status && message.status !== filters.status) continue;
        if (needle !== '' && !matchesSearch(message, needle)) continue;
        entries.push({
          ...summaryOf(this.forViewer(state, message, viewer)),
          mailbox_address: state.mailbox.address,
        });
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
   * included, over the mailboxes the viewer reads. `sent` and `failed` count outbound messages by
   * final status; `received` counts inbound messages. Sealed mail is left out.
   */
  stats(organizationId: number, viewer: MailboxViewer, days: number): MailDayStats[] {
    const now = this.dependencies.now();
    const today = Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), now.getUTCDate());
    const entries = Array.from({ length: days }, (_, index): MailDayStats => ({
      day: dayOf(today - (days - 1 - index) * dayMilliseconds),
      sent: 0,
      failed: 0,
      received: 0,
    }));
    const byDay = new Map(entries.map(entry => [entry.day, entry]));
    for (const state of this.readableStates(organizationId, viewer)) {
      for (const message of this.visibleMessages(state)) {
        const entry = byDay.get(message.created_at.slice(0, 10));
        if (!entry) continue;
        if (message.direction === 'inbound') entry.received += 1;
        else if (message.status === 'sent') entry.sent += 1;
        else if (message.status === 'failed') entry.failed += 1;
      }
    }
    return entries;
  }

  getMessage(
    organizationId: number,
    mailboxId: number,
    messageId: number,
    viewer: MailboxViewer
  ): MockMailMessageResult {
    const found = this.authorize(organizationId, mailboxId, viewer, 'view');
    if (!found.ok) return found;
    const message = this.visibleMessage(found.state, messageId);
    return message
      ? { ok: true, message: this.forViewer(found.state, message, viewer) }
      : { ok: false, error: 'not_found' };
  }

  /** Changes only the viewer's read state; outbound messages stay read for everyone. */
  setRead(
    organizationId: number,
    mailboxId: number,
    messageId: number,
    viewer: MailboxViewer,
    read: boolean
  ): MockMailMessageResult {
    const found = this.authorize(organizationId, mailboxId, viewer, 'view');
    if (!found.ok) return found;
    const message = this.visibleMessage(found.state, messageId);
    if (!message) return { ok: false, error: 'not_found' };
    if (message.direction === 'inbound') {
      const readers = found.state.reads.get(messageId) ?? new Set<number>();
      if (read) readers.add(viewer.userId);
      else readers.delete(viewer.userId);
      found.state.reads.set(messageId, readers);
    }
    return { ok: true, message: this.forViewer(found.state, message, viewer) };
  }

  deleteMessage(
    organizationId: number,
    mailboxId: number,
    messageId: number,
    viewer: MailboxViewer
  ): MockMailDeleteResult {
    const found = this.authorize(organizationId, mailboxId, viewer, 'send');
    if (!found.ok) return found;
    if (!this.visibleMessage(found.state, messageId)) return { ok: false, error: 'not_found' };
    this.forget(found.state, messageId);
    return { ok: true };
  }

  attachment(
    organizationId: number,
    mailboxId: number,
    messageId: number,
    attachmentId: number,
    viewer: MailboxViewer
  ): MockMailAttachmentResult {
    const found = this.authorize(organizationId, mailboxId, viewer, 'view');
    if (!found.ok) return found;
    if (!this.visibleMessage(found.state, messageId)) return { ok: false, error: 'not_found' };
    const file = found.state.files.get(messageId)?.get(attachmentId);
    return file ? { ok: true, ...file } : { ok: false, error: 'not_found' };
  }

  /** Members of a shared mailbox, ordered by user id. */
  members(
    organizationId: number,
    mailboxId: number,
    viewer: MailboxViewer
  ): MockAccessResult<MailboxMember[]> {
    const found = this.authorizeMembers(organizationId, mailboxId, viewer);
    if (!found.ok) return found;
    return { ok: true, value: sortedMembers(found.state) };
  }

  /** Adds the user or changes their permission. */
  setMember(
    organizationId: number,
    mailboxId: number,
    viewer: MailboxViewer,
    userId: number,
    permission: unknown
  ): MockAccessResult<MailboxMember> {
    const found = this.authorizeMembers(organizationId, mailboxId, viewer);
    if (!found.ok) return found;
    const membershipId = this.dependencies.membershipOf(organizationId, userId);
    if (!isPermission(permission) || membershipId === null) {
      return { ok: false, error: 'invalid' };
    }
    const { members } = found.state.access;
    const existing = members.get(userId);
    if (!existing && members.size >= MAILBOX_MAX_MEMBERS) return { ok: false, error: 'invalid' };
    const member: MailboxMember = {
      user_id: userId,
      permission,
      granted_by: viewer.userId,
      created_at: existing?.created_at ?? this.timestamp(),
    };
    members.set(userId, member);
    this.record(found.state, 'member_set', viewer.userId, userId, {
      membership_id: membershipId,
      permission,
    });
    return { ok: true, value: { ...member } };
  }

  /** Removes a member together with their read state in the mailbox. */
  removeMember(
    organizationId: number,
    mailboxId: number,
    viewer: MailboxViewer,
    userId: number
  ): MockAccessResult<null> {
    const found = this.authorizeMembers(organizationId, mailboxId, viewer);
    if (!found.ok) return found;
    if (!found.state.access.members.delete(userId)) {
      return { ok: false, error: 'member_not_found' };
    }
    forgetReads(found.state, userId);
    this.record(found.state, 'member_removed', viewer.userId, userId, {});
    return { ok: true, value: null };
  }

  /** The access history, newest first. Needs `see_history`. */
  accessEvents(
    organizationId: number,
    mailboxId: number,
    viewer: MailboxViewer,
    page: number,
    perPage: number
  ): MockAccessResult<MailboxAccessEventPage> {
    const found = this.authorize(organizationId, mailboxId, viewer, 'see_history');
    if (!found.ok) return found;
    const events = [...found.state.access.events].sort((left, right) => right.id - left.id);
    return {
      ok: true,
      value: paginated(events, page, perPage, `/api/mailboxes/${mailboxId}/access-events?`),
    };
  }

  /**
   * Turns a personal mailbox into a shared one the successor works with `send`. Mail from before
   * the owner left, or before now while the owner is still a member, is sealed for good.
   */
  handOver(
    organizationId: number,
    mailboxId: number,
    viewer: MailboxViewer,
    successorUserId: number
  ): MockAccessResult<Mailbox> {
    const found = this.authorize(organizationId, mailboxId, viewer, 'administer');
    if (!found.ok) return found;
    if (this.dependencies.membershipOf(organizationId, successorUserId) === null) {
      return { ok: false, error: 'invalid' };
    }
    const { access } = found.state;
    if (access.type !== 'personal') return { ok: false, error: 'conflict' };

    const formerOwner = access.ownerUserId;
    const boundary = access.ownerLeft ?? {
      at: this.timestamp(),
      throughMessageId: this.nextMessageId - 1,
    };
    access.type = 'shared';
    access.ownerUserId = null;
    access.ownerLeft = null;
    access.sealedBefore = boundary.at;
    access.sealedThroughMessageId = boundary.throughMessageId;
    if (formerOwner !== null) forgetReads(found.state, formerOwner);
    access.members.set(successorUserId, {
      user_id: successorUserId,
      permission: 'send',
      granted_by: viewer.userId,
      created_at: this.timestamp(),
    });
    this.record(found.state, 'handed_over', viewer.userId, successorUserId, {
      former_owner_user_id: formerOwner,
      sealed_before: boundary.at,
    });
    return { ok: true, value: this.view(found.state, viewer) };
  }

  /** Deletes the sealed mail for good; the boundary stays. The mock has no sealed broadcasts. */
  deleteSealedHistory(
    organizationId: number,
    mailboxId: number,
    viewer: MailboxViewer,
    confirmAddress: string
  ): MockAccessResult<MailboxSealedDeletion> {
    const found = this.authorize(organizationId, mailboxId, viewer, 'administer');
    if (!found.ok) return found;
    const { state } = found;
    if (state.access.sealedBefore === null) return { ok: false, error: 'conflict' };
    if (confirmAddress.trim().toLowerCase() !== state.mailbox.address.toLowerCase()) {
      return { ok: false, error: 'invalid' };
    }
    let deleted = 0;
    for (const messageId of [...state.messages.keys()]) {
      if (messageId <= state.access.sealedThroughMessageId) {
        this.forget(state, messageId);
        deleted += 1;
      }
    }
    const counts = { deleted_messages: deleted, deleted_broadcasts: 0 };
    this.record(state, 'sealed_deleted', viewer.userId, null, counts);
    return { ok: true, value: counts };
  }

  /** Turns organization webhooks of a shared mailbox on or off; only a change is recorded. */
  setWebhooks(
    organizationId: number,
    mailboxId: number,
    viewer: MailboxViewer,
    enabled: boolean
  ): MockAccessResult<Mailbox> {
    const found = this.authorizeMembers(organizationId, mailboxId, viewer);
    if (!found.ok) return found;
    if (found.state.access.webhooksEnabled !== enabled) {
      found.state.access.webhooksEnabled = enabled;
      this.record(found.state, 'webhooks_changed', viewer.userId, null, { enabled });
    }
    return { ok: true, value: this.view(found.state, viewer) };
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
    variables: Readonly<Record<string, string>>,
    viewer: MailboxViewer
  ): MockMailSendResult {
    const template = this.templatesOf(organizationId).get(templateId);
    if (!template) return { ok: false, error: 'template_not_found' };
    if (to.length === 0 || to.length > 5) return { ok: false, error: 'invalid' };
    return this.send(
      organizationId,
      mailboxId,
      {
        to: [...to],
        template: { id: templateId, variables: { ...template.sample_variables, ...variables } },
      },
      viewer
    );
  }

  /**
   * Validates like the API and stores the message as `sent`; the mock has no provider. A member
   * needs `send`; without `viewer` (the broadcast worker) access is not checked again.
   */
  send(
    organizationId: number,
    mailboxId: number,
    input: SendMailMessageInput,
    viewer?: MailboxViewer
  ): MockMailSendResult {
    let state: MailboxState | undefined;
    if (viewer) {
      const found = this.authorize(organizationId, mailboxId, viewer, 'send');
      if (!found.ok) return found;
      state = found.state;
    } else {
      state = this.stateOf(organizationId, mailboxId);
    }
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
      queued_reason: null,
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
    this.nextAccessEventId = 1;
    this.templates.clear();
    this.organizations.clear();
    this.claimedAddresses.clear();
  }

  private store(state: MailboxState, message: MailMessage): void {
    if (state.messages.size >= maxMockMessagesPerMailbox) {
      const oldest = state.messages.keys().next().value;
      if (oldest !== undefined) this.forget(state, oldest);
    }
    state.messages.set(message.id, message);
  }

  private forget(state: MailboxState, messageId: number): void {
    state.messages.delete(messageId);
    state.files.delete(messageId);
    state.reads.delete(messageId);
  }

  /** The access of a new mailbox, or null for `MAILBOX.ACCESS_INVALID`. */
  private initialAccess(
    organizationId: number,
    input: { type?: unknown; owner_user_id?: unknown; members?: unknown }
  ): MailboxAccessState | null {
    const isCurrentMember = (userId: unknown): userId is number =>
      typeof userId === 'number' && this.dependencies.membershipOf(organizationId, userId) !== null;
    const base = {
      ownerLeft: null,
      sealedBefore: null,
      sealedThroughMessageId: 0,
      webhooksEnabled: false,
      events: [],
    };
    if (input.type === 'personal') {
      if (input.members !== undefined || !isCurrentMember(input.owner_user_id)) return null;
      return { ...base, type: 'personal', ownerUserId: input.owner_user_id, members: new Map() };
    }
    if (input.type !== 'shared' || input.owner_user_id !== undefined) return null;
    const list = input.members;
    if (!Array.isArray(list) || list.length === 0 || list.length > MAILBOX_MAX_MEMBERS) {
      return null;
    }
    const members = new Map<number, MailboxMember>();
    for (const item of list as { user_id?: unknown; permission?: unknown }[]) {
      const userId = item.user_id;
      if (!isCurrentMember(userId) || !isPermission(item.permission) || members.has(userId)) {
        return null;
      }
      // `granted_by` is filled with the creator once the mailbox is stored.
      members.set(userId, {
        user_id: userId,
        permission: item.permission,
        granted_by: userId,
        created_at: this.timestamp(),
      });
    }
    return { ...base, type: 'shared', ownerUserId: null, members };
  }

  private record(
    state: MailboxState,
    type: MailboxAccessEventType,
    actorUserId: number | null,
    subjectUserId: number | null,
    detail: Record<string, unknown>
  ): void {
    const { events } = state.access;
    if (events.length >= maxMockAccessEventsPerMailbox) events.shift();
    events.push({
      id: this.nextAccessEventId++,
      type,
      actor_user_id: actorUserId,
      subject_user_id: subjectUserId,
      detail,
      created_at: this.timestamp(),
    });
  }

  /**
   * Applies departures and returns noticed since the last access, as the API's membership events
   * would: a member who left loses their row and read state; a personal owner who left leaves the
   * mailbox awaiting handover until they rejoin.
   */
  private sync(organizationId: number, state: MailboxState): MailboxState {
    const { access } = state;
    const isMember = (userId: number) =>
      this.dependencies.membershipOf(organizationId, userId) !== null;
    for (const userId of [...access.members.keys()]) {
      if (isMember(userId)) continue;
      access.members.delete(userId);
      forgetReads(state, userId);
      this.record(state, 'member_removed', null, userId, { reason: 'left_organization' });
    }
    if (access.type === 'personal' && access.ownerUserId !== null) {
      const owner = access.ownerUserId;
      if (!isMember(owner) && !access.ownerLeft) {
        access.ownerLeft = { at: this.timestamp(), throughMessageId: this.nextMessageId - 1 };
        forgetReads(state, owner);
        this.record(state, 'owner_left', null, owner, { reason: 'left_organization' });
      } else if (isMember(owner) && access.ownerLeft) {
        access.ownerLeft = null;
        this.record(state, 'owner_returned', null, owner, {});
      }
    }
    return state;
  }

  private stateOf(organizationId: number, mailboxId: number): MailboxState | undefined {
    const state = this.mailboxesOf(organizationId).get(mailboxId);
    return state ? this.sync(organizationId, state) : undefined;
  }

  private statesOf(organizationId: number): MailboxState[] {
    return [...this.mailboxesOf(organizationId).values()].map(state =>
      this.sync(organizationId, state)
    );
  }

  private readableStates(organizationId: number, viewer: MailboxViewer): MailboxState[] {
    return this.statesOf(organizationId).filter(
      state => mailboxAccessFor(state.access, viewer).view
    );
  }

  /** 404 without any access, 403 without the needed flag. */
  private authorize(
    organizationId: number,
    mailboxId: number,
    viewer: MailboxViewer,
    needs: keyof MailboxAccess
  ):
    | { ok: true; state: MailboxState; access: MailboxAccess }
    | { ok: false; error: MockAccessDenied } {
    const state = this.stateOf(organizationId, mailboxId);
    const access = state ? mailboxAccessFor(state.access, viewer) : null;
    if (!state || !access || !anyAccess(access)) return { ok: false, error: 'mailbox_not_found' };
    if (!access[needs]) return { ok: false, error: 'forbidden' };
    return { ok: true, state, access };
  }

  /** Member routes and the webhook switch: a personal mailbox conflicts before the capability. */
  private authorizeMembers(
    organizationId: number,
    mailboxId: number,
    viewer: MailboxViewer
  ): { ok: true; state: MailboxState } | { ok: false; error: MockAccessError } {
    const state = this.stateOf(organizationId, mailboxId);
    const access = state ? mailboxAccessFor(state.access, viewer) : null;
    if (!state || !access || !anyAccess(access)) return { ok: false, error: 'mailbox_not_found' };
    if (state.access.type !== 'shared') return { ok: false, error: 'conflict' };
    if (!access.manage_members) return { ok: false, error: 'forbidden' };
    return { ok: true, state };
  }

  private visibleMessages(state: MailboxState): MailMessage[] {
    return [...state.messages.values()].filter(
      message => message.id > state.access.sealedThroughMessageId
    );
  }

  private visibleMessage(state: MailboxState, messageId: number): MailMessage | undefined {
    return messageId > state.access.sealedThroughMessageId
      ? state.messages.get(messageId)
      : undefined;
  }

  /** The viewer's own read flag; outbound messages are read for everyone. */
  private forViewer(state: MailboxState, message: MailMessage, viewer: MailboxViewer): MailMessage {
    const read =
      message.direction === 'outbound' ||
      (state.reads.get(message.id)?.has(viewer.userId) ?? false);
    return { ...message, read };
  }

  private view(
    state: MailboxState,
    viewer: MailboxViewer,
    access: MailboxAccess = mailboxAccessFor(state.access, viewer)
  ): Mailbox {
    let unread = 0;
    if (access.view) {
      for (const message of this.visibleMessages(state)) {
        if (!this.forViewer(state, message, viewer).read) unread += 1;
      }
    }
    return {
      ...state.mailbox,
      type: state.access.type,
      owner_user_id: state.access.ownerUserId,
      awaiting_handover: state.access.type === 'personal' && state.access.ownerLeft !== null,
      sealed_before: state.access.sealedBefore,
      webhooks_enabled: state.access.webhooksEnabled,
      access,
      unread_count: unread,
    };
  }

  /** One unread inbound message, so a new mock inbox shows the read flow and the HTML sandbox. */
  private welcomeMessage(mailbox: StoredMailbox): MailMessage {
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
      queued_reason: null,
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
    queued_reason: message.queued_reason,
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

function sortedMembers(state: MailboxState): MailboxMember[] {
  return [...state.access.members.values()]
    .sort((left, right) => left.user_id - right.user_id)
    .map(member => ({ ...member }));
}

function forgetReads(state: MailboxState, userId: number): void {
  for (const readers of state.reads.values()) readers.delete(userId);
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
    design: input.design ?? null,
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
