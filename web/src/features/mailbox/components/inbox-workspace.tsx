'use client';

import { useCallback, useEffect, useState } from 'react';
import Link from 'next/link';
import { usePathname, useRouter, useSearchParams } from 'next/navigation';
import { Inbox, Mail, MailOpen, Settings2 } from 'lucide-react';

import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import {
  ComposeDialog,
  forwardDraft,
  quotedReplyDraft,
  replyAllDraft,
  type ComposeDraft,
} from '@/features/mailbox/components/compose-dialog';
import { InboxList } from '@/features/mailbox/components/inbox-list';
import {
  InboxReadingPane,
  type InboxReaderNavigation,
} from '@/features/mailbox/components/inbox-reading-pane';
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
import { useInboxMessages } from '@/features/mailbox/hooks/use-inbox';
import { useMailboxDomains, useMailboxes } from '@/features/mailbox/hooks/use-mailbox';
import { useMailOrganization } from '@/features/mailbox/hooks/use-mail-organization';
import type { InboxFilters, InboxItem, MailMessage, Mailbox } from '@/features/mailbox/types';
import { isOrganizationLost, resolveMailboxErrorKey } from '@/features/mailbox/utils/mailbox-error';
import { ApiErrorCode } from '@/http/codes';
import { ApiError } from '@/http/request';
import { useT } from '@/i18n';
import { cn } from '@/utils';

/** Query parameter naming the open row of the unified inbox, so a reload keeps it open. */
export const OPEN_PARAM = 'open';
const rowIdPattern = /^[1-9]\d{0,15}$/u;
const fallbackTimeZone = 'UTC';

/** The browser's IANA zone, which decides where "today" starts; UTC when it has none. */
function browserTimeZone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || fallbackTimeZone;
  } catch {
    return fallbackTimeZone;
  }
}

/** The inbox refused the query; with fixed filters only the browser's zone can be the cause. */
function isRejectedQuery(error: unknown): boolean {
  return (
    error instanceof ApiError &&
    error.status === 400 &&
    error.errorCode === ApiErrorCode.COMMON_INVALID_INPUT
  );
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
  const [timeZone, setTimeZone] = useState(browserTimeZone);
  const filters: InboxFilters = { view: 'all', group: 'thread', tz: timeZone };
  const inbox = useInboxMessages(organizationId, filters);
  const [held, setHeld] = useState<InboxItem | null>(null);
  const [draft, setDraft] = useState<{ mailbox: Mailbox; draft: ComposeDraft } | null>(null);
  const formatter = useDateTimeFormat();

  // A zone the server does not know would leave the inbox blank; UTC is always known.
  const zoneRejected = timeZone !== fallbackTimeZone && isRejectedQuery(inbox.error);
  useEffect(() => {
    if (zoneRejected) setTimeZone(fallbackTimeZone);
  }, [zoneRejected]);

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

  const setOpenParam = (id: number | null) => {
    const next = new URLSearchParams(window.location.search);
    if (id === null) next.delete(OPEN_PARAM);
    else next.set(OPEN_PARAM, String(id));
    const query = next.toString();
    router.replace(query ? `${pathname}?${query}` : pathname, { scroll: false });
  };
  const mailboxHref = (mailboxId: number) => {
    const next = new URLSearchParams(searchParams.toString());
    next.delete(OPEN_PARAM);
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

  let list: React.ReactNode;
  if (inbox.isPending || zoneRejected) {
    list = <ListSkeleton />;
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
        title={t('inbox.empty.view')}
        description={t('inbox.empty.viewHint')}
      />
    );
  } else {
    const lastPage = pages[pages.length - 1];
    list = (
      <InboxList
        pages={pages}
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
        <nav aria-label={t('views.label')} className="space-y-1">
          <span
            aria-current="page"
            className="flex items-center gap-2 rounded-md bg-accent px-3 py-2 text-sm font-medium"
          >
            <Inbox className="size-4" aria-hidden="true" />
            {t('inbox.allMail')}
          </span>
        </nav>
        <nav aria-label={t('inbox.mailboxesLabel')} className="space-y-1">
          <p className="px-3 text-xs font-medium text-muted-foreground">
            {t('inbox.mailboxesLabel')}
          </p>
          {readable.map(mailbox => (
            <Link
              key={mailbox.id}
              href={mailboxHref(mailbox.id)}
              className="block truncate rounded-md px-3 py-1.5 text-sm text-muted-foreground transition-colors focus-ring hover:bg-accent/70 hover:text-foreground"
            >
              {mailbox.address}
            </Link>
          ))}
        </nav>
      </aside>

      <div className="grid min-h-0 min-w-0 gap-4 xl:grid-cols-[minmax(20rem,28rem)_minmax(0,1fr)]">
        <section
          data-scroll-pane
          aria-label={t('inbox.allMail')}
          className={cn(
            'min-h-0 min-w-0 space-y-3 lg:overflow-y-auto lg:pr-1',
            openItem && 'hidden xl:block'
          )}
        >
          <h2 className="text-base font-semibold">{t('inbox.allMail')}</h2>
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
