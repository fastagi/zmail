import { encodeInboxCursor } from '@/features/mailbox/server/inbox-cursor';
import type {
  InboxCounts,
  InboxItem,
  InboxPage,
  MailDayStats,
  MailLogEntry,
  MailLogFilters,
  MailLogPage,
  MailMessage,
  MailMessageFilters,
  MailMessagePage,
  MailMessageSummary,
} from '@/features/mailbox/types';
import {
  dayOf,
  filterQuery,
  logSummaryOf,
  matchesFilters,
  matchesSearch,
  paginated,
  summaryOf,
  threadRows,
} from '@/features/mailbox/server/mock-store/helpers';
import type { MockMailboxState } from '@/features/mailbox/server/mock-store/state';
import {
  DAY_MILLISECONDS,
  NANOSECONDS_PER_MILLISECOND,
  type MailboxViewer,
  type MockInboxQuery,
  type MockOrganizeResult,
  type ViewerDay,
} from '@/features/mailbox/server/mock-store/types';

/**
 * One page of a mailbox listing. `filters` were checked by the route; this judges what needs
 * the mailbox: the folder and label must be its own, and a one-character search is too broad.
 */
export function listMailboxMessages(
  ctx: MockMailboxState,
  organizationId: number,
  mailboxId: number,
  viewer: MailboxViewer,
  filters: MailMessageFilters,
  page: number,
  perPage: number
): MockOrganizeResult<MailMessagePage> {
  const found = ctx.authorize(organizationId, mailboxId, viewer, 'view');
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

  const matching = ctx
    .visibleMessages(state)
    .map(message => ctx.forViewer(state, message, viewer))
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
export function listMailLog(
  ctx: MockMailboxState,
  organizationId: number,
  viewer: MailboxViewer,
  filters: MailLogFilters,
  page: number,
  perPage: number
): MailLogPage {
  const needle = filters.q?.trim().toLowerCase() ?? '';
  const entries: MailLogEntry[] = [];
  for (const state of ctx.readableStates(organizationId, viewer)) {
    if (filters.mailboxId !== undefined && state.mailbox.id !== filters.mailboxId) continue;
    for (const message of ctx.visibleMessages(state)) {
      if (filters.direction && message.direction !== filters.direction) continue;
      if (filters.status && message.status !== filters.status) continue;
      if (needle !== '' && !matchesSearch(message, needle)) continue;
      entries.push({
        ...logSummaryOf(ctx.forViewer(state, message, viewer)),
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
export function listUnifiedInbox(
  ctx: MockMailboxState,
  organizationId: number,
  viewer: MailboxViewer,
  query: MockInboxQuery
): MockOrganizeResult<InboxPage> {
  const needle = query.q?.trim().toLowerCase();
  if (needle !== undefined && Array.from(needle).length === 1) {
    return { ok: false, error: 'search_too_broad' };
  }
  const day = viewerDay(ctx.dependencies.now(), query.tz);
  const since = query.since === undefined ? -Infinity : dayStart(query.since, query.tz, 0);
  const until = query.until === undefined ? Infinity : dayStart(query.until, query.tz, 1);
  const rows: { item: InboxItem; createdAt: bigint }[] = [];
  for (const state of ctx.readableStates(organizationId, viewer)) {
    if (query.mailboxId !== undefined && state.mailbox.id !== query.mailboxId) continue;
    if (
      query.roleId !== undefined &&
      state.roleId !== (query.roleId === 'none' ? null : query.roleId)
    ) {
      continue;
    }
    const matching = ctx
      .visibleMessages(state)
      .map(message => ctx.forViewer(state, message, viewer))
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
export function getUnifiedInboxCounts(
  ctx: MockMailboxState,
  organizationId: number,
  viewer: MailboxViewer,
  tz: string
): InboxCounts {
  const day = viewerDay(ctx.dependencies.now(), tz);
  const views = { all_unread: 0, today: 0, today_unread: 0, pending: 0, sent_waiting: 0 };
  const mailboxes: InboxCounts['mailboxes'] = [];
  const roles = new Map<number, number>();
  let ungrouped = 0;
  const states = ctx
    .readableStates(organizationId, viewer)
    .sort((left, right) => left.mailbox.id - right.mailbox.id);
  for (const state of states) {
    let unread = 0;
    for (const stored of ctx.visibleMessages(state)) {
      const message = ctx.forViewer(state, stored, viewer);
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
export function getMailStats(
  ctx: MockMailboxState,
  organizationId: number,
  viewer: MailboxViewer,
  days: number
): MailDayStats[] {
  const now = ctx.dependencies.now();
  const today = Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), now.getUTCDate());
  const entries = Array.from({ length: days }, (_, index): MailDayStats => ({
    day: dayOf(today - (days - 1 - index) * DAY_MILLISECONDS),
    sent: 0,
    failed: 0,
    received: 0,
  }));
  const byDay = new Map(entries.map(entry => [entry.day, entry]));
  for (const state of ctx.readableStates(organizationId, viewer)) {
    for (const message of ctx.visibleMessages(state)) {
      const entry = byDay.get(message.created_at.slice(0, 10));
      if (!entry) continue;
      if (message.direction === 'inbound') entry.received += 1;
      else if (message.status === 'sent') entry.sent += 1;
      else if (message.status === 'failed') entry.failed += 1;
    }
  }
  return entries;
}

/**
 * `message` is as the viewer sees it; see `listUnifiedInbox` for the views. `needle` is the
 * lowercased search, which replaces the view; `day` is needed for `today` only.
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
  return BigInt(Date.parse(timestamp)) * NANOSECONDS_PER_MILLISECOND;
}
