import { notFound } from 'next/navigation';

import { isWebFeatureEnabled } from '@/config/features';
import { EmailLog } from '@/features/mailbox/components/email-log';
import { parseEmailLogView } from '@/features/mailbox/utils/email-log-view';

interface EmailsPageProps {
  searchParams: Promise<Record<string, string | string[] | undefined>>;
}

export default async function EmailsPage({ searchParams }: EmailsPageProps) {
  if (!isWebFeatureEnabled('mailbox')) notFound();
  // The filters live in the query string, so a reload opens the same list.
  const initialView = parseEmailLogView(await searchParams);

  return (
    <div className="mx-auto w-full max-w-6xl px-4 py-6 sm:px-6 lg:py-8">
      <EmailLog initialView={initialView} />
    </div>
  );
}
