import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { NextIntlClientProvider, type AbstractIntlMessages } from 'next-intl';
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';

import { MailboxesRoute } from '@/features/mailbox/components/mailboxes-route';
import { markInboxRowRead } from '@/features/mailbox/hooks/use-inbox';
import { mailboxService } from '@/features/mailbox/services/mailbox-service';
import { mailboxKeys } from '@/features/mailbox/hooks/use-mailbox';
import { settingKeys } from '@/features/setting/hooks/use-settings';
import type { InboxFilters, InboxItem } from '@/features/mailbox/types';
import { ApiErrorCode } from '@/http/codes';
import { ApiError } from '@/http/request';
import { messages } from '@/i18n/modules';

// The workspace loads its dialogs, reader, and sheet on demand. Load those modules once up front:
// when the whole suite runs in parallel, a cold dynamic import can outlast findBy's one-second
// wait, and these tests check the inbox's behavior, not how fast a module loads.
beforeAll(async () => {
  await Promise.all([
    import('@/features/mailbox/components/manage-roles-dialog'),
    import('@/features/mailbox/components/manage-mailboxes-dialog'),
    import('@/features/mailbox/components/inbox-reading-pane'),
    import('@/features/mailbox/components/shortcut-help-dialog'),
    import('@/features/mailbox/components/compose-dialog'),
    import('@/features/mailbox/components/inbox-triage'),
    import('@/features/mailbox/components/command-palette'),
    import('@/components/ui/sheet'),
    import('@/features/mailbox/components/external-connect-dialog'),
    import('@/features/mailbox/components/external-reconnect-banner'),
    import('@/features/mailbox/components/connected-mailboxes-dialog'),
    import('@/features/mailbox/components/inbox-sidebar-connected'),
  ]);
});

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
    stats: query(),
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
    /** External mail: the instance's connectors, the caller's connections, and the sign-in. */
    connectors: query(),
    connections: query(),
    begin: {
      begin: vi.fn(),
      isPending: false,
      error: null as unknown,
      reset: vi.fn(),
    },
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
  useMailStats: () => state.stats,
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
vi.mock('@/features/mailbox/hooks/use-external-mail', async importOriginal => ({
  ...(await importOriginal<typeof import('@/features/mailbox/hooks/use-external-mail')>()),
  useExternalMailConnectors: () => state.connectors,
  useExternalMailConnections: (
    _organizationId: number,
    { enabled = true }: { enabled?: boolean } = {}
  ) => (enabled ? state.connections : { ...state.connections, data: undefined }),
}));
vi.mock('@/features/mailbox/hooks/use-external-mail-actions', async importOriginal => ({
  ...(await importOriginal<typeof import('@/features/mailbox/hooks/use-external-mail-actions')>()),
  useBeginExternalAuthorization: () => state.begin,
}));
// The role dialog counts each role's platform accounts; the ledger is not under test here.
vi.mock('@/features/mailbox/hooks/use-platform-accounts', () => ({
  usePlatformAccountCount: () => ({ data: undefined }),
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
    state.stats.data = [{ day: '2026-11-16', sent: 7, failed: 1, received: 3 }];
    state.inbox.data = listing([[threadRow(), singleRow()]]);
    state.reject = null;
    state.counts.data = counts();
    state.countsCalls = [];
    state.thread.data = [message({ id: 29, thread_id: 30 }), message({ id: 31, thread_id: 30 })];
    state.detail.data = message({ id: 12, mailbox_id: 1 });
    state.roles.data = [];
    state.members.data = page([]);
    state.connectors.data = [];
    state.connections.data = [];
    state.begin.begin.mockReset();
    state.begin.error = null;
    state.settingsEnabled = false;
    state.userSettings.mockReset();
    state.userSettings.mockResolvedValue([]);
    for (const query of [
      state.organizations,
      state.mailboxes,
      state.domains,
      state.stats,
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

  it('hides every external mailbox control while the instance lists no connector', () => {
    renderRoute();

    expect(screen.queryByRole('navigation', { name: 'Connected by me' })).not.toBeInTheDocument();
    expect(
      screen.queryAllByRole('button', { name: 'Add external mailbox' })
    ).toHaveLength(0);
    expect(within(groupsNav()).getByText('Mailboxes')).toBeInTheDocument();
  });

  it('splits team mailboxes from the ones the viewer connected, with status dots', async () => {
    state.connectors.data = [{ id: 'gmail', auth_kind: 'oauth2', capabilities: { read: true } }];
    state.mailboxes.data = [
      mailbox(1, 'hello'),
      mailbox(2, 'sales'),
      externalMailbox(41, 'me@gmail.com'),
      // An API with the origin field marks a connected mailbox `personal` + `connected`.
      { ...externalMailbox(42, 'old@outlook.com'), type: 'personal', origin: 'connected' },
      // Another member's external mailbox, which an admin sees with `administer` only.
      { ...externalMailbox(43, 'them@gmail.com'), access: { ...noAccess(), administer: true } },
    ];
    state.connections.data = [
      connection({ id: 1, mailbox_id: 41, account_address: 'me@gmail.com' }),
      connection({
        id: 2,
        mailbox_id: 42,
        provider: 'outlook',
        account_address: 'old@outlook.com',
        status: 'needs_reconnect',
        last_error_code: 'external.auth_failed',
      }),
    ];
    renderRoute();

    const team = groupsNav();
    expect(within(team).getByText('Team mailboxes')).toBeInTheDocument();
    expect(within(team).queryByText(/gmail\.com|outlook\.com/u)).not.toBeInTheDocument();
    const mine = (await screen.findAllByRole('navigation', { name: 'Connected by me' }))[0];
    const active = within(mine).getByRole('button', {
      name: 'me@gmail.com, Gmail, Syncing',
    });
    expect(active).toHaveAttribute('data-connection-status', 'active');
    expect(active.querySelector('[data-status-dot="active"]')).toHaveClass('bg-success');
    const broken = within(mine).getByRole('button', {
      name: 'old@outlook.com, Outlook, Needs reconnecting',
    });
    expect(broken.querySelector('[data-status-dot="needs_reconnect"]')).toHaveClass('bg-warning');
    expect(within(mine).queryByText('them@gmail.com')).not.toBeInTheDocument();

    // A connected mailbox narrows the list to itself.
    fireEvent.click(active);
    expect(new URLSearchParams(window.location.search).get('box')).toBe('41');

    // "Add external mailbox" opens the connect dialog with the instance's connectors.
    fireEvent.click(within(mine).getByRole('button', { name: 'Add external mailbox' }));
    const dialog = await screen.findByRole('dialog', { name: 'Add an external mailbox' });
    expect(within(dialog).getByRole('button', { name: 'Gmail' })).toBeEnabled();
  });

  it('shows a one-time reconnect notice for a connection that lost its grant', async () => {
    state.connectors.data = [{ id: 'gmail', auth_kind: 'oauth2', capabilities: { read: true } }];
    state.mailboxes.data = [mailbox(1, 'hello'), externalMailbox(41, 'me@gmail.com')];
    state.connections.data = [
      connection({
        id: 5,
        mailbox_id: 41,
        account_address: 'me@gmail.com',
        status: 'needs_reconnect',
        last_synced_at: '2026-11-20T08:00:00Z',
        last_error_code: 'external.auth_failed',
      }),
    ];
    const { unmount } = renderRoute();

    const notice = await screen.findByText('Your Gmail account needs reconnecting');
    const banner = notice.closest('[role="status"]') as HTMLElement;
    expect(banner).toHaveTextContent(/me@gmail\.com stopped syncing\. Last synced Nov 20, 2026/u);
    fireEvent.click(within(banner).getByRole('button', { name: 'Reconnect' }));
    expect(state.begin.begin).toHaveBeenCalledWith({
      kind: 'reconnect',
      provider: 'gmail',
      connectionId: 5,
    });

    // Dismissed, the same failure stays quiet after a reload.
    fireEvent.click(within(banner).getByRole('button', { name: 'Dismiss' }));
    expect(screen.queryByText('Your Gmail account needs reconnecting')).not.toBeInTheDocument();
    unmount();
    renderRoute();
    await screen.findAllByRole('navigation', { name: 'Connected by me' });
    expect(screen.queryByText('Your Gmail account needs reconnecting')).not.toBeInTheDocument();
  });

  it('reads mail of an external mailbox without Reply, Forward, or Archive', async () => {
    state.connectors.data = [{ id: 'gmail', auth_kind: 'oauth2', capabilities: { read: true } }];
    state.mailboxes.data = [mailbox(1, 'hello'), externalMailbox(41, 'me@gmail.com')];
    state.connections.data = [
      connection({ id: 1, mailbox_id: 41, account_address: 'me@gmail.com' }),
    ];
    state.inbox.data = listing([
      [
        {
          ...summary({ id: 70, mailbox_id: 41, subject: 'Shipped', source: 'gmail' }),
          mailbox_address: 'me@gmail.com',
          role_id: null,
          snippet: 'Your order has shipped',
        },
      ],
    ]);
    state.detail.data = message({ id: 70, mailbox_id: 41, subject: 'Shipped', source: 'gmail' });
    renderRoute();

    fireEvent.click(rowButton('Shipped'));
    await readingPaneLoaded();

    const pane = screen.getByRole('region', { name: 'Reading pane' });
    expect(within(pane).queryByRole('toolbar')).not.toBeInTheDocument();
    for (const name of ['Reply', 'Reply all', 'Forward', 'Archive']) {
      expect(within(pane).queryByRole('button', { name })).not.toBeInTheDocument();
    }
    expect(within(pane).getByText(/Read-only copy from Gmail/u)).toBeInTheDocument();
  });

  it('opening a thread row loads the thread and marks it read', async () => {
    queryClient.setQueryData(mailboxKeys.inbox(1, { view: 'all', group: 'thread', tz: 'UTC' }), {
      pages: [{ items: [threadRow(), singleRow()], meta: meta(false) }],
      pageParams: [undefined],
    });
    renderRoute();

    fireEvent.click(rowButton('Launch plan'));
    await readingPaneLoaded();

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

  it('opening an unread single message marks it read with the single-message update', async () => {
    renderRoute();

    fireEvent.click(rowButton('Invoice'));
    await readingPaneLoaded();

    expect(state.detailCalls).toContainEqual({ mailboxId: 1, messageId: 12 });
    expect(state.markRead.mutate).toHaveBeenCalledWith(
      { mailboxId: 1, messageId: 12 },
      expect.objectContaining({ onError: expect.any(Function) })
    );
    expect(state.updateInbox.mutate).not.toHaveBeenCalled();
  });

  it('keeps the row named by ?open= open after a reload', async () => {
    window.history.replaceState(null, '', '/console/mailboxes?open=31');
    renderRoute();
    await readingPaneLoaded();

    expect(rowButton('Launch plan')).toHaveAttribute('aria-selected', 'true');
    expect(state.threadCalls).toContainEqual({ mailboxId: 2, threadId: 30 });
  });

  it('archive removes the row at once, opens the next one, and offers a 5 second undo', async () => {
    queryClient.setQueryData(mailboxKeys.inbox(1, { view: 'all', group: 'thread', tz: 'UTC' }), {
      pages: [{ items: [threadRow(), singleRow()], meta: meta(false) }],
      pageParams: [undefined],
    });
    window.history.replaceState(null, '', '/console/mailboxes?open=31');
    renderRoute();

    const toolbar = await screen.findByRole('toolbar', { name: 'Conversation actions' });
    await act(async () => {
      fireEvent.click(within(toolbar).getByRole('button', { name: 'Archive' }));
    });

    expect(state.updateInbox.mutateAsync).toHaveBeenCalledWith({
      mailboxId: 2,
      message_ids: [29, 30, 31],
      placement: 'archive',
    });
    expect(cachedRow(31)).toBeUndefined();
    expect(new URLSearchParams(window.location.search).get('open')).toBe('12');
    expect(toast.success).toHaveBeenCalledWith(
      'Archived',
      expect.objectContaining({
        duration: 5000,
        action: expect.objectContaining({ label: 'Undo' }) as unknown,
      })
    );

    // Undo puts every message of the row back where it was.
    const [, options] = toast.success.mock.calls[0] as [
      string,
      { action: { onClick: () => void } },
    ];
    await act(async () => options.action.onClick());
    expect(state.updateInbox.mutateAsync).toHaveBeenLastCalledWith({
      mailboxId: 2,
      message_ids: [29, 30, 31],
      placement: 'default',
    });
  });

  it('a failed archive puts the row back and says why', async () => {
    queryClient.setQueryData(mailboxKeys.inbox(1, { view: 'all', group: 'thread', tz: 'UTC' }), {
      pages: [{ items: [threadRow(), singleRow()], meta: meta(false) }],
      pageParams: [undefined],
    });
    state.updateInbox.mutateAsync.mockRejectedValueOnce(
      new ApiError('down', ApiErrorCode.COMMON_SERVICE_UNAVAILABLE, 503)
    );
    window.history.replaceState(null, '', '/console/mailboxes?open=31');
    renderRoute();

    const toolbar = await screen.findByRole('toolbar', { name: 'Conversation actions' });
    await act(async () => {
      fireEvent.click(within(toolbar).getByRole('button', { name: 'Archive' }));
    });

    expect(cachedRow(31)).toBeDefined();
    expect(toast.error).toHaveBeenCalled();
    expect(toast.success).not.toHaveBeenCalled();
  });

  it('trash on the last row sends its id and goes back to the list', async () => {
    window.history.replaceState(null, '', '/console/mailboxes?open=12');
    renderRoute();

    const toolbar = await screen.findByRole('toolbar', { name: 'Conversation actions' });
    await act(async () => {
      fireEvent.click(within(toolbar).getByRole('button', { name: 'Delete' }));
    });

    expect(state.updateInbox.mutateAsync).toHaveBeenCalledWith({
      mailboxId: 1,
      message_ids: [12],
      placement: 'trash',
    });
    expect(new URLSearchParams(window.location.search).get('open')).toBeNull();
  });

  it('archive and trash are hidden without send', async () => {
    state.mailboxes.data = [mailbox(1, 'hello'), mailbox(2, 'sales', { send: false })];
    window.history.replaceState(null, '', '/console/mailboxes?open=31');
    renderRoute();
    await readingPaneLoaded();

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

  it('keeps the three-pane shape while the organization and the mailboxes load', () => {
    state.organizations.data = undefined;
    state.organizations.isPending = true;
    const { container, rerender } = renderRoute();

    expect(container.querySelector('[data-inbox-skeleton]')).not.toBeNull();
    expect(screen.queryByRole('heading', { level: 1, name: 'Inbox' })).toBeNull();

    state.organizations.data = page([organization('owner')]);
    state.organizations.isPending = false;
    state.mailboxes.data = undefined;
    state.mailboxes.isPending = true;
    rerender(wrap(<MailboxesRoute />));
    expect(container.querySelector('[data-inbox-skeleton]')).not.toBeNull();
  });

  it('a loading list shows row-shaped skeletons, never the empty view', () => {
    state.inbox.data = undefined;
    state.inbox.isPending = true;
    const { container } = renderRoute();

    expect(container.querySelector('[data-list-skeleton]')).not.toBeNull();
    expect(screen.queryByText('No mail yet')).toBeNull();
    expect(screen.queryByRole('listbox')).toBeNull();
  });

  it('a loading mail shows the subject, sender, and body skeleton in the article column', async () => {
    state.thread.data = undefined;
    state.thread.isPending = true;
    window.history.replaceState(null, '', '/console/mailboxes?open=31');
    const { container } = renderRoute();
    await readingPaneLoaded();

    const skeleton = container.querySelector('[data-reader-skeleton]');
    expect(skeleton).not.toBeNull();
    expect(skeleton).toHaveClass('max-w-[640px]');
    expect(screen.queryByRole('heading', { level: 2, name: 'Launch plan' })).toBeNull();
  });

  it('a view with no mail shows the empty view', () => {
    state.inbox.data = listing([[]]);
    renderRoute();

    expect(screen.getByText('No mail yet')).toBeInTheDocument();
  });

  it('each empty listing says what would show there', () => {
    state.inbox.data = listing([[]]);
    const cases: [string, string, string][] = [
      ['?view=today', 'No new mail today', 'Mail that arrives today shows here, read or not.'],
      [
        '?view=pending',
        'Nothing starred to follow up',
        'Star mail you need to come back to (S) and it waits here.',
      ],
      ['?view=sent', 'No sent mail yet', 'Mail sent from the mailboxes you read shows here.'],
      ['?tab=unread', 'No unread mail', 'You are all caught up.'],
      [
        '?q=invoice',
        'No mail matches this search',
        'Search matches the subject, the sender, and the recipients. Try other words.',
      ],
    ];
    for (const [query, title, hint] of cases) {
      window.history.replaceState(null, '', `/console/mailboxes${query}`);
      const { unmount } = renderRoute();
      expect(screen.getByText(title)).toBeInTheDocument();
      expect(screen.getByText(hint)).toBeInTheDocument();
      unmount();
    }
  });

  it('refreshes messages and unread counts while keeping the open message', () => {
    renderRoute();
    fireEvent.click(rowButton('Launch plan'));
    const url = window.location.href;
    fireEvent.click(screen.getByRole('button', { name: 'Refresh mailbox' }));
    expect(state.inbox.refetch).toHaveBeenCalledTimes(1);
    expect(state.counts.refetch).toHaveBeenCalledTimes(1);
    expect(window.location.href).toBe(url);
  });

  it('a row whose mailbox became unreadable shows the unavailable notice and refetches list and mailboxes', async () => {
    state.thread.data = undefined;
    state.thread.error = new ApiError('gone', ApiErrorCode.MAILBOX_NOT_FOUND, 404);
    const { rerender } = renderRoute();

    fireEvent.click(rowButton('Launch plan'));
    await readingPaneLoaded();

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

  it('a row whose mailbox the mailbox list already shows unreadable shows the notice and refetches the list once', async () => {
    state.mailboxes.data = [mailbox(1, 'hello'), mailbox(2, 'sales', { view: false })];
    const { rerender } = renderRoute();

    fireEvent.click(rowButton('Launch plan'));
    await readingPaneLoaded();

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
      observers.filter(
        observer => !observer.disconnected && observer.target?.hasAttribute('data-list-end')
      );
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
    expect(screen.getByRole('heading', { level: 1, name: 'Today' })).toBeInTheDocument();
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
      screen.getByRole('heading', { level: 1, name: 'sales@mail.example.com' })
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
    expect(screen.getByRole('heading', { level: 1, name: 'Agency' })).toBeInTheDocument();

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
    expect(screen.getByRole('heading', { level: 1, name: 'Ungrouped' })).toBeInTheDocument();
  });

  it('choosing a role or a mailbox drops the search', () => {
    window.history.replaceState(null, '', '/console/mailboxes?q=invoice');
    state.mailboxes.data = [mailbox(1, 'hello', {}, 3), mailbox(2, 'sales')];
    state.roles.data = [role(3, 'Design', [1], 'blue')];
    renderRoute();

    fireEvent.click(within(groupsNav()).getByRole('button', { name: /^Design/u }));
    let params = new URLSearchParams(window.location.search);
    expect([params.get('role'), params.get('q')]).toEqual(['3', null]);
    expect(screen.getByRole('searchbox')).toHaveValue('');

    window.history.replaceState(null, '', '/console/mailboxes?q=invoice&role=3');
    fireEvent.click(mailboxButton(/^hello@/u));
    params = new URLSearchParams(window.location.search);
    expect([params.get('box'), params.get('q')]).toEqual(['1', null]);
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

  it('the roles dialog puts a readable mailbox in a role and the inbox follows', async () => {
    state.mailboxes.data = [
      mailbox(1, 'hello', { administer: true }),
      mailbox(2, 'sales', { administer: true }, 5),
      mailbox(3, 'ops', { administer: false }),
    ];
    state.roles.data = [role(5, 'Agency', [2], 'orange'), role(3, 'Design', [], 'blue')];
    state.inbox.data = listing([[threadRow(), singleRow()]]);
    // The server's answer, as the refetch after the write would show it.
    state.assignRole.mutateAsync.mockImplementation(
      async ({ mailboxId, roleId }: { mailboxId: number; roleId: number | null }) => {
        const mailboxes = state.mailboxes.data as ReturnType<typeof mailbox>[];
        state.mailboxes.data = mailboxes.map(item =>
          item.id === mailboxId ? { ...item, role_id: roleId } : item
        );
        state.roles.data = [
          role(5, 'Agency', mailboxId === 2 && roleId !== 5 ? [] : [2], 'orange'),
          role(3, 'Design', roleId === 3 ? [mailboxId] : [], 'blue'),
        ];
        const rows = [threadRow(), singleRow()];
        state.inbox.data = listing([
          rows.map(row => (row.mailbox_id === mailboxId ? { ...row, role_id: roleId } : row)),
        ]);
        return mailbox(mailboxId, 'x', {}, roleId);
      }
    );
    const view = renderRoute();

    fireEvent.click(within(groupsNav()).getByRole('button', { name: 'Manage roles' }));
    const dialog = await screen.findByRole('dialog', { name: 'Manage roles' });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Mailboxes of Design' }));
    const checklist = within(dialog).getByRole('group', { name: 'Mailboxes of Design' });
    // Only mailboxes the caller administers; one in another role says so.
    expect(within(checklist).queryByRole('checkbox', { name: /ops@/u })).toBeNull();
    expect(within(checklist).getByRole('checkbox', { name: /sales@.*Agency/u })).not.toBeChecked();
    const hello = within(checklist).getByRole('checkbox', { name: /^hello@/u });
    expect(hello).not.toBeChecked();

    await act(async () => {
      fireEvent.click(hello);
    });
    expect(state.assignRole.mutateAsync).toHaveBeenCalledWith({ mailboxId: 1, roleId: 3 });
    view.rerender(wrap(<MailboxesRoute />));

    expect(within(checklist).getByRole('checkbox', { name: /^hello@/u })).toBeChecked();
    // Once the dialog is closed, the row of hello@ carries the Design tag and the sidebar lists
    // hello@ under Design.
    fireEvent.keyDown(dialog, { key: 'Escape' });
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    expect(within(rowButton('Invoice')).getByText('Design')).toBeVisible();
    expect(within(groupsNav()).queryByRole('button', { name: /^hello@/u })).toBeNull();
    fireEvent.click(within(groupsNav()).getByRole('button', { name: /^Design/u }));
    expect(mailboxButton(/^hello@/u)).toBeInTheDocument();

    // Checking a mailbox of another role moves it; unchecking takes it out.
    fireEvent.click(within(groupsNav()).getByRole('button', { name: 'Manage roles' }));
    const again = await screen.findByRole('dialog', { name: 'Manage roles' });
    fireEvent.click(within(again).getByRole('button', { name: 'Mailboxes of Design' }));
    const list = within(again).getByRole('group', { name: 'Mailboxes of Design' });
    await act(async () => {
      fireEvent.click(within(list).getByRole('checkbox', { name: /sales@/u }));
    });
    expect(state.assignRole.mutateAsync).toHaveBeenLastCalledWith({ mailboxId: 2, roleId: 3 });
    await act(async () => {
      fireEvent.click(within(list).getByRole('checkbox', { name: /^hello@/u }));
    });
    expect(state.assignRole.mutateAsync).toHaveBeenLastCalledWith({ mailboxId: 1, roleId: null });
  });

  it('rows name the mailbox that received them while the list spans mailboxes', () => {
    state.mailboxes.data = [mailbox(1, 'hello'), mailbox(2, 'sales', {}, 3)];
    state.roles.data = [role(3, 'Design', [2])];
    state.inbox.data = listing([[{ ...threadRow(), role_id: 3 }, singleRow()]]);
    const { unmount } = renderRoute();

    const sales = screen.getByRole('button', { name: 'Filter by mailbox sales@mail.example.com' });
    expect(sales).toHaveTextContent('sales@mail.example.com');
    expect(screen.getByRole('button', { name: 'Filter by mailbox hello@mail.example.com' })).toBeVisible();
    fireEvent.click(sales);
    expect(window.location.search).toContain('box=2');
    unmount();

    // A role still spans its mailboxes; a single mailbox needs no label.
    window.history.replaceState(null, '', '/console/mailboxes?role=3');
    let view = renderRoute();
    expect(screen.getByRole('button', { name: 'Filter by mailbox sales@mail.example.com' })).toBeInTheDocument();
    view.unmount();
    window.history.replaceState(null, '', '/console/mailboxes?box=2');
    view = renderRoute();
    expect(screen.queryByRole('button', { name: 'Filter by mailbox sales@mail.example.com' })).toBeNull();
    view.unmount();
  });

  it('without roles the sidebar lists the mailboxes themselves', () => {
    state.mailboxes.data = [mailbox(1, 'hello'), mailbox(2, 'sales')];
    state.roles.data = [];
    state.counts.data = {
      ...counts(),
      mailboxes: [
        { mailbox_id: 1, unread: 0 },
        { mailbox_id: 2, unread: 2 },
      ],
    };
    renderRoute();

    // No lone "Ungrouped" folder to open first: every mailbox is one click away.
    expect(groupNames()).toEqual([]);
    expect(within(groupsNav()).getByText('Mailboxes')).toBeInTheDocument();
    expect(screen.getByText('2 mailboxes')).toBeInTheDocument();
    expect(screen.queryByText(/0 roles/u)).toBeNull();
    expect(mailboxButton(/^hello@/u)).toBeInTheDocument();
    fireEvent.click(mailboxButton(/^sales@/u));
    expect(new URLSearchParams(window.location.search).get('box')).toBe('2');
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

  it('quota card shows own stats to members and the organization limit only to admins', () => {
    const { unmount } = renderRoute();

    let card = screen.getByRole('region', { name: 'Sent today' });
    expect(card).toHaveTextContent('Mailboxes you can see: sent 7 · failed 1 (UTC day)');
    expect(card).toHaveTextContent('Organization limit 200 / rolling 24 hours');
    // No progress bar, no breakdown, and nothing queued.
    expect(within(card).queryByRole('progressbar')).not.toBeInTheDocument();
    expect(within(card).queryByRole('link')).not.toBeInTheDocument();
    unmount();

    state.organizations.data = page([organization('member')]);
    renderRoute();
    card = screen.getByRole('region', { name: 'Sent today' });
    expect(card).toHaveTextContent('Mailboxes you can see: sent 7 · failed 1 (UTC day)');
    expect(card).not.toHaveTextContent('Organization limit');
  });

  it('quota card shows queued and links to sent when sent_waiting is positive', () => {
    state.organizations.data = page([organization('member')]);
    state.counts.data = counts({ sent_waiting: 3 });
    renderRoute();

    const card = screen.getByRole('region', { name: 'Sent today' });
    const queued = within(card).getByRole('link', { name: 'Limit reached, queued' });
    expect(new URL(queued.getAttribute('href') ?? '', 'http://x').searchParams.get('view')).toBe(
      'sent'
    );
    // The number of queued messages is not shown.
    expect(card).not.toHaveTextContent('3');
  });

  it('reading pane toolbar hides send actions without send', async () => {
    window.history.replaceState(null, '', '/console/mailboxes?open=31');
    const { unmount } = renderRoute();

    const toolbar = await screen.findByRole('toolbar', { name: 'Conversation actions' });
    for (const name of ['Reply', 'Reply all', 'Forward', 'Archive', 'Delete']) {
      expect(within(toolbar).getByRole('button', { name })).toBeInTheDocument();
    }
    // The reading column is an article with the subject and the mailbox the mail came to.
    const article = screen.getByRole('article');
    expect(within(article).getByRole('heading', { level: 2, name: 'Launch plan' })).toBeVisible();
    expect(article).toHaveTextContent('To sales@mail.example.com');
    unmount();

    state.mailboxes.data = [mailbox(1, 'hello'), mailbox(2, 'sales', { send: false })];
    renderRoute();
    await readingPaneLoaded();
    expect(screen.queryByRole('toolbar', { name: 'Conversation actions' })).not.toBeInTheDocument();
    for (const name of ['Reply', 'Reply all', 'Forward', 'Archive', 'Delete']) {
      expect(screen.queryByRole('button', { name })).not.toBeInTheDocument();
    }
    expect(screen.getByRole('list', { name: 'Conversation' })).toBeInTheDocument();
  });

  it('rows expose listbox and option roles with aria-selected', () => {
    window.history.replaceState(null, '', '/console/mailboxes?open=31');
    renderRoute();

    const listbox = screen.getByRole('listbox');
    const options = within(listbox).getAllByRole('option');
    expect(options).toHaveLength(2);
    expect(rowButton('Launch plan')).toHaveAttribute('aria-selected', 'true');
    expect(rowButton('Invoice')).toHaveAttribute('aria-selected', 'false');

    fireEvent.click(rowButton('Invoice'));
    expect(rowButton('Invoice')).toHaveAttribute('aria-selected', 'true');
    expect(rowButton('Launch plan')).toHaveAttribute('aria-selected', 'false');
  });

  it('revealing the newest message of a thread shows its original subject in the heading', async () => {
    state.thread.data = [
      message({ id: 29, thread_id: 30, subject: 'Your code is ••••••' }),
      message({
        id: 31,
        thread_id: 30,
        subject: 'Your code is ••••••',
        secrets_masked: true,
      }),
    ];
    const reveal = vi.spyOn(mailboxService, 'revealMessage').mockResolvedValue(
      message({
        id: 31,
        thread_id: 30,
        subject: 'Your code is 482913',
        secrets_masked: false,
      }) as never
    );
    window.history.replaceState(null, '', '/console/mailboxes?open=31');
    renderRoute();

    expect(
      await screen.findByRole('heading', { level: 2, name: 'Your code is ••••••' })
    ).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Show codes' }));

    expect(await screen.findByText('Codes shown')).toBeInTheDocument();
    expect(reveal).toHaveBeenCalledWith(1, 2, 31);
    expect(
      screen.getByRole('heading', { level: 2, name: 'Your code is 482913' })
    ).toBeInTheDocument();
  });

  it('narrow layout shows a back button from the reading pane', async () => {
    window.history.replaceState(null, '', '/console/mailboxes?open=31');
    renderRoute();

    // Below 1024px one pane shows at a time: the list gives way to the open mail.
    const list = screen.getByRole('listbox').closest('section');
    expect(list).toHaveClass('max-lg:hidden');
    fireEvent.click(await screen.findByRole('button', { name: 'Back to list' }));

    expect(new URLSearchParams(window.location.search).get('open')).toBeNull();
    expect(screen.getByRole('listbox').closest('section')).not.toHaveClass('max-lg:hidden');
    expect(screen.queryByRole('button', { name: 'Back to list' })).not.toBeInTheDocument();
  });
});

describe('inbox keyboard shortcuts', () => {
  beforeEach(() => {
    queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    toast.success.mockReset();
    toast.error.mockReset();
    window.localStorage.clear();
    window.history.replaceState(null, '', '/console/mailboxes');
    state.organizations.data = page([organization('owner')]);
    state.mailboxes.data = [mailbox(1, 'hello'), mailbox(2, 'sales')];
    state.domains.data = [domain()];
    state.stats.data = [];
    state.inbox.data = listing([[threadRow(), singleRow()]]);
    state.inbox.hasNextPage = false;
    state.inbox.isFetchingNextPage = false;
    state.inbox.fetchNextPage.mockReset();
    state.reject = null;
    state.counts.data = counts();
    state.thread.data = [message({ id: 29, thread_id: 30 }), message({ id: 31, thread_id: 30 })];
    state.thread.error = null;
    state.detail.data = message({ id: 12, mailbox_id: 1 });
    state.detail.error = null;
    state.roles.data = [];
    for (const mutation of [state.markRead, state.updateInbox, state.update, state.send]) {
      mutation.isPending = false;
      mutation.mutate.mockReset();
      mutation.mutateAsync.mockReset();
      mutation.mutateAsync.mockResolvedValue({ updated: 1 });
    }
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('j j e archives the second row', async () => {
    renderRoute();

    press('j');
    expect(openParam()).toBe('31');
    press('j');
    expect(openParam()).toBe('12');
    await readingPaneLoaded();
    await act(async () => press('e'));

    expect(state.updateInbox.mutateAsync).toHaveBeenCalledTimes(1);
    expect(state.updateInbox.mutateAsync).toHaveBeenCalledWith({
      mailboxId: 1,
      message_ids: [12],
      placement: 'archive',
    });
    // k moves back up; the arrow keys do the same as j and k.
    window.history.replaceState(null, '', '/console/mailboxes?open=12');
    press('k');
    expect(openParam()).toBe('31');
    press('ArrowDown');
    expect(openParam()).toBe('12');
  });

  it('e e archives two rows in a row without waiting for the first answer', async () => {
    // The first archive is still on its way when the second key comes.
    state.updateInbox.mutateAsync.mockImplementationOnce(() => new Promise(() => undefined));
    state.updateInbox.isPending = true;
    window.history.replaceState(null, '', '/console/mailboxes?open=31');
    renderRoute();
    await readingPaneLoaded();

    await act(async () => press('e'));
    expect(openParam()).toBe('12');
    await readingPaneLoaded();
    await act(async () => press('e'));

    expect(state.updateInbox.mutateAsync).toHaveBeenCalledTimes(2);
    expect(state.updateInbox.mutateAsync).toHaveBeenLastCalledWith({
      mailboxId: 1,
      message_ids: [12],
      placement: 'archive',
    });
  });

  it('r opens reply from the row mailbox', async () => {
    window.history.replaceState(null, '', '/console/mailboxes?open=31');
    renderRoute();
    await readingPaneLoaded();

    press('r');

    const dialog = await screen.findByRole('dialog', { name: 'Reply' });
    expect(dialog).toHaveTextContent('Sent from sales@mail.example.com.');
    expect(within(dialog).queryByLabelText('From')).not.toBeInTheDocument();
  });

  it('e on a view-only mailbox sends nothing and toasts', async () => {
    state.mailboxes.data = [mailbox(1, 'hello'), mailbox(2, 'sales', { send: false })];
    window.history.replaceState(null, '', '/console/mailboxes?open=31');
    renderRoute();
    await readingPaneLoaded();

    for (const key of ['e', '#', 'r', 'a', 'f']) {
      await act(async () => press(key));
    }

    expect(state.updateInbox.mutateAsync).not.toHaveBeenCalled();
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(toast.error).toHaveBeenCalledTimes(5);
    expect(toast.error).toHaveBeenCalledWith('You don’t have permission to send from this mailbox.');
  });

  it('c needs a mailbox that may send', () => {
    state.mailboxes.data = [mailbox(1, 'hello', { send: false })];
    renderRoute();

    press('c');

    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(toast.error).toHaveBeenCalledWith('You don’t have a mailbox you can send from.');
  });

  it('single keys are ignored in the search box and during IME composition', () => {
    renderRoute();
    const search = screen.getByRole('searchbox', { name: 'Search all mail' });

    search.focus();
    press('j', search);
    fireEvent.keyDown(document.body, { key: 'j', isComposing: true });
    expect(openParam()).toBeNull();
    // A modifier combination is left to the browser.
    press('j', document.body, { ctrlKey: true });
    expect(openParam()).toBeNull();

    // `/` focuses the search box without typing into it.
    search.blur();
    const event = press('/');
    expect(event.defaultPrevented).toBe(true);
    expect(search).toHaveFocus();
  });

  it('Escape clears the search box', () => {
    window.history.replaceState(null, '', '/console/mailboxes?q=invoice');
    renderRoute();
    const search = screen.getByRole('searchbox', { name: 'Search all mail' });
    expect(search).toHaveValue('invoice');

    press('Escape', search);

    expect(search).toHaveValue('');
    expect(new URLSearchParams(window.location.search).get('q')).toBeNull();
  });

  it('Escape closes the open mail', async () => {
    window.history.replaceState(null, '', '/console/mailboxes?open=12');
    renderRoute();
    await readingPaneLoaded();

    press('Escape');

    expect(openParam()).toBeNull();
    // The focus comes back to the row that was open, which Tab now reaches.
    expect(rowButton('Invoice')).toHaveFocus();
    expect(rowButton('Invoice')).toHaveAttribute('tabindex', '0');
    expect(rowButton('Launch plan')).toHaveAttribute('tabindex', '-1');
  });

  it('closing the mail from a control in the pane returns the focus to its row', async () => {
    window.history.replaceState(null, '', '/console/mailboxes?open=12');
    renderRoute();
    const back = await screen.findByRole('button', { name: 'Back to list' });
    back.focus();

    fireEvent.click(back);

    expect(openParam()).toBeNull();
    expect(rowButton('Invoice')).toHaveFocus();
  });

  it('closing the mail leaves the focus alone when it is elsewhere', async () => {
    window.history.replaceState(null, '', '/console/mailboxes?open=12');
    renderRoute();
    await readingPaneLoaded();
    const search = screen.getByRole('searchbox', { name: 'Search all mail' });
    search.focus();

    press('Escape', search);

    expect(openParam()).toBeNull();
    expect(search).toHaveFocus();
  });

  it('s stars optimistically and rolls back on error', async () => {
    queryClient.setQueryData(mailboxKeys.inbox(1, { view: 'all', group: 'thread', tz: 'UTC' }), {
      pages: [{ items: [threadRow(), singleRow()], meta: meta(false) }],
      pageParams: [undefined],
    });
    let reject!: (error: unknown) => void;
    const pending = new Promise<never>((_, fail) => {
      reject = fail;
    });
    const update = vi.spyOn(mailboxService, 'updateMessage').mockReturnValue(pending);
    window.history.replaceState(null, '', '/console/mailboxes?open=12');
    renderRoute();
    await readingPaneLoaded();

    await act(async () => press('s'));

    expect(update).toHaveBeenCalledWith(1, 1, 12, { starred: true });
    expect(cachedRow(12)?.starred).toBe(true);

    await act(async () => {
      reject(new ApiError('down', ApiErrorCode.COMMON_SERVICE_UNAVAILABLE, 503));
      await pending.catch(() => undefined);
    });
    await waitFor(() => expect(cachedRow(12)?.starred).toBe(false));
    expect(toast.error).toHaveBeenCalled();
  });

  it('s on a thread row stars every message of the thread', async () => {
    const updates = vi
      .spyOn(mailboxService, 'updateMessages')
      .mockResolvedValue({ updated: 3 } as never);
    window.history.replaceState(null, '', '/console/mailboxes?open=31');
    renderRoute();
    await readingPaneLoaded();

    await act(async () => press('s'));

    expect(updates).toHaveBeenCalledWith(1, 2, { message_ids: [29, 30, 31], starred: true });
  });

  it('j past the last loaded row loads and selects the next page', () => {
    state.inbox.data = listing([[threadRow(), singleRow()]], true);
    state.inbox.hasNextPage = true;
    window.history.replaceState(null, '', '/console/mailboxes?open=12');
    const { rerender } = renderRoute();

    press('j');

    expect(state.inbox.fetchNextPage).toHaveBeenCalledTimes(1);
    expect(openParam()).toBe('12');

    const next = {
      ...singleRow(),
      ...summary({ id: 40, subject: 'Renewal', created_at: '2026-11-14T09:00:00Z' }),
    };
    state.inbox.data = listing([[threadRow(), singleRow()], [next]], false);
    state.inbox.hasNextPage = false;
    rerender(wrap(<MailboxesRoute />));

    expect(openParam()).toBe('40');
    expect(rowButton('Renewal')).toHaveAttribute('aria-selected', 'true');
  });

  it('closing the compose dialog keeps what was written as a draft, without asking', async () => {
    renderRoute();

    press('c');
    const subject = await screen.findByLabelText('Subject');
    fireEvent.change(subject, { target: { value: 'Quarterly numbers' } });
    fireEvent.change(screen.getByLabelText('Message'), { target: { value: 'Draft body' } });
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));

    await waitFor(() => expect(screen.queryByLabelText('Subject')).toBeNull());
    expect(screen.queryByRole('alertdialog')).toBeNull();
    expect(toast.success).toHaveBeenCalledWith('Saved as a draft');

    // The next new message starts from the draft.
    press('c');
    expect(await screen.findByLabelText('Subject')).toHaveValue('Quarterly numbers');
    expect(screen.getByLabelText('Message')).toHaveValue('Draft body');

    // A dialog closed with nothing written keeps no draft and says nothing.
    fireEvent.change(screen.getByLabelText('Subject'), { target: { value: '' } });
    fireEvent.change(screen.getByLabelText('Message'), { target: { value: '' } });
    toast.success.mockReset();
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    await waitFor(() => expect(screen.queryByLabelText('Subject')).toBeNull());
    expect(toast.success).not.toHaveBeenCalled();
    press('c');
    expect(await screen.findByLabelText('Subject')).toHaveValue('');
  });

  it('c defaults the sender to the narrowed mailbox', async () => {
    window.localStorage.setItem('zmail.mailbox.1', '1');
    window.history.replaceState(null, '', '/console/mailboxes?box=2');
    const { unmount } = renderRoute();

    press('c');
    expect(await screen.findByLabelText('From')).toHaveValue('2');
    unmount();

    // Unnarrowed, the remembered mailbox comes first, then the first that may send.
    window.history.replaceState(null, '', '/console/mailboxes');
    window.localStorage.setItem('zmail.mailbox.1', '2');
    const second = renderRoute();
    press('c');
    expect(await screen.findByLabelText('From')).toHaveValue('2');
    second.unmount();

    window.localStorage.clear();
    renderRoute();
    press('c');
    expect(await screen.findByLabelText('From')).toHaveValue('1');
  });

  it('? opens the shortcut help, and keys wait while it is open', async () => {
    renderRoute();

    press('?', document.body, { shiftKey: true });

    const help = await screen.findByRole('dialog', { name: 'Keyboard shortcuts' });
    expect(help).toHaveTextContent('Next message');
    press('j');
    expect(openParam()).toBeNull();
    fireEvent.click(within(help).getByRole('button', { name: 'Close' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());

    // The sidebar footer opens it too.
    fireEvent.click(screen.getByRole('button', { name: 'Shortcuts' }));
    expect(await screen.findByRole('dialog', { name: 'Keyboard shortcuts' })).toBeInTheDocument();
  });

  it('the sidebar footer links to the developer console in the active organization', async () => {
    renderRoute();
    const footer = await screen.findByRole('navigation', { name: 'Mail pages' });
    const link = within(footer).getByRole('link', { name: 'Developers' });
    expect(link.getAttribute('href')).toMatch(/^\/console\/developers\?org=1$/u);
  });
});

describe('command palette', () => {
  beforeEach(() => {
    queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    toast.success.mockReset();
    toast.error.mockReset();
    window.localStorage.clear();
    window.history.replaceState(null, '', '/console/mailboxes');
    state.organizations.data = page([organization('owner')]);
    state.mailboxes.data = [
      mailbox(1, 'hello'),
      mailbox(2, 'sales', {}, 7),
      mailbox(3, 'board', { view: false, send: false }),
    ];
    state.domains.data = [domain()];
    state.stats.data = [];
    state.inbox.data = listing([[threadRow(), singleRow()]]);
    state.inbox.hasNextPage = false;
    state.inbox.isFetchingNextPage = false;
    state.reject = null;
    state.counts.data = counts();
    state.thread.data = [message({ id: 29, thread_id: 30 }), message({ id: 31, thread_id: 30 })];
    state.detail.data = message({ id: 12, mailbox_id: 1 });
    state.roles.data = [role(7, 'Sales', [2], 'green')];
    vi.spyOn(mailboxService, 'listInbox').mockResolvedValue({ items: [], meta: meta(false) });
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  async function openPalette(init: KeyboardEventInit = { metaKey: true }) {
    const event = press('k', document.body, init);
    expect(event.defaultPrevented).toBe(true);
    const palette = await screen.findByRole('dialog', { name: 'Command palette' });
    return { palette, input: within(palette).getByRole('combobox') };
  }

  it('the palette shadow and backdrop come from the theme shadow color, not a literal', async () => {
    renderRoute();
    const { palette } = await openPalette();
    const backdrop = document.querySelector('[data-state="open"].fixed.inset-0');
    expect(backdrop).not.toBeNull();
    for (const className of [palette.className, backdrop!.className]) {
      expect(className).toContain('var(--shadow-color)');
      expect(className).not.toMatch(/rgba\(|#[0-9a-f]{3,8}\b/i);
    }
  });

  it('⌘K and Ctrl+K open the palette with its actions', async () => {
    renderRoute();

    const { palette, input } = await openPalette();
    expect(input).toHaveFocus();
    const actions = within(palette).getByRole('group', { name: 'Actions' });
    expect(within(actions).getByRole('option', { name: /New message/ })).toBeInTheDocument();
    expect(within(actions).getByRole('option', { name: /Manage mailboxes/ })).toBeInTheDocument();
    expect(within(actions).getByRole('option', { name: /Manage roles/ })).toBeInTheDocument();
    fireEvent.keyDown(input, { key: 'Escape' });
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());

    await openPalette({ ctrlKey: true });
  });

  it('a member sees no management actions', async () => {
    state.organizations.data = page([organization('member')]);
    renderRoute();

    const { palette } = await openPalette();
    expect(within(palette).getByRole('option', { name: /New message/ })).toBeInTheDocument();
    expect(within(palette).queryByRole('option', { name: /Manage mailboxes/ })).toBeNull();
    expect(within(palette).queryByRole('option', { name: /Manage roles/ })).toBeNull();
  });

  it('lists an unreadable mailbox locked and does not select it', async () => {
    renderRoute();
    const { palette, input } = await openPalette();

    fireEvent.change(input, { target: { value: 'board@' } });
    const locked = within(palette).getByRole('option', { name: /board@mail\.example\.com/ });
    expect(locked).toHaveAttribute('aria-disabled', 'true');
    fireEvent.click(locked);
    fireEvent.keyDown(input, { key: 'Enter' });

    expect(new URLSearchParams(window.location.search).get('box')).toBeNull();
    expect(screen.getByRole('dialog', { name: 'Command palette' })).toBeInTheDocument();
  });

  it('arrow keys and Enter run the highlighted result', async () => {
    renderRoute();
    const { palette, input } = await openPalette();

    fireEvent.change(input, { target: { value: 'sa' } });
    const options = within(palette).getAllByRole('option');
    // The mailbox whose address starts with the query comes first, then the role.
    expect(options[0]).toHaveAccessibleName(/sales@mail\.example\.com/);
    expect(input).toHaveAttribute('aria-activedescendant', options[0].id);
    fireEvent.keyDown(input, { key: 'ArrowDown' });
    expect(input).toHaveAttribute('aria-activedescendant', options[1].id);
    expect(options[1]).toHaveAccessibleName(/Sales/);
    fireEvent.keyDown(input, { key: 'Enter' });

    expect(new URLSearchParams(window.location.search).get('role')).toBe('7');
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
  });

  it('searches mail 250 ms after typing stops, five results at most, and opens one', async () => {
    const search = vi.spyOn(mailboxService, 'listInbox').mockResolvedValue({
      items: [singleRow()],
      meta: meta(false),
    } as never);
    renderRoute();
    const { palette, input } = await openPalette();

    vi.useFakeTimers();
    fireEvent.change(input, { target: { value: 'inv' } });
    act(() => vi.advanceTimersByTime(200));
    fireEvent.change(input, { target: { value: 'invoice' } });
    act(() => vi.advanceTimersByTime(249));
    expect(search).not.toHaveBeenCalled();
    await act(async () => vi.advanceTimersByTime(1));
    vi.useRealTimers();

    expect(search).toHaveBeenCalledTimes(1);
    expect(search).toHaveBeenCalledWith(
      1,
      expect.objectContaining({ view: 'all', q: 'invoice' }),
      undefined,
      5
    );
    const mail = await within(palette).findByRole('group', { name: 'Mail' });
    fireEvent.click(within(mail).getByRole('option', { name: /Invoice/ }));

    const params = new URLSearchParams(window.location.search);
    expect(params.get('q')).toBe('invoice');
    expect(params.get('open')).toBe('12');
  });

  it('asks for a longer keyword when the search is too broad', async () => {
    vi.spyOn(mailboxService, 'listInbox').mockRejectedValue(
      new ApiError('broad', ApiErrorCode.MAILBOX_SEARCH_TOO_BROAD, 422)
    );
    renderRoute();
    const { palette, input } = await openPalette();

    fireEvent.change(input, { target: { value: 'a' } });

    const mail = await within(palette).findByRole('group', { name: 'Mail' }, { timeout: 2000 });
    await waitFor(() => expect(mail).toHaveTextContent('Enter a longer keyword'));
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

/** Presses `key` on `target` (the page by default); answers the event, to see what it prevented. */
function press(key: string, target: Element = document.body, init: KeyboardEventInit = {}) {
  const event = new KeyboardEvent('keydown', { key, bubbles: true, cancelable: true, ...init });
  act(() => {
    target.dispatchEvent(event);
  });
  return event;
}

function openParam() {
  return new URLSearchParams(window.location.search).get('open');
}

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

/** The reading pane loads on demand; resolves once its bar is on screen. */
async function readingPaneLoaded() {
  await screen.findByRole('button', { name: 'Previous message' });
}

function rowButton(subject: string) {
  return screen.getByRole('option', { name: new RegExp(subject, 'u') });
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

function noAccess() {
  return {
    view: false,
    send: false,
    manage_members: false,
    administer: false,
    see_history: false,
    organize: false,
  };
}

/** The viewer's own external mailbox: they read it; nobody sends from it or organizes it. */
function externalMailbox(id: number, address: string) {
  return {
    ...mailbox(id, 'external'),
    address,
    type: 'external',
    owner_user_id: 1,
    access: { ...noAccess(), view: true, see_history: true },
  };
}

function connection(overrides: Record<string, unknown>) {
  return {
    id: 1,
    provider: 'gmail',
    account_address: 'me@gmail.com',
    mailbox_id: 41,
    status: 'active',
    last_synced_at: '2026-11-20T08:00:00Z',
    last_error_code: null,
    created_at: '2026-11-19T08:00:00Z',
    ...overrides,
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
