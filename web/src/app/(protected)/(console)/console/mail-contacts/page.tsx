import { notFound } from 'next/navigation';

import { isWebFeatureEnabled } from '@/config/features';
import { MailContacts } from '@/features/mailbox/components/mail-contacts';
import { MemberFeatureGate } from '@/features/mailbox/components/member-feature-gate';

export default function MailContactsPage() {
  if (!isWebFeatureEnabled('mailbox')) notFound();

  return (
    <div className="mx-auto w-full max-w-6xl px-4 py-6 sm:px-6 lg:py-8">
      <MemberFeatureGate feature="contacts">
        <MailContacts />
      </MemberFeatureGate>
    </div>
  );
}
