import { readStoredMailboxId } from '@/features/mailbox/components/mailbox-params';
import type { InboxRoleFilter } from '@/features/mailbox/components/inbox-sidebar';
import type { MailOrganizationScope } from '@/features/mailbox/hooks/use-mail-organization';
import {
  INBOX_ASSIGNMENTS,
  INBOX_DATE_RANGES,
  INBOX_VIEWS,
  MAIL_SEARCH_MAX_LENGTH,
} from '@/features/mailbox/schemas';
import {
  INBOX_TRIAGE_TABS,
  triageTabCategories,
  type InboxTriageTab,
} from '@/features/mailbox/triage-tabs';
import type {
  InboxAssignment,
  InboxCounts,
  InboxFilters,
  InboxView,
  Mailbox,
} from '@/features/mailbox/types';
import { ApiErrorCode } from '@/http/codes';
import { ApiError } from '@/http/request';

/** Query parameter naming the open row of the unified inbox, so a reload keeps it open. */
export const OPEN_PARAM = 'open';
/**
 * The URL state of the unified inbox (D20): the view, one mailbox or one mail role (`none` for the
 * mailboxes without one), the search, the Unread tab.
 */
export const VIEW_PARAM = 'view';
export const BOX_PARAM = 'box';
export const ROLE_PARAM = 'role';
export const SEARCH_PARAM = 'q';
export const TAB_PARAM = 'tab';
export const TRIAGE_PARAM = 'triage';
export const DATE_PARAM = 'date';
/** `new=mailbox`: the team was just created, so mailbox creation opens at once. */
export const NEW_PARAM = 'new';
export const NEW_TYPE_PARAM = 'type';

export const UNREAD_TAB = 'unread';
export const ATTACHMENTS_TAB = 'attachments';
export const NO_ROLE = 'none';
export const filterParams = [
  VIEW_PARAM,
  BOX_PARAM,
  ROLE_PARAM,
  SEARCH_PARAM,
  TAB_PARAM,
  TRIAGE_PARAM,
  DATE_PARAM,
] as const;
export const rowIdPattern = /^[1-9]\d{0,15}$/u;
export const fallbackTimeZone = 'UTC';

export type InboxDateRange = (typeof INBOX_DATE_RANGES)[number];
export type ReadyScope = Extract<MailOrganizationScope, { status: 'ready' }>;

/** What the URL asks the inbox to list. */
export interface InboxUrlState {
  view: InboxView;
  box: number | null;
  role: InboxRoleFilter | null;
  q: string;
  unread: boolean;
  /** The With attachments tab: only messages with files. */
  attachments: boolean;
  /** The AI triage tab; it applies only to `all` and `today` without a search. */
  triage: InboxTriageTab | null;
  /** The assignment tab of shared conversations. */
  assignment: InboxAssignment | null;
  /** How far back the list reaches, in the inbox's zone. */
  date: InboxDateRange | null;
}

/** The calendar day `YYYY-MM-DD` that is `days` before today in `tz`. */
function daysAgo(tz: string, days: number): string {
  const parts = new Intl.DateTimeFormat('en-US', {
    year: 'numeric',
    month: 'numeric',
    day: 'numeric',
    timeZone: tz,
  }).formatToParts(new Date());
  const part = (type: 'year' | 'month' | 'day') =>
    Number(parts.find(item => item.type === type)?.value);
  const [year, month, day] = [part('year'), part('month'), part('day')];
  return new Date(Date.UTC(year, month - 1, day - days)).toISOString().slice(0, 10);
}

/** The `since` or `until` day of a date range; the past week includes today and six days back. */
function dateBounds(range: InboxDateRange, tz: string): Pick<InboxFilters, 'since' | 'until'> {
  switch (range) {
    case 'week':
      return { since: daysAgo(tz, 6) };
    case 'month':
      return { since: daysAgo(tz, 29) };
    case 'year':
      return { since: daysAgo(tz, 364) };
    case 'older':
      return { until: daysAgo(tz, 365) };
  }
}

/**
 * The inbox's URL state; a value it cannot use falls back to the default without a word. A
 * search lists `all`, the only view it combines with.
 */
export function readInboxUrl(params: URLSearchParams): InboxUrlState {
  const rawSearch = params.get(SEARCH_PARAM) ?? '';
  const q =
    Array.from(rawSearch).length <= MAIL_SEARCH_MAX_LENGTH && !/\p{Cc}/u.test(rawSearch)
      ? rawSearch.trim()
      : '';
  const rawView = params.get(VIEW_PARAM) ?? '';
  const view =
    q === '' && (INBOX_VIEWS as readonly string[]).includes(rawView)
      ? (rawView as InboxView)
      : 'all';
  const rawBox = params.get(BOX_PARAM) ?? '';
  const rawRole = params.get(ROLE_PARAM) ?? '';
  let role: InboxRoleFilter | null = null;
  if (rawRole === NO_ROLE) role = NO_ROLE;
  else if (rowIdPattern.test(rawRole)) role = Number(rawRole);
  const rawTriage = params.get(TRIAGE_PARAM) ?? '';
  const triage =
    q === '' &&
    (view === 'all' || view === 'today') &&
    (INBOX_TRIAGE_TABS as readonly string[]).includes(rawTriage)
      ? (rawTriage as InboxTriageTab)
      : null;
  return {
    view,
    box: rowIdPattern.test(rawBox) ? Number(rawBox) : null,
    role,
    q,
    unread: params.get(TAB_PARAM) === UNREAD_TAB,
    attachments: params.get(TAB_PARAM) === ATTACHMENTS_TAB,
    triage,
    assignment: (INBOX_ASSIGNMENTS as readonly string[]).includes(params.get(TAB_PARAM) ?? '')
      ? (params.get(TAB_PARAM) as InboxAssignment)
      : null,
    date: (INBOX_DATE_RANGES as readonly string[]).includes(params.get(DATE_PARAM) ?? '')
      ? (params.get(DATE_PARAM) as InboxDateRange)
      : null,
  };
}

/**
 * The list query for the URL state. `all` and `sent` group by thread, except for the Unread tab,
 * a search, and an AI triage tab, which list single messages. The triage tab applies only while
 * the organization's triage is on.
 */
export function inboxFilters(
  { view, box, role, q, unread, attachments, triage, assignment, date }: InboxUrlState,
  tz: string,
  triageOn: boolean
): InboxFilters {
  const triageTab = triageOn ? triage : null;
  const grouped =
    !unread &&
    !attachments &&
    date === null &&
    q === '' &&
    triageTab === null &&
    (view === 'all' || view === 'sent');
  return {
    view,
    ...(role === null ? {} : { roleId: role }),
    ...(box === null ? {} : { mailboxId: box }),
    ...(unread ? { unread: true } : {}),
    ...(attachments ? { hasAttachments: true } : {}),
    ...(q === '' ? {} : { q }),
    ...(grouped ? { group: 'thread' as const } : {}),
    ...(triageTab === null ? {} : { triage: triageTabCategories(triageTab) }),
    ...(assignment === null ? {} : { assignment }),
    ...(date === null ? {} : dateBounds(date, tz)),
    tz,
  };
}

/** The unread number the counts hold for this listing, where they hold one. */
export function listingUnread(
  counts: InboxCounts | undefined,
  state: InboxUrlState
): number | undefined {
  if (!counts || state.q !== '') return undefined;
  if (state.box !== null) {
    if (state.view !== 'all' || state.role !== null) return undefined;
    return counts.mailboxes.find(item => item.mailbox_id === state.box)?.unread;
  }
  if (state.role !== null) {
    if (state.view !== 'all') return undefined;
    if (state.role === NO_ROLE) return counts.ungrouped_unread;
    const role = state.role;
    return counts.roles.find(item => item.role_id === role)?.unread ?? 0;
  }
  if (state.view === 'all') return counts.views.all_unread;
  return state.view === 'today' ? counts.views.today_unread : undefined;
}

/** The inbox refused the query: a filter of the URL, or else the zone, is not one it takes. */
export function isRejectedQuery(error: unknown): boolean {
  return (
    error instanceof ApiError &&
    error.status === 400 &&
    error.errorCode === ApiErrorCode.COMMON_INVALID_INPUT
  );
}

export function isSearchTooBroad(error: unknown): boolean {
  return error instanceof ApiError && error.errorCode === ApiErrorCode.MAILBOX_SEARCH_TOO_BROAD;
}

/**
 * The sender of a new message: the narrowed mailbox when it may send, else the mailbox last
 * opened, else the first that may (D26). `sendable` is not empty.
 */
export function newMessageSender(
  sendable: readonly Mailbox[],
  box: number | null,
  organizationId: number
): Mailbox {
  const stored = readStoredMailboxId(organizationId);
  return (
    sendable.find(mailbox => mailbox.id === box) ??
    sendable.find(mailbox => mailbox.id === stored) ??
    sendable[0]
  );
}
