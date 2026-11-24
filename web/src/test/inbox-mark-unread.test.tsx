import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { NextIntlClientProvider, type AbstractIntlMessages } from 'next-intl';
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';

import { InboxWorkspace } from '@/features/mailbox/components/inbox-workspace';
import { mailboxService } from '@/features/mailbox/services/mailbox-service';
import type { InboxCounts, InboxItem, MailMessage, Mailbox } from '@/features/mailbox/types';
import { messages } from '@/i18n/modules';

beforeAll(async () => {
  await Promise.all([import('@/features/mailbox/components/inbox-reading-pane')]);
});

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

vi.mock('sonner', () => ({
  toast: Object.assign(vi.fn(), { success: vi.fn(), error: vi.fn(), dismiss: vi.fn() }),
}));
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

const mailbox: Mailbox = {
  id: 118,
  address: 'alice@external.example',
  display_name: 'Alice External',
  type: 'personal',
  origin: 'connected',
  owner_user_id: 1,
  awaiting_handover: false,
  sealed_before: null,
  webhooks_enabled: false,
  role_id: null,
  unread_count: 0,
  access: {
    view: true,
    send: true,
    organize: true,
    manage_members: false,
    administer: false,
    see_history: false,
  },
  created_at: '2026-11-20T09:00:00Z',
};

function makeMessage(read: boolean): MailMessage {
  return {
    id: 501,
    mailbox_id: mailbox.id,
    direction: 'inbound',
    status: 'received',
    from: { address: 'sender@example.com', name: 'Sender' },
    to: [mailbox.address],
    cc: [],
    bcc: [],
    subject: 'Quarterly invoice',
    text_body: 'Please review the attached invoice.',
    html_body: '',
    message_id: '<msg-501@example.com>',
    in_reply_to: '',
    events: [],
    read,
    failure_code: '',
    queued_reason: null,
    last_event: '',
    attachment_count: 0,
    attachments: [],
    template_id: null,
    created_at: '2026-11-20T09:00:00Z',
    placement: 'default',
    folder_id: null,
    label_ids: [],
    starred: false,
    thread_id: 501,
    trashed_at: null,
  };
}

function makeInboxRow(read: boolean): InboxItem {
  return {
    id: 501,
    mailbox_id: mailbox.id,
    direction: 'inbound',
    status: 'received',
    from: { address: 'sender@example.com', name: 'Sender' },
    to: [mailbox.address],
    cc: [],
    subject: 'Quarterly invoice',
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
    starred: false,
    thread_id: 501,
    trashed_at: null,
    mailbox_address: mailbox.address,
    role_id: null,
    snippet: 'Please review the attached invoice.',
    thread: {
      message_count: 1,
      unread: !read,
      starred: false,
      message_ids: [501],
      label_ids: [],
    },
  };
}

function makeCounts(unread: number): InboxCounts {
  return {
    views: {
      all_unread: unread,
      today: 1,
      today_unread: unread,
      pending: 0,
      sent_waiting: 0,
    },
    roles: [],
    ungrouped_unread: unread,
    mailboxes: [{ mailbox_id: mailbox.id, unread }],
    today_starts_at: '2026-11-20T00:00:00Z',
    today_ends_at: '2026-11-21T00:00:00Z',
    triage: null,
  };
}

function renderWorkspace() {
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
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

describe('inbox mark unread regression', () => {
  let serverRead = true;

  beforeEach(() => {
    serverRead = true;
    navigation.replace.mockClear();
    window.history.replaceState(null, '', '/mail?org=10&box=118');

    vi.spyOn(mailboxService, 'list').mockImplementation(async () => [
      { ...mailbox, unread_count: serverRead ? 0 : 1 },
    ]);
    vi.spyOn(mailboxService, 'domains').mockResolvedValue([]);
    vi.spyOn(mailboxService, 'listRoles').mockResolvedValue([]);
    vi.spyOn(mailboxService, 'listInbox').mockImplementation(async () => ({
      items: [makeInboxRow(serverRead)],
      meta: { per_page: 20, has_more: false, next_cursor: null },
    }));
    vi.spyOn(mailboxService, 'inboxCounts').mockImplementation(async () =>
      makeCounts(serverRead ? 0 : 1)
    );
    vi.spyOn(mailboxService, 'getThread').mockImplementation(async () => [makeMessage(serverRead)]);
    vi.spyOn(mailboxService, 'updateMessage').mockImplementation(
      async (_orgId, _mailboxId, _messageId, input) => {
        if (typeof input.read === 'boolean') serverRead = input.read;
        return makeMessage(serverRead);
      }
    );
    vi.spyOn(mailboxService, 'setRead').mockImplementation(
      async (_orgId, _mailboxId, _messageId, read) => {
        serverRead = read;
        return makeMessage(serverRead);
      }
    );
    vi.spyOn(mailboxService, 'updateMessages').mockImplementation(
      async (_orgId, _mailboxId, input) => {
        if (typeof input.read === 'boolean') serverRead = input.read;
        return { updated: input.message_ids.length };
      }
    );
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('keeps a read thread unread and updates unread counts after clicking Mark unread', async () => {
    renderWorkspace();

    const row = await screen.findByRole('option', { name: /Quarterly invoice/u });
    expect(within(row).queryByText('Unread')).toBeNull();
    const unreadTab = screen.getByRole('tab', { name: /Unread/u });
    expect(unreadTab).not.toHaveTextContent('1');

    // Open the already-read thread.
    fireEvent.click(row);
    const markUnreadButton = await screen.findByRole('button', { name: 'Mark unread' });
    expect(mailboxService.setRead).not.toHaveBeenCalled();

    // Mark it unread and return to the list.
    fireEvent.click(markUnreadButton);

    await waitFor(() => {
      expect(mailboxService.updateMessage).toHaveBeenCalledWith(10, 118, 501, { read: false });
      expect(window.location.search).not.toContain('open=');
    });

    // Auto-mark-read must NOT fire and overwrite the unread state.
    expect(mailboxService.setRead).not.toHaveBeenCalled();
    expect(mailboxService.updateMessages).not.toHaveBeenCalled();
    expect(serverRead).toBe(false);

    // The list row shows the unread indicator and the Unread tab increments to 1.
    await waitFor(() => {
      const updatedRow = screen.getByRole('option', { name: /Quarterly invoice/u });
      expect(within(updatedRow).getByText('Unread')).toBeInTheDocument();
      expect(screen.getByRole('tab', { name: /Unread/u })).toHaveTextContent('1');
    });

    // Re-opening the now-unread row still auto-marks it read as expected.
    fireEvent.click(screen.getByRole('option', { name: /Quarterly invoice/u }));
    await waitFor(() => {
      expect(mailboxService.setRead).toHaveBeenCalledWith(10, 118, 501, true);
    });
  });
});
