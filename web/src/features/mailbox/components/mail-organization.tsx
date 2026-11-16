'use client';

import { createContext, useContext, type ReactNode } from 'react';
import Link from 'next/link';
import { Building2, ShieldAlert } from 'lucide-react';

import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import { ROUTES } from '@/constants/routes';
import {
  EmptyState,
  ErrorAlert,
  nativeSelectClassName,
} from '@/features/mailbox/components/mail-shared';
import type {
  MailOrganization,
  MailOrganizationScope,
} from '@/features/mailbox/hooks/use-mail-organization';
import { useT } from '@/i18n';
import { cn } from '@/utils';

const MailOrganizationContext = createContext<number | null>(null);

/** The organization every mailbox request below `MailOrganizationBoundary` acts in. */
export function useMailOrganizationId(): number {
  const organizationId = useContext(MailOrganizationContext);
  if (organizationId === null) {
    throw new Error('useMailOrganizationId must be used inside MailOrganizationBoundary');
  }
  return organizationId;
}

/**
 * Title of a mail page with the organization it shows. One organization is named as text; more
 * than one become a picker. `children` are the page's own actions.
 */
export function MailPageHeader({
  title,
  description,
  scope,
  children,
}: {
  title: string;
  description: string;
  scope: MailOrganizationScope;
  children?: ReactNode;
}) {
  const t = useT('mailbox');
  return (
    <div className="flex flex-wrap items-start justify-between gap-3 border-b pb-5">
      <div className="min-w-0">
        <h1 className="text-2xl font-semibold">{title}</h1>
        <p className="mt-1 text-sm text-muted-foreground">{description}</p>
      </div>
      <div className="flex flex-wrap items-center gap-2">
        {scope.status !== 'ready' ? null : scope.organizations.length > 1 ? (
          <select
            className={cn(nativeSelectClassName, 'max-w-56')}
            aria-label={t('organization.label')}
            value={String(scope.organization.id)}
            onChange={event => scope.select(Number(event.target.value))}
          >
            {scope.organizations.map(organization => (
              <option key={organization.id} value={String(organization.id)}>
                {organization.name}
              </option>
            ))}
          </select>
        ) : (
          <span
            data-testid="mail-organization-name"
            className="flex min-w-0 items-center gap-1.5 text-sm text-muted-foreground"
          >
            <Building2 className="size-4 shrink-0" aria-hidden="true" />
            <span className="max-w-56 truncate">{scope.organization.name}</span>
          </span>
        )}
        {children}
      </div>
    </div>
  );
}

/**
 * Renders a mail page for the chosen organization, or says why there is none: the list is
 * loading, could not be loaded, or the user belongs to no organization. The content is remounted
 * when the organization changes, so nothing of the previous one stays on screen.
 */
export function MailOrganizationBoundary({
  scope,
  children,
}: {
  scope: MailOrganizationScope;
  children: (organization: MailOrganization) => ReactNode;
}) {
  const t = useT('mailbox');
  if (scope.status === 'loading') {
    return (
      <div className="space-y-4" aria-hidden="true">
        <Skeleton className="h-28 w-full" />
        <Skeleton className="h-48 w-full" />
      </div>
    );
  }
  if (scope.status === 'error') {
    return (
      <ErrorAlert
        message={t('organization.loadFailed')}
        retryLabel={t('actions.retry')}
        onRetry={scope.retry}
      />
    );
  }
  if (scope.status === 'empty') {
    return (
      <EmptyState
        icon={<Building2 className="size-5" aria-hidden="true" />}
        title={t('organization.emptyTitle')}
        description={t('organization.empty')}
        action={<OrganizationsLink />}
      />
    );
  }
  return (
    <MailOrganizationContext.Provider key={scope.organization.id} value={scope.organization.id}>
      {children(scope.organization)}
    </MailOrganizationContext.Provider>
  );
}

/** Shown when the API no longer finds the organization: the member was removed from it. */
export function OrganizationLostState() {
  const t = useT('mailbox');
  return (
    <EmptyState
      icon={<ShieldAlert className="size-5" aria-hidden="true" />}
      title={t('organization.lostTitle')}
      description={t('errors.organizationNotFound')}
      action={<OrganizationsLink />}
    />
  );
}

function OrganizationsLink() {
  const t = useT('mailbox');
  return (
    <Button asChild size="sm" variant="outline">
      <Link href={ROUTES.CONSOLE.ORGANIZATIONS}>{t('organization.open')}</Link>
    </Button>
  );
}
