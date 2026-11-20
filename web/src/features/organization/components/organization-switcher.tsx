'use client';

import Link from 'next/link';
import { usePathname, useSearchParams } from 'next/navigation';
import { Building2, Check, ChevronsUpDown } from 'lucide-react';

import { Button } from '@/components/ui/button';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { ROUTES, getOrganizationRoute } from '@/constants/routes';
import { useOrganizations } from '@/features/organization/hooks/use-organizations';
import { useT } from '@/i18n';

const organizationPath = /^\/console\/organizations\/([1-9]\d*)(?:\/|$)/;
const organizationIdPattern = /^[1-9]\d{0,15}$/u;
const ACTIVE_ORGANIZATION_STORAGE_KEY = 'zmail.activeOrganizationId';

const queryScopedConsolePaths = new Set<string>([
  ROUTES.CONSOLE.EMAILS,
  ROUTES.CONSOLE.MAILBOXES,
  ROUTES.CONSOLE.MAIL_BROADCASTS,
  ROUTES.CONSOLE.MAIL_CONTACTS,
  ROUTES.CONSOLE.MAIL_TEMPLATES,
  ROUTES.CONSOLE.MAIL_DOMAIN,
  ROUTES.CONSOLE.ADMIN_MAILBOXES,
  ROUTES.CONSOLE.ADMIN_PLATFORM_ACCOUNTS,
  ROUTES.CONSOLE.DEVELOPERS,
]);

function rememberOrganization(organizationId: number): void {
  try {
    window.sessionStorage.setItem(ACTIVE_ORGANIZATION_STORAGE_KEY, String(organizationId));
  } catch {
    // Ignore storage errors in restricted contexts.
  }
}

function readRememberedOrganization(): number | null {
  if (typeof window === 'undefined') return null;
  try {
    const raw = window.sessionStorage.getItem(ACTIVE_ORGANIZATION_STORAGE_KEY) ?? '';
    return organizationIdPattern.test(raw) ? Number(raw) : null;
  } catch {
    return null;
  }
}

export function OrganizationSwitcher() {
  const pathname = usePathname();
  const searchParams = useSearchParams();
  const t = useT();
  const organizations = useOrganizations();
  const items = organizations.data?.items ?? [];

  const pathMatch = organizationPath.exec(pathname);
  const queryOrg = searchParams.get('org') ?? '';
  const queryId = organizationIdPattern.test(queryOrg) ? Number(queryOrg) : null;
  const candidateId = pathMatch ? Number(pathMatch[1]) : (queryId ?? readRememberedOrganization());
  const selected = items.find(({ id }) => id === candidateId) ?? items[0];
  const staysOnCurrentPage = queryScopedConsolePaths.has(pathname);

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          variant="outline"
          size="sm"
          noScale
          className="max-w-[11rem] justify-between md:max-w-[15rem]"
          aria-label={t('nav.organizations')}
        >
          <Building2 className="size-4" aria-hidden="true" />
          <span className="hidden min-w-0 flex-1 truncate text-left sm:block">
            {selected?.name ?? t('nav.organizations')}
          </span>
          <ChevronsUpDown
            className="hidden size-3.5 text-muted-foreground sm:block"
            aria-hidden="true"
          />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="w-64">
        <DropdownMenuLabel>{t('nav.organizations')}</DropdownMenuLabel>
        {items.map(organization => {
          const href = staysOnCurrentPage
            ? `${pathname}?org=${organization.id}`
            : getOrganizationRoute(organization.id);
          return (
            <DropdownMenuItem
              key={organization.id}
              asChild
              onSelect={() => rememberOrganization(organization.id)}
            >
              <Link href={href}>
                <span className="min-w-0 flex-1 truncate">{organization.name}</span>
                {organization.id === selected?.id ? (
                  <Check className="ml-auto size-4" aria-hidden="true" />
                ) : null}
              </Link>
            </DropdownMenuItem>
          );
        })}
        <DropdownMenuSeparator />
        <DropdownMenuItem asChild>
          <Link href={ROUTES.CONSOLE.ORGANIZATIONS}>
            <Building2 className="size-4" aria-hidden="true" />
            {t('nav.organizations')}
          </Link>
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
