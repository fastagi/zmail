'use client';

import { useMemo, useState, type FormEvent, type ReactNode } from 'react';
import { useLocale } from 'next-intl';
import {
  AlertCircle,
  ArrowLeft,
  Inbox,
  Mail,
  MailOpen,
  PenLine,
  Plus,
  RefreshCw,
  Reply,
  Send,
  Trash2,
} from 'lucide-react';
import { toast } from 'sonner';

import { Alert, AlertDescription } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
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
import { Skeleton } from '@/components/ui/skeleton';
import { Textarea } from '@/components/ui/textarea';
import {
  useCreateMailbox,
  useDeleteMailMessage,
  useDeleteMailbox,
  useMailMessage,
  useMailMessages,
  useMailboxDomains,
  useMailboxes,
  useSendMailMessage,
  useUpdateMailMessage,
} from '@/features/mailbox/hooks/use-mailbox';
import {
  MAILBOX_DISPLAY_NAME_MAX_LENGTH,
  MAILBOX_LOCAL_PART_MAX_LENGTH,
  MAIL_SUBJECT_MAX_LENGTH,
  normalizeDisplayName,
  normalizeLocalPart,
  normalizeMailMessage,
  splitRecipients,
} from '@/features/mailbox/schemas';
import type {
  MailDirection,
  MailMessage,
  MailMessageSummary,
  Mailbox,
  MailboxDomain,
} from '@/features/mailbox/types';
import {
  isStoredSendFailure,
  resolveMailboxErrorKey,
  type MailboxErrorKey,
} from '@/features/mailbox/utils/mailbox-error';
import { useT } from '@/i18n';
import { cn } from '@/utils';

interface ComposeDraft {
  to: string;
  subject: string;
  inReplyTo: string;
  reply: boolean;
}

const emptyDraft: ComposeDraft = { to: '', subject: '', inReplyTo: '', reply: false };

/**
 * Blocks every network load inside the message frame. The frame's empty `sandbox` already stops
 * scripts and gives the content an opaque origin; this also stops remote images and stylesheets,
 * so opening a message tells the sender nothing.
 */
const frameContentPolicy =
  '<meta http-equiv="Content-Security-Policy" content="default-src \'none\'; img-src data:; style-src \'unsafe-inline\'">';

/**
 * Mailbox console: the signed-in user's addresses on the configured domain, with the inbox and
 * sent folder of the selected one. The API remains the authority for ownership, address rules,
 * and delivery; the checks here only save a round trip.
 */
export function MailboxWorkspace() {
  const t = useT('mailbox');
  const domains = useMailboxDomains();
  const mailboxes = useMailboxes();
  const [selectedId, setSelectedId] = useState<number | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<Mailbox | null>(null);

  if (mailboxes.isPending || domains.isPending) return <WorkspaceSkeleton />;
  const loadError = mailboxes.error ?? domains.error;
  if (loadError || !mailboxes.data || !domains.data) {
    return (
      <ErrorAlert
        message={t(resolveMailboxErrorKey(loadError))}
        retryLabel={t('actions.retry')}
        onRetry={() => {
          void mailboxes.refetch();
          void domains.refetch();
        }}
      />
    );
  }

  const items = mailboxes.data;
  const domain = domains.data[0] ?? null;
  const selected = items.find(item => item.id === selectedId) ?? items[0] ?? null;

  return (
    <div className="grid gap-6 lg:grid-cols-[20rem_minmax(0,1fr)]">
      <section className="space-y-4" aria-labelledby="mailbox-list-title">
        <div className="flex items-baseline justify-between gap-3">
          <h2 id="mailbox-list-title" className="text-sm font-semibold">
            {t('mailboxes.title')}
          </h2>
          {domain ? (
            <span className="text-xs text-muted-foreground">
              {t('mailboxes.count', { count: items.length, max: domain.max_mailboxes })}
            </span>
          ) : null}
        </div>
        {items.length === 0 ? (
          <EmptyState
            icon={<Mail className="size-5" aria-hidden="true" />}
            title={t('mailboxes.emptyTitle')}
            description={t('mailboxes.empty')}
          />
        ) : (
          <ul className="space-y-1">
            {items.map(mailbox => (
              <li key={mailbox.id} className="flex items-center gap-1">
                <button
                  type="button"
                  aria-current={mailbox.id === selected?.id ? 'true' : undefined}
                  className={cn(
                    'min-w-0 flex-1 rounded-md border px-3 py-2 text-left focus-ring hover:bg-accent',
                    mailbox.id === selected?.id ? 'border-primary bg-accent' : 'border-transparent'
                  )}
                  onClick={() => setSelectedId(mailbox.id)}
                >
                  <span className="flex items-center gap-2">
                    <span className="min-w-0 flex-1 truncate text-sm font-medium">
                      {mailbox.address}
                    </span>
                    {mailbox.unread_count > 0 ? (
                      <span
                        className="shrink-0 rounded-full bg-primary px-2 py-0.5 text-xs font-medium text-primary-foreground"
                        aria-label={t('mailboxes.unreadCount', { count: mailbox.unread_count })}
                      >
                        {mailbox.unread_count > 99 ? '99+' : mailbox.unread_count}
                      </span>
                    ) : null}
                  </span>
                  <span className="block truncate text-xs text-muted-foreground">
                    {mailbox.display_name || t('mailboxes.noDisplayName')}
                  </span>
                </button>
                <Button
                  type="button"
                  isIcon
                  size="sm"
                  variant="ghost"
                  aria-label={t('deleteMailbox.label', { address: mailbox.address })}
                  title={t('deleteMailbox.label', { address: mailbox.address })}
                  onClick={() => setDeleteTarget(mailbox)}
                >
                  <Trash2 className="size-4" />
                </Button>
              </li>
            ))}
          </ul>
        )}
        {domain ? (
          <CreateMailboxForm
            domain={domain}
            count={items.length}
            onCreated={mailbox => setSelectedId(mailbox.id)}
          />
        ) : null}
      </section>

      <section className="min-w-0">
        {selected ? <MailboxMessages key={selected.id} mailbox={selected} /> : null}
      </section>

      <DeleteMailboxDialog mailbox={deleteTarget} onClose={() => setDeleteTarget(null)} />
    </div>
  );
}

function CreateMailboxForm({
  domain,
  count,
  onCreated,
}: {
  domain: MailboxDomain;
  count: number;
  onCreated: (mailbox: Mailbox) => void;
}) {
  const t = useT('mailbox');
  const create = useCreateMailbox();
  const [localPart, setLocalPart] = useState('');
  const [displayName, setDisplayName] = useState('');
  const [error, setError] = useState<string | null>(null);
  const limitReached = count >= domain.max_mailboxes;

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setError(null);
    const normalized = normalizeLocalPart(localPart);
    if (!normalized.ok) {
      setError(
        t(normalized.reason === 'reserved' ? 'errors.addressReserved' : 'errors.addressInvalid')
      );
      return;
    }
    const name = normalizeDisplayName(displayName);
    if (name === null) {
      setError(t('create.displayNameInvalid'));
      return;
    }
    try {
      const mailbox = await create.mutateAsync({
        local_part: normalized.value,
        ...(name === '' ? {} : { display_name: name }),
      });
      setLocalPart('');
      setDisplayName('');
      toast.success(t('messages.mailboxCreated'));
      onCreated(mailbox);
    } catch (failure) {
      setError(t(resolveMailboxErrorKey(failure)));
    }
  };

  return (
    <form className="space-y-3 rounded-md border p-4" onSubmit={submit}>
      <h3 className="text-sm font-semibold">{t('create.title')}</h3>
      {limitReached ? (
        <p className="text-sm text-muted-foreground">
          {t('create.limitReached', { max: domain.max_mailboxes })}
        </p>
      ) : (
        <>
          <div className="space-y-2">
            <Label htmlFor="mailbox-local-part">{t('create.localPart')}</Label>
            <div className="flex items-center gap-2">
              <Input
                id="mailbox-local-part"
                value={localPart}
                maxLength={MAILBOX_LOCAL_PART_MAX_LENGTH}
                autoComplete="off"
                autoCapitalize="none"
                spellCheck={false}
                disabled={create.isPending}
                placeholder={t('create.localPartPlaceholder')}
                aria-describedby="mailbox-local-part-hint"
                onChange={event => setLocalPart(event.target.value)}
              />
              <span className="shrink-0 text-sm text-muted-foreground">@{domain.domain}</span>
            </div>
            <p id="mailbox-local-part-hint" className="text-xs text-muted-foreground">
              {t('create.localPartHint')}
            </p>
          </div>
          <div className="space-y-2">
            <Label htmlFor="mailbox-display-name">{t('create.displayName')}</Label>
            <Input
              id="mailbox-display-name"
              value={displayName}
              maxLength={MAILBOX_DISPLAY_NAME_MAX_LENGTH}
              autoComplete="off"
              disabled={create.isPending}
              placeholder={t('create.displayNamePlaceholder')}
              onChange={event => setDisplayName(event.target.value)}
            />
          </div>
          {error ? (
            <p role="alert" className="text-sm text-error">
              {error}
            </p>
          ) : null}
          <Button
            type="submit"
            size="sm"
            loading={create.isPending}
            icon={<Plus className="size-4" />}
          >
            {t('actions.create')}
          </Button>
        </>
      )}
    </form>
  );
}

function DeleteMailboxDialog({
  mailbox,
  onClose,
}: {
  mailbox: Mailbox | null;
  onClose: () => void;
}) {
  const t = useT('mailbox');
  const remove = useDeleteMailbox();

  const confirm = async () => {
    if (!mailbox) return;
    try {
      await remove.mutateAsync(mailbox.id);
      toast.success(t('messages.mailboxDeleted'));
      onClose();
    } catch (failure) {
      toast.error(t(resolveMailboxErrorKey(failure)));
    }
  };

  return (
    <Dialog open={mailbox !== null} onOpenChange={open => !open && !remove.isPending && onClose()}>
      <DialogContent size="sm">
        <DialogHeader>
          <DialogTitle>{t('deleteMailbox.title')}</DialogTitle>
          <DialogDescription>
            {t('deleteMailbox.description', { address: mailbox?.address ?? '' })}
          </DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <Button type="button" variant="outline" disabled={remove.isPending} onClick={onClose}>
            {t('actions.cancel')}
          </Button>
          <Button
            type="button"
            variant="destructive"
            loading={remove.isPending}
            icon={<Trash2 className="size-4" />}
            onClick={() => void confirm()}
          >
            {t('actions.delete')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function MailboxMessages({ mailbox }: { mailbox: Mailbox }) {
  const t = useT('mailbox');
  const [direction, setDirection] = useState<MailDirection>('inbound');
  const [page, setPage] = useState(1);
  const [openId, setOpenId] = useState<number | null>(null);
  const [draft, setDraft] = useState<ComposeDraft | null>(null);
  const update = useUpdateMailMessage(mailbox.id);

  const showFolder = (next: MailDirection) => {
    setDirection(next);
    setPage(1);
    setOpenId(null);
  };

  const open = (message: MailMessageSummary) => {
    setOpenId(message.id);
    // Opening an unread inbound message is what marks it read.
    if (message.direction === 'inbound' && !message.read) {
      update.mutate({ messageId: message.id, read: true });
    }
  };

  const reply = (message: MailMessage) => {
    const subject = /^re:/iu.test(message.subject)
      ? message.subject
      : t('compose.replySubject', { subject: message.subject });
    setDraft({
      to: message.from.address,
      subject,
      inReplyTo: message.message_id,
      reply: true,
    });
  };

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="min-w-0">
          <h2 className="truncate text-base font-semibold">{mailbox.address}</h2>
          {mailbox.display_name ? (
            <p className="truncate text-sm text-muted-foreground">{mailbox.display_name}</p>
          ) : null}
        </div>
        <Button
          type="button"
          size="sm"
          icon={<PenLine className="size-4" />}
          onClick={() => setDraft(emptyDraft)}
        >
          {t('actions.compose')}
        </Button>
      </div>

      <div role="group" aria-label={t('folders.label')} className="flex gap-2">
        <Button
          type="button"
          size="sm"
          variant={direction === 'inbound' ? 'default' : 'outline'}
          aria-pressed={direction === 'inbound'}
          icon={<Inbox className="size-4" />}
          onClick={() => showFolder('inbound')}
        >
          {t('folders.inbound')}
        </Button>
        <Button
          type="button"
          size="sm"
          variant={direction === 'outbound' ? 'default' : 'outline'}
          aria-pressed={direction === 'outbound'}
          icon={<Send className="size-4" />}
          onClick={() => showFolder('outbound')}
        >
          {t('folders.outbound')}
        </Button>
      </div>

      {openId === null ? (
        <MessageList
          mailbox={mailbox}
          direction={direction}
          page={page}
          onPageChange={setPage}
          onOpen={open}
        />
      ) : (
        <MessageDetail
          mailboxId={mailbox.id}
          messageId={openId}
          onClose={() => setOpenId(null)}
          onReply={reply}
        />
      )}

      {draft ? (
        <ComposeDialog
          mailbox={mailbox}
          draft={draft}
          onClose={() => setDraft(null)}
          onStored={() => showFolder('outbound')}
        />
      ) : null}
    </div>
  );
}

function MessageList({
  mailbox,
  direction,
  page,
  onPageChange,
  onOpen,
}: {
  mailbox: Mailbox;
  direction: MailDirection;
  page: number;
  onPageChange: (page: number) => void;
  onOpen: (message: MailMessageSummary) => void;
}) {
  const t = useT('mailbox');
  const formatter = useDateTimeFormat();
  const messages = useMailMessages(mailbox.id, direction, page);

  if (messages.isPending) return <ListSkeleton />;
  if (messages.error || !messages.data) {
    return (
      <ErrorAlert
        message={t(resolveMailboxErrorKey(messages.error))}
        retryLabel={t('actions.retry')}
        onRetry={() => void messages.refetch()}
      />
    );
  }
  const { items, meta } = messages.data;
  if (items.length === 0) {
    return direction === 'inbound' ? (
      <EmptyState
        icon={<Inbox className="size-5" aria-hidden="true" />}
        title={t('list.emptyInboxTitle')}
        description={t('list.emptyInbox', { address: mailbox.address })}
      />
    ) : (
      <EmptyState
        icon={<Send className="size-5" aria-hidden="true" />}
        title={t('list.emptySentTitle')}
        description={t('list.emptySent', { address: mailbox.address })}
      />
    );
  }

  return (
    <div className="space-y-3">
      <ul className="divide-y rounded-md border">
        {items.map(message => {
          const unread = message.direction === 'inbound' && !message.read;
          return (
            <li key={message.id}>
              <button
                type="button"
                className="flex w-full items-start gap-3 px-4 py-3 text-left focus-ring hover:bg-accent"
                onClick={() => onOpen(message)}
              >
                <span
                  className={cn(
                    'mt-1.5 size-2 shrink-0 rounded-full',
                    unread ? 'bg-primary' : 'bg-transparent'
                  )}
                  aria-hidden="true"
                />
                <span className="min-w-0 flex-1">
                  <span className="flex items-center gap-2">
                    <span className={cn('truncate text-sm', unread && 'font-semibold')}>
                      {message.direction === 'inbound'
                        ? message.from.name || message.from.address
                        : t('list.to', { recipients: message.to.join(', ') })}
                    </span>
                    {unread ? <span className="sr-only">{t('list.unread')}</span> : null}
                    {message.status === 'failed' ? (
                      <Badge variant="destructive">{t('list.failed')}</Badge>
                    ) : null}
                    {message.status === 'failed' && message.failure_code ? (
                      <code className="truncate text-xs text-muted-foreground">
                        {message.failure_code}
                      </code>
                    ) : null}
                  </span>
                  <span
                    className={cn(
                      'block truncate text-sm',
                      unread ? 'font-medium' : 'text-muted-foreground'
                    )}
                  >
                    {message.subject || t('list.noSubject')}
                  </span>
                </span>
                <time
                  dateTime={message.created_at}
                  className="shrink-0 text-xs text-muted-foreground"
                >
                  {formatter.format(new Date(message.created_at))}
                </time>
              </button>
            </li>
          );
        })}
      </ul>
      {meta.last_page > 1 ? (
        <div className="flex items-center justify-between gap-3">
          <Button
            type="button"
            size="sm"
            variant="outline"
            disabled={meta.current_page <= 1}
            onClick={() => onPageChange(meta.current_page - 1)}
          >
            {t('list.previous')}
          </Button>
          <span className="text-sm text-muted-foreground">
            {t('list.page', { page: meta.current_page, total: meta.last_page })}
          </span>
          <Button
            type="button"
            size="sm"
            variant="outline"
            disabled={meta.current_page >= meta.last_page}
            onClick={() => onPageChange(meta.current_page + 1)}
          >
            {t('list.next')}
          </Button>
        </div>
      ) : null}
    </div>
  );
}

function MessageDetail({
  mailboxId,
  messageId,
  onClose,
  onReply,
}: {
  mailboxId: number;
  messageId: number;
  onClose: () => void;
  onReply: (message: MailMessage) => void;
}) {
  const t = useT('mailbox');
  const formatter = useDateTimeFormat();
  const query = useMailMessage(mailboxId, messageId);
  const update = useUpdateMailMessage(mailboxId);
  const remove = useDeleteMailMessage(mailboxId);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [plainText, setPlainText] = useState(false);

  const back = (
    <Button
      type="button"
      size="sm"
      variant="ghost"
      icon={<ArrowLeft className="size-4" />}
      onClick={onClose}
    >
      {t('actions.back')}
    </Button>
  );

  if (query.isPending) {
    return (
      <div className="space-y-3">
        {back}
        <Skeleton className="h-48 w-full" />
      </div>
    );
  }
  if (query.error || !query.data) {
    return (
      <div className="space-y-3">
        {back}
        <ErrorAlert
          message={t(resolveMailboxErrorKey(query.error))}
          retryLabel={t('actions.retry')}
          onRetry={() => void query.refetch()}
        />
      </div>
    );
  }

  const message = query.data;
  const inbound = message.direction === 'inbound';
  const hasHtml = message.html_body !== '';
  const hasText = message.text_body !== '';
  const showHtml = hasHtml && !(plainText && hasText);

  const markUnread = async () => {
    try {
      await update.mutateAsync({ messageId: message.id, read: false });
      onClose();
    } catch (failure) {
      toast.error(t(resolveMailboxErrorKey(failure)));
    }
  };

  const confirmRemove = async () => {
    try {
      await remove.mutateAsync(message.id);
      toast.success(t('messages.messageDeleted'));
      onClose();
    } catch (failure) {
      toast.error(t(resolveMailboxErrorKey(failure)));
    }
  };

  return (
    <article className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        {back}
        <div className="flex flex-wrap gap-2">
          {inbound ? (
            <>
              <Button
                type="button"
                size="sm"
                variant="outline"
                icon={<Reply className="size-4" />}
                onClick={() => onReply(message)}
              >
                {t('actions.reply')}
              </Button>
              <Button
                type="button"
                size="sm"
                variant="outline"
                loading={update.isPending}
                icon={<MailOpen className="size-4" />}
                onClick={() => void markUnread()}
              >
                {t('actions.markUnread')}
              </Button>
            </>
          ) : null}
          <Button
            type="button"
            size="sm"
            variant="outline"
            icon={<Trash2 className="size-4" />}
            onClick={() => setConfirmDelete(true)}
          >
            {t('actions.delete')}
          </Button>
        </div>
      </div>

      <div className="space-y-3 rounded-md border p-4">
        <h3 className="text-lg font-semibold break-words">
          {message.subject || t('list.noSubject')}
        </h3>
        {message.status === 'failed' ? (
          <Alert variant="destructive">
            <AlertCircle className="size-4" aria-hidden="true" />
            <AlertDescription>
              {message.failure_code
                ? t('message.failed', { code: message.failure_code })
                : t('message.failedUnknown')}
            </AlertDescription>
          </Alert>
        ) : null}
        <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1 text-sm">
          <dt className="text-muted-foreground">{t('message.from')}</dt>
          <dd className="break-words">
            {message.from.name
              ? `${message.from.name} <${message.from.address}>`
              : message.from.address}
          </dd>
          <dt className="text-muted-foreground">{t('message.to')}</dt>
          <dd className="break-words">{message.to.join(', ')}</dd>
          {message.cc.length > 0 ? (
            <>
              <dt className="text-muted-foreground">{t('message.cc')}</dt>
              <dd className="break-words">{message.cc.join(', ')}</dd>
            </>
          ) : null}
          <dt className="text-muted-foreground">{t('message.date')}</dt>
          <dd>
            <time dateTime={message.created_at}>
              {formatter.format(new Date(message.created_at))}
            </time>
          </dd>
        </dl>

        {hasHtml && hasText ? (
          <div role="group" aria-label={t('message.viewLabel')} className="flex gap-2">
            <Button
              type="button"
              size="xs"
              variant={showHtml ? 'default' : 'outline'}
              aria-pressed={showHtml}
              onClick={() => setPlainText(false)}
            >
              {t('message.viewHtml')}
            </Button>
            <Button
              type="button"
              size="xs"
              variant={showHtml ? 'outline' : 'default'}
              aria-pressed={!showHtml}
              onClick={() => setPlainText(true)}
            >
              {t('message.viewText')}
            </Button>
          </div>
        ) : null}

        {showHtml ? (
          // Sender HTML is untrusted. The empty sandbox runs no script and gives the content an
          // opaque origin, so it can never act as this application or read its cookies.
          <iframe
            sandbox=""
            srcDoc={frameContentPolicy + message.html_body}
            title={t('message.htmlTitle')}
            referrerPolicy="no-referrer"
            className="h-96 w-full rounded-md border bg-white"
          />
        ) : hasText ? (
          <pre className="font-sans text-sm break-words whitespace-pre-wrap">
            {message.text_body}
          </pre>
        ) : (
          <p className="text-sm text-muted-foreground">{t('message.emptyBody')}</p>
        )}
      </div>

      <Dialog
        open={confirmDelete}
        onOpenChange={open => !open && !remove.isPending && setConfirmDelete(false)}
      >
        <DialogContent size="sm">
          <DialogHeader>
            <DialogTitle>{t('deleteMessage.title')}</DialogTitle>
            <DialogDescription>
              {t('deleteMessage.description', {
                subject: message.subject || t('list.noSubject'),
              })}
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              disabled={remove.isPending}
              onClick={() => setConfirmDelete(false)}
            >
              {t('actions.cancel')}
            </Button>
            <Button
              type="button"
              variant="destructive"
              loading={remove.isPending}
              icon={<Trash2 className="size-4" />}
              onClick={() => void confirmRemove()}
            >
              {t('actions.delete')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </article>
  );
}

function ComposeDialog({
  mailbox,
  draft,
  onClose,
  onStored,
}: {
  mailbox: Mailbox;
  draft: ComposeDraft;
  onClose: () => void;
  /** The sent folder has a new entry: a delivered message, or one stored as failed. */
  onStored: () => void;
}) {
  const t = useT('mailbox');
  const send = useSendMailMessage(mailbox.id);
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
            {t('compose.description', { address: mailbox.address })}
          </DialogDescription>
        </DialogHeader>
        <form onSubmit={submit}>
          <DialogBody className="space-y-4">
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

function ErrorAlert({
  message,
  retryLabel,
  onRetry,
}: {
  message: string;
  retryLabel: string;
  onRetry: () => void;
}) {
  return (
    <Alert variant="destructive">
      <AlertCircle className="size-4" aria-hidden="true" />
      <AlertDescription className="flex flex-wrap items-center justify-between gap-3">
        <span>{message}</span>
        <Button
          type="button"
          size="sm"
          variant="outline"
          icon={<RefreshCw className="size-3.5" />}
          onClick={onRetry}
        >
          {retryLabel}
        </Button>
      </AlertDescription>
    </Alert>
  );
}

function EmptyState({
  icon,
  title,
  description,
}: {
  icon: ReactNode;
  title: string;
  description: string;
}) {
  return (
    <div className="flex flex-col items-center rounded-md border border-dashed px-6 py-10 text-center">
      <div className="flex size-10 items-center justify-center rounded-full bg-muted text-muted-foreground">
        {icon}
      </div>
      <p className="mt-3 text-sm font-medium">{title}</p>
      <p className="mt-1 max-w-sm text-sm text-muted-foreground">{description}</p>
    </div>
  );
}

function ListSkeleton() {
  return (
    <div className="space-y-2" aria-hidden="true">
      <Skeleton className="h-14 w-full" />
      <Skeleton className="h-14 w-full" />
      <Skeleton className="h-14 w-full" />
    </div>
  );
}

function WorkspaceSkeleton() {
  return (
    <div className="grid gap-6 lg:grid-cols-[20rem_minmax(0,1fr)]" aria-hidden="true">
      <ListSkeleton />
      <Skeleton className="h-48 w-full" />
    </div>
  );
}

function useDateTimeFormat(): Intl.DateTimeFormat {
  const locale = useLocale();
  return useMemo(
    () => new Intl.DateTimeFormat(locale, { dateStyle: 'medium', timeStyle: 'short' }),
    [locale]
  );
}
