'use client';

import { useId, useState } from 'react';

import { Button } from '@/components/ui/button';
import { Label } from '@/components/ui/label';
import { Skeleton } from '@/components/ui/skeleton';
import { Textarea } from '@/components/ui/textarea';
import type { Run } from '@/features/mailbox/components/mailbox-access-panel';
import {
  useMailSignature,
  useSaveMailSignature,
} from '@/features/mailbox/hooks/use-mail-signature';
import {
  MAIL_SIGNATURE_MAX_LENGTH,
  normalizeMailSignature,
} from '@/features/mailbox/signature-schemas';
import { useT } from '@/i18n';

/**
 * The caller's own signature for a mailbox they may send from. New messages written from the
 * mailbox start with it; other members never see it.
 */
export function MailboxSignatureEditor({
  organizationId,
  mailboxId,
  run,
}: {
  organizationId: number;
  mailboxId: number;
  run: Run;
}) {
  const t = useT('mailbox');
  const signature = useMailSignature(organizationId, mailboxId);
  const ids = { field: useId(), hint: useId() };

  return (
    <section className="space-y-2 rounded-md border p-3">
      <div className="space-y-1">
        <Label htmlFor={ids.field}>{t('access.signature.label')}</Label>
        <p id={ids.hint} className="text-xs text-muted-foreground">
          {t('access.signature.hint')}
        </p>
      </div>
      {signature.isPending ? (
        <Skeleton className="h-20 w-full" />
      ) : signature.isError ? (
        <p className="text-sm text-muted-foreground" role="status">
          {t('access.signature.loadFailed')}
        </p>
      ) : (
        <SignatureForm
          // A saved signature restarts the form from what the API stored.
          key={signature.data.updated_at ?? 'none'}
          organizationId={organizationId}
          mailboxId={mailboxId}
          saved={signature.data.body}
          fieldId={ids.field}
          hintId={ids.hint}
          run={run}
        />
      )}
    </section>
  );
}

function SignatureForm({
  organizationId,
  mailboxId,
  saved,
  fieldId,
  hintId,
  run,
}: {
  organizationId: number;
  mailboxId: number;
  saved: string;
  fieldId: string;
  hintId: string;
  run: Run;
}) {
  const t = useT('mailbox');
  const save = useSaveMailSignature(organizationId, mailboxId);
  const [draft, setDraft] = useState(saved);
  const normalized = normalizeMailSignature(draft);
  const length = [...normalized].length;
  const tooLong = length > MAIL_SIGNATURE_MAX_LENGTH;
  const unchanged = normalized === saved;

  return (
    <form
      className="space-y-2"
      onSubmit={event => {
        event.preventDefault();
        if (tooLong || unchanged) return;
        void run(
          () => save.mutateAsync(normalized),
          t(normalized ? 'messages.signatureSaved' : 'messages.signatureRemoved')
        );
      }}
    >
      <Textarea
        id={fieldId}
        aria-describedby={hintId}
        aria-invalid={tooLong || undefined}
        rows={4}
        value={draft}
        placeholder={t('access.signature.placeholder')}
        onChange={event => setDraft(event.target.value)}
      />
      <div className="flex items-center justify-between gap-2">
        <span
          className={tooLong ? 'text-xs text-error' : 'text-xs text-muted-foreground'}
          aria-live="polite"
        >
          {t('access.signature.count', { count: length, max: MAIL_SIGNATURE_MAX_LENGTH })}
        </span>
        <Button type="submit" size="sm" disabled={tooLong || unchanged || save.isPending}>
          {t('access.signature.save')}
        </Button>
      </div>
    </form>
  );
}
