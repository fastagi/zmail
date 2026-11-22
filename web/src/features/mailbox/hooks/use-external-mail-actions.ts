'use client';

import { useEffect, useRef } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';

import { LOCAL_ERROR_HANDLING_META } from '@/config/query-meta';
import type {
  BindExternalMailCredentialsInput,
  CompleteExternalMailAuthorizationInput,
  StartExternalMailAuthorizationInput,
} from '@/features/mailbox/external-mail-schemas';
import { externalMailKeys } from '@/features/mailbox/hooks/use-external-mail';
import { mailboxKeys } from '@/features/mailbox/hooks/mailbox-keys';
import { navigateToProvider, rememberAuthorization } from '@/features/mailbox/utils/external-mail';
import { ClientErrorCode } from '@/http/codes';
import { ApiError } from '@/http/request';

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

/** The bind request without its password: all a mutation may keep in the cache. */
export type ExternalMailCredentialsRequest = Omit<BindExternalMailCredentialsInput, 'password'>;

/**
 * Binds an account with an app password. The password never becomes a mutation variable, so the
 * mutation cache, devtools, and a retry never hold it: it waits in a ref only until the request
 * is built. The ref is cleared when the request starts, on every outcome, by `forget` (the dialog
 * closing), and on unmount. Offline, the bind fails at once instead of pausing until the network
 * returns, so a password is never sent after the member closed the dialog.
 */
export function useBindExternalMailCredentials(organizationId: number) {
  const queryClient = useQueryClient();
  const secret = useRef<string | null>(null);
  useEffect(
    () => () => {
      secret.current = null;
    },
    []
  );
  const mutation = useMutation({
    mutationFn: async (input: ExternalMailCredentialsRequest) => {
      const password = secret.current;
      secret.current = null;
      if (password === null) throw new Error('missing password');
      return (await service()).bindCredentials(organizationId, { ...input, password });
    },
    networkMode: 'always',
    gcTime: 0,
    retry: false,
    meta: LOCAL_ERROR_HANDLING_META,
    onSettled: () => {
      secret.current = null;
    },
  });
  const { mutateAsync } = mutation;
  /**
   * Resolves with the completion even when the form unmounts meanwhile (the inbox re-renders as
   * soon as the new mailbox is listed), so the caller can still close and navigate. The mailbox
   * list and connections are read again only after the caller has acted on the answer.
   */
  const bind = async (input: BindExternalMailCredentialsInput) => {
    const { password, ...rest } = input;
    if (typeof navigator !== 'undefined' && navigator.onLine === false) {
      throw new ApiError('Offline', ClientErrorCode.NETWORK_ERROR);
    }
    secret.current = password;
    try {
      const completion = await mutateAsync(rest);
      queueMicrotask(() => {
        void queryClient.invalidateQueries({
          queryKey: externalMailKeys.connections(organizationId),
        });
        void queryClient.invalidateQueries({ queryKey: mailboxKeys.list(organizationId) });
      });
      return completion;
    } finally {
      secret.current = null;
    }
  };
  const forget = () => {
    secret.current = null;
  };
  return { ...mutation, bind, forget };
}

/**
 * Everything cached about one connected mailbox: its messages, counts, folders, and agent grants
 * (the agent grant keys live under the mailbox key), plus the unified inbox and its counts.
 */
function invalidateConnectedMailbox(
  queryClient: ReturnType<typeof useQueryClient>,
  organizationId: number,
  mailboxId: number
) {
  return Promise.all([
    queryClient.invalidateQueries({ queryKey: externalMailKeys.connections(organizationId) }),
    queryClient.invalidateQueries({ queryKey: mailboxKeys.list(organizationId) }),
    queryClient.invalidateQueries({ queryKey: mailboxKeys.mailbox(organizationId, mailboxId) }),
    queryClient.invalidateQueries({ queryKey: mailboxKeys.inboxes(organizationId) }),
    queryClient.invalidateQueries({ queryKey: mailboxKeys.allInboxCounts(organizationId) }),
  ]);
}

/**
 * Deletes a revoked connection's copies at once. The server deletes the mailbox, its mail, and its
 * agent grants, so everything cached about that mailbox is read again.
 */
export function usePurgeExternalMail(organizationId: number) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async ({ connectionId }: { connectionId: number; mailboxId: number }) =>
      (await service()).purge(organizationId, connectionId),
    meta: LOCAL_ERROR_HANDLING_META,
    onSettled: (_result, _error, { mailboxId }) =>
      invalidateConnectedMailbox(queryClient, organizationId, mailboxId),
  });
}

/**
 * An owner or admin disconnects an account from the connection directory. It may be their own, and
 * the server revokes the mailbox's agent grants, so everything cached for the organization's mail
 * (the directory, their own connections and mailbox list, any mailbox's grants) is read again.
 */
export function useForceDisconnectExternalMail(organizationId: number) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (connectionId: number) =>
      (await service()).forceDisconnect(organizationId, connectionId),
    meta: LOCAL_ERROR_HANDLING_META,
    onSettled: () =>
      queryClient.invalidateQueries({ queryKey: mailboxKeys.organization(organizationId) }),
  });
}

/** Unbinds one of the caller's accounts; the server also revokes that mailbox's agent grants. */
export function useDisconnectExternalMail(organizationId: number) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async ({ connectionId }: { connectionId: number; mailboxId: number }) =>
      (await service()).disconnect(organizationId, connectionId),
    meta: LOCAL_ERROR_HANDLING_META,
    onSettled: (_result, _error, { mailboxId }) =>
      invalidateConnectedMailbox(queryClient, organizationId, mailboxId),
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
