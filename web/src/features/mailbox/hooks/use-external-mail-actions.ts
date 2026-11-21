'use client';

import { useEffect } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';

import { LOCAL_ERROR_HANDLING_META } from '@/config/query-meta';
import type {
  CompleteExternalMailAuthorizationInput,
  StartExternalMailAuthorizationInput,
} from '@/features/mailbox/external-mail-schemas';
import { externalMailKeys } from '@/features/mailbox/hooks/use-external-mail';
import { mailboxKeys } from '@/features/mailbox/hooks/mailbox-keys';
import { navigateToProvider, rememberAuthorization } from '@/features/mailbox/utils/external-mail';

/**
 * What a member does with external mail: start or reconnect a sign-in, finish it, and unbind. Only
 * the dialogs, the reconnect notice, and the callback page use these, and each loads on demand.
 */
const loadService = () => import('@/features/mailbox/services/external-mail-service');
const service = async () => (await loadService()).externalMailService;

/** Starts a sign-in or a reconnect; the caller sends the browser to the answered URL. */
export function useStartExternalMailAuthorization(organizationId: number) {
  return useMutation({
    mutationFn: async (input: StartExternalMailAuthorizationInput) =>
      (await service()).start(organizationId, input),
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

/** Finishes a sign-in; the mailbox list and the connections are read again afterwards. */
export function useCompleteExternalMailAuthorization(organizationId: number) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (input: CompleteExternalMailAuthorizationInput) =>
      (await service()).complete(organizationId, input),
    meta: LOCAL_ERROR_HANDLING_META,
    onSuccess: () =>
      Promise.all([
        queryClient.invalidateQueries({ queryKey: externalMailKeys.connections(organizationId) }),
        queryClient.invalidateQueries({ queryKey: mailboxKeys.list(organizationId) }),
      ]),
  });
}

/** Unbinds one of the caller's accounts. */
export function useDisconnectExternalMail(organizationId: number) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (connectionId: number) =>
      (await service()).disconnect(organizationId, connectionId),
    meta: LOCAL_ERROR_HANDLING_META,
    onSettled: () =>
      queryClient.invalidateQueries({ queryKey: externalMailKeys.connections(organizationId) }),
  });
}

/**
 * Starts a sign-in or a reconnect, remembers what the callback page needs under the answered
 * `state`, and sends the browser to the provider. The returned mutation stays pending while the
 * browser leaves, so the control that started it stays disabled.
 */
export function useBeginExternalAuthorization(organizationId: number) {
  const start = useStartExternalMailAuthorization(organizationId);
  const { reset } = start;
  // Back from the provider restores this page from the back-forward cache with the sign-in still
  // "leaving"; it starts over so the button can be used again.
  useEffect(() => {
    const restored = (event: PageTransitionEvent) => {
      if (event.persisted) reset();
    };
    window.addEventListener('pageshow', restored);
    return () => window.removeEventListener('pageshow', restored);
  }, [reset]);
  const begin = (
    input:
      | { kind: 'new'; provider: string; backfillDays: number }
      | { kind: 'reconnect'; provider: string; connectionId: number }
  ) =>
    start.mutate(
      input.kind === 'new'
        ? { provider: input.provider, backfill_days: input.backfillDays }
        : { connection_id: input.connectionId },
      {
        onSuccess: async authorization => {
          const state = (await loadService()).authorizationState(authorization.authorization_url);
          if (state) {
            rememberAuthorization(state, {
              organizationId,
              provider: input.provider,
              backfillDays: input.kind === 'new' ? input.backfillDays : null,
              connectionId: input.kind === 'reconnect' ? input.connectionId : null,
            });
          }
          navigateToProvider(authorization.authorization_url);
        },
      }
    );
  return {
    begin,
    isPending: start.isPending || start.isSuccess,
    error: start.error,
    reset: start.reset,
  };
}
