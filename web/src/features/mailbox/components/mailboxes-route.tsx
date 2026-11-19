'use client';

import { useSearchParams } from 'next/navigation';

import { InboxWorkspace } from '@/features/mailbox/components/inbox-workspace';
import { MAILBOX_PARAM, MailboxWorkspace } from '@/features/mailbox/components/mailbox-workspace';

/**
 * The mail page: the unified inbox, or one mailbox's own workspace (folders, labels, the trash,
 * bulk actions, access) when the URL names it with `?mailbox=`, which wins over every other
 * parameter. A remembered mailbox never redirects a bare page, or the inbox could not be reached.
 */
export function MailboxesRoute() {
  const searchParams = useSearchParams();
  return searchParams.has(MAILBOX_PARAM) ? <MailboxWorkspace /> : <InboxWorkspace />;
}
