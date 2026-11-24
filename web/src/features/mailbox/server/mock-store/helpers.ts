import {
  MAIL_LABEL_COLORS,
  MAIL_MESSAGE_UPDATES_MAX_IDS,
  MAIL_PLACEMENTS,
  MAIL_TEMPLATE_MAX_VARIABLES,
  MAIL_TEMPLATE_VALUE_MAX_LENGTH,
  MAIL_TEMPLATE_VARIABLE_PATTERN,
  MAIL_THREAD_MAX_MESSAGES,
  MAILBOX_PERMISSIONS,
} from '@/features/mailbox/schemas';
import type {
  MailLabelColor,
  MailLogEntry,
  MailMessage,
  MailMessageFilters,
  MailMessagePage,
  MailMessageSummary,
  MailPlacement,
  MailThreadSummary,
  MailboxAccess,
  MailboxMember,
  MailboxPermission,
} from '@/features/mailbox/types';
import type {
  MailboxAccessState,
  MailboxState,
  MailboxViewer,
  MockMessageChange,
} from '@/features/mailbox/server/mock-store/types';

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
export function awaitsHandover(access: MailboxAccessState): boolean {
  return access.type === 'personal' && access.origin === 'zmail' && access.ownerLeft !== null;
}

/** A member's own connected account, which the directory and the export never list. */
export function isMembersOwnAccount(access: MailboxAccessState): boolean {
  return access.origin === 'connected' && access.type === 'personal';
}

export function anyAccess(access: MailboxAccess): boolean {
  return Object.values(access).some(Boolean);
}

export function isPermission(value: unknown): value is MailboxPermission {
  return (MAILBOX_PERMISSIONS as readonly unknown[]).includes(value);
}

/** Only owners and admins write mail roles; roles never come from mailbox access. */
export function managesOrganization(viewer: MailboxViewer): boolean {
  return viewer.role === 'owner' || viewer.role === 'admin';
}

/** Quotes a CSV field the way Go's encoding/csv does. */
export function csvField(value: string): string {
  if (value === '' || !/[",\r\n]|^[ \t]/u.test(value)) return value;
  return `"${value.replaceAll('"', '""')}"`;
}

export function summaryOf(message: MailMessage): MailMessageSummary {
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
export function logSummaryOf(message: MailMessage): Omit<MailLogEntry, 'mailbox_address'> {
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
export function matchesFilters(
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
export function matchesMailboxSearch(message: MailMessage, needle: string): boolean {
  return [
    message.subject,
    message.from.address,
    message.from.name,
    ...message.to,
    ...message.cc,
    message.text_body,
  ].some(text => text.toLowerCase().includes(needle));
}

export function matchesSearch(message: MailMessage, needle: string): boolean {
  return (
    message.subject.toLowerCase().includes(needle) ||
    message.from.address.toLowerCase().includes(needle) ||
    message.to.some(address => address.toLowerCase().includes(needle))
  );
}

/** One row per thread: its newest message in the view, carrying the thread's summary there. */
export function threadRows(messages: MailMessage[]): MailMessageSummary[] {
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

/** The listing's filters as the pagination links repeat them, ready for `page=`. */
export function filterQuery(filters: MailMessageFilters): string {
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
export function changeErrors(
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
export function checkIdList(
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

export function addField(fields: Record<string, string[]>, field: string, message: string): void {
  fields[field] = [...(fields[field] ?? []), message];
}

/** Drops zero and repeated ids, keeping the first occurrence of each. */
export function positiveDistinct(ids: number[]): number[] {
  return [...new Set(ids.filter(id => id > 0))];
}

export function isPlacement(value: string): value is MailPlacement {
  return (MAIL_PLACEMENTS as readonly string[]).includes(value);
}

export function isLabelColor(value: string): value is MailLabelColor {
  return (MAIL_LABEL_COLORS as readonly string[]).includes(value);
}

export function toggle(
  map: Map<number, Set<number>>,
  messageId: number,
  userId: number,
  on: boolean
): void {
  const users = map.get(messageId) ?? new Set<number>();
  if (on) users.add(userId);
  else users.delete(userId);
  map.set(messageId, users);
}

/** A folder or label name trimmed, then 1 to `maxLength` characters. */
export function organizeName(
  fields: Record<string, string[]>,
  raw: string,
  maxLength: number
): string {
  const name = raw.trim();
  const length = Array.from(name).length;
  if (length < 1 || length > maxLength) {
    addField(fields, 'name', `must be 1 to ${maxLength} characters after trimming`);
  }
  return name;
}

/** Names are unique in the mailbox ignoring case; an item may keep its own name. */
export function nameTaken(
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
export function sortedByName<T extends { id: number; name: string }>(items: Map<number, T>): T[] {
  return [...items.values()].sort((left, right) => {
    const a = left.name.toLowerCase();
    const b = right.name.toLowerCase();
    return a < b ? -1 : a > b ? 1 : left.id - right.id;
  });
}

/** The UTC calendar day of a timestamp, as `YYYY-MM-DD`. */
export function dayOf(milliseconds: number): string {
  return new Date(milliseconds).toISOString().slice(0, 10);
}

/** `hrefPrefix` is the listing path with its filters, ready for `page=` to be appended. */
export function paginated<T>(
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

export function sortedMembers(state: MailboxState): MailboxMember[] {
  return [...state.access.members.values()]
    .sort((left, right) => left.user_id - right.user_id)
    .map(member => ({ ...member }));
}

/** Someone who loses access loses their read records and stars with it. */
export function forgetReads(state: MailboxState, userId: number): void {
  for (const readers of state.reads.values()) readers.delete(userId);
  for (const starrers of state.stars.values()) starrers.delete(userId);
}

export function cleanValues(
  values: Readonly<Record<string, string>>
): Record<string, string> | null {
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
