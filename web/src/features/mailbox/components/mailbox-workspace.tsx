'use client';

import { useEffect, useRef, useState } from 'react';
import { usePathname, useRouter, useSearchParams } from 'next/navigation';
import { Mail, MailOpen, PenLine, Settings2, ShieldCheck } from 'lucide-react';

import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import {
  ComposeDialog,
  emptyDraft,
  forwardDraft,
  quotedReplyDraft,
  replyAllDraft,
  type ComposeDraft,
} from '@/features/mailbox/components/compose-dialog';
import {
  hasAccessPanel,
  MailboxAccessPanel,
} from '@/features/mailbox/components/mailbox-access-panel';
import {
  inboxView,
  MailboxSidebar,
  viewFilters,
  type MailView,
} from '@/features/mailbox/components/mailbox-sidebar';
import {
  MAILBOX_PARAM,
  readStoredMailboxId,
  writeStoredMailboxId,
} from '@/features/mailbox/components/mailbox-params';
import { useComposeFocusReturn } from '@/features/mailbox/components/compose-focus';
import { MailboxSwitcher } from '@/features/mailbox/components/mailbox-switcher';
import { ManageFoldersDialog } from '@/features/mailbox/components/manage-folders-dialog';
import { ManageLabelsDialog } from '@/features/mailbox/components/manage-labels-dialog';
import { ManageMailboxesDialog } from '@/features/mailbox/components/manage-mailboxes-dialog';
import {
  MailOrganizationBoundary,
  MailPageHeader,
  OrganizationLostState,
  useMailOrganizationId,
} from '@/features/mailbox/components/mail-organization';
import {
  EmptyState,
  ErrorAlert,
  useDateTimeFormat,
} from '@/features/mailbox/components/mail-shared';
import {
  ListSkeleton,
  MessageListPane,
  rowMessageIds,
  rowUnread,
} from '@/features/mailbox/components/message-list';
import {
  MessageDetail,
  ThreadView,
  type ReaderProps,
} from '@/features/mailbox/components/thread-view';
import {
  useMailFolders,
  useMailLabels,
  useMailMessageCounts,
  useMailMessageList,
  useMailboxDomains,
  useMailboxes,
  useUpdateMailMessage,
  useUpdateMailMessages,
} from '@/features/mailbox/hooks/use-mailbox';
import { useMailOrganization } from '@/features/mailbox/hooks/use-mail-organization';
import type {
  MailMessage,
  MailMessageFilters,
  MailMessageSummary,
  Mailbox,
} from '@/features/mailbox/types';
import { canComposeFrom } from '@/features/mailbox/utils/connected-mailbox';
import { isOrganizationLost, resolveMailboxErrorKey } from '@/features/mailbox/utils/mailbox-error';
import { useT } from '@/i18n';
import { cn } from '@/utils';

export { MAILBOX_PARAM };
const mailboxIdPattern = /^[1-9]\d{0,15}$/u;
const searchDelayMilliseconds = 300;

/**
 * Mailbox console, laid out as a three-pane mail client: the left rail with the mailbox switcher
 * and the views, the list of the open view, and the reading pane. The switcher lists the
 * mailboxes the user reads; what a user may do follows each mailbox's `access`. Creating,
 * deleting, and giving access to mailboxes lives in the Manage mailboxes dialog for owners and
 * admins. The API remains the authority; the checks here only save a round trip.
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
  const router = useRouter();
  const pathname = usePathname();
  const searchParams = useSearchParams();
  const domains = useMailboxDomains(organizationId);
  const mailboxes = useMailboxes(organizationId, { live: true, scope: 'usable' });
  const [storedId] = useState(() => readStoredMailboxId(organizationId));
  const [manageOpen, setManageOpen] = useState(false);
  const [accessTargetId, setAccessTargetId] = useState<number | null>(null);

  const items = mailboxes.data;
  const readable = (items ?? []).filter(item => item.access.view);
  const raw = searchParams.get(MAILBOX_PARAM) ?? '';
  const requestedId = mailboxIdPattern.test(raw) ? Number(raw) : null;
  const selected =
    readable.find(item => item.id === requestedId) ??
    readable.find(item => item.id === storedId) ??
    readable[0] ??
    null;
  const selectedId = selected?.id;

  useEffect(() => {
    if (selectedId !== undefined) writeStoredMailboxId(organizationId, selectedId);
  }, [organizationId, selectedId]);

  if (mailboxes.isPending || domains.isPending) return <WorkspaceSkeleton />;
  const loadError = mailboxes.error ?? domains.error;
  if (isOrganizationLost(loadError)) return <OrganizationLostState />;
  if (loadError || !items || !domains.data) {
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

  const domain = domains.data.find(item => item.kind === 'shared') ?? domains.data[0] ?? null;
  // Looked up on every render, so the panel follows the list after a change.
  const accessTarget = items.find(item => item.id === accessTargetId) ?? null;

  const select = (mailbox: Mailbox) => {
    writeStoredMailboxId(organizationId, mailbox.id);
    const next = new URLSearchParams(window.location.search);
    next.set(MAILBOX_PARAM, String(mailbox.id));
    router.replace(`${pathname}?${next.toString()}`, { scroll: false });
  };
  const manageButton = canManage ? (
    <Button
      type="button"
      size="sm"
      variant="outline"
      icon={<Settings2 className="size-4" />}
      onClick={() => setManageOpen(true)}
    >
      {t('switcher.manage')}
    </Button>
  ) : null;

  return (
    <>
      {selected ? (
        <MailboxView
          key={selected.id}
          mailbox={selected}
          switcher={
            <MailboxSwitcher
              mailboxes={readable}
              selected={selected}
              canManage={canManage}
              onSelect={select}
              onManage={() => setManageOpen(true)}
            />
          }
          onOpenAccess={() => setAccessTargetId(selected.id)}
          onRefreshMailboxes={() => void mailboxes.refetch()}
        />
      ) : items.length === 0 ? (
        <EmptyState
          icon={<Mail className="size-5" aria-hidden="true" />}
          title={t('mailboxes.emptyTitle')}
          description={t(canManage ? 'mailboxes.empty' : 'organization.readOnly')}
          action={manageButton ?? undefined}
        />
      ) : (
        <EmptyState
          icon={<Mail className="size-5" aria-hidden="true" />}
          title={t('switcher.noReadableTitle')}
          description={t(canManage ? 'switcher.noReadable' : 'organization.readOnly')}
          action={manageButton ?? undefined}
        />
      )}

      {canManage ? (
        <ManageMailboxesDialog
          open={manageOpen}
          mailboxes={items}
          domain={domain}
          domains={domains.data}
          onClose={() => setManageOpen(false)}
          onCreated={select}
          onOpenAccess={mailbox => {
            setManageOpen(false);
            setAccessTargetId(mailbox.id);
          }}
        />
      ) : null}
      <MailboxAccessPanel
        mailbox={accessTarget}
        onClose={() => setAccessTargetId(null)}
        onRefresh={() => void mailboxes.refetch()}
      />
    </>
  );
}

/** What the reading pane shows: a thread from a placement view, or one message from the others. */
type OpenItem =
  | { kind: 'thread'; threadId: number; rowId: number }
  | { kind: 'message'; messageId: number; rowId: number };

/** One mailbox the user reads. Remounted per mailbox, so a switch starts at the Inbox. */
function MailboxView({
  mailbox,
  switcher,
  onOpenAccess,
  onRefreshMailboxes,
}: {
  mailbox: Mailbox;
  switcher: React.ReactNode;
  onOpenAccess: () => void;
  onRefreshMailboxes: () => void;
}) {
  const t = useT('mailbox');
  const organizationId = useMailOrganizationId();
  const [view, setView] = useState<MailView>(inboxView);
  const [q, setQ] = useState('');
  const [search, setSearch] = useState('');
  const [unreadOnly, setUnreadOnly] = useState(false);
  const [page, setPage] = useState(1);
  const [open, setOpen] = useState<OpenItem | null>(null);
  const [selected, setSelected] = useState<ReadonlySet<number>>(() => new Set());
  const [draft, setDraft] = useState<ComposeDraft | null>(null);
  // The page's composer replaces the workspace; closing it gives the focus back.
  useComposeFocusReturn(draft?.mode === 'compose');
  const [managing, setManaging] = useState<'folders' | 'labels' | null>(null);
  const searchTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(
    () => () => {
      if (searchTimer.current) clearTimeout(searchTimer.current);
    },
    []
  );

  const folders = useMailFolders(organizationId, mailbox.id);
  const labels = useMailLabels(organizationId, mailbox.id);
  // A folder or label deleted while it is shown (here or elsewhere) is gone from its list: the
  // view goes back to the inbox at once, so no request names the deleted id.
  const viewGone =
    (view.kind === 'folder' &&
      folders.data !== undefined &&
      !folders.data.some(folder => folder.id === view.id)) ||
    (view.kind === 'label' &&
      labels.data !== undefined &&
      !labels.data.some(label => label.id === view.id));
  if (viewGone) {
    setView(inboxView);
    setPage(1);
    setOpen(null);
    setSelected(new Set());
  }
  const shown = viewGone ? inboxView : view;
  const base: MailMessageFilters = q ? { q } : viewFilters(shown);
  // The unread filter lists single messages: threads group only whole placement views.
  const filters: MailMessageFilters = unreadOnly
    ? { ...base, group: undefined, unread: true }
    : base;
  const grouped = filters.group === 'thread';
  const messages = useMailMessageList(organizationId, mailbox.id, filters, page);
  const counts = useMailMessageCounts(organizationId, mailbox.id);
  const update = useUpdateMailMessage(organizationId, mailbox.id);
  const updateMany = useUpdateMailMessages(organizationId, mailbox.id);
  const folderList = folders.data ?? [];
  const labelList = labels.data ?? [];

  /** Every change of what is listed starts on page one, with nothing selected or open. */
  const resetListing = () => {
    setPage(1);
    setOpen(null);
    setSelected(new Set());
  };
  const showView = (next: MailView) => {
    if (searchTimer.current) clearTimeout(searchTimer.current);
    setView(next);
    setQ('');
    setSearch('');
    resetListing();
  };
  const commitSearch = (value: string) => {
    if (searchTimer.current) clearTimeout(searchTimer.current);
    const next = value.trim();
    if (next === q) return;
    setQ(next);
    resetListing();
  };
  const changeSearch = (value: string) => {
    setSearch(value);
    if (searchTimer.current) clearTimeout(searchTimer.current);
    if (value.trim() === '') {
      commitSearch('');
      return;
    }
    searchTimer.current = setTimeout(() => commitSearch(value), searchDelayMilliseconds);
  };

  const rows = messages.data?.items ?? [];
  const openIndex = open === null ? -1 : rows.findIndex(row => row.id === open.rowId);

  // Closing the reader gives the focus back to its row when the focus went with the reader.
  const lastOpenRow = useRef<number | null>(null);
  useEffect(() => {
    const closed = open === null ? lastOpenRow.current : null;
    lastOpenRow.current = open?.rowId ?? null;
    if (closed === null) return;
    const active = document.activeElement;
    if (active !== null && active !== document.body && !active.closest('[data-reading-pane]')) {
      return;
    }
    document.querySelector<HTMLElement>(`[data-row-id="${String(closed)}"]`)?.focus();
  }, [open]);

  // Esc closes the reader, unless the key went to a field, a dialog, or a control that used it.
  const reading = open !== null;
  useEffect(() => {
    if (!reading) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== 'Escape' || event.defaultPrevented || event.isComposing) return;
      if (event.metaKey || event.ctrlKey || event.altKey || event.shiftKey) return;
      const target = event.target;
      if (
        (target instanceof HTMLElement &&
          (target.isContentEditable || target.closest('input, textarea, select') !== null)) ||
        document.querySelector('[role="dialog"][data-state="open"], [role="alertdialog"]') !== null
      ) {
        return;
      }
      event.preventDefault();
      setOpen(null);
    };
    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, [reading]);

  // Another row starts the reader at its top, not where the last one was scrolled to.
  const readerPane = useRef<HTMLElement | null>(null);
  const openRowId = open?.rowId ?? null;
  useEffect(() => {
    if (openRowId !== null && readerPane.current) readerPane.current.scrollTop = 0;
  }, [openRowId]);

  const openRow = (row: MailMessageSummary) => {
    setOpen(
      grouped
        ? { kind: 'thread', threadId: row.thread_id, rowId: row.id }
        : { kind: 'message', messageId: row.id, rowId: row.id }
    );
    // Opening unread mail is what marks it read: the whole thread for a thread row.
    if (rowUnread(row)) {
      const ids = rowMessageIds(row);
      if (ids.length === 1) update.mutate({ messageId: ids[0], read: true });
      else updateMany.mutate({ message_ids: ids, read: true });
    }
  };

  const formatter = useDateTimeFormat();
  const replyCopy = {
    prefixSubject: (subject: string) => t('compose.replySubject', { subject }),
    quotedHeader: (params: { date: string; sender: string }) => t('compose.quotedHeader', params),
    formatDate: (iso: string) => formatter.format(new Date(iso)),
  };
  const reader: ReaderProps = {
    mailboxId: mailbox.id,
    mailboxAddress: mailbox.address,
    canSend: canComposeFrom(mailbox),
    canOrganize: mailbox.access.organize,
    shared: mailbox.type === 'shared' && mailbox.origin !== 'connected',
    onPrevious: openIndex > 0 ? () => openRow(rows[openIndex - 1]) : null,
    onNext:
      openIndex >= 0 && openIndex < rows.length - 1 ? () => openRow(rows[openIndex + 1]) : null,
    onClose: () => setOpen(null),
    // A message moved out of the view hands the reader to the next row, or back to the list.
    onLeave: () => {
      const next = openIndex >= 0 ? rows[openIndex + 1] : undefined;
      if (next) openRow(next);
      else setOpen(null);
    },
    onReply: (message: MailMessage) => setDraft(quotedReplyDraft(message, replyCopy)),
    onReplyAll: (message: MailMessage) =>
      setDraft(replyAllDraft(message, mailbox.address, replyCopy)),
    onForward: (message: MailMessage) =>
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
      ),
  };

  const title = q ? t('search.results', { q }) : viewTitle(view);
  function viewTitle(current: MailView): string {
    switch (current.kind) {
      case 'folder':
        return folderList.find(folder => folder.id === current.id)?.name ?? t('views.folders');
      case 'label':
        return labelList.find(label => label.id === current.id)?.name ?? t('views.labels');
      default:
        return t(`views.${current.kind}`);
    }
  }

  if (draft?.mode === 'compose' && canComposeFrom(mailbox)) {
    return (
      <ComposeDialog
        inline
        mailbox={mailbox}
        draft={draft}
        onClose={() => setDraft(null)}
        onStored={() => showView({ kind: 'sent' })}
        onReopen={() => setDraft(draft)}
      />
    );
  }
  return (
    <div className="grid gap-4 lg:h-[calc(100dvh-12rem)] lg:min-h-[36rem] lg:grid-cols-[16rem_minmax(0,1fr)]">
      <aside className="flex min-h-0 flex-col gap-3 lg:overflow-y-auto lg:border-r lg:pr-4">
        <div className="flex items-center gap-1">
          <div className="min-w-0 flex-1">{switcher}</div>
          {hasAccessPanel(mailbox) ? (
            <Button
              type="button"
              isIcon
              size="sm"
              variant="ghost"
              aria-label={t('access.openLabel', { address: mailbox.address })}
              title={t('access.openLabel', { address: mailbox.address })}
              onClick={onOpenAccess}
            >
              <ShieldCheck className="size-4" />
            </Button>
          ) : null}
        </div>
        {canComposeFrom(mailbox) ? (
          <Button
            type="button"
            className="w-full"
            data-compose-trigger=""
            icon={<PenLine className="size-4" />}
            onClick={() => setDraft(emptyDraft)}
          >
            {t('actions.compose')}
          </Button>
        ) : null}
        <MailboxSidebar
          view={q ? null : view}
          counts={counts.data}
          folders={folderList}
          labels={labelList}
          canOrganize={mailbox.access.organize}
          onSelect={showView}
          onManageFolders={() => setManaging('folders')}
          onManageLabels={() => setManaging('labels')}
        />
      </aside>

      <div className="grid min-h-0 min-w-0 gap-4 xl:grid-cols-[minmax(20rem,28rem)_minmax(0,1fr)]">
        <section
          data-scroll-pane
          aria-label={title}
          className={cn('min-h-0 min-w-0 lg:overflow-y-auto lg:pr-1', open && 'hidden xl:block')}
        >
          <MessageListPane
            mailbox={mailbox}
            view={view}
            listKey={JSON.stringify([filters, page])}
            title={title}
            q={q}
            unreadOnly={unreadOnly}
            messages={messages}
            folders={folderList}
            labels={labelList}
            openRowId={open?.rowId ?? null}
            selected={selected}
            search={search}
            refreshing={Boolean(messages.isFetching || counts.isFetching)}
            onUnreadOnlyChange={value => {
              setUnreadOnly(value);
              resetListing();
            }}
            onSelectedChange={setSelected}
            onSearchChange={changeSearch}
            onSearchSubmit={() => commitSearch(search)}
            onRefresh={() => {
              void messages.refetch();
              void counts.refetch();
              onRefreshMailboxes();
            }}
            onPageChange={next => {
              setPage(next);
              setOpen(null);
              setSelected(new Set());
            }}
            onOpen={openRow}
            onLeave={rowIds => {
              if (open && rowIds.includes(open.rowId)) setOpen(null);
            }}
          />
        </section>
        <section
          ref={readerPane}
          data-reading-pane=""
          className={cn(
            'animate-enter min-h-0 min-w-0 lg:overflow-y-auto xl:border-l xl:pl-4',
            !open && 'hidden xl:block'
          )}
        >
          {open?.kind === 'thread' ? (
            <ThreadView threadId={open.threadId} {...reader} />
          ) : open?.kind === 'message' ? (
            <MessageDetail messageId={open.messageId} {...reader} />
          ) : (
            <div className="flex h-full min-h-48 items-center justify-center">
              <p className="flex items-center gap-2 text-sm text-muted-foreground">
                <MailOpen className="size-4" aria-hidden="true" />
                {t('list.readerEmpty')}
              </p>
            </div>
          )}
        </section>
      </div>

      {draft && canComposeFrom(mailbox) ? (
        <ComposeDialog
          mailbox={mailbox}
          draft={draft}
          onClose={() => setDraft(null)}
          onStored={() => showView({ kind: 'sent' })}
          onReopen={() => setDraft(draft)}
        />
      ) : null}
      {mailbox.access.organize ? (
        <>
          <ManageFoldersDialog
            mailboxId={mailbox.id}
            folders={folderList}
            open={managing === 'folders'}
            onClose={() => setManaging(null)}
          />
          <ManageLabelsDialog
            mailboxId={mailbox.id}
            labels={labelList}
            open={managing === 'labels'}
            onClose={() => setManaging(null)}
          />
        </>
      ) : null}
    </div>
  );
}

function WorkspaceSkeleton() {
  return (
    <div className="grid gap-4 lg:grid-cols-[16rem_minmax(0,1fr)]" aria-hidden="true">
      <div className="space-y-2">
        <Skeleton className="h-12 w-full" />
        <Skeleton className="h-9 w-full" />
        <Skeleton className="h-40 w-full" />
      </div>
      <ListSkeleton />
    </div>
  );
}
