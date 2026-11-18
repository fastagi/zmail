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
  MailDomain,
  MailDirection,
  MailLogFilters,
  MailboxPermission,
  SendMailMessageInput,
} from '@/features/mailbox/types';

export const MAIL_PAGE_SIZE = 20;
/** How often a domain whose setup runs is read again. */
const setupRefreshMs = 3_000;
export const MAIL_STATS_DAYS = 7;

/** Every key names the organization, so one organization's data is never shown for another. */
export const mailboxKeys = {
  all: ['mailbox'] as const,
  organization: (organizationId: number) => [...mailboxKeys.all, organizationId] as const,
  domains: (organizationId: number) =>
    [...mailboxKeys.organization(organizationId), 'domains'] as const,
  mailDomains: (organizationId: number) =>
    [...mailboxKeys.organization(organizationId), 'mail-domains'] as const,
  list: (organizationId: number) => [...mailboxKeys.organization(organizationId), 'list'] as const,
  mailbox: (organizationId: number, mailboxId: number) =>
    [...mailboxKeys.organization(organizationId), mailboxId] as const,
  messages: (organizationId: number, mailboxId: number) =>
    [...mailboxKeys.mailbox(organizationId, mailboxId), 'messages'] as const,
  page: (organizationId: number, mailboxId: number, direction: MailDirection, page: number) =>
    [...mailboxKeys.messages(organizationId, mailboxId), direction, page] as const,
  message: (organizationId: number, mailboxId: number, messageId: number) =>
    [...mailboxKeys.mailbox(organizationId, mailboxId), 'message', messageId] as const,
  members: (organizationId: number, mailboxId: number) =>
    [...mailboxKeys.mailbox(organizationId, mailboxId), 'members'] as const,
  accessEvents: (organizationId: number, mailboxId: number) =>
    [...mailboxKeys.mailbox(organizationId, mailboxId), 'access-events'] as const,
  accessEventPage: (organizationId: number, mailboxId: number, page: number) =>
    [...mailboxKeys.accessEvents(organizationId, mailboxId), page] as const,
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

function validId(value: number): boolean {
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

/** A connected domain changes which domains new mailboxes may use. */
function refreshDomains(queryClient: QueryClient, organizationId: number) {
  return Promise.all([
    queryClient.invalidateQueries({ queryKey: mailboxKeys.mailDomains(organizationId) }),
    queryClient.invalidateQueries({ queryKey: mailboxKeys.domains(organizationId) }),
  ]);
}

export function useConnectMailDomain(organizationId: number) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (name: string) => mailboxService.connectMailDomain(organizationId, { name }),
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

export function useMailboxes(organizationId: number) {
  return useQuery({
    queryKey: mailboxKeys.list(organizationId),
    queryFn: () => mailboxService.list(organizationId),
    enabled: validId(organizationId),
    staleTime: 15_000,
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

export function useMailMessages(
  organizationId: number,
  mailboxId: number,
  direction: MailDirection,
  page: number
) {
  return useQuery({
    queryKey: mailboxKeys.page(organizationId, mailboxId, direction, page),
    queryFn: () =>
      mailboxService.messages(organizationId, mailboxId, {
        direction,
        page,
        perPage: MAIL_PAGE_SIZE,
      }),
    enabled: validId(organizationId),
    placeholderData: keepPreviousData,
    staleTime: 15_000,
    meta: LOCAL_ERROR_HANDLING_META,
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
    onSettled: () =>
      Promise.all([
        queryClient.invalidateQueries({
          queryKey: mailboxKeys.messages(organizationId, mailboxId),
        }),
        invalidateLog(queryClient, organizationId),
      ]),
  });
}

export function useUpdateMailMessage(organizationId: number, mailboxId: number) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ messageId, read }: { messageId: number; read: boolean }) =>
      mailboxService.setRead(organizationId, mailboxId, messageId, read),
    meta: LOCAL_ERROR_HANDLING_META,
    onSuccess: message => {
      queryClient.setQueryData(mailboxKeys.message(organizationId, mailboxId, message.id), message);
    },
    // Read state changes the unread count shown in the mailbox list.
    onSettled: () =>
      Promise.all([
        queryClient.invalidateQueries({
          queryKey: mailboxKeys.messages(organizationId, mailboxId),
        }),
        queryClient.invalidateQueries({ queryKey: mailboxKeys.list(organizationId) }),
        invalidateLog(queryClient, organizationId, false),
      ]),
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
      Promise.all([
        queryClient.invalidateQueries({
          queryKey: mailboxKeys.messages(organizationId, mailboxId),
        }),
        queryClient.invalidateQueries({ queryKey: mailboxKeys.list(organizationId) }),
        invalidateLog(queryClient, organizationId, false),
      ]),
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
    // Deleting an unread message lowers the unread count shown in the mailbox list.
    onSettled: () =>
      Promise.all([
        queryClient.invalidateQueries({
          queryKey: mailboxKeys.messages(organizationId, mailboxId),
        }),
        queryClient.invalidateQueries({ queryKey: mailboxKeys.list(organizationId) }),
        invalidateLog(queryClient, organizationId),
      ]),
  });
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
