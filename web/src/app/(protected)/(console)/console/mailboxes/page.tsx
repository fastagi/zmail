import { notFound } from 'next/navigation';

import { isWebFeatureEnabled } from '@/config/features';
import { MailboxWorkspace } from '@/features/mailbox/components/mailbox-workspace';
import { getT } from '@/i18n/server';

export default async function MailboxesPage() {
  if (!isWebFeatureEnabled('mailbox')) notFound();
  const t = await getT('mailbox');

  return (
    <div className="mx-auto w-full max-w-6xl px-4 py-6 sm:px-6 lg:py-8">
      <div className="border-b pb-5">
        <h1 className="text-2xl font-semibold">{t('title')}</h1>
        <p className="mt-1 text-sm text-muted-foreground">{t('description')}</p>
      </div>
      <div className="pt-5">
        <MailboxWorkspace />
      </div>
    </div>
  );
}
