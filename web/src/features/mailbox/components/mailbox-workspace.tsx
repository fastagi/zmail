'use client';

import { useState } from 'react';
import {
  AlertCircle,
  ArrowLeft,
  ChevronLeft,
  ChevronRight,
  Inbox,
  Lock,
  Mail,
  PenLine,
  Send,
  ShieldCheck,
  Trash2,
  User,
  UserX,
  Users,
} from 'lucide-react';
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
import { Skeleton } from '@/components/ui/skeleton';
import {
  ComposeDialog,
  emptyDraft,
  forwardDraft,
  quotedReplyDraft,
  replyAllDraft,
  type ComposeDraft,
} from '@/features/mailbox/components/compose-dialog';
import { CreateMailboxForm } from '@/features/mailbox/components/create-mailbox-form';
import {
  hasAccessPanel,
  MailboxAccessPanel,
} from '@/features/mailbox/components/mailbox-access-panel';
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
import {
  AttachmentIndicator,
  MailAttachmentList,
} from '@/features/mailbox/components/mail-attachments';
import { MessageActions } from '@/features/mailbox/components/message-actions';
import {
  useDeleteMailbox,
  useMailMessage,
  useMailMessages,
  useMailboxDomains,
  useMailboxes,
  useUpdateMailMessage,
} from '@/features/mailbox/hooks/use-mailbox';
import { useMailOrganization } from '@/features/mailbox/hooks/use-mail-organization';
import type {
  MailDirection,
  MailMessage,
  MailMessageSummary,
  Mailbox,
} from '@/features/mailbox/types';
import { isOrganizationLost, resolveMailboxErrorKey } from '@/features/mailbox/utils/mailbox-error';
import { useT } from '@/i18n';
import { cn } from '@/utils';

/**
 * Mailbox console: the mailboxes of the chosen organization the user has any access to, with the
 * inbox and sent folder of the selected one. What a user may do follows each mailbox's `access`:
 * without `view` the mailbox is locked, without `send` there is no compose, reply, or delete.
 * Only owners and admins create mailboxes. The API remains the authority for membership, access,
 * address rules, and delivery; the checks here only save a round trip.
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
  const [accessTargetId, setAccessTargetId] = useState<number | null>(null);

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
  const domain = domains.data.find(item => item.kind === 'shared') ?? domains.data[0] ?? null;
  const selected = items.find(item => item.id === selectedId) ?? items[0] ?? null;
  // Looked up on every render, so the panel follows the list after a change.
  const accessTarget = items.find(item => item.id === accessTargetId) ?? null;

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
          <ul className="space-y-1" aria-labelledby="mailbox-list-title">
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
                    <MailboxMarks mailbox={mailbox} />
                    {mailbox.access.view && mailbox.unread_count > 0 ? (
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
                {mailbox.access.administer ? (
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
            domains={domains.data}
            count={items.length}
            onCreated={mailbox => setSelectedId(mailbox.id)}
          />
        ) : null}
      </section>

      <section className="min-w-0">
        {selected ? (
          <MailboxMessages
            key={selected.id}
            mailbox={selected}
            onOpenAccess={() => setAccessTargetId(selected.id)}
          />
        ) : null}
      </section>

      <DeleteMailboxDialog mailbox={deleteTarget} onClose={() => setDeleteTarget(null)} />
      <MailboxAccessPanel
        mailbox={accessTarget}
        onClose={() => setAccessTargetId(null)}
        onRefresh={() => void mailboxes.refetch()}
      />
    </div>
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

/** Type, handover, and lock marks of a list item, with their names for assistive technology. */
function MailboxMarks({ mailbox }: { mailbox: Mailbox }) {
  const t = useT('mailbox');
  const typeLabel = t(
    mailbox.awaiting_handover
      ? 'mailboxes.awaitingHandover'
      : mailbox.type === 'personal'
        ? 'mailboxes.personal'
        : 'mailboxes.shared'
  );
  const TypeIcon = mailbox.awaiting_handover ? UserX : mailbox.type === 'personal' ? User : Users;
  return (
    <span className="flex shrink-0 items-center gap-1 text-muted-foreground">
      <span title={typeLabel} className="flex items-center">
        <TypeIcon className="size-3.5" aria-hidden="true" />
        <span className="sr-only">{typeLabel}</span>
      </span>
      {mailbox.access.view ? null : (
        <span title={t('mailboxes.locked')} className="flex items-center">
          <Lock className="size-3.5" aria-hidden="true" />
          <span className="sr-only">{t('mailboxes.locked')}</span>
        </span>
      )}
    </span>
  );
}

function MailboxMessages({
  mailbox,
  onOpenAccess,
}: {
  mailbox: Mailbox;
  onOpenAccess: () => void;
}) {
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

  const formatter = useDateTimeFormat();
  const replyCopy = {
    prefixSubject: (subject: string) => t('compose.replySubject', { subject }),
    quotedHeader: (params: { date: string; sender: string }) => t('compose.quotedHeader', params),
    formatDate: (iso: string) => formatter.format(new Date(iso)),
  };
  const reply = (message: MailMessage) => setDraft(quotedReplyDraft(message, replyCopy));
  const replyAll = (message: MailMessage) =>
    setDraft(replyAllDraft(message, mailbox.address, replyCopy));
  const forward = (message: MailMessage) => {
    setDraft(
      forwardDraft(message, {
        prefixSubject: subject => t('compose.forwardSubject', { subject }),
        forwardedHeader: t('compose.forwardedHeader'),
        formatDate: iso => formatter.format(new Date(iso)),
        labels: {
          from: t('message.from'),
          date: t('message.date'),
          subject: t('emails.subject'),
          to: t('message.to'),
          cc: t('message.cc'),
        },
      })
    );
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
        <div className="flex flex-wrap gap-2">
          {hasAccessPanel(mailbox) ? (
            <Button
              type="button"
              size="sm"
              variant="outline"
              icon={<ShieldCheck className="size-4" />}
              aria-label={t('access.openLabel', { address: mailbox.address })}
              onClick={onOpenAccess}
            >
              {t('access.open')}
            </Button>
          ) : null}
          {mailbox.access.send ? (
            <Button
              type="button"
              size="sm"
              icon={<PenLine className="size-4" />}
              onClick={() => setDraft(emptyDraft)}
            >
              {t('actions.compose')}
            </Button>
          ) : null}
        </div>
      </div>

      {mailbox.access.view ? (
        <MailboxFolders
          mailbox={mailbox}
          direction={direction}
          page={page}
          openId={openId}
          onShowFolder={showFolder}
          onPageChange={nextPage => {
            setPage(nextPage);
            setOpenId(null);
          }}
          onOpen={open}
          onClose={() => setOpenId(null)}
          onReply={reply}
          onReplyAll={replyAll}
          onForward={forward}
        />
      ) : (
        <EmptyState
          icon={<Lock className="size-5" aria-hidden="true" />}
          title={t('mailboxes.lockedTitle')}
          description={t(
            mailbox.awaiting_handover
              ? 'mailboxes.lockedAwaiting'
              : mailbox.type === 'personal'
                ? 'mailboxes.lockedPersonal'
                : 'mailboxes.lockedShared'
          )}
        />
      )}

      {draft && mailbox.access.send ? (
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

/** The folders and messages of a mailbox the user reads. */
function MailboxFolders({
  mailbox,
  direction,
  page,
  openId,
  onShowFolder,
  onPageChange,
  onOpen,
  onClose,
  onReply,
  onReplyAll,
  onForward,
}: {
  mailbox: Mailbox;
  direction: MailDirection;
  page: number;
  openId: number | null;
  onShowFolder: (direction: MailDirection) => void;
  onPageChange: (page: number) => void;
  onOpen: (message: MailMessageSummary) => void;
  onClose: () => void;
  onReply: (message: MailMessage) => void;
  onReplyAll: (message: MailMessage) => void;
  onForward: (message: MailMessage) => void;
}) {
  const t = useT('mailbox');
  const showFolder = onShowFolder;
  // Loaded here, only for a mailbox the user reads, so the open message can step through it.
  const messages = useMailMessages(useMailOrganizationId(), mailbox.id, direction, page);
  const items = messages.data?.items ?? [];
  const openIndex = openId === null ? -1 : items.findIndex(item => item.id === openId);
  const previousMessage = openIndex > 0 ? items[openIndex - 1] : null;
  const nextMessage = openIndex >= 0 && openIndex < items.length - 1 ? items[openIndex + 1] : null;
  return (
    <>
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

      <div
        className={cn(
          openId !== null && 'xl:grid xl:grid-cols-[20rem_minmax(0,1fr)] xl:items-start xl:gap-4'
        )}
      >
        <div className={cn('min-w-0', openId !== null && 'hidden xl:block')}>
          <MessageList
            messages={messages}
            mailbox={mailbox}
            direction={direction}
            openId={openId}
            onPageChange={onPageChange}
            onOpen={onOpen}
          />
        </div>
        {openId !== null ? (
          <div className="animate-enter min-w-0">
            <MessageDetail
              mailboxId={mailbox.id}
              mailboxAddress={mailbox.address}
              messageId={openId}
              canSend={mailbox.access.send}
              onPrevious={previousMessage ? () => onOpen(previousMessage) : null}
              onNext={nextMessage ? () => onOpen(nextMessage) : null}
              onClose={onClose}
              onReply={onReply}
              onReplyAll={onReplyAll}
              onForward={onForward}
            />
          </div>
        ) : null}
      </div>
    </>
  );
}

function MessageList({
  messages,
  mailbox,
  direction,
  openId,
  onPageChange,
  onOpen,
}: {
  messages: ReturnType<typeof useMailMessages>;
  mailbox: Mailbox;
  direction: MailDirection;
  openId: number | null;
  onPageChange: (page: number) => void;
  onOpen: (message: MailMessageSummary) => void;
}) {
  const t = useT('mailbox');
  const formatter = useDateTimeFormat();

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
          const selected = message.id === openId;
          return (
            <li key={message.id}>
              <button
                type="button"
                aria-current={selected ? 'true' : undefined}
                className={cn(
                  'flex w-full items-start gap-3 px-4 py-3 text-left transition-colors duration-150 focus-ring hover:bg-accent/70 active:bg-accent',
                  selected && 'bg-accent'
                )}
                onClick={() => onOpen(message)}
              >
                <span
                  className={cn(
                    'mt-1.5 size-2 shrink-0 rounded-full transition-transform duration-200',
                    unread ? 'bg-primary ring-2 ring-primary/20' : 'bg-transparent'
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
                    <AttachmentIndicator count={message.attachment_count} />{' '}
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
  mailboxAddress,
  messageId,
  canSend,
  onPrevious,
  onNext,
  onClose,
  onReply,
  onReplyAll,
  onForward,
}: {
  mailboxId: number;
  mailboxAddress: string;
  messageId: number;
  canSend: boolean;
  onPrevious: (() => void) | null;
  onNext: (() => void) | null;
  onClose: () => void;
  onReply: (message: MailMessage) => void;
  onReplyAll: (message: MailMessage) => void;
  onForward: (message: MailMessage) => void;
}) {
  const t = useT('mailbox');
  const formatter = useDateTimeFormat();
  const query = useMailMessage(useMailOrganizationId(), mailboxId, messageId);
  // Keyed by message, so stepping to another message shows its HTML again.
  const [plainTextId, setPlainTextId] = useState<number | null>(null);
  const plainText = plainTextId === messageId;

  const navigation = (
    <div className="flex items-center gap-1">
      <Button
        type="button"
        size="sm"
        variant="ghost"
        icon={<ArrowLeft className="size-4" />}
        onClick={onClose}
      >
        {t('actions.back')}
      </Button>
      <Button
        type="button"
        isIcon
        size="sm"
        variant="ghost"
        disabled={!onPrevious}
        aria-label={t('message.previous')}
        title={t('message.previous')}
        onClick={onPrevious ?? undefined}
      >
        <ChevronLeft className="size-4" />
      </Button>
      <Button
        type="button"
        isIcon
        size="sm"
        variant="ghost"
        disabled={!onNext}
        aria-label={t('message.next')}
        title={t('message.next')}
        onClick={onNext ?? undefined}
      >
        <ChevronRight className="size-4" />
      </Button>
    </div>
  );

  if (query.isPending) {
    return (
      <div className="space-y-3">
        {navigation}
        <Skeleton className="h-48 w-full" />
      </div>
    );
  }
  if (query.error || !query.data) {
    return (
      <div className="space-y-3">
        {navigation}
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
        {navigation}
        <MessageActions
          message={message}
          mailboxAddress={mailboxAddress}
          canSend={canSend}
          onReply={onReply}
          onReplyAll={onReplyAll}
          onForward={onForward}
          onClose={onClose}
        />
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

        <MailAttachmentList message={message} />

        {hasHtml && hasText ? (
          <div role="group" aria-label={t('message.viewLabel')} className="flex gap-2">
            <Button
              type="button"
              size="xs"
              variant={showHtml ? 'default' : 'outline'}
              aria-pressed={showHtml}
              onClick={() => setPlainTextId(null)}
            >
              {t('message.viewHtml')}
            </Button>
            <Button
              type="button"
              size="xs"
              variant={showHtml ? 'outline' : 'default'}
              aria-pressed={!showHtml}
              onClick={() => setPlainTextId(messageId)}
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
