import { notFound } from 'next/navigation';

import { isWebFeatureEnabled } from '@/config/features';
import { MailDomainStatus } from '@/features/mailbox/components/mail-domain-status';

export default function MailDomainPage() {
  if (!isWebFeatureEnabled('mailbox')) notFound();

  return (
    <div className="mx-auto w-full max-w-4xl px-4 py-6 sm:px-6 lg:py-8">
      <MailDomainStatus />
    </div>
  );
}
