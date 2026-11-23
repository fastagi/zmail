'use client';

import Link from 'next/link';
import { Code2 } from 'lucide-react';

import { Button } from '@/components/ui/button';
import { ROUTES } from '@/constants/routes';
import { useT } from '@/i18n';

/** Explains the developer console to a member without developer access, instead of raw 403s. */
export function DeveloperAccessRequired() {
  const t = useT('developer');
  return (
    <div
      role="status"
      className="mx-auto flex max-w-md flex-col items-center px-4 py-16 text-center sm:py-24"
    >
      <span className="flex size-12 items-center justify-center rounded-full bg-muted text-muted-foreground">
        <Code2 className="size-5" aria-hidden="true" />
      </span>
      <h1 className="mt-5 text-lg font-semibold">{t('accessRequired.title')}</h1>
      <p className="mt-2 text-sm text-muted-foreground">{t('accessRequired.description')}</p>
      <p className="mt-2 text-sm font-medium">{t('accessRequired.contactHint')}</p>
      <Button asChild variant="outline" className="mt-6">
        <Link href={ROUTES.CONSOLE.HOME}>{t('accessRequired.backHome')}</Link>
      </Button>
    </div>
  );
}
