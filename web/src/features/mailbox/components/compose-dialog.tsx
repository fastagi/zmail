'use client';

import { useEffect, useId, useRef, useState, type ChangeEvent, type FormEvent } from 'react';
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
import {
  composeDraftKey,
  readComposeDraft,
  writeComposeDraft,
} from '@/features/mailbox/components/compose-draft-store';
import { type ComposeDraft } from '@/features/mailbox/components/compose-drafts';
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
  normalizeRecipient,
  normalizeRecipientLists,
  splitRecipients,
} from '@/features/mailbox/schemas';
import type { Mailbox } from '@/features/mailbox/types';
import {
  isStoredSendFailure,
  resolveMailboxErrorKey,
  type MailboxErrorKey,
} from '@/features/mailbox/utils/mailbox-error';
import { useT } from '@/i18n';
import { cn } from '@/utils';

export {
  type ComposeMode,
  type ComposeDraft,
  type QuotedReplyCopy,
  type ForwardDraftCopy,
  emptyDraft,
  replyDraft,
  quotedReplyDraft,
  replyAllRecipients,
  replyAllDraft,
  forwardDraft,
} from '@/features/mailbox/components/compose-drafts';

type Sender = Pick<Mailbox, 'id' | 'address'>;

export function ComposeDialog({
  mailbox,
  fromOptions,
  inline = false,
  draft,
  onClose,
  onStored,
}: {
  /** The sending mailbox, or the initial choice when `fromOptions` lets the user pick. */
  mailbox: Sender;
  /** When given, the dialog shows a From picker over these mailboxes. */
  fromOptions?: readonly Sender[];
  inline?: boolean;
  draft: ComposeDraft;
  onClose: () => void;
  /** The sent folder has a new entry: a delivered message, or one stored as failed. */
  onStored?: () => void;
}) {
  const t = useT('mailbox');
  const [fromId, setFromId] = useState(mailbox.id);
  const from = fromOptions?.find(option => option.id === fromId) ?? mailbox;
  const organizationId = useMailOrganizationId();
  const send = useSendMailMessage(organizationId, from.id);
  // Replies and forwards quote an existing message, so they are always written by hand.
  const isReplyOrForward = draft.reply || draft.mode === 'reply' || draft.mode === 'forward';
  const templates = useMailTemplates(isReplyOrForward ? 0 : organizationId, 'standard');
  const [templateId, setTemplateId] = useState<number | null>(null);
  const [values, setValues] = useState<Record<string, string>>({});
  const template = templates.data?.find(candidate => candidate.id === templateId) ?? null;
  const templateHintId = useId();
  // A draft kept when the dialog last closed takes the place of the starting text.
  const draftKey = composeDraftKey(organizationId, draft);
  const [kept] = useState(() => readComposeDraft(draftKey));
  const [to, setTo] = useState(kept?.to ?? draft.to);
  const [cc, setCc] = useState(kept?.cc ?? draft.cc ?? '');
  const [subject, setSubject] = useState(kept?.subject ?? draft.subject);
  const [body, setBody] = useState(kept?.body ?? draft.body ?? '');
  const [files, setFiles] = useState<File[]>([]);
  const [errorKey, setErrorKey] = useState<MailboxErrorKey | null>(null);
  const fileInput = useRef<HTMLInputElement>(null);
  const toInput = useRef<HTMLTextAreaElement>(null);
  const ccInput = useRef<HTMLTextAreaElement>(null);
  // Addresses a recipient field holds that are not plain email addresses, shown on that field.
  const [badTo, setBadTo] = useState<string[]>([]);
  const [badCc, setBadCc] = useState<string[]>([]);
  // The page's composer takes the focus as it opens, so the next Tab stays in the form; the
  // dialog does the same through its own open focus.
  useEffect(() => {
    if (inline) toInput.current?.focus();
  }, [inline]);

  /**
   * Names every address of To and Cc that is not a plain email address on its own field and
   * focuses the first such field; false when there is one.
   */
  const checkRecipients = (): boolean => {
    const invalid = (value: string) =>
      splitRecipients(value).filter(address => normalizeRecipient(address) === null);
    const toInvalid = invalid(to);
    const ccInvalid = invalid(cc);
    setBadTo(toInvalid);
    setBadCc(ccInvalid);
    if (toInvalid.length > 0) toInput.current?.focus();
    else if (ccInvalid.length > 0) ccInput.current?.focus();
    return toInvalid.length === 0 && ccInvalid.length === 0;
  };
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
    if (!checkRecipients()) return;
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
      const stored = await send.mutateAsync({
        to: recipients.to,
        ...(recipients.cc.length > 0 ? { cc: recipients.cc } : {}),
        template: { id: template.id, variables },
        ...(attachments.length > 0 ? { attachments } : {}),
      });
      // At the send limit the message waits; nobody is shown how much of the limit is used.
      toast.success(t(stored.queued_reason === 'send_limit' ? 'messages.queued' : 'messages.sent'));
      writeComposeDraft(draftKey, null);
      onStored?.();
      onClose();
    } catch (failure) {
      setErrorKey(resolveMailboxErrorKey(failure));
      if (isStoredSendFailure(failure)) onStored?.();
    }
  };

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setErrorKey(null);
    if (template) {
      await submitTemplate();
      return;
    }
    if (!checkRecipients()) return;
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
      const stored = await send.mutateAsync({
        to: normalized.to,
        ...(normalized.cc.length > 0 ? { cc: normalized.cc } : {}),
        subject: normalized.subject,
        text: normalized.text,
        ...(normalized.in_reply_to === '' ? {} : { in_reply_to: normalized.in_reply_to }),
        ...(attachments.length > 0 ? { attachments } : {}),
      });
      // At the send limit the message waits; nobody is shown how much of the limit is used.
      toast.success(t(stored.queued_reason === 'send_limit' ? 'messages.queued' : 'messages.sent'));
      writeComposeDraft(draftKey, null);
      onStored?.();
      onClose();
    } catch (failure) {
      // The dialog stays open so the failure cannot be missed or mistaken for a delivery.
      setErrorKey(resolveMailboxErrorKey(failure));
      if (isStoredSendFailure(failure)) onStored?.();
    }
  };

  /**
   * Closing never asks: what was written is kept as this browser's draft and the dialog says so.
   * Nothing written, or only the starting text, keeps no draft.
   */
  const dismiss = () => {
    if (send.isPending) return;
    const written = { to, cc, subject, body };
    const empty = Object.values(written).every(value => value.trim() === '');
    const untouched =
      to === draft.to &&
      cc === (draft.cc ?? '') &&
      subject === draft.subject &&
      body === (draft.body ?? '');
    if (empty || untouched) {
      writeComposeDraft(draftKey, null);
    } else {
      writeComposeDraft(draftKey, written);
      toast.success(t('compose.draftSaved'));
    }
    onClose();
  };

  const titleKey =
    draft.mode === 'forward'
      ? 'compose.forwardTitle'
      : draft.reply
        ? 'compose.replyTitle'
        : 'compose.title';

  const form = (
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
            className={inline ? 'min-h-10' : undefined}
            id="mail-compose-to"
            value={to}
            rows={1}
            spellCheck={false}
            disabled={send.isPending}
            placeholder={t('compose.toPlaceholder')}
            aria-describedby="mail-compose-recipients-hint"
            root
            ref={toInput}
            errorText={
              badTo.length > 0
                ? t('compose.invalidRecipients', { addresses: badTo.join(', ') })
                : undefined
            }
            onChange={event => {
              setBadTo([]);
              setTo(event.target.value);
            }}
          />
          <p id="mail-compose-recipients-hint" className="text-xs text-muted-foreground">
            {t('compose.recipientsHint')}
          </p>
        </div>
        <div className="space-y-2">
          <Label htmlFor="mail-compose-cc">{t('compose.cc')}</Label>
          <Textarea
            className={inline ? 'min-h-10' : undefined}
            id="mail-compose-cc"
            value={cc}
            rows={1}
            spellCheck={false}
            disabled={send.isPending}
            root
            ref={ccInput}
            errorText={
              badCc.length > 0
                ? t('compose.invalidRecipients', { addresses: badCc.join(', ') })
                : undefined
            }
            onChange={event => {
              setBadCc([]);
              setCc(event.target.value);
            }}
          />
        </div>
        {!isReplyOrForward && templates.data && templates.data.length > 0 ? (
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
                  <Label htmlFor={`mail-compose-variable-${name}`} className="font-mono text-xs">
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
                  <Paperclip className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
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
        <Button type="button" variant="outline" disabled={send.isPending} onClick={dismiss}>
          {t('actions.cancel')}
        </Button>
        <Button type="submit" loading={send.isPending} icon={<Send className="size-4" />}>
          {t('actions.send')}
        </Button>
      </DialogFooter>
    </form>
  );
  if (inline)
    return (
      <section
        aria-label={t(titleKey)}
        className="mx-auto h-full w-full max-w-3xl space-y-5 overflow-y-auto px-4 py-5 sm:px-8"
      >
        <header className="border-b pb-4">
          <h1 className="font-serif text-2xl font-semibold">{t(titleKey)}</h1>
          {!fromOptions && (
            <p className="mt-1 text-sm text-muted-foreground">
              {t('compose.description', { address: from.address })}
            </p>
          )}
        </header>
        {form}
      </section>
    );
  return (
    <Dialog open onOpenChange={open => !open && dismiss()}>
      <DialogContent
        onOpenAutoFocus={event => {
          event.preventDefault();
          toInput.current?.focus();
        }}
      >
        <DialogHeader>
          <DialogTitle>{t(titleKey)}</DialogTitle>
          <DialogDescription>
            {fromOptions
              ? t('compose.descriptionPick')
              : t('compose.description', { address: from.address })}
          </DialogDescription>
        </DialogHeader>
        {form}
      </DialogContent>
    </Dialog>
  );
}
