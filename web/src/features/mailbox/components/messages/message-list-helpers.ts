'use client';

import { useState } from 'react';
import { toast } from 'sonner';

import { useMailOrganizationId } from '@/features/mailbox/components/mail-organization';
import type { MailView } from '@/features/mailbox/components/mailbox-sidebar';
import { originOf, type MailOrigin } from '@/features/mailbox/components/message-actions';
import { rowMessageIds } from '@/features/mailbox/components/message-rows';
import { useUpdateMailMessages } from '@/features/mailbox/hooks/use-mailbox';
import { MAIL_MESSAGE_UPDATES_MAX_IDS } from '@/features/mailbox/schemas';
import type { MailMessageSummary, UpdateMailMessagesInput } from '@/features/mailbox/types';
import { resolveMailboxErrorKey } from '@/features/mailbox/utils/mailbox-error';
import { useT } from '@/i18n';

export function rowStarred(row: MailMessageSummary): boolean {
  return row.thread?.starred ?? row.starred;
}

export function rowLabelIds(row: MailMessageSummary): number[] {
  return row.thread?.label_ids ?? row.label_ids;
}

function chunks<T>(items: T[], size: number): T[][] {
  const result: T[][] = [];
  for (let index = 0; index < items.length; index += size) {
    result.push(items.slice(index, index + size));
  }
  return result;
}

/** One change for any number of messages, sent in requests of at most 100 ids. */
export async function updateInChunks(
  mutateAsync: (input: UpdateMailMessagesInput) => Promise<unknown>,
  ids: number[],
  change: Omit<UpdateMailMessagesInput, 'message_ids'>
) {
  for (const part of chunks(ids, MAIL_MESSAGE_UPDATES_MAX_IDS)) {
    await mutateAsync({ message_ids: part, ...change });
  }
}

/** Puts moved rows back where they were. */
export function useUndoMove(mailboxId: number) {
  const t = useT('mailbox');
  const updateMany = useUpdateMailMessages(useMailOrganizationId(), mailboxId);
  return async (groups: Map<string, { origin: MailOrigin; ids: number[] }>) => {
    try {
      for (const { origin, ids } of groups.values()) {
        await updateInChunks(updateMany.mutateAsync, ids, origin);
      }
    } catch (failure) {
      toast.error(t(resolveMailboxErrorKey(failure)));
    }
  };
}

/** Where the rows of a view were before a move, grouped so one undo request covers each place. */
export function origins(
  view: MailView,
  rows: MailMessageSummary[]
): Map<string, { origin: MailOrigin; ids: number[] }> {
  const groups = new Map<string, { origin: MailOrigin; ids: number[] }>();
  for (const row of rows) {
    // A thread row's messages are all in the view's placement; a single message carries its own.
    const origin: MailOrigin = row.thread
      ? view.kind === 'folder'
        ? { placement: 'folder', folder_id: view.id }
        : { placement: view.kind === 'archive' ? 'archive' : 'default' }
      : originOf(row);
    const key = `${origin.placement}:${origin.folder_id ?? ''}`;
    const group = groups.get(key) ?? { origin, ids: [] };
    group.ids.push(...rowMessageIds(row));
    groups.set(key, group);
  }
  return groups;
}

/**
 * Rows that arrived with a background refetch: newer than anything shown before in the same
 * listing. They are counted for the "new messages" pill instead of being announced by a jump.
 */
export function useFreshRows(listKey: string, items: MailMessageSummary[] | undefined) {
  const [tracked, setTracked] = useState<{
    key: string;
    items: MailMessageSummary[] | undefined;
    newest: number;
    fresh: number[];
  }>({ key: listKey, items, newest: Math.max(0, ...(items ?? []).map(row => row.id)), fresh: [] });

  if (tracked.key !== listKey || (tracked.items === undefined && items !== undefined)) {
    setTracked({
      key: listKey,
      items,
      newest: Math.max(0, ...(items ?? []).map(row => row.id)),
      fresh: [],
    });
  } else if (items !== undefined && items !== tracked.items) {
    const added = items.filter(row => row.id > tracked.newest).map(row => row.id);
    setTracked({
      key: listKey,
      items,
      newest: Math.max(tracked.newest, ...items.map(row => row.id)),
      fresh: [...tracked.fresh, ...added],
    });
  }
  return {
    count: tracked.fresh.length,
    clear: () => setTracked(current => ({ ...current, fresh: [] })),
  };
}
