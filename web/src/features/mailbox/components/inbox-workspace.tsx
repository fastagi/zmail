'use client';

import { lazy, Suspense, useCallback, useEffect, useRef, useState } from 'react';
import Link from 'next/link';
import { usePathname, useRouter, useSearchParams } from 'next/navigation';
import { AlertCircle, Inbox, Mail, MailOpen, Settings2 } from 'lucide-react';
import { toast } from 'sonner';

import { Alert, AlertDescription } from '@/components/ui/alert';

import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import {
  ComposeDialog,
  emptyDraft,
  forwardDraft,
  quotedReplyDraft,
  replyAllDraft,
  type ComposeDraft,
} from '@/features/mailbox/components/compose-dialog';
import {
  InboxList,
  InboxListHeader,
  InboxListHeaderSkeleton,
  InboxListSkeleton,
} from '@/features/mailbox/components/inbox-list';
import type {
  InboxPaneActions,
  InboxReaderNavigation,
} from '@/features/mailbox/components/inbox-reading-pane';
import {
  InboxSidebar,
  visibleRoles,
  type InboxRoleFilter,
} from '@/features/mailbox/components/inbox-sidebar';
import { MAILBOX_PARAM, readStoredMailboxId } from '@/features/mailbox/components/mailbox-params';
import {
  MailOrganizationBoundary,
  MailOrganizationName,
  MailPageHeader,
  OrganizationLostState,
  mailPageClassName,
  useMailOrganizationId,
} from '@/features/mailbox/components/mail-organization';
import {
  EmptyState,
  ErrorAlert,
  ReaderArticleSkeleton,
  useDateTimeFormat,
} from '@/features/mailbox/components/mail-shared';
import type { PaletteCommand } from '@/features/mailbox/components/command-palette';
import {
  useInboxCounts,
  useInboxMessages,
  useInboxTimezone,
  useMailRoles,
} from '@/features/mailbox/hooks/use-inbox';
import { useMailShortcuts, type MailShortcut } from '@/features/mailbox/hooks/use-mail-shortcuts';
import { useMailboxDomains, useMailboxes } from '@/features/mailbox/hooks/use-mailbox';
import {
  useMailOrganization,
  type MailOrganizationScope,
} from '@/features/mailbox/hooks/use-mail-organization';
import { INBOX_VIEWS, MAIL_SEARCH_MAX_LENGTH } from '@/features/mailbox/schemas';
import type {
  InboxCounts,
  InboxFilters,
  InboxItem,
  InboxView,
  MailMessage,
  MailRole,
  Mailbox,
} from '@/features/mailbox/types';
import { isOrganizationLost, resolveMailboxErrorKey } from '@/features/mailbox/utils/mailbox-error';
import { ApiErrorCode } from '@/http/codes';
import { ApiError } from '@/http/request';
import { useT } from '@/i18n';
import { cn } from '@/utils';

// Administration dialogs load when opened, not with the mail page.
const ManageRolesDialog = lazy(async () => {
  const feature = await import('@/features/mailbox/components/manage-roles-dialog');
  return { default: feature.ManageRolesDialog };
});
const ManageMailboxesDialog = lazy(async () => {
  const feature = await import('@/features/mailbox/components/manage-mailboxes-dialog');
  return { default: feature.ManageMailboxesDialog };
});
// The reading pane, with the thread and message readers, loads when a row first opens.
const InboxReadingPane = lazy(async () => ({
  default: (await import('@/features/mailbox/components/inbox-reading-pane')).InboxReadingPane,
}));
const ShortcutHelpDialog = lazy(async () => ({
  default: (await import('@/features/mailbox/components/shortcut-help-dialog')).ShortcutHelpDialog,
}));
const CommandPalette = lazy(async () => ({
  default: (await import('@/features/mailbox/components/command-palette')).CommandPalette,
}));
// The sidebar sheet of narrow screens loads when first opened.
const Sheet = lazy(async () => ({ default: (await import('@/components/ui/sheet')).Sheet }));
const SheetContent = lazy(async () => ({
  default: (await import('@/components/ui/sheet')).SheetContent,
}));
const SheetTitle = lazy(async () => ({
  default: (await import('@/components/ui/sheet')).SheetTitle,
}));
const MailboxAccessPanel = lazy(async () => {
  const feature = await import('@/features/mailbox/components/mailbox-access-panel');
  return { default: feature.MailboxAccessPanel };
});

/** Query parameter naming the open row of the unified inbox, so a reload keeps it open. */
export const OPEN_PARAM = 'open';
/**
 * The URL state of the unified inbox (D20): the view, one mailbox or one mail role (`none` for the
 * mailboxes without one), the search, the Unread tab.
 */
const VIEW_PARAM = 'view';
const BOX_PARAM = 'box';
const ROLE_PARAM = 'role';
const SEARCH_PARAM = 'q';
const TAB_PARAM = 'tab';
const UNREAD_TAB = 'unread';
const NO_ROLE = 'none';
const filterParams = [VIEW_PARAM, BOX_PARAM, ROLE_PARAM, SEARCH_PARAM, TAB_PARAM] as const;
const rowIdPattern = /^[1-9]\d{0,15}$/u;
const fallbackTimeZone = 'UTC';

/** What the URL asks the inbox to list. */
interface InboxUrlState {
  view: InboxView;
  box: number | null;
  role: InboxRoleFilter | null;
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
  const rawRole = params.get(ROLE_PARAM) ?? '';
  let role: InboxRoleFilter | null = null;
  if (rawRole === NO_ROLE) role = NO_ROLE;
  else if (rowIdPattern.test(rawRole)) role = Number(rawRole);
  return {
    view,
    box: rowIdPattern.test(rawBox) ? Number(rawBox) : null,
    role,
    q,
    unread: params.get(TAB_PARAM) === UNREAD_TAB,
  };
}

/**
 * The list query for the URL state. `all` and `sent` group by thread, except for the Unread tab
 * and a search, which list single messages.
 */
function inboxFilters({ view, box, role, q, unread }: InboxUrlState, tz: string): InboxFilters {
  const grouped = !unread && q === '' && (view === 'all' || view === 'sent');
  return {
    view,
    ...(role === null ? {} : { roleId: role }),
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
    if (state.view !== 'all' || state.role !== null) return undefined;
    return counts.mailboxes.find(item => item.mailbox_id === state.box)?.unread;
  }
  if (state.role !== null) {
    if (state.view !== 'all') return undefined;
    if (state.role === NO_ROLE) return counts.ungrouped_unread;
    const role = state.role;
    return counts.roles.find(item => item.role_id === role)?.unread ?? 0;
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

type ReadyScope = Extract<MailOrganizationScope, { status: 'ready' }>;

/**
 * The default mail page: every mailbox the user reads in one list, as three panes (§11.0: the
 * 248px sidebar, the 470px list grouped by thread, and the reading pane), filling the console's
 * content area. One mailbox's own workspace stays one click away under `?mailbox=`.
 *
 * Responsive (§11.11): three panes from 1280px; from 1024px the sidebar folds to an icon rail;
 * below 1024px the list and the open mail take turns, with a way back; below 768px the sidebar
 * opens as a sheet. While the organization and the mailboxes load, the three panes show as
 * skeletons; without an organization, and when there is nothing to list, the page keeps the
 * console's ordinary header and frame.
 */
export function InboxWorkspace() {
  const t = useT('mailbox');
  const scope = useMailOrganization();
  // The three panes stand while the organization resolves, so the page does not change shape.
  if (scope.status === 'loading') return <InboxSkeleton />;
  if (scope.status !== 'ready') {
    return (
      <div className={cn(mailPageClassName, 'space-y-5')}>
        <MailPageHeader
          title={t('inbox.title')}
          description={t('inbox.description')}
          scope={scope}
        />
        <MailOrganizationBoundary scope={scope}>{() => null}</MailOrganizationBoundary>
      </div>
    );
  }
  return (
    <MailOrganizationBoundary scope={scope}>
      {organization => <UnifiedInbox canManage={organization.canManage} scope={scope} />}
    </MailOrganizationBoundary>
  );
}

/** A state of the page with nothing to list, in the console's ordinary frame and header. */
function InboxFrame({ scope, children }: { scope: ReadyScope; children: React.ReactNode }) {
  const t = useT('mailbox');
  return (
    <div className={cn(mailPageClassName, 'space-y-5')}>
      <MailPageHeader title={t('inbox.title')} description={t('inbox.description')} scope={scope} />
      {children}
    </div>
  );
}

function UnifiedInbox({ canManage, scope }: { canManage: boolean; scope: ReadyScope }) {
  const t = useT('mailbox');
  const organizationId = useMailOrganizationId();
  const router = useRouter();
  const pathname = usePathname();
  const searchParams = useSearchParams();
  const mailboxes = useMailboxes(organizationId, { live: true });
  const roles = useMailRoles(organizationId);
  const { zone: inboxZone, ready: zoneReady } = useInboxTimezone();
  // The zone the inbox refused; UTC stands in for it only while it is still the inbox's zone.
  const [refusedZone, setRefusedZone] = useState<string | null>(null);
  const timeZone = refusedZone === inboxZone ? fallbackTimeZone : inboxZone;
  const urlState = readInboxUrl(searchParams);
  const filters = inboxFilters(urlState, timeZone);
  const inbox = useInboxMessages(organizationId, filters, { enabled: zoneReady });
  const counts = useInboxCounts(organizationId, timeZone, { enabled: zoneReady });
  const [managingRoles, setManagingRoles] = useState(false);
  const [sidebarOpen, setSidebarOpen] = useState(false);
  // Mounted from the first open on, so the sheet can animate closed.
  const [sidebarSheet, setSidebarSheet] = useState(false);
  const [held, setHeld] = useState<InboxItem | null>(null);
  const [draft, setDraft] = useState<{
    mailbox: Mailbox;
    draft: ComposeDraft;
    /** The mailboxes a new message may be sent from; replies keep their own. */
    fromOptions?: readonly Mailbox[];
  } | null>(null);
  const formatter = useDateTimeFormat();
  // Mounted from the first open on, so the dialog can animate closed.
  const [helpOpen, setHelpOpen] = useState(false);
  const [helpMounted, setHelpMounted] = useState(false);
  const [paletteOpen, setPaletteOpen] = useState(false);
  const [paletteMounted, setPaletteMounted] = useState(false);
  const [managingMailboxes, setManagingMailboxes] = useState(false);
  const paneActions = useRef<InboxPaneActions | null>(null);
  const searchRef = useRef<HTMLInputElement | null>(null);
  /** `j` on the last loaded row asked for the next page: that row, whose successor opens next. */
  const [advanceAfter, setAdvanceAfter] = useState<number | null>(null);

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
  const setListing = (changes: Partial<Record<(typeof filterParams)[number], string | null>>) => {
    setAdvanceAfter(null);
    replaceParams(next => {
      next.delete(OPEN_PARAM);
      for (const [name, value] of Object.entries(changes)) {
        if (value === null) next.delete(name);
        else next.set(name, value);
      }
    });
  };

  // A 400 means the inbox refused the query (§11.3). With filters in the URL they are the cause,
  // so the URL goes back to `view=all` and the open row closes; without any, only the zone can be,
  // and UTC is always known. A listing already on screen keeps its URL when a later page fails.
  const rejected = !inbox.data && isRejectedQuery(inbox.error);
  const filtered = filterParams.some(name => searchParams.has(name));
  const resetting = rejected && (filtered || timeZone !== fallbackTimeZone);
  useEffect(() => {
    if (!rejected) return;
    if (filtered) {
      replaceParams(next => {
        for (const name of [OPEN_PARAM, ...filterParams]) next.delete(name);
      });
    } else if (timeZone !== fallbackTimeZone) {
      setRefusedZone(timeZone);
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

  // Once the next page asked for by `j` arrives, the row after the last one opens; with no more
  // pages, or with that row gone from the list, the wait ends.
  const advanceIndex = advanceAfter === null ? -1 : rows.findIndex(row => row.id === advanceAfter);
  const advanceTo = advanceIndex < 0 ? undefined : rows[advanceIndex + 1];
  const advanceDone =
    advanceAfter !== null && (advanceIndex < 0 || (!hasNextPage && !isFetchingNextPage));
  useEffect(() => {
    if (advanceTo) {
      setAdvanceAfter(null);
      replaceParams(next => next.set(OPEN_PARAM, String(advanceTo.id)));
    } else if (advanceDone) {
      setAdvanceAfter(null);
    }
  }, [advanceTo, advanceDone, replaceParams]);
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
    return (
      <InboxFrame scope={scope}>
        <OrganizationLostState />
      </InboxFrame>
    );
  }
  if (!mailboxes.data) {
    return (
      <InboxFrame scope={scope}>
        <ErrorAlert
          message={t(resolveMailboxErrorKey(mailboxes.error))}
          retryLabel={t('actions.retry')}
          onRetry={() => void mailboxes.refetch()}
        />
      </InboxFrame>
    );
  }

  const readable = mailboxes.data.filter(mailbox => mailbox.access.view);
  if (readable.length === 0) {
    return (
      <InboxFrame scope={scope}>
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
      </InboxFrame>
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
  const roleList = roles.data ?? [];
  const narrowedRole =
    typeof urlState.role === 'number'
      ? (roleList.find(role => role.id === urlState.role) ?? null)
      : null;
  let title: string;
  if (urlState.box !== null) title = narrowedTo?.address ?? t('inbox.views.all');
  else if (urlState.role === NO_ROLE) title = t('inbox.groups.ungrouped');
  else if (urlState.role !== null) title = narrowedRole?.name ?? t('inbox.views.all');
  else if (urlState.q !== '') title = t('inbox.search.results', { q: urlState.q });
  else if (urlState.view === 'all') title = t('inbox.title');
  else title = t(`inbox.views.${urlState.view}`);
  // Unnarrowed, the subtitle counts what the caller reads: readable mailboxes and visible roles.
  const subtitle =
    urlState.box === null && urlState.role === null && urlState.q === ''
      ? t('inbox.subtitle', {
          mailboxes: readable.length,
          roles: visibleRoles(mailboxes.data, roleList, canManage).length,
        })
      : undefined;
  const sendable = mailboxes.data.filter(mailbox => mailbox.access.send);
  const sendableIds = new Set(sendable.map(mailbox => mailbox.id));
  // A new message starts from the narrowed mailbox when it may send, else the mailbox last
  // opened, else the first that may (D26).
  const compose =
    sendable.length > 0
      ? () => {
          const stored = readStoredMailboxId(organizationId);
          const from =
            sendable.find(mailbox => mailbox.id === urlState.box) ??
            sendable.find(mailbox => mailbox.id === stored) ??
            sendable[0];
          setDraft({ mailbox: from, draft: emptyDraft, fromOptions: sendable });
        }
      : null;
  // Rows name their role only while the list spans roles.
  const roleTags =
    urlState.box === null && urlState.role === null
      ? new Map<number, MailRole>(roleList.map(role => [role.id, role]))
      : null;
  const emptyTitle =
    urlState.q !== ''
      ? t('inbox.empty.search')
      : urlState.unread
        ? t('inbox.empty.unread')
        : urlState.view === 'all'
          ? t('inbox.empty.view')
          : t(`inbox.empty.${urlState.view}`);

  /** Moves the open row by `delta`; past the last loaded row, opens the first of the next page. */
  const step = (delta: 1 | -1) => {
    if (rows.length === 0) return;
    const target = openIndex < 0 ? 0 : openIndex + delta;
    if (target >= 0 && target < rows.length) {
      setOpenParam(rows[target].id);
    } else if (delta > 0 && hasNextPage) {
      setAdvanceAfter(rows[rows.length - 1].id);
      loadMore();
    }
  };
  /** An action on the open row that its mailbox's `send` allows; without it nothing is sent. */
  const sending = (action: keyof InboxPaneActions) => () => {
    if (!openItem) return;
    if (!openMailbox?.access.send) {
      toast.error(t('inbox.noSendPermission'));
      return;
    }
    paneActions.current?.[action]();
  };
  const showHelp = () => {
    setHelpMounted(true);
    setHelpOpen(true);
  };
  const shortcuts: Partial<Record<MailShortcut, () => void>> = {
    next: () => step(1),
    previous: () => step(-1),
    open: () => {
      if (!openItem && rows.length > 0) setOpenParam(rows[0].id);
    },
    archive: sending('archive'),
    trash: sending('trash'),
    reply: sending('reply'),
    replyAll: sending('replyAll'),
    forward: sending('forward'),
    star: () => paneActions.current?.star(),
    compose: compose ?? (() => toast.error(t('inbox.noSendableMailbox'))),
    search: () => searchRef.current?.focus(),
    help: showHelp,
    palette: () => {
      setPaletteMounted(true);
      setPaletteOpen(open => !open);
    },
    escape: () => {
      if (openItem) setOpenParam(null);
    },
  };

  let list: React.ReactNode;
  if (inbox.isPending || resetting) {
    list = <InboxListSkeleton />;
  } else if (!inbox.data && isSearchTooBroad(inbox.error)) {
    list = (
      <Alert className="mx-3 w-auto">
        <AlertCircle className="size-4" aria-hidden="true" />
        <AlertDescription>{t(resolveMailboxErrorKey(inbox.error))}</AlertDescription>
      </Alert>
    );
  } else if (!inbox.data) {
    list = (
      <div className="space-y-3 px-3">
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
      <div className="px-3">
        <EmptyState
          icon={<Inbox className="size-5" aria-hidden="true" />}
          title={emptyTitle}
          description={
            urlState.view === 'all' && urlState.q === '' && !urlState.unread
              ? t('inbox.empty.viewHint')
              : undefined
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
        sendable={sendableIds}
        timeZone={timeZone}
        selectedId={openItem?.id ?? null}
        onOpen={row => setOpenParam(row.id)}
        onLoadMore={loadMore}
        hasMore={lastPage.meta.has_more && lastPage.meta.next_cursor !== null}
        loadingMore={isFetchingNextPage}
      />
    );
  }

  const sidebarProps = {
    counts: counts.data,
    mailboxes: mailboxes.data,
    roles: roles.data,
    canManage,
    view: urlState.view,
    box: urlState.box,
    role: urlState.role,
    onManageRoles: () => setManagingRoles(true),
    onShortcuts: showHelp,
  };
  const onView = (view: InboxView) =>
    setListing({ [VIEW_PARAM]: view === 'all' ? null : view, [SEARCH_PARAM]: null });
  // Narrowing starts a new listing: a search typed for the previous one no longer applies.
  const onBox = (mailboxId: number) =>
    setListing({ [BOX_PARAM]: String(mailboxId), [ROLE_PARAM]: null, [SEARCH_PARAM]: null });
  const onRole = (role: InboxRoleFilter) =>
    setListing({ [ROLE_PARAM]: String(role), [BOX_PARAM]: null, [SEARCH_PARAM]: null });
  const runCommand = (command: PaletteCommand) => {
    switch (command.kind) {
      case 'mailbox':
        onBox(command.mailboxId);
        break;
      case 'role':
        onRole(command.roleId);
        break;
      case 'mail':
        // The chosen mail shows at once; the search lists it with its neighbours.
        setAdvanceAfter(null);
        setHeld(command.item);
        replaceParams(next => {
          for (const name of filterParams) next.delete(name);
          next.set(SEARCH_PARAM, command.q);
          next.set(OPEN_PARAM, String(command.item.id));
        });
        break;
      case 'compose':
        compose?.();
        break;
      case 'view':
        onView(command.view);
        break;
      case 'help':
        showHelp();
        break;
      case 'manageMailboxes':
        setManagingMailboxes(true);
        break;
      case 'manageRoles':
        setManagingRoles(true);
        break;
    }
  };
  /** In the sheet, every choice also closes it. */
  const closing =
    <A extends unknown[]>(action: (...args: A) => void) =>
    (...args: A) => {
      setSidebarOpen(false);
      action(...args);
    };

  return (
    <div className="flex h-full min-h-0 bg-bg-canvas text-[13px] leading-normal antialiased">
      <MailShortcuts bindings={shortcuts} />
      <aside className="hidden w-14 flex-none flex-col overflow-y-auto px-2 pt-[18px] pb-3.5 md:flex xl:w-[248px] xl:px-3">
        <InboxSidebar
          {...sidebarProps}
          rail
          organization={<MailOrganizationName scope={scope} compact />}
          onView={onView}
          onBox={onBox}
          onRole={onRole}
          onCompose={compose}
        />
      </aside>

      <section
        aria-label={title}
        className={cn(
          'my-2.5 ml-2.5 flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden rounded-[14px] border border-border-main bg-bg-surface shadow-sm max-lg:mr-2.5 md:ml-0',
          'lg:w-[380px] lg:flex-none lg:rounded-r-none lg:border-y-0 xl:w-[470px]',
          openItem && 'max-lg:hidden'
        )}
      >
        <InboxListHeader
          title={title}
          subtitle={subtitle}
          unread={urlState.unread}
          unreadCount={listingUnread(counts.data, urlState)}
          onUnreadChange={unread => setListing({ [TAB_PARAM]: unread ? UNREAD_TAB : null })}
          q={urlState.q}
          onSearch={q => setListing({ [SEARCH_PARAM]: q === '' ? null : q, [VIEW_PARAM]: null })}
          mailboxHref={urlState.box === null ? null : mailboxHref(urlState.box)}
          onClearMailbox={
            urlState.box === null && urlState.role === null
              ? undefined
              : () => setListing({ [BOX_PARAM]: null, [ROLE_PARAM]: null })
          }
          onShowSidebar={() => {
            setSidebarSheet(true);
            setSidebarOpen(true);
          }}
          searchRef={searchRef}
        />
        <div data-scroll-pane className="min-h-0 flex-1 overflow-y-auto">
          {list}
        </div>
      </section>

      <section
        aria-label={t('inbox.readingPane')}
        data-reading-pane=""
        className={cn(
          'my-2.5 mr-2.5 flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden rounded-[14px] border border-border-main bg-bg-surface max-md:ml-2.5',
          'lg:rounded-l-none lg:border-l-0',
          !openItem && 'max-lg:hidden'
        )}
      >
        {openItem ? (
          <Suspense fallback={<ReaderSkeleton />}>
            <InboxReadingPane
              key={openItem.id}
              item={openItem}
              access={openMailbox?.access ?? null}
              roleName={
                openItem.role_id === null
                  ? null
                  : (roleList.find(role => role.id === openItem.role_id)?.name ?? null)
              }
              navigation={navigation}
              onUnavailable={refreshListing}
              actionsRef={paneActions}
            />
          </Suspense>
        ) : (
          <div className="flex h-full min-h-48 items-center justify-center">
            <p className="flex items-center gap-2 text-text-muted">
              <MailOpen className="size-4" aria-hidden="true" />
              {t('list.readerEmpty')}
            </p>
          </div>
        )}
      </section>

      {sidebarSheet ? (
        <Suspense fallback={null}>
          <Sheet open={sidebarOpen} onOpenChange={setSidebarOpen}>
            <SheetContent
              side="left"
              className="w-[280px] gap-0 overflow-y-auto bg-bg-canvas px-3 pt-12 pb-3.5 motion-reduce:data-[state=closed]:animate-none motion-reduce:data-[state=open]:animate-none"
            >
              <SheetTitle className="sr-only">{t('inbox.groups.label')}</SheetTitle>
              <InboxSidebar
                {...sidebarProps}
                organization={<MailOrganizationName scope={scope} compact />}
                onView={closing(onView)}
                onBox={closing(onBox)}
                onRole={closing(onRole)}
                onCompose={compose ? closing(compose) : null}
              />
            </SheetContent>
          </Sheet>
        </Suspense>
      ) : null}

      {helpMounted ? (
        <Suspense fallback={null}>
          <ShortcutHelpDialog open={helpOpen} onOpenChange={setHelpOpen} />
        </Suspense>
      ) : null}

      {paletteMounted ? (
        <Suspense fallback={null}>
          <CommandPalette
            open={paletteOpen}
            onOpenChange={setPaletteOpen}
            organizationId={organizationId}
            timeZone={timeZone}
            mailboxes={mailboxes.data}
            roles={visibleRoles(mailboxes.data, roleList, canManage)}
            canManage={canManage}
            canCompose={compose !== null}
            onCommand={runCommand}
          />
        </Suspense>
      ) : null}

      {canManage ? (
        <ManageMailboxesFlow
          open={managingMailboxes}
          onOpenChange={setManagingMailboxes}
          mailboxes={mailboxes.data}
          onRefresh={() => void mailboxes.refetch()}
        />
      ) : null}

      {canManage && managingRoles ? (
        <Suspense fallback={null}>
          <ManageRolesDialog open onOpenChange={open => setManagingRoles(open)} />
        </Suspense>
      ) : null}

      {draft && draft.mailbox.access.send ? (
        <ComposeDialog
          mailbox={draft.mailbox}
          fromOptions={draft.fromOptions}
          draft={draft.draft}
          onClose={() => setDraft(null)}
          onStored={() => setDraft(null)}
        />
      ) : null}
    </div>
  );
}

/** The inbox keys, bound while the three panes are on screen. */
function MailShortcuts({ bindings }: { bindings: Partial<Record<MailShortcut, () => void>> }) {
  useMailShortcuts(bindings, { enabled: true });
  return null;
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
      <ManageMailboxesFlow
        open={open}
        onOpenChange={setOpen}
        mailboxes={mailboxes}
        onRefresh={onRefresh}
      />
    </>
  );
}

/** Manage mailboxes, then the access panel of the mailbox it opens; each loads when first shown. */
function ManageMailboxesFlow({
  open,
  onOpenChange,
  mailboxes,
  onRefresh,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  mailboxes: Mailbox[];
  onRefresh: () => void;
}) {
  const organizationId = useMailOrganizationId();
  const domains = useMailboxDomains(organizationId);
  const [accessTargetId, setAccessTargetId] = useState<number | null>(null);
  const domainList = domains.data ?? [];
  const domain = domainList.find(item => item.kind === 'shared') ?? domainList[0] ?? null;
  const accessTarget = mailboxes.find(item => item.id === accessTargetId) ?? null;

  return (
    <Suspense fallback={null}>
      {open && domains.data ? (
        <ManageMailboxesDialog
          open
          mailboxes={mailboxes}
          domain={domain}
          domains={domainList}
          onClose={() => onOpenChange(false)}
          onCreated={() => onOpenChange(false)}
          onOpenAccess={mailbox => {
            onOpenChange(false);
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
    </Suspense>
  );
}

/** The reading pane while its code loads: the 52px bar over the article column. */
function ReaderSkeleton() {
  return (
    <div className="flex h-full min-h-0 flex-col" aria-hidden="true">
      <div className="h-[52px] shrink-0 border-b border-border-subtle" />
      <div className="py-[34px]">
        <ReaderArticleSkeleton className="mx-auto w-full max-w-[640px] space-y-4 px-5 sm:px-7" />
      </div>
    </div>
  );
}

/** The three panes while the mailboxes load, at their finished sizes. */
function InboxSkeleton() {
  return (
    <div className="flex h-full min-h-0 bg-bg-canvas" aria-hidden="true" data-inbox-skeleton="">
      <div className="hidden w-14 flex-none flex-col gap-2 px-2 pt-[18px] md:flex xl:w-[248px] xl:px-3">
        <Skeleton className="h-9 w-full" />
        <Skeleton className="h-40 w-full" />
      </div>
      <div className="my-2.5 ml-2.5 flex-1 overflow-hidden rounded-[14px] border border-border-main bg-bg-surface max-lg:mr-2.5 md:ml-0 lg:w-[380px] lg:flex-none lg:rounded-r-none lg:border-y-0 xl:w-[470px]">
        <InboxListHeaderSkeleton />
        <InboxListSkeleton />
      </div>
      <div className="my-2.5 mr-2.5 hidden flex-1 rounded-r-[14px] border border-l-0 border-border-main bg-bg-surface lg:block" />
    </div>
  );
}
