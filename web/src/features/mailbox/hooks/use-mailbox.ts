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
  MailDomainInput,
  MailDirection,
  MailDnsRecordInput,
  MailDnsRecordPatch,
  MailLogFilters,
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
  page: (organizationId: number, mailboxId: number, direction: MailDirection, page: number) =>
    [...mailboxKeys.messages(organizationId, mailboxId), direction, page] as const,
  message: (organizationId: number, mailboxId: number, messageId: number) =>
    [...mailboxKeys.mailbox(organizationId, mailboxId), 'message', messageId] as const,
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
