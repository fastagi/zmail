'use client';

import { lazy, Suspense } from 'react';
import { useSearchParams } from 'next/navigation';

import { InboxWorkspace } from '@/features/mailbox/components/inbox-workspace';
import { MAILBOX_PARAM } from '@/features/mailbox/components/mailbox-params';

// One mailbox's own workspace loads only when the URL names a mailbox.
const MailboxWorkspace = lazy(async () => {
  const feature = await import('@/features/mailbox/components/mailbox-workspace');
  return { default: feature.MailboxWorkspace };
});

/**
 * The mail page: the unified inbox, or one mailbox's own workspace (folders, labels, the trash,
 * bulk actions, access) when the URL names it with `?mailbox=`, which wins over every other
 * parameter. A remembered mailbox never redirects a bare page, or the inbox could not be reached.
 */
export function MailboxesRoute() {
  const searchParams = useSearchParams();
  return searchParams.has(MAILBOX_PARAM) ? (
    <Suspense fallback={null}>
      <MailboxWorkspace />
    </Suspense>
  ) : (
    <InboxWorkspace />
  );
}
