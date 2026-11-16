'use client';

import { useState, type FormEvent } from 'react';
import { AlertCircle, ArrowLeft, Inbox, Mail, PenLine, Plus, Send, Trash2 } from 'lucide-react';
import { toast } from 'sonner';

import { Alert, AlertDescription } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Skeleton } from '@/components/ui/skeleton';
import {
  ComposeDialog,
  emptyDraft,
  replyDraft,
  type ComposeDraft,
} from '@/features/mailbox/components/compose-dialog';
import {
  MailOrganizationBoundary,
  MailPageHeader,
  OrganizationLostState,
  useMailOrganizationId,
} from '@/features/mailbox/components/mail-organization';
import {
  EmptyState,
  ErrorAlert,
  MailHtmlFrame,
  MailStatusBadge,
  mailBadgeKey,
  useDateTimeFormat,
} from '@/features/mailbox/components/mail-shared';
import { MessageActions } from '@/features/mailbox/components/message-actions';
import {
  useCreateMailbox,
  useDeleteMailbox,
  useMailMessage,
  useMailMessages,
  useMailboxDomains,
  useMailboxes,
  useUpdateMailMessage,
} from '@/features/mailbox/hooks/use-mailbox';
import { useMailOrganization } from '@/features/mailbox/hooks/use-mail-organization';
import {
  MAILBOX_DISPLAY_NAME_MAX_LENGTH,
  MAILBOX_LOCAL_PART_MAX_LENGTH,
  normalizeDisplayName,
  normalizeLocalPart,
} from '@/features/mailbox/schemas';
import type {
  MailDirection,
  MailMessage,
  MailMessageSummary,
  Mailbox,
  MailboxDomain,
} from '@/features/mailbox/types';
import { isOrganizationLost, resolveMailboxErrorKey } from '@/features/mailbox/utils/mailbox-error';
import { useT } from '@/i18n';
import { cn } from '@/utils';

/**
 * Mailbox console: the addresses of the chosen organization on the configured domain, with the
 * inbox and sent folder of the selected one. Every member reads and sends; only owners and admins
 * see the controls that create and delete mailboxes. The API remains the authority for
 * membership, roles, address rules, and delivery; the checks here only save a round trip.
 */
export function MailboxWorkspace() {
  const t = useT('mailbox');
  const scope = useMailOrganization();
  return (
    <div className="space-y-5">
      <MailPageHeader title={t('title')} description={t('description')} scope={scope} />
      <MailOrganizationBoundary scope={scope}>
        {organization => <Workspace canManage={organization.canManage} />}
      </MailOrganizationBoundary>
    </div>
  );
}

function Workspace({ canManage }: { canManage: boolean }) {
  const t = useT('mailbox');
  const organizationId = useMailOrganizationId();
  const domains = useMailboxDomains(organizationId);
  const mailboxes = useMailboxes(organizationId);
  const [selectedId, setSelectedId] = useState<number | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<Mailbox | null>(null);

  if (mailboxes.isPending || domains.isPending) return <WorkspaceSkeleton />;
  const loadError = mailboxes.error ?? domains.error;
  if (isOrganizationLost(loadError)) return <OrganizationLostState />;
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
            description={t(canManage ? 'mailboxes.empty' : 'organization.readOnly')}
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
                {canManage ? (
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
                ) : null}
              </li>
            ))}
          </ul>
        )}
        {!canManage ? (
          items.length > 0 ? (
            <p className="text-xs text-muted-foreground">{t('organization.readOnly')}</p>
          ) : null
        ) : domain ? (
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

      {canManage ? (
        <DeleteMailboxDialog mailbox={deleteTarget} onClose={() => setDeleteTarget(null)} />
      ) : null}
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
  const create = useCreateMailbox(useMailOrganizationId());
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
  const remove = useDeleteMailbox(useMailOrganizationId());

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
  const update = useUpdateMailMessage(useMailOrganizationId(), mailbox.id);

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
    setDraft(replyDraft(message, subject => t('compose.replySubject', { subject })));
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
  const messages = useMailMessages(useMailOrganizationId(), mailbox.id, direction, page);

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
                    ) : message.last_event !== '' ? (
                      <MailStatusBadge message={message} label={t(mailBadgeKey(message))} />
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
  const query = useMailMessage(useMailOrganizationId(), mailboxId, messageId);
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
  const hasHtml = message.html_body !== '';
  const hasText = message.text_body !== '';
  const showHtml = hasHtml && !(plainText && hasText);

  return (
    <article className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        {back}
        <MessageActions message={message} onReply={onReply} onClose={onClose} />
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
          <MailHtmlFrame html={message.html_body} title={t('message.htmlTitle')} />
        ) : hasText ? (
          <pre className="font-sans text-sm break-words whitespace-pre-wrap">
            {message.text_body}
          </pre>
        ) : (
          <p className="text-sm text-muted-foreground">{t('message.emptyBody')}</p>
        )}
      </div>
    </article>
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
