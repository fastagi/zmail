'use client';

import Link from 'next/link';
import { AlertCircle } from 'lucide-react';

import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { authConfig } from '@/config/auth';
import { withReturnUrl } from '@/features/auth/utils/return-url';
import { type ComposeDraft } from '@/features/mailbox/components/compose-drafts';
import { COMPOSE_PARAM, COMPOSE_RESUME } from '@/features/mailbox/components/mailbox-params';
import { type MailboxErrorKey } from '@/features/mailbox/utils/mailbox-error';
import { useT } from '@/i18n';

/** Sign-in link that returns to this page, reopening a new message on its kept draft. */
function signInHref(mode: ComposeDraft['mode']) {
  const back = new URL(window.location.href);
  if ((mode ?? 'compose') === 'compose') {
    back.searchParams.set(COMPOSE_PARAM, COMPOSE_RESUME);
  }
  return withReturnUrl(authConfig.routes.login, `${back.pathname}${back.search}`);
}

export function ComposeErrorAlert({
  errorKey,
  mode,
}: {
  errorKey: MailboxErrorKey;
  mode: ComposeDraft['mode'];
}) {
  const t = useT('mailbox');

  return (
    <Alert variant="destructive">
      <AlertCircle className="size-4" aria-hidden="true" />
      <AlertDescription>
        {t(errorKey)}
        {errorKey === 'errors.sessionEnded' ? (
          <>
            <span className="block">{t('compose.sessionEndedDraft')}</span>
            <Button asChild size="sm" variant="outline" className="mt-2">
              <Link href={signInHref(mode)}>{t('compose.signIn')}</Link>
            </Button>
          </>
        ) : null}
      </AlertDescription>
    </Alert>
  );
}
