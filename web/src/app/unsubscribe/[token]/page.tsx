import { notFound } from 'next/navigation';

import { isWebFeatureEnabled } from '@/config/features';
import { UnsubscribePanel } from '@/features/mailbox/components/unsubscribe-panel';

export const metadata = { robots: { index: false, follow: false } };

interface PageProps {
  params: Promise<{ token: string }>;
}

/**
 * The page an unsubscribe link opens. It shows nothing about the address or the sender, and
 * unsubscribes only when the recipient confirms, so link scanners that open pages do not.
 */
export default async function UnsubscribePage({ params }: PageProps) {
  if (!isWebFeatureEnabled('mailbox')) notFound();
  const { token } = await params;
  return (
    <main className="flex min-h-screen items-center justify-center bg-bg-canvas px-4 py-12">
      <UnsubscribePanel token={token} />
    </main>
  );
}
