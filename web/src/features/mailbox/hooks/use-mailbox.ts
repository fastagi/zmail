'use client';

import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';

import { LOCAL_ERROR_HANDLING_META } from '@/config/query-meta';
import { mailboxService } from '@/features/mailbox/services/mailbox-service';
import type {
  CreateMailboxInput,
  MailDirection,
  SendMailMessageInput,
} from '@/features/mailbox/types';

export const MAIL_PAGE_SIZE = 20;

export const mailboxKeys = {
  all: ['mailbox'] as const,
  domains: () => [...mailboxKeys.all, 'domains'] as const,
  list: () => [...mailboxKeys.all, 'list'] as const,
  mailbox: (mailboxId: number) => [...mailboxKeys.all, mailboxId] as const,
  messages: (mailboxId: number) => [...mailboxKeys.mailbox(mailboxId), 'messages'] as const,
  page: (mailboxId: number, direction: MailDirection, page: number) =>
    [...mailboxKeys.messages(mailboxId), direction, page] as const,
  message: (mailboxId: number, messageId: number) =>
    [...mailboxKeys.mailbox(mailboxId), 'message', messageId] as const,
};

// The workspace renders every error inline, so the global error toast stays quiet.

export function useMailboxDomains() {
  return useQuery({
    queryKey: mailboxKeys.domains(),
    queryFn: () => mailboxService.domains(),
    staleTime: 5 * 60_000,
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

export function useMailboxes() {
  return useQuery({
    queryKey: mailboxKeys.list(),
    queryFn: () => mailboxService.list(),
    staleTime: 15_000,
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

export function useCreateMailbox() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: CreateMailboxInput) => mailboxService.create(input),
    meta: LOCAL_ERROR_HANDLING_META,
    onSettled: () => queryClient.invalidateQueries({ queryKey: mailboxKeys.list() }),
  });
}

export function useDeleteMailbox() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (mailboxId: number) => mailboxService.remove(mailboxId),
    meta: LOCAL_ERROR_HANDLING_META,
    onSuccess: (_result, mailboxId) => {
      queryClient.removeQueries({ queryKey: mailboxKeys.mailbox(mailboxId) });
    },
    onSettled: () => queryClient.invalidateQueries({ queryKey: mailboxKeys.list() }),
  });
}

export function useMailMessages(mailboxId: number, direction: MailDirection, page: number) {
  return useQuery({
    queryKey: mailboxKeys.page(mailboxId, direction, page),
    queryFn: () => mailboxService.messages(mailboxId, { direction, page, perPage: MAIL_PAGE_SIZE }),
    placeholderData: keepPreviousData,
    staleTime: 15_000,
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

export function useMailMessage(mailboxId: number, messageId: number | null) {
  return useQuery({
    queryKey: mailboxKeys.message(mailboxId, messageId ?? 0),
    queryFn: () => mailboxService.message(mailboxId, messageId ?? 0),
    enabled: messageId !== null,
    staleTime: 15_000,
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

/**
 * The sent folder is refreshed after every attempt: a send the provider rejected is still stored,
 * with status `failed`.
 */
export function useSendMailMessage(mailboxId: number) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: SendMailMessageInput) => mailboxService.send(mailboxId, input),
    meta: LOCAL_ERROR_HANDLING_META,
    onSettled: () => queryClient.invalidateQueries({ queryKey: mailboxKeys.messages(mailboxId) }),
  });
}

export function useUpdateMailMessage(mailboxId: number) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ messageId, read }: { messageId: number; read: boolean }) =>
      mailboxService.setRead(mailboxId, messageId, read),
    meta: LOCAL_ERROR_HANDLING_META,
    onSuccess: message => {
      queryClient.setQueryData(mailboxKeys.message(mailboxId, message.id), message);
    },
    onSettled: () => queryClient.invalidateQueries({ queryKey: mailboxKeys.messages(mailboxId) }),
  });
}

export function useDeleteMailMessage(mailboxId: number) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (messageId: number) => mailboxService.removeMessage(mailboxId, messageId),
    meta: LOCAL_ERROR_HANDLING_META,
    onSuccess: (_result, messageId) => {
      queryClient.removeQueries({ queryKey: mailboxKeys.message(mailboxId, messageId) });
    },
    onSettled: () => queryClient.invalidateQueries({ queryKey: mailboxKeys.messages(mailboxId) }),
  });
}
