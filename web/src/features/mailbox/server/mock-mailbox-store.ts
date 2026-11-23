import 'server-only';

import {
  MAILBOX_MAX_MEMBERS,
  MAIL_FOLDERS_MAX_PER_MAILBOX,
  MAIL_FOLDER_NAME_MAX_LENGTH,
  MAIL_LABELS_MAX_PER_MAILBOX,
  MAIL_LABEL_COLORS,
  MAIL_LABEL_NAME_MAX_LENGTH,
  MAIL_MESSAGE_UPDATES_MAX_IDS,
  MAIL_PLACEMENTS,
  MAIL_THREAD_MAX_MESSAGES,
  MAIL_TRASH_RETENTION_DAYS,
  MAILBOX_MAX_PER_ORGANIZATION,
  MAILBOX_PERMISSIONS,
  MAIL_SUBJECT_MAX_LENGTH,
  MAIL_TEMPLATE_MAX_PER_ORGANIZATION,
  MAIL_TEMPLATE_MAX_VARIABLES,
  MAIL_TEMPLATE_NAME_MAX_LENGTH,
  MAIL_TEMPLATE_VALUE_MAX_LENGTH,
  MAIL_TEMPLATE_VARIABLE_PATTERN,
  MAIL_BODY_MAX_BYTES,
  MAIL_ROLES_MAX_PER_ORGANIZATION,
  MAIL_ROLE_DESCRIPTION_MAX_LENGTH,
  MAIL_ROLE_NAME_MAX_LENGTH,
  attachmentsWithinLimits,
  normalizeDisplayName,
  normalizeLocalPart,
  normalizeMailMessage,
} from '@/features/mailbox/schemas';
import { encodeInboxCursor, type InboxPosition } from '@/features/mailbox/server/inbox-cursor';
import type {
  CreateMailboxInput,
  InboxCounts,
  InboxFilters,
  InboxItem,
  InboxPage,
  MailAttachment,
  MailDayStats,
  MailEvent,
  MailFolder,
  MailLabel,
  MailRole,
  MailRoleInput,
  MailLabelColor,
  MailLogEntry,
  MailLogFilters,
  MailLogPage,
  MailMessage,
  MailMessageCounts,
  MailMessageFilters,
  MailMessagePage,
  MailMessageSummary,
  MailMessageUpdatesResult,
  MailPlacement,
  MailThreadSummary,
  MailTrashPurge,
  Mailbox,
  MailboxAccess,
  MailboxAccessEvent,
  MailboxAccessEventPage,
  MailboxAccessEventType,
  MailboxDomain,
  MailboxMember,
  MailboxPermission,
  MailboxSealedDeletion,
  MailboxDirectoryEntry,
  MailboxImportResult,
  MailboxImportRowResult,
  MailboxListScope,
  MemberImportInput,
  MemberImportResult,
  MemberImportRowResult,
  MailboxType,
  MailTemplate,
  MailTemplateInput,
  MailTemplateKind,
  MailTemplateSummary,
  SendMailMessageInput,
} from '@/features/mailbox/types';
import { mockTransactionalStore } from '@/features/developer/server/mock-transactional-store';
import { mockMemberWelcomeStore } from '@/features/mailbox/server/mock-member-welcome-store';
import { renderTemplate, templateVariables } from '@/features/mailbox/utils/template-render';
import { mockOrganizationStore } from '@/features/organization/server/mock-organization-store';
import {
  guardFormulaCell,
  parseImportMembers,
  stripFormulaGuard,
} from '@/features/mailbox/utils/mailbox-import';
import { ApiErrorCode } from '@/http/codes';
import type { OrganizationRole } from '@/features/organization/types';

/**
 * Development-only mailboxes, isolated per organization. It mirrors the browser-facing rules of
 * the Go mailbox starter: address validation and reserved names, addresses that stay reserved
 * after a mailbox is deleted, the per-organization mailbox limit, send validation, newest-first
 * listings without bodies, the cross-mailbox log, and per-day counts. Access follows
 * `contracts/MAILBOXES.md`: personal and shared mailboxes, member permissions, 404 without any
 * access and 403 without the needed one, per-person read state, handover with a seal boundary,
 * the webhook switch, and the access history. Mail is organized as the API does it: placements
 * shared by everyone, per-person stars, threads linked by `in_reply_to`, folders and labels with
 * their limits, a trash that keeps mail for 30 days, and a case-insensitive substring search that
 * answers a one-character text as too broad. Organization membership comes from the
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

/** The organization's mailboxes, roles, and members as the platform account mock reads them. */
export interface MockLedgerDirectory {
  mailboxes: ReadonlyMap<number, { address: string; readable: boolean; visible: boolean }>;
  roleIds: ReadonlySet<number>;
  isMember: (userId: number) => boolean;
}

interface MailboxAccessState {
  type: Mailbox['type'];
  /** `connected` holds copies of an outside account; `zmail` is Zmail's own. */
  origin: 'zmail' | 'connected';
  /** A connected mailbox bound with its SMTP server sends through it. */
  connectedSend?: boolean;
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
  /** A personal mailbox a roster import reserved: no owner until this email joins. */
  pendingOwnerEmail?: string;
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
  /** The mail role the mailbox belongs to; null when ungrouped. Grants nothing. */
  roleId: number | null;
  /** Stored with `starred: false`; each viewer's star comes from `stars`. */
  messages: Map<number, MailMessage>;
  /** Who read each inbound message, by user id. */
  reads: Map<number, Set<number>>;
  /** Who starred each message, by user id. */
  stars: Map<number, Set<number>>;
  folders: Map<number, MailFolder>;
  labels: Map<number, MailLabel>;
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
  | {
      ok: false;
      error: 'not_found' | 'invalid' | 'taken' | 'key_taken' | 'kind_immutable' | 'limit';
    };

export type MockMailAttachmentResult =
  | { ok: true; filename: string; content: Uint8Array }
  | { ok: false; error: MockAccessDenied | 'not_found' };

export type MockMailDeleteResult =
  { ok: true } | { ok: false; error: MockAccessDenied | 'not_found' };

/**
 * Failures of the organize routes: lookups (`not_found` is a message), the folder and label
 * rules, and a search too broad to answer. `validation` carries field errors for
 * `COMMON.VALIDATION_FAILED`.
 */
export type MockOrganizeError =
  | MockAccessDenied
  | 'not_found'
  | 'folder_not_found'
  | 'label_not_found'
  | 'folder_name_taken'
  | 'label_name_taken'
  | 'folder_limit'
  | 'label_limit'
  | 'search_too_broad'
  | 'role_not_found'
  | 'role_name_taken'
  | 'role_limit';
export type MockOrganizeResult<T> =
  | { ok: true; value: T }
  | { ok: false; error: MockOrganizeError }
  | { ok: false; error: 'validation'; fields: Record<string, string[]> };

/**
 * One change to one or several messages, shape-checked by the route. An absent field changes
 * nothing; the values are judged here as the API judges them.
 */
export interface MockMessageChange {
  read?: boolean;
  starred?: boolean;
  placement?: string;
  folder_id?: number;
  add_label_ids?: number[];
  remove_label_ids?: number[];
  label_ids?: number[];
}

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
  /** Account emails of the organization's current members by user id, lowercased. */
  memberEmails: (organizationId: number) => Map<number, string>;
  /** The organization half of a roster import: pending invitations and new ones. */
  rosterInviter: MockRosterInviter;
}

export interface MockRosterInviter {
  pendingInvitationIdFor: (organizationId: number, email: string) => number | null;
  inviteRosterMember: (
    organizationId: number,
    email: string,
    role: 'member' | 'admin',
    features: MemberImportInput['features']
  ) => { id: number; email_send_status: 'not_configured' };
}

/** The longest email an organization invitation stores, as in the API. */
const maxRosterEmailLength = 100;
const rosterEmailPattern = /^[^\s@<>(),;:"]+@[^\s@<>(),;:"]+\.[^\s@<>(),;:"]+$/u;

const defaultDependencies: MockMailboxStoreDependencies = {
  now: () => new Date(),
  membershipOf: (organizationId, userId) =>
    mockOrganizationStore.membershipIdOf(organizationId, userId),
  memberEmails: organizationId => mockOrganizationStore.memberEmails(organizationId),
  rosterInviter: mockOrganizationStore,
};

/** One directory page as the route validated its query. */
export interface MockDirectoryQuery {
  type?: MailboxType;
  q?: string;
  beforeId?: number;
  perPage: number;
}

export interface MockDirectoryPage {
  items: MailboxDirectoryEntry[];
  /** The last row's id when an older page follows; null otherwise. */
  nextBeforeId: number | null;
  mailboxCount: number;
  sentLast24h: number;
}

/** Resolves an import row's domain as the create route does. */
export type MockImportDomainResolver = (
  name: string
) => { ok: true; name: string } | { ok: false; error: 'not_found' | 'not_verified' };

const maxMockExportRows = 5_000;
const exportHeader = 'address,display_name,members,member_count,sent_last_24h,created_at';

/** What a viewer may do with a mailbox: the union of role, personal ownership, and member row. */
export function mailboxAccessFor(
  access: Pick<MailboxAccessState, 'type' | 'origin' | 'ownerUserId' | 'members' | 'connectedSend'>,
  viewer: MailboxViewer
): MailboxAccess {
  const connected = access.origin === 'connected';
  // A connected personal mailbox is its owner's own account: the role grants nothing on it.
  const admin =
    (viewer.role === 'owner' || viewer.role === 'admin') && !(connected && access.type === 'personal');
  const result: MailboxAccess = {
    view: false,
    send: false,
    manage_members: admin && access.type === 'shared',
    administer: admin,
    see_history: admin,
    // Never from the role alone: an owner or admin who cannot read the mail cannot delete it.
    organize: false,
  };
  if (access.type === 'personal' && access.ownerUserId === viewer.userId) {
    result.view = true;
    result.send = true;
    result.see_history = true;
    result.organize = true;
  }
  const member = access.type === 'shared' ? access.members.get(viewer.userId) : undefined;
  if (member) {
    result.view = true;
    if (member.permission !== 'view') result.send = true;
    if (member.permission === 'manage') {
      result.manage_members = true;
      result.see_history = true;
      result.organize = true;
    }
  }
  if (connected) {
    // Nobody organizes it, and it sends only through the account's own SMTP server.
    result.send = result.send && access.connectedSend === true;
    result.organize = false;
  }
  return result;
}

/** A Zmail personal mailbox whose owner left; a connected one never awaits a handover. */
function awaitsHandover(access: MailboxAccessState): boolean {
  return access.type === 'personal' && access.origin === 'zmail' && access.ownerLeft !== null;
}

/** A member's own connected account, which the directory and the export never list. */
function isMembersOwnAccount(access: MailboxAccessState): boolean {
  return access.origin === 'connected' && access.type === 'personal';
}

function anyAccess(access: MailboxAccess): boolean {
  return Object.values(access).some(Boolean);
}

function isPermission(value: unknown): value is MailboxPermission {
  return (MAILBOX_PERMISSIONS as readonly unknown[]).includes(value);
}

/** A mail role as stored: the owner as set, the mailboxes found from `MailboxState.roleId`. */
type StoredRole = Omit<MailRole, 'mailbox_ids'>;

/** A role write as the route shape-checked it; the values are judged by the store. */
export interface MockMailRoleInput {
  name: string;
  color?: string;
  description?: string;
  owner_user_id?: number | null;
}

/** Only owners and admins write mail roles; roles never come from mailbox access. */
function managesOrganization(viewer: MailboxViewer): boolean {
  return viewer.role === 'owner' || viewer.role === 'admin';
}

/** One unified inbox page as the route validated it. */
export interface MockInboxQuery extends InboxFilters {
  after?: InboxPosition;
  perPage: number;
}

const nanosecondsPerMillisecond = BigInt(1_000_000);

export class MockMailboxStore {
  private nextMailboxId = 1;
  private nextMessageId = 1;
  private nextAttachmentId = 1;
  private nextTemplateId = 1;
  private nextAccessEventId = 1;
  private nextFolderId = 1;
  private nextLabelId = 1;
  private nextRoleId = 1;
  private roles = new Map<number, Map<number, StoredRole>>();
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
      daily_broadcast_limit: MOCK_DAILY_SEND_LIMIT,
      daily_transactional_limit: MOCK_DAILY_SEND_LIMIT,
      transactional_recipient_limit_per_minute: 3,
      transactional_key_limit_per_second: 20,
      simulator_domain: 'test.zmail.im',
      broadcasts_enabled: true,
      custom_domains_enabled: true,
      // The instance switch of the transactional mock; on by default so the console is usable.
      transactional_enabled: mockTransactionalStore.isEnabled(),
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

  /**
   * Mailboxes the viewer has any access to, oldest first, as the API lists them. `usable` keeps
   * those the viewer reads or sends from, and for owners and admins those awaiting handover.
   */
  list(organizationId: number, viewer: MailboxViewer, scope?: MailboxListScope): Mailbox[] {
    return this.statesOf(organizationId)
      .flatMap(state => {
        const access = mailboxAccessFor(state.access, viewer);
        if (!anyAccess(access)) return [];
        const awaiting = awaitsHandover(state.access);
        if (
          scope === 'usable' &&
          !access.view &&
          !access.send &&
          !(awaiting && managesOrganization(viewer))
        ) {
          return [];
        }
        return [this.view(state, viewer, access)];
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
    const localPart = normalizeLocalPart(input.local_part, {
      sharedDomain: addressDomain === MOCK_MAILBOX_DOMAIN,
    });
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
      roleId: null,
      messages: new Map(),
      reads: new Map(),
      stars: new Map(),
      folders: new Map(),
      labels: new Map(),
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

  /**
   * The `external` mailbox of a newly bound outside account, owned by `ownerUserId` alone, with a
   * few synced copies so the development inbox shows them. The address is the outside account's,
   * never claimed, and the mailbox does not count toward the organization's limit.
   */
  createExternal(
    organizationId: number,
    ownerUserId: number,
    address: string,
    provider: string,
    backfillDays: number
  ): number {
    const mailbox: StoredMailbox = {
      id: this.nextMailboxId++,
      address,
      display_name: address,
      created_at: this.timestamp(),
    };
    const state: MailboxState = {
      mailbox,
      access: {
        type: 'personal',
        origin: 'connected',
        ownerUserId,
        ownerLeft: null,
        sealedBefore: null,
        sealedThroughMessageId: 0,
        webhooksEnabled: false,
        members: new Map(),
        events: [],
      },
      roleId: null,
      messages: new Map(),
      reads: new Map(),
      stars: new Map(),
      folders: new Map(),
      labels: new Map(),
      files: new Map(),
    };
    this.record(state, 'created', null, ownerUserId, { type: 'personal' });
    this.mailboxesOf(organizationId).set(mailbox.id, state);
    this.syncExternal(organizationId, mailbox.id, provider, backfillDays);
    return mailbox.id;
  }

  /** Records whether a connected mailbox sends through its account's SMTP server. */
  setConnectedSend(organizationId: number, mailboxId: number, sends: boolean): void {
    const state = this.mailboxesOf(organizationId).get(mailboxId);
    if (state && state.access.origin === 'connected') state.access.connectedSend = sends;
  }

  /** Copies one more synced message into an external mailbox, as a sync would. */
  syncExternal(
    organizationId: number,
    mailboxId: number,
    provider: string,
    backfillDays: number
  ): void {
    const state = this.mailboxesOf(organizationId).get(mailboxId);
    if (!state || state.access.origin !== 'connected') return;
    const now = this.dependencies.now().getTime();
    const samples = [
      { hoursAgo: 2, subject: 'Your order has shipped', from: 'orders@shop.example' },
      { hoursAgo: 30, subject: 'Notes from Tuesday', from: 'sam@partner.example' },
      { hoursAgo: 24 * Math.min(backfillDays, 10), subject: 'Welcome aboard', from: 'hi@news.example' },
    ];
    const stored = new Set([...state.messages.values()].map(message => message.message_id));
    for (const [index, sample] of samples.entries()) {
      // A copy is stored once per provider message id, so a rebind never adds a second one.
      const providerMessageId = `mock-${mailboxId}-${index}@${provider}.example`;
      if (stored.has(providerMessageId)) continue;
      const message = this.welcomeMessage(state.mailbox);
      this.store(state, {
        ...message,
        from: { address: sample.from, name: '' },
        subject: sample.subject,
        source: provider,
        created_at: new Date(now - sample.hoursAgo * 60 * 60 * 1_000).toISOString(),
        text_body: 'A read-only copy synced by the development mock connector.',
        html_body: '<p>A read-only copy synced by the <strong>development mock</strong> connector.</p>',
        message_id: providerMessageId,
      });
    }
  }

  /**
   * Deletes a connected mailbox and its synced copies for good, as a purge of a revoked connection
   * does; answers how many copies went, or null when there is no such connected mailbox.
   */
  removeExternal(organizationId: number, mailboxId: number): number | null {
    const mailboxes = this.mailboxesOf(organizationId);
    const state = mailboxes.get(mailboxId);
    if (!state || state.access.origin !== 'connected') return null;
    mailboxes.delete(mailboxId);
    return state.messages.size;
  }

  /** One page of the administration directory, newest first; owners and admins only. */
  directory(
    organizationId: number,
    viewer: MailboxViewer,
    query: MockDirectoryQuery
  ): { ok: true; value: MockDirectoryPage } | { ok: false; error: 'forbidden' } {
    if (!managesOrganization(viewer)) return { ok: false, error: 'forbidden' };
    const needle = query.q?.trim().toLowerCase() ?? '';
    const states = this.statesOf(organizationId).sort(
      (left, right) => right.mailbox.id - left.mailbox.id
    );
    const matching = states.filter(
      state =>
        !isMembersOwnAccount(state.access) &&
        (query.type === undefined || state.access.type === query.type) &&
        (query.beforeId === undefined || state.mailbox.id < query.beforeId) &&
        (needle === '' ||
          state.mailbox.address.toLowerCase().includes(needle) ||
          state.mailbox.display_name.toLowerCase().includes(needle))
    );
    const page = matching.slice(0, query.perPage);
    const items = page.map(state => this.directoryEntry(state));
    return {
      ok: true,
      value: {
        items,
        nextBeforeId:
          matching.length > query.perPage ? (page[page.length - 1]?.mailbox.id ?? null) : null,
        mailboxCount: states.length,
        sentLast24h: states.reduce((total, state) => total + this.sentLast24h(state), 0),
      },
    };
  }

  /** Changes the display name and records `renamed`; owners and admins only. */
  rename(
    organizationId: number,
    mailboxId: number,
    viewer: MailboxViewer,
    rawDisplayName: string
  ):
    | { ok: true; mailbox: Mailbox }
    | { ok: false; error: 'forbidden' | 'mailbox_not_found' | 'invalid' } {
    if (!managesOrganization(viewer)) return { ok: false, error: 'forbidden' };
    const state = this.stateOf(organizationId, mailboxId);
    if (!state) return { ok: false, error: 'mailbox_not_found' };
    const displayName = normalizeDisplayName(rawDisplayName);
    if (displayName === null) return { ok: false, error: 'invalid' };
    const from = state.mailbox.display_name;
    if (from !== displayName) {
      state.mailbox.display_name = displayName;
      this.record(state, 'renamed', viewer.userId, null, { from, to: displayName });
    }
    return { ok: true, mailbox: this.view(state, viewer) };
  }

  /**
   * Checks or creates shared mailboxes from import rows with the API's rules and row codes. A
   * real run creates each passing row through the create path; a dry run counts the limit in row
   * order and changes nothing.
   */
  importRows(
    organizationId: number,
    viewer: MailboxViewer,
    rows: readonly { address: string; display_name: string; members: string }[],
    dryRun: boolean,
    resolveDomain: MockImportDomainResolver
  ): { ok: true; value: MailboxImportResult } | { ok: false; error: 'forbidden' } {
    if (!managesOrganization(viewer)) return { ok: false, error: 'forbidden' };
    const emails = new Map<string, number>();
    for (const [userId, email] of this.dependencies.memberEmails(organizationId)) {
      emails.set(email, userId);
    }
    const seen = new Set<string>();
    let planned = this.mailboxesOf(organizationId).size;
    const result: MailboxImportResult = { dry_run: dryRun, succeeded: 0, failed: 0, rows: [] };
    rows.forEach((raw, index) => {
      const address = stripFormulaGuard(raw.address.trim()).trim();
      const outcome = (
        status: MailboxImportRowResult['status'],
        extra: Partial<MailboxImportRowResult> = {}
      ) => {
        result.rows.push({
          row: index + 1,
          address,
          status,
          mailbox_id: null,
          error_code: null,
          missing_emails: [],
          ...extra,
        });
        if (status === 'failed') result.failed += 1;
        else result.succeeded += 1;
      };
      const fail = (code: string, missing: string[] = []) =>
        outcome('failed', { error_code: code, missing_emails: missing });

      const key = address.toLowerCase();
      const duplicate = seen.has(key);
      seen.add(key);
      const at = address.lastIndexOf('@');
      if (at <= 0 || at === address.length - 1) return fail(ApiErrorCode.MAILBOX_ADDRESS_INVALID);
      if (duplicate) return fail(ApiErrorCode.MAILBOX_IMPORT_DUPLICATE_ROW);
      const displayName = normalizeDisplayName(stripFormulaGuard(raw.display_name));
      if (displayName === null) return fail(ApiErrorCode.MAILBOX_ADDRESS_INVALID);
      const members = parseImportMembers(stripFormulaGuard(raw.members.trim()));
      if (members === null) return fail(ApiErrorCode.MAILBOX_ACCESS_INVALID);
      const missing = members.filter(member => !emails.has(member.email)).map(item => item.email);
      if (missing.length > 0) return fail(ApiErrorCode.MAILBOX_IMPORT_MEMBER_NOT_FOUND, missing);
      const localPart = normalizeLocalPart(address.slice(0, at), {
        sharedDomain: address.slice(at + 1).toLowerCase() === MOCK_MAILBOX_DOMAIN,
      });
      if (!localPart.ok) {
        return fail(
          localPart.reason === 'reserved'
            ? ApiErrorCode.MAILBOX_ADDRESS_RESERVED
            : ApiErrorCode.MAILBOX_ADDRESS_INVALID
        );
      }
      const placement = resolveDomain(address.slice(at + 1));
      if (!placement.ok) {
        return fail(
          placement.error === 'not_verified'
            ? ApiErrorCode.MAILBOX_DOMAIN_NOT_VERIFIED
            : ApiErrorCode.MAILBOX_DOMAIN_NOT_FOUND
        );
      }
      if (this.claimedAddresses.has(`${localPart.value}@${placement.name}`)) {
        return fail(ApiErrorCode.MAILBOX_ADDRESS_TAKEN);
      }
      if (dryRun) {
        if (planned >= MAILBOX_MAX_PER_ORGANIZATION)
          return fail(ApiErrorCode.MAILBOX_LIMIT_REACHED);
        planned += 1;
        return outcome('valid');
      }
      const created = this.create(
        organizationId,
        viewer,
        {
          local_part: localPart.value,
          display_name: displayName,
          type: 'shared',
          members: members.map(member => ({
            user_id: emails.get(member.email) ?? 0,
            permission: member.permission,
          })),
        },
        placement.name
      );
      if (created.ok) return outcome('created', { mailbox_id: created.mailbox.id });
      const codes = {
        invalid: ApiErrorCode.MAILBOX_ADDRESS_INVALID,
        reserved: ApiErrorCode.MAILBOX_ADDRESS_RESERVED,
        taken: ApiErrorCode.MAILBOX_ADDRESS_TAKEN,
        limit: ApiErrorCode.MAILBOX_LIMIT_REACHED,
        access_invalid: ApiErrorCode.MAILBOX_ACCESS_INVALID,
      } as const;
      return fail(codes[created.error]);
    });
    return { ok: true, value: result };
  }


  /**
   * The roster import mock, mirroring the API's `ImportMembers`: each row reconciles toward a
   * pending invitation plus a personal mailbox reserved for its email (or, for a current member
   * without one, their own mailbox), so sending the same rows again changes nothing.
   */
  importMembers(
    organizationId: number,
    viewer: MailboxViewer,
    input: MemberImportInput,
    resolveDomain: MockImportDomainResolver
  ): { ok: true; value: MemberImportResult } | { ok: false; error: 'forbidden' } {
    if (!managesOrganization(viewer)) return { ok: false, error: 'forbidden' };
    const members = new Map<string, number>();
    for (const [userId, email] of this.dependencies.memberEmails(organizationId)) {
      members.set(email, userId);
    }
    const defaultDomain = input.domain.trim().toLowerCase().replace(/\.$/u, '');
    const seenEmails = new Set<string>();
    const seenAddresses = new Set<string>();
    let planned = this.mailboxesOf(organizationId).size;
    const result: MemberImportResult = {
      dry_run: input.dryRun,
      total: input.rows.length,
      valid: 0,
      invited: 0,
      created: 0,
      skipped: 0,
      failed: 0,
      welcome_mails_queued: 0,
      welcome_mails_failed: 0,
      rows: [],
    };

    input.rows.forEach((raw, index) => {
      const email = stripFormulaGuard(raw.email.trim()).trim().toLowerCase();
      let address = '';
      const push = (
        status: MemberImportRowResult['status'],
        extra: Partial<MemberImportRowResult> = {}
      ) => {
        const mailboxId = extra.mailbox_id ?? null;
        const provisioned =
          !input.dryRun &&
          mailboxId !== null &&
          (status === 'invited' ||
            status === 'created' ||
            (status === 'skipped' && extra.skip_reason === 'already_invited'));
        const welcome = provisioned
          ? mockMemberWelcomeStore.welcome(organizationId, mailboxId)
          : null;
        result.rows.push({
          row: index + 1,
          email,
          address,
          status,
          skip_reason: null,
          error_code: null,
          mailbox_id: null,
          invitation_id: null,
          email_send_status: null,
          welcome_mail_status: welcome,
          welcome_mail_error_code: null,
          ...extra,
        });
        result[status] += 1;
        if (welcome === 'queued') result.welcome_mails_queued += 1;
      };
      const fail = (code: string) => push('failed', { error_code: code });

      const duplicateEmail = seenEmails.has(email);
      if (email !== '') seenEmails.add(email);
      if (email === '' || email.length > maxRosterEmailLength || !rosterEmailPattern.test(email)) {
        return fail(ApiErrorCode.MAILBOX_IMPORT_EMAIL_INVALID);
      }
      const roleText = raw.role.trim().toLowerCase();
      if (roleText !== '' && roleText !== 'member' && roleText !== 'admin') {
        return fail(ApiErrorCode.MAILBOX_IMPORT_ROLE_INVALID);
      }
      const role = roleText === 'admin' ? 'admin' : 'member';
      const mailboxText = stripFormulaGuard(raw.mailbox.trim()).trim().toLowerCase();
      const at = mailboxText.lastIndexOf('@');
      let rawLocal = at >= 0 ? mailboxText.slice(0, at) : mailboxText;
      const domainName =
        at >= 0 ? mailboxText.slice(at + 1).replace(/\.$/u, '') : defaultDomain;
      if (rawLocal === '') rawLocal = email.slice(0, email.indexOf('@')).split('+')[0] ?? '';
      const localPart = normalizeLocalPart(rawLocal, {
        sharedDomain: domainName === MOCK_MAILBOX_DOMAIN,
      });
      if (!localPart.ok) {
        return fail(
          localPart.reason === 'reserved'
            ? ApiErrorCode.MAILBOX_ADDRESS_RESERVED
            : ApiErrorCode.MAILBOX_ADDRESS_INVALID
        );
      }
      if (domainName === '' || domainName === MOCK_MAILBOX_DOMAIN) {
        return fail(ApiErrorCode.MAILBOX_IMPORT_DOMAIN_REQUIRED);
      }
      address = `${localPart.value}@${domainName}`;
      const displayName = normalizeDisplayName(stripFormulaGuard(raw.name));
      if (displayName === null) return fail(ApiErrorCode.MAILBOX_ADDRESS_INVALID);
      const duplicateAddress = seenAddresses.has(address);
      seenAddresses.add(address);
      if (duplicateEmail || duplicateAddress) {
        return fail(ApiErrorCode.MAILBOX_IMPORT_DUPLICATE_ROW);
      }
      const placement = resolveDomain(domainName);
      if (!placement.ok) {
        return fail(
          placement.error === 'not_verified'
            ? ApiErrorCode.MAILBOX_DOMAIN_NOT_VERIFIED
            : ApiErrorCode.MAILBOX_DOMAIN_NOT_FOUND
        );
      }

      const reservation = () => this.reservationFor(organizationId, address, email);
      const plan = (): string | null => {
        if (planned >= MAILBOX_MAX_PER_ORGANIZATION) return ApiErrorCode.MAILBOX_LIMIT_REACHED;
        planned += 1;
        return null;
      };

      const memberId = members.get(email);
      if (memberId !== undefined) {
        if (this.ownsPersonal(organizationId, memberId)) {
          return push('skipped', { skip_reason: 'already_member' });
        }
        const reserved = reservation();
        if (reserved === 'taken') return push('skipped', { skip_reason: 'mailbox_taken' });
        if (reserved) {
          if (input.dryRun) return push('valid', { mailbox_id: reserved.mailbox.id });
          this.claimReserved(organizationId, memberId, email);
          return push('created', { mailbox_id: reserved.mailbox.id });
        }
        if (input.dryRun) {
          const limited = plan();
          return limited ? fail(limited) : push('valid');
        }
        const created = this.create(
          organizationId,
          viewer,
          {
            local_part: localPart.value,
            display_name: displayName,
            type: 'personal',
            owner_user_id: memberId,
          },
          placement.name
        );
        if (!created.ok) return fail(ApiErrorCode.MAILBOX_LIMIT_REACHED);
        return push('created', { mailbox_id: created.mailbox.id });
      }

      const pending = this.dependencies.rosterInviter.pendingInvitationIdFor(organizationId, email);
      const reserved = reservation();
      if (reserved === 'taken') return push('skipped', { skip_reason: 'mailbox_taken' });
      if (pending !== null && reserved) {
        return push('skipped', {
          skip_reason: 'already_invited',
          mailbox_id: reserved.mailbox.id,
          invitation_id: pending,
        });
      }
      let mailboxId: number | null = reserved ? reserved.mailbox.id : null;
      if (!reserved) {
        const limited = input.dryRun
          ? plan()
          : this.mailboxesOf(organizationId).size >= MAILBOX_MAX_PER_ORGANIZATION
            ? ApiErrorCode.MAILBOX_LIMIT_REACHED
            : null;
        if (limited) return fail(limited);
        if (!input.dryRun) {
          mailboxId = this.reservePersonal(organizationId, viewer, address, displayName, email);
        }
      }
      if (input.dryRun) return push('valid', { mailbox_id: mailboxId });
      if (pending !== null) {
        return push('invited', { mailbox_id: mailboxId, invitation_id: pending });
      }
      const invitation = this.dependencies.rosterInviter.inviteRosterMember(
        organizationId,
        email,
        role,
        input.features
      );
      return push('invited', {
        mailbox_id: mailboxId,
        invitation_id: invitation.id,
        email_send_status: invitation.email_send_status,
      });
    });
    return { ok: true, value: result };
  }

  /**
   * Gives `userId` every personal mailbox reserved for `email` in the organization, as the API
   * does on `member_joined`; access history records `owner_assigned`.
   */
  claimReserved(organizationId: number, userId: number, email: string): number[] {
    const claimed: number[] = [];
    for (const state of this.mailboxesOf(organizationId).values()) {
      if (state.access.pendingOwnerEmail !== email || state.access.ownerUserId !== null) continue;
      state.access.ownerUserId = userId;
      delete state.access.pendingOwnerEmail;
      this.record(state, 'owner_assigned', null, userId, {});
      claimed.push(state.mailbox.id);
    }
    return claimed;
  }

  /** The live reservation of `address` for `email`, null when the address is free, else taken. */
  private reservationFor(
    organizationId: number,
    address: string,
    email: string
  ): MailboxState | null | 'taken' {
    for (const state of this.mailboxesOf(organizationId).values()) {
      if (state.mailbox.address !== address) continue;
      return state.access.type === 'personal' &&
        state.access.ownerUserId === null &&
        state.access.pendingOwnerEmail === email
        ? state
        : 'taken';
    }
    return this.claimedAddresses.has(address) ? 'taken' : null;
  }

  private ownsPersonal(organizationId: number, userId: number): boolean {
    for (const state of this.mailboxesOf(organizationId).values()) {
      const { access } = state;
      if (access.type === 'personal' && access.origin === 'zmail' && access.ownerUserId === userId) {
        return true;
      }
    }
    return false;
  }

  /** A personal mailbox with no owner, held for `email`: nobody reads it until they join. */
  private reservePersonal(
    organizationId: number,
    viewer: MailboxViewer,
    address: string,
    displayName: string,
    email: string
  ): number {
    const mailbox: StoredMailbox = {
      id: this.nextMailboxId++,
      address,
      display_name: displayName,
      created_at: this.timestamp(),
    };
    const state: MailboxState = {
      mailbox,
      access: {
        type: 'personal',
        origin: 'zmail',
        ownerUserId: null,
        ownerLeft: null,
        sealedBefore: null,
        sealedThroughMessageId: 0,
        webhooksEnabled: false,
        members: new Map(),
        events: [],
        pendingOwnerEmail: email,
      },
      roleId: null,
      messages: new Map(),
      reads: new Map(),
      stars: new Map(),
      folders: new Map(),
      labels: new Map(),
      files: new Map(),
    };
    this.record(state, 'created', viewer.userId, null, { type: 'personal' });
    this.mailboxesOf(organizationId).set(mailbox.id, state);
    this.claimedAddresses.add(address);
    return mailbox.id;
  }

  /** Every mailbox of one type as the API's CSV file, newest first; owners and admins only. */
  exportCsv(
    organizationId: number,
    viewer: MailboxViewer,
    type: MailboxType
  ): { ok: true; csv: string; filename: string } | { ok: false; error: 'forbidden' } {
    if (!managesOrganization(viewer)) return { ok: false, error: 'forbidden' };
    const emails = this.dependencies.memberEmails(organizationId);
    const lines = this.statesOf(organizationId)
      .filter(state => state.access.type === type && !isMembersOwnAccount(state.access))
      .sort((left, right) => right.mailbox.id - left.mailbox.id)
      .slice(0, maxMockExportRows)
      .map(state => {
        const entry = this.directoryEntry(state);
        const members: string[] = [];
        const owner = state.access.ownerUserId;
        if (owner !== null && emails.has(owner)) members.push(emails.get(owner) ?? '');
        for (const member of state.access.members.values()) {
          const email = emails.get(member.user_id);
          if (email) members.push(`${email}:${member.permission}`);
        }
        members.sort();
        return [
          guardFormulaCell(entry.address),
          guardFormulaCell(entry.display_name),
          guardFormulaCell(members.join(';')),
          String(entry.member_count),
          String(entry.sent_last_24h),
          new Date(entry.created_at).toISOString().replace(/\.\d{3}Z$/u, 'Z'),
        ]
          .map(csvField)
          .join(',');
      });
    const day = this.dependencies.now().toISOString().slice(0, 10).replaceAll('-', '');
    return {
      ok: true,
      csv: `\uFEFF${[exportHeader, ...lines].join('\r\n')}\r\n`,
      filename: `${type}-mailboxes-${day}.csv`,
    };
  }

  /** Deletes the mailbox and its messages; the address stays claimed. Needs `administer`. */
  delete(organizationId: number, mailboxId: number, viewer: MailboxViewer): MockMailDeleteResult {
    const found = this.authorize(organizationId, mailboxId, viewer, 'administer');
    if (!found.ok) return found;
    this.mailboxesOf(organizationId).delete(mailboxId);
    return { ok: true };
  }

  /**
   * One page of a mailbox listing. `filters` were checked by the route; this judges what needs
   * the mailbox: the folder and label must be its own, and a one-character search is too broad.
   */
  listMessages(
    organizationId: number,
    mailboxId: number,
    viewer: MailboxViewer,
    filters: MailMessageFilters,
    page: number,
    perPage: number
  ): MockOrganizeResult<MailMessagePage> {
    const found = this.authorize(organizationId, mailboxId, viewer, 'view');
    if (!found.ok) return found;
    const { state } = found;
    if (filters.folderId !== undefined && !state.folders.has(filters.folderId)) {
      return { ok: false, error: 'folder_not_found' };
    }
    if (filters.labelId !== undefined && !state.labels.has(filters.labelId)) {
      return { ok: false, error: 'label_not_found' };
    }
    const needle = filters.q?.trim().toLowerCase() ?? '';
    if (Array.from(needle).length === 1) return { ok: false, error: 'search_too_broad' };

    const matching = this.visibleMessages(state)
      .map(message => this.forViewer(state, message, viewer))
      .filter(message => matchesFilters(message, filters, needle))
      .sort(
        filters.placement === 'trash'
          ? (left, right) =>
              (right.trashed_at ?? '').localeCompare(left.trashed_at ?? '') || right.id - left.id
          : (left, right) => right.id - left.id
      );
    const items: MailMessageSummary[] =
      filters.group === 'thread' ? threadRows(matching) : matching.map(summaryOf);
    return {
      ok: true,
      value: paginated(
        items,
        page,
        perPage,
        `/api/mailboxes/${mailboxId}/messages?${filterQuery(filters)}`
      ),
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
          ...logSummaryOf(this.forViewer(state, message, viewer)),
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
   * One cursor page of the unified inbox over the mailboxes the viewer reads, newest first by
   * `(created_at, id)`. `all` is inbound mail in `default`, `sent` outbound mail in `default`,
   * `today` inbound mail outside the trash that arrived during the viewer's day in `tz`, and
   * `pending` the viewer's stars outside the archive and the trash. `q` searches both directions
   * outside the trash, as the mailbox search does, and a one-character search is too broad. A role
   * id keeps the mailboxes in that role and `none` those without one; an unreadable or foreign
   * mailbox, or a role holding none the viewer reads, matches nothing.
   */
  listInbox(
    organizationId: number,
    viewer: MailboxViewer,
    query: MockInboxQuery
  ): MockOrganizeResult<InboxPage> {
    const needle = query.q?.trim().toLowerCase();
    if (needle !== undefined && Array.from(needle).length === 1) {
      return { ok: false, error: 'search_too_broad' };
    }
    const day = viewerDay(this.dependencies.now(), query.tz);
    const since = query.since === undefined ? -Infinity : dayStart(query.since, query.tz, 0);
    const until = query.until === undefined ? Infinity : dayStart(query.until, query.tz, 1);
    const rows: { item: InboxItem; createdAt: bigint }[] = [];
    for (const state of this.readableStates(organizationId, viewer)) {
      if (query.mailboxId !== undefined && state.mailbox.id !== query.mailboxId) continue;
      if (
        query.roleId !== undefined &&
        state.roleId !== (query.roleId === 'none' ? null : query.roleId)
      ) {
        continue;
      }
      const matching = this.visibleMessages(state)
        .map(message => this.forViewer(state, message, viewer))
        .filter(message => inInboxView(message, query, needle, day))
        .filter(message => {
          const created = Date.parse(message.created_at);
          return created >= since && created < until;
        });
      const summaries = query.group === 'thread' ? threadRows(matching) : matching.map(summaryOf);
      for (const summary of summaries) {
        rows.push({
          item: {
            ...summary,
            mailbox_address: state.mailbox.address,
            role_id: state.roleId,
            snippet: inboxSnippet(state.messages.get(summary.id)?.text_body ?? ''),
          },
          createdAt: unixNanoseconds(summary.created_at),
        });
      }
    }
    const after = query.after;
    const page = rows
      .filter(
        row =>
          !after ||
          row.createdAt < after.createdAt ||
          (row.createdAt === after.createdAt && BigInt(row.item.id) < after.id)
      )
      .sort(
        (left, right) =>
          (left.createdAt === right.createdAt ? 0 : left.createdAt < right.createdAt ? 1 : -1) ||
          right.item.id - left.item.id
      )
      .slice(0, query.perPage + 1);
    const hasMore = page.length > query.perPage;
    const items = page.slice(0, query.perPage);
    const last = items[items.length - 1];
    return {
      ok: true,
      value: {
        items: items.map(row => row.item),
        meta: {
          per_page: query.perPage,
          has_more: hasMore,
          next_cursor:
            hasMore && last
              ? encodeInboxCursor({ createdAt: last.createdAt, id: BigInt(last.item.id) })
              : null,
        },
      },
    };
  }

  /**
   * The viewer's own unified inbox counts over the mailboxes they read, by mailbox id; the views
   * and the per-mailbox unread counts use the same rules as `listInbox` and the mailbox list.
   * Each role sums its readable mailboxes, by role id; the rest is ungrouped. Mail waiting for the
   * send limit counts outside the trash only.
   */
  inboxCounts(organizationId: number, viewer: MailboxViewer, tz: string): InboxCounts {
    const day = viewerDay(this.dependencies.now(), tz);
    const views = { all_unread: 0, today: 0, today_unread: 0, pending: 0, sent_waiting: 0 };
    const mailboxes: InboxCounts['mailboxes'] = [];
    const roles = new Map<number, number>();
    let ungrouped = 0;
    const states = this.readableStates(organizationId, viewer).sort(
      (left, right) => left.mailbox.id - right.mailbox.id
    );
    for (const state of states) {
      let unread = 0;
      for (const stored of this.visibleMessages(state)) {
        const message = this.forViewer(state, stored, viewer);
        const isUnread = !message.read;
        if (inInboxView(message, { view: 'all' }) && isUnread) unread += 1;
        if (inInboxView(message, { view: 'today' }, undefined, day)) {
          views.today += 1;
          if (isUnread) views.today_unread += 1;
        }
        if (inInboxView(message, { view: 'pending' })) views.pending += 1;
        if (message.queued_reason === 'send_limit' && message.placement !== 'trash') {
          views.sent_waiting += 1;
        }
      }
      views.all_unread += unread;
      mailboxes.push({ mailbox_id: state.mailbox.id, unread });
      if (state.roleId === null) ungrouped += unread;
      else roles.set(state.roleId, (roles.get(state.roleId) ?? 0) + unread);
    }
    return {
      views,
      roles: [...roles]
        .sort(([left], [right]) => left - right)
        .map(([roleId, unread]) => ({ role_id: roleId, unread })),
      ungrouped_unread: ungrouped,
      mailboxes,
      today_starts_at: apiTimestamp(day.start),
      today_ends_at: apiTimestamp(day.end),
    };
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

  /** Applies a change to one message and answers it as the viewer sees it. */
  updateMessage(
    organizationId: number,
    mailboxId: number,
    messageId: number,
    viewer: MailboxViewer,
    change: MockMessageChange
  ): MockOrganizeResult<MailMessage> {
    const applied = this.applyChange(organizationId, mailboxId, viewer, [messageId], change, false);
    if (!applied.ok) return applied;
    const message = applied.value.messages.get(messageId);
    return message
      ? { ok: true, value: this.forViewer(applied.value, message, viewer) }
      : { ok: false, error: 'not_found' };
  }

  /** Applies one change to 1 to 100 messages, all or nothing. */
  updateMessages(
    organizationId: number,
    mailboxId: number,
    viewer: MailboxViewer,
    messageIds: number[],
    change: MockMessageChange
  ): MockOrganizeResult<MailMessageUpdatesResult> {
    const applied = this.applyChange(organizationId, mailboxId, viewer, messageIds, change, true);
    return applied.ok ? { ok: true, value: { updated: messageIds.length } } : applied;
  }

  /**
   * Outside the trash a delete moves the message there, which needs `send`; in the trash it
   * deletes the message for good, which needs `organize`.
   */
  deleteMessage(
    organizationId: number,
    mailboxId: number,
    messageId: number,
    viewer: MailboxViewer
  ): MockMailDeleteResult {
    const found = this.authorize(organizationId, mailboxId, viewer, 'send');
    if (!found.ok) return found;
    const message = this.visibleMessage(found.state, messageId);
    if (!message) return { ok: false, error: 'not_found' };
    if (message.placement !== 'trash') {
      found.state.messages.set(messageId, this.placed(message, 'trash', null));
      return { ok: true };
    }
    if (!found.access.organize) return { ok: false, error: 'forbidden' };
    this.forget(found.state, messageId);
    return { ok: true };
  }

  /** Deletes every visible message in the trash for good; sealed mail stays. */
  purgeTrash(
    organizationId: number,
    mailboxId: number,
    viewer: MailboxViewer
  ): MockOrganizeResult<MailTrashPurge> {
    const found = this.authorize(organizationId, mailboxId, viewer, 'organize');
    if (!found.ok) return found;
    let deleted = 0;
    for (const message of this.visibleMessages(found.state)) {
      if (message.placement !== 'trash') continue;
      this.forget(found.state, message.id);
      deleted += 1;
    }
    return { ok: true, value: { deleted } };
  }

  /** The thread's messages outside the trash, oldest first, at most the latest 100. */
  thread(
    organizationId: number,
    mailboxId: number,
    threadId: number,
    viewer: MailboxViewer
  ): MockOrganizeResult<MailMessage[]> {
    const found = this.authorize(organizationId, mailboxId, viewer, 'view');
    if (!found.ok) return found;
    const messages = this.visibleMessages(found.state)
      .filter(message => message.thread_id === threadId && message.placement !== 'trash')
      .sort((left, right) => left.id - right.id)
      .slice(-MAIL_THREAD_MAX_MESSAGES)
      .map(message => this.forViewer(found.state, message, viewer));
    return messages.length > 0 ? { ok: true, value: messages } : { ok: false, error: 'not_found' };
  }

  /** The viewer's own unread and starred counts; the trash never counts. */
  counts(
    organizationId: number,
    mailboxId: number,
    viewer: MailboxViewer
  ): MockOrganizeResult<MailMessageCounts> {
    const found = this.authorize(organizationId, mailboxId, viewer, 'view');
    if (!found.ok) return found;
    const { state } = found;
    const folderUnread = new Map(sortedByName(state.folders).map(folder => [folder.id, 0]));
    const value: MailMessageCounts = {
      placements: { default_inbound_unread: 0, archive_unread: 0 },
      folders: [],
      starred: 0,
    };
    for (const stored of this.visibleMessages(state)) {
      if (stored.placement === 'trash') continue;
      const message = this.forViewer(state, stored, viewer);
      if (message.starred) value.starred += 1;
      if (message.direction !== 'inbound' || message.read) continue;
      if (message.placement === 'default') value.placements.default_inbound_unread += 1;
      else if (message.placement === 'archive') value.placements.archive_unread += 1;
      else if (message.folder_id !== null && folderUnread.has(message.folder_id)) {
        folderUnread.set(message.folder_id, (folderUnread.get(message.folder_id) ?? 0) + 1);
      }
    }
    value.folders = [...folderUnread].map(([folderId, unread]) => ({
      folder_id: folderId,
      unread,
    }));
    return { ok: true, value };
  }

  /** Every folder of the mailbox by name, ignoring case. */
  folders(
    organizationId: number,
    mailboxId: number,
    viewer: MailboxViewer
  ): MockOrganizeResult<MailFolder[]> {
    const found = this.authorize(organizationId, mailboxId, viewer, 'view');
    if (!found.ok) return found;
    return { ok: true, value: sortedByName(found.state.folders).map(folder => ({ ...folder })) };
  }

  /** Creates a folder, or renames one when `folderId` is given. Needs `organize`. */
  saveFolder(
    organizationId: number,
    mailboxId: number,
    viewer: MailboxViewer,
    rawName: string,
    folderId?: number
  ): MockOrganizeResult<MailFolder> {
    const found = this.authorize(organizationId, mailboxId, viewer, 'organize');
    if (!found.ok) return found;
    const { folders } = found.state;
    const existing = folderId === undefined ? undefined : folders.get(folderId);
    if (folderId !== undefined && !existing) return { ok: false, error: 'folder_not_found' };
    const fields: Record<string, string[]> = {};
    const name = organizeName(fields, rawName, MAIL_FOLDER_NAME_MAX_LENGTH);
    if (Object.keys(fields).length > 0) return { ok: false, error: 'validation', fields };
    if (!existing && folders.size >= MAIL_FOLDERS_MAX_PER_MAILBOX) {
      return { ok: false, error: 'folder_limit' };
    }
    if (nameTaken(folders, name, folderId)) return { ok: false, error: 'folder_name_taken' };
    const folder: MailFolder = existing
      ? { ...existing, name }
      : { id: this.nextFolderId++, name, created_at: this.timestamp() };
    folders.set(folder.id, folder);
    return { ok: true, value: { ...folder } };
  }

  /** Deletes a folder; its messages go back to `default`. Needs `organize`. */
  deleteFolder(
    organizationId: number,
    mailboxId: number,
    viewer: MailboxViewer,
    folderId: number
  ): MockOrganizeResult<null> {
    const found = this.authorize(organizationId, mailboxId, viewer, 'organize');
    if (!found.ok) return found;
    const { state } = found;
    if (!state.folders.delete(folderId)) return { ok: false, error: 'folder_not_found' };
    for (const message of state.messages.values()) {
      if (message.folder_id === folderId) {
        state.messages.set(message.id, this.placed(message, 'default', null));
      }
    }
    return { ok: true, value: null };
  }

  /** Every label of the mailbox by name, ignoring case. */
  labels(
    organizationId: number,
    mailboxId: number,
    viewer: MailboxViewer
  ): MockOrganizeResult<MailLabel[]> {
    const found = this.authorize(organizationId, mailboxId, viewer, 'view');
    if (!found.ok) return found;
    return { ok: true, value: sortedByName(found.state.labels).map(label => ({ ...label })) };
  }

  /** Creates a label, or replaces the name and color of one. Needs `organize`. */
  saveLabel(
    organizationId: number,
    mailboxId: number,
    viewer: MailboxViewer,
    input: { name: string; color: string },
    labelId?: number
  ): MockOrganizeResult<MailLabel> {
    const found = this.authorize(organizationId, mailboxId, viewer, 'organize');
    if (!found.ok) return found;
    const { labels } = found.state;
    const existing = labelId === undefined ? undefined : labels.get(labelId);
    if (labelId !== undefined && !existing) return { ok: false, error: 'label_not_found' };
    const fields: Record<string, string[]> = {};
    const name = organizeName(fields, input.name, MAIL_LABEL_NAME_MAX_LENGTH);
    if (!isLabelColor(input.color)) {
      fields.color = [`must be one of: ${MAIL_LABEL_COLORS.join(', ')}`];
    }
    if (Object.keys(fields).length > 0) return { ok: false, error: 'validation', fields };
    if (!existing && labels.size >= MAIL_LABELS_MAX_PER_MAILBOX) {
      return { ok: false, error: 'label_limit' };
    }
    if (nameTaken(labels, name, labelId)) return { ok: false, error: 'label_name_taken' };
    const label: MailLabel = existing
      ? { ...existing, name, color: input.color as MailLabelColor }
      : {
          id: this.nextLabelId++,
          name,
          color: input.color as MailLabelColor,
          created_at: this.timestamp(),
        };
    labels.set(label.id, label);
    return { ok: true, value: { ...label } };
  }

  /** Deletes a label and removes it from every message. Needs `organize`. */
  deleteLabel(
    organizationId: number,
    mailboxId: number,
    viewer: MailboxViewer,
    labelId: number
  ): MockOrganizeResult<null> {
    const found = this.authorize(organizationId, mailboxId, viewer, 'organize');
    if (!found.ok) return found;
    const { state } = found;
    if (!state.labels.delete(labelId)) return { ok: false, error: 'label_not_found' };
    for (const message of state.messages.values()) {
      if (message.label_ids.includes(labelId)) {
        state.messages.set(message.id, {
          ...message,
          label_ids: message.label_ids.filter(id => id !== labelId),
        });
      }
    }
    return { ok: true, value: null };
  }

  /**
   * The organization's mail roles by name ignoring case, then id. Owners and admins see every role
   * with every mailbox in it; members only roles holding a mailbox they can see, with only those.
   * An owner who left the organization reads as none.
   */
  listRoles(organizationId: number, viewer: MailboxViewer): MailRole[] {
    const roles = sortedByName(this.rolesOf(organizationId))
      .map(role => this.roleView(organizationId, role, viewer))
      .filter(role => managesOrganization(viewer) || role.mailbox_ids.length > 0);
    return roles;
  }

  /** Owners and admins only; the limit is judged before the name, as the API's insert does. */
  createRole(
    organizationId: number,
    viewer: MailboxViewer,
    input: MockMailRoleInput
  ): MockOrganizeResult<MailRole> {
    if (!managesOrganization(viewer)) return { ok: false, error: 'forbidden' };
    const checked = this.checkRole(organizationId, input);
    if (!checked.ok) return checked;
    const roles = this.rolesOf(organizationId);
    if (roles.size >= MAIL_ROLES_MAX_PER_ORGANIZATION) return { ok: false, error: 'role_limit' };
    if (nameTaken(roles, checked.value.name)) return { ok: false, error: 'role_name_taken' };
    const now = this.timestamp();
    const role: StoredRole = {
      id: this.nextRoleId++,
      ...checked.value,
      created_at: now,
      updated_at: now,
    };
    roles.set(role.id, role);
    return { ok: true, value: this.roleView(organizationId, role, viewer) };
  }

  /** Replaces every field; the last write wins. Owners and admins only. */
  updateRole(
    organizationId: number,
    viewer: MailboxViewer,
    roleId: number,
    input: MockMailRoleInput
  ): MockOrganizeResult<MailRole> {
    if (!managesOrganization(viewer)) return { ok: false, error: 'forbidden' };
    const roles = this.rolesOf(organizationId);
    const existing = roles.get(roleId);
    if (!existing) return { ok: false, error: 'role_not_found' };
    const checked = this.checkRole(organizationId, input);
    if (!checked.ok) return checked;
    if (nameTaken(roles, checked.value.name, roleId))
      return { ok: false, error: 'role_name_taken' };
    const role: StoredRole = { ...existing, ...checked.value, updated_at: this.timestamp() };
    roles.set(roleId, role);
    return { ok: true, value: this.roleView(organizationId, role, viewer) };
  }

  /** Deletes the role; its mailboxes become ungrouped, and no access changes. */
  deleteRole(
    organizationId: number,
    viewer: MailboxViewer,
    roleId: number
  ): MockOrganizeResult<null> {
    if (!managesOrganization(viewer)) return { ok: false, error: 'forbidden' };
    if (!this.rolesOf(organizationId).delete(roleId)) return { ok: false, error: 'role_not_found' };
    for (const state of this.mailboxesOf(organizationId).values()) {
      if (state.roleId === roleId) state.roleId = null;
    }
    return { ok: true, value: null };
  }

  /**
   * Puts the mailbox in a role, or takes it out with null; needs `administer`. Setting the current
   * value succeeds. Answers the mailbox as the caller sees it.
   */
  assignRole(
    organizationId: number,
    mailboxId: number,
    viewer: MailboxViewer,
    roleId: number | null
  ): MockOrganizeResult<Mailbox> {
    const found = this.authorize(organizationId, mailboxId, viewer, 'administer');
    if (!found.ok) return found;
    if (roleId !== null && !this.rolesOf(organizationId).has(roleId)) {
      return { ok: false, error: 'role_not_found' };
    }
    found.state.roleId = roleId;
    return { ok: true, value: this.view(found.state, viewer, found.access) };
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
    if (access.type !== 'personal' || access.origin === 'connected') return { ok: false, error: 'conflict' };

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

  /** Every template of the organization by name, or only those of `kind`. */
  listTemplates(organizationId: number, kind?: MailTemplateKind): MailTemplateSummary[] {
    return [...this.templatesOf(organizationId).values()]
      .filter(template => kind === undefined || template.kind === kind)
      .sort((a, b) => (a.name < b.name ? -1 : a.name > b.name ? 1 : a.id - b.id))
      .map(template => ({
        id: template.id,
        name: template.name,
        subject: template.subject,
        variables: template.variables,
        created_at: template.created_at,
        updated_at: template.updated_at,
        kind: template.kind,
        key: template.key,
        from: template.from,
        variable_definitions: template.variable_definitions,
        // The mock has no developer scopes: every caller is inside.
        outside_sending_scope: false,
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
    if (
      built.key !== null &&
      [...templates.values()].some(existing => existing.key === built.key)
    ) {
      return { ok: false, error: 'key_taken' };
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
    if (built.kind !== existing.kind) return { ok: false, error: 'kind_immutable' };
    if (
      [...templates.values()].some(other => other.id !== templateId && other.name === built.name)
    ) {
      return { ok: false, error: 'taken' };
    }
    if (
      built.key !== null &&
      [...templates.values()].some(other => other.id !== templateId && other.key === built.key)
    ) {
      return { ok: false, error: 'key_taken' };
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
    // A reply joins the thread of the message it answers, when that message is in this mailbox.
    const replied =
      normalized.in_reply_to === ''
        ? undefined
        : [...state.messages.values()].find(
            message => message.message_id === normalized.in_reply_to
          );
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
      placement: 'default',
      folder_id: null,
      label_ids: [],
      starred: false,
      thread_id: replied?.thread_id ?? id,
      trashed_at: null,
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

  /**
   * What the platform account ledger mock needs to judge a request as the API does: each mailbox
   * with whether the viewer may read it or see it at all, the organization's role ids, and who is a
   * current member.
   */
  ledgerDirectory(organizationId: number, viewer: MailboxViewer): MockLedgerDirectory {
    const mailboxes = new Map<number, { address: string; readable: boolean; visible: boolean }>();
    for (const state of this.statesOf(organizationId)) {
      const access = mailboxAccessFor(state.access, viewer);
      mailboxes.set(state.mailbox.id, {
        address: state.mailbox.address,
        readable: access.view,
        visible: anyAccess(access),
      });
    }
    return {
      mailboxes,
      roleIds: new Set(this.rolesOf(organizationId).keys()),
      isMember: userId => this.dependencies.membershipOf(organizationId, userId) !== null,
    };
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
    state.stars.delete(messageId);
  }

  /** The message in another placement; `trashed_at` is kept only while it stays in the trash. */
  private placed(
    message: MailMessage,
    placement: MailPlacement,
    folderId: number | null
  ): MailMessage {
    const trashedAt = placement !== 'trash' ? null : (message.trashed_at ?? this.timestamp());
    return {
      ...message,
      placement,
      folder_id: placement === 'folder' ? folderId : null,
      trashed_at: trashedAt,
    };
  }

  /**
   * The checks of `PUT .../messages/:id` and `POST .../message-updates`, in the API's order:
   * mailbox, permission, an over-long batch, every named message, folder, and label, then the
   * body rules. Nothing changes unless all pass.
   */
  private applyChange(
    organizationId: number,
    mailboxId: number,
    viewer: MailboxViewer,
    messageIds: number[],
    change: MockMessageChange,
    batch: boolean
  ): MockOrganizeResult<MailboxState> {
    const shared =
      change.placement !== undefined ||
      change.folder_id !== undefined ||
      change.add_label_ids !== undefined ||
      change.remove_label_ids !== undefined ||
      change.label_ids !== undefined;
    const found = this.authorize(organizationId, mailboxId, viewer, shared ? 'send' : 'view');
    if (!found.ok) return found;
    const { state } = found;
    if (batch && messageIds.length > MAIL_MESSAGE_UPDATES_MAX_IDS) {
      const fields: Record<string, string[]> = {};
      checkIdList(fields, 'message_ids', messageIds, 1);
      return { ok: false, error: 'validation', fields };
    }
    const messages = positiveDistinct(messageIds).map(id => this.visibleMessage(state, id));
    if (messages.some(message => message === undefined)) return { ok: false, error: 'not_found' };
    if (
      change.folder_id !== undefined &&
      change.folder_id !== 0 &&
      !state.folders.has(change.folder_id)
    ) {
      return { ok: false, error: 'folder_not_found' };
    }
    const labelIds = positiveDistinct([
      ...(change.add_label_ids ?? []),
      ...(change.remove_label_ids ?? []),
      ...(change.label_ids ?? []),
    ]);
    if (labelIds.some(id => !state.labels.has(id))) return { ok: false, error: 'label_not_found' };
    const fields = changeErrors(messageIds, change, batch, shared);
    if (Object.keys(fields).length > 0) return { ok: false, error: 'validation', fields };

    for (const stored of messages as MailMessage[]) {
      let message = stored;
      if (change.read !== undefined && message.direction === 'inbound') {
        toggle(state.reads, message.id, viewer.userId, change.read);
      }
      if (change.starred !== undefined) {
        toggle(state.stars, message.id, viewer.userId, change.starred);
      }
      if (change.placement !== undefined) {
        message = this.placed(message, change.placement as MailPlacement, change.folder_id ?? null);
      }
      if (change.label_ids !== undefined || change.add_label_ids || change.remove_label_ids) {
        const next = new Set(change.label_ids ?? message.label_ids);
        for (const id of change.add_label_ids ?? []) next.add(id);
        for (const id of change.remove_label_ids ?? []) next.delete(id);
        message = { ...message, label_ids: [...next].sort((left, right) => left - right) };
      }
      state.messages.set(message.id, message);
    }
    return { ok: true, value: state };
  }

  /** The access of a new mailbox, or null for `MAILBOX.ACCESS_INVALID`. */
  private initialAccess(
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
    // The API's worker deletes trashed mail for good after 30 days; the mock does it on access.
    const expiry = this.dependencies.now().getTime() - MAIL_TRASH_RETENTION_DAYS * dayMilliseconds;
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

  /** The viewer's own read flag and star; outbound messages are read for everyone. */
  private forViewer(state: MailboxState, message: MailMessage, viewer: MailboxViewer): MailMessage {
    const read =
      message.direction === 'outbound' ||
      (state.reads.get(message.id)?.has(viewer.userId) ?? false);
    const starred = state.stars.get(message.id)?.has(viewer.userId) ?? false;
    return { ...message, read, starred };
  }

  private view(
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

  private rolesOf(organizationId: number): Map<number, StoredRole> {
    const existing = this.roles.get(organizationId);
    if (existing) return existing;
    if (this.roles.size >= maxMockOrganizations) {
      const oldest = this.roles.keys().next().value;
      if (oldest !== undefined) this.roles.delete(oldest);
    }
    const created = new Map<number, StoredRole>();
    this.roles.set(organizationId, created);
    return created;
  }

  /** The role as the viewer sees it: the mailboxes they can see, and only a current owner. */
  private roleView(organizationId: number, role: StoredRole, viewer: MailboxViewer): MailRole {
    const mailboxIds = this.statesOf(organizationId)
      .filter(
        state => state.roleId === role.id && anyAccess(mailboxAccessFor(state.access, viewer))
      )
      .map(state => state.mailbox.id)
      .sort((left, right) => left - right);
    const owner =
      role.owner_user_id !== null &&
      this.dependencies.membershipOf(organizationId, role.owner_user_id) !== null
        ? role.owner_user_id
        : null;
    return { ...role, owner_user_id: owner, mailbox_ids: mailboxIds };
  }

  /** The field rules of a role write, with the defaults applied. */
  private checkRole(
    organizationId: number,
    input: MockMailRoleInput
  ):
    | { ok: true; value: Required<MailRoleInput> & { color: MailLabelColor } }
    | { ok: false; error: 'validation'; fields: Record<string, string[]> } {
    const fields: Record<string, string[]> = {};
    const name = organizeName(fields, input.name, MAIL_ROLE_NAME_MAX_LENGTH);
    const color = input.color ?? 'gray';
    if (!isLabelColor(color))
      addField(fields, 'color', `must be one of: ${MAIL_LABEL_COLORS.join(', ')}`);
    const description = (input.description ?? '').trim();
    if (
      Array.from(description).length > MAIL_ROLE_DESCRIPTION_MAX_LENGTH ||
      /\p{Cc}/u.test(description)
    ) {
      addField(
        fields,
        'description',
        `must be at most ${MAIL_ROLE_DESCRIPTION_MAX_LENGTH} characters without control characters`
      );
    }
    const owner = input.owner_user_id ?? null;
    if (owner !== null && this.dependencies.membershipOf(organizationId, owner) === null) {
      addField(fields, 'owner_user_id', 'must be a member of the organization');
    }
    if (Object.keys(fields).length > 0) return { ok: false, error: 'validation', fields };
    return {
      ok: true,
      value: { name, color: color as MailLabelColor, description, owner_user_id: owner },
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

  private directoryEntry(state: MailboxState): MailboxDirectoryEntry {
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
  private sentLast24h(state: MailboxState): number {
    const since = this.dependencies.now().getTime() - dayMilliseconds;
    let sent = 0;
    for (const message of state.messages.values()) {
      if (message.direction === 'outbound' && Date.parse(message.created_at) >= since) sent += 1;
    }
    return sent;
  }
}

/** Quotes a CSV field the way Go's encoding/csv does. */
function csvField(value: string): string {
  if (value === '' || !/[",\r\n]|^[ \t]/u.test(value)) return value;
  return `"${value.replaceAll('"', '""')}"`;
}

export const mockMailboxStore = new MockMailboxStore();
mockOrganizationStore.onMemberJoined((organizationId, userId, email) => {
  mockMailboxStore.claimReserved(organizationId, userId, email);
});

function summaryOf(message: MailMessage): MailMessageSummary {
  return {
    ...logSummaryOf(message),
    placement: message.placement,
    folder_id: message.folder_id,
    label_ids: [...message.label_ids],
    starred: message.starred,
    thread_id: message.thread_id,
    trashed_at: message.trashed_at,
  };
}

/** A log entry carries neither placements, stars, nor threads. */
function logSummaryOf(message: MailMessage): Omit<MailLogEntry, 'mailbox_address'> {
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
    // As in the API: the connector id for a synced copy, `zmail` for Zmail's own mail.
    source: message.source ?? 'zmail',
    template_id: message.template_id,
    created_at: message.created_at,
  };
}

/** `message` is as the viewer sees it; `needle` is the trimmed, lowercased search. */
function matchesFilters(
  message: MailMessage,
  filters: MailMessageFilters,
  needle: string
): boolean {
  const unread = message.direction === 'inbound' && !message.read;
  return (
    (filters.placement === undefined
      ? message.placement !== 'trash'
      : message.placement === filters.placement) &&
    (filters.folderId === undefined || message.folder_id === filters.folderId) &&
    (filters.direction === undefined || message.direction === filters.direction) &&
    (filters.labelId === undefined || message.label_ids.includes(filters.labelId)) &&
    (filters.starred === undefined || message.starred === filters.starred) &&
    (filters.unread === undefined || unread === filters.unread) &&
    (filters.hasAttachments === undefined ||
      message.attachment_count > 0 === filters.hasAttachments) &&
    (needle === '' || matchesMailboxSearch(message, needle))
  );
}

/** Subject, sender address and name, `to` and `cc`, and the text body; `%` and `_` are literal. */
function matchesMailboxSearch(message: MailMessage, needle: string): boolean {
  return [
    message.subject,
    message.from.address,
    message.from.name,
    ...message.to,
    ...message.cc,
    message.text_body,
  ].some(text => text.toLowerCase().includes(needle));
}

/** One row per thread: its newest message in the view, carrying the thread's summary there. */
function threadRows(messages: MailMessage[]): MailMessageSummary[] {
  const threads = new Map<number, MailMessage[]>();
  for (const message of messages) {
    threads.set(message.thread_id, [...(threads.get(message.thread_id) ?? []), message]);
  }
  return [...threads.values()]
    .map(members => {
      const ascending = [...members].sort((left, right) => left.id - right.id);
      const newest = ascending[ascending.length - 1];
      const thread: MailThreadSummary = {
        message_count: ascending.length,
        message_ids: ascending.slice(-MAIL_THREAD_MAX_MESSAGES).map(message => message.id),
        unread: ascending.some(message => message.direction === 'inbound' && !message.read),
        starred: ascending.some(message => message.starred),
        label_ids: [...new Set(ascending.flatMap(message => message.label_ids))].sort(
          (left, right) => left - right
        ),
      };
      return { ...summaryOf(newest), thread };
    })
    .sort((left, right) => right.id - left.id);
}

/** The viewer's day in a zone, as UTC milliseconds: `start` inclusive, `end` exclusive. */
interface ViewerDay {
  start: number;
  end: number;
}

/**
 * `message` is as the viewer sees it; see `listInbox` for the views. `needle` is the lowercased
 * search, which replaces the view; `day` is needed for `today` only.
 */
function inInboxView(
  message: MailMessage,
  query: Pick<MockInboxQuery, 'view' | 'unread' | 'hasAttachments'>,
  needle?: string,
  day?: ViewerDay
): boolean {
  const unread = message.direction === 'inbound' && !message.read;
  if (query.unread !== undefined && unread !== query.unread) return false;
  if (
    query.hasAttachments !== undefined &&
    message.attachment_count > 0 !== query.hasAttachments
  ) {
    return false;
  }
  if (needle !== undefined) {
    return message.placement !== 'trash' && matchesSearch(message, needle);
  }
  switch (query.view) {
    case 'all':
      return message.direction === 'inbound' && message.placement === 'default';
    case 'sent':
      return message.direction === 'outbound' && message.placement === 'default';
    case 'today': {
      const at = Date.parse(message.created_at);
      return (
        day !== undefined &&
        message.direction === 'inbound' &&
        message.placement !== 'trash' &&
        at >= day.start &&
        at < day.end
      );
    }
    case 'pending':
      return message.starred && message.placement !== 'archive' && message.placement !== 'trash';
    default:
      return false;
  }
}

/**
 * The day `now` falls on in `zone`, from its midnight to the next one. Like the API, a day can be
 * 23 or 25 hours long where the clocks change.
 */
function viewerDay(now: Date, zone: string): ViewerDay {
  const parts = new Intl.DateTimeFormat('en-US', {
    timeZone: zone,
    year: 'numeric',
    month: 'numeric',
    day: 'numeric',
  }).formatToParts(now);
  const part = (type: 'year' | 'month' | 'day') =>
    Number(parts.find(item => item.type === type)?.value);
  const [year, month, day] = [part('year'), part('month'), part('day')];
  return {
    start: zonedMidnight(year, month, day, zone),
    end: zonedMidnight(year, month, day + 1, zone),
  };
}

/** The UTC instant of 00:00 on the calendar day `YYYY-MM-DD` plus `days`, in `zone`. */
function dayStart(date: string, zone: string, days: number): number {
  const [year, month, day] = date.split('-').map(Number);
  return zonedMidnight(year, month, day + days, zone);
}

/** The UTC instant of 00:00 on a calendar day in `zone`; `day` may overflow into the next month. */
function zonedMidnight(year: number, month: number, day: number, zone: string): number {
  const local = Date.UTC(year, month - 1, day);
  // Two rounds settle the zone's offset at that midnight, also next to a clock change.
  let instant = local - zoneOffset(local, zone);
  instant = local - zoneOffset(instant, zone);
  return instant;
}

/** How far `zone`'s wall clock is ahead of UTC at `instant`, in milliseconds. */
function zoneOffset(instant: number, zone: string): number {
  const parts = new Intl.DateTimeFormat('en-US', {
    timeZone: zone,
    hourCycle: 'h23',
    year: 'numeric',
    month: 'numeric',
    day: 'numeric',
    hour: 'numeric',
    minute: 'numeric',
    second: 'numeric',
  }).formatToParts(new Date(instant));
  const part = (type: Intl.DateTimeFormatPartTypes) =>
    Number(parts.find(item => item.type === type)?.value);
  const wall = Date.UTC(
    part('year'),
    part('month') - 1,
    part('day'),
    part('hour'),
    part('minute'),
    part('second')
  );
  return wall - Math.floor(instant / 1_000) * 1_000;
}

/** A UTC instant as the API writes it: seconds, then `Z`. */
function apiTimestamp(milliseconds: number): string {
  return new Date(milliseconds).toISOString().replace(/\.\d{3}Z$/u, 'Z');
}

/**
 * As the API cuts it: the first 400 characters of the text body, whitespace runs collapsed to one
 * space, trimmed, then at most 140 characters.
 */
function inboxSnippet(textBody: string): string {
  const collapsed = Array.from(textBody)
    .slice(0, 400)
    .join('')
    .split(/\s+/u)
    .filter(Boolean)
    .join(' ');
  const characters = Array.from(collapsed);
  return characters.length <= 140 ? collapsed : characters.slice(0, 140).join('').trimEnd();
}

function unixNanoseconds(timestamp: string): bigint {
  return BigInt(Date.parse(timestamp)) * nanosecondsPerMillisecond;
}

/** The listing's filters as the pagination links repeat them, ready for `page=`. */
function filterQuery(filters: MailMessageFilters): string {
  const query = new URLSearchParams();
  if (filters.direction) query.set('direction', filters.direction);
  if (filters.placement) query.set('placement', filters.placement);
  if (filters.folderId !== undefined) query.set('folder_id', String(filters.folderId));
  if (filters.labelId !== undefined) query.set('label_id', String(filters.labelId));
  if (filters.starred !== undefined) query.set('starred', String(filters.starred));
  if (filters.unread !== undefined) query.set('unread', String(filters.unread));
  if (filters.hasAttachments !== undefined) {
    query.set('has_attachments', String(filters.hasAttachments));
  }
  if (filters.q !== undefined) query.set('q', filters.q);
  if (filters.group) query.set('group', filters.group);
  return query.size > 0 ? `${query.toString()}&` : '';
}

/** The body rules of a message change, after access and lookups, keyed as the API keys them. */
function changeErrors(
  messageIds: number[],
  change: MockMessageChange,
  batch: boolean,
  shared: boolean
): Record<string, string[]> {
  const fields: Record<string, string[]> = {};
  if (batch) checkIdList(fields, 'message_ids', messageIds, 1);
  if (!shared && change.read === undefined && change.starred === undefined) {
    addField(fields, 'change', 'at least one change is required');
  }
  if (change.placement !== undefined && !isPlacement(change.placement)) {
    addField(fields, 'placement', `must be one of: ${MAIL_PLACEMENTS.join(', ')}`);
  }
  const inFolder = change.placement === 'folder';
  if (inFolder && change.folder_id === undefined) {
    addField(fields, 'folder_id', 'is required with placement folder');
  } else if (!inFolder && change.folder_id !== undefined) {
    addField(fields, 'folder_id', 'is allowed only with placement folder');
  } else if (change.folder_id === 0) {
    addField(fields, 'folder_id', 'must be a positive id');
  }
  if (change.add_label_ids) checkIdList(fields, 'add_label_ids', change.add_label_ids, 1);
  if (change.remove_label_ids) checkIdList(fields, 'remove_label_ids', change.remove_label_ids, 1);
  if (change.label_ids) checkIdList(fields, 'label_ids', change.label_ids, 0);
  if (change.add_label_ids?.some(id => change.remove_label_ids?.includes(id))) {
    addField(fields, 'add_label_ids', 'a label may not be both added and removed');
  }
  return fields;
}

/** `minItems` to 100 different positive ids. */
function checkIdList(
  fields: Record<string, string[]>,
  field: string,
  ids: number[],
  minItems: number
): void {
  if (ids.length < minItems || ids.length > MAIL_MESSAGE_UPDATES_MAX_IDS) {
    addField(fields, field, `must hold ${minItems} to ${MAIL_MESSAGE_UPDATES_MAX_IDS} ids`);
  }
  if (positiveDistinct(ids).length !== ids.length) {
    addField(fields, field, 'must hold different positive ids');
  }
}

function addField(fields: Record<string, string[]>, field: string, message: string): void {
  fields[field] = [...(fields[field] ?? []), message];
}

/** Drops zero and repeated ids, keeping the first occurrence of each. */
function positiveDistinct(ids: number[]): number[] {
  return [...new Set(ids.filter(id => id > 0))];
}

function isPlacement(value: string): value is MailPlacement {
  return (MAIL_PLACEMENTS as readonly string[]).includes(value);
}

function isLabelColor(value: string): value is MailLabelColor {
  return (MAIL_LABEL_COLORS as readonly string[]).includes(value);
}

function toggle(map: Map<number, Set<number>>, messageId: number, userId: number, on: boolean) {
  const users = map.get(messageId) ?? new Set<number>();
  if (on) users.add(userId);
  else users.delete(userId);
  map.set(messageId, users);
}

/** A folder or label name trimmed, then 1 to `maxLength` characters. */
function organizeName(fields: Record<string, string[]>, raw: string, maxLength: number): string {
  const name = raw.trim();
  const length = Array.from(name).length;
  if (length < 1 || length > maxLength) {
    addField(fields, 'name', `must be 1 to ${maxLength} characters after trimming`);
  }
  return name;
}

/** Names are unique in the mailbox ignoring case; an item may keep its own name. */
function nameTaken(
  items: Map<number, { id: number; name: string }>,
  name: string,
  exceptId?: number
): boolean {
  const lowered = name.toLowerCase();
  return [...items.values()].some(
    item => item.id !== exceptId && item.name.toLowerCase() === lowered
  );
}

/** By name ignoring case, then by id. */
function sortedByName<T extends { id: number; name: string }>(items: Map<number, T>): T[] {
  return [...items.values()].sort((left, right) => {
    const a = left.name.toLowerCase();
    const b = right.name.toLowerCase();
    return a < b ? -1 : a > b ? 1 : left.id - right.id;
  });
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

/** Someone who loses access loses their read records and stars with it. */
function forgetReads(state: MailboxState, userId: number): void {
  for (const readers of state.reads.values()) readers.delete(userId);
  for (const starrers of state.stars.values()) starrers.delete(userId);
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
  const kind = input.kind ?? 'standard';
  const transactionalFields =
    input.key !== undefined ||
    input.from_address !== undefined ||
    input.from_name !== undefined ||
    input.variable_definitions !== undefined;
  if (kind === 'standard' && transactionalFields) return null;
  if (
    kind === 'transactional' &&
    (!input.key || !input.from_address || !input.variable_definitions)
  ) {
    return null;
  }
  return {
    name,
    subject,
    html_body: html,
    text_body: text,
    sample_variables: samples,
    variables,
    design: input.design ?? null,
    kind,
    key: kind === 'transactional' ? (input.key ?? null) : null,
    from:
      kind === 'transactional' && input.from_address
        ? { address: input.from_address.toLowerCase(), name: input.from_name ?? '' }
        : null,
    variable_definitions: kind === 'transactional' ? (input.variable_definitions ?? []) : [],
    outside_sending_scope: false,
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
