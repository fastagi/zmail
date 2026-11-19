import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { QueryClientProvider } from '@tanstack/react-query';
import { NextIntlClientProvider, type AbstractIntlMessages } from 'next-intl';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { createQueryClient } from '@/config/query-client';
import { MailboxWorkspace } from '@/features/mailbox/components/mailbox-workspace';
import { messages } from '@/i18n/modules';

/**
 * Live refresh of the mailbox workspace with the real query hooks: only the service is replaced,
 * so the polling interval, the focus refetch, and the refresh button run as in the browser.
 */

const service = vi.hoisted(() => ({
  domains: vi.fn(),
  list: vi.fn(),
  messages: vi.fn(),
  getMessageCounts: vi.fn(),
  listFolders: vi.fn(),
  listLabels: vi.fn(),
  message: vi.fn(),
  getThread: vi.fn(),
}));

vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }));
vi.mock('next/navigation', () => ({
  useRouter: () => ({ replace: vi.fn() }),
  usePathname: () => '/console/mailboxes',
  useSearchParams: () => new URLSearchParams('org=1'),
}));
vi.mock('@/features/organization/hooks/use-organizations', () => ({
  useOrganizations: () => ({
    data: page([
      { id: 1, name: 'Acme', slug: 'acme', role: 'owner', created_at: now, updated_at: now },
    ]),
    isPending: false,
    error: null,
    refetch: vi.fn(),
  }),
  useOrganizationMembers: () => ({ data: page([]), isPending: false, error: null }),
}));
vi.mock('@/features/mailbox/hooks/use-mail-templates', () => ({
  useMailTemplates: () => ({ data: [] }),
}));
vi.mock('@/features/mailbox/services/mailbox-service', () => ({ mailboxService: service }));

const now = '2026-11-16T10:00:00Z';

describe('mailbox live refresh', () => {
  beforeEach(() => {
    window.localStorage.clear();
    for (const fn of Object.values(service)) fn.mockReset();
    service.domains.mockResolvedValue([
      {
        domain: 'mail.example.com',
        kind: 'shared',
        max_mailboxes: 10,
        provider: 'log',
        inbound_enabled: true,
        daily_send_limit: 200,
        broadcasts_enabled: false,
        custom_domains_enabled: false,
      },
    ]);
    service.list.mockResolvedValue([mailbox()]);
    service.messages.mockResolvedValue(page([summary(10, 'Old news')]));
    service.getMessageCounts.mockResolvedValue({
      placements: { default_inbound_unread: 0, archive_unread: 0 },
      folders: [],
      starred: 0,
    });
    service.listFolders.mockResolvedValue([]);
    service.listLabels.mockResolvedValue([]);
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('shows new mail from a later fetch without a reload, keeping the selection', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    renderWorkspace();
    expect(await screen.findByText('Old news')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('checkbox', { name: 'Select Old news' }));
    const listCalls = service.messages.mock.calls.length;
    const countCalls = service.getMessageCounts.mock.calls.length;
    const mailboxCalls = service.list.mock.calls.length;

    service.messages.mockResolvedValue(
      page([summary(11, 'Fresh mail', false), summary(10, 'Old news')])
    );
    await act(async () => {
      await vi.advanceTimersByTimeAsync(15_000);
    });

    expect(await screen.findByText('Fresh mail')).toBeInTheDocument();
    expect(service.messages.mock.calls.length).toBeGreaterThan(listCalls);
    expect(service.getMessageCounts.mock.calls.length).toBeGreaterThan(countCalls);
    expect(service.list.mock.calls.length).toBeGreaterThan(mailboxCalls);
    // A poll keeps what the user selected.
    expect(screen.getByRole('checkbox', { name: 'Select Old news' })).toBeChecked();
    expect(screen.getByText('1 selected')).toBeInTheDocument();
  });

  it('refetches the list, the counts, and the mailboxes from the refresh button', async () => {
    renderWorkspace();
    expect(await screen.findByText('Old news')).toBeInTheDocument();
    const listCalls = service.messages.mock.calls.length;
    const countCalls = service.getMessageCounts.mock.calls.length;
    const mailboxCalls = service.list.mock.calls.length;

    service.messages.mockResolvedValue(page([summary(12, 'Just arrived', false)]));
    fireEvent.click(screen.getByRole('button', { name: 'Refresh mailbox' }));

    expect(await screen.findByText('Just arrived')).toBeInTheDocument();
    expect(service.messages.mock.calls.length).toBeGreaterThan(listCalls);
    expect(service.getMessageCounts.mock.calls.length).toBeGreaterThan(countCalls);
    await waitFor(() => expect(service.list.mock.calls.length).toBeGreaterThan(mailboxCalls));
  });

  it('keeps the loaded list when a background poll fails', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    renderWorkspace();
    expect(await screen.findByText('Old news')).toBeInTheDocument();
    const listCalls = service.messages.mock.calls.length;

    service.messages.mockRejectedValue(new Error('offline'));
    await act(async () => {
      await vi.advanceTimersByTimeAsync(15_000);
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(5_000);
    });

    // The poll and its retry both failed, and the list is still on screen.
    expect(service.messages.mock.calls.length).toBeGreaterThanOrEqual(listCalls + 2);
    expect(screen.getByText('Old news')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Retry' })).not.toBeInTheDocument();
    expect(await screen.findByText(staleNotice)).toBeInTheDocument();

    // The next good poll clears the notice.
    service.messages.mockResolvedValue(page([summary(10, 'Old news')]));
    await act(async () => {
      await vi.advanceTimersByTimeAsync(15_000);
    });
    await waitFor(() => expect(screen.queryByText(staleNotice)).not.toBeInTheDocument());
  });

  it('keeps the open thread when a background poll fails', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    service.getThread.mockResolvedValue([message(10, 'Old news')]);
    renderWorkspace();
    fireEvent.click(await screen.findByText('Old news'));
    expect(await screen.findByText('Body of Old news')).toBeInTheDocument();
    const threadCalls = service.getThread.mock.calls.length;

    service.getThread.mockRejectedValue(new Error('offline'));
    await act(async () => {
      await vi.advanceTimersByTimeAsync(15_000);
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(5_000);
    });

    expect(service.getThread.mock.calls.length).toBeGreaterThanOrEqual(threadCalls + 2);
    expect(screen.getByText('Body of Old news')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Retry' })).not.toBeInTheDocument();
    expect(await screen.findByText(staleNotice)).toBeInTheDocument();
  });
});

const staleNotice = messages.mailbox.list.refreshFailed;

function message(id: number, subject: string) {
  return {
    ...summary(id, subject),
    text_body: `Body of ${subject}`,
    html_body: '',
    message_id: `${id}@example.com`,
    in_reply_to: '',
    events: [],
    attachments: [],
  };
}

function mailbox() {
  return {
    id: 1,
    address: 'hello@mail.example.com',
    display_name: 'Hello Team',
    type: 'shared',
    owner_user_id: null,
    awaiting_handover: false,
    sealed_before: null,
    webhooks_enabled: false,
    access: {
      view: true,
      send: true,
      manage_members: true,
      administer: true,
      see_history: true,
      organize: true,
    },
    unread_count: 0,
    created_at: now,
  };
}

function summary(id: number, subject: string, read = true) {
  return {
    id,
    mailbox_id: 1,
    direction: 'inbound',
    status: 'received',
    from: { address: 'bob@example.com', name: 'Bob' },
    to: ['hello@mail.example.com'],
    cc: [],
    subject,
    read,
    failure_code: '',
    queued_reason: null,
    last_event: '',
    attachment_count: 0,
    template_id: null,
    created_at: now,
    placement: 'default',
    folder_id: null,
    label_ids: [],
    starred: false,
    thread_id: id,
    trashed_at: null,
    thread: { message_count: 1, message_ids: [id], unread: !read, starred: false, label_ids: [] },
  };
}

function page(items: unknown[]) {
  return {
    items,
    meta: {
      current_page: 1,
      per_page: 20,
      total: items.length,
      last_page: 1,
      from: items.length ? 1 : 0,
      to: items.length,
    },
    links: { first: '', last: '', prev: null, next: null },
  };
}

function renderWorkspace() {
  return render(
    <QueryClientProvider client={createQueryClient()}>
      <NextIntlClientProvider locale="en-US" messages={messages as unknown as AbstractIntlMessages}>
        <MailboxWorkspace />
      </NextIntlClientProvider>
    </QueryClientProvider>
  );
}
