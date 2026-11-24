import type {
  InboxFilters,
  MailLogFilters,
  MailMessageFilters,
  MailboxDirectoryFilters,
  MailboxListScope,
} from '@/features/mailbox/types';

/**
 * The mailbox query keys and refresh settings, apart from the hooks and the mailbox service, so a
 * page that only invalidates a list does not load them.
 */

/** How often an open mailbox looks for new mail while its tab is visible. */
export const MAIL_REFRESH_MS = 5_000;

/**
 * Polling for the open mailbox: every 15 seconds while the tab is visible, and again when the
 * window regains focus (the global client turns focus refetches off). A refetch keeps the data on
 * screen, so it neither resets the selection nor moves the scroll position or the focus.
 */
export const liveRefresh = {
  refetchInterval: MAIL_REFRESH_MS,
  refetchIntervalInBackground: false,
  refetchOnWindowFocus: true,
} as const;

/** Every key names the organization, so one organization's data is never shown for another. */
export const mailboxKeys = {
  all: ['mailbox'] as const,
  organization: (organizationId: number) => [...mailboxKeys.all, organizationId] as const,
  domains: (organizationId: number) =>
    [...mailboxKeys.organization(organizationId), 'domains'] as const,
  mailDomains: (organizationId: number) =>
    [...mailboxKeys.organization(organizationId), 'mail-domains'] as const,
  mailZones: (organizationId: number) =>
    [...mailboxKeys.organization(organizationId), 'mail-zones'] as const,
  mailZone: (organizationId: number, zoneId: number) =>
    [...mailboxKeys.mailZones(organizationId), zoneId] as const,
  dnsRecords: (organizationId: number, zoneId: number) =>
    [...mailboxKeys.mailZone(organizationId, zoneId), 'dns-records'] as const,
  /** Without a scope, the key covers every scoped list too. */
  list: (organizationId: number, scope?: MailboxListScope) =>
    scope === undefined
      ? ([...mailboxKeys.organization(organizationId), 'list'] as const)
      : ([...mailboxKeys.organization(organizationId), 'list', scope] as const),
  /** Every directory page of the organization. */
  directories: (organizationId: number) =>
    [...mailboxKeys.organization(organizationId), 'directory'] as const,
  directory: (organizationId: number, filters: MailboxDirectoryFilters, cursor?: string) =>
    [
      ...mailboxKeys.directories(organizationId),
      filters.type ?? '',
      filters.q ?? '',
      cursor ?? '',
    ] as const,
  mailbox: (organizationId: number, mailboxId: number) =>
    [...mailboxKeys.organization(organizationId), mailboxId] as const,
  messages: (organizationId: number, mailboxId: number) =>
    [...mailboxKeys.mailbox(organizationId, mailboxId), 'messages'] as const,
  filteredPage: (
    organizationId: number,
    mailboxId: number,
    filters: MailMessageFilters,
    page: number
  ) =>
    [
      ...mailboxKeys.messages(organizationId, mailboxId),
      'filtered',
      filters.direction ?? '',
      filters.placement ?? '',
      filters.folderId ?? 0,
      filters.labelId ?? 0,
      filters.starred ?? '',
      filters.unread ?? '',
      filters.hasAttachments ?? '',
      filters.q ?? '',
      filters.group ?? '',
      page,
    ] as const,
  messageDetails: (organizationId: number, mailboxId: number) =>
    [...mailboxKeys.mailbox(organizationId, mailboxId), 'message'] as const,
  message: (organizationId: number, mailboxId: number, messageId: number) =>
    [...mailboxKeys.messageDetails(organizationId, mailboxId), messageId] as const,
  threads: (organizationId: number, mailboxId: number) =>
    [...mailboxKeys.mailbox(organizationId, mailboxId), 'thread'] as const,
  thread: (organizationId: number, mailboxId: number, threadId: number) =>
    [...mailboxKeys.threads(organizationId, mailboxId), threadId] as const,
  counts: (organizationId: number, mailboxId: number) =>
    [...mailboxKeys.mailbox(organizationId, mailboxId), 'counts'] as const,
  folders: (organizationId: number, mailboxId: number) =>
    [...mailboxKeys.mailbox(organizationId, mailboxId), 'folders'] as const,
  labels: (organizationId: number, mailboxId: number) =>
    [...mailboxKeys.mailbox(organizationId, mailboxId), 'labels'] as const,
  members: (organizationId: number, mailboxId: number) =>
    [...mailboxKeys.mailbox(organizationId, mailboxId), 'members'] as const,
  accessEvents: (organizationId: number, mailboxId: number) =>
    [...mailboxKeys.mailbox(organizationId, mailboxId), 'access-events'] as const,
  accessEventPage: (organizationId: number, mailboxId: number, page: number) =>
    [...mailboxKeys.accessEvents(organizationId, mailboxId), page] as const,
  roles: (organizationId: number) =>
    [...mailboxKeys.organization(organizationId), 'roles'] as const,
  /** Every unified inbox listing of the organization. */
  inboxes: (organizationId: number) =>
    [...mailboxKeys.organization(organizationId), 'inbox'] as const,
  inbox: (organizationId: number, filters: InboxFilters) =>
    [
      ...mailboxKeys.inboxes(organizationId),
      filters.view,
      filters.roleId ?? '',
      filters.mailboxId ?? 0,
      filters.unread ?? '',
      filters.hasAttachments ?? '',
      filters.q ?? '',
      filters.group ?? '',
      filters.triage?.join(',') ?? '',
      filters.assignment ?? '',
      filters.since ?? '',
      filters.until ?? '',
      filters.tz,
    ] as const,
  /** Every unified inbox count of the organization, whatever the zone. */
  allInboxCounts: (organizationId: number) =>
    [...mailboxKeys.organization(organizationId), 'inbox-counts'] as const,
  inboxCounts: (organizationId: number, tz: string) =>
    [...mailboxKeys.allInboxCounts(organizationId), tz] as const,
  log: (organizationId: number) => [...mailboxKeys.organization(organizationId), 'log'] as const,
  logPage: (organizationId: number, filters: MailLogFilters, page: number) =>
    [
      ...mailboxKeys.log(organizationId),
      filters.direction ?? '',
      filters.status ?? '',
      filters.mailboxId ?? 0,
      filters.q ?? '',
      page,
    ] as const,
  stats: (organizationId: number) =>
    [...mailboxKeys.organization(organizationId), 'stats'] as const,
  statsWindow: (organizationId: number, days: number) =>
    [...mailboxKeys.stats(organizationId), days] as const,
};

export function validId(value: number): boolean {
  return Number.isSafeInteger(value) && value > 0;
}
