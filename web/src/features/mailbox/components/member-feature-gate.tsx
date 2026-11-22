'use client';

import type { ReactNode } from 'react';
import Link from 'next/link';
import { Lock } from 'lucide-react';

import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import { ROUTES } from '@/constants/routes';
import { useChosenMemberFeatures } from '@/features/mailbox/hooks/use-mail-organization';
import { useOrganizations } from '@/features/organization/hooks/use-organizations';
import type { MemberFeature } from '@/features/organization/types';
import { useT } from '@/i18n';

interface MemberFeatureGateProps {
  feature: MemberFeature;
  children: ReactNode;
  /** Shown instead of the explanation when the feature is not granted; null hides it entirely. */
  fallback?: ReactNode;
}

/**
 * Shows its children only when the member may use the feature in the organization the console
 * acts in. A deep link without the grant explains who can open it instead of failing on every
 * request. Owners and admins always pass; the API stays the authority.
 */
export function MemberFeatureGate({ feature, children, fallback }: MemberFeatureGateProps) {
  const organizations = useOrganizations();
  const features = useChosenMemberFeatures();

  if (features === undefined) {
    // No organization (or the list failed): the page shows its own empty or error state.
    if (organizations.isError || organizations.data?.items.length === 0) return <>{children}</>;
    return fallback === null ? null : <GateSkeleton />;
  }
  if (features[feature]) return <>{children}</>;
  if (fallback !== undefined) return <>{fallback}</>;
  return <FeatureNotGranted feature={feature} />;
}

function FeatureNotGranted({ feature }: { feature: MemberFeature }) {
  const t = useT('organization');
  return (
    <div
      role="status"
      className="mx-auto flex max-w-md flex-col items-center px-4 py-16 text-center sm:py-24"
    >
      <span className="flex size-12 items-center justify-center rounded-full bg-muted text-muted-foreground">
        <Lock className="size-5" aria-hidden="true" />
      </span>
      <h1 className="mt-5 text-lg font-semibold">
        {t('featureGate.title', { feature: t(`features.${feature}`) })}
      </h1>
      <p className="mt-2 text-sm text-muted-foreground">{t('featureGate.description')}</p>
      <Button asChild variant="outline" className="mt-6">
        <Link href={ROUTES.CONSOLE.HOME}>{t('featureGate.backHome')}</Link>
      </Button>
    </div>
  );
}

function GateSkeleton() {
  return (
    <div className="space-y-4 py-2" aria-hidden="true">
      <Skeleton className="h-8 w-48" />
      <Skeleton className="h-24 w-full" />
      <Skeleton className="h-24 w-full" />
    </div>
  );
}
