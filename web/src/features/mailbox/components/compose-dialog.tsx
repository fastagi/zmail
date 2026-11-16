'use client';

import { useId, useRef, useState, type ChangeEvent, type FormEvent } from 'react';
import { AlertCircle, Paperclip, Send, X } from 'lucide-react';
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
import { fileToBase64, formatBytes } from '@/features/mailbox/components/mail-attachments';
import { useMailOrganizationId } from '@/features/mailbox/components/mail-organization';
import { nativeSelectClassName } from '@/features/mailbox/components/mail-shared';
import { useSendMailMessage } from '@/features/mailbox/hooks/use-mailbox';
import { useMailTemplates } from '@/features/mailbox/hooks/use-mail-templates';
import {
  MAIL_ATTACHMENTS_MAX_BYTES,
  MAIL_MAX_ATTACHMENTS,
  MAIL_SUBJECT_MAX_LENGTH,
  normalizeMailMessage,
  normalizeRecipientLists,
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
  const organizationId = useMailOrganizationId();
  const send = useSendMailMessage(organizationId, from.id);
  // A reply quotes nothing and answers one message, so it is always written by hand.
  const templates = useMailTemplates(draft.reply ? 0 : organizationId);
  const [templateId, setTemplateId] = useState<number | null>(null);
  const [values, setValues] = useState<Record<string, string>>({});
  const template = templates.data?.find(candidate => candidate.id === templateId) ?? null;
  const templateHintId = useId();
  const [to, setTo] = useState(draft.to);
  const [cc, setCc] = useState('');
  const [subject, setSubject] = useState(draft.subject);
  const [body, setBody] = useState('');
  const [files, setFiles] = useState<File[]>([]);
  const [errorKey, setErrorKey] = useState<MailboxErrorKey | null>(null);
  const fileInput = useRef<HTMLInputElement>(null);
  const attachHintId = useId();
  const filesTooMany = files.length > MAIL_MAX_ATTACHMENTS;
  const filesTooLarge =
    files.reduce((total, file) => total + file.size, 0) > MAIL_ATTACHMENTS_MAX_BYTES;

  const addFiles = (event: ChangeEvent<HTMLInputElement>) => {
    const chosen = Array.from(event.target.files ?? []);
    // Clear the input so the same file can be chosen again after it is removed.
    event.target.value = '';
    setErrorKey(null);
    setFiles(current => [...current, ...chosen]);
  };

  const submitTemplate = async () => {
    if (!template) return;
    const recipients = normalizeRecipientLists(splitRecipients(to), splitRecipients(cc));
    if (!recipients) {
      setErrorKey('errors.messageInvalid');
      return;
    }
    if (filesTooMany || filesTooLarge || files.some(file => file.size === 0)) {
      setErrorKey('errors.attachmentsInvalid');
      return;
    }
    // An empty field is left out, so the template's fallback applies.
    const variables = Object.fromEntries(
      template.variables
        .map(name => [name, (values[name] ?? '').trim()] as const)
        .filter(([, value]) => value !== '')
    );
    try {
      const attachments = await Promise.all(
        files.map(async file => ({
          filename: file.name,
          ...(file.type ? { content_type: file.type } : {}),
          content: await fileToBase64(file),
        }))
      );
      await send.mutateAsync({
        to: recipients.to,
        ...(recipients.cc.length > 0 ? { cc: recipients.cc } : {}),
        template: { id: template.id, variables },
        ...(attachments.length > 0 ? { attachments } : {}),
      });
      toast.success(t('messages.sent'));
      onStored();
      onClose();
    } catch (failure) {
      setErrorKey(resolveMailboxErrorKey(failure));
      if (isStoredSendFailure(failure)) onStored();
    }
  };

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setErrorKey(null);
    if (template) {
      await submitTemplate();
      return;
    }
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
    if (filesTooMany || filesTooLarge || files.some(file => file.size === 0)) {
      setErrorKey('errors.attachmentsInvalid');
      return;
    }
    try {
      const attachments = await Promise.all(
        files.map(async file => ({
          filename: file.name,
          ...(file.type ? { content_type: file.type } : {}),
          content: await fileToBase64(file),
        }))
      );
      await send.mutateAsync({
        to: normalized.to,
        ...(normalized.cc.length > 0 ? { cc: normalized.cc } : {}),
        subject: normalized.subject,
        text: normalized.text,
        ...(normalized.in_reply_to === '' ? {} : { in_reply_to: normalized.in_reply_to }),
        ...(attachments.length > 0 ? { attachments } : {}),
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
            {templates.data && templates.data.length > 0 ? (
              <div className="space-y-2">
                <Label htmlFor="mail-compose-template">{t('compose.template')}</Label>
                <select
                  id="mail-compose-template"
                  className={cn(nativeSelectClassName, 'w-full')}
                  value={templateId === null ? '' : String(templateId)}
                  disabled={send.isPending}
                  onChange={event => {
                    setErrorKey(null);
                    setValues({});
                    setTemplateId(event.target.value === '' ? null : Number(event.target.value));
                  }}
                >
                  <option value="">{t('compose.templateNone')}</option>
                  {templates.data.map(option => (
                    <option key={option.id} value={String(option.id)}>
                      {option.name}
                    </option>
                  ))}
                </select>
              </div>
            ) : null}
            {template ? (
              template.variables.length > 0 ? (
                <fieldset className="space-y-2" aria-describedby={templateHintId}>
                  <legend className="text-sm font-medium">{t('templates.variables')}</legend>
                  <p id={templateHintId} className="text-xs text-muted-foreground">
                    {t('compose.templateHint')}
                  </p>
                  {template.variables.map(name => (
                    <div key={name} className="space-y-1">
                      <Label
                        htmlFor={`mail-compose-variable-${name}`}
                        className="font-mono text-xs"
                      >
                        {name}
                      </Label>
                      <Input
                        id={`mail-compose-variable-${name}`}
                        value={values[name] ?? ''}
                        disabled={send.isPending}
                        onChange={event =>
                          setValues(current => ({ ...current, [name]: event.target.value }))
                        }
                      />
                    </div>
                  ))}
                </fieldset>
              ) : null
            ) : (
              <>
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
              </>
            )}
            <div className="space-y-2">
              <input
                ref={fileInput}
                id="mail-compose-files"
                type="file"
                multiple
                className="sr-only"
                tabIndex={-1}
                aria-hidden="true"
                onChange={addFiles}
              />
              <div className="flex flex-wrap items-center gap-3">
                <Button
                  type="button"
                  size="sm"
                  variant="outline"
                  disabled={send.isPending}
                  icon={<Paperclip className="size-4" />}
                  aria-describedby={attachHintId}
                  onClick={() => fileInput.current?.click()}
                >
                  {t('compose.attach')}
                </Button>
                <p
                  id={attachHintId}
                  className={cn(
                    'text-xs',
                    filesTooMany || filesTooLarge ? 'text-error' : 'text-muted-foreground'
                  )}
                >
                  {t('compose.attachHint')}
                </p>
              </div>
              {files.length > 0 ? (
                <ul className="divide-y rounded-md border text-sm">
                  {files.map((file, index) => (
                    // A list the user edits by position; two files may share a name.
                    <li key={index} className="flex items-center gap-3 px-3 py-1.5">
                      <Paperclip
                        className="size-4 shrink-0 text-muted-foreground"
                        aria-hidden="true"
                      />
                      <span className="min-w-0 flex-1 truncate" title={file.name}>
                        {file.name}
                      </span>
                      <span className="text-xs whitespace-nowrap text-muted-foreground">
                        {formatBytes(file.size)}
                      </span>
                      <Button
                        type="button"
                        size="xs"
                        isIcon
                        variant="ghost"
                        disabled={send.isPending}
                        icon={<X className="size-3.5" />}
                        aria-label={t('compose.removeAttachment', { name: file.name })}
                        onClick={() =>
                          setFiles(current => current.filter((_, position) => position !== index))
                        }
                      />
                    </li>
                  ))}
                </ul>
              ) : null}
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
