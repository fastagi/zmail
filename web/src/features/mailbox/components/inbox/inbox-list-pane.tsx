'use client';

import { lazy, Suspense, type RefObject } from 'react';
import Link from 'next/link';
import { AlertCircle, Inbox } from 'lucide-react';

import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { isWebFeatureEnabled } from '@/config/features';
import type { ExternalConnectPreset } from '@/features/mailbox/components/external-connect-dialog';
import {
  ATTACHMENTS_TAB,
  BOX_PARAM,
  DATE_PARAM,
  isSearchTooBroad,
  listingUnread,
  ROLE_PARAM,
  SEARCH_PARAM,
  TAB_PARAM,
  TRIAGE_PARAM,
  UNREAD_TAB,
  VIEW_PARAM,
  type filterParams,
  type InboxUrlState,
} from '@/features/mailbox/components/inbox/inbox-url';
import { InboxBulkBar } from '@/features/mailbox/components/inbox/inbox-bulk-bar';
import type {
  InboxBulkAction,
  InboxQuickAction,
} from '@/features/mailbox/components/inbox/use-inbox-actions';
import {
  InboxList,
  InboxListHeader,
  InboxListSkeleton,
} from '@/features/mailbox/components/inbox-list';
import { EmptyState, ErrorAlert } from '@/features/mailbox/components/mail-shared';
import type {
  ExternalMailConnection,
  ExternalMailConnector,
} from '@/features/mailbox/external-mail-schemas';
import type {
  InboxCounts,
  InboxItem,
  InboxPage,
  InboxTriageCounts,
  MailRole,
  Mailbox,
} from '@/features/mailbox/types';
import { resolveMailboxErrorKey } from '@/features/mailbox/utils/mailbox-error';
import { useT } from '@/i18n';
import { cn } from '@/utils';

// The triage tabs load only for an organization with AI triage on.
const InboxTriageTabs = lazy(async () => ({
  default: (await import('@/features/mailbox/components/inbox-triage')).InboxTriageTabs,
}));
// External reconnect notice loads only when a connection needs it.
const ExternalReconnectBanner = lazy(async () => ({
  default: (await import('@/features/mailbox/components/external-reconnect-banner'))
    .ExternalReconnectBanner,
}));
// The new-mail alerts offer exists only with the notification feature, which owns web push.
const webPushOffered = isWebFeatureEnabled('notification');
const WebPushBanner = lazy(async () => ({
  default: (await import('@/features/notification/components/web-push-banner')).WebPushBanner,
}));

export function InboxListPane({
  organizationId,
  title,
  subtitle,
  narrowedTo,
  urlState,
  counts,
  triageCounts,
  assignmentTabs,
  zoneReady,
  timeZone,
  inboxPending,
  inboxFetching,
  inboxHasData,
  inboxError,
  resetting,
  pages,
  rows,
  roleTags,
  showMailbox,
  sendableIds,
  openItem,
  readable,
  isFetchingNextPage,
  connectionList,
  connectors,
  searchRef,
  onRefresh,
  onRefetchInbox,
  onSetListing,
  onBox,
  onOpenRow,
  onLoadMore,
  onShowSidebar,
  onReconnectWithPassword,
  mailboxHref,
  checkedIds,
  selectedItems = [],
  bulkPending = false,
  onToggleSelect,
  onToggleSelectAll,
  onClearSelection,
  onQuickAction,
  onBulkAction,
}: {
  organizationId: number;
  title: string;
  subtitle: string | undefined;
  narrowedTo: Mailbox | null;
  urlState: InboxUrlState;
  counts: InboxCounts | undefined;
  triageCounts: InboxTriageCounts | null;
  assignmentTabs: boolean;
  zoneReady: boolean;
  timeZone: string;
  inboxPending: boolean;
  inboxFetching: boolean;
  inboxHasData: boolean;
  inboxError: unknown;
  resetting: boolean;
  pages: InboxPage[];
  rows: InboxItem[];
  roleTags: Map<number, MailRole>;
  showMailbox: boolean;
  sendableIds: Set<number>;
  openItem: InboxItem | null;
  readable: Mailbox[];
  isFetchingNextPage: boolean;
  connectionList: ExternalMailConnection[];
  connectors: ExternalMailConnector[] | undefined;
  searchRef: RefObject<HTMLInputElement | null>;
  onRefresh: () => void;
  onRefetchInbox: () => void;
  onSetListing: (changes: Partial<Record<(typeof filterParams)[number], string | null>>) => void;
  onBox: (mailboxId: number) => void;
  onOpenRow: (rowId: number) => void;
  onLoadMore: () => void;
  onShowSidebar: () => void;
  onReconnectWithPassword: (preset: ExternalConnectPreset) => void;
  mailboxHref: (mailboxId: number) => string;
  checkedIds?: ReadonlySet<number>;
  selectedItems?: readonly InboxItem[];
  bulkPending?: boolean;
  onToggleSelect?: (id: number) => void;
  onToggleSelectAll?: () => void;
  onClearSelection?: () => void;
  onQuickAction?: (item: InboxItem, action: InboxQuickAction) => void;
  onBulkAction?: (action: InboxBulkAction) => void;
}) {
  const t = useT('mailbox');

  // A date or attachment filter that hides every row is not an empty mailbox.
  const dateOrFiles = urlState.date !== null || urlState.attachments;
  const narrowed = urlState.q === '' && dateOrFiles;
  const emptyTitle =
    urlState.q !== ''
      ? t('inbox.empty.search')
      : narrowed
        ? t('inbox.empty.filtered')
        : urlState.assignment
          ? t(`inbox.empty.${urlState.assignment}`)
          : urlState.unread
            ? t('inbox.empty.unread')
            : urlState.view === 'all'
              ? t('inbox.empty.view')
              : t(`inbox.empty.${urlState.view}`);
  const emptyHint =
    urlState.q !== ''
      ? t(dateOrFiles ? 'inbox.empty.searchFilteredHint' : 'inbox.empty.searchHint')
      : narrowed
        ? t('inbox.empty.filteredHint')
        : urlState.assignment
          ? t(`inbox.empty.${urlState.assignment}Hint`)
          : urlState.unread
            ? t('inbox.empty.unreadHint')
            : urlState.view === 'all'
              ? t('inbox.empty.viewHint')
              : t(`inbox.empty.${urlState.view}Hint`);

  let list: React.ReactNode;
  if (inboxPending || resetting) {
    list = <InboxListSkeleton />;
  } else if (!inboxHasData && isSearchTooBroad(inboxError)) {
    list = (
      <Alert className="mx-3 w-auto">
        <AlertCircle className="size-4" aria-hidden="true" />
        <AlertDescription>{t(resolveMailboxErrorKey(inboxError))}</AlertDescription>
      </Alert>
    );
  } else if (!inboxHasData) {
    list = (
      <div className="space-y-3 px-3">
        <ErrorAlert
          message={t(resolveMailboxErrorKey(inboxError))}
          retryLabel={t('actions.retry')}
          onRetry={onRefetchInbox}
        />
        <Button asChild size="sm" variant="outline">
          <Link href={mailboxHref(readable[0].id)}>{t('inbox.viewByMailbox')}</Link>
        </Button>
      </div>
    );
  } else if (rows.length === 0) {
    list = (
      <div className="px-3">
        <EmptyState
          icon={<Inbox className="size-5" aria-hidden="true" />}
          title={emptyTitle}
          description={emptyHint}
          action={
            dateOrFiles ? (
              <Button
                size="sm"
                variant="outline"
                onClick={() =>
                  onSetListing({
                    [DATE_PARAM]: null,
                    ...(urlState.attachments ? { [TAB_PARAM]: null } : {}),
                  })
                }
              >
                {t('inbox.empty.clearFilters')}
              </Button>
            ) : undefined
          }
        />
      </div>
    );
  } else {
    const lastPage = pages[pages.length - 1];
    list = (
      <InboxList
        label={title}
        pages={pages}
        roles={roleTags}
        // A single mailbox needs no label on every row.
        showMailbox={showMailbox}
        sendable={sendableIds}
        view={urlState.view}
        timeZone={timeZone}
        onFilterMailbox={onBox}
        selectedId={openItem?.id ?? null}
        onOpen={row => onOpenRow(row.id)}
        onLoadMore={onLoadMore}
        hasMore={lastPage.meta.has_more && lastPage.meta.next_cursor !== null}
        loadingMore={isFetchingNextPage}
        onQuickAction={onQuickAction}
        checkedIds={checkedIds}
        onToggleSelect={onToggleSelect}
      />
    );
  }

  return (
    <section
      aria-label={title}
      className={cn(
        'my-2.5 ml-2.5 flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden rounded-[14px] border border-border-main bg-bg-surface shadow-sm max-lg:mr-2.5 md:ml-0',
        'lg:w-[380px] lg:flex-none lg:rounded-r-none lg:border-y-0 xl:w-[470px]',
        openItem && 'max-lg:hidden'
      )}
    >
      <InboxListHeader
        onRefresh={onRefresh}
        refreshing={!zoneReady || inboxFetching}
        title={title}
        mailboxSelected={narrowedTo !== null}
        copyAddress={narrowedTo?.address ?? null}
        subtitle={subtitle}
        unread={urlState.unread}
        unreadCount={listingUnread(counts, urlState)}
        onUnreadChange={unread => onSetListing({ [TAB_PARAM]: unread ? UNREAD_TAB : null })}
        attachments={urlState.attachments}
        onAttachmentsChange={on => onSetListing({ [TAB_PARAM]: on ? ATTACHMENTS_TAB : null })}
        dateRange={urlState.date}
        onDateRangeChange={date => onSetListing({ [DATE_PARAM]: date })}
        assignment={urlState.assignment}
        onAssignmentChange={
          assignmentTabs ? assignment => onSetListing({ [TAB_PARAM]: assignment }) : undefined
        }
        q={urlState.q}
        onSearch={q => onSetListing({ [SEARCH_PARAM]: q === '' ? null : q, [VIEW_PARAM]: null })}
        mailboxHref={urlState.box === null ? null : mailboxHref(urlState.box)}
        onClearMailbox={
          urlState.box === null && urlState.role === null
            ? undefined
            : () => onSetListing({ [BOX_PARAM]: null, [ROLE_PARAM]: null })
        }
        onShowSidebar={onShowSidebar}
        searchRef={searchRef}
      />
      {connectionList.some(connection => connection.status === 'needs_reconnect') ? (
        <Suspense fallback={null}>
          <ExternalReconnectBanner
            organizationId={organizationId}
            connections={connectionList}
            connectors={connectors}
            onReconnectWithPassword={onReconnectWithPassword}
          />
        </Suspense>
      ) : null}
      {webPushOffered ? (
        <Suspense fallback={null}>
          <WebPushBanner />
        </Suspense>
      ) : null}
      {triageCounts !== null &&
      urlState.q === '' &&
      (urlState.view === 'all' || urlState.view === 'today') ? (
        <Suspense fallback={null}>
          <InboxTriageTabs
            counts={triageCounts}
            active={urlState.triage}
            onChange={tab => onSetListing({ [TRIAGE_PARAM]: tab })}
          />
        </Suspense>
      ) : null}
      {selectedItems.length > 0 && onToggleSelectAll && onClearSelection && onBulkAction ? (
        <InboxBulkBar
          selectedItems={selectedItems}
          totalCount={rows.length}
          sendable={sendableIds}
          view={urlState.view}
          busy={bulkPending}
          onToggleAll={onToggleSelectAll}
          onClear={onClearSelection}
          onAction={onBulkAction}
        />
      ) : null}
      <div data-scroll-pane className="min-h-0 flex-1 overflow-y-auto">
        {list}
      </div>
    </section>
  );
}
