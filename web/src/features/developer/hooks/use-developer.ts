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
  TransactionalLogFilters,
  TransactionalLogPage,
  TransactionalTestSendInput,
} from '@/features/developer/types';

/** How often the send log is read again while the page is visible. */
export const TRANSACTIONAL_LOG_REFRESH_MS = 10_000;
export const TRANSACTIONAL_LOG_PAGE_SIZE = 50;

/** Every key names the organization, so one organization's mail is never shown for another. */
export const developerKeys = {
  all: ['developer'] as const,
  organization: (organizationId: number) => [...developerKeys.all, organizationId] as const,
  logs: (organizationId: number) => [...developerKeys.organization(organizationId), 'log'] as const,
  log: (organizationId: number, filters: TransactionalLogFilters) =>
    [
      ...developerKeys.logs(organizationId),
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
};

function validId(value: number): boolean {
  return Number.isSafeInteger(value) && value > 0;
}

// The console renders every error inline, so the global error toast stays quiet.

/**
 * The send log, one keyset page at a time: each next page continues after the last one's
 * `meta.next_cursor`. It is read again every 10 seconds while the page is visible.
 */
export function useTransactionalLog(organizationId: number, filters: TransactionalLogFilters) {
  return useInfiniteQuery({
    queryKey: developerKeys.log(organizationId, filters),
    queryFn: ({ pageParam }) =>
      developerService.log(organizationId, filters, {
        ...(pageParam === undefined ? {} : { cursor: pageParam }),
        perPage: TRANSACTIONAL_LOG_PAGE_SIZE,
      }),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: lastPage => lastPage.meta.next_cursor ?? undefined,
    enabled: validId(organizationId),
    staleTime: TRANSACTIONAL_LOG_REFRESH_MS,
    refetchInterval: TRANSACTIONAL_LOG_REFRESH_MS,
    refetchIntervalInBackground: false,
    refetchOnWindowFocus: true,
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

/** One log entry with its timeline; `messageId` null holds the query back. */
export function useTransactionalLogEntry(organizationId: number, messageId: number | null) {
  return useQuery({
    queryKey: developerKeys.logEntry(organizationId, messageId ?? 0),
    queryFn: () => developerService.logEntry(organizationId, messageId ?? 0),
    enabled: validId(organizationId) && messageId !== null && validId(messageId),
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
