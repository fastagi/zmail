import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, fireEvent, render, screen, within } from '@testing-library/react';
import { NextIntlClientProvider, type AbstractIntlMessages } from 'next-intl';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { MailboxesRoute } from '@/features/mailbox/components/mailboxes-route';
import type { InboxFilters } from '@/features/mailbox/types';
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
    /** Whether the `setting` feature runs; the user's settings come from `userSettings`. */
    settingsEnabled: false,
    userSettings: vi.fn(async (): Promise<unknown> => []),
    override: mutation(),
    draft: { ...mutation(), data: undefined as unknown, error: null as unknown },
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
vi.mock('@/features/mailbox/hooks/use-mail-triage', () => ({
  useOverrideMailTriage: () => state.override,
  useMailReplyDraft: () => state.draft,
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

describe('unified inbox AI triage', () => {
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
    state.override.mutate.mockReset();
    state.draft.mutate.mockReset();
    state.draft.isPending = false;
    state.draft.data = undefined;
    state.draft.error = null;
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it('shows nothing of AI triage while the organization has it off', async () => {
    window.history.replaceState(null, '', '/console/mailboxes?open=12&triage=reply');
    renderRoute();
    await readingPaneLoaded();

    expect(screen.queryByRole('tablist', { name: 'AI triage' })).not.toBeInTheDocument();
    expect(screen.queryByText(/AI sorted your mail/u)).not.toBeInTheDocument();
    expect(screen.queryByRole('region', { name: 'AI triage' })).not.toBeInTheDocument();
    // A triage tab in the URL is ignored while triage is off, so the list stays grouped.
    expect(state.inboxCalls.at(-1)).not.toHaveProperty('triage');
    press('F', document.body, { shiftKey: true });
    expect(screen.queryByRole('menu')).not.toBeInTheDocument();
  });

  it('shows the tabs with unread counts and lists one tab without grouping', async () => {
    state.counts.data = { ...counts(), triage: { reply: 2, deadline: 1, fyi: 4, review: 1 } };
    state.inbox.data = listing([[threadRow(), { ...singleRow(), triage: replyTriage() }]]);
    renderRoute();

    expect(await screen.findByText(/AI sorted your mail/u)).toBeInTheDocument();
    const tabs = screen.getByRole('tablist', { name: 'AI triage' });
    expect(within(tabs).getByRole('tab', { name: 'Everything' })).toHaveAttribute(
      'aria-selected',
      'true'
    );
    // `review` rides with `reply`.
    expect(within(tabs).getByRole('tab', { name: 'Needs your reply 3' })).toBeInTheDocument();
    expect(within(tabs).getByRole('tab', { name: 'Has a deadline 1' })).toBeInTheDocument();
    expect(within(tabs).getByRole('tab', { name: 'Just so you know 4' })).toBeInTheDocument();
    // The row shows its category and reason under the subject.
    expect(rowButton('Invoice')).toHaveTextContent('ReplyAsks you a question');

    fireEvent.click(within(tabs).getByRole('tab', { name: 'Needs your reply 3' }));
    expect(new URLSearchParams(window.location.search).get('triage')).toBe('reply');
    expect(state.inboxCalls.at(-1)).toMatchObject({ view: 'all', triage: ['reply', 'review'] });
    expect(state.inboxCalls.at(-1)).not.toHaveProperty('group');
  });

  it('opens the override menu with Shift F and records the choice', async () => {
    state.counts.data = { ...counts(), triage: { reply: 1, deadline: 0, fyi: 0, review: 0 } };
    state.inbox.data = listing([[threadRow(), { ...singleRow(), triage: replyTriage() }]]);
    state.detail.data = message({ id: 12, mailbox_id: 1, subject: 'Invoice' });
    window.history.replaceState(null, '', '/console/mailboxes?open=12');
    renderRoute();
    await readingPaneLoaded();

    const panel = await screen.findByRole('region', { name: 'AI triage' });
    expect(panel).toHaveTextContent('Asks you a question');
    // Lowercase f still forwards; the capital opens the menu.
    press('F', document.body, { shiftKey: true });
    const choice = await screen.findByRole('menuitemradio', { name: 'FYI' });
    expect(screen.queryByRole('menuitemradio', { name: 'Check this' })).not.toBeInTheDocument();
    fireEvent.click(choice);

    expect(state.override.mutate).toHaveBeenCalledWith(
      { mailboxId: 1, messageId: 12, category: 'fyi' },
      expect.anything()
    );
  });

  it('drafts a reply on request and puts it in the reply window', async () => {
    state.counts.data = { ...counts(), triage: { reply: 1, deadline: 0, fyi: 0, review: 0 } };
    state.inbox.data = listing([[threadRow(), { ...singleRow(), triage: replyTriage() }]]);
    state.detail.data = message({ id: 12, mailbox_id: 1, subject: 'Invoice' });
    window.history.replaceState(null, '', '/console/mailboxes?open=12');
    const { rerender } = renderRoute();
    await readingPaneLoaded();

    fireEvent.click(await screen.findByRole('button', { name: 'Draft a reply' }));
    expect(state.draft.mutate).toHaveBeenCalledWith({ mailboxId: 1, messageId: 12 });

    state.draft.data = {
      body: 'Thanks, paying it today.',
      basis: [{ kind: 'message', id: 12, label: 'Invoice' }],
    };
    rerender(wrap(<MailboxesRoute />));
    expect(screen.getByText('Thanks, paying it today.')).toBeInTheDocument();
    expect(screen.getByText('Based on: Invoice')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Reply with this draft' }));

    const dialog = await screen.findByRole('dialog', { name: 'Reply' });
    const body = within(dialog).getAllByRole('textbox').at(-1) as HTMLTextAreaElement;
    expect(body.value.startsWith('Thanks, paying it today.')).toBe(true);
  });

  it('offers no draft without send on the mailbox', async () => {
    state.mailboxes.data = [mailbox(1, 'hello', { send: false }), mailbox(2, 'sales')];
    state.counts.data = { ...counts(), triage: { reply: 1, deadline: 0, fyi: 0, review: 0 } };
    state.inbox.data = listing([[threadRow(), { ...singleRow(), triage: replyTriage() }]]);
    state.detail.data = message({ id: 12, mailbox_id: 1, subject: 'Invoice' });
    window.history.replaceState(null, '', '/console/mailboxes?open=12');
    renderRoute();
    await readingPaneLoaded();

    expect(await screen.findByRole('region', { name: 'AI triage' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Draft a reply' })).not.toBeInTheDocument();
  });
});

function replyTriage() {
  return { category: 'reply', reason: 'Asks you a question', due_at: null, source: 'ai' };
}

function press(key: string, target: Element = document.body, init: KeyboardEventInit = {}) {
  const event = new KeyboardEvent('keydown', { key, bubbles: true, cancelable: true, ...init });
  act(() => {
    target.dispatchEvent(event);
  });
  return event;
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
