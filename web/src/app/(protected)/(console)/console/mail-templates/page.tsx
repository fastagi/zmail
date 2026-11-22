import { notFound } from 'next/navigation';

import { isWebFeatureEnabled } from '@/config/features';
import { MailTemplates } from '@/features/mailbox/components/mail-templates';
import { MemberFeatureGate } from '@/features/mailbox/components/member-feature-gate';

export default function MailTemplatesPage() {
  if (!isWebFeatureEnabled('mailbox')) notFound();

  return (
    <div className="mx-auto w-full max-w-6xl px-4 py-6 sm:px-6 lg:py-8">
      <MemberFeatureGate feature="templates">
        <MailTemplates />
      </MemberFeatureGate>
    </div>
  );
}
