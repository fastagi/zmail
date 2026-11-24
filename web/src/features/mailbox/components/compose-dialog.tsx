'use client';

import {
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ChangeEvent,
  type FormEvent,
} from 'react';
import { Send } from 'lucide-react';
import { toast } from 'sonner';

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
import { Label } from '@/components/ui/label';
import { Textarea } from '@/components/ui/textarea';
import { AuthStoreContext } from '@/features/auth/store/auth-store';
import {
  ComposeAttachmentsSection,
  encodeComposeAttachments,
} from '@/features/mailbox/components/compose-attachments-section';
import { ComposeContentFields } from '@/features/mailbox/components/compose-content-fields';
import {
  type StoredComposeDraft,
  composeDraftFilesKept,
  composeDraftKey,
  draftToKeep,
  loadComposeDraftFiles,
  readComposeDraft,
  readComposeDraftFiles,
  writeComposeDraft,
  writeComposeDraftFiles,
} from '@/features/mailbox/components/compose-draft-store';
import { type ComposeDraft } from '@/features/mailbox/components/compose-drafts';
import { ComposeErrorAlert } from '@/features/mailbox/components/compose-error-alert';
import { useMailOrganizationId } from '@/features/mailbox/components/mail-organization';
import { nativeSelectClassName } from '@/features/mailbox/components/mail-shared';
import { useSendMailMessage } from '@/features/mailbox/hooks/use-mailbox';
import { useChosenMemberFeatures } from '@/features/mailbox/hooks/use-mail-organization';
import { useMailTemplates } from '@/features/mailbox/hooks/use-mail-templates';
import {
  MAIL_ATTACHMENTS_MAX_BYTES,
  MAIL_MAX_ATTACHMENTS,
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
  // A saved draft this browser still keeps as its new message (with its files) continues there.
  const [draftKey] = useState(() => {
    const own = composeDraftKey(userId, organizationId, draft);
    if (draft.savedId === undefined) return own;
    const fresh = composeDraftKey(userId, organizationId, { ...draft, savedId: undefined });
    return readComposeDraft(fresh)?.savedId === draft.savedId ? fresh : own;
  });
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
  // Writing from a template needs the templates grant; without it the picker stays hidden.
  const canUseTemplates = useChosenMemberFeatures()?.templates ?? false;
  const templates = useMailTemplates(
    isReplyOrForward || !canUseTemplates ? 0 : organizationId,
    'standard'
  );
  // New messages are also kept by the API as drafts, so they show in the list on every device.
  const saveDraft = useSaveMailDraft(organizationId);
  const removeDraft = useDeleteMailDraft(organizationId).mutateAsync;
  /** A sent message is no longer a draft. */
  const forgetSaved = () => {
    const id = knownSavedId;
    if (id !== undefined) void removeDraft(id).catch(() => undefined);
  };
  const [templateId, setTemplateId] = useState<number | null>(null);
  const [values, setValues] = useState<Record<string, string>>({});
  const template = templates.data?.find(candidate => candidate.id === templateId) ?? null;
  const [kept] = useState(() => restored?.draft ?? (flushed ? null : readComposeDraft(draftKey)));
  const knownSavedId = draft.savedId ?? kept?.savedId;
  // A draft opened from the list starts from the API's text, which another device may have changed,
  // unless this browser holds a close the API never took (made offline); this browser's copy still
  // lends it its files.
  const keptText = draft.savedId !== undefined && !restored && !kept?.pending ? null : kept;
  const [to, setTo] = useState(keptText?.to ?? draft.to);
  const [cc, setCc] = useState(keptText?.cc ?? draft.cc ?? '');
  const [subject, setSubject] = useState(keptText?.subject ?? draft.subject);
  const [body, setBody] = useState(keptText?.body ?? draft.body ?? '');
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
    if (body.endsWith(previous)) {
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
  // After a reload the files come back from where this browser kept them; until that read ends,
  // they are neither reported lost nor forgotten.
  const [restoringFiles, setRestoringFiles] = useState(() => lostFiles.length > 0);
  useEffect(() => {
    if (!restoringFiles) return;
    let live = true;
    void loadComposeDraftFiles(draftKey).then(found => {
      if (!live) return;
      setRestoringFiles(false);
      if (found.length === 0) return;
      // Files attached meanwhile already settled the draft.
      setFiles(current => (current.length > 0 ? current : found));
      setLostFiles([]);
    });
    return () => {
      live = false;
    };
  }, [draftKey, restoringFiles]);
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
    const keep = draftToKeep(
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
    );
    const pending = readComposeDraft(draftKey)?.pending;
    writeComposeDraft(
      draftKey,
      keep && {
        ...keep,
        ...(knownSavedId !== undefined && { savedId: knownSavedId }),
        ...(pending && { pending }),
      }
    );
    // Files still being read back are not forgotten in the meantime.
    if (!restoringFiles || files.length > 0) writeComposeDraftFiles(draftKey, files);
  }, [
    draftKey,
    startingDraft,
    to,
    cc,
    subject,
    body,
    files,
    lostFiles,
    knownSavedId,
    restoringFiles,
  ]);
  // The forwarded message's files, carried along unless removed; read only when sending.
  const [carried, setCarried] = useState<readonly MailAttachment[]>(
    () => draft.forwarded?.attachments ?? []
  );
  const [errorKey, setErrorKey] = useState<MailboxErrorKey | null>(() =>
    restored?.failure === undefined ? null : resolveMailboxErrorKey(restored.failure)
  );
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
  const filesTooMany = files.length + carried.length > MAIL_MAX_ATTACHMENTS;
  const filesTooLarge =
    files.reduce((total, file) => total + file.size, 0) +
      carried.reduce((total, file) => total + file.size, 0) >
    MAIL_ATTACHMENTS_MAX_BYTES;

  const encodeAttachments = () =>
    encodeComposeAttachments({
      organizationId,
      forwarded: draft.forwarded,
      carried,
      files,
    });

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
   * signing in again and coming back (see `ComposeErrorAlert`) opens it once more.
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
  const keepOnServer = (keep: StoredComposeDraft, sayKept: () => void) => {
    saveDraft
      .mutateAsync({
        input: {
          mailbox_id: from.id,
          to: keep.to,
          cc: keep.cc,
          subject: keep.subject,
          body: keep.body,
          in_reply_to: draft.inReplyTo,
        },
        id: knownSavedId ?? null,
      })
      .then(
        saved => {
          const current = readComposeDraft(draftKey);
          const textOf = (value: StoredComposeDraft | null) =>
            JSON.stringify({ ...value, savedId: undefined, pending: undefined });
          if (textOf(current) === textOf(keep)) {
            writeComposeDraft(draftKey, { ...keep, savedId: saved.id });
          }
          sayKept();
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
    const known = knownSavedId;
    const keep = found && known !== undefined ? { ...found, savedId: known } : found;
    writeComposeDraft(draftKey, keep && !isReplyOrForward ? { ...keep, pending: true } : keep);
    const blank =
      files.length === 0 &&
      lostFiles.length === 0 &&
      [to, cc, subject, body].every(value => value.trim() === '');
    // Clearing a saved draft and closing it discards it everywhere.
    if (blank && !isReplyOrForward) forgetSaved();
    // Files still being read back after a reload stay where this browser kept them.
    if (!restoringFiles || files.length > 0) writeComposeDraftFiles(draftKey, files);
    // The toast says whether attached files outlive this page: the API keeps none of them.
    const filesKept = files.length > 0 ? composeDraftFilesKept(draftKey) : null;
    const sayKept = () => {
      if (!filesKept) {
        toast.success(t('compose.draftSaved'));
        return;
      }
      void filesKept.then(stored =>
        toast.success(
          t(stored ? 'compose.draftSavedWithStoredFiles' : 'compose.draftSavedWithFiles')
        )
      );
    };
    // A new message says it is saved once the API has it; a refusal says it stays in this browser.
    if (keep && !isReplyOrForward) keepOnServer(keep, sayKept);
    else if (keep) sayKept();
    onClose();
  };

  const titleKey =
    draft.mode === 'forward'
      ? 'compose.forwardTitle'
      : draft.reply
        ? 'compose.replyTitle'
        : 'compose.title';

  const form = (
    // In the dialog the body scrolls and Send stays in view, even on a phone.
    <form onSubmit={submit} className="flex min-h-0 flex-1 flex-col">
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
        <ComposeContentFields
          isReplyOrForward={isReplyOrForward}
          templates={templates.data}
          templateId={templateId}
          template={template}
          values={values}
          subject={subject}
          body={body}
          disabled={send.isPending}
          onSelectTemplate={nextTemplateId => {
            setErrorKey(null);
            setValues({});
            setTemplateId(nextTemplateId);
          }}
          onChangeValue={(name, value) => setValues(current => ({ ...current, [name]: value }))}
          onChangeSubject={setSubject}
          onChangeBody={setBody}
        />
        <ComposeAttachmentsSection
          files={files}
          carried={carried}
          lostFiles={lostFiles}
          restoringFiles={restoringFiles}
          filesTooMany={filesTooMany}
          filesTooLarge={filesTooLarge}
          disabled={send.isPending}
          onAddFiles={addFiles}
          onRemoveCarried={attachmentId =>
            setCarried(current => current.filter(file => file.id !== attachmentId))
          }
          onRemoveFile={index =>
            setFiles(current => current.filter((_, position) => position !== index))
          }
        />
        {errorKey ? <ComposeErrorAlert errorKey={errorKey} mode={draft.mode} /> : null}
      </DialogBody>
      {/* Inline, the page scrolls the form; Send stays pinned to its bottom, even on a phone. */}
      <DialogFooter
        className={cn(
          'mt-4',
          inline && 'sticky bottom-0 z-10 -mx-4 border-t bg-background px-4 py-3 sm:-mx-8 sm:px-8'
        )}
      >
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
        className="mx-auto h-full w-full max-w-3xl space-y-5 overflow-y-auto px-4 pt-5 sm:px-8"
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
        closeLabel={t('actions.close')}
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
