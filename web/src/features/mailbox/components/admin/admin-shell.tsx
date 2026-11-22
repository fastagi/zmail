'use client';

import type { ReactNode } from 'react';
import Link from 'next/link';
import { notFound, usePathname } from 'next/navigation';
import { AtSign, Globe, Inbox, Link2, Sparkles, type LucideIcon } from 'lucide-react';

import { Skeleton } from '@/components/ui/skeleton';
import { ROUTES } from '@/constants/routes';
import { ErrorAlert } from '@/features/mailbox/components/mail-shared';
import {
  mailOrganizationHref,
  useMailOrganization,
} from '@/features/mailbox/hooks/use-mail-organization';
import { useT } from '@/i18n';
import { cn } from '@/utils';

interface AdminNavItem {
  titleKey:
    | 'nav.adminDomains'
    | 'nav.adminSharedMailboxes'
    | 'nav.adminPlatformAccounts'
    | 'nav.adminAiTriage'
    | 'nav.adminExternalConnections';
  href: string;
  icon: LucideIcon;
}

const adminNavItems: AdminNavItem[] = [
  { titleKey: 'nav.adminDomains', href: ROUTES.CONSOLE.MAIL_DOMAIN, icon: Globe },
  { titleKey: 'nav.adminSharedMailboxes', href: ROUTES.CONSOLE.ADMIN_MAILBOXES, icon: Inbox },
  {
    titleKey: 'nav.adminPlatformAccounts',
    href: ROUTES.CONSOLE.ADMIN_PLATFORM_ACCOUNTS,
    icon: AtSign,
  },
  { titleKey: 'nav.adminAiTriage', href: ROUTES.CONSOLE.ADMIN_AI_TRIAGE, icon: Sparkles },
  {
    titleKey: 'nav.adminExternalConnections',
    href: ROUTES.CONSOLE.ADMIN_EXTERNAL_CONNECTIONS,
    icon: Link2,
  },
];

/**
 * Frame of the organization admin area: its navigation beside the page. Members of the chosen
 * organization get the console's 404, since the area does not exist for them; the API remains the
 * authority for every request.
 */
export function AdminShell({ children }: { children: ReactNode }) {
  const t = useT();
  const pathname = usePathname();
  const scope = useMailOrganization();

  if (scope.status === 'loading') {
    return (
      <div className="mx-auto w-full max-w-6xl space-y-4 px-4 py-6 sm:px-6" aria-hidden="true">
        <Skeleton className="h-10 w-64" />
        <Skeleton className="h-48 w-full" />
      </div>
    );
  }
  if (scope.status === 'error') {
    return (
      <div className="mx-auto w-full max-w-6xl px-4 py-6 sm:px-6">
        <ErrorAlert
          message={t('mailbox.organization.loadFailed')}
          retryLabel={t('mailbox.actions.retry')}
          onRetry={scope.retry}
        />
      </div>
    );
  }
  // Without an organization the pages explain that themselves.
  if (scope.status === 'ready' && !scope.organization.canManage) notFound();

  const organizationId = scope.status === 'ready' ? scope.organization.id : null;
  return (
    <div className="flex min-h-full flex-col md:flex-row">
      <nav
        aria-label={t('nav.admin')}
        className="flex shrink-0 gap-1 overflow-x-auto border-b px-2 py-2 text-sm font-medium md:w-48 md:flex-col md:border-r md:border-b-0 md:py-4"
      >
        <p className="hidden px-3 pb-2 text-xs font-semibold tracking-wide text-muted-foreground uppercase md:block">
          {t('nav.admin')}
        </p>
        {adminNavItems.map(item => {
          const Icon = item.icon;
          const active = pathname === item.href || pathname.startsWith(`${item.href}/`);
          return (
            <Link
              key={item.href}
              href={
                organizationId === null
                  ? item.href
                  : mailOrganizationHref(item.href, organizationId)
              }
              aria-current={active ? 'page' : undefined}
              className={cn(
                'flex items-center gap-2 rounded-lg px-3 py-2 whitespace-nowrap transition-colors hover:text-primary',
                active
                  ? 'bg-muted font-semibold text-primary'
                  : 'text-text-subtle hover:bg-muted/50'
              )}
            >
              <Icon className="size-4" aria-hidden="true" />
              {t(item.titleKey)}
            </Link>
          );
        })}
      </nav>
      <div className="min-w-0 flex-1">{children}</div>
    </div>
  );
}
