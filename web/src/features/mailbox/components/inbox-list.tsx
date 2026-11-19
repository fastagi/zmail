'use client';

import { useEffect, useMemo, useRef, useState, type FormEvent } from 'react';
import Link from 'next/link';
import { useLocale } from 'next-intl';
import { ExternalLink, Search, X } from 'lucide-react';

import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { rowUnread } from '@/features/mailbox/components/message-list';
import { MAIL_SEARCH_MAX_LENGTH } from '@/features/mailbox/schemas';
import type { InboxItem, InboxPage } from '@/features/mailbox/types';
import { useT } from '@/i18n';
import { cn } from '@/utils';

/** How far below the visible rows the next page starts loading. */
const loadAheadMargin = '240px';

/** `zone` when this browser knows it, else undefined (the browser's own zone). */
function knownZone(zone: string): string | undefined {
  try {
    new Intl.DateTimeFormat('en-US', { timeZone: zone });
    return zone;
  } catch {
    return undefined;
  }
}

/**
 * The time of a row in the inbox's zone: `HH:mm` for today, the month and day for this year, the
 * full date before that.
 */
function useRowTime(zone: string): (iso: string) => string {
  const locale = useLocale();
  return useMemo(() => {
    const timeZone = knownZone(zone);
    const time = new Intl.DateTimeFormat(locale, {
      hour: '2-digit',
      minute: '2-digit',
      hourCycle: 'h23',
      timeZone,
    });
    const day = new Intl.DateTimeFormat(locale, { month: 'short', day: 'numeric', timeZone });
    const date = new Intl.DateTimeFormat(locale, { dateStyle: 'medium', timeZone });
    // `YYYY-MM-DD` of the zone's calendar, to compare days and years.
    const calendar = new Intl.DateTimeFormat('en-CA', {
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      timeZone,
    });
    return (iso: string) => {
      const then = new Date(iso);
      const thenDay = calendar.format(then);
      const today = calendar.format(new Date());
      if (thenDay === today) return time.format(then);
      return thenDay.slice(0, 4) === today.slice(0, 4) ? day.format(then) : date.format(then);
    };
  }, [locale, zone]);
}

/**
 * The head of the unified inbox list: its title, a way out of a mailbox narrowing (back to every
 * mailbox, or into that mailbox's own workspace), the All / Unread tabs, and the search field.
 * `unreadCount` is shown on the Unread tab when the counts know it for this listing.
 */
export function InboxListHeader({
  title,
  unread,
  unreadCount,
  onUnreadChange,
  q,
  onSearch,
  mailboxHref = null,
  onClearMailbox,
}: {
  title: string;
  unread: boolean;
  unreadCount?: number;
  onUnreadChange: (unread: boolean) => void;
  q: string;
  onSearch: (q: string) => void;
  mailboxHref?: string | null;
  onClearMailbox?: () => void;
}) {
  const t = useT('mailbox');
  const [search, setSearch] = useState(q);
  const [committed, setCommitted] = useState(q);
  // A search changed from outside (a reset URL, another view) shows in the field.
  if (committed !== q) {
    setCommitted(q);
    setSearch(q);
  }

  const submit = (event: FormEvent) => {
    event.preventDefault();
    onSearch(search.trim());
  };
  const change = (value: string) => {
    setSearch(value);
    if (value.trim() === '' && q !== '') onSearch('');
  };
  const tab = (value: boolean, label: string, count?: number) => (
    <button
      type="button"
      role="tab"
      aria-selected={unread === value}
      className={cn(
        '-mb-px border-b-2 px-1 pb-2 text-sm transition-colors focus-ring',
        unread === value
          ? 'border-foreground font-semibold text-foreground'
          : 'border-transparent text-muted-foreground hover:text-foreground'
      )}
      onClick={() => onUnreadChange(value)}
    >
      {label}
      {count ? (
        <>
          {' '}
          <span className="text-xs font-normal text-muted-foreground tabular-nums">{count}</span>
        </>
      ) : null}
    </button>
  );

  return (
    <div className="space-y-3">
      <div className="flex min-h-9 flex-wrap items-center gap-2">
        <h2 className="min-w-0 flex-1 truncate text-base font-semibold">{title}</h2>
        {mailboxHref ? (
          <Button asChild size="xs" variant="ghost" icon={<ExternalLink className="size-3.5" />}>
            <Link href={mailboxHref}>{t('inbox.openInMailbox')}</Link>
          </Button>
        ) : null}
        {onClearMailbox ? (
          <Button
            type="button"
            isIcon
            size="sm"
            variant="ghost"
            aria-label={t('inbox.clearMailbox')}
            title={t('inbox.clearMailbox')}
            onClick={onClearMailbox}
          >
            <X className="size-4" />
          </Button>
        ) : null}
      </div>
      <div role="tablist" aria-label={t('inbox.tabs.label')} className="flex gap-4 border-b">
        {tab(false, t('inbox.tabs.all'))}
        {tab(true, t('inbox.tabs.unread'), unreadCount)}
      </div>
      <form
        role="search"
        aria-label={t('inbox.search.label')}
        onSubmit={submit}
        className="relative"
      >
        <Search
          className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground"
          aria-hidden="true"
        />
        <Input
          type="search"
          value={search}
          maxLength={MAIL_SEARCH_MAX_LENGTH}
          aria-label={t('inbox.search.label')}
          placeholder={t('inbox.search.placeholder')}
          className="pl-9"
          onChange={event => change(event.target.value)}
        />
      </form>
    </div>
  );
}

/**
 * The rows of the unified inbox, newest first, across every loaded page. Each row shows an unread
 * dot (with bold text, so unread never rests on color alone), the sender, the thread size, the
 * time, the subject, and the snippet. The next page loads as the end of the list comes near, and
 * stops once the last page says there is no more.
 */
export function InboxList({
  pages,
  timeZone,
  selectedId,
  onOpen,
  onLoadMore,
  hasMore,
  loadingMore = false,
}: {
  pages: readonly InboxPage[];
  /** The inbox's zone, in which each row's time is shown. */
  timeZone: string;
  selectedId: number | null;
  onOpen: (item: InboxItem) => void;
  onLoadMore: () => void;
  hasMore: boolean;
  loadingMore?: boolean;
}) {
  const t = useT('mailbox');
  const rowTime = useRowTime(timeZone);
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
