import { Suspense } from 'react';
import { notFound } from 'next/navigation';

import { isWebFeatureEnabled } from '@/config/features';
import { ExternalMailCallback } from '@/features/mailbox/components/external-mail-callback';
import { getT } from '@/i18n/server';

/**
 * Where a mail provider sends the browser back after a member signs in to bind an outside account
 * (`EXTERNAL_MAIL_REDIRECT_URL` points here). It finishes the sign-in and opens the inbox.
 */
export default async function ExternalMailCallbackPage() {
  if (!isWebFeatureEnabled('mailbox')) notFound();
  const t = await getT('mailbox');

  return (
    <div className="mx-auto flex w-full max-w-md flex-1 flex-col gap-6 px-4 py-16">
      <h1 className="text-center text-xl font-semibold">{t('external.callback.title')}</h1>
      <Suspense fallback={null}>
        <ExternalMailCallback />
      </Suspense>
    </div>
  );
}
