import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { NextIntlClientProvider, type AbstractIntlMessages } from 'next-intl';
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';

import { InboxBulkBar } from '@/features/mailbox/components/inbox/inbox-bulk-bar';
import { InboxList } from '@/features/mailbox/components/inbox-list';
import { InboxWorkspace } from '@/features/mailbox/components/inbox-workspace';
import { mailboxService } from '@/features/mailbox/services/mailbox-service';
import type { InboxCounts, InboxItem, Mailbox } from '@/features/mailbox/types';
import { messages } from '@/i18n/modules';
import zhHansMailbox from '@/i18n/modules/mailbox/zh-Hans';

beforeAll(async () => {
  await Promise.all([import('@/features/mailbox/components/inbox-reading-pane')]);
});

const toast = vi.hoisted(() =>
  Object.assign(vi.fn(), { success: vi.fn(), error: vi.fn(), dismiss: vi.fn() })
);

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
  useOrganizations: () => ({
    data: {
      items: [{ id: 10, name: 'Acme', slug: 'acme', role: 'owner' }],
      meta: { page: 1, per_page: 20, total: 1, total_pages: 1 },
    },
    isPending: false,
    error: null,
    refetch: vi.fn(),
  }),
  useOrganizationMembers: () => ({
    data: { items: [], meta: { page: 1, per_page: 20, total: 0, total_pages: 1 } },
    isPending: false,
    error: null,
    refetch: vi.fn(),
  }),
  useCreateOrganization: () => ({ mutate: vi.fn(), reset: vi.fn(), isPending: false, error: null }),
}));
vi.mock('@/config/features', async importOriginal => {
  const original = await importOriginal<typeof import('@/config/features')>();
  return {
    ...original,
    isWebFeatureEnabled: (feature: Parameters<typeof original.isWebFeatureEnabled>[0]) =>
      feature === 'setting' || feature === 'notification'
        ? false
        : original.isWebFeatureEnabled(feature),
  };
});
vi.mock('@/features/mailbox/hooks/use-external-mail', () => ({
  useExternalMailConnectors: () => ({ data: [], isPending: false, isError: false }),
  useExternalMailConnections: () => ({ data: [], isPending: false, isError: false }),
}));
vi.mock('@/features/mailbox/hooks/use-mail-drafts', () => ({
  useMailDrafts: () => ({ data: [], isPending: false }),
  useOpenMailDraft: () => ({ mutate: vi.fn(), isPending: false }),
}));
vi.mock('@/features/mailbox/hooks/use-thread-assignment', () => ({
  useThreadAssignment: () => ({ data: undefined, isSuccess: false }),
  useSaveThreadAssignment: () => ({ mutate: vi.fn(), isPending: false, isError: false }),
}));
vi.mock('@/features/mailbox/hooks/use-thread-notes', () => ({
  useThreadNotes: () => ({ data: undefined, isSuccess: false }),
  useAddThreadNote: () => ({ mutate: vi.fn(), isPending: false, isError: false }),
  useDeleteThreadNote: () => ({ mutate: vi.fn(), isPending: false, isError: false }),
}));

function makeMailbox(id: number, local: string, send = true): Mailbox {
  return {
    id,
    address: `${local}@example.com`,
    display_name: local,
    type: 'shared',
    origin: 'zmail',
    owner_user_id: null,
    awaiting_handover: false,
    sealed_before: null,
    webhooks_enabled: false,
    role_id: null,
    unread_count: 1,
    access: {
      view: true,
      send,
      organize: send,
      manage_members: false,
      administer: false,
      see_history: false,
    },
    created_at: '2026-11-20T09:00:00Z',
  };
}

function makeRow(overrides: Partial<InboxItem> & { id: number; subject: string }): InboxItem {
  const { id, mailbox_id: mailboxId = 1, read = false, starred = false } = overrides;
  return {
    mailbox_id: mailboxId,
    direction: 'inbound',
    status: 'received',
    from: { address: 'sender@example.com', name: 'Alice' },
    to: [`box${mailboxId}@example.com`],
    cc: [],
    read,
    failure_code: '',
    queued_reason: null,
    last_event: '',
    attachment_count: 0,
    template_id: null,
    created_at: '2026-11-20T09:00:00Z',
    placement: 'default',
    folder_id: null,
    label_ids: [],
    starred,
    thread_id: id,
    trashed_at: null,
    mailbox_address: `box${mailboxId}@example.com`,
    role_id: null,
    snippet: 'Preview snippet',
    ...overrides,
  };
}

const defaultCounts: InboxCounts = {
  views: { all_unread: 2, today: 3, today_unread: 2, pending: 0, sent_waiting: 0 },
  roles: [],
  ungrouped_unread: 2,
  mailboxes: [
    { mailbox_id: 1, unread: 1 },
    { mailbox_id: 2, unread: 1 },
    { mailbox_id: 3, unread: 0 },
  ],
  today_starts_at: '2026-11-20T00:00:00Z',
  today_ends_at: '2026-11-21T00:00:00Z',
  triage: null,
};

function renderWorkspace() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <NextIntlClientProvider
        locale="en-US"
        timeZone="UTC"
        messages={messages as unknown as AbstractIntlMessages}
      >
        <InboxWorkspace />
      </NextIntlClientProvider>
    </QueryClientProvider>
  );
}

describe('unified inbox Gmail hover quick actions and bulk action bar', () => {
  let serverRows: InboxItem[];
  let updateMessageSpy: ReturnType<typeof vi.spyOn>;
  let updateMessagesSpy: ReturnType<typeof vi.spyOn>;

  beforeEach(() => {
    toast.mockReset();
    toast.success.mockReset();
    toast.error.mockReset();
    navigation.replace.mockClear();
    window.history.replaceState(null, '', '/console/mailboxes');

    serverRows = [
      makeRow({
        id: 101,
        mailbox_id: 1,
        subject: 'Launch plan',
        read: false,
        starred: false,
        thread: {
          message_count: 2,
          unread: true,
          starred: false,
          message_ids: [100, 101],
          label_ids: [],
        },
      }),
      makeRow({
        id: 202,
        mailbox_id: 2,
        subject: 'Quarterly invoice',
        read: true,
        starred: true,
      }),
      makeRow({
        id: 303,
        mailbox_id: 3,
        subject: 'Read-only digest',
        read: false,
        starred: false,
      }),
    ];

    vi.spyOn(mailboxService, 'list').mockResolvedValue([
      makeMailbox(1, 'hello', true),
      makeMailbox(2, 'sales', true),
      makeMailbox(3, 'readonly', false),
    ]);
    vi.spyOn(mailboxService, 'domains').mockResolvedValue([]);
    vi.spyOn(mailboxService, 'listRoles').mockResolvedValue([]);
    vi.spyOn(mailboxService, 'listInbox').mockImplementation(async () => ({
      items: serverRows.filter(row => row.placement !== 'archive' && row.placement !== 'trash'),
      meta: { per_page: 20, has_more: false, next_cursor: null },
    }));
    vi.spyOn(mailboxService, 'inboxCounts').mockResolvedValue(defaultCounts);

    updateMessageSpy = vi
      .spyOn(mailboxService, 'updateMessage')
      .mockImplementation(async (_orgId, _mailboxId, messageId, input) => {
        serverRows = serverRows.map(row =>
          row.id === messageId
            ? {
                ...row,
                ...(input.starred !== undefined ? { starred: input.starred } : {}),
                ...(input.read !== undefined ? { read: input.read } : {}),
                ...(input.placement !== undefined ? { placement: input.placement } : {}),
              }
            : row
        );
        return {} as never;
      });

    updateMessagesSpy = vi
      .spyOn(mailboxService, 'updateMessages')
      .mockImplementation(async (_orgId, mailboxId, input) => {
        const ids = new Set(input.message_ids);
        serverRows = serverRows.map(row => {
          if (row.mailbox_id !== mailboxId) return row;
          const rowIds = row.thread?.message_ids ?? [row.id];
          if (!rowIds.some(id => ids.has(id))) return row;
          return {
            ...row,
            ...(input.starred !== undefined ? { starred: input.starred } : {}),
            ...(input.read !== undefined ? { read: input.read } : {}),
            ...(input.placement !== undefined ? { placement: input.placement } : {}),
            ...(row.thread
              ? {
                  thread: {
                    ...row.thread,
                    ...(input.starred !== undefined ? { starred: input.starred } : {}),
                    ...(input.read !== undefined ? { unread: !input.read } : {}),
                  },
                }
              : {}),
          };
        });
        return { updated: input.message_ids.length };
      });
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('stars and unstars rows via hover quick actions without opening the email', async () => {
    renderWorkspace();

    const starBtn = await screen.findByRole('button', { name: 'Star Launch plan' });
    expect(starBtn).toHaveAttribute('aria-pressed', 'false');

    fireEvent.click(starBtn);

    await waitFor(() => {
      expect(updateMessagesSpy).toHaveBeenCalledWith(10, 1, {
        message_ids: [100, 101],
        starred: true,
      });
    });
    expect(window.location.search).not.toContain('open=');

    const unstarBtn = await screen.findByRole('button', {
      name: 'Remove star from Launch plan',
    });
    expect(unstarBtn).toHaveAttribute('aria-pressed', 'true');

    // Unstarring a single-message row calls updateMessage with starred: false.
    const unstarSingle = screen.getByRole('button', {
      name: 'Remove star from Quarterly invoice',
    });
    fireEvent.click(unstarSingle);
    await waitFor(() => {
      expect(updateMessageSpy).toHaveBeenCalledWith(10, 2, 202, { starred: false });
    });
    expect(window.location.search).not.toContain('open=');
  });

  it('toggles read/unread via hover quick actions without opening the email', async () => {
    renderWorkspace();

    const markReadBtn = await screen.findByRole('button', { name: 'Mark Launch plan read' });
    fireEvent.click(markReadBtn);

    await waitFor(() => {
      expect(updateMessagesSpy).toHaveBeenCalledWith(10, 1, {
        message_ids: [100, 101],
        read: true,
      });
    });
    expect(window.location.search).not.toContain('open=');
    expect(
      await screen.findByRole('button', { name: 'Mark Launch plan unread' })
    ).toBeInTheDocument();

    // Mark an already-read row as unread.
    fireEvent.click(screen.getByRole('button', { name: 'Mark Quarterly invoice unread' }));
    await waitFor(() => {
      expect(updateMessagesSpy).toHaveBeenCalledWith(10, 2, {
        message_ids: [202],
        read: false,
      });
    });
    expect(
      await screen.findByRole('button', { name: 'Mark Quarterly invoice read' })
    ).toBeInTheDocument();
  });

  it('archives a row with an undo toast and restores it when Undo is clicked', async () => {
    renderWorkspace();

    const archiveBtn = await screen.findByRole('button', { name: 'Archive Launch plan' });
    fireEvent.click(archiveBtn);

    await waitFor(() => {
      expect(updateMessagesSpy).toHaveBeenCalledWith(10, 1, {
        message_ids: [100, 101],
        placement: 'archive',
      });
    });
    expect(window.location.search).not.toContain('open=');
    expect(screen.queryByRole('option', { name: /Launch plan/u })).not.toBeInTheDocument();
    expect(toast.success).toHaveBeenCalledWith(
      'Archived',
      expect.objectContaining({
        duration: 5000,
        action: expect.objectContaining({ label: 'Undo' }) as unknown,
      })
    );

    const [, options] = toast.success.mock.calls[0] as [
      string,
      { action: { onClick: () => void } },
    ];
    await act(async () => options.action.onClick());

    await waitFor(() => {
      expect(updateMessagesSpy).toHaveBeenLastCalledWith(10, 1, {
        message_ids: [100, 101],
        placement: 'default',
      });
    });
  });

  it('moves a row to trash via hover Delete quick action with an undo toast', async () => {
    renderWorkspace();

    const deleteBtn = await screen.findByRole('button', { name: 'Delete Quarterly invoice' });
    fireEvent.click(deleteBtn);

    await waitFor(() => {
      expect(updateMessagesSpy).toHaveBeenCalledWith(10, 2, {
        message_ids: [202],
        placement: 'trash',
      });
    });
    expect(window.location.search).not.toContain('open=');
    expect(screen.queryByRole('option', { name: /Quarterly invoice/u })).not.toBeInTheDocument();
    expect(toast.success).toHaveBeenCalledWith(
      'Moved to the trash',
      expect.objectContaining({
        duration: 5000,
        action: expect.objectContaining({ label: 'Undo' }) as unknown,
      })
    );
  });

  it('hides Archive and Delete on read-only rows, and hides Archive on Sent view', async () => {
    const { unmount } = renderWorkspace();
    await screen.findByRole('option', { name: /Read-only digest/u });

    expect(screen.getByRole('button', { name: 'Star Read-only digest' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Mark Read-only digest read' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Archive Read-only digest' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Delete Read-only digest' })).toBeNull();
    unmount();

    window.history.replaceState(null, '', '/console/mailboxes?view=sent');
    renderWorkspace();
    await screen.findByRole('option', { name: /Launch plan/u });

    expect(screen.queryByRole('button', { name: 'Archive Launch plan' })).toBeNull();
    expect(screen.getByRole('button', { name: 'Delete Launch plan' })).toBeInTheDocument();
  });

  it('supports multi-select checkboxes and cross-mailbox bulk mark read / unread', async () => {
    renderWorkspace();

    const cb1 = await screen.findByRole('checkbox', { name: 'Select Launch plan' });
    const cb3 = screen.getByRole('checkbox', { name: 'Select Read-only digest' });

    fireEvent.click(cb1);
    expect(window.location.search).not.toContain('open=');

    fireEvent.click(cb3);
    const bar = screen.getByRole('toolbar', { name: 'Bulk actions for 2 selected' });
    expect(within(bar).getByText('2 selected')).toBeInTheDocument();

    fireEvent.click(within(bar).getByRole('button', { name: 'Mark read' }));

    await waitFor(() => {
      expect(updateMessagesSpy).toHaveBeenCalledWith(10, 1, {
        message_ids: [100, 101],
        read: true,
      });
      expect(updateMessagesSpy).toHaveBeenCalledWith(10, 3, {
        message_ids: [303],
        read: true,
      });
    });
    expect(toast.success).toHaveBeenCalledWith('Marked 2 as read');
    expect(screen.queryByRole('toolbar', { name: /Bulk actions/u })).not.toBeInTheDocument();
  });

  it('groups bulk Archive across sendable mailboxes, skips read-only rows, and supports Undo', async () => {
    renderWorkspace();

    fireEvent.click(await screen.findByRole('checkbox', { name: 'Select Launch plan' }));
    const bar = screen.getByRole('toolbar', { name: 'Bulk actions for 1 selected' });
    // Select-all selects all 3 loaded rows (2 sendable + 1 read-only).
    fireEvent.click(within(bar).getByRole('checkbox', { name: 'Select all loaded messages' }));
    expect(screen.getByRole('toolbar', { name: 'Bulk actions for 3 selected' })).toBeInTheDocument();

    fireEvent.click(
      within(screen.getByRole('toolbar', { name: 'Bulk actions for 3 selected' })).getByRole(
        'button',
        { name: 'Archive' }
      )
    );

    await waitFor(() => {
      expect(updateMessagesSpy).toHaveBeenCalledWith(10, 1, {
        message_ids: [100, 101],
        placement: 'archive',
      });
      expect(updateMessagesSpy).toHaveBeenCalledWith(10, 2, {
        message_ids: [202],
        placement: 'archive',
      });
    });
    // Mailbox 3 is read-only and must not receive an archive mutation.
    expect(updateMessagesSpy).not.toHaveBeenCalledWith(10, 3, expect.anything());
    expect(screen.queryByRole('toolbar', { name: /Bulk actions/u })).not.toBeInTheDocument();
    expect(toast.success).toHaveBeenCalledWith(
      'Archived',
      expect.objectContaining({
        duration: 5000,
        action: expect.objectContaining({ label: 'Undo' }) as unknown,
      })
    );

    const [, options] = toast.success.mock.calls[0] as [
      string,
      { action: { onClick: () => void } },
    ];
    await act(async () => options.action.onClick());

    await waitFor(() => {
      expect(updateMessagesSpy).toHaveBeenCalledWith(10, 1, {
        message_ids: [100, 101],
        placement: 'default',
      });
      expect(updateMessagesSpy).toHaveBeenCalledWith(10, 2, {
        message_ids: [202],
        placement: 'default',
      });
    });
  });

  it('clears selection via the bulk bar Cancel button and stays backwards compatible standalone', async () => {
    const { unmount } = renderWorkspace();

    fireEvent.click(await screen.findByRole('checkbox', { name: 'Select Launch plan' }));
    const bar = screen.getByRole('toolbar', { name: 'Bulk actions for 1 selected' });
    fireEvent.click(within(bar).getByRole('button', { name: 'Clear selection' }));
    expect(screen.queryByRole('toolbar', { name: /Bulk actions/u })).not.toBeInTheDocument();
    unmount();

    // Standalone InboxList without optional action/select callbacks renders cleanly.
    render(
      <NextIntlClientProvider
        locale="zh-Hans"
        timeZone="UTC"
        messages={{ ...messages, mailbox: zhHansMailbox } as unknown as AbstractIntlMessages}
      >
        <InboxBulkBar
          selectedItems={[
            makeRow({ id: 1, mailbox_id: 1, subject: 'One' }),
            makeRow({ id: 2, mailbox_id: 1, subject: 'Two' }),
          ]}
          totalCount={2}
          sendable={new Set([1])}
          view="all"
          onToggleAll={() => undefined}
          onClear={() => undefined}
          onAction={() => undefined}
        />
        <InboxList
          label="收件"
          pages={[
            {
              items: [makeRow({ id: 1, mailbox_id: 1, subject: 'Standalone' })],
              meta: { per_page: 20, has_more: false, next_cursor: null },
            },
          ]}
          sendable={new Set([1])}
          timeZone="UTC"
          selectedId={null}
          onOpen={() => undefined}
          onFilterMailbox={() => undefined}
          onLoadMore={() => undefined}
          hasMore={false}
        />
      </NextIntlClientProvider>
    );
    expect(screen.getByText('已选 2 封')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /Standalone/u })).toBeNull();
  });
});
