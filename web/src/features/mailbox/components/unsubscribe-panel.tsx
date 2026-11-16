'use client';

import { useState } from 'react';
import { CheckCircle2, MailX } from 'lucide-react';

import { Button } from '@/components/ui/button';
import { MAIL_UNSUBSCRIBE_TOKEN_PATTERN } from '@/features/mailbox/schemas';
import { useT } from '@/i18n';

type State = 'ready' | 'pending' | 'done' | 'invalid' | 'failed';

/** Confirms an unsubscribe link with one button; it never shows whose address it is. */
export function UnsubscribePanel({ token }: { token: string }) {
  const t = useT('mailbox');
  const [state, setState] = useState<State>(() =>
    MAIL_UNSUBSCRIBE_TOKEN_PATTERN.test(token) ? 'ready' : 'invalid'
  );

  const confirm = async () => {
    setState('pending');
    try {
      const response = await fetch(`/api/mail-unsubscribes/${token}`, {
        method: 'POST',
        cache: 'no-store',
        credentials: 'omit',
        referrerPolicy: 'no-referrer',
      });
      setState(response.ok ? 'done' : response.status === 404 ? 'invalid' : 'failed');
    } catch {
      setState('failed');
    }
  };

  const done = state === 'done';
  const invalid = state === 'invalid';
  return (
    <section className="w-full max-w-md space-y-5 rounded-lg border bg-card p-8 text-center shadow-sm">
      <div className="mx-auto flex size-12 items-center justify-center rounded-full bg-muted">
        {done ? (
          <CheckCircle2 className="size-6" aria-hidden="true" />
        ) : (
          <MailX className="size-6" aria-hidden="true" />
        )}
      </div>
      <div className="space-y-2">
        <h1 className="text-xl font-semibold">
          {t(
            done
              ? 'unsubscribe.doneTitle'
              : invalid
                ? 'unsubscribe.invalidTitle'
                : 'unsubscribe.title'
          )}
        </h1>
        <p role="status" className="text-sm text-muted-foreground">
          {t(
            done
              ? 'unsubscribe.doneDescription'
              : invalid
                ? 'unsubscribe.invalidDescription'
                : state === 'failed'
                  ? 'unsubscribe.failed'
                  : 'unsubscribe.description'
          )}
        </p>
      </div>
      {done || invalid ? null : (
        <Button type="button" loading={state === 'pending'} onClick={() => void confirm()}>
          {t('unsubscribe.confirm')}
        </Button>
      )}
    </section>
  );
}
