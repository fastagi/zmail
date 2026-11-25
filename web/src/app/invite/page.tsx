'use client';

import { useEffect, useRef, useState } from 'react';
import Link from 'next/link';
import { useRouter } from 'next/navigation';
import { AlertCircle, Loader2, LogIn, Mail, UserPlus } from 'lucide-react';

import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Logo } from '@/components/ui/icons';
import { ROUTES } from '@/constants/routes';
import { withReturnUrl } from '@/features/auth/utils/return-url';
import {
  readInvitationFragment,
  stashPendingInvitation,
} from '@/features/organization/utils/pending-invitation';
import { useT } from '@/i18n';

type InviteStatus = 'checking' | 'unauthenticated' | 'missing';

const acceptTarget = `${ROUTES.CONSOLE.ORGANIZATIONS}?accept=1`;

/**
 * Where an invitation mail links: `/invite#<token>`. The token stays out of every URL a server
 * sees; it is kept in this tab while the invitee signs in or signs up, then the organization page
 * offers it in the accept dialog.
 */
export default function InvitePage() {
  const router = useRouter();
  const t = useT('organization');
  const resolvedTokenRef = useRef<string | null | undefined>(undefined);
  const [status, setStatus] = useState<InviteStatus>('checking');

  useEffect(() => {
    if (resolvedTokenRef.current === undefined) {
      const fragmentToken = readInvitationFragment(window.location.hash);
      if (fragmentToken !== null) {
        stashPendingInvitation(fragmentToken);
      }
      // Drop the fragment from history before moving on.
      window.history.replaceState(null, '', window.location.pathname);
      resolvedTokenRef.current = fragmentToken;
    }

    if (resolvedTokenRef.current === null) {
      setStatus('missing');
      return;
    }

    const controller = new AbortController();

    fetch('/api/auth/me', {
      credentials: 'include',
      signal: controller.signal,
    })
      .then((res) => {
        if (controller.signal.aborted) return;
        if (res.ok) {
          router.replace(acceptTarget);
          return;
        }
        setStatus('unauthenticated');
      })
      .catch(() => {
        if (controller.signal.aborted) return;
        setStatus('unauthenticated');
      });

    return () => {
      controller.abort();
    };
  }, [router]);

  const registerHref = withReturnUrl(ROUTES.AUTH.REGISTER, acceptTarget);
  const loginHref = withReturnUrl(ROUTES.AUTH.LOGIN, acceptTarget);

  if (status === 'checking') {
    return (
      <main
        aria-busy="true"
        className="flex min-h-svh flex-col items-center justify-center bg-bg-canvas px-4 py-12"
      >
        <div className="flex flex-col items-center gap-3 text-center">
          <Loader2 className="size-6 animate-spin text-primary" aria-hidden="true" />
          <p className="text-sm text-muted-foreground">{t('inviteLanding.checking')}</p>
        </div>
      </main>
    );
  }

  return (
    <main className="flex min-h-svh flex-col items-center justify-center bg-bg-canvas px-4 py-12">
      <div className="w-full max-w-md space-y-6">
        <div className="flex items-center justify-center gap-2.5">
          <Logo className="size-8 text-primary" />
          <span className="text-xl font-semibold tracking-tight text-foreground">Zmail</span>
        </div>

        {status === 'missing' ? (
          <Card>
            <CardHeader className="items-center text-center">
              <div className="mb-2 flex size-12 items-center justify-center rounded-full bg-destructive/10 text-destructive">
                <AlertCircle className="size-6" aria-hidden="true" />
              </div>
              <CardTitle className="text-xl">{t('inviteLanding.invalidTitle')}</CardTitle>
              <CardDescription className="leading-relaxed">
                {t('inviteLanding.invalidDescription')}
              </CardDescription>
            </CardHeader>
            <CardContent className="flex flex-col gap-3">
              <Button asChild className="w-full">
                <Link href={ROUTES.AUTH.LOGIN}>{t('inviteLanding.goToLogin')}</Link>
              </Button>
            </CardContent>
          </Card>
        ) : (
          <Card>
            <CardHeader className="items-center text-center">
              <div className="mb-2 flex size-12 items-center justify-center rounded-full bg-primary/10 text-primary">
                <Mail className="size-6" aria-hidden="true" />
              </div>
              <CardTitle className="text-xl">{t('inviteLanding.title')}</CardTitle>
              <CardDescription className="leading-relaxed">
                {t('inviteLanding.hint')}
              </CardDescription>
            </CardHeader>
            <CardContent className="flex flex-col gap-3">
              <Button asChild className="w-full" icon={<UserPlus className="size-4" />}>
                <Link href={registerHref}>{t('inviteLanding.registerCta')}</Link>
              </Button>
              <Button
                asChild
                variant="outline"
                className="w-full"
                icon={<LogIn className="size-4" />}
              >
                <Link href={loginHref}>{t('inviteLanding.loginCta')}</Link>
              </Button>
            </CardContent>
          </Card>
        )}
      </div>
    </main>
  );
}
