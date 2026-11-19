import { notFound } from 'next/navigation';

import { isWebFeatureEnabled } from '@/config/features';
import { MailboxesRoute } from '@/features/mailbox/components/mailboxes-route';

export default function MailboxesPage() {
  if (!isWebFeatureEnabled('mailbox')) notFound();

  // The unified inbox fills the console's content area; each view sets its own frame.
  return (
    <div className="h-full w-full">
      <MailboxesRoute />
    </div>
  );
}
