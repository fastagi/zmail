'use client';

import { useEffect, useMemo, useRef } from 'react';
import { useLocale } from 'next-intl';

import { rowUnread } from '@/features/mailbox/components/message-list';
import type { InboxItem, InboxPage } from '@/features/mailbox/types';
import { useT } from '@/i18n';
import { cn } from '@/utils';

/** How far below the visible rows the next page starts loading. */
const loadAheadMargin = '240px';

/**
 * The time of a row: the clock time for today, the day for this year, the full date before that,
 * all in the browser's zone.
 */
function useRowTime(): (iso: string) => string {
  const locale = useLocale();
  return useMemo(() => {
    const time = new Intl.DateTimeFormat(locale, { hour: '2-digit', minute: '2-digit' });
    const day = new Intl.DateTimeFormat(locale, { month: 'short', day: 'numeric' });
    const date = new Intl.DateTimeFormat(locale, { dateStyle: 'medium' });
    return (iso: string) => {
      const then = new Date(iso);
      const now = new Date();
      if (then.toDateString() === now.toDateString()) return time.format(then);
      return then.getFullYear() === now.getFullYear() ? day.format(then) : date.format(then);
    };
  }, [locale]);
}

/**
 * The rows of the unified inbox, newest first, across every loaded page. Each row shows an unread
 * dot (with bold text, so unread never rests on color alone), the sender, the thread size, the
 * time, the subject, and the snippet. The next page loads as the end of the list comes near, and
 * stops once the last page says there is no more.
 */
export function InboxList({
  pages,
  selectedId,
  onOpen,
  onLoadMore,
  hasMore,
  loadingMore = false,
}: {
  pages: readonly InboxPage[];
  selectedId: number | null;
  onOpen: (item: InboxItem) => void;
  onLoadMore: () => void;
  hasMore: boolean;
  loadingMore?: boolean;
}) {
  const t = useT('mailbox');
  const rowTime = useRowTime();
  const sentinel = useRef<HTMLLIElement | null>(null);
  const pageCount = pages.length;

  // Observed again after every page, so a short page that leaves the end in view loads the next.
  useEffect(() => {
    const target = sentinel.current;
    if (!hasMore || !target || typeof IntersectionObserver === 'undefined') return;
    const observer = new IntersectionObserver(
      entries => {
        if (entries.some(entry => entry.isIntersecting)) onLoadMore();
      },
      { rootMargin: loadAheadMargin }
    );
    observer.observe(target);
    return () => observer.disconnect();
  }, [hasMore, onLoadMore, pageCount]);

  const items = pages.flatMap(page => page.items);
  return (
    <ul className="divide-y rounded-md border">
      {items.map(item => {
        const unread = rowUnread(item);
        const selected = item.id === selectedId;
        const count = item.thread?.message_count ?? 1;
        return (
          <li key={item.id}>
            <button
              type="button"
              aria-current={selected ? 'true' : undefined}
              className={cn(
                'flex w-full flex-col gap-0.5 px-3 py-2.5 text-left transition-colors focus-ring hover:bg-accent/70',
                selected && 'bg-accent'
              )}
              onClick={() => onOpen(item)}
            >
              <span className="flex min-w-0 items-center gap-2">
                <span
                  aria-hidden="true"
                  className={cn(
                    'size-1.5 shrink-0 rounded-full',
                    unread ? 'bg-primary' : 'bg-transparent'
                  )}
                />
                {unread ? <span className="sr-only">{t('list.unread')}</span> : null}
                <span
                  className={cn(
                    'min-w-0 truncate text-sm',
                    unread ? 'font-semibold' : 'text-muted-foreground'
                  )}
                >
                  {item.from.name || item.from.address}
                </span>
                {count > 1 ? (
                  <span className="shrink-0 text-xs text-muted-foreground">
                    {t('inbox.threadCount', { count })}
                  </span>
                ) : null}
                <time
                  dateTime={item.created_at}
                  className="ml-auto shrink-0 text-xs text-muted-foreground tabular-nums"
                >
                  {rowTime(item.created_at)}
                </time>
              </span>
              <span
                className={cn(
                  'truncate pl-3.5 text-sm',
                  unread ? 'font-semibold' : 'text-muted-foreground'
                )}
              >
                {item.subject || t('list.noSubject')}
              </span>
              {item.snippet ? (
                <span className="truncate pl-3.5 text-xs text-muted-foreground">
                  {item.snippet}
                </span>
              ) : null}
            </button>
          </li>
        );
      })}
      {hasMore ? (
        <li ref={sentinel} className="px-3 py-2 text-center text-xs text-muted-foreground">
          {loadingMore ? t('inbox.loadingMore') : null}
        </li>
      ) : null}
    </ul>
  );
}
