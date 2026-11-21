'use client';

import dynamic from 'next/dynamic';

/**
 * The getting-started checklist, loaded after the console home: it reads the team's mailboxes,
 * members, and domains, which the home page's own bundle does not need.
 */
export const LazyOnboardingChecklist = dynamic(
  () =>
    import('@/features/mailbox/components/onboarding-checklist').then(
      feature => feature.OnboardingChecklist
    ),
  { ssr: false }
);
