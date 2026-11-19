'use client';

import { useInfiniteQuery, useMutation, useQueryClient } from '@tanstack/react-query';

import { LOCAL_ERROR_HANDLING_META } from '@/config/query-meta';
import {
  invalidateMail,
  liveRefresh,
  MAIL_PAGE_SIZE,
  mailboxKeys,
  validId,
} from '@/features/mailbox/hooks/use-mailbox';
import { mailboxService } from '@/features/mailbox/services/mailbox-service';
import type { InboxFilters, UpdateMailMessagesInput } from '@/features/mailbox/types';

/**
 * The unified inbox, one cursor page at a time: each next page continues after the last one's
 * `meta.next_cursor`, and the list stays fresh while the tab is visible.
 */
export function useInboxMessages(organizationId: number, filters: InboxFilters) {
  return useInfiniteQuery({
    queryKey: mailboxKeys.inbox(organizationId, filters),
    queryFn: ({ pageParam }) =>
      mailboxService.listInbox(organizationId, filters, pageParam, MAIL_PAGE_SIZE),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: lastPage => lastPage.meta.next_cursor ?? undefined,
    enabled: validId(organizationId),
    staleTime: 15_000,
    ...liveRefresh,
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

/**
 * One change for 1 to 100 messages of one mailbox from the unified inbox, where each row belongs to
 * a different mailbox, so the mailbox is a variable of the mutation rather than of the hook.
 */
export function useUpdateInboxMessages(organizationId: number) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ mailboxId, ...input }: { mailboxId: number } & UpdateMailMessagesInput) =>
      mailboxService.updateMessages(organizationId, mailboxId, input),
    meta: LOCAL_ERROR_HANDLING_META,
    onSettled: (_result, _error, { mailboxId }) =>
      invalidateMail(queryClient, organizationId, mailboxId),
  });
}
