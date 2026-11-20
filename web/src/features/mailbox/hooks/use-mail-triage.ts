'use client';

import { useMutation, useQueryClient } from '@tanstack/react-query';

import { LOCAL_ERROR_HANDLING_META } from '@/config/query-meta';
import { mailboxKeys } from '@/features/mailbox/hooks/use-mailbox';
import { triageService } from '@/features/mailbox/services/triage-service';
import type { MailTriageOverrideInput } from '@/features/mailbox/triage-schemas';

/**
 * The reader's own triage category for an inbound message. Every reader sees it, so the unified
 * inbox rows and counts of the organization refresh afterwards.
 */
export function useOverrideMailTriage(organizationId: number) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({
      mailboxId,
      messageId,
      ...input
    }: { mailboxId: number; messageId: number } & MailTriageOverrideInput) =>
      triageService.overrideTriage(organizationId, mailboxId, messageId, input),
    meta: LOCAL_ERROR_HANDLING_META,
    onSettled: () =>
      Promise.all([
        queryClient.invalidateQueries({ queryKey: mailboxKeys.inboxes(organizationId) }),
        queryClient.invalidateQueries({ queryKey: mailboxKeys.allInboxCounts(organizationId) }),
      ]),
  });
}

/** Generates a reply draft on request; the draft lives only in this mutation's result. */
export function useMailReplyDraft(organizationId: number) {
  return useMutation({
    mutationFn: ({ mailboxId, messageId }: { mailboxId: number; messageId: number }) =>
      triageService.replyDraft(organizationId, mailboxId, messageId),
    meta: LOCAL_ERROR_HANDLING_META,
  });
}
