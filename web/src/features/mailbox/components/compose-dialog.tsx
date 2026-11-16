'use client';

import { useState, type FormEvent } from 'react';
import { AlertCircle, Send } from 'lucide-react';
import { toast } from 'sonner';

import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogBody,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Textarea } from '@/components/ui/textarea';
import { useMailOrganizationId } from '@/features/mailbox/components/mail-organization';
import { nativeSelectClassName } from '@/features/mailbox/components/mail-shared';
import { useSendMailMessage } from '@/features/mailbox/hooks/use-mailbox';
import {
  MAIL_SUBJECT_MAX_LENGTH,
  normalizeMailMessage,
  splitRecipients,
} from '@/features/mailbox/schemas';
import type { MailMessage, Mailbox } from '@/features/mailbox/types';
import {
  isStoredSendFailure,
  resolveMailboxErrorKey,
  type MailboxErrorKey,
} from '@/features/mailbox/utils/mailbox-error';
import { useT } from '@/i18n';
import { cn } from '@/utils';

export interface ComposeDraft {
  to: string;
  subject: string;
  inReplyTo: string;
  reply: boolean;
}

export const emptyDraft: ComposeDraft = { to: '', subject: '', inReplyTo: '', reply: false };

/** A reply goes to the sender, keeps an existing `Re:` prefix, and names the message it answers. */
export function replyDraft(
  message: MailMessage,
  prefixSubject: (subject: string) => string
): ComposeDraft {
  return {
    to: message.from.address,
    subject: /^re:/iu.test(message.subject) ? message.subject : prefixSubject(message.subject),
    inReplyTo: message.message_id,
    reply: true,
  };
}

type Sender = Pick<Mailbox, 'id' | 'address'>;

export function ComposeDialog({
  mailbox,
  fromOptions,
  draft,
  onClose,
  onStored,
}: {
  /** The sending mailbox, or the initial choice when `fromOptions` lets the user pick. */
  mailbox: Sender;
  /** When given, the dialog shows a From picker over these mailboxes. */
  fromOptions?: readonly Sender[];
  draft: ComposeDraft;
  onClose: () => void;
  /** The sent folder has a new entry: a delivered message, or one stored as failed. */
  onStored: () => void;
}) {
  const t = useT('mailbox');
  const [fromId, setFromId] = useState(mailbox.id);
  const from = fromOptions?.find(option => option.id === fromId) ?? mailbox;
  const send = useSendMailMessage(useMailOrganizationId(), from.id);
  const [to, setTo] = useState(draft.to);
  const [cc, setCc] = useState('');
  const [subject, setSubject] = useState(draft.subject);
  const [body, setBody] = useState('');
  const [errorKey, setErrorKey] = useState<MailboxErrorKey | null>(null);

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setErrorKey(null);
    const normalized = normalizeMailMessage({
      to: splitRecipients(to),
      cc: splitRecipients(cc),
      subject,
      text: body,
      in_reply_to: draft.inReplyTo,
    });
    if (!normalized) {
      setErrorKey('errors.messageInvalid');
      return;
    }
    try {
      await send.mutateAsync({
        to: normalized.to,
        ...(normalized.cc.length > 0 ? { cc: normalized.cc } : {}),
        subject: normalized.subject,
        text: normalized.text,
        ...(normalized.in_reply_to === '' ? {} : { in_reply_to: normalized.in_reply_to }),
      });
      toast.success(t('messages.sent'));
      onStored();
      onClose();
    } catch (failure) {
      // The dialog stays open so the failure cannot be missed or mistaken for a delivery.
      setErrorKey(resolveMailboxErrorKey(failure));
      if (isStoredSendFailure(failure)) onStored();
    }
  };

  return (
    <Dialog open onOpenChange={open => !open && !send.isPending && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t(draft.reply ? 'compose.replyTitle' : 'compose.title')}</DialogTitle>
          <DialogDescription>
            {fromOptions
              ? t('compose.descriptionPick')
              : t('compose.description', { address: from.address })}
          </DialogDescription>
        </DialogHeader>
        <form onSubmit={submit}>
          <DialogBody className="space-y-4">
            {fromOptions ? (
              <div className="space-y-2">
                <Label htmlFor="mail-compose-from">{t('compose.from')}</Label>
                <select
                  id="mail-compose-from"
                  className={cn(nativeSelectClassName, 'w-full')}
                  value={String(from.id)}
                  disabled={send.isPending}
                  onChange={event => setFromId(Number(event.target.value))}
                >
                  {fromOptions.map(option => (
                    <option key={option.id} value={String(option.id)}>
                      {option.address}
                    </option>
                  ))}
                </select>
              </div>
            ) : null}
            <div className="space-y-2">
              <Label htmlFor="mail-compose-to">{t('compose.to')}</Label>
              <Textarea
                id="mail-compose-to"
                value={to}
                rows={2}
                spellCheck={false}
                disabled={send.isPending}
                placeholder={t('compose.toPlaceholder')}
                aria-describedby="mail-compose-recipients-hint"
                onChange={event => setTo(event.target.value)}
              />
              <p id="mail-compose-recipients-hint" className="text-xs text-muted-foreground">
                {t('compose.recipientsHint')}
              </p>
            </div>
            <div className="space-y-2">
              <Label htmlFor="mail-compose-cc">{t('compose.cc')}</Label>
              <Textarea
                id="mail-compose-cc"
                value={cc}
                rows={1}
                spellCheck={false}
                disabled={send.isPending}
                onChange={event => setCc(event.target.value)}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="mail-compose-subject">{t('compose.subject')}</Label>
              <Input
                id="mail-compose-subject"
                value={subject}
                maxLength={MAIL_SUBJECT_MAX_LENGTH}
                disabled={send.isPending}
                onChange={event => setSubject(event.target.value)}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="mail-compose-body">{t('compose.body')}</Label>
              <Textarea
                id="mail-compose-body"
                value={body}
                rows={8}
                disabled={send.isPending}
                onChange={event => setBody(event.target.value)}
              />
            </div>
            {errorKey ? (
              <Alert variant="destructive">
                <AlertCircle className="size-4" aria-hidden="true" />
                <AlertDescription>{t(errorKey)}</AlertDescription>
              </Alert>
            ) : null}
          </DialogBody>
          <DialogFooter className="mt-4">
            <Button type="button" variant="outline" disabled={send.isPending} onClick={onClose}>
              {t('actions.cancel')}
            </Button>
            <Button type="submit" loading={send.isPending} icon={<Send className="size-4" />}>
              {t('actions.send')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
