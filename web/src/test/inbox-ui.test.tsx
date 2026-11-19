import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, fireEvent, render, screen, within } from '@testing-library/react';
import { NextIntlClientProvider, type AbstractIntlMessages } from 'next-intl';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { MailboxesRoute } from '@/features/mailbox/components/mailboxes-route';
import { markInboxRowRead } from '@/features/mailbox/hooks/use-inbox';
import { mailboxKeys } from '@/features/mailbox/hooks/use-mailbox';
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
    /** What the inbox answers for a `tz` the server does not know; null answers every zone. */
    rejectedZone: null as string | null,
    inboxCalls: [] as InboxFilters[],
    thread: query(),
    detail: query(),
    threadCalls: [] as { mailboxId: number; threadId: number | null }[],
    detailCalls: [] as { mailboxId: number; messageId: number | null }[],
    markRead: mutation(),
    updateInbox: mutation(),
    update: mutation(),
    removeMessage: mutation(),
    send: mutation(),
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
  useInboxMessages: (_organizationId: number, filters: InboxFilters) => {
    state.inboxCalls.push(filters);
    if (state.rejectedZone !== null && filters.tz === state.rejectedZone) {
      return {
        ...state.inbox,
        data: undefined,
        error: new ApiError('bad zone', ApiErrorCode.COMMON_INVALID_INPUT, 400),
      };
    }
    return state.inbox;
  },
  useUpdateInboxMessages: () => state.updateInbox,
}));

let queryClient: QueryClient;

describe('unified inbox', () => {
  beforeEach(() => {
    queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    toast.success.mockReset();
    toast.error.mockReset();
    navigation.replace.mockClear();
    window.localStorage.clear();
    window.history.replaceState(null, '', '/console/mailboxes');
    state.organizations.data = page([organization('owner')]);
    state.mailboxes.data = [mailbox(1, 'hello'), mailbox(2, 'sales')];
    state.domains.data = [domain()];
    state.inbox.data = listing([[threadRow(), singleRow()]]);
    state.rejectedZone = null;
    state.thread.data = [message({ id: 29, thread_id: 30 }), message({ id: 31, thread_id: 30 })];
    state.detail.data = message({ id: 12, mailbox_id: 1 });
    for (const query of [
      state.organizations,
      state.mailboxes,
      state.domains,
      state.inbox,
      state.thread,
      state.detail,
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

  it('renders the unified inbox by default and the mailbox workspace for ?mailbox=', () => {
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
    // Each readable mailbox is a way into its own workspace.
    expect(screen.getByRole('link', { name: 'sales@mail.example.com' })).toHaveAttribute(
      'href',
      '/console/mailboxes?org=1&mailbox=2'
    );
    unmount();

    window.history.replaceState(null, '', '/console/mailboxes?mailbox=2');
    renderRoute();
    expect(screen.getByTestId('mailbox-workspace')).toBeInTheDocument();
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
    const resolved = Intl.DateTimeFormat.prototype.resolvedOptions;
    vi.spyOn(Intl.DateTimeFormat.prototype, 'resolvedOptions').mockImplementation(function (
      this: Intl.DateTimeFormat
    ) {
      return { ...resolved.call(this), timeZone: 'Mars/Olympus_Mons' };
    });
    state.rejectedZone = 'Mars/Olympus_Mons';
    renderRoute();

    expect(state.inboxCalls[0]).toMatchObject({ tz: 'Mars/Olympus_Mons' });
    expect(state.inboxCalls.at(-1)).toMatchObject({ view: 'all', group: 'thread', tz: 'UTC' });
    expect(rowButton('Launch plan')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Retry' })).not.toBeInTheDocument();
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

function mailbox(id: number, localPart: string, access: Record<string, boolean> = {}) {
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
