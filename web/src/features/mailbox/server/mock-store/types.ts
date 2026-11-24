import type { InboxPosition } from '@/features/mailbox/server/inbox-cursor';
import type {
  InboxFilters,
  MailFolder,
  MailLabel,
  MailMessage,
  MailRole,
  MailTemplate,
  Mailbox,
  MailboxAccessEvent,
  MailboxDirectoryEntry,
  MailboxMember,
  MailboxType,
  MemberImportInput,
} from '@/features/mailbox/types';
import type { OrganizationRole } from '@/features/organization/types';

export const MOCK_MAILBOX_DOMAIN = 'mail.example.com';
export const MOCK_DAILY_SEND_LIMIT = 200;

export const DAY_MILLISECONDS = 24 * 60 * 60 * 1_000;
export const MAX_MOCK_ORGANIZATIONS = 1_000;
export const MAX_MOCK_MESSAGES_PER_MAILBOX = 500;
export const MAX_MOCK_ACCESS_EVENTS_PER_MAILBOX = 1_000;
export const MAX_MOCK_EXPORT_ROWS = 5_000;
export const EXPORT_HEADER = 'address,display_name,members,member_count,sent_last_24h,created_at';
/** The longest email an organization invitation stores, as in the API. */
export const MAX_ROSTER_EMAIL_LENGTH = 100;
export const ROSTER_EMAIL_PATTERN = /^[^\s@<>(),;:"]+@[^\s@<>(),;:"]+\.[^\s@<>(),;:"]+$/u;
export const NANOSECONDS_PER_MILLISECOND = BigInt(1_000_000);

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

export interface MailboxAccessState {
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

export interface StoredMailbox {
  id: number;
  address: string;
  display_name: string;
  created_at: string;
}

export interface MailboxState {
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

/** A mail role as stored: the owner as set, the mailboxes found from `MailboxState.roleId`. */
export type StoredRole = Omit<MailRole, 'mailbox_ids'>;

export type MockMailboxCreateResult =
  | { ok: true; mailbox: Mailbox }
  | { ok: false; error: 'invalid' | 'reserved' | 'taken' | 'limit' | 'access_invalid' };

/** No access at all reads as a missing mailbox; some access but not the needed one is `forbidden`. */
export type MockAccessDenied = 'mailbox_not_found' | 'forbidden';

export type MockMailMessageResult =
  | { ok: true; message: MailMessage }
  | { ok: false; error: MockAccessDenied | 'not_found' };

export type MockMailMessagePageResult =
  | { ok: true; page: import('@/features/mailbox/types').MailMessagePage }
  | { ok: false; error: MockAccessDenied };

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
  | { ok: true }
  | { ok: false; error: MockAccessDenied | 'not_found' };

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

export interface MockRosterInviter {
  pendingInvitationIdFor: (organizationId: number, email: string) => number | null;
  inviteRosterMember: (
    organizationId: number,
    email: string,
    role: 'member' | 'admin',
    features: MemberImportInput['features']
  ) => { id: number; email_send_status: 'not_configured' };
}

export interface MockMailboxStoreDependencies {
  now: () => Date;
  /** The membership id of a current member of the organization, or null. */
  membershipOf: (organizationId: number, userId: number) => number | null;
  /** Account emails of the organization's current members by user id, lowercased. */
  memberEmails: (organizationId: number) => Map<number, string>;
  /** The organization half of a roster import: pending invitations and new ones. */
  rosterInviter: MockRosterInviter;
}

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

/** A role write as the route shape-checked it; the values are judged by the store. */
export interface MockMailRoleInput {
  name: string;
  color?: string;
  description?: string;
  owner_user_id?: number | null;
}

/** One unified inbox page as the route validated it. */
export interface MockInboxQuery extends InboxFilters {
  after?: InboxPosition;
  perPage: number;
}

/** The viewer's day in a zone, as UTC milliseconds: `start` inclusive, `end` exclusive. */
export interface ViewerDay {
  start: number;
  end: number;
}
