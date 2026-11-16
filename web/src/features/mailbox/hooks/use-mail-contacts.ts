'use client';

import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';

import { LOCAL_ERROR_HANDLING_META } from '@/config/query-meta';
import { mailboxKeys } from '@/features/mailbox/hooks/use-mailbox';
import { mailboxService } from '@/features/mailbox/services/mailbox-service';
import type {
  MailContactFilters,
  MailContactImportInput,
  MailContactInput,
} from '@/features/mailbox/types';

export const MAIL_CONTACT_PAGE_SIZE = 25;

/** Contact keys live under the organization, like every other mailbox key. */
export const mailContactKeys = {
  all: (organizationId: number) =>
    [...mailboxKeys.organization(organizationId), 'contacts'] as const,
  pages: (organizationId: number) => [...mailContactKeys.all(organizationId), 'pages'] as const,
  page: (organizationId: number, filters: MailContactFilters, page: number) =>
    [
      ...mailContactKeys.pages(organizationId),
      filters.q ?? '',
      filters.status ?? '',
      filters.listId ?? 0,
      page,
    ] as const,
  lists: (organizationId: number) => [...mailContactKeys.all(organizationId), 'lists'] as const,
};

function validId(value: number): boolean {
  return Number.isSafeInteger(value) && value > 0;
}

export function useMailContacts(organizationId: number, filters: MailContactFilters, page: number) {
  return useQuery({
    queryKey: mailContactKeys.page(organizationId, filters, page),
    queryFn: () =>
      mailboxService.contacts(organizationId, {
        ...filters,
        page,
        perPage: MAIL_CONTACT_PAGE_SIZE,
      }),
    enabled: validId(organizationId),
    placeholderData: keepPreviousData,
    staleTime: 15_000,
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

export function useMailContactLists(organizationId: number) {
  return useQuery({
    queryKey: mailContactKeys.lists(organizationId),
    queryFn: () => mailboxService.contactLists(organizationId),
    enabled: validId(organizationId),
    staleTime: 30_000,
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

/** Contacts and list counts change together, so every write refreshes both. */
function useInvalidateContacts(organizationId: number) {
  const queryClient = useQueryClient();
  return () => queryClient.invalidateQueries({ queryKey: mailContactKeys.all(organizationId) });
}

export function useSaveMailContact(organizationId: number) {
  const invalidate = useInvalidateContacts(organizationId);
  return useMutation({
    mutationFn: ({ input, contactId }: { input: MailContactInput; contactId?: number }) =>
      mailboxService.saveContact(organizationId, input, contactId),
    meta: LOCAL_ERROR_HANDLING_META,
    onSettled: invalidate,
  });
}

export function useDeleteMailContact(organizationId: number) {
  const invalidate = useInvalidateContacts(organizationId);
  return useMutation({
    mutationFn: (contactId: number) => mailboxService.deleteContact(organizationId, contactId),
    meta: LOCAL_ERROR_HANDLING_META,
    onSettled: invalidate,
  });
}

export function useImportMailContacts(organizationId: number) {
  const invalidate = useInvalidateContacts(organizationId);
  return useMutation({
    mutationFn: (input: MailContactImportInput) =>
      mailboxService.importContacts(organizationId, input),
    meta: LOCAL_ERROR_HANDLING_META,
    onSettled: invalidate,
  });
}

export function useSaveMailContactList(organizationId: number) {
  const invalidate = useInvalidateContacts(organizationId);
  return useMutation({
    mutationFn: ({ name, listId }: { name: string; listId?: number }) =>
      mailboxService.saveContactList(organizationId, { name }, listId),
    meta: LOCAL_ERROR_HANDLING_META,
    onSettled: invalidate,
  });
}

export function useDeleteMailContactList(organizationId: number) {
  const invalidate = useInvalidateContacts(organizationId);
  return useMutation({
    mutationFn: (listId: number) => mailboxService.deleteContactList(organizationId, listId),
    meta: LOCAL_ERROR_HANDLING_META,
    onSettled: invalidate,
  });
}
