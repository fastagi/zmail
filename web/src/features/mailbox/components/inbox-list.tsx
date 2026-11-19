'use client';

import { useEffect, useMemo, useRef, useState, type FormEvent, type KeyboardEvent } from 'react';
import Link from 'next/link';
import { useLocale } from 'next-intl';
import { ExternalLink, PanelLeft, Search, X } from 'lucide-react';

import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Kbd } from '@/components/ui/kbd';
import { LabelDot } from '@/features/mailbox/components/mailbox-sidebar';
import { rowUnread } from '@/features/mailbox/components/message-list';
import { MAIL_SEARCH_MAX_LENGTH } from '@/features/mailbox/schemas';
import type { InboxItem, InboxPage, MailRole } from '@/features/mailbox/types';
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
 * The head of the unified inbox list (§11.0, §11.5): the serif title with its subtitle, a way out
 * of a narrowing (back to every mailbox, or into that mailbox's own workspace), the search field,
 * and the All / Unread tabs. `unreadCount` is shown on the Unread tab when the counts know it for
 * this listing. `onShowSidebar` opens the mailboxes below 1280px, where the sidebar is a rail or
 * hidden.
 */
export function InboxListHeader({
  title,
  subtitle,
  unread,
  unreadCount,
  onUnreadChange,
  q,
  onSearch,
  mailboxHref = null,
  onClearMailbox,
  onShowSidebar,
}: {
  title: string;
  subtitle?: string;
  unread: boolean;
  unreadCount?: number;
  onUnreadChange: (unread: boolean) => void;
  q: string;
  onSearch: (q: string) => void;
  mailboxHref?: string | null;
  onClearMailbox?: () => void;
  onShowSidebar?: () => void;
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
        '-mb-px border-b-2 pb-2.5 transition-colors focus-ring motion-reduce:transition-none',
        unread === value
          ? 'border-text-main font-semibold text-text-main'
          : 'border-transparent text-text-muted hover:text-text-main'
      )}
      onClick={() => onUnreadChange(value)}
    >
      {label}
      {count ? (
        <>
          {' '}
          <span className="font-normal text-text-faint tabular-nums">{count}</span>
        </>
      ) : null}
    </button>
  );

  return (
    <div className="shrink-0 px-4 pt-[22px] pb-2.5 sm:px-6">
      <div className="flex min-w-0 items-center gap-2">
        {onShowSidebar ? (
          <Button
            type="button"
            isIcon
            size="sm"
            variant="ghost"
            aria-label={t('inbox.showSidebar')}
            title={t('inbox.showSidebar')}
            className="-ml-2 shrink-0 xl:hidden"
            onClick={onShowSidebar}
          >
            <PanelLeft className="size-4" />
          </Button>
        ) : null}
        <div className="flex min-w-0 flex-1 items-baseline gap-2.5">
          <h1 className="min-w-0 truncate font-serif text-[28px] leading-tight font-semibold tracking-[-0.01em]">
            {title}
          </h1>
          {subtitle ? <span className="shrink-0 text-text-muted">{subtitle}</span> : null}
        </div>
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
      <form
        role="search"
        aria-label={t('inbox.search.label')}
        onSubmit={submit}
        className="relative mt-3"
      >
        <Search
          className="pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-text-muted"
          aria-hidden="true"
        />
        <Input
          type="search"
          value={search}
          maxLength={MAIL_SEARCH_MAX_LENGTH}
          aria-label={t('inbox.search.label')}
          placeholder={t('inbox.search.placeholder')}
          className="h-8 pl-8 text-[13px]"
          onChange={event => change(event.target.value)}
        />
      </form>
      <div
        role="tablist"
        aria-label={t('inbox.tabs.label')}
        className="mt-3.5 flex gap-[18px] border-b border-border-subtle"
      >
        {tab(false, t('inbox.tabs.all'))}
        {tab(true, t('inbox.tabs.unread'), unreadCount)}
      </div>
    </div>
  );
}

/**
 * The rows of the unified inbox, newest first, across every loaded page, as a listbox whose
 * options are the rows (§11.0 row anatomy): an unread dot (with bold text, so unread never rests
 * on color alone), the sender, the thread size, the mailbox's role tag, the `E` `#` caps on hover
 * where the mailbox may send, the time, then the subject and the snippet. The open row is the
 * selected option. The next page loads as the end of the list comes near, and stops once the
 * last page says there is no more.
 */
export function InboxList({
  label,
  pages,
  roles = null,
  sendable,
  timeZone,
  selectedId,
  onOpen,
  onLoadMore,
  hasMore,
  loadingMore = false,
}: {
  /** The listbox's name: the list's title. */
  label: string;
  pages: readonly InboxPage[];
  /** The roles by id for the rows' role tags; null hides the tags (a list narrowed to one). */
  roles?: ReadonlyMap<number, MailRole> | null;
  /** The mailboxes that may send, whose rows hint at the archive and delete keys. */
  sendable: ReadonlySet<number>;
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
  const sentinel = useRef<HTMLDivElement | null>(null);
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
  const focusable = items.some(item => item.id === selectedId)
    ? selectedId
    : (items[0]?.id ?? null);
  /** Enter and Space open the focused row; the arrow keys move between rows. */
  const keyDown = (event: KeyboardEvent<HTMLLIElement>, item: InboxItem) => {
    if (event.key === 'Enter' || event.key === ' ') {
      event.preventDefault();
      onOpen(item);
      return;
    }
    if (event.key !== 'ArrowDown' && event.key !== 'ArrowUp') return;
    event.preventDefault();
    const sibling =
      event.key === 'ArrowDown'
        ? event.currentTarget.nextElementSibling
        : event.currentTarget.previousElementSibling;
    if (sibling instanceof HTMLElement) sibling.focus();
  };

  return (
    <div className="px-3 pb-3">
      <ul role="listbox" aria-label={label} className="flex flex-col">
        {items.map(item => {
          const unread = rowUnread(item);
          const selected = item.id === selectedId;
          const count = item.thread?.message_count ?? 1;
          const role = item.role_id === null ? undefined : roles?.get(item.role_id);
          return (
            <li
              key={item.id}
              role="option"
              aria-selected={selected}
              tabIndex={item.id === focusable ? 0 : -1}
              className={cn(
                'group cursor-pointer rounded-[10px] p-3 focus-ring motion-reduce:transition-none',
                selected ? 'bg-row-selected' : 'hover:bg-row-hover'
              )}
              onClick={() => onOpen(item)}
              onKeyDown={event => keyDown(event, item)}
            >
              <span className="flex min-w-0 items-center gap-2">
                <span
                  aria-hidden="true"
                  className={cn(
                    'size-1.5 shrink-0 rounded-full',
                    unread ? 'bg-brand' : 'bg-transparent'
                  )}
                />
                {unread ? <span className="sr-only">{t('list.unread')}</span> : null}
                <span
                  className={cn(
                    'min-w-0 truncate text-[13.5px]',
                    unread ? 'font-semibold text-text-main' : 'text-text-subtle'
                  )}
                >
                  {item.from.name || item.from.address}
                </span>
                {count > 1 ? (
                  <span className="shrink-0 text-xs text-text-muted">
                    {t('inbox.threadCount', { count })}
                  </span>
                ) : null}
                {role ? (
                  <span className="flex min-w-0 shrink items-center gap-1 rounded-full bg-row-selected px-[7px] text-[11px] text-text-muted group-aria-selected:bg-bg-surface">
                    <LabelDot color={role.color} className="size-1.5" />
                    <span className="truncate">{role.name}</span>
                  </span>
                ) : null}
                <span className="flex-1" />
                {sendable.has(item.mailbox_id) ? (
                  <span
                    aria-hidden="true"
                    className="flex shrink-0 gap-1 opacity-0 transition-opacity duration-[120ms] group-hover:opacity-100 motion-reduce:transition-none max-sm:hidden"
                  >
                    <Kbd>E</Kbd>
                    <Kbd>#</Kbd>
                  </span>
                ) : null}
                <time
                  dateTime={item.created_at}
                  className="shrink-0 text-xs text-text-faint tabular-nums"
                >
                  {rowTime(item.created_at)}
                </time>
              </span>
              <span
                className={cn(
                  'mt-0.5 block truncate',
                  unread ? 'font-semibold text-text-main' : 'text-text-subtle'
                )}
              >
                {item.subject || t('list.noSubject')}
              </span>
              {item.snippet ? (
                <span className="block truncate text-text-muted">{item.snippet}</span>
              ) : null}
            </li>
          );
        })}
      </ul>
      {hasMore ? (
        <div
          ref={sentinel}
          data-list-end=""
          className="px-3 py-2 text-center text-xs text-text-muted"
        >
          {loadingMore ? t('inbox.loadingMore') : null}
        </div>
      ) : null}
    </div>
  );
}
