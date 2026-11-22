'use client';

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';

import { LOCAL_ERROR_HANDLING_META } from '@/config/query-meta';
import { mailboxKeys, validId } from '@/features/mailbox/hooks/mailbox-keys';
import { signatureService } from '@/features/mailbox/services/signature-service';

/** Cache key of the caller's signature for a mailbox. */
export function mailSignatureKey(organizationId: number, mailboxId: number) {
  return [...mailboxKeys.mailbox(organizationId, mailboxId), 'signature'] as const;
}

/** The caller's signature for a mailbox they may send from. */
export function useMailSignature(organizationId: number, mailboxId: number, enabled = true) {
  return useQuery({
    queryKey: mailSignatureKey(organizationId, mailboxId),
    queryFn: () => signatureService.signature(organizationId, mailboxId),
    enabled: enabled && validId(organizationId) && validId(mailboxId),
    staleTime: 60_000,
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

/** Replaces the caller's signature; the saved one becomes the cached one. */
export function useSaveMailSignature(organizationId: number, mailboxId: number) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: string) => signatureService.saveSignature(organizationId, mailboxId, body),
    onSuccess: signature => {
      queryClient.setQueryData(mailSignatureKey(organizationId, mailboxId), signature);
    },
    meta: LOCAL_ERROR_HANDLING_META,
  });
}
