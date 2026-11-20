import { notFound } from 'next/navigation';

import { isWebFeatureEnabled } from '@/config/features';
import { DeveloperConsole } from '@/features/developer';

export default function DevelopersPage() {
  if (!isWebFeatureEnabled('organization') || !isWebFeatureEnabled('mailbox')) notFound();

  return (
    <div className="mx-auto w-full max-w-7xl px-4 py-6 sm:px-6 lg:py-8">
      <DeveloperConsole />
    </div>
  );
}
