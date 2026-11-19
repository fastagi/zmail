import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { NextIntlClientProvider, type AbstractIntlMessages } from 'next-intl';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { MailboxesRoute } from '@/features/mailbox/components/mailboxes-route';
import { markInboxRowRead } from '@/features/mailbox/hooks/use-inbox';
import { mailboxKeys } from '@/features/mailbox/hooks/use-mailbox';
import { settingKeys } from '@/features/setting/hooks/use-settings';
import type { InboxFilters, InboxItem } from '@/features/mailbox/types';
import { ApiErrorCode } from '@/http/codes';
import { ApiError } from '@/http/request';
import { messages } from '@/i18n/modules';

const toast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn() }));
const state = vi.hoisted(() => {
  const query = () => ({
    data: undefined as unknown,
    isPending: false,
    error: null as unknown,
    refetch: vi.fn(),
  });
  const mutation = () => ({ isPending: false, mutate: vi.fn(), mutateAsync: vi.fn() });
  return {
    organizations: query(),
    mailboxes: query(),
    domains: query(),
    inbox: {
      ...query(),
      isFetching: false,
      hasNextPage: false,
      isFetchingNextPage: false,
      fetchNextPage: vi.fn(),
    },
    /** The error the inbox answers for these filters; null answers them. */
    reject: null as ((filters: InboxFilters) => ApiError | null) | null,
    inboxCalls: [] as InboxFilters[],
    counts: query(),
    countsCalls: [] as string[],
    thread: query(),
    detail: query(),
    threadCalls: [] as { mailboxId: number; threadId: number | null }[],
    detailCalls: [] as { mailboxId: number; messageId: number | null }[],
    markRead: mutation(),
    updateInbox: mutation(),
    update: mutation(),
    removeMessage: mutation(),
    send: mutation(),
    roles: query(),
    members: query(),
    createRole: mutation(),
    updateRole: mutation(),
    deleteRole: mutation(),
    assignRole: mutation(),
    /** Whether the `setting` feature runs; the user's settings come from `userSettings`. */
    settingsEnabled: false,
    userSettings: vi.fn(async (): Promise<unknown> => []),
  };
});

/** A router whose `replace` changes the URL the page reads, as the real one does. */
const navigation = vi.hoisted(() => {
  const listeners = new Set<() => void>();
  return {
    listeners,
    replace: vi.fn((url: string) => {
      window.history.replaceState(null, '', url);
      for (const listener of listeners) listener();
    }),
  };
});

vi.mock('sonner', () => ({ toast }));
vi.mock('next/navigation', async () => {
  const { useSyncExternalStore } = await import('react');
  const router = { replace: navigation.replace, push: navigation.replace };
  const subscribe = (listener: () => void) => {
    navigation.listeners.add(listener);
    return () => navigation.listeners.delete(listener);
  };
  return {
    useRouter: () => router,
    usePathname: () => window.location.pathname,
    useSearchParams: () =>
      new URLSearchParams(useSyncExternalStore(subscribe, () => window.location.search)),
  };
});
vi.mock('@/features/organization/hooks/use-organizations', () => ({
  useOrganizations: () => state.organizations,
  useOrganizationMembers: () => state.members,
}));
vi.mock('@/config/features', async importOriginal => {
  const original = await importOriginal<typeof import('@/config/features')>();
  return {
    ...original,
    isWebFeatureEnabled: (feature: Parameters<typeof original.isWebFeatureEnabled>[0]) =>
      feature === 'setting' ? state.settingsEnabled : original.isWebFeatureEnabled(feature),
  };
});
vi.mock('@/features/setting/services/setting-service', () => ({
  settingService: { user: () => state.userSettings() },
}));
vi.mock('@/features/mailbox/components/mailbox-workspace', async importOriginal => ({
  ...(await importOriginal<typeof import('@/features/mailbox/components/mailbox-workspace')>()),
  MailboxWorkspace: () => <div data-testid="mailbox-workspace" />,
}));
vi.mock('@/features/mailbox/hooks/use-mail-templates', () => ({
  useMailTemplates: () => ({ data: [] }),
}));
vi.mock('@/features/mailbox/hooks/use-mailbox', async importOriginal => ({
  ...(await importOriginal<typeof import('@/features/mailbox/hooks/use-mailbox')>()),
  useMailboxes: () => state.mailboxes,
  useMailboxDomains: () => state.domains,
  useMailThread: (_organizationId: number, mailboxId: number, threadId: number | null) => {
    state.threadCalls.push({ mailboxId, threadId });
    return threadId === null ? { ...state.thread, data: undefined, error: null } : state.thread;
  },
  useMailMessage: (_organizationId: number, mailboxId: number, messageId: number | null) => {
    state.detailCalls.push({ mailboxId, messageId });
    return messageId === null ? { ...state.detail, data: undefined, error: null } : state.detail;
  },
  useMarkMailMessageRead: () => state.markRead,
  useUpdateMailMessage: () => state.update,
  useDeleteMailMessage: () => state.removeMessage,
  useSendMailMessage: () => state.send,
}));
vi.mock('@/features/mailbox/hooks/use-inbox', async importOriginal => ({
  ...(await importOriginal<typeof import('@/features/mailbox/hooks/use-inbox')>()),
  // Only calls that load are recorded; a disabled query is pending without data.
  useInboxMessages: (
    _organizationId: number,
    filters: InboxFilters,
    { enabled = true }: { enabled?: boolean } = {}
  ) => {
    if (!enabled) return { ...state.inbox, data: undefined, isPending: true };
    state.inboxCalls.push(filters);
    const error = state.reject?.(filters) ?? null;
    return error ? { ...state.inbox, data: undefined, error } : state.inbox;
  },
  useInboxCounts: (
    _organizationId: number,
    tz: string,
    { enabled = true }: { enabled?: boolean } = {}
  ) => {
    if (!enabled) return { ...state.counts, data: undefined, isPending: true };
    state.countsCalls.push(tz);
    return state.counts;
  },
  useUpdateInboxMessages: () => state.updateInbox,
  useMailRoles: () => state.roles,
  useCreateMailRole: () => state.createRole,
  useUpdateMailRole: () => state.updateRole,
  useDeleteMailRole: () => state.deleteRole,
  useAssignMailboxRole: () => state.assignRole,
}));

let queryClient: QueryClient;
const originalResolvedOptions = Intl.DateTimeFormat.prototype.resolvedOptions;

describe('unified inbox', () => {
  beforeEach(() => {
    queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    toast.success.mockReset();
    toast.error.mockReset();
    navigation.replace.mockClear();
    window.localStorage.clear();
    window.history.replaceState(null, '', '/console/mailboxes');
    // Every test runs in UTC unless it names another zone, whatever the machine's zone.
    browserZone('UTC');
    state.organizations.data = page([organization('owner')]);
    state.mailboxes.data = [mailbox(1, 'hello'), mailbox(2, 'sales')];
    state.domains.data = [domain()];
    state.inbox.data = listing([[threadRow(), singleRow()]]);
    state.reject = null;
    state.counts.data = counts();
    state.countsCalls = [];
    state.thread.data = [message({ id: 29, thread_id: 30 }), message({ id: 31, thread_id: 30 })];
    state.detail.data = message({ id: 12, mailbox_id: 1 });
    state.roles.data = [];
    state.members.data = page([]);
    state.settingsEnabled = false;
    state.userSettings.mockReset();
    state.userSettings.mockResolvedValue([]);
    for (const query of [
      state.organizations,
      state.mailboxes,
      state.domains,
      state.inbox,
      state.counts,
      state.thread,
      state.detail,
      state.roles,
      state.members,
    ]) {
      query.isPending = false;
      query.error = null;
      query.refetch.mockReset();
    }
    state.inbox.fetchNextPage.mockReset();
    state.inbox.hasNextPage = false;
    state.inboxCalls = [];
    state.threadCalls = [];
    state.detailCalls = [];
    for (const mutation of [
      state.markRead,
      state.updateInbox,
      state.update,
      state.removeMessage,
      state.send,
      state.createRole,
      state.updateRole,
      state.deleteRole,
      state.assignRole,
    ]) {
      mutation.isPending = false;
      mutation.mutate.mockReset();
      mutation.mutateAsync.mockReset();
      mutation.mutateAsync.mockResolvedValue({ updated: 1 });
    }
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it('renders the unified inbox by default and the mailbox workspace for ?mailbox=', async () => {
    // A remembered mailbox no longer takes a bare /console/mailboxes to that mailbox.
    window.localStorage.setItem('zmail.mailbox.1', '2');
    const { unmount } = renderRoute();

    expect(screen.getByRole('heading', { level: 1, name: 'Inbox' })).toBeInTheDocument();
    expect(screen.queryByTestId('mailbox-workspace')).not.toBeInTheDocument();
    expect(rowButton('Launch plan')).toBeInTheDocument();
    expect(rowButton('Invoice')).toBeInTheDocument();
    expect(state.inboxCalls.at(-1)).toMatchObject({ view: 'all', group: 'thread' });
    expect(navigation.replace).not.toHaveBeenCalledWith(
      expect.stringContaining('mailbox='),
      expect.anything()
    );
    // The row shows the sender, the thread size, and the snippet.
    expect(rowButton('Launch plan')).toHaveTextContent('Bob');
    expect(rowButton('Launch plan')).toHaveTextContent('(3)');
    expect(rowButton('Launch plan')).toHaveTextContent('Are we ready');
    // The sidebar lists the views and the readable mailboxes with the caller's counts.
    expect(viewButton(/^All mail/u)).toHaveAccessibleName('All mail: 4 unread');
    expect(viewButton(/^All mail/u)).toHaveAttribute('aria-current', 'page');
    expect(viewButton(/^Today/u)).toHaveAccessibleName('Today: 5');
    expect(viewButton(/^Pending/u)).toHaveAccessibleName('Pending: 1');
    expect(mailboxButton(/^sales@/u)).toHaveAccessibleName('sales@mail.example.com: 3 unread');
    unmount();

    window.history.replaceState(null, '', '/console/mailboxes?mailbox=2');
    renderRoute();
    // The mailbox workspace loads on demand.
    expect(await screen.findByTestId('mailbox-workspace')).toBeInTheDocument();
    expect(screen.queryByRole('heading', { level: 1, name: 'Inbox' })).not.toBeInTheDocument();
  });

  it('opening a thread row loads the thread and marks it read', () => {
    queryClient.setQueryData(mailboxKeys.inbox(1, { view: 'all', group: 'thread', tz: 'UTC' }), {
      pages: [{ items: [threadRow(), singleRow()], meta: meta(false) }],
      pageParams: [undefined],
    });
    renderRoute();

    fireEvent.click(rowButton('Launch plan'));

    expect(new URLSearchParams(window.location.search).get('open')).toBe('31');
    expect(state.threadCalls).toContainEqual({ mailboxId: 2, threadId: 30 });
    expect(screen.getByRole('list', { name: 'Conversation' })).toBeInTheDocument();
    expect(state.updateInbox.mutate).toHaveBeenCalledTimes(1);
    expect(state.updateInbox.mutate).toHaveBeenCalledWith(
      { mailboxId: 2, message_ids: [29, 30, 31], read: true },
      expect.objectContaining({ onError: expect.any(Function) })
    );
    // The row reads as read in the cached listing before the server answers.
    expect(cachedRow(31)?.thread?.unread).toBe(false);

    // A failed request puts the row back and says why.
    const [, options] = state.updateInbox.mutate.mock.calls[0] as [
      unknown,
      { onError: (e: unknown) => void },
    ];
    act(() => options.onError(new ApiError('down', ApiErrorCode.COMMON_SERVICE_UNAVAILABLE, 503)));
    expect(cachedRow(31)?.thread?.unread).toBe(true);
    expect(toast.error).toHaveBeenCalled();
  });

  it('opening an unread single message marks it read with the single-message update', () => {
    renderRoute();

    fireEvent.click(rowButton('Invoice'));

    expect(state.detailCalls).toContainEqual({ mailboxId: 1, messageId: 12 });
    expect(state.markRead.mutate).toHaveBeenCalledWith(
      { mailboxId: 1, messageId: 12 },
      expect.objectContaining({ onError: expect.any(Function) })
    );
    expect(state.updateInbox.mutate).not.toHaveBeenCalled();
  });

  it('keeps the row named by ?open= open after a reload', () => {
    window.history.replaceState(null, '', '/console/mailboxes?open=31');
    renderRoute();

    expect(rowButton('Launch plan')).toHaveAttribute('aria-current', 'true');
    expect(state.threadCalls).toContainEqual({ mailboxId: 2, threadId: 30 });
  });

  it('archive sends the row thread message ids with placement archive', async () => {
    window.history.replaceState(null, '', '/console/mailboxes?open=31');
    renderRoute();

    const toolbar = screen.getByRole('toolbar', { name: 'Conversation actions' });
    await act(async () => {
      fireEvent.click(within(toolbar).getByRole('button', { name: 'Archive' }));
    });

    expect(state.updateInbox.mutateAsync).toHaveBeenCalledWith({
      mailboxId: 2,
      message_ids: [29, 30, 31],
      placement: 'archive',
    });
    expect(toast.success).toHaveBeenCalledWith('Archived');
    expect(new URLSearchParams(window.location.search).get('open')).toBeNull();
  });

  it('trash sends a single row id with placement trash', async () => {
    window.history.replaceState(null, '', '/console/mailboxes?open=12');
    renderRoute();

    const toolbar = screen.getByRole('toolbar', { name: 'Conversation actions' });
    await act(async () => {
      fireEvent.click(within(toolbar).getByRole('button', { name: 'Move to trash' }));
    });

    expect(state.updateInbox.mutateAsync).toHaveBeenCalledWith({
      mailboxId: 1,
      message_ids: [12],
      placement: 'trash',
    });
  });

  it('archive and trash are hidden without send', () => {
    state.mailboxes.data = [mailbox(1, 'hello'), mailbox(2, 'sales', { send: false })];
    window.history.replaceState(null, '', '/console/mailboxes?open=31');
    renderRoute();

    expect(screen.getByRole('list', { name: 'Conversation' })).toBeInTheDocument();
    expect(screen.queryByRole('toolbar', { name: 'Conversation actions' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Archive' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Reply' })).not.toBeInTheDocument();
  });

  it('no readable mailbox shows the empty state and, for an admin, Manage mailboxes', () => {
    state.mailboxes.data = [mailbox(1, 'hello', { view: false })];
    const { unmount } = renderRoute();

    expect(screen.getByText('No mailbox to read yet')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Manage mailboxes' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /Launch plan/u })).not.toBeInTheDocument();
    unmount();

    state.organizations.data = page([organization('member')]);
    renderRoute();
    expect(screen.getByText('No mailbox to read yet')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Manage mailboxes' })).not.toBeInTheDocument();
  });

  it('a view with no mail shows the empty view', () => {
    state.inbox.data = listing([[]]);
    renderRoute();

    expect(screen.getByText('No mail yet')).toBeInTheDocument();
  });

  it('a row whose mailbox became unreadable shows the unavailable notice and refetches list and mailboxes', () => {
    state.thread.data = undefined;
    state.thread.error = new ApiError('gone', ApiErrorCode.MAILBOX_NOT_FOUND, 404);
    const { rerender } = renderRoute();

    fireEvent.click(rowButton('Launch plan'));

    expect(screen.getByText(/no longer available to you/u)).toBeInTheDocument();
    expect(state.inbox.refetch).toHaveBeenCalledTimes(1);
    expect(state.mailboxes.refetch).toHaveBeenCalledTimes(1);
    expect(toast.error).not.toHaveBeenCalled();

    // The refetch drops the mailbox and its rows; the notice stays and nothing is fetched again.
    state.mailboxes.data = [mailbox(1, 'hello'), mailbox(2, 'sales', { view: false })];
    state.inbox.data = listing([[singleRow()]]);
    rerender(wrap(<MailboxesRoute />));

    expect(screen.getByText(/no longer available to you/u)).toBeInTheDocument();
    expect(state.inbox.refetch).toHaveBeenCalledTimes(1);
    expect(state.mailboxes.refetch).toHaveBeenCalledTimes(1);
  });

  it('a row whose mailbox the mailbox list already shows unreadable shows the notice and refetches the list once', () => {
    state.mailboxes.data = [mailbox(1, 'hello'), mailbox(2, 'sales', { view: false })];
    const { rerender } = renderRoute();

    fireEvent.click(rowButton('Launch plan'));

    expect(screen.getByText(/no longer available to you/u)).toBeInTheDocument();
    expect(state.threadCalls).not.toContainEqual({ mailboxId: 2, threadId: 30 });
    expect(state.inbox.refetch).toHaveBeenCalledTimes(1);

    rerender(wrap(<MailboxesRoute />));
    expect(state.inbox.refetch).toHaveBeenCalledTimes(1);
  });

  it('scrolling to the end fetches the next page only while has_more', () => {
    // Links observe themselves for prefetching too; only the list's end matters here.
    const observers: {
      callback: IntersectionObserverCallback;
      disconnected: boolean;
      target?: Element;
    }[] = [];
    vi.stubGlobal(
      'IntersectionObserver',
      class {
        private entry: (typeof observers)[number];
        constructor(callback: IntersectionObserverCallback) {
          this.entry = { callback, disconnected: false };
          observers.push(this.entry);
        }
        observe(target: Element) {
          this.entry.target = target;
        }
        unobserve() {}
        disconnect() {
          this.entry.disconnected = true;
        }
      }
    );
    state.inbox.data = listing([[threadRow()]], true);
    state.inbox.hasNextPage = true;
    const { rerender } = renderRoute();

    const live = () =>
      observers.filter(observer => !observer.disconnected && observer.target?.tagName === 'LI');
    expect(live()).toHaveLength(1);
    act(() =>
      live()[0].callback(
        [{ isIntersecting: true } as IntersectionObserverEntry],
        {} as IntersectionObserver
      )
    );
    expect(state.inbox.fetchNextPage).toHaveBeenCalledTimes(1);

    state.inbox.data = listing([[threadRow()], [singleRow()]], false);
    state.inbox.hasNextPage = false;
    rerender(wrap(<MailboxesRoute />));

    expect(live()).toHaveLength(0);
    expect(rowButton('Invoice')).toBeInTheDocument();
    expect(state.inbox.fetchNextPage).toHaveBeenCalledTimes(1);
  });

  it('a failed list shows retry and a View by mailbox link to ?mailbox=', () => {
    state.inbox.data = undefined;
    state.inbox.error = new ApiError('down', ApiErrorCode.COMMON_SERVICE_UNAVAILABLE, 503);
    renderRoute();

    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(state.inbox.refetch).toHaveBeenCalled();
    expect(screen.getByRole('link', { name: 'View by mailbox' })).toHaveAttribute(
      'href',
      '/console/mailboxes?org=1&mailbox=1'
    );
  });

  it('retries with tz=UTC when the server does not know the browser zone', () => {
    browserZone('Mars/Olympus_Mons');
    state.reject = filters =>
      filters.tz === 'Mars/Olympus_Mons'
        ? new ApiError('bad zone', ApiErrorCode.COMMON_INVALID_INPUT, 400)
        : null;
    renderRoute();

    expect(state.inboxCalls[0]).toMatchObject({ tz: 'Mars/Olympus_Mons' });
    expect(state.inboxCalls.at(-1)).toMatchObject({ view: 'all', group: 'thread', tz: 'UTC' });
    expect(rowButton('Launch plan')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Retry' })).not.toBeInTheDocument();
  });
  it('Today requests view=today with the inbox timezone', () => {
    browserZone('Asia/Shanghai');
    renderRoute();

    expect(state.countsCalls.at(-1)).toBe('Asia/Shanghai');
    fireEvent.click(viewButton(/^Today/u));

    expect(new URLSearchParams(window.location.search).get('view')).toBe('today');
    expect(state.inboxCalls.at(-1)).toEqual({ view: 'today', tz: 'Asia/Shanghai' });
    expect(viewButton(/^Today/u)).toHaveAttribute('aria-current', 'page');
    expect(screen.getByRole('heading', { level: 2, name: 'Today' })).toBeInTheDocument();
  });

  it('Sent shows the queued badge when sent_waiting is positive', () => {
    const { unmount } = renderRoute();
    expect(viewButton(/^Sent/u)).toHaveAccessibleName('Sent');
    unmount();

    state.counts.data = counts({ sent_waiting: 2 });
    renderRoute();
    expect(within(viewButton(/^Sent/u)).getByText('Queued')).toBeInTheDocument();
    expect(viewButton(/^Sent/u)).toHaveAccessibleName('Sent: mail is queued');
  });

  it('Unread tab requests unread=true without grouping', () => {
    renderRoute();

    expect(screen.getByRole('tab', { name: /^All/u })).toHaveAttribute('aria-selected', 'true');
    fireEvent.click(screen.getByRole('tab', { name: /^Unread/u }));

    expect(new URLSearchParams(window.location.search).get('tab')).toBe('unread');
    expect(state.inboxCalls.at(-1)).toEqual({ view: 'all', unread: true, tz: 'UTC' });
    expect(screen.getByRole('tab', { name: /^Unread/u })).toHaveAttribute('aria-selected', 'true');
  });

  it('search requests view=all with q and shows the too-broad notice on 422', () => {
    window.history.replaceState(null, '', '/console/mailboxes?view=today');
    state.reject = filters =>
      filters.q === 'x' ? new ApiError('broad', ApiErrorCode.MAILBOX_SEARCH_TOO_BROAD, 422) : null;
    renderRoute();

    const search = screen.getByRole('searchbox', { name: 'Search all mail' });
    fireEvent.change(search, { target: { value: '  x ' } });
    fireEvent.submit(search);

    const params = new URLSearchParams(window.location.search);
    expect(params.get('q')).toBe('x');
    expect(params.get('view')).toBeNull();
    expect(state.inboxCalls.at(-1)).toEqual({ view: 'all', q: 'x', tz: 'UTC' });
    expect(
      screen.getByText('This search is too broad. Enter a longer keyword.')
    ).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Retry' })).not.toBeInTheDocument();
    // The too-broad answer is the search's, not a broken query: the URL keeps it.
    expect(new URLSearchParams(window.location.search).get('q')).toBe('x');

    // Clearing the field lists the view again.
    fireEvent.change(search, { target: { value: '' } });
    expect(new URLSearchParams(window.location.search).get('q')).toBeNull();
  });

  it('box narrowing links to the mailbox workspace', () => {
    renderRoute();

    fireEvent.click(mailboxButton(/^sales@/u));

    expect(new URLSearchParams(window.location.search).get('box')).toBe('2');
    expect(state.inboxCalls.at(-1)).toEqual({
      view: 'all',
      mailboxId: 2,
      group: 'thread',
      tz: 'UTC',
    });
    expect(mailboxButton(/^sales@/u)).toHaveAttribute('aria-current', 'page');
    expect(
      screen.getByRole('heading', { level: 2, name: 'sales@mail.example.com' })
    ).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Open in mailbox' })).toHaveAttribute(
      'href',
      '/console/mailboxes?org=1&mailbox=2'
    );

    // A view keeps the narrowing; clearing it lists every mailbox again.
    fireEvent.click(viewButton(/^Sent/u));
    expect(state.inboxCalls.at(-1)).toEqual({
      view: 'sent',
      mailboxId: 2,
      group: 'thread',
      tz: 'UTC',
    });
    fireEvent.click(screen.getByRole('button', { name: 'Show all mailboxes' }));
    expect(new URLSearchParams(window.location.search).get('box')).toBeNull();
    expect(state.inboxCalls.at(-1)).toEqual({ view: 'sent', group: 'thread', tz: 'UTC' });
  });

  it('an invalid view falls back to all', () => {
    window.history.replaceState(null, '', '/console/mailboxes?view=spam&tab=bogus&box=0&q=%20%20');
    renderRoute();

    expect(state.inboxCalls.at(-1)).toEqual({ view: 'all', group: 'thread', tz: 'UTC' });
    expect(viewButton(/^All mail/u)).toHaveAttribute('aria-current', 'page');
    expect(screen.getByRole('tab', { name: /^All/u })).toHaveAttribute('aria-selected', 'true');
    expect(rowButton('Launch plan')).toBeInTheDocument();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('waits for the timezone setting before loading the list and counts', async () => {
    state.settingsEnabled = true;
    const settings = deferred<unknown>();
    state.userSettings.mockReturnValue(settings.promise);
    browserZone('UTC');
    renderRoute();

    expect(state.inboxCalls).toEqual([]);
    expect(state.countsCalls).toEqual([]);

    await act(async () => settings.resolve(timezoneSetting('Asia/Shanghai')));

    await waitFor(() => expect(state.inboxCalls.length).toBeGreaterThan(0));
    expect(state.inboxCalls.every(filters => filters.tz === 'Asia/Shanghai')).toBe(true);
    expect(state.countsCalls.length).toBeGreaterThan(0);
    expect(state.countsCalls.every(tz => tz === 'Asia/Shanghai')).toBe(true);
    expect(rowButton('Launch plan')).toBeInTheDocument();
  });

  it('a refused zone falls back to UTC only until a valid setting loads', async () => {
    state.settingsEnabled = true;
    state.userSettings.mockResolvedValue(timezoneSetting('Mars/Olympus_Mons'));
    state.reject = filters =>
      filters.tz === 'Mars/Olympus_Mons'
        ? new ApiError('bad zone', ApiErrorCode.COMMON_INVALID_INPUT, 400)
        : null;
    renderRoute();

    await waitFor(() => expect(state.inboxCalls.at(-1)).toMatchObject({ tz: 'UTC' }));
    expect(state.inboxCalls[0]).toMatchObject({ tz: 'Mars/Olympus_Mons' });

    act(() => {
      queryClient.setQueryData(settingKeys.user(), timezoneSetting('Asia/Shanghai'));
    });
    await waitFor(() => expect(state.inboxCalls.at(-1)).toMatchObject({ tz: 'Asia/Shanghai' }));
    expect(state.countsCalls.at(-1)).toBe('Asia/Shanghai');
  });

  it('a failed settings fetch is handled on the mail page with the browser zone', async () => {
    state.settingsEnabled = true;
    state.userSettings.mockRejectedValue(
      new ApiError('down', ApiErrorCode.COMMON_SERVICE_UNAVAILABLE, 503)
    );
    browserZone('Asia/Shanghai');
    renderRoute();

    await waitFor(() => expect(state.inboxCalls.at(-1)).toMatchObject({ tz: 'Asia/Shanghai' }));
    const query = queryClient.getQueryCache().find({ queryKey: settingKeys.user() });
    expect(query?.meta).toEqual({ errorHandling: 'local' });
    expect(toast.error).not.toHaveBeenCalled();
  });

  it('sidebar groups mailboxes by role with Ungrouped last', () => {
    state.mailboxes.data = [
      mailbox(1, 'hello', {}, 3),
      mailbox(2, 'sales', {}, 5),
      mailbox(3, 'ops'),
    ];
    state.roles.data = [role(5, 'Agency', [2], 'orange'), role(3, 'Design', [1], 'blue')];
    state.counts.data = {
      ...counts(),
      roles: [
        { role_id: 3, unread: 1 },
        { role_id: 5, unread: 3 },
      ],
      ungrouped_unread: 0,
    };
    renderRoute();

    // Roles in the API's order, then Ungrouped; their mailboxes stay folded until opened.
    expect(groupNames()).toEqual(['Agency: 3 unread', 'Design: 1 unread', 'Ungrouped']);
    expect(within(groupsNav()).queryByRole('button', { name: /^sales@/u })).toBeNull();

    // A role narrows the list and opens its mailboxes.
    fireEvent.click(within(groupsNav()).getByRole('button', { name: 'Agency: 3 unread' }));
    expect(new URLSearchParams(window.location.search).get('role')).toBe('5');
    expect(state.inboxCalls.at(-1)).toEqual({
      view: 'all',
      roleId: 5,
      group: 'thread',
      tz: 'UTC',
    });
    expect(within(groupsNav()).getByRole('button', { name: 'Agency: 3 unread' })).toHaveAttribute(
      'aria-expanded',
      'true'
    );
    expect(mailboxButton(/^sales@/u)).toHaveAccessibleName('sales@mail.example.com: 3 unread');
    expect(screen.getByRole('heading', { level: 2, name: 'Agency' })).toBeInTheDocument();

    // A mailbox of the role narrows to that mailbox instead.
    fireEvent.click(mailboxButton(/^sales@/u));
    let params = new URLSearchParams(window.location.search);
    expect([params.get('box'), params.get('role')]).toEqual(['2', null]);

    // Ungrouped lists the mailboxes without a role.
    fireEvent.click(within(groupsNav()).getByRole('button', { name: 'Ungrouped' }));
    params = new URLSearchParams(window.location.search);
    expect([params.get('box'), params.get('role')]).toEqual([null, 'none']);
    expect(state.inboxCalls.at(-1)).toMatchObject({ roleId: 'none' });
    expect(mailboxButton(/^ops@/u)).toBeInTheDocument();
    expect(screen.getByRole('heading', { level: 2, name: 'Ungrouped' })).toBeInTheDocument();
  });

  it('an admin sees unreadable mailboxes locked without a number', () => {
    state.organizations.data = page([organization('admin')]);
    state.mailboxes.data = [
      mailbox(1, 'hello', {}, 3),
      mailbox(4, 'vault', { view: false, send: false, administer: true }, 3),
      mailbox(2, 'sales'),
    ];
    state.roles.data = [role(3, 'Design', [1, 4])];
    renderRoute();

    fireEvent.click(within(groupsNav()).getByRole('button', { name: /^Design/u }));

    expect(mailboxButton(/^hello@/u)).toBeInTheDocument();
    expect(within(groupsNav()).queryByRole('button', { name: /vault@/u })).toBeNull();
    const locked = within(groupsNav()).getByText('vault@mail.example.com').parentElement;
    expect(locked?.tagName).not.toBe('BUTTON');
    expect(locked).toHaveTextContent('No access');
    expect(locked?.textContent).not.toMatch(/\d/u);
  });

  it('a member does not see a role without a visible mailbox', () => {
    state.organizations.data = page([organization('member')]);
    state.mailboxes.data = [mailbox(1, 'hello', {}, 3), mailbox(2, 'sales')];
    state.roles.data = [role(3, 'Design', [1]), role(9, 'Secret', [])];
    renderRoute();

    expect(groupNames()).toEqual(['Design', 'Ungrouped: 4 unread']);
  });

  it('Manage roles is only offered to owners and admins', () => {
    for (const membership of ['owner', 'admin'] as const) {
      state.organizations.data = page([organization(membership)]);
      const { unmount } = renderRoute();
      expect(within(groupsNav()).getByRole('button', { name: 'Manage roles' })).toBeInTheDocument();
      unmount();
    }

    state.organizations.data = page([organization('member')]);
    renderRoute();
    expect(screen.queryByRole('button', { name: 'Manage roles' })).not.toBeInTheDocument();
  });

  it('a taken role name shows under the name field', async () => {
    state.roles.data = [role(3, 'Design', [1])];
    renderRoute();

    fireEvent.click(within(groupsNav()).getByRole('button', { name: 'Manage roles' }));
    // The dialog loads when it is first opened.
    const dialog = await screen.findByRole('dialog', { name: 'Manage roles' });
    const form = within(dialog).getByRole('form', { name: 'New role' });
    const name = within(form).getByRole('textbox', { name: 'Name' });

    state.createRole.mutateAsync.mockRejectedValueOnce(
      new ApiError('taken', ApiErrorCode.MAILBOX_ROLE_NAME_TAKEN, 409)
    );
    fireEvent.change(name, { target: { value: '  design ' } });
    await act(async () => {
      fireEvent.click(within(form).getByRole('button', { name: 'Add role' }));
    });

    expect(state.createRole.mutateAsync).toHaveBeenCalledWith({
      name: 'design',
      color: 'gray',
      description: '',
      owner_user_id: null,
    });
    expect(name).toHaveAttribute('aria-invalid', 'true');
    expect(name).toHaveAccessibleDescription('Another role already has this name.');

    // The role limit is about the whole form, not the name.
    state.createRole.mutateAsync.mockRejectedValueOnce(
      new ApiError('limit', ApiErrorCode.MAILBOX_ROLE_LIMIT_REACHED, 422)
    );
    fireEvent.change(name, { target: { value: 'Agency' } });
    await act(async () => {
      fireEvent.click(within(form).getByRole('button', { name: 'Add role' }));
    });
    expect(within(form).getByRole('alert')).toHaveTextContent(
      'This organization has 50 roles. Delete one to create another.'
    );
    expect(name).not.toHaveAttribute('aria-invalid', 'true');

    // Deleting asks first and says the mailboxes become ungrouped.
    fireEvent.click(within(dialog).getByRole('button', { name: 'Delete Design' }));
    expect(within(dialog).getByText(/its mailboxes become ungrouped/iu)).toBeInTheDocument();
    expect(state.deleteRole.mutateAsync).not.toHaveBeenCalled();
  });

  it('Manage mailboxes sets the role of a mailbox the admin administers', async () => {
    state.mailboxes.data = [mailbox(1, 'hello', { view: false, administer: true })];
    state.roles.data = [role(3, 'Design', [])];
    state.assignRole.mutateAsync.mockResolvedValue(mailbox(1, 'hello', {}, 3));
    renderRoute();

    fireEvent.click(screen.getByRole('button', { name: 'Manage mailboxes' }));
    const select = await screen.findByRole('combobox', { name: 'Role of hello@mail.example.com' });
    expect(select).toHaveValue('');
    await act(async () => {
      fireEvent.change(select, { target: { value: '3' } });
    });

    expect(state.assignRole.mutateAsync).toHaveBeenCalledWith({ mailboxId: 1, roleId: 3 });
    expect(toast.success).toHaveBeenCalledWith('Role updated');
  });

  it('narrowing to a role hides row role tags', () => {
    state.mailboxes.data = [mailbox(1, 'hello'), mailbox(2, 'sales', {}, 3)];
    state.roles.data = [role(3, 'Design', [2])];
    state.inbox.data = listing([[{ ...threadRow(), role_id: 3 }, singleRow()]]);
    const { unmount } = renderRoute();

    expect(rowButton('Launch plan')).toHaveTextContent('Design');
    expect(rowButton('Invoice')).not.toHaveTextContent('Design');
    unmount();

    for (const query of ['?role=3', '?box=2']) {
      window.history.replaceState(null, '', `/console/mailboxes${query}`);
      const view = renderRoute();
      expect(rowButton('Launch plan')).not.toHaveTextContent('Design');
      view.unmount();
    }
  });

  it('a 400 resets the URL to view=all', () => {
    browserZone('Asia/Shanghai');
    window.history.replaceState(
      null,
      '',
      '/console/mailboxes?view=today&box=2&role=3&tab=unread&open=31'
    );
    state.reject = filters =>
      filters.view === 'today' ? new ApiError('bad', ApiErrorCode.COMMON_INVALID_INPUT, 400) : null;
    renderRoute();

    const params = new URLSearchParams(window.location.search);
    for (const name of ['view', 'box', 'role', 'tab', 'q', 'open']) {
      expect(params.get(name)).toBeNull();
    }
    expect(state.inboxCalls.at(-1)).toEqual({
      view: 'all',
      group: 'thread',
      tz: 'Asia/Shanghai',
    });
    // A bad filter is not the zone's fault: the inbox never retries with UTC.
    expect(state.inboxCalls.some(filters => filters.tz === 'UTC')).toBe(false);
    expect(rowButton('Launch plan')).toBeInTheDocument();
  });
});

describe('markInboxRowRead', () => {
  it('marks the row read in every cached listing and puts them back on rollback', () => {
    const client = new QueryClient();
    const all = mailboxKeys.inbox(1, { view: 'all', group: 'thread', tz: 'UTC' });
    const other = mailboxKeys.inbox(1, { view: 'pending', tz: 'UTC' });
    const elsewhere = mailboxKeys.inbox(2, { view: 'all', tz: 'UTC' });
    for (const key of [all, other, elsewhere]) {
      client.setQueryData(key, {
        pages: [{ items: [threadRow(), singleRow()], meta: meta(false) }],
        pageParams: [undefined],
      });
    }

    const rollback = markInboxRowRead(client, 1, 12);

    const items = (key: readonly unknown[]) =>
      client.getQueryData<{ pages: { items: InboxItem[] }[] }>(key)?.pages[0].items;
    expect(items(all)?.find(item => item.id === 12)?.read).toBe(true);
    expect(items(other)?.find(item => item.id === 12)?.read).toBe(true);
    expect(items(elsewhere)?.find(item => item.id === 12)?.read).toBe(false);
    expect(items(all)?.find(item => item.id === 31)?.thread?.unread).toBe(true);

    rollback();
    expect(items(all)?.find(item => item.id === 12)?.read).toBe(false);
    expect(items(other)?.find(item => item.id === 12)?.read).toBe(false);
  });
});

function viewButton(name: RegExp) {
  const views = screen.getByRole('navigation', { name: 'Inbox views' });
  return within(views).getByRole('button', { name });
}

function groupsNav() {
  return screen.getByRole('navigation', { name: 'Roles & mailboxes' });
}

function mailboxButton(name: RegExp) {
  return within(groupsNav()).getByRole('button', { name });
}

/** The role and Ungrouped rows of the sidebar, by accessible name, in order. */
function groupNames() {
  return within(groupsNav())
    .getAllByRole('button')
    .filter(button => button.hasAttribute('aria-expanded'))
    .map(button => button.getAttribute('aria-label') ?? button.textContent);
}

function role(id: number, name: string, mailboxIds: number[], color = 'gray') {
  return {
    id,
    name,
    color,
    description: '',
    owner_user_id: null,
    mailbox_ids: mailboxIds,
    created_at: now(),
    updated_at: now(),
  };
}

/** A promise with its resolve function, to answer a request at a chosen moment. */
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>(done => {
    resolve = done;
  });
  return { promise, resolve };
}

function timezoneSetting(zone: string) {
  return [{ key: 'localization.timezone', value: zone }];
}

/** Makes the browser report `zone` as its time zone. */
function browserZone(zone: string) {
  vi.spyOn(Intl.DateTimeFormat.prototype, 'resolvedOptions').mockImplementation(function (
    this: Intl.DateTimeFormat
  ) {
    return { ...originalResolvedOptions.call(this), timeZone: zone };
  });
}

function counts(views: Record<string, number> = {}) {
  return {
    views: { all_unread: 4, today: 5, today_unread: 2, pending: 1, sent_waiting: 0, ...views },
    roles: [],
    ungrouped_unread: 4,
    mailboxes: [
      { mailbox_id: 1, unread: 1 },
      { mailbox_id: 2, unread: 3 },
    ],
    today_starts_at: '2026-11-16T00:00:00Z',
    today_ends_at: '2026-11-17T00:00:00Z',
  };
}

function rowButton(subject: string) {
  return screen.getByRole('button', { name: new RegExp(subject, 'u') });
}

function cachedRow(id: number) {
  const [, listing] = queryClient.getQueriesData<{
    pages: { items: ReturnType<typeof threadRow>[] }[];
  }>({ queryKey: mailboxKeys.inboxes(1) })[0];
  return listing?.pages.flatMap(item => item.items).find(item => item.id === id);
}

function organization(role: 'owner' | 'admin' | 'member') {
  return { id: 1, name: 'Acme', slug: 'acme', role, created_at: now(), updated_at: now() };
}

function domain() {
  return {
    domain: 'mail.example.com',
    kind: 'shared',
    max_mailboxes: 10,
    provider: 'log',
    inbound_enabled: false,
    daily_send_limit: 200,
    broadcasts_enabled: true,
    custom_domains_enabled: false,
  };
}

function mailbox(
  id: number,
  localPart: string,
  access: Record<string, boolean> = {},
  roleId: number | null = null
) {
  return {
    id,
    address: `${localPart}@mail.example.com`,
    display_name: '',
    type: 'shared',
    owner_user_id: null,
    awaiting_handover: false,
    sealed_before: null,
    webhooks_enabled: false,
    access: {
      view: true,
      send: true,
      manage_members: false,
      administer: false,
      see_history: false,
      organize: false,
      ...access,
    },
    unread_count: 0,
    created_at: now(),
    role_id: roleId,
  };
}

function summary(overrides: Record<string, unknown>) {
  const id = typeof overrides.id === 'number' ? overrides.id : 11;
  return {
    id,
    placement: 'default',
    folder_id: null,
    label_ids: [] as number[],
    starred: false,
    thread_id: id,
    trashed_at: null,
    mailbox_id: 1,
    direction: 'inbound',
    status: 'received',
    from: { address: 'bob@example.com', name: 'Bob' },
    to: ['hello@mail.example.com'],
    cc: [] as string[],
    subject: 'Subject',
    read: false,
    failure_code: '',
    queued_reason: null,
    last_event: '',
    attachment_count: 0,
    template_id: null,
    created_at: now(),
    ...overrides,
  };
}

/** A three-message thread of mailbox 2 with unread mail, listed by its newest message. */
function threadRow() {
  return {
    ...summary({ id: 31, thread_id: 30, mailbox_id: 2, subject: 'Launch plan', read: true }),
    mailbox_address: 'sales@mail.example.com',
    role_id: null,
    snippet: 'Are we ready for Monday?',
    thread: {
      message_count: 3,
      message_ids: [29, 30, 31],
      unread: true,
      starred: false,
      label_ids: [] as number[],
    },
  };
}

/** One unread message of mailbox 1, as views that list single messages answer it (no `thread`). */
function singleRow() {
  return {
    ...summary({
      id: 12,
      mailbox_id: 1,
      subject: 'Invoice',
      from: { address: 'billing@example.com', name: '' },
      created_at: '2026-11-15T09:00:00Z',
    }),
    mailbox_address: 'hello@mail.example.com',
    role_id: null,
    snippet: 'Your invoice is attached.',
  };
}

function message(overrides: Record<string, unknown>) {
  return {
    ...summary({ mailbox_id: 2, subject: 'Launch plan', ...overrides }),
    text_body: 'Are we ready for Monday?',
    html_body: '',
    message_id: `m-${String(overrides.id)}@example.com`,
    in_reply_to: '',
    events: [],
    attachments: [],
  };
}

function meta(hasMore: boolean) {
  return { per_page: 20, has_more: hasMore, next_cursor: hasMore ? 'MTcwMC4xMg' : null };
}

function listing(pages: unknown[][], hasMore = false) {
  return {
    pages: pages.map((items, index) => ({
      items,
      meta: meta(index === pages.length - 1 ? hasMore : true),
    })),
    pageParams: pages.map((_, index) => (index === 0 ? undefined : `cursor-${index}`)),
  };
}

function page(items: unknown[]) {
  return {
    items,
    meta: { current_page: 1, per_page: 20, total: items.length, last_page: 1, from: 1, to: 1 },
    links: { first: '', last: '', prev: null, next: null },
  };
}

function now() {
  return '2026-11-16T10:00:00Z';
}

function wrap(children: React.ReactNode) {
  return (
    <QueryClientProvider client={queryClient}>
      <NextIntlClientProvider locale="en-US" messages={messages as unknown as AbstractIntlMessages}>
        {children}
      </NextIntlClientProvider>
    </QueryClientProvider>
  );
}

function renderRoute() {
  return render(wrap(<MailboxesRoute />));
}
