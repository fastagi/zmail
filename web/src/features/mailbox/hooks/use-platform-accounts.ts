'use client';

import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';

import { LOCAL_ERROR_HANDLING_META } from '@/config/query-meta';
import { mailboxKeys, validId } from '@/features/mailbox/hooks/use-mailbox';
import type {
  PlatformAccountFilters,
  PlatformAccountHandoverInput,
  PlatformAccountInput,
} from '@/features/mailbox/platform-account-schemas';
import { platformAccountService } from '@/features/mailbox/services/platform-account-service';

export const platformAccountKeys = {
  all: (organizationId: number) =>
    [...mailboxKeys.organization(organizationId), 'platform-accounts'] as const,
  page: (organizationId: number, filters: PlatformAccountFilters, page: number, perPage: number) =>
    [
      ...platformAccountKeys.all(organizationId),
      'page',
      filters.q ?? '',
      filters.roleId ?? 0,
      filters.mailboxId ?? 0,
      filters.ownerUserId ?? 0,
      filters.status ?? '',
      page,
      perPage,
    ] as const,
  roleCount: (organizationId: number, roleId: number) =>
    [...platformAccountKeys.all(organizationId), 'role-count', roleId] as const,
  events: (organizationId: number, accountId: number, page: number) =>
    [...platformAccountKeys.all(organizationId), 'events', accountId, page] as const,
};

/** One page of the ledger, as the caller may see it; keeps the last page while the next loads. */
export function usePlatformAccounts(
  organizationId: number,
  filters: PlatformAccountFilters = {},
  page = 1,
  perPage = 15
) {
  return useQuery({
    queryKey: platformAccountKeys.page(organizationId, filters, page, perPage),
    queryFn: () => platformAccountService.list(organizationId, filters, { page, perPage }),
    enabled: validId(organizationId),
    placeholderData: keepPreviousData,
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

/**
 * How many accounts the caller may see in one mail role: a one-entry page filtered by the role,
 * read for its total.
 */
export function usePlatformAccountCount(organizationId: number, roleId: number, enabled = true) {
  return useQuery({
    queryKey: platformAccountKeys.roleCount(organizationId, roleId),
    queryFn: async () =>
      (await platformAccountService.list(organizationId, { roleId }, { page: 1, perPage: 1 })).meta
        .total,
    enabled: enabled && validId(organizationId) && validId(roleId),
    staleTime: 30_000,
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

/** One page of an account's history, newest first. */
export function usePlatformAccountEvents(organizationId: number, accountId: number, page = 1) {
  return useQuery({
    queryKey: platformAccountKeys.events(organizationId, accountId, page),
    queryFn: () => platformAccountService.events(organizationId, accountId, { page }),
    enabled: validId(organizationId) && validId(accountId),
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

function useLedgerMutation<TInput, TResult>(
  organizationId: number,
  mutationFn: (input: TInput) => Promise<TResult>
) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn,
    meta: LOCAL_ERROR_HANDLING_META,
    // Every write can change listings and histories, so the whole ledger is refetched.
    onSettled: () =>
      queryClient.invalidateQueries({ queryKey: platformAccountKeys.all(organizationId) }),
  });
}

export function useCreatePlatformAccount(organizationId: number) {
  return useLedgerMutation(organizationId, (input: PlatformAccountInput) =>
    platformAccountService.create(organizationId, input)
  );
}

export function useUpdatePlatformAccount(organizationId: number) {
  return useLedgerMutation(
    organizationId,
    ({ accountId, ...input }: { accountId: number } & PlatformAccountInput) =>
      platformAccountService.update(organizationId, accountId, input)
  );
}

export function useDeletePlatformAccount(organizationId: number) {
  return useLedgerMutation(organizationId, (accountId: number) =>
    platformAccountService.remove(organizationId, accountId)
  );
}

/** Moves every account of one owner to another; independent of a mailbox handover. */
export function useHandOverPlatformAccounts(organizationId: number) {
  return useLedgerMutation(organizationId, (input: PlatformAccountHandoverInput) =>
    platformAccountService.handOver(organizationId, input)
  );
}
