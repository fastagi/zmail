import { FileText, ShieldOff, TriangleAlert } from 'lucide-react';
import { type FormEvent, useId, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogTitle,
} from '@/components/ui/dialog';
import { Label } from '@/components/ui/label';
import { useBroadcastContent } from '@/features/mail-operations/hooks/use-mail-operations';
import { contentReasonMaxLength, type OutboundMessage } from '@/features/mail-operations/types';
import { ApiErrorCode } from '@/http/codes';
import { ApiError } from '@/http/client';
import { cn } from '@/lib/cn';

interface BroadcastContentDialogProps {
  /** The broadcast message to read; null keeps the dialog closed. */
  message: OutboundMessage | null;
  onClose: () => void;
}

const timeFormat = new Intl.DateTimeFormat('en-GB', {
  timeZone: 'UTC',
  dateStyle: 'medium',
  timeStyle: 'medium',
});

/**
 * Asks for a reason, then shows the sent text of one broadcast message. The API stores the access
 * record before it returns the text. The text lives only in this dialog: closing it resets the
 * mutation, which is never cached.
 */
export function BroadcastContentDialog({ message, onClose }: BroadcastContentDialogProps) {
  const { t } = useTranslation();
  const content = useBroadcastContent();
  const [reason, setReason] = useState('');
  const [touched, setTouched] = useState(false);
  const reasonId = useId();
  const hintId = useId();
  const trimmed = reason.trim();
  const reasonError = !trimmed
    ? t('mailOperations.content.reasonRequired')
    : trimmed.length > contentReasonMaxLength
      ? t('mailOperations.content.reasonTooLong', { max: contentReasonMaxLength })
      : undefined;

  const close = () => {
    content.reset();
    setReason('');
    setTouched(false);
    onClose();
  };

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setTouched(true);
    if (!message || reasonError || content.isPending) {
      return;
    }
    content.mutate({
      organizationId: message.organization_id,
      messageId: message.id,
      reason: trimmed,
    });
  };

  const result = content.data;

  return (
    <Dialog open={message !== null} onOpenChange={(open) => (open ? undefined : close())}>
      <DialogContent className="max-w-2xl">
        <DialogTitle>{t('mailOperations.content.title')}</DialogTitle>
        <DialogDescription>{t('mailOperations.content.description')}</DialogDescription>

        {result ? (
          <div className="grid gap-3 text-sm">
            <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1">
              <dt className="text-muted-foreground">{t('mailOperations.content.subject')}</dt>
              <dd className="break-words">
                {result.subject || t('mailOperations.messages.noSubject')}
              </dd>
              <dt className="text-muted-foreground">{t('mailOperations.content.from')}</dt>
              <dd className="break-all">{result.from_address}</dd>
              <dt className="text-muted-foreground">{t('mailOperations.content.sent')}</dt>
              <dd className="tabular-nums">{`${timeFormat.format(new Date(result.created_at))} UTC`}</dd>
            </dl>
            <section aria-label={t('mailOperations.content.text')} className="grid gap-1">
              <h3 className="flex items-center gap-2 font-medium">
                <FileText aria-hidden="true" className="size-4" />
                {t('mailOperations.content.text')}
              </h3>
              {result.text_source === 'html' ? (
                <p className="text-xs text-muted-foreground">
                  {t('mailOperations.content.fromHtml')}
                </p>
              ) : null}
              {/* Shown as text only: the API never returns HTML and nothing here is rendered as markup. */}
              <pre className="max-h-96 overflow-auto whitespace-pre-wrap break-words rounded-md border bg-muted/40 p-3 font-sans text-sm">
                {result.text || t('mailOperations.content.empty')}
              </pre>
            </section>
            <p className="text-xs text-muted-foreground">
              {t('mailOperations.content.recorded', { id: result.access_id })}
            </p>
            <DialogFooter>
              <Button type="button" onClick={close}>
                {t('common.close')}
              </Button>
            </DialogFooter>
          </div>
        ) : (
          <form onSubmit={submit} className="grid gap-3" noValidate>
            <div className="grid gap-1.5">
              <Label htmlFor={reasonId}>{t('mailOperations.content.reason')}</Label>
              <textarea
                id={reasonId}
                name="reason"
                rows={3}
                value={reason}
                aria-describedby={hintId}
                aria-invalid={touched && reasonError ? true : undefined}
                onChange={(event) => setReason(event.target.value)}
                className={cn(
                  'w-full rounded-md border border-input bg-transparent px-3 py-2 text-base shadow-sm placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring md:text-sm',
                  touched && reasonError ? 'border-destructive' : undefined,
                )}
              />
              <p id={hintId} className="text-xs text-muted-foreground">
                {touched && reasonError
                  ? reasonError
                  : t('mailOperations.content.reasonHint', { max: contentReasonMaxLength })}
              </p>
            </div>
            {content.isError ? <ContentFailure error={content.error} /> : null}
            <DialogFooter>
              <Button type="button" variant="outline" onClick={close}>
                {t('common.cancel')}
              </Button>
              <Button type="submit" disabled={content.isPending}>
                {content.isPending
                  ? t('mailOperations.content.submitting')
                  : t('mailOperations.content.submit')}
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  );
}

function ContentFailure({ error }: { error: unknown }) {
  const { t } = useTranslation();
  const code = error instanceof ApiError ? error.errorCode : undefined;
  if (code === ApiErrorCode.OPERATOR_CAPABILITY_REQUIRED) {
    return (
      <div role="alert" className="flex items-start gap-2 text-sm">
        <ShieldOff aria-hidden="true" className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
        <p>{t('mailOperations.content.forbidden')}</p>
      </div>
    );
  }
  const messageKey =
    code === ApiErrorCode.OPERATOR_CONTENT_NOT_ELIGIBLE
      ? 'mailOperations.content.notEligible'
      : code === ApiErrorCode.MAILBOX_MESSAGE_NOT_FOUND
        ? 'mailOperations.content.notFound'
        : code === ApiErrorCode.COMMON_VALIDATION_FAILED
          ? 'mailOperations.content.invalid'
          : 'mailOperations.content.unavailable';
  return (
    <div role="alert" className="flex items-start gap-2 text-sm text-destructive">
      <TriangleAlert aria-hidden="true" className="mt-0.5 size-4 shrink-0" />
      <p>{t(messageKey, { max: contentReasonMaxLength })}</p>
    </div>
  );
}
