import { MAIL_TRASH_RETENTION_DAYS, MAILBOX_MAX_MEMBERS } from '@/features/mailbox/schemas';
import type {
  MailMessage,
  MailPlacement,
  MailTemplate,
  Mailbox,
  MailboxAccess,
  MailboxAccessEventType,
  MailboxDirectoryEntry,
  MailboxMember,
} from '@/features/mailbox/types';
import { mockOrganizationStore } from '@/features/organization/server/mock-organization-store';
import {
  anyAccess,
  awaitsHandover,
  forgetReads,
  isPermission,
  mailboxAccessFor,
} from '@/features/mailbox/server/mock-store/helpers';
import {
  DAY_MILLISECONDS,
  MAX_MOCK_ACCESS_EVENTS_PER_MAILBOX,
  MAX_MOCK_MESSAGES_PER_MAILBOX,
  MAX_MOCK_ORGANIZATIONS,
  MOCK_MAILBOX_DOMAIN,
  type MailboxAccessState,
  type MailboxState,
  type MailboxViewer,
  type MockAccessDenied,
  type MockAccessError,
  type MockMailboxStoreDependencies,
  type StoredMailbox,
  type StoredRole,
} from '@/features/mailbox/server/mock-store/types';

const defaultDependencies: MockMailboxStoreDependencies = {
  now: () => new Date(),
  membershipOf: (organizationId, userId) =>
    mockOrganizationStore.membershipIdOf(organizationId, userId),
  memberEmails: organizationId => mockOrganizationStore.memberEmails(organizationId),
  rosterInviter: mockOrganizationStore,
};

/**
 * Shared mutable state and low-level access/lifecycle primitives for the mock mailbox store
 * subdomains.
 */
export class MockMailboxState {
  nextMailboxId = 1;
  nextMessageId = 1;
  nextAttachmentId = 1;
  nextTemplateId = 1;
  nextAccessEventId = 1;
  nextFolderId = 1;
  nextLabelId = 1;
  nextRoleId = 1;
  roles = new Map<number, Map<number, StoredRole>>();
  templates = new Map<number, Map<number, MailTemplate>>();
  organizations = new Map<number, Map<number, MailboxState>>();
  /** Every address that is or ever was a mailbox, across all organizations. */
  claimedAddresses = new Set<string>();
  readonly dependencies: MockMailboxStoreDependencies;

  constructor(dependencies: Partial<MockMailboxStoreDependencies> = {}) {
    this.dependencies = { ...defaultDependencies, ...dependencies };
  }

  /** Test seam: forget every mailbox, message, and claimed address. */
  reset(): void {
    this.nextMailboxId = 1;
    this.nextMessageId = 1;
    this.nextAttachmentId = 1;
    this.nextTemplateId = 1;
    this.nextAccessEventId = 1;
    this.nextFolderId = 1;
    this.nextLabelId = 1;
    this.nextRoleId = 1;
    this.roles.clear();
    this.templates.clear();
    this.organizations.clear();
    this.claimedAddresses.clear();
  }

  timestamp(): string {
    return this.dependencies.now().toISOString();
  }

  store(state: MailboxState, message: MailMessage): void {
    if (state.messages.size >= MAX_MOCK_MESSAGES_PER_MAILBOX) {
      const oldest = state.messages.keys().next().value;
      if (oldest !== undefined) this.forget(state, oldest);
    }
    state.messages.set(message.id, message);
  }

  forget(state: MailboxState, messageId: number): void {
    state.messages.delete(messageId);
    state.files.delete(messageId);
    state.reads.delete(messageId);
    state.stars.delete(messageId);
  }

  /** The message in another placement; `trashed_at` is kept only while it stays in the trash. */
  placed(message: MailMessage, placement: MailPlacement, folderId: number | null): MailMessage {
    const trashedAt = placement !== 'trash' ? null : (message.trashed_at ?? this.timestamp());
    return {
      ...message,
      placement,
      folder_id: placement === 'folder' ? folderId : null,
      trashed_at: trashedAt,
    };
  }

  /** The access of a new mailbox, or null for `MAILBOX.ACCESS_INVALID`. */
  initialAccess(
    organizationId: number,
    input: { type?: unknown; owner_user_id?: unknown; members?: unknown }
  ): MailboxAccessState | null {
    const isCurrentMember = (userId: unknown): userId is number =>
      typeof userId === 'number' && this.dependencies.membershipOf(organizationId, userId) !== null;
    const base = {
      origin: 'zmail' as const,
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

  record(
    state: MailboxState,
    type: MailboxAccessEventType,
    actorUserId: number | null,
    subjectUserId: number | null,
    detail: Record<string, unknown>
  ): void {
    const { events } = state.access;
    if (events.length >= MAX_MOCK_ACCESS_EVENTS_PER_MAILBOX) events.shift();
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
  sync(organizationId: number, state: MailboxState): MailboxState {
    const { access } = state;
    // The API's worker deletes trashed mail for good after 30 days; the mock does it on access.
    const expiry = this.dependencies.now().getTime() - MAIL_TRASH_RETENTION_DAYS * DAY_MILLISECONDS;
    for (const message of [...state.messages.values()]) {
      if (message.trashed_at !== null && Date.parse(message.trashed_at) <= expiry) {
        this.forget(state, message.id);
      }
    }
    const isMember = (userId: number) =>
      this.dependencies.membershipOf(organizationId, userId) !== null;
    for (const userId of [...access.members.keys()]) {
      if (isMember(userId)) continue;
      access.members.delete(userId);
      forgetReads(state, userId);
      this.record(state, 'member_removed', null, userId, { reason: 'left_organization' });
    }
    // A connected personal mailbox is the owner's own account; it never awaits a handover.
    if (access.type === 'personal' && access.origin === 'zmail' && access.ownerUserId !== null) {
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

  stateOf(organizationId: number, mailboxId: number): MailboxState | undefined {
    const state = this.mailboxesOf(organizationId).get(mailboxId);
    return state ? this.sync(organizationId, state) : undefined;
  }

  statesOf(organizationId: number): MailboxState[] {
    return [...this.mailboxesOf(organizationId).values()].map(state =>
      this.sync(organizationId, state)
    );
  }

  readableStates(organizationId: number, viewer: MailboxViewer): MailboxState[] {
    return this.statesOf(organizationId).filter(
      state => mailboxAccessFor(state.access, viewer).view
    );
  }

  /** 404 without any access, 403 without the needed flag. */
  authorize(
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
  authorizeMembers(
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

  visibleMessages(state: MailboxState): MailMessage[] {
    return [...state.messages.values()].filter(
      message => message.id > state.access.sealedThroughMessageId
    );
  }

  visibleMessage(state: MailboxState, messageId: number): MailMessage | undefined {
    return messageId > state.access.sealedThroughMessageId
      ? state.messages.get(messageId)
      : undefined;
  }

  /** The viewer's own read flag and star; outbound messages are read for everyone. */
  forViewer(state: MailboxState, message: MailMessage, viewer: MailboxViewer): MailMessage {
    const read =
      message.direction === 'outbound' ||
      (state.reads.get(message.id)?.has(viewer.userId) ?? false);
    const starred = state.stars.get(message.id)?.has(viewer.userId) ?? false;
    return { ...message, read, starred };
  }

  view(
    state: MailboxState,
    viewer: MailboxViewer,
    access: MailboxAccess = mailboxAccessFor(state.access, viewer)
  ): Mailbox {
    // Only the Inbox counts: inbound mail in `default` the viewer has not read.
    let unread = 0;
    if (access.view) {
      for (const message of this.visibleMessages(state)) {
        if (message.placement !== 'default') continue;
        if (!this.forViewer(state, message, viewer).read) unread += 1;
      }
    }
    return {
      ...state.mailbox,
      type: state.access.type,
      origin: state.access.origin,
      owner_user_id: state.access.ownerUserId,
      awaiting_handover: awaitsHandover(state.access),
      sealed_before: state.access.sealedBefore,
      webhooks_enabled: state.access.webhooksEnabled,
      access,
      unread_count: unread,
      role_id: state.roleId,
    };
  }

  /** One unread inbound message, so a new mock inbox shows the read flow and the HTML sandbox. */
  welcomeMessage(mailbox: StoredMailbox): MailMessage {
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
      placement: 'default',
      folder_id: null,
      label_ids: [],
      starred: false,
      thread_id: id,
      trashed_at: null,
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

  rolesOf(organizationId: number): Map<number, StoredRole> {
    const existing = this.roles.get(organizationId);
    if (existing) return existing;
    if (this.roles.size >= MAX_MOCK_ORGANIZATIONS) {
      const oldest = this.roles.keys().next().value;
      if (oldest !== undefined) this.roles.delete(oldest);
    }
    const created = new Map<number, StoredRole>();
    this.roles.set(organizationId, created);
    return created;
  }

  templatesOf(organizationId: number): Map<number, MailTemplate> {
    const existing = this.templates.get(organizationId);
    if (existing) return existing;
    const created = new Map<number, MailTemplate>();
    this.templates.set(organizationId, created);
    return created;
  }

  mailboxesOf(organizationId: number): Map<number, MailboxState> {
    const existing = this.organizations.get(organizationId);
    if (existing) return existing;
    if (this.organizations.size >= MAX_MOCK_ORGANIZATIONS) {
      const oldest = this.organizations.keys().next().value;
      if (oldest !== undefined) this.organizations.delete(oldest);
    }
    const created = new Map<number, MailboxState>();
    this.organizations.set(organizationId, created);
    return created;
  }

  directoryEntry(state: MailboxState): MailboxDirectoryEntry {
    return {
      ...state.mailbox,
      type: state.access.type,
      owner_user_id: state.access.ownerUserId,
      awaiting_handover: awaitsHandover(state.access),
      member_count: state.access.type === 'shared' ? state.access.members.size : 0,
      sent_last_24h: this.sentLast24h(state),
    };
  }

  /** Outbound mail of the last 24 hours; the mock has one lane. */
  sentLast24h(state: MailboxState): number {
    const since = this.dependencies.now().getTime() - DAY_MILLISECONDS;
    let sent = 0;
    for (const message of state.messages.values()) {
      if (message.direction === 'outbound' && Date.parse(message.created_at) >= since) sent += 1;
    }
    return sent;
  }
}
