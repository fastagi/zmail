'use client';

import {
  keepPreviousData,
  useMutation,
  useQuery,
  useQueryClient,
  type QueryClient,
} from '@tanstack/react-query';

import { LOCAL_ERROR_HANDLING_META } from '@/config/query-meta';
import { liveRefresh, mailboxKeys, validId } from '@/features/mailbox/hooks/mailbox-keys';
import { flagChange, showFlagChange } from '@/features/mailbox/hooks/mailbox-optimistic-flags';
import { mailboxService } from '@/features/mailbox/services/mailbox-service';
import type {
  CreateMailboxInput,
  MailFolderInput,
  MailLabelInput,
  MailLogFilters,
  MailMessageFilters,
  MailboxListScope,
  SendMailMessageInput,
  UpdateMailMessageInput,
  UpdateMailMessagesInput,
} from '@/features/mailbox/types';

export const MAIL_PAGE_SIZE = 20;
export const MAIL_STATS_DAYS = 7;
export {
  MAIL_REFRESH_MS,
  liveRefresh,
  mailboxKeys,
  validId,
} from '@/features/mailbox/hooks/mailbox-keys';
export {
  useConnectMailDomain,
  useCreateDnsRecord,
  useDeleteDnsRecord,
  useDeleteMailZone,
  useEnableReceiving,
  useListDnsRecords,
  useListMailZones,
  useMailDomains,
  useMailZone,
  useMailboxDomains,
  usePreviewMailDomain,
  useRefreshMailDomain,
  useRemoveMailDomain,
  useSetDefaultMailDomain,
  useUpdateDnsRecord,
} from '@/features/mailbox/hooks/use-mail-domains';
export {
  MAILBOX_ACCESS_EVENTS_PAGE_SIZE,
  useDeleteMailboxSealedHistory,
  useHandOverMailbox,
  useMailboxAccessEvents,
  useMailboxMembers,
  useRemoveMailboxMember,
  useSetMailboxMember,
  useSetMailboxWebhooks,
} from '@/features/mailbox/hooks/use-mailbox-access';


/** The log and the per-day counts span every mailbox, so any message change touches them. */
function invalidateLog(queryClient: QueryClient, organizationId: number, withStats = true) {
  return Promise.all([
    queryClient.invalidateQueries({ queryKey: mailboxKeys.log(organizationId) }),
    ...(withStats
      ? [queryClient.invalidateQueries({ queryKey: mailboxKeys.stats(organizationId) })]
      : []),
  ]);
}

/**
 * Every change to a mailbox's mail: its listings, threads, and details, the caller's counts, the
 * unread count in the mailbox list, the unified inbox and its counts, and the log. Statistics only
 * move when mail is deleted.
 */
export function invalidateMail(
  queryClient: QueryClient,
  organizationId: number,
  mailboxId: number,
  withStats = false
) {
  return Promise.all([
    queryClient.invalidateQueries({ queryKey: mailboxKeys.messages(organizationId, mailboxId) }),
    queryClient.invalidateQueries({
      queryKey: mailboxKeys.messageDetails(organizationId, mailboxId),
    }),
    queryClient.invalidateQueries({ queryKey: mailboxKeys.threads(organizationId, mailboxId) }),
    queryClient.invalidateQueries({ queryKey: mailboxKeys.counts(organizationId, mailboxId) }),
    queryClient.invalidateQueries({ queryKey: mailboxKeys.list(organizationId) }),
    queryClient.invalidateQueries({ queryKey: mailboxKeys.inboxes(organizationId) }),
    queryClient.invalidateQueries({ queryKey: mailboxKeys.allInboxCounts(organizationId) }),
    invalidateLog(queryClient, organizationId, withStats),
  ]);
}


// The workspace renders every error inline, so the global error toast stays quiet.

/** `live` keeps the list (and its unread counts) fresh while a mailbox is open. */
export function useMailboxes(
  organizationId: number,
  {
    live = false,
    scope,
    enabled = true,
  }: { live?: boolean; scope?: MailboxListScope; enabled?: boolean } = {}
) {
  return useQuery({
    queryKey: mailboxKeys.list(organizationId, scope),
    queryFn: () => mailboxService.list(organizationId, scope),
    enabled: enabled && validId(organizationId),
    staleTime: 15_000,
    ...(live ? liveRefresh : {}),
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

export function useCreateMailbox(organizationId: number) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: CreateMailboxInput) => mailboxService.create(organizationId, input),
    meta: LOCAL_ERROR_HANDLING_META,
    onSettled: () =>
      Promise.all([
        queryClient.invalidateQueries({ queryKey: mailboxKeys.list(organizationId) }),
        queryClient.invalidateQueries({ queryKey: mailboxKeys.directories(organizationId) }),
      ]),
  });
}

export function useDeleteMailbox(organizationId: number) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (mailboxId: number) => mailboxService.remove(organizationId, mailboxId),
    meta: LOCAL_ERROR_HANDLING_META,
    onSuccess: (_result, mailboxId) => {
      queryClient.removeQueries({ queryKey: mailboxKeys.mailbox(organizationId, mailboxId) });
    },
    // Messages of a deleted mailbox leave the log and the counts.
    onSettled: () =>
      Promise.all([
        queryClient.invalidateQueries({ queryKey: mailboxKeys.list(organizationId) }),
        queryClient.invalidateQueries({ queryKey: mailboxKeys.directories(organizationId) }),
        invalidateLog(queryClient, organizationId),
      ]),
  });
}

export function useMailLog(organizationId: number, filters: MailLogFilters, page: number) {
  return useQuery({
    queryKey: mailboxKeys.logPage(organizationId, filters, page),
    queryFn: () =>
      mailboxService.listLog(organizationId, { ...filters, page, perPage: MAIL_PAGE_SIZE }),
    enabled: validId(organizationId),
    placeholderData: keepPreviousData,
    staleTime: 15_000,
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

export function useMailStats(organizationId: number, days: number = MAIL_STATS_DAYS) {
  return useQuery({
    queryKey: mailboxKeys.statsWindow(organizationId, days),
    queryFn: () => mailboxService.stats(organizationId, days),
    enabled: validId(organizationId),
    staleTime: 60_000,
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

export function useMailMessage(
  organizationId: number,
  mailboxId: number,
  messageId: number | null
) {
  return useQuery({
    queryKey: mailboxKeys.message(organizationId, mailboxId, messageId ?? 0),
    queryFn: () => mailboxService.message(organizationId, mailboxId, messageId ?? 0),
    enabled: validId(organizationId) && messageId !== null,
    staleTime: 15_000,
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

/**
 * The sent folder is refreshed after every attempt: a send the provider rejected is still stored,
 * with status `failed`.
 */
export function useSendMailMessage(organizationId: number, mailboxId: number) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: SendMailMessageInput) =>
      mailboxService.send(organizationId, mailboxId, input),
    meta: LOCAL_ERROR_HANDLING_META,
    // A reply joins the thread it answers.
    onSettled: () => invalidateMail(queryClient, organizationId, mailboxId, true),
  });
}

/**
 * One change of one message. A change of only its star or read state shows everywhere at once and
 * is put back if the request fails; the caller says why.
 */
export function useUpdateMailMessage(organizationId: number, mailboxId: number) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ messageId, ...input }: { messageId: number } & UpdateMailMessageInput) =>
      mailboxService.updateMessage(organizationId, mailboxId, messageId, input),
    meta: LOCAL_ERROR_HANDLING_META,
    onMutate: ({ messageId, ...input }) => {
      const change = flagChange(input);
      return {
        rollback: change
          ? showFlagChange(queryClient, organizationId, mailboxId, [messageId], change)
          : null,
      };
    },
    onError: (_error, _variables, context) => context?.rollback?.(),
    onSuccess: message => {
      queryClient.setQueryData(mailboxKeys.message(organizationId, mailboxId, message.id), message);
    },
    // Read state and placement change the counts and the unread count in the mailbox list.
    onSettled: () => invalidateMail(queryClient, organizationId, mailboxId),
  });
}

/**
 * One change for 1 to 100 messages; a thread row sends its `thread.message_ids`. A change of only
 * star or read state shows at once and is put back if the request fails.
 */
export function useUpdateMailMessages(organizationId: number, mailboxId: number) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: UpdateMailMessagesInput) =>
      mailboxService.updateMessages(organizationId, mailboxId, input),
    meta: LOCAL_ERROR_HANDLING_META,
    onMutate: input => {
      const change = flagChange(input);
      return {
        rollback: change
          ? showFlagChange(queryClient, organizationId, mailboxId, input.message_ids, change)
          : null,
      };
    },
    onError: (_error, _variables, context) => context?.rollback?.(),
    onSettled: () => invalidateMail(queryClient, organizationId, mailboxId),
  });
}

/** Deletes everything in the trash for good; needs `organize`. */
export function usePurgeMailTrash(organizationId: number, mailboxId: number) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: () => mailboxService.purgeTrash(organizationId, mailboxId),
    meta: LOCAL_ERROR_HANDLING_META,
    onSettled: () => invalidateMail(queryClient, organizationId, mailboxId, true),
  });
}

/**
 * Marks a message read from the log, where each row belongs to a different mailbox, so the
 * mailbox is a variable of the mutation rather than of the hook.
 */
export function useMarkMailMessageRead(organizationId: number) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ mailboxId, messageId }: { mailboxId: number; messageId: number }) =>
      mailboxService.setRead(organizationId, mailboxId, messageId, true),
    meta: LOCAL_ERROR_HANDLING_META,
    onSuccess: (message, { mailboxId }) => {
      queryClient.setQueryData(mailboxKeys.message(organizationId, mailboxId, message.id), message);
    },
    onSettled: (_message, _error, { mailboxId }) =>
      invalidateMail(queryClient, organizationId, mailboxId),
  });
}

/**
 * Moves a message to the trash, or deletes one already there for good (`organize`). Either way it
 * leaves the counts and, when unread in the Inbox, the unread count in the mailbox list.
 */
/**
 * Shows the masked codes of one message. The original is returned to the caller only and never
 * written to the query cache, so reopening the message masks it again.
 */
export function useRevealMailMessage(organizationId: number, mailboxId: number) {
  return useMutation({
    mutationFn: (messageId: number) =>
      mailboxService.revealMessage(organizationId, mailboxId, messageId),
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

export function useDeleteMailMessage(organizationId: number, mailboxId: number) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (messageId: number) =>
      mailboxService.removeMessage(organizationId, mailboxId, messageId),
    meta: LOCAL_ERROR_HANDLING_META,
    onSuccess: (_result, messageId) => {
      queryClient.removeQueries({
        queryKey: mailboxKeys.message(organizationId, mailboxId, messageId),
      });
    },
    onSettled: () => invalidateMail(queryClient, organizationId, mailboxId, true),
  });
}

/**
 * One page of a mailbox listing with the organize filters; `group: 'thread'` lists one row per
 * thread of a placement view.
 */
export function useMailMessageList(
  organizationId: number,
  mailboxId: number,
  filters: MailMessageFilters,
  page: number
) {
  return useQuery({
    queryKey: mailboxKeys.filteredPage(organizationId, mailboxId, filters, page),
    queryFn: () =>
      mailboxService.messages(organizationId, mailboxId, {
        ...filters,
        page,
        perPage: MAIL_PAGE_SIZE,
      }),
    enabled: validId(organizationId) && validId(mailboxId),
    placeholderData: keepPreviousData,
    staleTime: 15_000,
    ...liveRefresh,
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

/** A thread's messages outside the trash, oldest first; only asked for once one is open. */
export function useMailThread(organizationId: number, mailboxId: number, threadId: number | null) {
  return useQuery({
    queryKey: mailboxKeys.thread(organizationId, mailboxId, threadId ?? 0),
    queryFn: () => mailboxService.getThread(organizationId, mailboxId, threadId ?? 0),
    enabled: validId(organizationId) && validId(mailboxId) && threadId !== null,
    staleTime: 15_000,
    ...liveRefresh,
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

/** The caller's own unread and starred counts for the mailbox's navigation. */
export function useMailMessageCounts(organizationId: number, mailboxId: number, enabled = true) {
  return useQuery({
    queryKey: mailboxKeys.counts(organizationId, mailboxId),
    queryFn: () => mailboxService.getMessageCounts(organizationId, mailboxId),
    enabled: enabled && validId(organizationId) && validId(mailboxId),
    staleTime: 15_000,
    ...liveRefresh,
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

export function useMailFolders(organizationId: number, mailboxId: number, enabled = true) {
  return useQuery({
    queryKey: mailboxKeys.folders(organizationId, mailboxId),
    queryFn: () => mailboxService.listFolders(organizationId, mailboxId),
    enabled: enabled && validId(organizationId) && validId(mailboxId),
    staleTime: 60_000,
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

export function useMailLabels(organizationId: number, mailboxId: number, enabled = true) {
  return useQuery({
    queryKey: mailboxKeys.labels(organizationId, mailboxId),
    queryFn: () => mailboxService.listLabels(organizationId, mailboxId),
    enabled: enabled && validId(organizationId) && validId(mailboxId),
    staleTime: 60_000,
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

/**
 * Folder and label changes need `organize`. Deleting a folder moves its mail to `default` and
 * deleting a label removes it from every message, so the mail is refreshed with them.
 */
function useOrganizeMutation<Variables, Result>(
  organizationId: number,
  mailboxId: number,
  kind: 'folders' | 'labels',
  mutationFn: (variables: Variables) => Promise<Result>
) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn,
    meta: LOCAL_ERROR_HANDLING_META,
    onSettled: () =>
      Promise.all([
        queryClient.invalidateQueries({ queryKey: mailboxKeys[kind](organizationId, mailboxId) }),
        invalidateMail(queryClient, organizationId, mailboxId),
      ]),
  });
}

export function useCreateMailFolder(organizationId: number, mailboxId: number) {
  return useOrganizeMutation(organizationId, mailboxId, 'folders', (input: MailFolderInput) =>
    mailboxService.createFolder(organizationId, mailboxId, input)
  );
}

export function useRenameMailFolder(organizationId: number, mailboxId: number) {
  return useOrganizeMutation(
    organizationId,
    mailboxId,
    'folders',
    ({ folderId, ...input }: { folderId: number } & MailFolderInput) =>
      mailboxService.renameFolder(organizationId, mailboxId, folderId, input)
  );
}

export function useDeleteMailFolder(organizationId: number, mailboxId: number) {
  return useOrganizeMutation(organizationId, mailboxId, 'folders', (folderId: number) =>
    mailboxService.deleteFolder(organizationId, mailboxId, folderId)
  );
}

export function useCreateMailLabel(organizationId: number, mailboxId: number) {
  return useOrganizeMutation(organizationId, mailboxId, 'labels', (input: MailLabelInput) =>
    mailboxService.createLabel(organizationId, mailboxId, input)
  );
}

export function useUpdateMailLabel(organizationId: number, mailboxId: number) {
  return useOrganizeMutation(
    organizationId,
    mailboxId,
    'labels',
    ({ labelId, ...input }: { labelId: number } & MailLabelInput) =>
      mailboxService.updateLabel(organizationId, mailboxId, labelId, input)
  );
}

export function useDeleteMailLabel(organizationId: number, mailboxId: number) {
  return useOrganizeMutation(organizationId, mailboxId, 'labels', (labelId: number) =>
    mailboxService.deleteLabel(organizationId, mailboxId, labelId)
  );
}
