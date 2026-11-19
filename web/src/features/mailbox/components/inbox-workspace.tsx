'use client';

import { useCallback, useEffect, useState } from 'react';
import Link from 'next/link';
import { usePathname, useRouter, useSearchParams } from 'next/navigation';
import { AlertCircle, Inbox, Mail, MailOpen, Settings2 } from 'lucide-react';

import { Alert, AlertDescription } from '@/components/ui/alert';

import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import {
  ComposeDialog,
  forwardDraft,
  quotedReplyDraft,
  replyAllDraft,
  type ComposeDraft,
} from '@/features/mailbox/components/compose-dialog';
import { InboxList, InboxListHeader } from '@/features/mailbox/components/inbox-list';
import {
  InboxReadingPane,
  type InboxReaderNavigation,
} from '@/features/mailbox/components/inbox-reading-pane';
import { InboxSidebar } from '@/features/mailbox/components/inbox-sidebar';
import { MailboxAccessPanel } from '@/features/mailbox/components/mailbox-access-panel';
import { MAILBOX_PARAM } from '@/features/mailbox/components/mailbox-workspace';
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
import { ListSkeleton } from '@/features/mailbox/components/message-list';
import {
  useInboxCounts,
  useInboxMessages,
  useInboxTimezone,
} from '@/features/mailbox/hooks/use-inbox';
import { useMailboxDomains, useMailboxes } from '@/features/mailbox/hooks/use-mailbox';
import { useMailOrganization } from '@/features/mailbox/hooks/use-mail-organization';
import { INBOX_VIEWS, MAIL_SEARCH_MAX_LENGTH } from '@/features/mailbox/schemas';
import type {
  InboxCounts,
  InboxFilters,
  InboxItem,
  InboxView,
  MailMessage,
  Mailbox,
} from '@/features/mailbox/types';
import { isOrganizationLost, resolveMailboxErrorKey } from '@/features/mailbox/utils/mailbox-error';
import { ApiErrorCode } from '@/http/codes';
import { ApiError } from '@/http/request';
import { useT } from '@/i18n';
import { cn } from '@/utils';

/** Query parameter naming the open row of the unified inbox, so a reload keeps it open. */
export const OPEN_PARAM = 'open';
/** The URL state of the unified inbox (D20): the view, one mailbox, the search, the Unread tab. */
const VIEW_PARAM = 'view';
const BOX_PARAM = 'box';
const SEARCH_PARAM = 'q';
const TAB_PARAM = 'tab';
const UNREAD_TAB = 'unread';
const filterParams = [VIEW_PARAM, BOX_PARAM, SEARCH_PARAM, TAB_PARAM] as const;
const rowIdPattern = /^[1-9]\d{0,15}$/u;
const fallbackTimeZone = 'UTC';

/** What the URL asks the inbox to list. */
interface InboxUrlState {
  view: InboxView;
  box: number | null;
  q: string;
  unread: boolean;
}

/**
 * The inbox's URL state; a value it cannot use falls back to the default without a word. A
 * search lists `all`, the only view it combines with.
 */
function readInboxUrl(params: URLSearchParams): InboxUrlState {
  const rawSearch = params.get(SEARCH_PARAM) ?? '';
  const q =
    Array.from(rawSearch).length <= MAIL_SEARCH_MAX_LENGTH && !/\p{Cc}/u.test(rawSearch)
      ? rawSearch.trim()
      : '';
  const rawView = params.get(VIEW_PARAM) ?? '';
  const view =
    q === '' && (INBOX_VIEWS as readonly string[]).includes(rawView)
      ? (rawView as InboxView)
      : 'all';
  const rawBox = params.get(BOX_PARAM) ?? '';
  return {
    view,
    box: rowIdPattern.test(rawBox) ? Number(rawBox) : null,
    q,
    unread: params.get(TAB_PARAM) === UNREAD_TAB,
  };
}

/**
 * The list query for the URL state. `all` and `sent` group by thread, except for the Unread tab
 * and a search, which list single messages.
 */
function inboxFilters({ view, box, q, unread }: InboxUrlState, tz: string): InboxFilters {
  const grouped = !unread && q === '' && (view === 'all' || view === 'sent');
  return {
    view,
    ...(box === null ? {} : { mailboxId: box }),
    ...(unread ? { unread: true } : {}),
    ...(q === '' ? {} : { q }),
    ...(grouped ? { group: 'thread' as const } : {}),
    tz,
  };
}

/** The unread number the counts hold for this listing, where they hold one. */
function listingUnread(counts: InboxCounts | undefined, state: InboxUrlState): number | undefined {
  if (!counts || state.q !== '') return undefined;
  if (state.box !== null) {
    if (state.view !== 'all') return undefined;
    return counts.mailboxes.find(item => item.mailbox_id === state.box)?.unread;
  }
  if (state.view === 'all') return counts.views.all_unread;
  return state.view === 'today' ? counts.views.today_unread : undefined;
}

/** The inbox refused the query: a filter of the URL, or else the zone, is not one it takes. */
function isRejectedQuery(error: unknown): boolean {
  return (
    error instanceof ApiError &&
    error.status === 400 &&
    error.errorCode === ApiErrorCode.COMMON_INVALID_INPUT
  );
}

function isSearchTooBroad(error: unknown): boolean {
  return error instanceof ApiError && error.errorCode === ApiErrorCode.MAILBOX_SEARCH_TOO_BROAD;
}

/**
 * The default mail page: every mailbox the user reads in one list, as three panes (the mailboxes,
 * the list grouped by thread, and the reading pane). One mailbox's own workspace stays one click
 * away under `?mailbox=`.
 */
export function InboxWorkspace() {
  const t = useT('mailbox');
  const scope = useMailOrganization();
  return (
    <div className="space-y-5">
      <MailPageHeader title={t('inbox.title')} description={t('inbox.description')} scope={scope} />
      <MailOrganizationBoundary scope={scope}>
        {organization => <UnifiedInbox canManage={organization.canManage} />}
      </MailOrganizationBoundary>
    </div>
  );
}

function UnifiedInbox({ canManage }: { canManage: boolean }) {
  const t = useT('mailbox');
  const organizationId = useMailOrganizationId();
  const router = useRouter();
  const pathname = usePathname();
  const searchParams = useSearchParams();
  const mailboxes = useMailboxes(organizationId, { live: true });
  const inboxZone = useInboxTimezone();
  const [zoneRefused, setZoneRefused] = useState(false);
  const timeZone = zoneRefused ? fallbackTimeZone : inboxZone;
  const urlState = readInboxUrl(searchParams);
  const filters = inboxFilters(urlState, timeZone);
  const inbox = useInboxMessages(organizationId, filters);
  const counts = useInboxCounts(organizationId, timeZone);
  const [held, setHeld] = useState<InboxItem | null>(null);
  const [draft, setDraft] = useState<{ mailbox: Mailbox; draft: ComposeDraft } | null>(null);
  const formatter = useDateTimeFormat();

  /** Changes the URL state in place; every change of what is listed closes the open row. */
  const replaceParams = useCallback(
    (change: (next: URLSearchParams) => void) => {
      const next = new URLSearchParams(window.location.search);
      change(next);
      const query = next.toString();
      router.replace(query ? `${pathname}?${query}` : pathname, { scroll: false });
    },
    [pathname, router]
  );
  const setListing = (changes: Partial<Record<(typeof filterParams)[number], string | null>>) =>
    replaceParams(next => {
      next.delete(OPEN_PARAM);
      for (const [name, value] of Object.entries(changes)) {
        if (value === null) next.delete(name);
        else next.set(name, value);
      }
    });

  // A 400 means the inbox refused the query (§11.3). With filters in the URL they are the cause,
  // so the URL goes back to `view=all`; without any, only the zone can be, and UTC is always known.
  // A listing already on screen keeps its URL when a later page fails.
  const rejected = !inbox.data && isRejectedQuery(inbox.error);
  const filtered = filterParams.some(name => searchParams.has(name));
  const resetting = rejected && (filtered || timeZone !== fallbackTimeZone);
  useEffect(() => {
    if (!rejected) return;
    if (filtered) {
      replaceParams(next => {
        for (const name of filterParams) next.delete(name);
      });
    } else if (timeZone !== fallbackTimeZone) {
      setZoneRefused(true);
    }
  }, [rejected, filtered, timeZone, replaceParams]);

  const { refetch: refetchInbox, hasNextPage, isFetchingNextPage, fetchNextPage } = inbox;
  const refetchMailboxes = mailboxes.refetch;
  const loadMore = useCallback(() => {
    if (hasNextPage && !isFetchingNextPage) void fetchNextPage();
  }, [hasNextPage, isFetchingNextPage, fetchNextPage]);
  const refreshListing = useCallback(() => {
    void refetchInbox();
    void refetchMailboxes();
  }, [refetchInbox, refetchMailboxes]);

  const pages = inbox.data?.pages ?? [];
  const rows = pages.flatMap(page => page.items);
  const rawOpen = searchParams.get(OPEN_PARAM) ?? '';
  const openId = rowIdPattern.test(rawOpen) ? Number(rawOpen) : null;
  const listed = openId === null ? undefined : rows.find(row => row.id === openId);
  // The open row stays in the pane when a refresh drops it from the list.
  if (listed && listed !== held) setHeld(listed);
  const openItem = listed ?? (held !== null && held.id === openId ? held : null);

  const setOpenParam = (id: number | null) =>
    replaceParams(next => {
      if (id === null) next.delete(OPEN_PARAM);
      else next.set(OPEN_PARAM, String(id));
    });
  const mailboxHref = (mailboxId: number) => {
    const next = new URLSearchParams(searchParams.toString());
    for (const name of [OPEN_PARAM, ...filterParams]) next.delete(name);
    next.set(MAILBOX_PARAM, String(mailboxId));
    return `${pathname}?${next.toString()}`;
  };

  if (mailboxes.isPending) return <InboxSkeleton />;
  if (isOrganizationLost(mailboxes.error) || isOrganizationLost(inbox.error)) {
    return <OrganizationLostState />;
  }
  if (!mailboxes.data) {
    return (
      <ErrorAlert
        message={t(resolveMailboxErrorKey(mailboxes.error))}
        retryLabel={t('actions.retry')}
        onRetry={() => void mailboxes.refetch()}
      />
    );
  }

  const readable = mailboxes.data.filter(mailbox => mailbox.access.view);
  if (readable.length === 0) {
    return (
      <EmptyState
        icon={<Mail className="size-5" aria-hidden="true" />}
        title={t('inbox.empty.noMailboxes')}
        description={t(
          canManage ? 'inbox.empty.noMailboxesManage' : 'inbox.empty.noMailboxesMember'
        )}
        action={
          canManage ? (
            <ManageMailboxesAction
              mailboxes={mailboxes.data}
              onRefresh={() => void mailboxes.refetch()}
            />
          ) : undefined
        }
      />
    );
  }

  const openMailbox = openItem
    ? (mailboxes.data.find(mailbox => mailbox.id === openItem.mailbox_id) ?? null)
    : null;
  const openIndex = openItem ? rows.findIndex(row => row.id === openItem.id) : -1;
  const replyCopy = {
    prefixSubject: (subject: string) => t('compose.replySubject', { subject }),
    quotedHeader: (params: { date: string; sender: string }) => t('compose.quotedHeader', params),
    formatDate: (iso: string) => formatter.format(new Date(iso)),
  };
  const withSender = (build: (mailbox: Mailbox) => ComposeDraft) => {
    if (openMailbox) setDraft({ mailbox: openMailbox, draft: build(openMailbox) });
  };
  const navigation: InboxReaderNavigation = {
    onPrevious: openIndex > 0 ? () => setOpenParam(rows[openIndex - 1].id) : null,
    onNext:
      openIndex >= 0 && openIndex < rows.length - 1
        ? () => setOpenParam(rows[openIndex + 1].id)
        : null,
    onClose: () => setOpenParam(null),
    onReply: (message: MailMessage) => withSender(() => quotedReplyDraft(message, replyCopy)),
    onReplyAll: (message: MailMessage) =>
      withSender(mailbox => replyAllDraft(message, mailbox.address, replyCopy)),
    onForward: (message: MailMessage) =>
      withSender(() =>
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

  const narrowedTo =
    urlState.box === null ? null : (readable.find(mailbox => mailbox.id === urlState.box) ?? null);
  let title: string;
  if (urlState.box !== null) title = narrowedTo?.address ?? t('inbox.views.all');
  else if (urlState.q !== '') title = t('inbox.search.results', { q: urlState.q });
  else title = t(`inbox.views.${urlState.view}`);
  const emptyTitle =
    urlState.q !== ''
      ? t('inbox.empty.search')
      : urlState.unread
        ? t('inbox.empty.unread')
        : urlState.view === 'all'
          ? t('inbox.empty.view')
          : t(`inbox.empty.${urlState.view}`);

  let list: React.ReactNode;
  if (inbox.isPending || resetting) {
    list = <ListSkeleton />;
  } else if (!inbox.data && isSearchTooBroad(inbox.error)) {
    list = (
      <Alert>
        <AlertCircle className="size-4" aria-hidden="true" />
        <AlertDescription>{t(resolveMailboxErrorKey(inbox.error))}</AlertDescription>
      </Alert>
    );
  } else if (!inbox.data) {
    list = (
      <div className="space-y-3">
        <ErrorAlert
          message={t(resolveMailboxErrorKey(inbox.error))}
          retryLabel={t('actions.retry')}
          onRetry={() => void inbox.refetch()}
        />
        <Button asChild size="sm" variant="outline">
          <Link href={mailboxHref(readable[0].id)}>{t('inbox.viewByMailbox')}</Link>
        </Button>
      </div>
    );
  } else if (rows.length === 0) {
    list = (
      <EmptyState
        icon={<Inbox className="size-5" aria-hidden="true" />}
        title={emptyTitle}
        description={
          urlState.view === 'all' && urlState.q === '' && !urlState.unread
            ? t('inbox.empty.viewHint')
            : undefined
        }
      />
    );
  } else {
    const lastPage = pages[pages.length - 1];
    list = (
      <InboxList
        pages={pages}
        timeZone={timeZone}
        selectedId={openItem?.id ?? null}
        onOpen={row => setOpenParam(row.id)}
        onLoadMore={loadMore}
        hasMore={lastPage.meta.has_more && lastPage.meta.next_cursor !== null}
        loadingMore={isFetchingNextPage}
      />
    );
  }

  return (
    <div className="grid gap-4 lg:h-[calc(100dvh-12rem)] lg:min-h-[36rem] lg:grid-cols-[14rem_minmax(0,1fr)]">
      <aside className="flex min-h-0 flex-col gap-3 lg:overflow-y-auto lg:border-r lg:pr-4">
        <InboxSidebar
          counts={counts.data}
          mailboxes={readable}
          view={urlState.view}
          box={urlState.box}
          onView={view =>
            setListing({ [VIEW_PARAM]: view === 'all' ? null : view, [SEARCH_PARAM]: null })
          }
          onBox={mailboxId => setListing({ [BOX_PARAM]: String(mailboxId) })}
        />
      </aside>

      <div className="grid min-h-0 min-w-0 gap-4 xl:grid-cols-[minmax(20rem,28rem)_minmax(0,1fr)]">
        <section
          data-scroll-pane
          aria-label={title}
          className={cn(
            'min-h-0 min-w-0 space-y-3 lg:overflow-y-auto lg:pr-1',
            openItem && 'hidden xl:block'
          )}
        >
          <InboxListHeader
            title={title}
            unread={urlState.unread}
            unreadCount={listingUnread(counts.data, urlState)}
            onUnreadChange={unread => setListing({ [TAB_PARAM]: unread ? UNREAD_TAB : null })}
            q={urlState.q}
            onSearch={q => setListing({ [SEARCH_PARAM]: q === '' ? null : q, [VIEW_PARAM]: null })}
            mailboxHref={urlState.box === null ? null : mailboxHref(urlState.box)}
            onClearMailbox={
              urlState.box === null ? undefined : () => setListing({ [BOX_PARAM]: null })
            }
          />
          {list}
        </section>
        <section
          className={cn(
            'animate-enter min-h-0 min-w-0 lg:overflow-y-auto xl:border-l xl:pl-4',
            !openItem && 'hidden xl:block'
          )}
        >
          {openItem ? (
            <InboxReadingPane
              key={openItem.id}
              item={openItem}
              access={openMailbox?.access ?? null}
              navigation={navigation}
              onUnavailable={refreshListing}
            />
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

      {draft && draft.mailbox.access.send ? (
        <ComposeDialog
          mailbox={draft.mailbox}
          draft={draft.draft}
          onClose={() => setDraft(null)}
          onStored={() => setDraft(null)}
        />
      ) : null}
    </div>
  );
}

/**
 * Manage mailboxes for an owner or admin with nothing to read: create a mailbox or open one's
 * access to add themselves. The domains load only when this is shown.
 */
function ManageMailboxesAction({
  mailboxes,
  onRefresh,
}: {
  mailboxes: Mailbox[];
  onRefresh: () => void;
}) {
  const t = useT('mailbox');
  const organizationId = useMailOrganizationId();
  const domains = useMailboxDomains(organizationId);
  const [open, setOpen] = useState(false);
  const [accessTargetId, setAccessTargetId] = useState<number | null>(null);
  const domainList = domains.data ?? [];
  const domain = domainList.find(item => item.kind === 'shared') ?? domainList[0] ?? null;
  const accessTarget = mailboxes.find(item => item.id === accessTargetId) ?? null;

  return (
    <>
      <Button
        type="button"
        size="sm"
        variant="outline"
        icon={<Settings2 className="size-4" />}
        disabled={!domains.data}
        onClick={() => setOpen(true)}
      >
        {t('switcher.manage')}
      </Button>
      {open ? (
        <ManageMailboxesDialog
          open
          mailboxes={mailboxes}
          domain={domain}
          domains={domainList}
          onClose={() => setOpen(false)}
          onCreated={() => setOpen(false)}
          onOpenAccess={mailbox => {
            setOpen(false);
            setAccessTargetId(mailbox.id);
          }}
        />
      ) : null}
      {accessTarget ? (
        <MailboxAccessPanel
          mailbox={accessTarget}
          onClose={() => setAccessTargetId(null)}
          onRefresh={onRefresh}
        />
      ) : null}
    </>
  );
}

function InboxSkeleton() {
  return (
    <div className="grid gap-4 lg:grid-cols-[14rem_minmax(0,1fr)]" aria-hidden="true">
      <div className="space-y-2">
        <Skeleton className="h-9 w-full" />
        <Skeleton className="h-40 w-full" />
      </div>
      <ListSkeleton />
    </div>
  );
}
