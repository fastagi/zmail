'use client';

import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';

import { LOCAL_ERROR_HANDLING_META } from '@/config/query-meta';
import { mailboxKeys, validId } from '@/features/mailbox/hooks/use-mailbox';
import { mailboxAdminService } from '@/features/mailbox/services/mailbox-admin-service';
import type {
  MailboxDirectoryFilters,
  MailboxImportRowInput,
  MailboxType,
} from '@/features/mailbox/types';

/** One directory page; the previous page stays on screen while the next one loads. */
export function useMailboxDirectory(
  organizationId: number,
  filters: MailboxDirectoryFilters,
  cursor?: string
) {
  return useQuery({
    queryKey: mailboxKeys.directory(organizationId, filters, cursor),
    queryFn: () => mailboxAdminService.directory(organizationId, filters, cursor),
    enabled: validId(organizationId),
    placeholderData: keepPreviousData,
    staleTime: 15_000,
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

export function useRenameMailbox(organizationId: number) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ mailboxId, displayName }: { mailboxId: number; displayName: string }) =>
      mailboxAdminService.rename(organizationId, mailboxId, displayName),
    meta: LOCAL_ERROR_HANDLING_META,
    onSettled: (_result, _error, { mailboxId }) =>
      Promise.all([
        queryClient.invalidateQueries({ queryKey: mailboxKeys.list(organizationId) }),
        queryClient.invalidateQueries({ queryKey: mailboxKeys.directories(organizationId) }),
        queryClient.invalidateQueries({
          queryKey: mailboxKeys.accessEvents(organizationId, mailboxId),
        }),
      ]),
  });
}

/** A dry run changes nothing; a real run refreshes everything of the organization's mailboxes. */
export function useImportMailboxes(organizationId: number) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ rows, dryRun }: { rows: MailboxImportRowInput[]; dryRun: boolean }) =>
      mailboxAdminService.importMailboxes(organizationId, rows, dryRun),
    meta: LOCAL_ERROR_HANDLING_META,
    onSettled: (_result, _error, { dryRun }) =>
      dryRun
        ? undefined
        : queryClient.invalidateQueries({ queryKey: mailboxKeys.organization(organizationId) }),
  });
}

/** Downloads the CSV file of one mailbox type and hands it to the browser to save. */
export function useExportMailboxes(organizationId: number) {
  return useMutation({
    mutationFn: async (type: MailboxType) => {
      const file = await mailboxAdminService.exportMailboxes(organizationId, type);
      saveFile(file.blob, file.filename);
      return file.filename;
    },
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

/** Saves a file through a temporary link; the page never shows its content. */
export function saveFile(blob: Blob, filename: string): void {
  const objectUrl = URL.createObjectURL(blob);
  try {
    const anchor = document.createElement('a');
    anchor.href = objectUrl;
    anchor.download = filename;
    anchor.rel = 'noopener noreferrer';
    anchor.click();
  } finally {
    // Revoke after the click has been handled, so the download can start.
    setTimeout(() => URL.revokeObjectURL(objectUrl), 0);
  }
}
