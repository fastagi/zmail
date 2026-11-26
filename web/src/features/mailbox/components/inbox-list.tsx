'use client';

import {
  Fragment,
  useEffect,
  useMemo,
  useRef,
  useState,
  type KeyboardEvent,
  type ReactNode,
  type Ref,
} from 'react';
import Link from 'next/link';
import { useLocale } from 'next-intl';
import {
  Archive,
  Clock,
  ExternalLink,
  Inbox,
  Mail,
  MailOpen,
  PanelLeft,
  RefreshCw,
  Star,
  Trash2,
  X,
} from 'lucide-react';

import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { Skeleton } from '@/components/ui/skeleton';
import {
  canArchiveInboxItem,
  canDeleteInboxItem,
  canSnoozeInboxItem,
  type InboxQuickAction,
} from '@/features/mailbox/components/inbox/use-inbox-actions';
import { SnoozeMenu } from '@/features/mailbox/components/snooze-menu';
import { InboxSearchBar } from '@/features/mailbox/components/inbox-search-bar';
import { TriageReasonLine } from '@/features/mailbox/components/inbox-triage-reason';
import { CopyAddressButton } from '@/features/mailbox/components/copy-address-button';
import { LabelDot } from '@/features/mailbox/components/mailbox-sidebar';
import { knownTimeZone } from '@/features/mailbox/components/mail-shared';
import { rowUnread } from '@/features/mailbox/components/message-rows';
import { INBOX_ASSIGNMENTS, INBOX_DATE_RANGES } from '@/features/mailbox/schemas';
import type {
  InboxAssignment,
  InboxItem,
  InboxPage,
  InboxView,
  MailRole,
} from '@/features/mailbox/types';
import { displayExternalProviderLabel } from '@/features/mailbox/utils/external-mail';
import { senderAvatarProps } from '@/features/mailbox/utils/sender-avatar';

import { useT } from '@/i18n';
import { cn } from '@/utils';

type ListTab = 'all' | 'unread' | 'attachments' | InboxAssignment;
type DateRange = (typeof INBOX_DATE_RANGES)[number];

/** How far below the visible rows the next page starts loading. */
const loadAheadMargin = '240px';

/**
 * The time of a row in the inbox's zone: `HH:mm` for today, the month and day for this year, the
 * full date before that.
 */
function useRowTime(zone: string): (iso: string) => string {
  const locale = useLocale();
  return useMemo(() => {
    const timeZone = knownTimeZone(zone);
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
 * this listing. `onShowSidebar` opens the mailboxes below 1024px, where the sidebar is a rail or
 * hidden.
 */
export function InboxListHeader({
  title,
  mailboxSelected,
  copyAddress = null,
  subtitle,
  unread,
  unreadCount,
  onUnreadChange,
  attachments = false,
  onAttachmentsChange,
  assignment = null,
  onAssignmentChange,
  dateRange = null,
  onDateRangeChange,
  q,
  onSearch,
  mailboxHref = null,
  onClearMailbox,
  onShowSidebar,
  searchRef,
  onRefresh,
  refreshing,
}: {
  onRefresh: () => void;
  refreshing: boolean;
  title: string;
  mailboxSelected: boolean;
  copyAddress?: string | null;
  subtitle?: string;
  unread: boolean;
  unreadCount?: number;
  onUnreadChange: (unread: boolean) => void;
  /** The With attachments tab, shown when `onAttachmentsChange` is given. */
  attachments?: boolean;
  onAttachmentsChange?: (attachments: boolean) => void;
  /** The assignment tabs of shared conversations, shown when `onAssignmentChange` is given. */
  assignment?: InboxAssignment | null;
  onAssignmentChange?: (assignment: InboxAssignment | null) => void;
  /** How far back the list reaches; the date menu shows when `onDateRangeChange` is given. */
  dateRange?: DateRange | null;
  onDateRangeChange?: (range: DateRange | null) => void;
  q: string;
  onSearch: (q: string) => void;
  mailboxHref?: string | null;
  onClearMailbox?: () => void;
  onShowSidebar?: () => void;
  /** The search field, which `/` focuses. */
  searchRef?: Ref<HTMLInputElement>;
}) {
  const t = useT('mailbox');
  const selectTab = (value: ListTab) => {
    if (value === 'attachments') onAttachmentsChange?.(true);
    else if (value === 'unread') onUnreadChange(true);
    else if (value !== 'all') onAssignmentChange?.(value);
    else if (assignment) onAssignmentChange?.(null);
    else if (attachments) onAttachmentsChange?.(false);
    else onUnreadChange(false);
  };
  const current: ListTab = assignment ?? (attachments ? 'attachments' : unread ? 'unread' : 'all');
  const tab = (value: ListTab, label: string, count?: number) => (
    <button
      type="button"
      role="tab"
      aria-selected={current === value}
      className={cn(
        '-mb-px shrink-0 border-b-2 pb-2.5 whitespace-nowrap transition-colors focus-ring motion-reduce:transition-none',
        current === value
          ? 'border-text-main font-semibold text-text-main'
          : 'border-transparent text-text-muted hover:text-text-main'
      )}
      onClick={() => selectTab(value)}
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
            className="-ml-2 shrink-0 lg:hidden"
            onClick={onShowSidebar}
          >
            <PanelLeft className="size-4" />
          </Button>
        ) : null}
        <div className="flex min-w-0 flex-1 items-center gap-1.5">
          <h1
            className={cn(
              'min-w-0 leading-tight font-semibold tracking-[-0.01em]',
              mailboxSelected
                ? 'break-all rounded-md bg-blue-50 px-2 py-1 font-sans text-[13px] leading-[18px] font-medium tracking-normal text-blue-800 dark:bg-blue-950 dark:text-blue-200'
                : 'truncate font-serif text-[28px]'
            )}
          >
            {title}
          </h1>
          {copyAddress ? (
            <CopyAddressButton
              address={copyAddress}
              label={t('inbox.copyAddress', { address: copyAddress })}
              copiedLabel={t('inbox.addressCopied', { address: copyAddress })}
              failedLabel={t('inbox.copyAddressFailed')}
              size="xs"
            />
          ) : null}
          {subtitle ? <span className="shrink-0 text-text-muted">{subtitle}</span> : null}
        </div>
        <Button
          type="button"
          isIcon
          size="sm"
          variant="ghost"
          aria-label={t('actions.refresh')}
          title={t('actions.refresh')}
          aria-busy={refreshing}
          disabled={refreshing}
          onClick={onRefresh}
        >
          <RefreshCw className={cn('size-4', refreshing && 'motion-safe:animate-spin')} />
        </Button>
        {mailboxHref ? (
          <Button asChild size="sm" isIcon variant="ghost">
            <Link
              href={mailboxHref}
              aria-label={t('inbox.openInMailbox')}
              title={t('inbox.openInMailbox')}
            >
              <ExternalLink className="size-4" />
            </Link>
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
      <InboxSearchBar
        q={q}
        onSearch={onSearch}
        unread={unread}
        onUnreadChange={onUnreadChange}
        attachments={attachments}
        onAttachmentsChange={onAttachmentsChange}
        dateRange={dateRange}
        onDateRangeChange={onDateRangeChange}
        searchRef={searchRef}
      />
      <div
        role="tablist"
        aria-label={t('inbox.tabs.label')}
        className="mt-3.5 flex gap-[18px] overflow-x-auto border-b border-border-subtle [scrollbar-width:none]"
      >
        {tab('all', t('inbox.tabs.all'))}
        {tab('unread', t('inbox.tabs.unread'), unreadCount)}
        {onAttachmentsChange ? tab('attachments', t('inbox.tabs.attachments')) : null}
        {onAssignmentChange
          ? INBOX_ASSIGNMENTS.map(value => (
              <Fragment key={value}>{tab(value, t(`inbox.tabs.${value}`))}</Fragment>
            ))
          : null}
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
  showMailbox = false,
  sendable,
  view = 'all',
  timeZone,
  selectedId,
  onOpen,
  onFilterMailbox,
  onLoadMore,
  hasMore,
  loadingMore = false,
  onQuickAction,
  onSnooze,
  checkedIds,
  onToggleSelect,
}: {
  /** The listbox's name: the list's title. */
  label: string;
  pages: readonly InboxPage[];
  /** The roles by id, which color the rows' mailbox labels. */
  roles?: ReadonlyMap<number, MailRole> | null;
  /** Whether rows name the mailbox that received them: off for a list of one mailbox. */
  showMailbox?: boolean;
  /** The mailboxes that may send, whose rows allow archive and delete actions. */
  sendable: ReadonlySet<number>;
  /** Active inbox view, used to hide Archive on Sent mail. */
  view?: InboxView;
  /** The inbox's zone, in which each row's time is shown. */
  timeZone: string;
  selectedId: number | null;
  onOpen: (item: InboxItem) => void;
  onFilterMailbox: (id: number) => void;
  onLoadMore: () => void;
  hasMore: boolean;
  loadingMore?: boolean;
  /** Optional Gmail-style hover quick action handler. */
  onQuickAction?: (item: InboxItem, action: InboxQuickAction) => void;
  /** Optional Gmail-style Snooze / Unsnooze handler. */
  onSnooze?: (item: InboxItem, snoozedUntil: string | null) => void;
  /** Optional multi-select checked row IDs. */
  checkedIds?: ReadonlySet<number>;
  /** Optional toggle callback for multi-select checkboxes. */
  onToggleSelect?: (id: number) => void;
}) {
  const t = useT('mailbox');
  const rowTime = useRowTime(timeZone);
  const sentinel = useRef<HTMLDivElement | null>(null);
  const pageCount = pages.length;
  const hasQuickActions = onQuickAction !== undefined || onSnooze !== undefined;
  const selectable = checkedIds !== undefined && onToggleSelect !== undefined;
  const hasChecked = selectable && checkedIds.size > 0;
  const [snoozeOpenId, setSnoozeOpenId] = useState<number | null>(null);

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

  // The row the keyboard moved to comes into view.
  const list = useRef<HTMLUListElement | null>(null);
  useEffect(() => {
    list.current?.querySelector('[aria-selected="true"]')?.scrollIntoView?.({ block: 'nearest' });
  }, [selectedId]);

  const items = pages.flatMap(page => page.items);
  // The row last open, and where it stood, so closing it gives the focus back to the list.
  const [lastOpen, setLastOpen] = useState<{ id: number; index: number } | null>(null);
  const selectedIndex = items.findIndex(item => item.id === selectedId);
  if (selectedIndex >= 0 && (lastOpen?.id !== selectedId || lastOpen.index !== selectedIndex)) {
    setLastOpen({ id: items[selectedIndex].id, index: selectedIndex });
  }
  // Without an open row, the last open one (or, once it left the list, the row now in its place)
  // is the one Tab reaches.
  const resting =
    lastOpen === null
      ? undefined
      : (items.find(item => item.id === lastOpen.id) ??
        items[Math.min(lastOpen.index, items.length - 1)]);
  const focusable = selectedIndex >= 0 ? selectedId : (resting?.id ?? items[0]?.id ?? null);

  // Closing the open mail returns the focus to its row when the focus went with the mail.
  const wasOpen = useRef(selectedId);
  useEffect(() => {
    const closed = wasOpen.current !== null && selectedId === null;
    wasOpen.current = selectedId;
    if (!closed || focusable === null) return;
    const active = document.activeElement;
    const lost =
      active === null || active === document.body || active.closest('[data-reading-pane]') !== null;
    if (!lost) return;
    list.current?.querySelector<HTMLElement>(`[data-row-id="${String(focusable)}"]`)?.focus();
  }, [selectedId, focusable]);
  /** Enter and Space open the focused row; the arrow keys move between rows. */
  const keyDown = (event: KeyboardEvent<HTMLDivElement>, item: InboxItem) => {
    if (event.key === 'Enter' || event.key === ' ') {
      event.preventDefault();
      onOpen(item);
      return;
    }
    if (event.key !== 'ArrowDown' && event.key !== 'ArrowUp') return;
    event.preventDefault();
    const sibling =
      event.key === 'ArrowDown'
        ? event.currentTarget.parentElement?.nextElementSibling
        : event.currentTarget.parentElement?.previousElementSibling;
    sibling?.querySelector<HTMLElement>('[data-row-id]')?.focus();
  };

  return (
    <div className="px-3 pb-3">
      <ul ref={list} role="listbox" aria-label={label} className="flex flex-col gap-0.5">
        {items.map(item => {
          const unread = rowUnread(item);
          const starred = item.thread?.starred ?? item.starred;
          const selected = item.id === selectedId;
          const checked = selectable && checkedIds.has(item.id);
          const count = item.thread?.message_count ?? 1;
          const role = item.role_id === null ? undefined : roles?.get(item.role_id);
          const senderLabel = item.from.name || item.from.address;
          const subject = item.subject || t('list.noSubject');
          const avatar = senderAvatarProps(senderLabel);
          const canArchive = onQuickAction !== undefined && canArchiveInboxItem(item, sendable, view);
          const canDelete = onQuickAction !== undefined && canDeleteInboxItem(item, sendable);
          const canSnooze = onSnooze !== undefined && canSnoozeInboxItem(item, sendable, view);
          const snoozeOpen = snoozeOpenId === item.id;
          return (
            <li key={item.id} role="presentation" className="group/row relative min-w-0">
              <div
                role="option"
                data-row-id={item.id}
                aria-selected={selected}
                tabIndex={item.id === focusable ? 0 : -1}
                className={cn(
                  'group relative flex cursor-pointer items-start gap-2.5 overflow-hidden rounded-[10px] px-3 py-2.5 transition-[background-color,box-shadow] duration-[120ms] focus-ring motion-reduce:transition-none',
                  showMailbox && 'pb-8',
                  unread &&
                    'before:absolute before:inset-y-2 before:left-0 before:w-[2.5px] before:rounded-r-full before:bg-brand',
                  checked
                    ? 'bg-blue-50/80 ring-1 ring-inset ring-blue-200/80 dark:bg-blue-950/50 dark:ring-blue-800/80'
                    : selected
                      ? 'bg-blue-50 ring-1 ring-inset ring-blue-200 dark:bg-blue-950/65 dark:ring-blue-800'
                      : unread
                        ? 'bg-bg-surface/70 hover:bg-row-hover hover:shadow-[0_1px_2px_rgba(15,23,42,0.04)]'
                        : 'hover:bg-row-hover hover:shadow-[0_1px_2px_rgba(15,23,42,0.03)]'
                )}
                onClick={() => onOpen(item)}
                onKeyDown={event => keyDown(event, item)}
              >
                <span
                  aria-hidden="true"
                  className={cn(
                    'mt-0.5 flex size-7 shrink-0 items-center justify-center rounded-full text-[11px] font-semibold tracking-tight select-none transition-opacity duration-[120ms] motion-reduce:transition-none',
                    avatar.className,
                    selectable &&
                      (hasChecked
                        ? 'opacity-0'
                        : 'group-has-[[data-avatar-select]:hover]/row:opacity-0 group-has-[[data-avatar-select]:focus-within]/row:opacity-0')
                  )}
                >
                  {avatar.monogram}
                </span>
                <span className="min-w-0 flex-1">
                  <span className="flex min-w-0 items-center gap-1.5">
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
                        'min-w-0 truncate text-[13.5px] leading-snug',
                        unread ? 'font-semibold text-text-main' : 'text-text-subtle'
                      )}
                    >
                      {senderLabel}
                    </span>
                    {count > 1 ? (
                      <span className="shrink-0 text-xs text-text-muted">
                        {t('inbox.threadCount', { count })}
                      </span>
                    ) : null}
                    {item.source && item.source !== 'zmail' ? (
                      <span
                        data-source={item.source}
                        title={t('inbox.sourceTitle', {
                          name: displayExternalProviderLabel(item.source, item.mailbox_address),
                        })}
                        className="shrink-0 rounded-full border border-border px-[6px] text-[11px] text-text-muted"
                      >
                        {displayExternalProviderLabel(item.source, item.mailbox_address)}
                      </span>
                    ) : null}
                    {role ? (
                      <span className="flex min-w-0 shrink items-center gap-1 rounded-full bg-row-selected px-[7px] text-[11px] text-text-muted group-aria-selected:bg-bg-surface">
                        <LabelDot color={role.color} className="size-1.5" />
                        <span className="truncate">{role.name}</span>
                      </span>
                    ) : null}
                    <span className="flex-1" />
                    {starred ? (
                      <Star
                        aria-hidden="true"
                        className={cn(
                          'size-3 shrink-0 fill-amber-500 text-amber-500',
                          hasQuickActions &&
                            'transition-opacity duration-[120ms] group-focus-within/row:opacity-0 group-hover/row:opacity-0 motion-reduce:transition-none',
                          snoozeOpen && 'opacity-0'
                        )}
                      />
                    ) : null}
                    <time
                      dateTime={item.created_at}
                      className={cn(
                        'shrink-0 text-xs text-text-faint tabular-nums',
                        hasQuickActions &&
                          'transition-opacity duration-[120ms] group-focus-within/row:opacity-0 group-hover/row:opacity-0 motion-reduce:transition-none',
                        snoozeOpen && 'opacity-0'
                      )}
                    >
                      {rowTime(item.created_at)}
                    </time>
                  </span>
                  <span
                    className={cn(
                      'mt-0.5 block truncate leading-snug',
                      unread ? 'font-semibold text-text-main' : 'text-text-subtle'
                    )}
                  >
                    {subject}
                  </span>
                  {view === 'snoozed' && item.snoozed_until ? (
                    <span className="mt-0.5 inline-flex items-center gap-1 text-[11px] font-medium text-amber-600 dark:text-amber-400">
                      <Clock className="size-3 shrink-0" aria-hidden="true" />
                      <span>
                        {t('inbox.snooze.untilBadge', { time: rowTime(item.snoozed_until) })}
                      </span>
                    </span>
                  ) : null}
                  {item.triage ? <TriageReasonLine triage={item.triage} /> : null}
                  {item.snippet ? (
                    <span className="mt-0.5 block truncate text-[12.5px] text-text-muted">
                      {item.snippet}
                    </span>
                  ) : null}
                </span>
              </div>
              {selectable ? (
                <span
                  data-avatar-select=""
                  onClick={event => {
                    event.stopPropagation();
                    if (event.target === event.currentTarget) onToggleSelect(item.id);
                  }}
                  className={cn(
                    'absolute top-3 left-3 z-10 flex size-7 cursor-pointer items-center justify-center rounded-full transition-opacity duration-[120ms] motion-reduce:transition-none',
                    hasChecked ? 'opacity-100' : 'opacity-0 hover:opacity-100 focus-within:opacity-100'
                  )}
                >
                  <Checkbox
                    aria-label={t('list.select', { subject })}
                    checked={checked}
                    onCheckedChange={() => onToggleSelect(item.id)}
                    onClick={event => event.stopPropagation()}
                  />
                </span>
              ) : null}
              {hasQuickActions ? (
                <div
                  role="group"
                  aria-label={t('inbox.quickActions.label', { subject })}
                  onClick={event => event.stopPropagation()}
                  className={cn(
                    'absolute top-2 right-2 z-10 flex items-center gap-0.5 rounded-lg border border-border-subtle/80 bg-bg-surface/95 px-1 py-0.5 opacity-0 shadow-sm backdrop-blur-xs transition-opacity duration-[120ms] group-focus-within/row:opacity-100 group-hover/row:opacity-100 motion-reduce:transition-none',
                    snoozeOpen && 'opacity-100'
                  )}
                >
                  {onQuickAction ? (
                    <>
                      <RowQuickAction
                        label={t(starred ? 'list.unstarRow' : 'list.starRow', { subject })}
                        tooltip={t(starred ? 'list.unstar' : 'list.star')}
                        pressed={starred}
                        className={starred ? 'text-amber-500 hover:text-amber-600' : undefined}
                        onClick={() => onQuickAction(item, 'star')}
                      >
                        <Star className={cn('size-3.5', starred && 'fill-amber-500 text-amber-500')} />
                      </RowQuickAction>
                      <RowQuickAction
                        label={t(unread ? 'list.markRowRead' : 'list.markRowUnread', { subject })}
                        tooltip={t(unread ? 'actions.markRead' : 'actions.markUnread')}
                        onClick={() => onQuickAction(item, 'toggleRead')}
                      >
                        {unread ? <MailOpen className="size-3.5" /> : <Mail className="size-3.5" />}
                      </RowQuickAction>
                    </>
                  ) : null}
                  {canSnooze ? (
                    <SnoozeMenu
                      label={t('list.snoozeRow', { subject })}
                      tooltip={t('actions.snooze')}
                      timeZone={timeZone}
                      isSnoozed={view === 'snoozed' || Boolean(item.snoozed_until)}
                      onOpenChange={open =>
                        setSnoozeOpenId(current => (open ? item.id : current === item.id ? null : current))
                      }
                      onSnooze={iso => onSnooze(item, iso)}
                      onUnsnooze={() => onSnooze(item, null)}
                    />
                  ) : null}
                  {canArchive ? (
                    <RowQuickAction
                      label={t('list.archiveRow', { subject })}
                      tooltip={t('actions.archive')}
                      onClick={() => onQuickAction(item, 'archive')}
                    >
                      <Archive className="size-3.5" />
                    </RowQuickAction>
                  ) : null}
                  {canDelete ? (
                    <RowQuickAction
                      label={t('list.deleteRow', { subject })}
                      tooltip={t('actions.delete')}
                      onClick={() => onQuickAction(item, 'trash')}
                    >
                      <Trash2 className="size-3.5" />
                    </RowQuickAction>
                  ) : null}
                </div>
              ) : null}
              {showMailbox && (
                <button
                  type="button"
                  title={t('inbox.filterMailbox', { address: item.mailbox_address })}
                  aria-label={t('inbox.filterMailbox', { address: item.mailbox_address })}
                  onClick={() => onFilterMailbox(item.mailbox_id)}
                  className="absolute bottom-1.5 left-[44px] inline-flex h-6 max-w-[calc(100%-3.5rem)] items-center gap-1.5 rounded-md px-1.5 text-xs text-text-muted focus-ring hover:bg-blue-50 hover:text-blue-800 focus-visible:text-blue-800 motion-safe:transition-colors dark:hover:bg-blue-950 dark:hover:text-blue-200 dark:focus-visible:text-blue-200"
                >
                  <Inbox className="size-3.5 shrink-0" aria-hidden="true" />
                  <span className="truncate">{item.mailbox_address}</span>
                </button>
              )}
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

/** One icon button inside a row's hover quick-action cluster. */
function RowQuickAction({
  label,
  tooltip,
  pressed,
  className,
  onClick,
  children,
}: {
  label: string;
  tooltip: string;
  pressed?: boolean;
  className?: string;
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
      aria-pressed={pressed}
      title={tooltip}
      className={cn(
        'size-6 text-text-muted hover:bg-row-hover hover:text-text-main',
        className
      )}
      onClick={event => {
        event.stopPropagation();
        onClick();
      }}
    >
      {children}
    </Button>
  );
}

/** One line of a row, at the row's line height, so the skeleton rows match the rows. */
function SkeletonLine({ className }: { className: string }) {
  return (
    <span className="flex h-[19.5px] items-center">
      <Skeleton className={cn('h-3', className)} />
    </span>
  );
}

/**
 * The rows of the unified inbox while they load, in the rows' own anatomy and size: the sender
 * line with its time, the subject, the snippet. The list keeps its height when the rows arrive.
 */
export function InboxListSkeleton({ rows = 6 }: { rows?: number }) {
  return (
    <div aria-hidden="true" data-list-skeleton="" className="px-3 pb-3">
      {Array.from({ length: rows }, (_, index) => (
        <div key={index} className="flex items-start gap-2.5 px-3 py-2.5">
          <Skeleton className="mt-0.5 size-7 shrink-0 rounded-full" />
          <div className="min-w-0 flex-1">
            <span className="flex items-center gap-1.5">
              <span className="size-1.5 shrink-0" />
              <SkeletonLine className="w-28" />
              <span className="flex-1" />
              <SkeletonLine className="w-9" />
            </span>
            <span className="mt-0.5 block">
              <SkeletonLine className="w-3/5" />
            </span>
            <SkeletonLine className="w-11/12" />
          </div>
        </div>
      ))}
    </div>
  );
}

/** The head of the list while the page loads: the title, the search field, the tabs. */
export function InboxListHeaderSkeleton() {
  return (
    <div aria-hidden="true" className="shrink-0 px-4 pt-[22px] pb-2.5 sm:px-6">
      <Skeleton className="h-[35px] w-40" />
      <Skeleton className="mt-3 h-8 w-full" />
      <div className="mt-3.5 h-[31px] border-b border-border-subtle" />
    </div>
  );
}
