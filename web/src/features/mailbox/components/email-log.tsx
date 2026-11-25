'use client';

import { lazy, Suspense, useEffect, useMemo, useRef, useState } from 'react';
import Link from 'next/link';
import { useLocale } from 'next-intl';
import { ArrowDownLeft, ArrowUpRight, Inbox, Search, Send, SearchX } from 'lucide-react';

import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Skeleton } from '@/components/ui/skeleton';
import {
  Table,
  TableBody,
  TableCaption,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { ROUTES } from '@/constants/routes';
import {
  emptyDraft,
  forwardDraft,
  quotedReplyDraft,
  replyAllDraft,
  type ComposeDraft,
} from '@/features/mailbox/components/compose-drafts';
import { EmailDetail } from '@/features/mailbox/components/email-detail';
import {
  MailOrganizationBoundary,
  MailPageHeader,
  OrganizationLostState,
  useMailOrganizationId,
} from '@/features/mailbox/components/mail-organization';
import {
  EmptyState,
  ErrorAlert,
  MailStatusBadge,
  mailBadgeKey,
  mailStatusKey,
  relativeTime,
  useDateTimeFormat,
  useRelativeTimeFormat,
} from '@/features/mailbox/components/mail-shared';
import {
  MAIL_STATS_DAYS,
  useMailLog,
  useMailStats,
  useMailboxes,
  useMarkMailMessageRead,
} from '@/features/mailbox/hooks/use-mailbox';
import {
  mailOrganizationHref,
  useMailOrganization,
  type MailOrganizationScope,
} from '@/features/mailbox/hooks/use-mail-organization';
import { MAIL_LOG_QUERY_MAX_LENGTH } from '@/features/mailbox/schemas';
import type {
  MailDayStats,
  MailDirection,
  MailLogEntry,
  MailMessage,
  MailStatus,
  Mailbox,
} from '@/features/mailbox/types';
import {
  emailLogSearch,
  hasEmailLogFilters,
  type EmailLogView,
} from '@/features/mailbox/utils/email-log-view';
import { isOrganizationLost, resolveMailboxErrorKey } from '@/features/mailbox/utils/mailbox-error';
import { AttachmentIndicator } from '@/features/mailbox/components/mail-attachments';
import { useT } from '@/i18n';
import { cn } from '@/utils';

// The composer loads when it first opens.
const ComposeDialog = lazy(async () => ({
  default: (await import('@/features/mailbox/components/compose-dialog')).ComposeDialog,
}));

const searchDelayMilliseconds = 300;
const statuses: readonly MailStatus[] = ['received', 'queued', 'sent', 'failed'];
const ALL_FILTER = '__all__';

interface Composition {
  draft: ComposeDraft;
  mailbox: Pick<Mailbox, 'id' | 'address'>;
  /** A new message may leave from any mailbox; a reply leaves from the one that received it. */
  pickFrom: boolean;
}

/**
 * Emails console: every message across the chosen organization's mailboxes, with a week of
 * counts, filters kept in the URL, and the message detail. The API stays the authority for
 * membership and filtering; this component only chooses what to ask for.
 */
export function EmailLog({ initialView }: { initialView: EmailLogView }) {
  const t = useT('mailbox');
  const scope = useMailOrganization();
  // The filters in the URL belong to the organization the page opened with.
  const [switched, setSwitched] = useState(false);
  const header: MailOrganizationScope =
    scope.status === 'ready'
      ? {
          ...scope,
          select: organizationId => {
            setSwitched(true);
            scope.select(organizationId);
          },
        }
      : scope;

  return (
    <div className="space-y-6">
      {scope.status === 'ready' ? null : (
        <MailPageHeader
          title={t('emails.title')}
          description={t('emails.description')}
          scope={scope}
        />
      )}
      <MailOrganizationBoundary scope={scope}>
        {() => (
          <EmailLogContent scope={header} initialView={switched ? { page: 1 } : initialView} />
        )}
      </MailOrganizationBoundary>
    </div>
  );
}

function EmailLogContent({
  scope,
  initialView,
}: {
  scope: MailOrganizationScope;
  initialView: EmailLogView;
}) {
  const t = useT('mailbox');
  const formatter = useDateTimeFormat();
  const organizationId = useMailOrganizationId();
  const mailboxes = useMailboxes(organizationId);
  const markRead = useMarkMailMessageRead(organizationId);
  const [view, setView] = useState(initialView);
  const [search, setSearch] = useState(initialView.q ?? '');
  const [opened, setOpened] = useState<MailLogEntry | null>(null);
  const [composition, setComposition] = useState<Composition | null>(null);
  const searchTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  // The URL mirrors the organization and the view, so a reload or a shared link shows the same
  // list.
  useEffect(() => {
    window.history.replaceState(
      window.history.state,
      '',
      `${window.location.pathname}${emailLogSearch(view, organizationId)}`
    );
  }, [organizationId, view]);
  useEffect(
    () => () => {
      if (searchTimer.current) clearTimeout(searchTimer.current);
    },
    []
  );

  /** Any filter change starts again at the first page. */
  const filter = (patch: Partial<Omit<EmailLogView, 'page'>>) =>
    setView(current => ({ ...current, ...patch, page: 1 }));

  const changeSearch = (value: string) => {
    setSearch(value);
    if (searchTimer.current) clearTimeout(searchTimer.current);
    searchTimer.current = setTimeout(() => {
      const q = value.trim();
      filter({ q: q === '' ? undefined : q });
    }, searchDelayMilliseconds);
  };

  const clearFilters = () => {
    if (searchTimer.current) clearTimeout(searchTimer.current);
    setSearch('');
    setView({ page: 1 });
  };

  const open = (entry: MailLogEntry) => {
    setOpened(entry);
    // Opening an unread inbound message is what marks it read.
    if (entry.direction === 'inbound' && !entry.read) {
      markRead.mutate({ mailboxId: entry.mailbox_id, messageId: entry.id });
    }
  };

  // The filter lists the mailboxes the user reads; compose offers those they send from.
  const owned = (mailboxes.data ?? []).filter(item => item.access.view);
  const senders = (mailboxes.data ?? []).filter(item => item.access.send);
  const compose = () => {
    const mailbox = senders.find(item => item.id === view.mailboxId) ?? senders[0];
    if (mailbox) setComposition({ draft: emptyDraft, mailbox, pickFrom: true });
  };
  // Replying needs `send` on the opened message's mailbox; one not among the user's has none.
  const openedCanSend = opened !== null && senders.some(item => item.id === opened.mailbox_id);
  const openedCanOrganize =
    opened !== null && owned.some(item => item.id === opened.mailbox_id && item.access.organize);
  const replyCopy = {
    prefixSubject: (subject: string) => t('compose.replySubject', { subject }),
    quotedHeader: (params: { date: string; sender: string }) => t('compose.quotedHeader', params),
    formatDate: (isoDate: string) => formatter.format(new Date(isoDate)),
  };
  const reply = (message: MailMessage) => {
    if (!opened || !openedCanSend) return;
    setComposition({
      draft: quotedReplyDraft(message, replyCopy),
      mailbox: { id: opened.mailbox_id, address: opened.mailbox_address },
      pickFrom: false,
    });
  };
  const replyAll = (message: MailMessage) => {
    if (!opened || !openedCanSend) return;
    setComposition({
      draft: replyAllDraft(message, opened.mailbox_address, replyCopy),
      mailbox: { id: opened.mailbox_id, address: opened.mailbox_address },
      pickFrom: false,
    });
  };
  const forward = (message: MailMessage) => {
    if (!opened || !openedCanSend) return;
    setComposition({
      draft: forwardDraft(message, {
        prefixSubject: subject => t('compose.forwardSubject', { subject }),
        forwardedHeader: t('compose.forwardedHeader'),
        formatDate: isoDate => formatter.format(new Date(isoDate)),
        labels: {
          from: t('message.from'),
          date: t('message.date'),
          subject: t('compose.subject'),
          to: t('message.to'),
          cc: t('message.cc'),
        },
      }),
      mailbox: { id: opened.mailbox_id, address: opened.mailbox_address },
      pickFrom: false,
    });
  };

  const lost = isOrganizationLost(mailboxes.error);
  if (composition?.pickFrom && !lost) {
    return (
      <Suspense fallback={null}>
        <ComposeDialog
          inline
          mailbox={composition.mailbox}
          fromOptions={senders}
          draft={composition.draft}
          onClose={() => setComposition(null)}
          onStored={() => setView(current => ({ ...current, page: 1 }))}
          onReopen={() => setComposition(composition)}
        />
      </Suspense>
    );
  }

  return (
    <>
      <MailPageHeader title={t('emails.title')} description={t('emails.description')} scope={scope}>
        <Button asChild variant="outline">
          <Link href={mailOrganizationHref(ROUTES.CONSOLE.MAIL_BROADCASTS, organizationId)}>
            {t('broadcasts.title')}
          </Link>
        </Button>
        <Button
          type="button"
          icon={<Send className="size-4" />}
          disabled={senders.length === 0}
          title={
            mailboxes.isSuccess && senders.length === 0 ? t('emails.sendUnavailable') : undefined
          }
          onClick={compose}
        >
          {t('emails.send')}
        </Button>
      </MailPageHeader>

      {lost ? (
        <OrganizationLostState />
      ) : opened ? (
        <EmailDetail
          key={opened.id}
          mailboxId={opened.mailbox_id}
          mailboxAddress={opened.mailbox_address}
          messageId={opened.id}
          canSend={openedCanSend}
          canOrganize={openedCanOrganize}
          onClose={() => setOpened(null)}
          onReply={reply}
          onReplyAll={replyAll}
          onForward={forward}
        />
      ) : (
        <>
          <EmailStats />
          <div
            role="search"
            aria-label={t('emails.filters')}
            className="flex flex-wrap items-center gap-2"
          >
            <div className="relative min-w-48 flex-1">
              <Search
                className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground"
                aria-hidden="true"
              />
              <Input
                type="search"
                value={search}
                maxLength={MAIL_LOG_QUERY_MAX_LENGTH}
                autoComplete="off"
                spellCheck={false}
                className="pl-9"
                aria-label={t('emails.search')}
                placeholder={t('emails.searchPlaceholder')}
                onChange={event => changeSearch(event.target.value)}
              />
            </div>
            <Select
              value={view.direction ?? ALL_FILTER}
              onValueChange={value =>
                filter({
                  direction:
                    value === ALL_FILTER ? undefined : (value as MailDirection),
                })
              }
            >
              <SelectTrigger aria-label={t('emails.direction')}>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value={ALL_FILTER}>{t('emails.directionAll')}</SelectItem>
                <SelectItem value="inbound">{t('emails.statusReceived')}</SelectItem>
                <SelectItem value="outbound">{t('emails.statusSent')}</SelectItem>
              </SelectContent>
            </Select>
            <Select
              value={view.status ?? ALL_FILTER}
              onValueChange={value =>
                filter({
                  status: value === ALL_FILTER ? undefined : (value as MailStatus),
                })
              }
            >
              <SelectTrigger aria-label={t('emails.status')}>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value={ALL_FILTER}>{t('emails.statusAll')}</SelectItem>
                {statuses.map(status => (
                  <SelectItem key={status} value={status}>
                    {t(mailStatusKey[status])}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Select
              value={view.mailboxId === undefined ? ALL_FILTER : String(view.mailboxId)}
              onValueChange={value =>
                filter({ mailboxId: value === ALL_FILTER ? undefined : Number(value) })
              }
            >
              <SelectTrigger aria-label={t('emails.mailbox')} className="max-w-56">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value={ALL_FILTER}>{t('emails.mailboxAll')}</SelectItem>
                {owned.map(mailbox => (
                  <SelectItem key={mailbox.id} value={String(mailbox.id)}>
                    {mailbox.address}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <EmailTable
            view={view}
            hasMailboxes={!mailboxes.isSuccess || owned.length > 0}
            onPageChange={page => setView(current => ({ ...current, page }))}
            onClearFilters={clearFilters}
            onOpen={open}
          />
        </>
      )}

      {composition ? (
        <Suspense fallback={null}>
          <ComposeDialog
            mailbox={composition.mailbox}
            fromOptions={composition.pickFrom ? senders : undefined}
            draft={composition.draft}
            onClose={() => setComposition(null)}
            onStored={() => setView(current => ({ ...current, page: 1 }))}
            onReopen={() => setComposition(composition)}
          />
        </Suspense>
      ) : null}
    </>
  );
}

function EmailStats() {
  const t = useT('mailbox');
  const locale = useLocale();
  const stats = useMailStats(useMailOrganizationId(), MAIL_STATS_DAYS);
  const dayFormat = useMemo(
    () => new Intl.DateTimeFormat(locale, { month: 'short', day: 'numeric', timeZone: 'UTC' }),
    [locale]
  );

  if (stats.isPending) return <Skeleton className="h-28 w-full" aria-hidden="true" />;
  if (stats.error || !stats.data) {
    return (
      <ErrorAlert
        message={t(resolveMailboxErrorKey(stats.error))}
        retryLabel={t('actions.retry')}
        onRetry={() => void stats.refetch()}
      />
    );
  }

  const days = stats.data;
  const total = (key: 'sent' | 'failed' | 'received') =>
    days.reduce((sum, day) => sum + day[key], 0);
  const peak = Math.max(1, ...days.map(day => Math.max(day.sent, day.failed, day.received)));

  return (
    <section
      aria-labelledby="email-stats-title"
      className="grid gap-4 rounded-md border p-4 lg:grid-cols-[auto_minmax(0,1fr)] lg:items-end"
    >
      <div>
        <h2 id="email-stats-title" className="text-sm font-medium text-muted-foreground">
          {t('emails.statsTitle')}
        </h2>
        <dl className="mt-2 flex gap-6">
          <StatFigure label={t('emails.statusSent')} value={total('sent')} tone="bg-success" />
          <StatFigure label={t('emails.statusFailed')} value={total('failed')} tone="bg-error" />
          <StatFigure label={t('emails.statusReceived')} value={total('received')} tone="bg-info" />
        </dl>
      </div>
      <div
        role="group"
        aria-label={t('emails.statsChart')}
        className="flex h-24 items-stretch gap-1 lg:justify-self-end"
      >
        {days.map(day => {
          const label = t('emails.statsDay', {
            day: dayFormat.format(new Date(`${day.day}T00:00:00Z`)),
            sent: day.sent,
            failed: day.failed,
            received: day.received,
          });
          return <DayBars key={day.day} day={day} peak={peak} label={label} />;
        })}
      </div>
    </section>
  );
}

function StatFigure({ label, value, tone }: { label: string; value: number; tone: string }) {
  return (
    <div>
      <dt className="flex items-center gap-1.5 text-xs text-muted-foreground">
        <span className={cn('size-2 rounded-full', tone)} aria-hidden="true" />
        {label}
      </dt>
      <dd className="mt-0.5 text-2xl font-semibold tabular-nums">{value}</dd>
    </div>
  );
}

/** One day: sent, failed, and received side by side, scaled to the busiest count of the week. */
function DayBars({ day, peak, label }: { day: MailDayStats; peak: number; label: string }) {
  const bars = [
    { count: day.sent, tone: 'bg-success' },
    { count: day.failed, tone: 'bg-error' },
    { count: day.received, tone: 'bg-info' },
  ];
  return (
    <div
      role="img"
      aria-label={label}
      title={label}
      className="flex w-9 flex-col justify-end gap-1 rounded-sm px-1 hover:bg-muted/60"
    >
      <div className="flex min-h-0 flex-1 items-end gap-0.5 border-b">
        {bars.map(bar => (
          <span
            key={bar.tone}
            className={cn('flex-1 rounded-t-xs', bar.tone)}
            style={{ height: bar.count === 0 ? 0 : `${Math.max(6, (bar.count / peak) * 100)}%` }}
          />
        ))}
      </div>
      <span className="text-center text-[10px] leading-none text-muted-foreground tabular-nums">
        {day.day.slice(8)}
      </span>
    </div>
  );
}

function EmailTable({
  view,
  hasMailboxes,
  onPageChange,
  onClearFilters,
  onOpen,
}: {
  view: EmailLogView;
  hasMailboxes: boolean;
  onPageChange: (page: number) => void;
  onClearFilters: () => void;
  onOpen: (entry: MailLogEntry) => void;
}) {
  const t = useT('mailbox');
  const absolute = useDateTimeFormat();
  const relative = useRelativeTimeFormat();
  const [now] = useState(() => Date.now());
  const organizationId = useMailOrganizationId();
  const { page, ...filters } = view;
  const log = useMailLog(organizationId, filters, page);

  if (log.isPending) {
    return (
      <div className="space-y-2" aria-hidden="true">
        <Skeleton className="h-10 w-full" />
        <Skeleton className="h-12 w-full" />
        <Skeleton className="h-12 w-full" />
        <Skeleton className="h-12 w-full" />
      </div>
    );
  }
  if (log.error || !log.data) {
    return (
      <ErrorAlert
        message={t(resolveMailboxErrorKey(log.error))}
        retryLabel={t('actions.retry')}
        onRetry={() => void log.refetch()}
      />
    );
  }

  const { items, meta } = log.data;
  if (items.length === 0) {
    if (hasEmailLogFilters(view)) {
      return (
        <EmptyState
          icon={<SearchX className="size-5" aria-hidden="true" />}
          title={t('emails.filteredEmptyTitle')}
          description={t('emails.filteredEmpty')}
          action={
            <Button type="button" size="sm" variant="outline" onClick={onClearFilters}>
              {t('emails.clearFilters')}
            </Button>
          }
        />
      );
    }
    return (
      <EmptyState
        icon={<Inbox className="size-5" aria-hidden="true" />}
        title={t('emails.emptyTitle')}
        description={t(hasMailboxes ? 'emails.empty' : 'emails.emptyNoMailbox')}
        action={
          hasMailboxes ? undefined : (
            <Button asChild size="sm" variant="outline">
              <Link href={mailOrganizationHref(ROUTES.CONSOLE.MAILBOXES, organizationId)}>
                {t('emails.openMailboxes')}
              </Link>
            </Button>
          )
        }
      />
    );
  }

  return (
    <div className="space-y-3">
      <div className="rounded-md border">
        <Table>
          <TableCaption className="sr-only">{t('emails.caption')}</TableCaption>
          <TableHeader>
            <TableRow>
              <TableHead>{t('emails.counterpart')}</TableHead>
              <TableHead>{t('emails.status')}</TableHead>
              <TableHead>{t('emails.subject')}</TableHead>
              <TableHead>{t('emails.mailbox')}</TableHead>
              <TableHead className="text-right">{t('emails.time')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {items.map(entry => {
              const inbound = entry.direction === 'inbound';
              const unread = inbound && !entry.read;
              const Direction = inbound ? ArrowDownLeft : ArrowUpRight;
              const created = new Date(entry.created_at);
              return (
                <TableRow
                  key={entry.id}
                  data-unread={unread ? 'true' : undefined}
                  className={cn('relative', unread && 'bg-primary/5')}
                >
                  <TableCell className="max-w-64">
                    {/* The button's overlay makes the whole row its click target. */}
                    <button
                      type="button"
                      className="flex w-full min-w-0 items-center gap-2 text-left focus-ring after:absolute after:inset-0"
                      onClick={() => onOpen(entry)}
                    >
                      <span
                        className={cn(
                          'size-2 shrink-0 rounded-full',
                          unread ? 'bg-primary' : 'bg-transparent'
                        )}
                        aria-hidden="true"
                      />
                      <Direction
                        className={cn('size-4 shrink-0', inbound ? 'text-info' : 'text-success')}
                        aria-hidden="true"
                      />
                      <span className="sr-only">
                        {t(inbound ? 'emails.inbound' : 'emails.outbound')}
                      </span>
                      <span className={cn('truncate', unread && 'font-semibold')}>
                        {inbound ? entry.from.name || entry.from.address : entry.to.join(', ')}
                      </span>
                      {unread ? <span className="sr-only">{t('list.unread')}</span> : null}
                    </button>
                  </TableCell>
                  <TableCell>
                    <span
                      className="flex flex-col items-start gap-0.5"
                      title={entry.failure_code || undefined}
                    >
                      <MailStatusBadge message={entry} label={t(mailBadgeKey(entry))} />
                      {entry.status === 'failed' && entry.failure_code ? (
                        <code className="max-w-40 truncate text-xs text-muted-foreground">
                          {entry.failure_code}
                        </code>
                      ) : null}
                    </span>
                  </TableCell>
                  <TableCell className={cn('max-w-72 truncate', unread && 'font-medium')}>
                    <AttachmentIndicator count={entry.attachment_count} />{' '}
                    {entry.subject || t('list.noSubject')}
                  </TableCell>
                  <TableCell className="max-w-56 truncate text-muted-foreground">
                    {entry.mailbox_address}
                  </TableCell>
                  <TableCell className="text-right whitespace-nowrap text-muted-foreground">
                    <time dateTime={entry.created_at} title={absolute.format(created)}>
                      {relativeTime(relative, created.getTime(), now)}
                    </time>
                  </TableCell>
                </TableRow>
              );
            })}
          </TableBody>
        </Table>
      </div>
      <div className="flex flex-wrap items-center justify-between gap-3">
        <span className="text-sm text-muted-foreground">
          {t('emails.range', { from: meta.from, to: meta.to, total: meta.total })}
        </span>
        {meta.last_page > 1 ? (
          <div className="flex items-center gap-3">
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
    </div>
  );
}
