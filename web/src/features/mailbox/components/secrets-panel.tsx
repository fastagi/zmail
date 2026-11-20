'use client';

import { LockKeyhole } from 'lucide-react';

import { Button } from '@/components/ui/button';
import { useT } from '@/i18n';

/**
 * The notice above a message whose codes or sign-in links the API masked, with the one control
 * that shows them. Once shown, it shrinks to a short note.
 */
export function SecretsPanel({
  revealed,
  pending,
  failed,
  onReveal,
}: {
  revealed: boolean;
  pending: boolean;
  failed: boolean;
  onReveal: () => void;
}) {
  const t = useT('mailbox');
  if (revealed) {
    return (
      <p className="flex items-center gap-1.5 text-xs text-muted-foreground">
        <LockKeyhole className="size-3.5" aria-hidden="true" />
        {t('message.secretsRevealed')}
      </p>
    );
  }
  return (
    <div className="rounded-xl border border-dashed p-7 text-center leading-[1.8] text-muted-foreground">
      <div className="text-[22px] tracking-[0.2em]" aria-hidden="true">
        ● ● ● ● ● ●
      </div>
      <p>{t('message.secretsHidden')}</p>
      {failed ? (
        <p role="alert" className="text-sm text-error">
          {t('message.revealFailed')}
        </p>
      ) : null}
      <Button
        type="button"
        variant="outline"
        size="sm"
        className="mt-2.5"
        loading={pending}
        onClick={onReveal}
      >
        {t('message.revealSecrets')}
      </Button>
    </div>
  );
}
