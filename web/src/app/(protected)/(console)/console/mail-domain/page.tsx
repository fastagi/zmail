import { notFound } from 'next/navigation';

import { isWebFeatureEnabled } from '@/config/features';
import { MailDomainStatus } from '@/features/mailbox/components/mail-domain-status';
import { getT } from '@/i18n/server';

export default async function MailDomainPage() {
  if (!isWebFeatureEnabled('mailbox')) notFound();
  const t = await getT('mailbox');

  return (
    <div className="mx-auto w-full max-w-4xl px-4 py-6 sm:px-6 lg:py-8">
      <div className="border-b pb-5">
        <h1 className="text-2xl font-semibold">{t('domain.title')}</h1>
        <p className="mt-1 text-sm text-muted-foreground">{t('domain.description')}</p>
      </div>
      <div className="pt-5">
        <MailDomainStatus />
      </div>
    </div>
  );
}
