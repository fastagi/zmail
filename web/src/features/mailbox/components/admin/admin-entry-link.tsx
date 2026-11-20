'use client';

import Link from 'next/link';
import { ShieldCheck } from 'lucide-react';

import { Button } from '@/components/ui/button';
import { ROUTES } from '@/constants/routes';
import {
  mailOrganizationHref,
  useChosenMailOrganization,
} from '@/features/mailbox/hooks/use-mail-organization';
import { useT } from '@/i18n';

/**
 * The console header's way into the admin area, shown only to owners and admins of the
 * organization the mail pages show. It reads that choice and never changes the URL.
 */
export function AdminEntryLink() {
  const t = useT();
  const organization = useChosenMailOrganization();
  if (!organization || (organization.role !== 'owner' && organization.role !== 'admin')) {
    return null;
  }
  return (
    <Button asChild variant="ghost" size="sm" className="gap-1.5">
      <Link
        href={mailOrganizationHref(ROUTES.CONSOLE.ADMIN, organization.id)}
        aria-label={t('nav.admin')}
      >
        <ShieldCheck className="size-4" aria-hidden="true" />
        <span className="hidden sm:inline">{t('nav.admin')}</span>
      </Link>
    </Button>
  );
}
