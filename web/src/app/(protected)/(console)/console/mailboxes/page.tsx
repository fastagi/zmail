import { notFound } from 'next/navigation';

import { isWebFeatureEnabled } from '@/config/features';
import { MailboxWorkspace } from '@/features/mailbox/components/mailbox-workspace';

export default function MailboxesPage() {
  if (!isWebFeatureEnabled('mailbox')) notFound();

  return (
    <div className="mx-auto w-full max-w-6xl px-4 py-6 sm:px-6 lg:py-8">
      <MailboxWorkspace />
    </div>
  );
}
