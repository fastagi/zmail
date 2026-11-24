'use client';

import type { InfiniteData, QueryClient } from '@tanstack/react-query';

import { mailboxKeys } from '@/features/mailbox/hooks/mailbox-keys';
import type {
  InboxItem,
  InboxPage,
  MailMessage,
  MailMessagePage,
  MailMessageSummary,
} from '@/features/mailbox/types';

/** A change of only the caller's own flags, which the caches can show before the server answers. */
export interface MailFlagChange {
  read?: boolean;
  starred?: boolean;
}

const flagFields = new Set(['read', 'starred', 'message_ids']);

/** The flags `input` changes when it changes nothing else (no move, folder, or label); else null. */
export function flagChange(input: Record<string, unknown>): MailFlagChange | null {
  const { read, starred } = input;
  const others = Object.entries(input).filter(
    ([name, value]) => value !== undefined && !flagFields.has(name)
  );
  if (others.length > 0) return null;
  if (read === undefined && starred === undefined) return null;
  return {
    ...(typeof read === 'boolean' ? { read } : {}),
    ...(typeof starred === 'boolean' ? { starred } : {}),
  };
}

/** One message with the flags changed; `read` only means something for inbound mail. */
function flagMessage<T extends MailMessageSummary>(message: T, change: MailFlagChange): T {
  return {
    ...message,
    ...(change.starred === undefined ? {} : { starred: change.starred }),
    ...(change.read === undefined || message.direction !== 'inbound' ? {} : { read: change.read }),
  };
}

/**
 * A listed row with the flags of `ids` changed. A thread row is starred or unread as soon as one
 * of its messages is, and stops being so only when the change covers all of them.
 */
function flagRow<T extends MailMessageSummary | InboxItem>(
  row: T,
  ids: ReadonlySet<number>,
  change: MailFlagChange
): T {
  const threadIds = row.thread?.message_ids ?? [row.id];
  if (!ids.has(row.id) && !threadIds.some(id => ids.has(id))) return row;
  const next = ids.has(row.id) ? flagMessage(row, change) : { ...row };
  if (!row.thread) return next;
  const all = threadIds.every(id => ids.has(id));
  let { starred, unread } = row.thread;
  if (change.starred === true || (change.starred === false && all)) starred = change.starred;
  if (change.read === false) unread = true;
  else if (change.read === true && all) unread = false;
  return { ...next, thread: { ...row.thread, starred, unread } };
}

/**
 * Shows a flag change of `messageIds` in every cached view of the mailbox (the open message, its
 * thread, the mailbox's lists, the unified inbox) before the server answers. Answers the function
 * that puts those caches back, for a failed request.
 */
export function showFlagChange(
  queryClient: QueryClient,
  organizationId: number,
  mailboxId: number,
  messageIds: readonly number[],
  change: MailFlagChange
): () => void {
  const ids = new Set(messageIds);
  const keys = [
    mailboxKeys.messageDetails(organizationId, mailboxId),
    mailboxKeys.threads(organizationId, mailboxId),
    mailboxKeys.messages(organizationId, mailboxId),
    mailboxKeys.inboxes(organizationId),
  ];
  const snapshots = keys.flatMap(queryKey => queryClient.getQueriesData<unknown>({ queryKey }));
  queryClient.setQueriesData<MailMessage>({ queryKey: keys[0] }, message =>
    message && ids.has(message.id) ? flagMessage(message, change) : message
  );
  queryClient.setQueriesData<MailMessage[]>({ queryKey: keys[1] }, thread =>
    Array.isArray(thread)
      ? thread.map(message => (ids.has(message.id) ? flagMessage(message, change) : message))
      : thread
  );
  queryClient.setQueriesData<MailMessagePage>({ queryKey: keys[2] }, page =>
    page && Array.isArray(page.items)
      ? { ...page, items: page.items.map(row => flagRow(row, ids, change)) }
      : page
  );
  queryClient.setQueriesData<InfiniteData<InboxPage, unknown>>({ queryKey: keys[3] }, listing =>
    listing?.pages
      ? {
          ...listing,
          pages: listing.pages.map(page => ({
            ...page,
            items: page.items.map(row =>
              row.mailbox_id === mailboxId ? flagRow(row, ids, change) : row
            ),
          })),
        }
      : listing
  );
  return () => {
    for (const [key, data] of snapshots) queryClient.setQueryData(key, data);
  };
}
