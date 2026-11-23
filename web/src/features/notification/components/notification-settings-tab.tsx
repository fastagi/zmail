'use client';

import dynamic from 'next/dynamic';

import { Skeleton } from '@/components/ui/skeleton';

/**
 * The settings Notifications tab loads its panel on demand, so the push and device code stays out
 * of the settings route's first load.
 */
export const NotificationSettingsTab = dynamic(
  async () =>
    (await import('@/features/notification/components/notification-settings-panel'))
      .NotificationSettingsPanel,
  {
    ssr: false,
    loading: () => (
      <div className="max-w-2xl space-y-5 py-2" aria-hidden="true">
        <Skeleton className="h-14 w-full" />
        <Skeleton className="h-20 w-full" />
      </div>
    ),
  }
);
