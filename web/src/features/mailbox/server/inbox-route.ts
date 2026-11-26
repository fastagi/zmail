import 'server-only';

import { NextResponse } from 'next/server';
import { apiCursorPageResponse, apiPaginatedResponse } from '@/app/api/_shared/success-response';
import {
  INBOX_TIME_ZONE_MAX_LENGTH,
  INBOX_ASSIGNMENTS,
  INBOX_VIEWS,
  MAIL_BODY_MAX_BYTES,
  MAIL_LOG_QUERY_MAX_LENGTH,
  MAIL_PLACEMENTS,
  MAIL_SEARCH_MAX_LENGTH,
  MAIL_MESSAGE_ID_MAX_LENGTH,
  MAIL_PAGE_MAX_SIZE,
  MAIL_STATS_DEFAULT_DAYS,
  MAIL_STATS_MAX_DAYS,
  MAIL_SUBJECT_MAX_LENGTH,
  MAIL_TRIAGE_CATEGORIES,
  isInboxDay,
} from '@/features/mailbox/schemas';
import { decodeInboxCursor, encodeInboxCursor } from '@/features/mailbox/server/inbox-cursor';
import { mockAssignmentStore } from '@/features/mailbox/server/mock-assignment-store';
import { mockTriageStore } from '@/features/mailbox/server/mock-triage-store';
import {
  mockMailboxStore,
  type MockInboxQuery,
} from '@/features/mailbox/server/mock-mailbox-store';
import type {
  InboxAssignment,
  InboxView,
  MailDirection,
  MailLogFilters,
  MailMessageFilters,
  MailPlacement,
  MailStatus,
  MailTriageCategory,
  SendMailMessageInput,
} from '@/features/mailbox/types';
import { forwardAuthenticatedGoApi } from '@/server/api-adapter/authenticated-route';
import {
  type Parsed,
  defaultPerPage,
  maxSendBodyBytes,
  resolveMailboxReadRoute,
  resolveMailboxMutationRoute,
  viewerOf,
  jsonObject,
  boundedString,
  addressList,
  parseRouteId,
  isOneOf,
  positiveInteger,
  privateMailboxHeaders,
  success,
  invalidInput,
  validationFailed,
  variableMap,
  attachmentList,
  mockSendResponse,
  organizeFailure,
} from './route-shared';

export async function listMailMessagesRoute(
  request: Request,
  rawMailboxId: string
): Promise<NextResponse> {
  const target = await resolveMailboxReadRoute(request);
  if (!target.available) return target.response;
  const mailboxId = parseRouteId(rawMailboxId);
  if (!mailboxId.ok) return mailboxId.response;
  const query = new URL(request.url).searchParams;
  const filters = parseMessageFilters(query);
  if (!filters.ok) return filters.response;
  const page = positiveInteger(query.get('page'), 1);
  const perPage = Math.min(
    positiveInteger(query.get('per_page'), defaultPerPage),
    MAIL_PAGE_MAX_SIZE
  );

  if (target.backend === 'go-api') {
    // Only the documented parameters reach the API; anything else in the query is dropped.
    const searchParams = new URLSearchParams({ page: String(page), per_page: String(perPage) });
    for (const [name, value] of messageFilterParams(filters.value)) searchParams.set(name, value);
    return forwardAuthenticatedGoApi(request, {
      method: 'GET',
      path: `mailboxes/${mailboxId.value}/messages`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      searchParams,
    });
  }
  const result = mockMailboxStore.listMessages(
    target.organizationId,
    mailboxId.value,
    viewerOf(target),
    filters.value,
    page,
    perPage
  );
  if (!result.ok) return organizeFailure(result);
  const { items, meta, links } = result.value;
  return apiPaginatedResponse(items, meta, links, privateMailboxHeaders());
}

/** The log: every mailbox of the organization, filtered by the whitelisted parameters only. */
export async function listMailLogRoute(request: Request): Promise<NextResponse> {
  const target = await resolveMailboxReadRoute(request);
  if (!target.available) return target.response;
  const query = new URL(request.url).searchParams;
  const filters = parseLogFilters(query);
  if (!filters.ok) return filters.response;
  const page = positiveInteger(query.get('page'), 1);
  const perPage = Math.min(
    positiveInteger(query.get('per_page'), defaultPerPage),
    MAIL_PAGE_MAX_SIZE
  );

  if (target.backend === 'go-api') {
    // Only these six parameters reach the API; anything else in the query is dropped.
    const searchParams = new URLSearchParams({ page: String(page), per_page: String(perPage) });
    const { direction, status, mailboxId, q } = filters.value;
    if (direction) searchParams.set('direction', direction);
    if (status) searchParams.set('status', status);
    if (mailboxId !== undefined) searchParams.set('mailbox_id', String(mailboxId));
    if (q) searchParams.set('q', q);
    return forwardAuthenticatedGoApi(request, {
      method: 'GET',
      path: 'mail-messages',
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      searchParams,
    });
  }
  const result = mockMailboxStore.listLog(
    target.organizationId,
    viewerOf(target),
    filters.value,
    page,
    perPage
  );
  return apiPaginatedResponse(result.items, result.meta, result.links, privateMailboxHeaders());
}

/**
 * The unified inbox: one cursor page across the mailboxes the caller reads. The query is checked
 * here as the API checks it, so a malformed one never leaves the BFF, and only the nine documented
 * parameters reach the API.
 */
export async function listInboxMessagesRoute(request: Request): Promise<NextResponse> {
  const target = await resolveMailboxReadRoute(request);
  if (!target.available) return target.response;
  const query = parseInboxQuery(new URL(request.url).searchParams);
  if (!query.ok) return query.response;

  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'GET',
      path: 'inbox-messages',
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      searchParams: query.value.forward,
    });
  }
  const { triage, assignment } = query.value;
  if (!triage && !assignment) {
    const page = mockMailboxStore.listInbox(
      target.organizationId,
      viewerOf(target),
      query.value.mock
    );
    if (!page.ok) return organizeFailure(page);
    return apiCursorPageResponse(
      mockTriageStore.decorate(target.organizationId, page.value.items),
      page.value.meta,
      privateMailboxHeaders()
    );
  }
  // The triage and assignment filters apply before paging: every row after the cursor, then the
  // page of matches. A conversation never assigned is open and unassigned.
  const { perPage } = query.value.mock;
  const all = mockMailboxStore.listInbox(target.organizationId, viewerOf(target), {
    ...query.value.mock,
    perPage: Number.MAX_SAFE_INTEGER - 1,
  });
  if (!all.ok) return organizeFailure(all);
  const userId = target.context.user_id;
  const matching = all.value.items.filter(item => {
    if (triage && !mockTriageStore.matches(target.organizationId, item, triage)) return false;
    if (!assignment) return true;
    const state = mockAssignmentStore.read(
      target.organizationId,
      item.mailbox_id,
      item.thread_id,
      []
    );
    if (assignment === 'closed') return state.status === 'closed';
    if (state.status !== 'open') return false;
    return assignment === 'mine'
      ? state.assignee_user_id === userId
      : state.assignee_user_id === null;
  });
  const items = mockTriageStore.decorate(target.organizationId, matching.slice(0, perPage));
  const hasMore = matching.length > perPage;
  const last = items.at(-1);
  return apiCursorPageResponse(
    items,
    {
      per_page: perPage,
      has_more: hasMore,
      next_cursor:
        hasMore && last
          ? encodeInboxCursor({
              createdAt: BigInt(Date.parse(last.created_at)) * BigInt(1_000_000),
              id: BigInt(last.id),
            })
          : null,
    },
    privateMailboxHeaders()
  );
}

/**
 * The caller's own unified inbox counts. Only `tz` reaches the API, checked as the inbox checks
 * it; other parameters are dropped.
 */
export async function getInboxMessageCountsRoute(request: Request): Promise<NextResponse> {
  const target = await resolveMailboxReadRoute(request);
  if (!target.available) return target.response;
  const zones = new URL(request.url).searchParams.getAll('tz');
  if (zones.length > 1) return invalidInput('Repeated time zone');
  const [tz] = zones;
  if (tz !== undefined && !isTimeZone(tz)) return invalidInput('Invalid time zone');

  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'GET',
      path: 'inbox-message-counts',
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      searchParams: new URLSearchParams(tz === undefined ? {} : { tz }),
    });
  }
  const viewer = viewerOf(target);
  const unreadInbox = mockMailboxStore.listInbox(target.organizationId, viewer, {
    view: 'all',
    unread: true,
    tz: tz ?? 'UTC',
    perPage: Number.MAX_SAFE_INTEGER - 1,
  });
  return success({
    ...mockMailboxStore.inboxCounts(target.organizationId, viewer, tz ?? 'UTC'),
    triage: mockTriageStore.counts(
      target.organizationId,
      unreadInbox.ok ? unreadInbox.value.items : []
    ),
  });
}

/** Per-day counts for the last `days` UTC days; the window defaults to a week. */
export async function listMailMessageStatsRoute(request: Request): Promise<NextResponse> {
  const target = await resolveMailboxReadRoute(request);
  if (!target.available) return target.response;
  const days = parseStatsDays(new URL(request.url).searchParams.getAll('days'));
  if (!days.ok) return days.response;

  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'GET',
      path: 'mail-message-stats',
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      searchParams: new URLSearchParams({ days: String(days.value) }),
    });
  }
  return success(mockMailboxStore.stats(target.organizationId, viewerOf(target), days.value));
}

export async function sendMailMessageRoute(
  request: Request,
  rawMailboxId: string
): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  const mailboxId = parseRouteId(rawMailboxId);
  if (!mailboxId.ok) return mailboxId.response;
  const input = await sendBody(request);
  if (!input.ok) return input.response;

  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'POST',
      path: `mailboxes/${mailboxId.value}/messages`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      body: input.value,
      fieldMap: {
        to: 'to',
        cc: 'cc',
        subject: 'subject',
        text: 'text',
        html: 'html',
        in_reply_to: 'in_reply_to',
        attachments: 'attachments',
        scheduled_at: 'scheduled_at',
      },
    });
  }
  return mockSendResponse(
    mockMailboxStore.send(target.organizationId, mailboxId.value, input.value, viewerOf(target))
  );
}

/** Shape checks only; recipients, subject, and bodies are judged by the API or the mock. */
async function sendBody(request: Request): Promise<Parsed<SendMailMessageInput>> {
  const body = await jsonObject(request, maxSendBodyBytes);
  if (!body.ok) return body;
  const {
    to,
    cc,
    subject,
    text,
    html,
    in_reply_to: inReplyTo,
    attachments,
    template,
    scheduled_at: scheduledAt,
  } = body.value;
  const errors: Record<string, string[]> = {};
  if (!addressList(to) || to.length === 0) errors.to = ['The to field is invalid'];
  if (cc !== undefined && !addressList(cc)) errors.cc = ['The cc field is invalid'];
  // A template supplies the subject; without one the subject is required.
  if (
    template === undefined
      ? !boundedString(subject, MAIL_SUBJECT_MAX_LENGTH) || subject === ''
      : subject !== undefined && !boundedString(subject, MAIL_SUBJECT_MAX_LENGTH)
  ) {
    errors.subject = ['The subject field is invalid'];
  }
  if (template !== undefined && !templateReference(template)) {
    errors.template = ['The template field is invalid'];
  }
  if (text !== undefined && !boundedString(text, MAIL_BODY_MAX_BYTES)) {
    errors.text = ['The text field is invalid'];
  }
  if (html !== undefined && !boundedString(html, MAIL_BODY_MAX_BYTES)) {
    errors.html = ['The html field is invalid'];
  }
  if (inReplyTo !== undefined && !boundedString(inReplyTo, MAIL_MESSAGE_ID_MAX_LENGTH)) {
    errors.in_reply_to = ['The in_reply_to field is invalid'];
  }
  if (attachments !== undefined && !attachmentList(attachments)) {
    errors.attachments = ['The attachments field is invalid'];
  }
  if (
    scheduledAt !== undefined &&
    (!boundedString(scheduledAt, 64) || Number.isNaN(Date.parse(scheduledAt)))
  ) {
    errors.scheduled_at = ['The scheduled_at field is invalid'];
  }
  if (Object.keys(errors).length > 0) return { ok: false, response: validationFailed(errors) };
  return {
    ok: true,
    value: {
      to: to as string[],
      ...(cc === undefined ? {} : { cc: cc as string[] }),
      ...(typeof subject === 'string' ? { subject } : {}),
      ...(typeof text === 'string' ? { text } : {}),
      ...(typeof html === 'string' ? { html } : {}),
      ...(typeof inReplyTo === 'string' ? { in_reply_to: inReplyTo } : {}),
      ...(attachmentList(attachments) ? { attachments } : {}),
      ...(templateReference(template) ? { template } : {}),
      ...(typeof scheduledAt === 'string' ? { scheduled_at: scheduledAt } : {}),
    },
  };
}

function templateReference(value: unknown): value is NonNullable<SendMailMessageInput['template']> {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) return false;
  const { id, variables, ...rest } = value as Record<string, unknown>;
  return (
    Object.keys(rest).length === 0 &&
    typeof id === 'number' &&
    Number.isSafeInteger(id) &&
    id > 0 &&
    (variables === undefined || variableMap(variables))
  );
}

/**
 * The listing's query, as `contracts/MAILBOXES.md` allows it: each parameter at most once, known
 * values only, `folder_id` exactly with `placement=folder`, a search of at most 200 characters as
 * sent that is not empty after trimming and not in the trash, and `group=thread` only for
 * placement views. Anything else is invalid input; the API checks the same again.
 */
function parseMessageFilters(query: URLSearchParams): Parsed<MailMessageFilters> {
  const invalid = (message: string): Parsed<MailMessageFilters> => ({
    ok: false,
    response: invalidInput(message),
  });
  const names = [
    'direction',
    'placement',
    'folder_id',
    'label_id',
    'starred',
    'unread',
    'has_attachments',
    'q',
    'group',
  ];
  for (const name of names) {
    if (query.getAll(name).length > 1) return invalid(`Repeated ${name} filter`);
  }
  const filters: MailMessageFilters = {};
  const direction = query.get('direction');
  if (direction !== null) {
    if (!isOneOf(logDirections, direction)) return invalid('Invalid message direction');
    filters.direction = direction;
  }
  const placement = query.get('placement');
  if (placement !== null) {
    if (!isOneOf<MailPlacement>(MAIL_PLACEMENTS, placement)) return invalid('Invalid placement');
    filters.placement = placement;
  }
  for (const [name, key] of [
    ['folder_id', 'folderId'],
    ['label_id', 'labelId'],
  ] as const) {
    const raw = query.get(name);
    if (raw === null) continue;
    const id = parseRouteId(raw);
    if (!id.ok) return invalid(`Invalid ${name} filter`);
    filters[key] = id.value;
  }
  for (const [name, key] of [
    ['starred', 'starred'],
    ['unread', 'unread'],
    ['has_attachments', 'hasAttachments'],
  ] as const) {
    const raw = query.get(name);
    if (raw === null) continue;
    if (raw !== 'true' && raw !== 'false') return invalid(`Invalid ${name} filter`);
    filters[key] = raw === 'true';
  }
  const rawSearch = query.get('q');
  if (rawSearch !== null) {
    const q = rawSearch.trim();
    if (q === '' || Array.from(rawSearch).length > MAIL_SEARCH_MAX_LENGTH) {
      return invalid('Invalid search');
    }
    filters.q = q;
  }
  const group = query.get('group');
  if (group !== null) {
    if (group !== 'thread') return invalid('Invalid grouping');
    filters.group = group;
  }

  if ((filters.placement === 'folder') !== (filters.folderId !== undefined)) {
    return invalid('folder_id goes exactly with placement=folder');
  }
  if (filters.q !== undefined && filters.placement === 'trash') {
    return invalid('The trash cannot be searched');
  }
  if (filters.group === 'thread') {
    const view = filters.placement;
    if (view !== 'default' && view !== 'archive' && view !== 'folder') {
      return invalid('group=thread needs a placement view');
    }
    if (view === 'default' && filters.direction === undefined) {
      return invalid('group=thread on default needs direction');
    }
    if (
      filters.labelId !== undefined ||
      filters.starred !== undefined ||
      filters.unread !== undefined ||
      filters.hasAttachments !== undefined ||
      filters.q !== undefined
    ) {
      return invalid('group=thread lists placement views only');
    }
  }
  return { ok: true, value: filters };
}

/** The filters as the API's query, in a fixed order. */
function messageFilterParams(filters: MailMessageFilters): [string, string][] {
  const params: [string, string][] = [];
  if (filters.direction) params.push(['direction', filters.direction]);
  if (filters.placement) params.push(['placement', filters.placement]);
  if (filters.folderId !== undefined) params.push(['folder_id', String(filters.folderId)]);
  if (filters.labelId !== undefined) params.push(['label_id', String(filters.labelId)]);
  if (filters.starred !== undefined) params.push(['starred', String(filters.starred)]);
  if (filters.unread !== undefined) params.push(['unread', String(filters.unread)]);
  if (filters.hasAttachments !== undefined) {
    params.push(['has_attachments', String(filters.hasAttachments)]);
  }
  if (filters.q !== undefined) params.push(['q', filters.q]);
  if (filters.group) params.push(['group', filters.group]);
  return params;
}

const logDirections: readonly MailDirection[] = ['inbound', 'outbound'];

const logStatuses: readonly MailStatus[] = ['received', 'queued', 'sent', 'failed'];

/**
 * As in the API, an absent or empty parameter does not filter. A repeated parameter, an unknown
 * direction or status, a non-positive mailbox, or an over-long search is invalid input.
 */
function parseLogFilters(query: URLSearchParams): Parsed<MailLogFilters> {
  const invalid = (message: string): Parsed<MailLogFilters> => ({
    ok: false,
    response: invalidInput(message),
  });
  const single = (name: string): string | null => {
    const values = query.getAll(name);
    return values.length > 1 ? null : (values[0] ?? '');
  };
  const direction = single('direction');
  if (direction === null || (direction !== '' && !isOneOf(logDirections, direction))) {
    return invalid('Invalid message direction');
  }
  const status = single('status');
  if (status === null || (status !== '' && !isOneOf(logStatuses, status))) {
    return invalid('Invalid message status');
  }
  const mailbox = single('mailbox_id');
  if (mailbox === null) return invalid('Invalid mailbox filter');
  const mailboxId = mailbox === '' ? null : parseRouteId(mailbox);
  if (mailboxId !== null && !mailboxId.ok) return invalid('Invalid mailbox filter');
  const rawSearch = single('q');
  if (rawSearch === null) return invalid('Invalid search');
  const q = rawSearch.trim();
  if (Array.from(q).length > MAIL_LOG_QUERY_MAX_LENGTH) return invalid('Invalid search');

  return {
    ok: true,
    value: {
      ...(direction === '' ? {} : { direction }),
      ...(status === '' ? {} : { status }),
      ...(mailboxId === null ? {} : { mailboxId: mailboxId.value }),
      ...(q === '' ? {} : { q }),
    },
  };
}

/** The unified inbox parameters, in the order they are forwarded; each may appear once. */
const inboxParameters = [
  'view',
  'role_id',
  'mailbox_id',
  'unread',
  'has_attachments',
  'q',
  'group',
  'triage',
  'assignment',
  'since',
  'until',
  'tz',
  'cursor',
  'per_page',
] as const;

const inboxTimeZonePattern = /^[A-Za-z][A-Za-z0-9_+\-/]*$/u;

const integerPattern = /^[+-]?\d+$/u;

/**
 * The unified inbox query rules of `contracts/MAILBOXES.md`, as the API applies them: `page`, a
 * repeated parameter, an empty or unknown value, a malformed cursor, an unknown zone, or a
 * combination outside the rules is invalid input; an integer `per_page` out of range is clamped.
 * Unknown parameters are dropped. Answers the query to forward and the query for the mock.
 */
function parseInboxQuery(query: URLSearchParams): Parsed<{
  forward: URLSearchParams;
  mock: MockInboxQuery;
  triage?: MailTriageCategory[];
  assignment?: InboxAssignment;
}> {
  const invalid = (message: string) => ({ ok: false as const, response: invalidInput(message) });
  if (query.has('page')) return invalid('Use cursor instead of page');
  for (const name of inboxParameters) {
    if (query.getAll(name).length > 1) return invalid('Repeated inbox parameter');
  }
  const value = (name: (typeof inboxParameters)[number]) => query.get(name);
  const forward = new URLSearchParams();
  const mock: MockInboxQuery = { view: 'all', tz: 'UTC', perPage: defaultPerPage };

  const view = value('view');
  if (view !== null) {
    if (!isOneOf<InboxView>(INBOX_VIEWS, view)) return invalid('Invalid inbox view');
    mock.view = view;
    forward.set('view', view);
  }
  const role = value('role_id');
  if (role !== null) {
    const roleId = role === 'none' ? null : parseRouteId(role);
    if (roleId !== null && !roleId.ok) return invalid('Invalid role filter');
    mock.roleId = roleId === null ? 'none' : roleId.value;
    forward.set('role_id', role);
  }
  const mailbox = value('mailbox_id');
  if (mailbox !== null) {
    const mailboxId = parseRouteId(mailbox);
    if (!mailboxId.ok) return invalid('Invalid mailbox filter');
    mock.mailboxId = mailboxId.value;
    forward.set('mailbox_id', mailbox);
  }
  const unread = value('unread');
  if (unread !== null) {
    if (unread !== 'true' && unread !== 'false') return invalid('Invalid unread filter');
    mock.unread = unread === 'true';
    forward.set('unread', unread);
  }
  const hasAttachments = value('has_attachments');
  if (hasAttachments !== null) {
    if (hasAttachments !== 'true' && hasAttachments !== 'false') {
      return invalid('Invalid attachments filter');
    }
    mock.hasAttachments = hasAttachments === 'true';
    forward.set('has_attachments', hasAttachments);
  }
  const rawSearch = value('q');
  if (rawSearch !== null) {
    // As in the mailbox search: the length counts as sent, the rest after trimming.
    const q = rawSearch.trim();
    if (q === '' || Array.from(rawSearch).length > MAIL_SEARCH_MAX_LENGTH || /\p{Cc}/u.test(q)) {
      return invalid('Invalid search');
    }
    mock.q = q;
    forward.set('q', q);
  }
  const group = value('group');
  if (group !== null) {
    if (group !== 'thread') return invalid('Invalid grouping');
    mock.group = group;
    forward.set('group', group);
  }
  let triage: MailTriageCategory[] | undefined;
  const rawTriage = value('triage');
  if (rawTriage !== null) {
    const categories = rawTriage.split(',');
    if (
      new Set(categories).size !== categories.length ||
      !categories.every(category => isOneOf(MAIL_TRIAGE_CATEGORIES, category))
    ) {
      return invalid('Invalid triage filter');
    }
    triage = categories as MailTriageCategory[];
    forward.set('triage', rawTriage);
  }
  const assignment = value('assignment');
  if (assignment !== null) {
    if (!isOneOf<InboxAssignment>(INBOX_ASSIGNMENTS, assignment)) {
      return invalid('Invalid assignment filter');
    }
    forward.set('assignment', assignment);
  }
  for (const name of ['since', 'until'] as const) {
    const day = value(name);
    if (day === null) continue;
    if (!isInboxDay(day)) return invalid('Invalid date filter');
    mock[name] = day;
    forward.set(name, day);
  }
  const tz = value('tz');
  if (tz !== null) {
    if (!isTimeZone(tz)) return invalid('Invalid time zone');
    mock.tz = tz;
    forward.set('tz', tz);
  }
  const cursor = value('cursor');
  if (cursor !== null) {
    const after = decodeInboxCursor(cursor);
    if (!after) return invalid('Invalid cursor');
    mock.after = after;
    forward.set('cursor', cursor);
  }
  const perPage = value('per_page');
  if (perPage !== null) {
    if (!integerPattern.test(perPage)) return invalid('Invalid page size');
    mock.perPage = Math.min(Math.max(Number(perPage), 1), MAIL_PAGE_MAX_SIZE);
  }
  forward.set('per_page', String(mock.perPage));

  if (mock.q !== undefined && mock.view !== 'all') return invalid('Search needs view all');
  if (mock.group && mock.view !== 'all' && mock.view !== 'sent') {
    return invalid('Grouping needs view all or sent');
  }
  if (
    mock.group &&
    (mock.unread !== undefined ||
      mock.hasAttachments !== undefined ||
      mock.q !== undefined ||
      mock.since !== undefined ||
      mock.until !== undefined)
  ) {
    return invalid('Grouping cannot be combined with unread, attachments, search or dates');
  }
  if (mock.since !== undefined && mock.until !== undefined && mock.until < mock.since) {
    return invalid('The end date is before the start date');
  }
  if (
    triage &&
    ((mock.view !== 'all' && mock.view !== 'today') || mock.group || mock.q !== undefined)
  ) {
    return invalid('Triage needs view all or today, without grouping or search');
  }
  return {
    ok: true,
    value: {
      forward,
      mock,
      ...(triage ? { triage } : {}),
      ...(assignment === null ? {} : { assignment }),
    },
  };
}

/** An IANA zone name; `Local` and the empty name are never a viewer's zone. */
function isTimeZone(value: string): boolean {
  if (
    value === 'Local' ||
    value.length > INBOX_TIME_ZONE_MAX_LENGTH ||
    !inboxTimeZonePattern.test(value)
  ) {
    return false;
  }
  try {
    new Intl.DateTimeFormat('en-US', { timeZone: value });
    return true;
  } catch {
    return false;
  }
}

/** Absent means the default week; anything but one integer from 1 to 90 is invalid input. */
function parseStatsDays(values: string[]): Parsed<number> {
  if (values.length === 0) return { ok: true, value: MAIL_STATS_DEFAULT_DAYS };
  const [value] = values;
  const days = values.length === 1 && /^[1-9]\d?$/u.test(value) ? Number(value) : Number.NaN;
  return days >= 1 && days <= MAIL_STATS_MAX_DAYS
    ? { ok: true, value: days }
    : { ok: false, response: invalidInput('Invalid statistics window') };
}
