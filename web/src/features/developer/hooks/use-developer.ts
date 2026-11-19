'use client';

import {
  useInfiniteQuery,
  useMutation,
  useQuery,
  useQueryClient,
  type InfiniteData,
} from '@tanstack/react-query';

import { LOCAL_ERROR_HANDLING_META } from '@/config/query-meta';
import { developerService } from '@/features/developer/services/developer-service';
import type {
  MailSuppressionFilters,
  TransactionalLogEntryDetail,
  TransactionalLogFilters,
  TransactionalLogPage,
  TransactionalTestSendInput,
} from '@/features/developer/types';

/** How often the send log is read again while the page is visible. */
export const TRANSACTIONAL_LOG_REFRESH_MS = 10_000;
export const TRANSACTIONAL_LOG_PAGE_SIZE = 50;
export const MAIL_SUPPRESSION_PAGE_SIZE = 50;

/** Every key names the organization, so one organization's mail is never shown for another. */
export const developerKeys = {
  all: ['developer'] as const,
  organization: (organizationId: number) => [...developerKeys.all, organizationId] as const,
  logs: (organizationId: number) => [...developerKeys.organization(organizationId), 'log'] as const,
  log: (organizationId: number, filters: TransactionalLogFilters, perPage: number) =>
    [
      ...developerKeys.logs(organizationId),
      perPage,
      filters.mode,
      filters.status ?? '',
      filters.lastEvent ?? '',
      filters.outcome ?? '',
      filters.template ?? '',
      filters.to?.trim().toLowerCase() ?? '',
      filters.since ?? '',
      filters.until ?? '',
    ] as const,
  logEntries: (organizationId: number) =>
    [...developerKeys.organization(organizationId), 'log-entry'] as const,
  logEntry: (organizationId: number, messageId: number) =>
    [...developerKeys.logEntries(organizationId), messageId] as const,
  sendLanes: (organizationId: number) =>
    [...developerKeys.organization(organizationId), 'send-lanes'] as const,
  suppressionLists: (organizationId: number) =>
    [...developerKeys.organization(organizationId), 'suppressions'] as const,
  suppressions: (organizationId: number, filters: MailSuppressionFilters) =>
    [
      ...developerKeys.suppressionLists(organizationId),
      filters.email?.trim().toLowerCase() ?? '',
      filters.reason ?? '',
    ] as const,
};

function validId(value: number): boolean {
  return Number.isSafeInteger(value) && value > 0;
}

// The console renders every error inline, so the global error toast stays quiet.

/**
 * The send log, one keyset page at a time: each next page continues after the last one's
 * `meta.next_cursor`. It is read again every 10 seconds while the page is visible.
 */
export function useTransactionalLog(
  organizationId: number,
  filters: TransactionalLogFilters,
  {
    perPage = TRANSACTIONAL_LOG_PAGE_SIZE,
    enabled = true,
  }: { perPage?: number; enabled?: boolean } = {}
) {
  return useInfiniteQuery({
    queryKey: developerKeys.log(organizationId, filters, perPage),
    queryFn: ({ pageParam }) =>
      developerService.log(organizationId, filters, {
        ...(pageParam === undefined ? {} : { cursor: pageParam }),
        perPage,
      }),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: lastPage => lastPage.meta.next_cursor ?? undefined,
    enabled: enabled && validId(organizationId),
    staleTime: TRANSACTIONAL_LOG_REFRESH_MS,
    // Only the newest page refreshes: an infinite query reads every loaded page again, so the
    // timer and the focus refresh stop once the user has loaded older pages.
    refetchInterval: query =>
      (query.state.data?.pages.length ?? 0) > 1 ? false : TRANSACTIONAL_LOG_REFRESH_MS,
    refetchIntervalInBackground: false,
    refetchOnWindowFocus: query => (query.state.data?.pages.length ?? 0) <= 1,
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

/**
 * One log entry with its timeline; `messageId` null holds the query back. `refetchInterval` follows
 * a fresh message until its delivery events arrive.
 */
export function useTransactionalLogEntry(
  organizationId: number,
  messageId: number | null,
  {
    refetchInterval = false,
  }: {
    refetchInterval?:
      number | false | ((data: TransactionalLogEntryDetail | undefined) => number | false);
  } = {}
) {
  return useQuery({
    queryKey: developerKeys.logEntry(organizationId, messageId ?? 0),
    queryFn: () => developerService.logEntry(organizationId, messageId ?? 0),
    enabled: validId(organizationId) && messageId !== null && validId(messageId),
    refetchInterval:
      typeof refetchInterval === 'function'
        ? query => refetchInterval(query.state.data)
        : refetchInterval,
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

/**
 * A console test send to the caller's own address. The new message is the newest of its mode, so
 * every log of the organization drops to its first page and is read again.
 */
export function useTransactionalTestSend(organizationId: number) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: TransactionalTestSendInput) =>
      developerService.testSend(organizationId, input),
    onSuccess: () => {
      queryClient.setQueriesData<InfiniteData<TransactionalLogPage, string | undefined>>(
        { queryKey: developerKeys.logs(organizationId) },
        data =>
          data && data.pages.length > 1
            ? { pages: data.pages.slice(0, 1), pageParams: data.pageParams.slice(0, 1) }
            : data
      );
      return queryClient.invalidateQueries({ queryKey: developerKeys.logs(organizationId) });
    },
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

/**
 * How full each send lane is. Only owners and admins may read usage, so the query runs only when
 * `canManage`; members read the limits from `mailbox-domains`. The API counts it on every request.
 */
export function useSendLanes(organizationId: number, { canManage }: { canManage: boolean }) {
  return useQuery({
    queryKey: developerKeys.sendLanes(organizationId),
    queryFn: () => developerService.sendLanes(organizationId),
    enabled: canManage && validId(organizationId),
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

/** The suppression list, one keyset page at a time, newest first; owners and admins only. */
export function useMailSuppressions(
  organizationId: number,
  filters: MailSuppressionFilters,
  { enabled = true }: { enabled?: boolean } = {}
) {
  return useInfiniteQuery({
    queryKey: developerKeys.suppressions(organizationId, filters),
    queryFn: ({ pageParam }) =>
      developerService.suppressions(organizationId, filters, {
        ...(pageParam === undefined ? {} : { cursor: pageParam }),
        perPage: MAIL_SUPPRESSION_PAGE_SIZE,
      }),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: lastPage => lastPage.meta.next_cursor ?? undefined,
    enabled: enabled && validId(organizationId),
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

/** Takes an address off the suppression list, then reads every list of the organization again. */
export function useRemoveMailSuppression(organizationId: number) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (suppressionId: number) =>
      developerService.removeSuppression(organizationId, suppressionId),
    onSuccess: () =>
      queryClient.invalidateQueries({ queryKey: developerKeys.suppressionLists(organizationId) }),
    meta: LOCAL_ERROR_HANDLING_META,
  });
}
