'use client';

import type { ReactNode } from 'react';
import {
  Archive,
  Inbox,
  MailCheck,
  MailOpen,
  Search,
  Send,
  Star,
  Trash2,
} from 'lucide-react';
import { toast } from 'sonner';

import { Alert, AlertDescription } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { Skeleton } from '@/components/ui/skeleton';
import { AttachmentIndicator } from '@/features/mailbox/components/mail-attachments';
import { useMailOrganizationId } from '@/features/mailbox/components/mail-organization';
import {
  EmptyState,
  ErrorAlert,
  MailStatusBadge,
  mailBadgeKey,
  StaleNotice,
  useDateTimeFormat,
} from '@/features/mailbox/components/mail-shared';
import { LabelDot, type MailView } from '@/features/mailbox/components/mailbox-sidebar';
import { toastWithUndo } from '@/features/mailbox/components/message-actions';
import { rowMessageIds, rowUnread } from '@/features/mailbox/components/message-rows';
import {
  origins,
  rowLabelIds,
  rowStarred,
  updateInChunks,
  useUndoMove,
} from '@/features/mailbox/components/messages/message-list-helpers';
import {
  useMailMessageList,
  useUpdateMailMessage,
  useUpdateMailMessages,
} from '@/features/mailbox/hooks/use-mailbox';
import type {
  MailLabel,
  MailMessageSummary,
  Mailbox,
  UpdateMailMessagesInput,
} from '@/features/mailbox/types';
import { resolveMailboxErrorKey } from '@/features/mailbox/utils/mailbox-error';
import { useT } from '@/i18n';
import { cn } from '@/utils';

export function MessageRows({
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
  // A failed poll keeps the list already on screen; only a list that never loaded is an error.
  if (!messages.data) {
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
  const stale = messages.isRefetchError ? <StaleNotice message={t('list.refreshFailed')} /> : null;
  if (items.length === 0) {
    return (
      <>
        {stale}
        <EmptyView mailbox={mailbox} view={view} q={q} />
      </>
    );
  }

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
      {stale}
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
                className={cn(
                  'mt-2',
                  starred ? 'text-warning hover:text-warning' : 'text-muted-foreground'
                )}
                onClick={() => void star(row)}
              >
                <Star className={cn('size-4', starred && 'fill-current')} />
              </Button>
              <button
                type="button"
                data-row-id={row.id}
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
