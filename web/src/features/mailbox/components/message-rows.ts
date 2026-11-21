import type { MailMessageSummary } from '@/features/mailbox/types';

// Row helpers shared by the unified inbox and the single-mailbox list. They live apart from
// message-list.tsx so the inbox's first load does not pull that component and its dependencies in.

/** The message ids a row stands for: a thread row acts on the thread's messages in the view. */
export function rowMessageIds(row: MailMessageSummary): number[] {
  return row.thread?.message_ids ?? [row.id];
}

export function rowUnread(row: MailMessageSummary): boolean {
  return row.thread ? row.thread.unread : row.direction === 'inbound' && !row.read;
}
