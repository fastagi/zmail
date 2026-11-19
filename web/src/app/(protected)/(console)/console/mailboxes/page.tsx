import { notFound } from 'next/navigation';

import { isWebFeatureEnabled } from '@/config/features';
import { MailboxesRoute } from '@/features/mailbox/components/mailboxes-route';

export default function MailboxesPage() {
  if (!isWebFeatureEnabled('mailbox')) notFound();

  return (
    <div className="mx-auto w-full max-w-[100rem] px-4 py-6 sm:px-6 lg:py-8">
      <MailboxesRoute />
    </div>
  );
}
