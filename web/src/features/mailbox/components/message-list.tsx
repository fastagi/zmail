'use client';

import { useState, type FormEvent, type MouseEvent, type ReactNode } from 'react';
import {
  Archive,
  ArchiveRestore,
  ArrowUp,
  FolderInput,
  Inbox,
  MailCheck,
  MailOpen,
  RefreshCw,
  Search,
  Send,
  Star,
  Tag,
  Trash2,
} from 'lucide-react';
import { toast } from 'sonner';

import { Alert, AlertDescription } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { Input } from '@/components/ui/input';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import { Skeleton } from '@/components/ui/skeleton';
import { AttachmentIndicator } from '@/features/mailbox/components/mail-attachments';
import { useMailOrganizationId } from '@/features/mailbox/components/mail-organization';
import {
  ConfirmDialog,
  EmptyState,
  ErrorAlert,
  MailStatusBadge,
  mailBadgeKey,
  useDateTimeFormat,
} from '@/features/mailbox/components/mail-shared';
import { LabelDot, type MailView } from '@/features/mailbox/components/mailbox-sidebar';
import {
  originOf,
  toastWithUndo,
  type MailOrigin,
} from '@/features/mailbox/components/message-actions';
import {
  useDeleteMailMessage,
  useMailMessageList,
  usePurgeMailTrash,
  useUpdateMailMessage,
  useUpdateMailMessages,
} from '@/features/mailbox/hooks/use-mailbox';
import { MAIL_MESSAGE_UPDATES_MAX_IDS } from '@/features/mailbox/schemas';
import type {
  MailFolder,
  MailLabel,
  MailMessageSummary,
  Mailbox,
  UpdateMailMessagesInput,
} from '@/features/mailbox/types';
import { resolveMailboxErrorKey } from '@/features/mailbox/utils/mailbox-error';
import { useT } from '@/i18n';
import { cn } from '@/utils';

/** The message ids a row stands for: a thread row acts on the thread's messages in the view. */
export function rowMessageIds(row: MailMessageSummary): number[] {
  return row.thread?.message_ids ?? [row.id];
}

export function rowUnread(row: MailMessageSummary): boolean {
  return row.thread ? row.thread.unread : row.direction === 'inbound' && !row.read;
}

function rowStarred(row: MailMessageSummary): boolean {
  return row.thread?.starred ?? row.starred;
}

function rowLabelIds(row: MailMessageSummary): number[] {
  return row.thread?.label_ids ?? row.label_ids;
}

function chunks<T>(items: T[], size: number): T[][] {
  const result: T[][] = [];
  for (let index = 0; index < items.length; index += size) {
    result.push(items.slice(index, index + size));
  }
  return result;
}

/** One change for any number of messages, sent in requests of at most 100 ids. */
async function updateInChunks(
  mutateAsync: (input: UpdateMailMessagesInput) => Promise<unknown>,
  ids: number[],
  change: Omit<UpdateMailMessagesInput, 'message_ids'>
) {
  for (const part of chunks(ids, MAIL_MESSAGE_UPDATES_MAX_IDS)) {
    await mutateAsync({ message_ids: part, ...change });
  }
}

/** Puts moved rows back where they were. */
function useUndoMove(mailboxId: number) {
  const t = useT('mailbox');
  const updateMany = useUpdateMailMessages(useMailOrganizationId(), mailboxId);
  return async (groups: Map<string, { origin: MailOrigin; ids: number[] }>) => {
    try {
      for (const { origin, ids } of groups.values()) {
        await updateInChunks(updateMany.mutateAsync, ids, origin);
      }
    } catch (failure) {
      toast.error(t(resolveMailboxErrorKey(failure)));
    }
  };
}

/** Where the rows of a view were before a move, grouped so one undo request covers each place. */
function origins(
  view: MailView,
  rows: MailMessageSummary[]
): Map<string, { origin: MailOrigin; ids: number[] }> {
  const groups = new Map<string, { origin: MailOrigin; ids: number[] }>();
  for (const row of rows) {
    // A thread row's messages are all in the view's placement; a single message carries its own.
    const origin: MailOrigin = row.thread
      ? view.kind === 'folder'
        ? { placement: 'folder', folder_id: view.id }
        : { placement: view.kind === 'archive' ? 'archive' : 'default' }
      : originOf(row);
    const key = `${origin.placement}:${origin.folder_id ?? ''}`;
    const group = groups.get(key) ?? { origin, ids: [] };
    group.ids.push(...rowMessageIds(row));
    groups.set(key, group);
  }
  return groups;
}

/**
 * Rows that arrived with a background refetch: newer than anything shown before in the same
 * listing. They are counted for the "new messages" pill instead of being announced by a jump.
 */
function useFreshRows(listKey: string, items: MailMessageSummary[] | undefined) {
  const [tracked, setTracked] = useState<{
    key: string;
    items: MailMessageSummary[] | undefined;
    newest: number;
    fresh: number[];
  }>({ key: listKey, items, newest: Math.max(0, ...(items ?? []).map(row => row.id)), fresh: [] });

  if (tracked.key !== listKey || (tracked.items === undefined && items !== undefined)) {
    setTracked({
      key: listKey,
      items,
      newest: Math.max(0, ...(items ?? []).map(row => row.id)),
      fresh: [],
    });
  } else if (items !== undefined && items !== tracked.items) {
    const added = items.filter(row => row.id > tracked.newest).map(row => row.id);
    setTracked({
      key: listKey,
      items,
      newest: Math.max(tracked.newest, ...items.map(row => row.id)),
      fresh: [...tracked.fresh, ...added],
    });
  }
  return {
    count: tracked.fresh.length,
    clear: () => setTracked(current => ({ ...current, fresh: [] })),
  };
}

/**
 * The middle pane: the view's name, the unread filter, the refresh button, and the search box;
 * with rows selected, the header becomes the bar of actions on them. Polling replaces the data in
 * place, so the selection (kept by the parent as row ids), the scroll position, and the focus stay
 * where they are; mail that arrives meanwhile is announced by a pill above the rows.
 */
export function MessageListPane({
  mailbox,
  view,
  listKey,
  title,
  q,
  unreadOnly,
  messages,
  folders,
  labels,
  openRowId,
  selected,
  search,
  refreshing,
  onUnreadOnlyChange,
  onSelectedChange,
  onSearchChange,
  onSearchSubmit,
  onRefresh,
  onPageChange,
  onOpen,
  onLeave,
}: {
  mailbox: Mailbox;
  view: MailView;
  /** Names the listing (view, search, filter, page); new-mail tracking restarts when it changes. */
  listKey: string;
  title: string;
  q: string;
  unreadOnly: boolean;
  messages: ReturnType<typeof useMailMessageList>;
  folders: readonly MailFolder[];
  labels: readonly MailLabel[];
  openRowId: number | null;
  selected: ReadonlySet<number>;
  search: string;
  refreshing: boolean;
  onUnreadOnlyChange: (unreadOnly: boolean) => void;
  onSelectedChange: (selected: ReadonlySet<number>) => void;
  onSearchChange: (value: string) => void;
  onSearchSubmit: () => void;
  onRefresh: () => void;
  onPageChange: (page: number) => void;
  onOpen: (row: MailMessageSummary) => void;
  /** Rows that left the view after a change, so the reader can close when it showed one. */
  onLeave: (rowIds: number[]) => void;
}) {
  const t = useT('mailbox');
  const items = messages.data?.items;
  const rows = items ?? [];
  const chosen = rows.filter(row => selected.has(row.id));
  const allChosen = rows.length > 0 && chosen.length === rows.length;
  const fresh = useFreshRows(listKey, messages.isPlaceholderData ? undefined : items);

  const submit = (event: FormEvent) => {
    event.preventDefault();
    onSearchSubmit();
  };

  const showNew = (event: MouseEvent<HTMLElement>) => {
    event.currentTarget.closest('[data-scroll-pane]')?.scrollTo?.({ top: 0, behavior: 'smooth' });
    fresh.clear();
  };

  return (
    <div className="space-y-3">
      <div className="flex min-h-9 flex-wrap items-center gap-2">
        {chosen.length > 0 ? (
          <>
            <Checkbox
              aria-label={t('list.selectAll')}
              checked={allChosen ? true : 'indeterminate'}
              onCheckedChange={() =>
                onSelectedChange(allChosen ? new Set() : new Set(rows.map(row => row.id)))
              }
              className="ml-3"
            />
            <BulkActions
              mailbox={mailbox}
              view={view}
              q={q}
              rows={chosen}
              folders={folders}
              labels={labels}
              onDone={moved => {
                if (moved) {
                  onSelectedChange(new Set());
                  onLeave(chosen.map(row => row.id));
                }
              }}
            />
          </>
        ) : (
          <>
            <h2 className="min-w-0 flex-1 truncate text-base font-semibold">{title}</h2>
            <Button
              type="button"
              size="xs"
              variant={unreadOnly ? 'secondary' : 'ghost'}
              aria-pressed={unreadOnly}
              icon={<MailCheck className="size-3.5" />}
              onClick={() => onUnreadOnlyChange(!unreadOnly)}
            >
              {t('list.unreadOnly')}
            </Button>
            <Button
              type="button"
              isIcon
              size="sm"
              variant="ghost"
              aria-label={t('actions.refresh')}
              title={t('actions.refresh')}
              aria-busy={refreshing}
              onClick={onRefresh}
            >
              <RefreshCw className={cn('size-4', refreshing && 'animate-spin')} />
            </Button>
          </>
        )}
      </div>

      <form role="search" aria-label={t('search.label')} onSubmit={submit} className="relative">
        <Search
          className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground"
          aria-hidden="true"
        />
        <Input
          type="search"
          value={search}
          maxLength={200}
          aria-label={t('search.label')}
          placeholder={t('search.placeholder')}
          className="pl-9"
          onChange={event => onSearchChange(event.target.value)}
        />
      </form>

      {fresh.count > 0 ? (
        <div className="sticky top-0 z-10 flex justify-center">
          <Button
            type="button"
            size="xs"
            className="rounded-full shadow-md"
            icon={<ArrowUp className="size-3.5" />}
            onClick={showNew}
          >
            {t('list.newMessages', { count: fresh.count })}
          </Button>
        </div>
      ) : null}

      {view.kind === 'trash' && !q && rows.length > 0 ? <TrashNote mailbox={mailbox} /> : null}

      <MessageRows
        mailbox={mailbox}
        view={view}
        q={q}
        messages={messages}
        labels={labels}
        openRowId={openRowId}
        selected={selected}
        onSelectedChange={onSelectedChange}
        onPageChange={onPageChange}
        onOpen={onOpen}
        onLeave={onLeave}
      />
    </div>
  );
}

function TrashNote({ mailbox }: { mailbox: Mailbox }) {
  const t = useT('mailbox');
  const purge = usePurgeMailTrash(useMailOrganizationId(), mailbox.id);
  const [confirm, setConfirm] = useState(false);
  return (
    <div className="flex flex-wrap items-center justify-between gap-2 rounded-md bg-muted/50 px-3 py-2">
      <p className="text-xs text-muted-foreground">{t('list.trashNote')}</p>
      {mailbox.access.organize ? (
        <>
          <Button
            type="button"
            size="xs"
            variant="outline"
            icon={<Trash2 className="size-3.5" />}
            onClick={() => setConfirm(true)}
          >
            {t('actions.emptyTrash')}
          </Button>
          <ConfirmDialog
            open={confirm}
            title={t('emptyTrash.title')}
            description={t('emptyTrash.description')}
            confirmLabel={t('actions.emptyTrash')}
            cancelLabel={t('actions.cancel')}
            pending={purge.isPending}
            icon={<Trash2 className="size-4" />}
            onCancel={() => setConfirm(false)}
            onConfirm={() =>
              void (async () => {
                try {
                  await purge.mutateAsync();
                  toast.success(t('messages.trashEmptied'));
                  setConfirm(false);
                } catch (failure) {
                  toast.error(t(resolveMailboxErrorKey(failure)));
                }
              })()
            }
          />
        </>
      ) : null}
    </div>
  );
}

/**
 * What may be done to the selected rows. `view` marks read and unread; `send` moves and labels;
 * in the trash `send` restores and `organize` deletes for good.
 */
function BulkActions({
  mailbox,
  view,
  q,
  rows,
  folders,
  labels,
  onDone,
}: {
  mailbox: Mailbox;
  view: MailView;
  q: string;
  rows: MailMessageSummary[];
  folders: readonly MailFolder[];
  labels: readonly MailLabel[];
  onDone: (moved: boolean) => void;
}) {
  const t = useT('mailbox');
  const organizationId = useMailOrganizationId();
  const updateMany = useUpdateMailMessages(organizationId, mailbox.id);
  const remove = useDeleteMailMessage(organizationId, mailbox.id);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const ids = [...new Set(rows.flatMap(rowMessageIds))];
  const canSend = mailbox.access.send;
  const inTrash = view.kind === 'trash' && !q;

  const undoMove = useUndoMove(mailbox.id);
  const apply = async (
    change: Omit<UpdateMailMessagesInput, 'message_ids'>,
    done: string | null,
    moved: boolean
  ) => {
    // Taken before the change: the rows still say where they are.
    const before = origins(view, rows);
    try {
      await updateInChunks(updateMany.mutateAsync, ids, change);
      // A move out of the view can be undone; restoring from the trash is itself the undo.
      if (done && moved && !inTrash) toastWithUndo(done, t('actions.undo'), () => undoMove(before));
      else if (done) toast.success(done);
      onDone(moved);
    } catch (failure) {
      toast.error(t(resolveMailboxErrorKey(failure)));
    }
  };

  const deleteForever = async () => {
    setDeleting(true);
    try {
      for (const id of ids) await remove.mutateAsync(id);
      toast.success(t('messages.deletedForever'));
      setConfirmDelete(false);
      onDone(true);
    } catch (failure) {
      toast.error(t(resolveMailboxErrorKey(failure)));
    } finally {
      setDeleting(false);
    }
  };

  const busy = updateMany.isPending || deleting;
  const action = (label: string, icon: ReactNode, onClick: () => void) => (
    <Button type="button" size="xs" variant="outline" disabled={busy} icon={icon} onClick={onClick}>
      {label}
    </Button>
  );
  const iconClass = 'size-3.5';
  const otherFolders = folders.filter(folder => !(view.kind === 'folder' && view.id === folder.id));

  return (
    <div
      className="flex flex-wrap items-center gap-1.5"
      role="toolbar"
      aria-label={t('list.selected', { count: rows.length })}
    >
      <span className="mr-1 text-xs text-muted-foreground">
        {t('list.selected', { count: rows.length })}
      </span>
      {action(
        t('actions.markRead'),
        <MailCheck className={iconClass} />,
        () => void apply({ read: true }, null, false)
      )}
      {action(
        t('actions.markUnread'),
        <MailOpen className={iconClass} />,
        () => void apply({ read: false }, null, false)
      )}
      {canSend && !inTrash ? (
        <>
          {(view.kind === 'archive' || view.kind === 'folder') && !q
            ? action(
                t('actions.moveToInbox'),
                <Inbox className={iconClass} />,
                () => void apply({ placement: 'default' }, t('messages.movedToInbox'), true)
              )
            : action(
                t('actions.archive'),
                <Archive className={iconClass} />,
                () => void apply({ placement: 'archive' }, t('messages.archived'), true)
              )}
          {otherFolders.length > 0 ? (
            <ChoiceMenu
              label={t('actions.moveToFolder')}
              icon={<FolderInput className={iconClass} />}
              disabled={busy}
            >
              {close =>
                otherFolders.map(folder => (
                  <MenuButton
                    key={folder.id}
                    onClick={() => {
                      close();
                      void apply(
                        { placement: 'folder', folder_id: folder.id },
                        t('messages.movedToFolder', { folder: folder.name }),
                        true
                      );
                    }}
                  >
                    {folder.name}
                  </MenuButton>
                ))
              }
            </ChoiceMenu>
          ) : null}
          {labels.length > 0 ? (
            <ChoiceMenu
              label={t('actions.label')}
              icon={<Tag className={iconClass} />}
              disabled={busy}
            >
              {close =>
                labels.map(label => {
                  const onAll = rows.every(row => rowLabelIds(row).includes(label.id));
                  return (
                    <MenuButton
                      key={label.id}
                      pressed={onAll}
                      onClick={() => {
                        close();
                        void apply(
                          onAll ? { remove_label_ids: [label.id] } : { add_label_ids: [label.id] },
                          t('messages.labelsUpdated'),
                          false
                        );
                      }}
                    >
                      <LabelDot color={label.color} />
                      {label.name}
                    </MenuButton>
                  );
                })
              }
            </ChoiceMenu>
          ) : null}
          {action(
            t('actions.delete'),
            <Trash2 className={iconClass} />,
            () => void apply({ placement: 'trash' }, t('messages.trashed'), true)
          )}
        </>
      ) : null}
      {canSend && inTrash
        ? action(
            t('actions.restore'),
            <ArchiveRestore className={iconClass} />,
            () => void apply({ placement: 'default' }, t('messages.restored'), true)
          )
        : null}
      {mailbox.access.organize && inTrash
        ? action(t('actions.deleteForever'), <Trash2 className={iconClass} />, () =>
            setConfirmDelete(true)
          )
        : null}
      <ConfirmDialog
        open={confirmDelete}
        title={t('deleteForever.title')}
        description={t('deleteForever.description', { count: ids.length })}
        confirmLabel={t('actions.deleteForever')}
        cancelLabel={t('actions.cancel')}
        pending={deleting}
        icon={<Trash2 className="size-4" />}
        onCancel={() => setConfirmDelete(false)}
        onConfirm={() => void deleteForever()}
      />
    </div>
  );
}

function ChoiceMenu({
  label,
  icon,
  disabled,
  children,
}: {
  label: string;
  icon: ReactNode;
  disabled: boolean;
  children: (close: () => void) => ReactNode;
}) {
  const [open, setOpen] = useState(false);
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <Button type="button" size="xs" variant="outline" disabled={disabled} icon={icon}>
          {label}
        </Button>
      </PopoverTrigger>
      <PopoverContent align="start" className="w-56 p-1">
        <div role="group" aria-label={label} className="flex max-h-72 flex-col overflow-y-auto">
          {children(() => setOpen(false))}
        </div>
      </PopoverContent>
    </Popover>
  );
}

function MenuButton({
  pressed,
  onClick,
  children,
}: {
  pressed?: boolean;
  onClick: () => void;
  children: ReactNode;
}) {
  return (
    <button
      type="button"
      aria-pressed={pressed}
      className={cn(
        'flex items-center gap-2 rounded-md px-2.5 py-1.5 text-left text-sm focus-ring hover:bg-accent',
        pressed && 'font-medium'
      )}
      onClick={onClick}
    >
      {children}
    </button>
  );
}

function MessageRows({
  mailbox,
  view,
  q,
  messages,
  labels,
  openRowId,
  selected,
  onSelectedChange,
  onPageChange,
  onOpen,
  onLeave,
}: {
  mailbox: Mailbox;
  view: MailView;
  q: string;
  messages: ReturnType<typeof useMailMessageList>;
  labels: readonly MailLabel[];
  openRowId: number | null;
  selected: ReadonlySet<number>;
  onSelectedChange: (selected: ReadonlySet<number>) => void;
  onPageChange: (page: number) => void;
  onOpen: (row: MailMessageSummary) => void;
  onLeave: (rowIds: number[]) => void;
}) {
  const t = useT('mailbox');
  const formatter = useDateTimeFormat();
  const organizationId = useMailOrganizationId();
  const update = useUpdateMailMessage(organizationId, mailbox.id);
  const updateMany = useUpdateMailMessages(organizationId, mailbox.id);
  const undoMove = useUndoMove(mailbox.id);
  const inTrash = view.kind === 'trash' && !q;

  if (messages.isPending) return <ListSkeleton />;
  if (messages.error || !messages.data) {
    const key = resolveMailboxErrorKey(messages.error);
    if (key === 'errors.searchTooBroad') {
      return (
        <Alert>
          <Search className="size-4" aria-hidden="true" />
          <AlertDescription>{t(key)}</AlertDescription>
        </Alert>
      );
    }
    return (
      <ErrorAlert
        message={t(key)}
        retryLabel={t('actions.retry')}
        onRetry={() => void messages.refetch()}
      />
    );
  }
  const { items, meta } = messages.data;
  if (items.length === 0) return <EmptyView mailbox={mailbox} view={view} q={q} />;

  const labelById = new Map(labels.map(label => [label.id, label]));
  const toggle = (row: MailMessageSummary) => {
    const next = new Set(selected);
    if (next.has(row.id)) next.delete(row.id);
    else next.add(row.id);
    onSelectedChange(next);
  };
  /** A quick action on one row; moves offer an undo. */
  const quick = async (
    row: MailMessageSummary,
    change: Omit<UpdateMailMessagesInput, 'message_ids'>,
    done: string | null
  ) => {
    const before = origins(view, [row]);
    try {
      await updateInChunks(updateMany.mutateAsync, rowMessageIds(row), change);
      if (done) {
        toastWithUndo(done, t('actions.undo'), () => undoMove(before));
        onLeave([row.id]);
        if (selected.has(row.id)) {
          const next = new Set(selected);
          next.delete(row.id);
          onSelectedChange(next);
        }
      }
    } catch (failure) {
      toast.error(t(resolveMailboxErrorKey(failure)));
    }
  };
  const star = async (row: MailMessageSummary) => {
    const ids = rowMessageIds(row);
    try {
      // Starring marks the newest message; removing the star clears it from the whole row.
      if (!rowStarred(row)) await update.mutateAsync({ messageId: row.id, starred: true });
      else if (ids.length === 1) await update.mutateAsync({ messageId: row.id, starred: false });
      else await updateMany.mutateAsync({ message_ids: ids, starred: false });
    } catch (failure) {
      toast.error(t(resolveMailboxErrorKey(failure)));
    }
  };

  return (
    <div className="space-y-3">
      <ul className="divide-y rounded-md border">
        {items.map(row => {
          const unread = rowUnread(row);
          const starred = rowStarred(row);
          const current = row.id === openRowId;
          const subject = row.subject || t('list.noSubject');
          const rowLabels = rowLabelIds(row)
            .map(id => labelById.get(id))
            .filter(label => label !== undefined);
          return (
            <li
              key={row.id}
              className={cn(
                'group relative flex items-start gap-1 py-1 pr-1 pl-3 transition-colors duration-150 hover:bg-accent/40',
                current ? 'bg-accent' : selected.has(row.id) && 'bg-accent/50'
              )}
            >
              <Checkbox
                aria-label={t('list.select', { subject })}
                checked={selected.has(row.id)}
                onCheckedChange={() => toggle(row)}
                className={cn(
                  'mt-3.5 transition-opacity group-hover:opacity-100 focus-visible:opacity-100',
                  selected.size > 0 || selected.has(row.id) ? 'opacity-100' : 'opacity-0'
                )}
              />
              <Button
                type="button"
                isIcon
                size="xs"
                variant="ghost"
                aria-pressed={starred}
                aria-label={t(starred ? 'list.unstar' : 'list.star')}
                title={t(starred ? 'list.unstar' : 'list.star')}
                className={cn('mt-2', starred ? 'text-warning' : 'text-muted-foreground')}
                onClick={() => void star(row)}
              >
                <Star className={cn('size-4', starred && 'fill-current')} />
              </Button>
              <button
                type="button"
                aria-current={current ? 'true' : undefined}
                className="flex min-w-0 flex-1 items-start gap-3 rounded-md px-2 py-2 text-left focus-ring hover:bg-accent/70 active:bg-accent"
                onClick={() => onOpen(row)}
              >
                <span
                  className={cn(
                    'mt-1.5 size-2 shrink-0 rounded-full',
                    unread ? 'bg-primary ring-2 ring-primary/20' : 'bg-transparent'
                  )}
                  aria-hidden="true"
                />
                <span className="min-w-0 flex-1">
                  <span className="flex items-center gap-2">
                    <span className={cn('truncate text-sm', unread && 'font-semibold')}>
                      {row.direction === 'inbound'
                        ? row.from.name || row.from.address
                        : t('list.to', { recipients: row.to.join(', ') })}
                    </span>
                    {row.thread && row.thread.message_count > 1 ? (
                      <span
                        aria-label={t('list.threadCount', { count: row.thread.message_count })}
                        className="shrink-0 text-xs text-muted-foreground tabular-nums"
                      >
                        {row.thread.message_count}
                      </span>
                    ) : null}
                    {unread ? <span className="sr-only">{t('list.unread')}</span> : null}
                    {row.status === 'failed' ? (
                      <Badge variant="destructive">{t('list.failed')}</Badge>
                    ) : row.last_event !== '' ? (
                      <MailStatusBadge message={row} label={t(mailBadgeKey(row))} />
                    ) : null}
                    {row.status === 'failed' && row.failure_code ? (
                      <code className="truncate text-xs text-muted-foreground">
                        {row.failure_code}
                      </code>
                    ) : null}
                  </span>
                  <span
                    className={cn(
                      'block truncate text-sm',
                      unread ? 'font-medium' : 'text-muted-foreground'
                    )}
                  >
                    <AttachmentIndicator count={row.attachment_count} /> {subject}
                  </span>
                  {rowLabels.length > 0 ? (
                    <span className="mt-1 flex flex-wrap gap-1">
                      {rowLabels.map(label => (
                        <span
                          key={label.id}
                          className="inline-flex items-center gap-1 rounded-full border px-1.5 py-0.5 text-[11px] leading-none text-muted-foreground"
                        >
                          <LabelDot color={label.color} className="size-2" />
                          {label.name}
                        </span>
                      ))}
                    </span>
                  ) : null}
                </span>
                <time
                  dateTime={row.created_at}
                  className="shrink-0 text-xs text-muted-foreground group-focus-within:opacity-0 group-hover:opacity-0"
                >
                  {formatter.format(new Date(row.created_at))}
                </time>
              </button>
              <span className="absolute top-2 right-2 flex gap-0.5 rounded-md bg-background/95 opacity-0 shadow-xs transition-opacity group-focus-within:opacity-100 group-hover:opacity-100">
                {mailbox.access.send && !inTrash && (view.kind !== 'archive' || q) ? (
                  <QuickAction
                    label={t('list.archiveRow', { subject })}
                    onClick={() =>
                      void quick(row, { placement: 'archive' }, t('messages.archived'))
                    }
                  >
                    <Archive className="size-4" />
                  </QuickAction>
                ) : null}
                {mailbox.access.send && !inTrash ? (
                  <QuickAction
                    label={t('list.deleteRow', { subject })}
                    onClick={() => void quick(row, { placement: 'trash' }, t('messages.trashed'))}
                  >
                    <Trash2 className="size-4" />
                  </QuickAction>
                ) : null}
                <QuickAction
                  label={t(unread ? 'list.markRowRead' : 'list.markRowUnread', { subject })}
                  onClick={() => void quick(row, { read: unread }, null)}
                >
                  {unread ? <MailCheck className="size-4" /> : <MailOpen className="size-4" />}
                </QuickAction>
              </span>
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

function QuickAction({
  label,
  onClick,
  children,
}: {
  label: string;
  onClick: () => void;
  children: ReactNode;
}) {
  return (
    <Button
      type="button"
      isIcon
      size="xs"
      variant="ghost"
      aria-label={label}
      title={label}
      className="text-muted-foreground hover:text-foreground"
      onClick={onClick}
    >
      {children}
    </Button>
  );
}

function EmptyView({ mailbox, view, q }: { mailbox: Mailbox; view: MailView; q: string }) {
  const t = useT('mailbox');
  if (q) {
    return (
      <EmptyState
        icon={<Search className="size-5" aria-hidden="true" />}
        title={t('list.searchEmptyTitle')}
        description={t('list.searchEmpty', { q })}
      />
    );
  }
  switch (view.kind) {
    case 'inbox':
      return (
        <EmptyState
          icon={<Inbox className="size-5" aria-hidden="true" />}
          title={t('list.emptyInboxTitle')}
          description={t('list.emptyInbox', { address: mailbox.address })}
        />
      );
    case 'sent':
      return (
        <EmptyState
          icon={<Send className="size-5" aria-hidden="true" />}
          title={t('list.emptySentTitle')}
          description={t('list.emptySent', { address: mailbox.address })}
        />
      );
    case 'starred':
      return (
        <EmptyState
          icon={<Star className="size-5" aria-hidden="true" />}
          title={t('list.emptyStarredTitle')}
          description={t('list.emptyStarred')}
        />
      );
    case 'trash':
      return (
        <EmptyState
          icon={<Trash2 className="size-5" aria-hidden="true" />}
          title={t('list.emptyTrashTitle')}
          description={t('list.trashNote')}
        />
      );
    default:
      return (
        <EmptyState
          icon={<Archive className="size-5" aria-hidden="true" />}
          title={t('list.emptyTitle')}
          description={t('list.empty')}
        />
      );
  }
}

export function ListSkeleton() {
  return (
    <div className="space-y-2" aria-hidden="true">
      <Skeleton className="h-14 w-full" />
      <Skeleton className="h-14 w-full" />
      <Skeleton className="h-14 w-full" />
    </div>
  );
}
