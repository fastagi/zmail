'use client';

import dynamic from 'next/dynamic';

import { Skeleton } from '@/components/ui/skeleton';

/**
 * The mail apps settings tab, loaded only when it is shown: its dialogs, guides, and schemas stay
 * out of the settings page's first load.
 */
export const LazyMailClientsTab = dynamic(
  () =>
    import('@/features/mailbox/components/mail-clients-tab').then(feature => feature.MailClientsTab),
  {
    ssr: false,
    loading: () => (
      <div className="space-y-4 py-2" aria-hidden="true">
        <Skeleton className="h-24 w-full" />
        <Skeleton className="h-40 w-full" />
      </div>
    ),
  }
);
