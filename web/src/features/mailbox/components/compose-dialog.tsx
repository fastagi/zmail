'use client';

import {
  useContext,
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
  type ChangeEvent,
  type FormEvent,
} from 'react';
import Link from 'next/link';
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
import { authConfig } from '@/config/auth';
import { AuthStoreContext } from '@/features/auth/store/auth-store';
import { withReturnUrl } from '@/features/auth/utils/return-url';
import {
  type StoredComposeDraft,
  composeDraftKey,
  draftToKeep,
  readComposeDraft,
  readComposeDraftFiles,
  writeComposeDraft,
  writeComposeDraftFiles,
} from '@/features/mailbox/components/compose-draft-store';
import { COMPOSE_PARAM, COMPOSE_RESUME } from '@/features/mailbox/components/mailbox-params';
import { type ComposeDraft } from '@/features/mailbox/components/compose-drafts';
import { fileToBase64, formatBytes } from '@/features/mailbox/components/mail-attachments';
import { useMailOrganizationId } from '@/features/mailbox/components/mail-organization';
import { mailboxService } from '@/features/mailbox/services/mailbox-service';
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
import type { MailAttachment, Mailbox, SendMailMessageInput } from '@/features/mailbox/types';
import {
  isStoredSendFailure,
  resolveMailboxErrorKey,
  type MailboxErrorKey,
} from '@/features/mailbox/utils/mailbox-error';
import { useMailSignature } from '@/features/mailbox/hooks/use-mail-signature';
import { useDeleteMailDraft, useSaveMailDraft } from '@/features/mailbox/hooks/use-mail-drafts';
import {
  flushPendingSend,
  markReopenedCompose,
  scheduleUndoableSend,
  takeReopenedCompose,
  undoSendDelayMs,
  type ReopenedCompose,
} from '@/features/mailbox/components/undo-send';
import { mailSignatureBlock } from '@/features/mailbox/signature-schemas';
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
  onReopen,
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
  /**
   * Opens this composer again on the same draft. Given, Send closes the composer and waits a few
   * seconds with Undo before sending; Undo or a failed send reopens it as written.
   */
  onReopen?: () => void;
}) {
  const t = useT('mailbox');
  const organizationId = useMailOrganizationId();
  // A draft kept when the dialog last closed takes the place of the starting text.
  // Drafts belong to the signed-in user; the composer reads them once as it opens.
  const userId = useContext(AuthStoreContext)?.getState().user?.id ?? 'anonymous';
  const draftKey = composeDraftKey(userId, organizationId, draft);
  // A send of this draft still waiting to be undone goes now, and this composer starts afresh.
  const [flushed] = useState(() => flushPendingSend(draftKey));
  const [restored] = useState(() => takeReopenedCompose(draftKey));
  const [fromId, setFromId] = useState(() => {
    const id = restored?.fromId;
    return id !== undefined && (fromOptions?.some(option => option.id === id) ?? id === mailbox.id)
      ? id
      : mailbox.id;
  });
  const from = fromOptions?.find(option => option.id === fromId) ?? mailbox;
  const send = useSendMailMessage(organizationId, from.id);
  // Replies and forwards quote an existing message, so they are always written by hand.
  const isReplyOrForward = draft.reply || draft.mode === 'reply' || draft.mode === 'forward';
  const templates = useMailTemplates(isReplyOrForward ? 0 : organizationId, 'standard');
  // New messages are also kept by the API as drafts, so they show in the list on every device.
  const saveDraft = useSaveMailDraft(organizationId);
  const removeDraft = useDeleteMailDraft(organizationId).mutateAsync;
  const savedId = () => draft.savedId ?? kept?.savedId;
  /** A sent message is no longer a draft. */
  const forgetSaved = () => {
    const id = savedId();
    if (id !== undefined) void removeDraft(id).catch(() => undefined);
  };
  const [templateId, setTemplateId] = useState<number | null>(null);
  const [values, setValues] = useState<Record<string, string>>({});
  const template = templates.data?.find(candidate => candidate.id === templateId) ?? null;
  const templateHintId = useId();
  const [kept] = useState(() => restored?.draft ?? (flushed ? null : readComposeDraft(draftKey)));
  const [to, setTo] = useState(kept?.to ?? draft.to);
  const [cc, setCc] = useState(kept?.cc ?? draft.cc ?? '');
  const [subject, setSubject] = useState(kept?.subject ?? draft.subject);
  const [body, setBody] = useState(kept?.body ?? draft.body ?? '');
  // A new message starts with the writer's signature for the sending mailbox; choosing another
  // mailbox swaps it while the signature is still the last thing written.
  const startsBlank = !isReplyOrForward && !kept && !draft.body;
  const signature = useMailSignature(organizationId, from.id, startsBlank);
  const [signed, setSigned] = useState<{ mailboxId: number; block: string } | null>(null);
  if (
    startsBlank &&
    signature.data &&
    signature.data.mailbox_id === from.id &&
    signed?.mailboxId !== from.id
  ) {
    const block = mailSignatureBlock(signature.data.body);
    const previous = signed?.block ?? '';
    setSigned({ mailboxId: from.id, block });
    if (body === '' || (previous !== '' && body.endsWith(previous))) {
      setBody(body.slice(0, body.length - previous.length) + block);
    }
  }
  // A body holding only the signature is as untouched as an empty one.
  const startingDraft = useMemo(
    () => (signed?.block ? { ...draft, body: signed.block } : draft),
    [draft, signed]
  );
  // What is written is kept as it changes, so leaving the page any way (the global sidebar, the
  // browser's back button, a reload) keeps the draft as closing the dialog does. Sending clears it.
  const sentRef = useRef(false);
  const [keptFiles] = useState<readonly File[]>(() =>
    restored ? restored.files : flushed ? [] : readComposeDraftFiles(draftKey)
  );
  const [files, setFiles] = useState<File[]>(() => [...keptFiles]);
  // Files named by a draft kept before a reload, which this page no longer holds.
  const [lostFiles, setLostFiles] = useState(() =>
    keptFiles.length > 0 ? [] : (kept?.attachments ?? [])
  );
  // Until they are attached again, the names of lost files stay with the draft as a reminder.
  const attachments = files.length > 0 ? files.map(file => file.name) : lostFiles;
  const written = () => ({
    to,
    cc,
    subject,
    body,
    ...(attachments.length > 0 ? { attachments } : {}),
  });
  useEffect(() => {
    if (sentRef.current) return;
    writeComposeDraft(
      draftKey,
      draftToKeep(
        {
          to,
          cc,
          subject,
          body,
          ...(files.length > 0
            ? { attachments: files.map(file => file.name) }
            : lostFiles.length > 0
              ? { attachments: lostFiles }
              : {}),
        },
        startingDraft
      )
    );
    writeComposeDraftFiles(draftKey, files);
  }, [draftKey, startingDraft, to, cc, subject, body, files, lostFiles]);
  // The forwarded message's files, carried along unless removed; read only when sending.
  const [carried, setCarried] = useState<readonly MailAttachment[]>(
    () => draft.forwarded?.attachments ?? []
  );
  const [errorKey, setErrorKey] = useState<MailboxErrorKey | null>(() =>
    restored?.failure === undefined ? null : resolveMailboxErrorKey(restored.failure)
  );
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
  const filesTooMany = files.length + carried.length > MAIL_MAX_ATTACHMENTS;
  const filesTooLarge =
    files.reduce((total, file) => total + file.size, 0) +
      carried.reduce((total, file) => total + file.size, 0) >
    MAIL_ATTACHMENTS_MAX_BYTES;

  /** Every file to send: the forwarded ones, read from their message, then the chosen ones. */
  const encodeAttachments = async () => {
    const source = draft.forwarded;
    const forwarded = source
      ? await Promise.all(
          carried.map(async attachment => ({
            filename: attachment.filename,
            ...(attachment.content_type ? { content_type: attachment.content_type } : {}),
            content: await fileToBase64(
              await mailboxService.fetchAttachment(
                organizationId,
                source.mailboxId,
                source.messageId,
                attachment
              )
            ),
          }))
        )
      : [];
    const chosen = await Promise.all(
      files.map(async file => ({
        filename: file.name,
        ...(file.type ? { content_type: file.type } : {}),
        content: await fileToBase64(file),
      }))
    );
    return [...forwarded, ...chosen];
  };

  const addFiles = (event: ChangeEvent<HTMLInputElement>) => {
    const chosen = Array.from(event.target.files ?? []);
    // Clear the input so the same file can be chosen again after it is removed.
    event.target.value = '';
    setErrorKey(null);
    setFiles(current => [...current, ...chosen]);
    // Attaching anything settles the reminder; removing files later does not bring it back.
    setLostFiles([]);
  };

  /**
   * A refused send. When the session ended, what is written is kept as this browser's draft, so
   * signing in again and coming back (see `signInHref`) opens it once more.
   */
  const failed = (failure: unknown) => {
    const key = resolveMailboxErrorKey(failure);
    setErrorKey(key);
    if (key === 'errors.sessionEnded') {
      writeComposeDraft(draftKey, draftToKeep(written(), startingDraft));
    }
    if (isStoredSendFailure(failure)) {
      // The parent may close the composer for its sent folder; the toast still tells the writer.
      toast.error(t(key));
      onStored?.();
    }
  };

  /** Sign-in that returns to this page, reopening a new message on its kept draft. */
  const signInHref = () => {
    const back = new URL(window.location.href);
    if ((draft.mode ?? 'compose') === 'compose') {
      back.searchParams.set(COMPOSE_PARAM, COMPOSE_RESUME);
    }
    return withReturnUrl(authConfig.routes.login, `${back.pathname}${back.search}`);
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
      const attachments = await encodeAttachments();
      const stored = await send.mutateAsync({
        to: recipients.to,
        ...(recipients.cc.length > 0 ? { cc: recipients.cc } : {}),
        template: { id: template.id, variables },
        ...(attachments.length > 0 ? { attachments } : {}),
      });
      // At the send limit the message waits; nobody is shown how much of the limit is used.
      toast.success(t(stored.queued_reason === 'send_limit' ? 'messages.queued' : 'messages.sent'));
      sentRef.current = true;
      forgetSaved();
      writeComposeDraft(draftKey, null);
      writeComposeDraftFiles(draftKey, null);
      onStored?.();
      onClose();
    } catch (failure) {
      failed(failure);
    }
  };

  /**
   * Closes the composer and sends after `delayMs` unless Undo is chosen. What is written is held
   * in memory, so Undo or a failure reopens the composer as it was even where the browser keeps
   * no draft; the stored draft stays until the send succeeds, for a reload. `onStored` is not
   * called: by then the page may show another message or composer.
   */
  const sendLater = (input: SendMailMessageInput, delayMs: number, reopen: () => void) => {
    const sender = from.id;
    const mutate = send.mutateAsync;
    const snapshot = { draft: written(), files: [...files] };
    const reopenWith = (state: Omit<ReopenedCompose, 'draft' | 'files'>) => {
      markReopenedCompose(draftKey, { ...snapshot, ...state });
      reopen();
    };
    onClose();
    scheduleUndoableSend(draftKey, {
      delayMs,
      sending: t('compose.sending'),
      undoLabel: t('compose.undo'),
      undo: () => reopenWith({ fromId: sender }),
      run: () => {
        const stored = JSON.stringify(readComposeDraft(draftKey));
        return mutate(input).then(
          result => {
            toast.success(
              t(result.queued_reason === 'send_limit' ? 'messages.queued' : 'messages.sent')
            );
            forgetSaved();
            // A composer opened meanwhile may have kept a draft of its own; that one stays.
            if (JSON.stringify(readComposeDraft(draftKey)) === stored) {
              writeComposeDraft(draftKey, null);
              writeComposeDraftFiles(draftKey, null);
            }
          },
          (failure: unknown) => {
            toast.error(t('compose.notSent'), {
              action: {
                label: t('compose.openDraft'),
                onClick: () => reopenWith({ fromId: sender, failure }),
              },
            });
          }
        );
      },
    });
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
      const attachments = await encodeAttachments();
      const input: SendMailMessageInput = {
        to: normalized.to,
        ...(normalized.cc.length > 0 ? { cc: normalized.cc } : {}),
        subject: normalized.subject,
        text: normalized.text,
        ...(normalized.in_reply_to === '' ? {} : { in_reply_to: normalized.in_reply_to }),
        ...(attachments.length > 0 ? { attachments } : {}),
      };
      const delayMs = undoSendDelayMs();
      if (onReopen && delayMs > 0 && !isReplyOrForward) {
        sendLater(input, delayMs, onReopen);
        return;
      }
      const stored = await send.mutateAsync(input);
      // At the send limit the message waits; nobody is shown how much of the limit is used.
      toast.success(t(stored.queued_reason === 'send_limit' ? 'messages.queued' : 'messages.sent'));
      sentRef.current = true;
      forgetSaved();
      writeComposeDraft(draftKey, null);
      writeComposeDraftFiles(draftKey, null);
      onStored?.();
      onClose();
    } catch (failure) {
      // The dialog stays open so the failure cannot be missed or mistaken for a delivery.
      failed(failure);
    }
  };

  /**
   * Saves a closed new message as a draft of the API too. The browser copy remembers the saved
   * id, so closing again updates that draft rather than adding another one.
   */
  const keepOnServer = (keep: StoredComposeDraft) => {
    saveDraft
      .mutateAsync({
        input: {
          mailbox_id: from.id,
          to: keep.to,
          cc: keep.cc,
          subject: keep.subject,
          body: keep.body,
          in_reply_to: '',
        },
        id: savedId() ?? null,
      })
      .then(
        saved => {
          const current = readComposeDraft(draftKey);
          const textOf = (value: StoredComposeDraft | null) =>
            JSON.stringify({ ...value, savedId: undefined });
          if (textOf(current) === textOf(keep)) {
            writeComposeDraft(draftKey, { ...keep, savedId: saved.id });
          }
        },
        () => toast.error(t('drafts.saveFailed'))
      );
  };

  /**
   * Closing never asks: what was written is kept as this browser's draft and the dialog says so.
   * Nothing written, or only the starting text, keeps no draft.
   */
  const dismiss = () => {
    if (send.isPending) return;
    const found = draftToKeep(written(), startingDraft);
    const known = savedId();
    const keep = found && known !== undefined ? { ...found, savedId: known } : found;
    writeComposeDraft(draftKey, keep);
    writeComposeDraftFiles(draftKey, files);
    if (keep && !isReplyOrForward) keepOnServer(keep);
    if (keep)
      toast.success(t(files.length > 0 ? 'compose.draftSavedWithFiles' : 'compose.draftSaved'));
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
          {lostFiles.length > 0 && files.length === 0 ? (
            <p role="status" className="text-xs text-warning">
              {t('compose.draftFilesLost', { names: lostFiles.join(', ') })}
            </p>
          ) : null}
          {files.length + carried.length > 0 ? (
            <ul className="divide-y rounded-md border text-sm">
              {carried.map(attachment => (
                <li
                  key={`forwarded-${String(attachment.id)}`}
                  className="flex items-center gap-3 px-3 py-1.5"
                >
                  <Paperclip className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
                  <span className="min-w-0 flex-1 truncate" title={attachment.filename}>
                    {attachment.filename}
                  </span>
                  <span className="text-xs whitespace-nowrap text-muted-foreground">
                    {formatBytes(attachment.size)}
                  </span>
                  <Button
                    type="button"
                    size="xs"
                    isIcon
                    variant="ghost"
                    disabled={send.isPending}
                    icon={<X className="size-3.5" />}
                    aria-label={t('compose.removeAttachment', { name: attachment.filename })}
                    onClick={() =>
                      setCarried(current => current.filter(file => file.id !== attachment.id))
                    }
                  />
                </li>
              ))}
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
            <AlertDescription>
              {t(errorKey)}
              {errorKey === 'errors.sessionEnded' ? (
                <>
                  <span className="block">{t('compose.sessionEndedDraft')}</span>
                  <Button asChild size="sm" variant="outline" className="mt-2">
                    <Link href={signInHref()}>{t('compose.signIn')}</Link>
                  </Button>
                </>
              ) : null}
            </AlertDescription>
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
