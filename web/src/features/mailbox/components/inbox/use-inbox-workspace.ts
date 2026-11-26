'use client';

import { useCallback, useEffect, useRef, useState } from 'react';
import { usePathname, useRouter, useSearchParams } from 'next/navigation';
import { toast } from 'sonner';

import type { PaletteCommand } from '@/features/mailbox/components/command-palette';
import {
  emptyDraft,
  forwardDraft,
  quotedReplyDraft,
  replyAllDraft,
  type ComposeDraft,
} from '@/features/mailbox/components/compose-drafts';
import { useComposeFocusReturn } from '@/features/mailbox/components/compose-focus';
import type { ExternalConnectPreset } from '@/features/mailbox/components/external-connect-dialog';
import type {
  ActiveDraft,
  InboxSidebarSharedProps,
} from '@/features/mailbox/components/inbox/inbox-dialogs';
import {
  BOX_PARAM,
  fallbackTimeZone,
  filterParams,
  inboxFilters,
  isRejectedQuery,
  NEW_PARAM,
  NEW_TYPE_PARAM,
  newMessageSender,
  NO_ROLE,
  OPEN_PARAM,
  readInboxUrl,
  ROLE_PARAM,
  rowIdPattern,
  SEARCH_PARAM,
  VIEW_PARAM,
} from '@/features/mailbox/components/inbox/inbox-url';
import { useInboxActions } from '@/features/mailbox/components/inbox/use-inbox-actions';
import type {
  InboxPaneActions,
  InboxReaderNavigation,
} from '@/features/mailbox/components/inbox-reading-pane';
import {
  visibleRoles,
  type InboxRoleFilter,
  type InboxSidebarExternal,
} from '@/features/mailbox/components/inbox-sidebar';
import { useMailOrganizationId } from '@/features/mailbox/components/mail-organization';
import { useDateTimeFormat } from '@/features/mailbox/components/mail-shared';
import {
  COMPOSE_PARAM,
  COMPOSE_RESUME,
  MAILBOX_PARAM,
} from '@/features/mailbox/components/mailbox-params';
import type { MailDraftSummary } from '@/features/mailbox/draft-schemas';
import {
  useExternalMailConnections,
  useExternalMailConnectors,
} from '@/features/mailbox/hooks/use-external-mail';
import {
  useInboxCounts,
  useInboxMessages,
  useInboxTimezone,
  useMailRoles,
} from '@/features/mailbox/hooks/use-inbox';
import { useMailDrafts, useOpenMailDraft } from '@/features/mailbox/hooks/use-mail-drafts';
import type { MailShortcut } from '@/features/mailbox/hooks/use-mail-shortcuts';
import { useMailboxes } from '@/features/mailbox/hooks/use-mailbox';
import { usePublicMailboxOptions } from '@/features/mailbox/hooks/use-public-mailbox';
import { useIsPersonalMode } from '@/features/mailbox/stores/workspace-mode';
import type { InboxItem, InboxView, MailMessage, MailRole, Mailbox } from '@/features/mailbox/types';
import { canComposeFrom, isConnectedMailbox } from '@/features/mailbox/utils/connected-mailbox';
import { useT } from '@/i18n';

const SORT_KEY = 'zmail.mailbox-sort';

/** Headless state, URL synchronization, shortcuts, and actions for the unified inbox workspace. */
export function useInboxWorkspace({ canManage }: { canManage: boolean }) {
  const t = useT('mailbox');
  const organizationId = useMailOrganizationId();
  const isPersonalMode = useIsPersonalMode();
  const router = useRouter();
  const pathname = usePathname();
  const searchParams = useSearchParams();

  const [sort, setSort] = useState('default');
  useEffect(() => {
    try {
      const saved = window.localStorage.getItem(SORT_KEY);
      if (saved === 'address' || saved === 'unread') setSort(saved);
    } catch {
      /* Storage may be unavailable in private browsing. */
    }
  }, []);
  const changeSort = (value: string) => {
    setSort(value);
    try {
      window.localStorage.setItem(SORT_KEY, value);
    } catch {
      /* Keep the session preference. */
    }
  };

  const mailboxes = useMailboxes(organizationId, { live: true });
  const roles = useMailRoles(organizationId);
  const publicMailboxOptionsQuery = usePublicMailboxOptions({ enabled: isPersonalMode });
  const publicMailboxOptions = publicMailboxOptionsQuery.data ?? null;
  const canClaimPublicMailbox =
    isPersonalMode &&
    Boolean(publicMailboxOptions?.enabled) &&
    (publicMailboxOptions?.claimed_count ?? 0) < (publicMailboxOptions?.limit_per_user ?? 0);
  const [claimPublicOpen, setClaimPublicOpen] = useState(false);
  const [claimPublicMounted, setClaimPublicMounted] = useState(false);
  const openClaimPublic = () => {
    setClaimPublicMounted(true);
    setClaimPublicOpen(true);
  };
  // External mail is on while the instance lists a connector; the caller's connections are read
  // only then, since their route answers 404 while it is off.
  const connectors = useExternalMailConnectors(organizationId);
  const connectorList = connectors.data ?? [];
  const externalOn = connectorList.length > 0;
  // Connections are also read for connected mailboxes the list already holds, so their status
  // shows even after the instance stops offering connectors.
  const hasConnected = (mailboxes.data ?? []).some(isConnectedMailbox);
  const connections = useExternalMailConnections(organizationId, {
    enabled: externalOn || hasConnected,
  });
  const connectionList = connections.data ?? [];

  // Mounted from the first open on, so the dialogs can animate closed.
  const [connectOpen, setConnectOpen] = useState(false);
  const [connectMounted, setConnectMounted] = useState(false);
  const [manageExternalOpen, setManageExternalOpen] = useState(false);
  const [manageExternalMounted, setManageExternalMounted] = useState(false);
  // Where focus goes back when the external mailbox dialogs close, shared because each opens the
  // other: the sidebar control that started the flow.
  const externalReturnFocus = useRef<HTMLElement | null>(null);
  // A password connection being restored opens the connect dialog on its form.
  const [connectPreset, setConnectPreset] = useState<ExternalConnectPreset | null>(null);
  // Every open starts the dialog afresh, so a second Reconnect of the same account opens on its
  // form again rather than on whatever step the last one ended.
  const [connectSession, setConnectSession] = useState(0);
  const openConnect = (preset: ExternalConnectPreset | null = null) => {
    setManageExternalOpen(false);
    setConnectPreset(preset);
    setConnectSession(session => session + 1);
    setConnectMounted(true);
    setConnectOpen(true);
  };
  const openManageExternal = () => {
    setManageExternalMounted(true);
    setManageExternalOpen(true);
  };

  const { zone: inboxZone, ready: zoneReady } = useInboxTimezone();
  // The zone the inbox refused; UTC stands in for it only while it is still the inbox's zone.
  const [refusedZone, setRefusedZone] = useState<string | null>(null);
  const timeZone = refusedZone === inboxZone ? fallbackTimeZone : inboxZone;
  const urlParsed = readInboxUrl(searchParams);
  // The assignment tabs show while the listing reaches a shared mailbox that is not connected.
  const assignable = (mailboxes.data ?? []).filter(
    mailbox => mailbox.access.view && mailbox.type === 'shared' && !isConnectedMailbox(mailbox)
  );
  const assignmentTabs =
    urlParsed.box === null
      ? assignable.length > 0
      : assignable.some(mailbox => mailbox.id === urlParsed.box);
  const urlState = assignmentTabs ? urlParsed : { ...urlParsed, assignment: null };
  const counts = useInboxCounts(organizationId, timeZone, { enabled: zoneReady });
  // AI triage shows only while the organization has it on, which its counts say by not being null.
  const triageCounts = counts.data?.triage ?? null;
  const filters = inboxFilters(urlState, timeZone, triageCounts !== null);
  const inbox = useInboxMessages(organizationId, filters, { enabled: zoneReady });
  const openDraft = useOpenMailDraft(organizationId);
  const savedDrafts = useMailDrafts(organizationId, mailboxes.data?.some(canComposeFrom) ?? false);

  const [triageMenuOpen, setTriageMenuOpen] = useState(false);
  const [managingRoles, setManagingRoles] = useState(false);
  const [sidebarOpen, setSidebarOpen] = useState(false);
  // Mounted from the first open on, so the sheet can animate closed.
  const [sidebarSheet, setSidebarSheet] = useState(false);
  const [held, setHeld] = useState<InboxItem | null>(null);
  const [draft, setDraft] = useState<ActiveDraft | null>(null);
  // Undo send opens the composer again on what it held.
  const reopenDraft = (value: ActiveDraft) => () => setDraft(value);
  const [draftsOpen, setDraftsOpen] = useState(false);
  const [draftsMounted, setDraftsMounted] = useState(false);
  // Quoted dates read in the zone the list shows, as the reader's do.
  const formatter = useDateTimeFormat(timeZone);
  // The page's composer replaces the workspace; closing it gives the focus back.
  useComposeFocusReturn(draft?.draft.mode === 'compose');
  // Mounted from the first open on, so the dialog can animate closed.
  const [helpOpen, setHelpOpen] = useState(false);
  const [helpMounted, setHelpMounted] = useState(false);
  const [paletteOpen, setPaletteOpen] = useState(false);
  const returnFocus = useRef<HTMLElement | null>(null);
  const [paletteMounted, setPaletteMounted] = useState(false);
  const [managingMailboxes, setManagingMailboxes] = useState(false);
  const [newMailboxType, setNewMailboxType] = useState<'personal' | undefined>(undefined);
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
  const dropNewParam = useCallback(
    () =>
      replaceParams(next => {
        next.delete(NEW_PARAM);
        next.delete(NEW_TYPE_PARAM);
      }),
    [replaceParams]
  );

  // Back from signing in again after a refused send: the composer opens on the kept draft.
  const resumeCompose = searchParams.get(COMPOSE_PARAM) === COMPOSE_RESUME;
  const mailboxList = mailboxes.data;
  // `new=mailbox` from the console home's setup guide: open mailbox creation here too, once the
  // team has mailboxes (without any, the empty state's own action opens it).
  const createRequested = searchParams.get(NEW_PARAM) === 'mailbox';
  // `type=personal` comes with it from "give colleagues a mailbox".
  const requestedType = searchParams.get(NEW_TYPE_PARAM) === 'personal' ? 'personal' : undefined;
  const hasReadable = mailboxList?.some(mailbox => mailbox.access.view) ?? false;
  useEffect(() => {
    if (!createRequested || !hasReadable) return;
    if (canManage) {
      setNewMailboxType(requestedType);
      setManagingMailboxes(true);
    }
    dropNewParam();
  }, [createRequested, hasReadable, canManage, requestedType, dropNewParam]);

  useEffect(() => {
    if (!resumeCompose || !mailboxList) return;
    const sendableList = mailboxList.filter(canComposeFrom);
    if (sendableList.length > 0) {
      setDraft({
        mailbox: newMessageSender(sendableList, urlState.box, organizationId),
        draft: emptyDraft,
        fromOptions: sendableList,
      });
    }
    replaceParams(next => next.delete(COMPOSE_PARAM));
  }, [resumeCompose, mailboxList, urlState.box, organizationId, replaceParams]);

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
  const refetchCounts = counts.refetch;
  const refetchMailboxes = mailboxes.refetch;
  const loadMore = useCallback(() => {
    if (hasNextPage && !isFetchingNextPage) void fetchNextPage();
  }, [hasNextPage, isFetchingNextPage, fetchNextPage]);
  const refreshListing = useCallback(() => {
    void refetchInbox();
    void refetchMailboxes();
    void refetchCounts();
  }, [refetchInbox, refetchMailboxes, refetchCounts]);

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

  const allMailboxes = mailboxes.data ?? [];
  const readable = allMailboxes.filter(mailbox => mailbox.access.view);
  const openMailbox = openItem
    ? (allMailboxes.find(mailbox => mailbox.id === openItem.mailbox_id) ?? null)
    : null;
  const openIndex = openItem ? rows.findIndex(row => row.id === openItem.id) : -1;
  const replyCopy = {
    prefixSubject: (subject: string) => t('compose.replySubject', { subject }),
    quotedHeader: (params: { date: string; sender: string }) => t('compose.quotedHeader', params),
    formatDate: (iso: string) => formatter.format(new Date(iso)),
  };
  const withSender = (build: (mailbox: Mailbox) => ComposeDraft) => {
    if (openMailbox && canComposeFrom(openMailbox)) {
      setDraft({ mailbox: openMailbox, draft: build(openMailbox) });
    }
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
  // Unnarrowed, the subtitle counts what the caller reads: readable mailboxes and visible roles
  // (the roles only when there are any).
  const shownRoleCount = visibleRoles(allMailboxes, roleList, canManage).length;
  const subtitle =
    urlState.box === null && urlState.role === null && urlState.q === ''
      ? shownRoleCount > 0
        ? t('inbox.subtitle', { mailboxes: readable.length, roles: shownRoleCount })
        : t('inbox.subtitleMailboxes', { mailboxes: readable.length })
      : undefined;

  const sendable = allMailboxes.filter(canComposeFrom);
  const sendableIds = new Set(sendable.map(mailbox => mailbox.id));
  const scopeKey = JSON.stringify([
    urlState.view,
    urlState.box,
    urlState.role,
    urlState.q,
    urlState.unread,
    urlState.attachments,
    urlState.assignment,
    urlState.date,
    urlState.triage,
  ]);
  const rowActions = useInboxActions({
    organizationId,
    rows,
    sendableIds,
    view: urlState.view,
    timeZone,
    scopeKey,
    openItemId: openItem?.id ?? null,
    onLeaveOpenRow: () => (navigation.onNext ?? navigation.onClose)(),
    onCloseOpenRow: navigation.onClose,
  });
  const compose =
    sendable.length > 0
      ? () =>
          setDraft({
            mailbox: newMessageSender(sendable, urlState.box, organizationId),
            draft: emptyDraft,
            fromOptions: sendable,
          })
      : null;
  const openSavedDraft = (summary: MailDraftSummary) => {
    openDraft.mutate(summary.id, {
      onSuccess: saved => {
        setDraftsOpen(false);
        setDraft({
          mailbox:
            sendable.find(mailbox => mailbox.id === saved.mailbox_id) ??
            newMessageSender(sendable, urlState.box, organizationId),
          draft: {
            ...emptyDraft,
            to: saved.to,
            cc: saved.cc,
            subject: saved.subject,
            body: saved.body,
            inReplyTo: saved.in_reply_to,
            savedId: saved.id,
          },
          fromOptions: sendable,
        });
      },
      onError: () => toast.error(t('drafts.openFailed')),
    });
  };
  // Rows name their mailbox, tinted by its role, unless the list is one mailbox.
  const roleTags = new Map<number, MailRole>(roleList.map(role => [role.id, role]));

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
  // The help dialog and the palette open from a key or a button; closing puts focus back there.
  const rememberFocus = () => {
    returnFocus.current =
      document.activeElement instanceof HTMLElement ? document.activeElement : null;
  };
  const restoreFocus = (event: Event) => {
    const target = returnFocus.current;
    returnFocus.current = null;
    if (!target?.isConnected) return;
    event.preventDefault();
    target.focus();
  };
  const showHelp = () => {
    rememberFocus();
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
    triage: () => {
      if (triageCounts !== null && openItem?.direction === 'inbound') setTriageMenuOpen(true);
    },
    star: () => paneActions.current?.star(),
    compose: compose ?? (() => toast.error(t('inbox.noSendableMailbox'))),
    search: () => searchRef.current?.focus(),
    help: showHelp,
    palette: () => {
      if (!paletteOpen) rememberFocus();
      setPaletteMounted(true);
      setPaletteOpen(open => !open);
    },
    escape: () => {
      if (openItem) setOpenParam(null);
      else if (rowActions.checkedIds.size > 0) rowActions.clearSelection();
    },
  };

  // Narrowing starts a new listing: a search typed for the previous one no longer applies.
  const onBox = (mailboxId: number) =>
    setListing({ [BOX_PARAM]: String(mailboxId), [ROLE_PARAM]: null, [SEARCH_PARAM]: null });
  const onView = (view: InboxView) =>
    setListing({ [VIEW_PARAM]: view === 'all' ? null : view, [SEARCH_PARAM]: null });
  const onRole = (role: InboxRoleFilter) =>
    setListing({ [ROLE_PARAM]: String(role), [BOX_PARAM]: null, [SEARCH_PARAM]: null });

  const external: InboxSidebarExternal = {
    connections: connections.data,
    onAdd: externalOn ? () => openConnect() : null,
    onManage: openManageExternal,
  };
  const externalProvider = (mailboxId: number): string | null => {
    const mailbox = allMailboxes.find(item => item.id === mailboxId);
    const bound = connectionList.find(connection => connection.mailbox_id === mailboxId);
    if (!bound && !(mailbox && isConnectedMailbox(mailbox))) return null;
    return bound?.provider ?? openItem?.source ?? '';
  };
  const sidebarProps: InboxSidebarSharedProps = {
    external,
    sort,
    onSort: changeSort,
    counts: counts.data,
    mailboxes: allMailboxes,
    roles: roles.data,
    canManage,
    view: urlState.view,
    box: urlState.box,
    role: urlState.role,
    onManageRoles: () => setManagingRoles(true),
    onShortcuts: showHelp,
    drafts:
      sendable.length > 0
        ? {
            count: savedDrafts.data?.length,
            onOpen: () => {
              setDraftsMounted(true);
              setDraftsOpen(true);
            },
          }
        : null,
    publicMailbox: canClaimPublicMailbox
      ? {
          domain: publicMailboxOptions?.domains[0] ?? 'zmail.im',
          onClaim: openClaimPublic,
        }
      : null,
  };

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

  const useTriageDraft = (message: MailMessage, body: string) =>
    withSender(() => {
      const reply = quotedReplyDraft(message, replyCopy);
      return { ...reply, body: `${body}\n\n${reply.body ?? ''}` };
    });

  return {
    organizationId,
    searchParams,
    mailboxes,
    inbox,
    counts,
    connectors,
    connectorList,
    externalOn,
    connections,
    connectionList,
    connectOpen,
    setConnectOpen,
    connectMounted,
    connectPreset,
    connectSession,
    manageExternalOpen,
    setManageExternalOpen,
    manageExternalMounted,
    externalReturnFocus,
    openConnect,
    zoneReady,
    timeZone,
    urlState,
    assignmentTabs,
    triageCounts,
    triageMenuOpen,
    setTriageMenuOpen,
    managingRoles,
    setManagingRoles,
    sidebarOpen,
    setSidebarOpen,
    sidebarSheet,
    setSidebarSheet,
    draft,
    setDraft,
    reopenDraft,
    draftsOpen,
    setDraftsOpen,
    draftsMounted,
    helpOpen,
    setHelpOpen,
    helpMounted,
    paletteOpen,
    setPaletteOpen,
    paletteMounted,
    managingMailboxes,
    setManagingMailboxes,
    newMailboxType,
    setNewMailboxType,
    paneActions,
    searchRef,
    dropNewParam,
    setListing,
    resetting,
    loadMore,
    refreshListing,
    pages,
    rows,
    openItem,
    setOpenParam,
    mailboxHref,
    readable,
    openMailbox,
    navigation,
    narrowedTo,
    roleList,
    title,
    subtitle,
    sendable,
    sendableIds,
    compose,
    openSavedDraft,
    roleTags,
    restoreFocus,
    shortcuts,
    onBox,
    onView,
    onRole,
    externalProvider,
    sidebarProps,
    runCommand,
    useTriageDraft,
    publicMailboxOptions,
    canClaimPublicMailbox,
    claimPublicOpen,
    setClaimPublicOpen,
    claimPublicMounted,
    openClaimPublic,
    showMailbox: urlState.box === null && (mailboxList?.length ?? 0) > 1,
    isFetchingNextPage,
    checkedIds: rowActions.checkedIds,
    selectedItems: rowActions.selectedItems,
    bulkPending: rowActions.bulkPending,
    toggleSelect: rowActions.toggleSelect,
    toggleSelectAll: rowActions.toggleSelectAll,
    clearSelection: rowActions.clearSelection,
    runQuickAction: rowActions.runQuickAction,
    runBulkAction: rowActions.runBulkAction,
    runSnooze: rowActions.runSnooze,
  };
}
