'use client';

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';

import { LOCAL_ERROR_HANDLING_META } from '@/config/query-meta';
import { mailboxKeys, validId } from '@/features/mailbox/hooks/use-mailbox';
import { triageService } from '@/features/mailbox/services/triage-service';
import type {
  MailTriageOverrideInput,
  MailTriageSettingInput,
} from '@/features/mailbox/triage-schemas';

/** Cache key of the organization's AI triage switches. */
export function mailTriageSettingKey(organizationId: number) {
  return [...mailboxKeys.organization(organizationId), 'triage-setting'] as const;
}

/** The organization's AI triage switches; any member reads them. */
export function useMailTriageSetting(organizationId: number, enabled = true) {
  return useQuery({
    queryKey: mailTriageSettingKey(organizationId),
    queryFn: () => triageService.setting(organizationId),
    enabled: enabled && validId(organizationId),
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

/**
 * Replaces the organization's switches (owners and admins). Triage results, counts, and the
 * mailbox list depend on them, so the organization's mail queries refresh afterwards.
 */
export function useSaveMailTriageSetting(organizationId: number) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: MailTriageSettingInput) => triageService.saveSetting(organizationId, input),
    meta: LOCAL_ERROR_HANDLING_META,
    onSuccess: saved => queryClient.setQueryData(mailTriageSettingKey(organizationId), saved),
    onSettled: () =>
      queryClient.invalidateQueries({ queryKey: mailboxKeys.organization(organizationId) }),
  });
}

/** Turns one mailbox's triage on or off; the mailbox list carries the switch. */
export function useSetMailboxTriage(organizationId: number, mailboxId: number) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (enabled: boolean) =>
      triageService.setMailboxTriage(organizationId, mailboxId, enabled),
    meta: LOCAL_ERROR_HANDLING_META,
    onSettled: () =>
      Promise.all([
        queryClient.invalidateQueries({ queryKey: mailboxKeys.list(organizationId) }),
        queryClient.invalidateQueries({ queryKey: mailboxKeys.mailbox(organizationId, mailboxId) }),
      ]),
  });
}

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
