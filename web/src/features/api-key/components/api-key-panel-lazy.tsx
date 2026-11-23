'use client';

import dynamic from 'next/dynamic';

import { Skeleton } from '@/components/ui/skeleton';

/**
 * The API keys settings tab, loaded only when it is shown: its create dialog, scope and expiry
 * choices, and code samples stay out of the settings page's first load.
 */
export const LazyApiKeyPanel = dynamic(
  () => import('@/features/api-key/components/api-key-panel').then(feature => feature.ApiKeyPanel),
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
