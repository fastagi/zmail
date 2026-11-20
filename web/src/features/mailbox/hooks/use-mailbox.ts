'use client';

import {
  keepPreviousData,
  useMutation,
  useQuery,
  useQueryClient,
  type QueryClient,
} from '@tanstack/react-query';

import { LOCAL_ERROR_HANDLING_META } from '@/config/query-meta';
import { mailboxService } from '@/features/mailbox/services/mailbox-service';
import type {
  CreateMailboxInput,
  InboxFilters,
  MailDnsRecordInput,
  MailDnsRecordPatch,
  MailDomain,
  MailDomainInput,
  MailFolderInput,
  MailLabelInput,
  MailLogFilters,
  MailMessageFilters,
  MailboxPermission,
  SendMailMessageInput,
  UpdateMailMessageInput,
  UpdateMailMessagesInput,
} from '@/features/mailbox/types';

export const MAIL_PAGE_SIZE = 20;
/** How often a domain whose setup runs is read again. */
const setupRefreshMs = 3_000;
export const MAIL_STATS_DAYS = 7;
/** How often an open mailbox looks for new mail while its tab is visible. */
export const MAIL_REFRESH_MS = 15_000;

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
  list: (organizationId: number) => [...mailboxKeys.organization(organizationId), 'list'] as const,
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
      filters.q ?? '',
      filters.group ?? '',
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

/** The log and the per-day counts span every mailbox, so any message change touches them. */
function invalidateLog(queryClient: QueryClient, organizationId: number, withStats = true) {
  return Promise.all([
    queryClient.invalidateQueries({ queryKey: mailboxKeys.log(organizationId) }),
    ...(withStats
      ? [queryClient.invalidateQueries({ queryKey: mailboxKeys.stats(organizationId) })]
      : []),
  ]);
}

/**
 * Every change to a mailbox's mail: its listings, threads, and details, the caller's counts, the
 * unread count in the mailbox list, the unified inbox and its counts, and the log. Statistics only
 * move when mail is deleted.
 */
export function invalidateMail(
  queryClient: QueryClient,
  organizationId: number,
  mailboxId: number,
  withStats = false
) {
  return Promise.all([
    queryClient.invalidateQueries({ queryKey: mailboxKeys.messages(organizationId, mailboxId) }),
    queryClient.invalidateQueries({
      queryKey: mailboxKeys.messageDetails(organizationId, mailboxId),
    }),
    queryClient.invalidateQueries({ queryKey: mailboxKeys.threads(organizationId, mailboxId) }),
    queryClient.invalidateQueries({ queryKey: mailboxKeys.counts(organizationId, mailboxId) }),
    queryClient.invalidateQueries({ queryKey: mailboxKeys.list(organizationId) }),
    queryClient.invalidateQueries({ queryKey: mailboxKeys.inboxes(organizationId) }),
    queryClient.invalidateQueries({ queryKey: mailboxKeys.allInboxCounts(organizationId) }),
    invalidateLog(queryClient, organizationId, withStats),
  ]);
}

export function validId(value: number): boolean {
  return Number.isSafeInteger(value) && value > 0;
}

// The workspace renders every error inline, so the global error toast stays quiet.

export function useMailboxDomains(organizationId: number) {
  return useQuery({
    queryKey: mailboxKeys.domains(organizationId),
    queryFn: () => mailboxService.domains(organizationId),
    enabled: validId(organizationId),
    staleTime: 5 * 60_000,
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

/** Connected domains; only loaded when the instance allows them. */
export function useMailDomains(organizationId: number, enabled: boolean) {
  return useQuery({
    queryKey: mailboxKeys.mailDomains(organizationId),
    queryFn: () => mailboxService.mailDomains(organizationId),
    enabled: enabled && validId(organizationId),
    // A setup runs in the background on the API; follow it until it is done.
    refetchInterval: query =>
      query.state.data?.some(domain => domain.setting_up) ? setupRefreshMs : false,
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

/**
 * A connected domain changes which domains new mailboxes may use; connecting can create a managed
 * zone, a check can activate one, and a setup writes its records.
 */
function refreshDomains(queryClient: QueryClient, organizationId: number) {
  return Promise.all([
    queryClient.invalidateQueries({ queryKey: mailboxKeys.mailDomains(organizationId) }),
    queryClient.invalidateQueries({ queryKey: mailboxKeys.domains(organizationId) }),
    queryClient.invalidateQueries({ queryKey: mailboxKeys.mailZones(organizationId) }),
  ]);
}

/**
 * Puts a domain the API just answered with into the cached list, so its card shows the answer at
 * once instead of waiting for the list to be read again.
 */
function storeDomain(queryClient: QueryClient, organizationId: number, domain: MailDomain) {
  queryClient.setQueryData<MailDomain[]>(mailboxKeys.mailDomains(organizationId), current =>
    current?.map(item => (item.id === domain.id ? domain : item))
  );
}

export function useConnectMailDomain(organizationId: number) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: MailDomainInput) => mailboxService.connectMailDomain(organizationId, input),
    meta: LOCAL_ERROR_HANDLING_META,
    onSettled: () => refreshDomains(queryClient, organizationId),
  });
}

export function useRefreshMailDomain(organizationId: number) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ domain, action }: { domain: MailDomain; action: 'setups' | 'checks' }) =>
      mailboxService.refreshMailDomain(organizationId, domain.id, action),
    meta: LOCAL_ERROR_HANDLING_META,
    onSuccess: domain => storeDomain(queryClient, organizationId, domain),
    onSettled: () => refreshDomains(queryClient, organizationId),
  });
}

export function useRemoveMailDomain(organizationId: number) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (domainId: number) => mailboxService.removeMailDomain(organizationId, domainId),
    meta: LOCAL_ERROR_HANDLING_META,
    onSettled: () => refreshDomains(queryClient, organizationId),
  });
}

/** Changes nothing on the server, so nothing is invalidated. */
export function usePreviewMailDomain(organizationId: number) {
  return useMutation({
    mutationFn: (name: string) => mailboxService.previewMailDomain(organizationId, name),
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

export function useEnableReceiving(organizationId: number) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (domainId: number) => mailboxService.enableReceiving(organizationId, domainId),
    meta: LOCAL_ERROR_HANDLING_META,
    onSuccess: domain => storeDomain(queryClient, organizationId, domain),
    onSettled: () => refreshDomains(queryClient, organizationId),
  });
}

/** Managed zones; only loaded when the instance allows custom domains. */
export function useListMailZones(organizationId: number, enabled: boolean) {
  return useQuery({
    queryKey: mailboxKeys.mailZones(organizationId),
    queryFn: () => mailboxService.listMailZones(organizationId),
    enabled: enabled && validId(organizationId),
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

export function useMailZone(organizationId: number, zoneId: number) {
  return useQuery({
    queryKey: mailboxKeys.mailZone(organizationId, zoneId),
    queryFn: () => mailboxService.getMailZone(organizationId, zoneId),
    enabled: validId(organizationId) && validId(zoneId),
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

export function useDeleteMailZone(organizationId: number) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (zoneId: number) => mailboxService.deleteMailZone(organizationId, zoneId),
    meta: LOCAL_ERROR_HANDLING_META,
    onSuccess: (_result, zoneId) => {
      queryClient.removeQueries({ queryKey: mailboxKeys.mailZone(organizationId, zoneId) });
    },
    onSettled: () =>
      queryClient.invalidateQueries({ queryKey: mailboxKeys.mailZones(organizationId) }),
  });
}

/** Read from Cloudflare on every request; the API keeps no copy. */
export function useListDnsRecords(organizationId: number, zoneId: number) {
  return useQuery({
    queryKey: mailboxKeys.dnsRecords(organizationId, zoneId),
    queryFn: () => mailboxService.listDnsRecords(organizationId, zoneId),
    enabled: validId(organizationId) && validId(zoneId),
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

function refreshDnsRecords(queryClient: QueryClient, organizationId: number, zoneId: number) {
  return queryClient.invalidateQueries({
    queryKey: mailboxKeys.dnsRecords(organizationId, zoneId),
  });
}

export function useCreateDnsRecord(organizationId: number, zoneId: number) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: MailDnsRecordInput) =>
      mailboxService.createDnsRecord(organizationId, zoneId, input),
    meta: LOCAL_ERROR_HANDLING_META,
    onSettled: () => refreshDnsRecords(queryClient, organizationId, zoneId),
  });
}

export function useUpdateDnsRecord(organizationId: number, zoneId: number) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ recordId, input }: { recordId: string; input: MailDnsRecordPatch }) =>
      mailboxService.updateDnsRecord(organizationId, zoneId, recordId, input),
    meta: LOCAL_ERROR_HANDLING_META,
    onSettled: () => refreshDnsRecords(queryClient, organizationId, zoneId),
  });
}

export function useDeleteDnsRecord(organizationId: number, zoneId: number) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (recordId: string) =>
      mailboxService.deleteDnsRecord(organizationId, zoneId, recordId),
    meta: LOCAL_ERROR_HANDLING_META,
    onSettled: () => refreshDnsRecords(queryClient, organizationId, zoneId),
  });
}

/** `live` keeps the list (and its unread counts) fresh while a mailbox is open. */
export function useMailboxes(organizationId: number, { live = false }: { live?: boolean } = {}) {
  return useQuery({
    queryKey: mailboxKeys.list(organizationId),
    queryFn: () => mailboxService.list(organizationId),
    enabled: validId(organizationId),
    staleTime: 15_000,
    ...(live ? liveRefresh : {}),
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

export function useCreateMailbox(organizationId: number) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: CreateMailboxInput) => mailboxService.create(organizationId, input),
    meta: LOCAL_ERROR_HANDLING_META,
    onSettled: () => queryClient.invalidateQueries({ queryKey: mailboxKeys.list(organizationId) }),
  });
}

export function useDeleteMailbox(organizationId: number) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (mailboxId: number) => mailboxService.remove(organizationId, mailboxId),
    meta: LOCAL_ERROR_HANDLING_META,
    onSuccess: (_result, mailboxId) => {
      queryClient.removeQueries({ queryKey: mailboxKeys.mailbox(organizationId, mailboxId) });
    },
    // Messages of a deleted mailbox leave the log and the counts.
    onSettled: () =>
      Promise.all([
        queryClient.invalidateQueries({ queryKey: mailboxKeys.list(organizationId) }),
        invalidateLog(queryClient, organizationId),
      ]),
  });
}

export function useMailLog(organizationId: number, filters: MailLogFilters, page: number) {
  return useQuery({
    queryKey: mailboxKeys.logPage(organizationId, filters, page),
    queryFn: () =>
      mailboxService.listLog(organizationId, { ...filters, page, perPage: MAIL_PAGE_SIZE }),
    enabled: validId(organizationId),
    placeholderData: keepPreviousData,
    staleTime: 15_000,
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

export function useMailStats(organizationId: number, days: number = MAIL_STATS_DAYS) {
  return useQuery({
    queryKey: mailboxKeys.statsWindow(organizationId, days),
    queryFn: () => mailboxService.stats(organizationId, days),
    enabled: validId(organizationId),
    staleTime: 60_000,
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

export function useMailMessage(
  organizationId: number,
  mailboxId: number,
  messageId: number | null
) {
  return useQuery({
    queryKey: mailboxKeys.message(organizationId, mailboxId, messageId ?? 0),
    queryFn: () => mailboxService.message(organizationId, mailboxId, messageId ?? 0),
    enabled: validId(organizationId) && messageId !== null,
    staleTime: 15_000,
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

/**
 * The sent folder is refreshed after every attempt: a send the provider rejected is still stored,
 * with status `failed`.
 */
export function useSendMailMessage(organizationId: number, mailboxId: number) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: SendMailMessageInput) =>
      mailboxService.send(organizationId, mailboxId, input),
    meta: LOCAL_ERROR_HANDLING_META,
    // A reply joins the thread it answers.
    onSettled: () => invalidateMail(queryClient, organizationId, mailboxId, true),
  });
}

/** Read state, star, placement, folder, or the complete set of labels of one message. */
export function useUpdateMailMessage(organizationId: number, mailboxId: number) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ messageId, ...input }: { messageId: number } & UpdateMailMessageInput) =>
      mailboxService.updateMessage(organizationId, mailboxId, messageId, input),
    meta: LOCAL_ERROR_HANDLING_META,
    onSuccess: message => {
      queryClient.setQueryData(mailboxKeys.message(organizationId, mailboxId, message.id), message);
    },
    // Read state and placement change the counts and the unread count in the mailbox list.
    onSettled: () => invalidateMail(queryClient, organizationId, mailboxId),
  });
}

/** One change for 1 to 100 messages; a thread row sends its `thread.message_ids`. */
export function useUpdateMailMessages(organizationId: number, mailboxId: number) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: UpdateMailMessagesInput) =>
      mailboxService.updateMessages(organizationId, mailboxId, input),
    meta: LOCAL_ERROR_HANDLING_META,
    onSettled: () => invalidateMail(queryClient, organizationId, mailboxId),
  });
}

/** Deletes everything in the trash for good; needs `organize`. */
export function usePurgeMailTrash(organizationId: number, mailboxId: number) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: () => mailboxService.purgeTrash(organizationId, mailboxId),
    meta: LOCAL_ERROR_HANDLING_META,
    onSettled: () => invalidateMail(queryClient, organizationId, mailboxId, true),
  });
}

/**
 * Marks a message read from the log, where each row belongs to a different mailbox, so the
 * mailbox is a variable of the mutation rather than of the hook.
 */
export function useMarkMailMessageRead(organizationId: number) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ mailboxId, messageId }: { mailboxId: number; messageId: number }) =>
      mailboxService.setRead(organizationId, mailboxId, messageId, true),
    meta: LOCAL_ERROR_HANDLING_META,
    onSuccess: (message, { mailboxId }) => {
      queryClient.setQueryData(mailboxKeys.message(organizationId, mailboxId, message.id), message);
    },
    onSettled: (_message, _error, { mailboxId }) =>
      invalidateMail(queryClient, organizationId, mailboxId),
  });
}

/**
 * Moves a message to the trash, or deletes one already there for good (`organize`). Either way it
 * leaves the counts and, when unread in the Inbox, the unread count in the mailbox list.
 */
/**
 * Shows the masked codes of one message. The original is returned to the caller only and never
 * written to the query cache, so reopening the message masks it again.
 */
export function useRevealMailMessage(organizationId: number, mailboxId: number) {
  return useMutation({
    mutationFn: (messageId: number) =>
      mailboxService.revealMessage(organizationId, mailboxId, messageId),
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

export function useDeleteMailMessage(organizationId: number, mailboxId: number) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (messageId: number) =>
      mailboxService.removeMessage(organizationId, mailboxId, messageId),
    meta: LOCAL_ERROR_HANDLING_META,
    onSuccess: (_result, messageId) => {
      queryClient.removeQueries({
        queryKey: mailboxKeys.message(organizationId, mailboxId, messageId),
      });
    },
    onSettled: () => invalidateMail(queryClient, organizationId, mailboxId, true),
  });
}

/**
 * One page of a mailbox listing with the organize filters; `group: 'thread'` lists one row per
 * thread of a placement view.
 */
export function useMailMessageList(
  organizationId: number,
  mailboxId: number,
  filters: MailMessageFilters,
  page: number
) {
  return useQuery({
    queryKey: mailboxKeys.filteredPage(organizationId, mailboxId, filters, page),
    queryFn: () =>
      mailboxService.messages(organizationId, mailboxId, {
        ...filters,
        page,
        perPage: MAIL_PAGE_SIZE,
      }),
    enabled: validId(organizationId) && validId(mailboxId),
    placeholderData: keepPreviousData,
    staleTime: 15_000,
    ...liveRefresh,
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

/** A thread's messages outside the trash, oldest first; only asked for once one is open. */
export function useMailThread(organizationId: number, mailboxId: number, threadId: number | null) {
  return useQuery({
    queryKey: mailboxKeys.thread(organizationId, mailboxId, threadId ?? 0),
    queryFn: () => mailboxService.getThread(organizationId, mailboxId, threadId ?? 0),
    enabled: validId(organizationId) && validId(mailboxId) && threadId !== null,
    staleTime: 15_000,
    ...liveRefresh,
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

/** The caller's own unread and starred counts for the mailbox's navigation. */
export function useMailMessageCounts(organizationId: number, mailboxId: number, enabled = true) {
  return useQuery({
    queryKey: mailboxKeys.counts(organizationId, mailboxId),
    queryFn: () => mailboxService.getMessageCounts(organizationId, mailboxId),
    enabled: enabled && validId(organizationId) && validId(mailboxId),
    staleTime: 15_000,
    ...liveRefresh,
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

export function useMailFolders(organizationId: number, mailboxId: number, enabled = true) {
  return useQuery({
    queryKey: mailboxKeys.folders(organizationId, mailboxId),
    queryFn: () => mailboxService.listFolders(organizationId, mailboxId),
    enabled: enabled && validId(organizationId) && validId(mailboxId),
    staleTime: 60_000,
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

export function useMailLabels(organizationId: number, mailboxId: number, enabled = true) {
  return useQuery({
    queryKey: mailboxKeys.labels(organizationId, mailboxId),
    queryFn: () => mailboxService.listLabels(organizationId, mailboxId),
    enabled: enabled && validId(organizationId) && validId(mailboxId),
    staleTime: 60_000,
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

/**
 * Folder and label changes need `organize`. Deleting a folder moves its mail to `default` and
 * deleting a label removes it from every message, so the mail is refreshed with them.
 */
function useOrganizeMutation<Variables, Result>(
  organizationId: number,
  mailboxId: number,
  kind: 'folders' | 'labels',
  mutationFn: (variables: Variables) => Promise<Result>
) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn,
    meta: LOCAL_ERROR_HANDLING_META,
    onSettled: () =>
      Promise.all([
        queryClient.invalidateQueries({ queryKey: mailboxKeys[kind](organizationId, mailboxId) }),
        invalidateMail(queryClient, organizationId, mailboxId),
      ]),
  });
}

export function useCreateMailFolder(organizationId: number, mailboxId: number) {
  return useOrganizeMutation(organizationId, mailboxId, 'folders', (input: MailFolderInput) =>
    mailboxService.createFolder(organizationId, mailboxId, input)
  );
}

export function useRenameMailFolder(organizationId: number, mailboxId: number) {
  return useOrganizeMutation(
    organizationId,
    mailboxId,
    'folders',
    ({ folderId, ...input }: { folderId: number } & MailFolderInput) =>
      mailboxService.renameFolder(organizationId, mailboxId, folderId, input)
  );
}

export function useDeleteMailFolder(organizationId: number, mailboxId: number) {
  return useOrganizeMutation(organizationId, mailboxId, 'folders', (folderId: number) =>
    mailboxService.deleteFolder(organizationId, mailboxId, folderId)
  );
}

export function useCreateMailLabel(organizationId: number, mailboxId: number) {
  return useOrganizeMutation(organizationId, mailboxId, 'labels', (input: MailLabelInput) =>
    mailboxService.createLabel(organizationId, mailboxId, input)
  );
}

export function useUpdateMailLabel(organizationId: number, mailboxId: number) {
  return useOrganizeMutation(
    organizationId,
    mailboxId,
    'labels',
    ({ labelId, ...input }: { labelId: number } & MailLabelInput) =>
      mailboxService.updateLabel(organizationId, mailboxId, labelId, input)
  );
}

export function useDeleteMailLabel(organizationId: number, mailboxId: number) {
  return useOrganizeMutation(organizationId, mailboxId, 'labels', (labelId: number) =>
    mailboxService.deleteLabel(organizationId, mailboxId, labelId)
  );
}

export const MAILBOX_ACCESS_EVENTS_PAGE_SIZE = 20;

/** Members of a shared mailbox; only asked for when the caller may manage them. */
export function useMailboxMembers(organizationId: number, mailboxId: number, enabled: boolean) {
  return useQuery({
    queryKey: mailboxKeys.members(organizationId, mailboxId),
    queryFn: () => mailboxService.members(organizationId, mailboxId),
    enabled: enabled && validId(organizationId) && validId(mailboxId),
    staleTime: 15_000,
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

/** One page of the access history; only asked for with `see_history`. */
export function useMailboxAccessEvents(
  organizationId: number,
  mailboxId: number,
  page: number,
  enabled: boolean
) {
  return useQuery({
    queryKey: mailboxKeys.accessEventPage(organizationId, mailboxId, page),
    queryFn: () =>
      mailboxService.accessEvents(organizationId, mailboxId, {
        page,
        perPage: MAILBOX_ACCESS_EVENTS_PAGE_SIZE,
      }),
    enabled: enabled && validId(organizationId) && validId(mailboxId),
    placeholderData: keepPreviousData,
    staleTime: 15_000,
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

/**
 * An access change can change what the caller may do with the mailbox, its members, its
 * history, and, after a handover or a sealed deletion, which mail is visible anywhere.
 */
function invalidateAccess(queryClient: QueryClient, organizationId: number, mailboxId: number) {
  return Promise.all([
    queryClient.invalidateQueries({ queryKey: mailboxKeys.list(organizationId) }),
    queryClient.invalidateQueries({ queryKey: mailboxKeys.mailbox(organizationId, mailboxId) }),
    invalidateLog(queryClient, organizationId),
  ]);
}

function useAccessMutation<Variables, Result>(
  organizationId: number,
  mailboxId: number,
  mutationFn: (variables: Variables) => Promise<Result>
) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn,
    meta: LOCAL_ERROR_HANDLING_META,
    onSettled: () => invalidateAccess(queryClient, organizationId, mailboxId),
  });
}

export function useSetMailboxMember(organizationId: number, mailboxId: number) {
  return useAccessMutation(
    organizationId,
    mailboxId,
    ({ userId, permission }: { userId: number; permission: MailboxPermission }) =>
      mailboxService.setMember(organizationId, mailboxId, userId, permission)
  );
}

export function useRemoveMailboxMember(organizationId: number, mailboxId: number) {
  return useAccessMutation(organizationId, mailboxId, (userId: number) =>
    mailboxService.removeMember(organizationId, mailboxId, userId)
  );
}

export function useHandOverMailbox(organizationId: number, mailboxId: number) {
  return useAccessMutation(organizationId, mailboxId, (successorUserId: number) =>
    mailboxService.handOver(organizationId, mailboxId, successorUserId)
  );
}

export function useDeleteMailboxSealedHistory(organizationId: number, mailboxId: number) {
  return useAccessMutation(organizationId, mailboxId, (confirmAddress: string) =>
    mailboxService.deleteSealedHistory(organizationId, mailboxId, confirmAddress)
  );
}

export function useSetMailboxWebhooks(organizationId: number, mailboxId: number) {
  return useAccessMutation(organizationId, mailboxId, (enabled: boolean) =>
    mailboxService.setWebhooks(organizationId, mailboxId, enabled)
  );
}
