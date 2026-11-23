'use client';

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';

import { LOCAL_ERROR_HANDLING_META } from '@/config/query-meta';
import type { CreateMailAppPasswordInput } from '@/features/mailbox/mail-client-schemas';
import { mailClientService } from '@/features/mailbox/services/mail-client-service';

export const mailClientKeys = {
  all: ['mail-clients'] as const,
  passwords: (organizationId: number) =>
    [...mailClientKeys.all, organizationId, 'passwords'] as const,
  settings: (organizationId: number) => [...mailClientKeys.all, organizationId, 'settings'] as const,
};

function validId(value: number | undefined): value is number {
  return value !== undefined && Number.isSafeInteger(value) && value > 0;
}

/** The caller's own mail client passwords in an organization, without secrets. */
export function useMailAppPasswords(organizationId: number | undefined) {
  return useQuery({
    queryKey: mailClientKeys.passwords(organizationId ?? 0),
    queryFn: () => mailClientService.list(organizationId ?? 0),
    enabled: validId(organizationId),
    staleTime: 15_000,
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

/**
 * Creates a password. The secret lives only in the dialog's state: the mutation is never cached
 * (`gcTime: 0`), and the listing that is refetched afterwards carries no secret.
 */
export function useCreateMailAppPassword(organizationId: number | undefined) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: CreateMailAppPasswordInput) =>
      mailClientService.create(organizationId ?? 0, input),
    gcTime: 0,
    meta: LOCAL_ERROR_HANDLING_META,
    onSuccess: () => {
      if (validId(organizationId)) {
        void queryClient.invalidateQueries({ queryKey: mailClientKeys.passwords(organizationId) });
      }
    },
  });
}

export function useRevokeMailAppPassword(organizationId: number | undefined) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (passwordId: number) => mailClientService.revoke(organizationId ?? 0, passwordId),
    meta: LOCAL_ERROR_HANDLING_META,
    onSuccess: () => {
      if (validId(organizationId)) {
        void queryClient.invalidateQueries({ queryKey: mailClientKeys.passwords(organizationId) });
      }
    },
  });
}

/** The organization switch; read only for owners and admins, who are the ones to change it. */
export function useMailClientSettings(organizationId: number | undefined, enabled: boolean) {
  return useQuery({
    queryKey: mailClientKeys.settings(organizationId ?? 0),
    queryFn: () => mailClientService.settings(organizationId ?? 0),
    enabled: enabled && validId(organizationId),
    staleTime: 15_000,
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

export function useUpdateMailClientSettings(organizationId: number | undefined) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (accessEnabled: boolean) =>
      mailClientService.updateSettings(organizationId ?? 0, accessEnabled),
    meta: LOCAL_ERROR_HANDLING_META,
    onSuccess: settings => {
      if (!validId(organizationId)) return;
      queryClient.setQueryData(mailClientKeys.settings(organizationId), settings);
      // The listing's access depends on the switch.
      void queryClient.invalidateQueries({ queryKey: mailClientKeys.passwords(organizationId) });
    },
  });
}
