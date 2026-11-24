'use client';

import type { FormEvent, MouseEvent } from 'react';
import { ArrowUp, MailCheck, RefreshCw, Search } from 'lucide-react';

import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { Input } from '@/components/ui/input';
import type { MailView } from '@/features/mailbox/components/mailbox-sidebar';
import { rowMessageIds, rowUnread } from '@/features/mailbox/components/message-rows';
import { BulkActions } from '@/features/mailbox/components/messages/bulk-actions';
import { useFreshRows } from '@/features/mailbox/components/messages/message-list-helpers';
import { ListSkeleton, MessageRows } from '@/features/mailbox/components/messages/message-rows';
import { TrashNote } from '@/features/mailbox/components/messages/trash-note';
import type { useMailMessageList } from '@/features/mailbox/hooks/use-mailbox';
import type {
  MailFolder,
  MailLabel,
  MailMessageSummary,
  Mailbox,
} from '@/features/mailbox/types';
import { useT } from '@/i18n';
import { cn } from '@/utils';

export { ListSkeleton, rowMessageIds, rowUnread };

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
