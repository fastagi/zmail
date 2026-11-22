'use client';

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';

import { LOCAL_ERROR_HANDLING_META } from '@/config/query-meta';
import type { MailDraftInput } from '@/features/mailbox/draft-schemas';
import { mailboxKeys, validId } from '@/features/mailbox/hooks/mailbox-keys';
import { draftService } from '@/features/mailbox/services/draft-service';

/** Cache key of the caller's drafts in an organization. */
export function mailDraftsKey(organizationId: number) {
  return [...mailboxKeys.organization(organizationId), 'drafts'] as const;
}

/** The caller's saved drafts, the latest change first. */
export function useMailDrafts(organizationId: number, enabled = true) {
  return useQuery({
    queryKey: mailDraftsKey(organizationId),
    queryFn: () => draftService.drafts(organizationId),
    enabled: enabled && validId(organizationId),
    staleTime: 30_000,
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

/** Reads one draft in full, to continue it in the composer. */
export function useOpenMailDraft(organizationId: number) {
  return useMutation({
    mutationFn: (id: number) => draftService.draft(organizationId, id),
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

/** Saves a new draft (`id` null) or replaces one; the list refreshes either way. */
export function useSaveMailDraft(organizationId: number) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ input, id }: { input: MailDraftInput; id: number | null }) =>
      draftService.save(organizationId, input, id),
    onSettled: () => queryClient.invalidateQueries({ queryKey: mailDraftsKey(organizationId) }),
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

/** Deletes one of the caller's drafts; the list refreshes either way. */
export function useDeleteMailDraft(organizationId: number) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => draftService.remove(organizationId, id),
    onSettled: () => queryClient.invalidateQueries({ queryKey: mailDraftsKey(organizationId) }),
    meta: LOCAL_ERROR_HANDLING_META,
  });
}
