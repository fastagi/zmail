'use client';

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';

import { LOCAL_ERROR_HANDLING_META } from '@/config/query-meta';
import type { MailThreadAssignmentInput } from '@/features/mailbox/assignment-schemas';
import { mailboxKeys, validId } from '@/features/mailbox/hooks/mailbox-keys';
import { assignmentService } from '@/features/mailbox/services/assignment-service';

/** Cache key of a thread's assignment. */
export function threadAssignmentKey(organizationId: number, mailboxId: number, threadId: number) {
  return [...mailboxKeys.mailbox(organizationId, mailboxId), 'assignment', threadId] as const;
}

/** Who works a shared mailbox's thread; polled so a colleague's change shows up on its own. */
export function useThreadAssignment(
  organizationId: number,
  mailboxId: number,
  threadId: number,
  enabled = true
) {
  return useQuery({
    queryKey: threadAssignmentKey(organizationId, mailboxId, threadId),
    queryFn: () => assignmentService.assignment(organizationId, mailboxId, threadId),
    enabled: enabled && validId(organizationId) && validId(mailboxId) && validId(threadId),
    staleTime: 15_000,
    refetchInterval: 30_000,
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

/** Replaces a thread's assignment; the saved one becomes the cached one. */
export function useSaveThreadAssignment(organizationId: number, mailboxId: number, threadId: number) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: MailThreadAssignmentInput) =>
      assignmentService.saveAssignment(organizationId, mailboxId, threadId, input),
    onSuccess: assignment => {
      queryClient.setQueryData(threadAssignmentKey(organizationId, mailboxId, threadId), assignment);
    },
    meta: LOCAL_ERROR_HANDLING_META,
  });
}
