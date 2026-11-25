'use client';

import {
  keepPreviousData,
  useMutation,
  useQuery,
  useQueryClient,
  type QueryClient,
} from '@tanstack/react-query';

import { LOCAL_ERROR_HANDLING_META } from '@/config/query-meta';
import { mailboxKeys, validId } from '@/features/mailbox/hooks/mailbox-keys';
import { mailboxService } from '@/features/mailbox/services/mailbox-service';
import type { MailboxPermission } from '@/features/mailbox/types';

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
    // The directory counts members.
    queryClient.invalidateQueries({ queryKey: mailboxKeys.directories(organizationId) }),
    queryClient.invalidateQueries({ queryKey: mailboxKeys.mailbox(organizationId, mailboxId) }),
    queryClient.invalidateQueries({ queryKey: mailboxKeys.log(organizationId) }),
    queryClient.invalidateQueries({ queryKey: mailboxKeys.stats(organizationId) }),
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

export function useSetPendingMailboxMember(organizationId: number) {
  return useMutation({
    mutationFn: ({
      mailboxId,
      email,
      permission,
    }: {
      mailboxId: number;
      email: string;
      permission: MailboxPermission;
    }) => mailboxService.setPendingMember(organizationId, mailboxId, email, permission),
    meta: LOCAL_ERROR_HANDLING_META,
  });
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
