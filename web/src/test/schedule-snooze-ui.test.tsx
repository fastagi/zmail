import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { NextIntlClientProvider, type AbstractIntlMessages } from 'next-intl';
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';

import { ComposeDialog, emptyDraft } from '@/features/mailbox/components/compose-dialog';
import { InboxWorkspace } from '@/features/mailbox/components/inbox-workspace';
import { MailOrganizationBoundary } from '@/features/mailbox/components/mail-organization';
import {
  toDateTimeLocalValue,
  tomorrowAtPreset,
} from '@/features/mailbox/components/schedule-snooze-presets';
import { setUndoSendDelayMs } from '@/features/mailbox/components/undo-send';
import { useMailOrganization } from '@/features/mailbox/hooks/use-mail-organization';
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

const draftMocks = vi.hoisted(() => ({
  saveDraft: { isPending: false, mutateAsync: vi.fn() },
  deleteDraft: { isPending: false, mutateAsync: vi.fn() },
}));

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
  useSaveMailDraft: () => draftMocks.saveDraft,
  useDeleteMailDraft: () => draftMocks.deleteDraft,
}));
vi.mock('@/features/mailbox/hooks/use-mail-signature', () => ({
  useMailSignature: () => ({ data: undefined }),
}));
vi.mock('@/features/mailbox/hooks/use-mail-templates', () => ({
  useMailTemplates: () => ({ data: [] }),
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

function WithOrgBoundary({ children }: { children: React.ReactNode }) {
  const scope = useMailOrganization();
  return <MailOrganizationBoundary scope={scope}>{() => children}</MailOrganizationBoundary>;
}

function renderWithLocale(ui: React.ReactNode, locale: 'en-US' | 'zh-Hans' = 'en-US') {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const localeMessages =
    locale === 'zh-Hans' ? { ...messages, mailbox: zhHansMailbox } : messages;
  return render(
    <QueryClientProvider client={client}>
      <NextIntlClientProvider
        locale={locale}
        timeZone="UTC"
        messages={localeMessages as unknown as AbstractIntlMessages}
      >
        {ui}
      </NextIntlClientProvider>
    </QueryClientProvider>
  );
}

describe('Schedule Send split-button in ComposeDialog', () => {
  beforeEach(() => {
    window.localStorage.clear();
    setUndoSendDelayMs(5000);
    toast.mockReset();
    toast.success.mockReset();
    toast.error.mockReset();
    draftMocks.saveDraft.mutateAsync.mockReset().mockResolvedValue({ id: 77 });
    draftMocks.deleteDraft.mutateAsync.mockReset().mockResolvedValue(undefined);
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('schedules send via preset with scheduled_at, bypasses undo-send timer, deletes saved draft, and shows toast', async () => {
    const expectedIso = tomorrowAtPreset(9).toISOString();
    const sendSpy = vi.spyOn(mailboxService, 'send').mockResolvedValue({
      id: 501,
      mailbox_id: 1,
      direction: 'outbound',
      status: 'queued',
      from: { address: 'hello@example.com', name: 'Hello' },
      to: ['bob@example.com'],
      cc: [],
      subject: 'Morning update',
      read: true,
      failure_code: '',
      queued_reason: 'scheduled',
      scheduled_at: expectedIso,
      last_event: '',
      attachment_count: 0,
      template_id: null,
      placement: 'default',
      folder_id: null,
      label_ids: [],
      starred: false,
      thread_id: 501,
      trashed_at: null,
      created_at: '2026-11-26T10:00:00Z',
      text_body: 'See you tomorrow morning.',
      html_body: '',
      message_id: 'msg-501@example.com',
      in_reply_to: '',
      events: [],
      attachments: [],
    });

    const onClose = vi.fn();
    const onReopen = vi.fn();
    const onStored = vi.fn();
    renderWithLocale(
      <WithOrgBoundary>
        <ComposeDialog
          mailbox={{ id: 1, address: 'hello@example.com' }}
          draft={{ ...emptyDraft, savedId: 42 }}
          onClose={onClose}
          onReopen={onReopen}
          onStored={onStored}
        />
      </WithOrgBoundary>
    );

    const dialog = screen.getByRole('dialog', { name: 'New message' });
    fireEvent.change(within(dialog).getByLabelText('To'), {
      target: { value: 'bob@example.com' },
    });
    fireEvent.change(within(dialog).getByLabelText('Subject'), {
      target: { value: 'Morning update' },
    });
    fireEvent.change(within(dialog).getByLabelText('Message'), {
      target: { value: 'See you tomorrow morning.' },
    });

    fireEvent.click(within(dialog).getByRole('button', { name: 'More send options' }));
    expect(screen.getByText('Tomorrow afternoon (13:00)')).toBeInTheDocument();
    expect(screen.getByText('Monday morning (09:00)')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: /Tomorrow morning \(09:00\)/u }));

    await waitFor(() => {
      expect(sendSpy).toHaveBeenCalledWith(10, 1, {
        to: ['bob@example.com'],
        subject: 'Morning update',
        text: 'See you tomorrow morning.',
        scheduled_at: expect.any(String),
      });
    });

    const sentPayload = sendSpy.mock.calls[0][2];
    expect(new Date(sentPayload.scheduled_at!).getTime()).toBe(new Date(expectedIso).getTime());
    expect(draftMocks.deleteDraft.mutateAsync).toHaveBeenCalledWith(42);
    expect(onStored).toHaveBeenCalledTimes(1);
    expect(onClose).toHaveBeenCalledTimes(1);
    expect(toast.success).toHaveBeenCalledWith(expect.stringMatching(/^Scheduled for /u));
    // Must bypass the 5s undo-send toast (`toast(...)` is used by undo-send).
    expect(toast).not.toHaveBeenCalled();
  });

  it('validates custom schedule time and supports Chinese copy', async () => {
    const sendSpy = vi.spyOn(mailboxService, 'send').mockResolvedValue({
      id: 502,
      queued_reason: 'scheduled',
      scheduled_at: '2026-12-01T10:30:00.000Z',
    } as never);

    const onClose = vi.fn();
    renderWithLocale(
      <WithOrgBoundary>
        <ComposeDialog
          mailbox={{ id: 1, address: 'hello@example.com' }}
          draft={emptyDraft}
          onClose={onClose}
        />
      </WithOrgBoundary>,
      'zh-Hans'
    );

    const dialog = screen.getByRole('dialog', { name: '写邮件' });
    fireEvent.change(within(dialog).getByLabelText('收件人'), {
      target: { value: 'bob@example.com' },
    });
    fireEvent.change(within(dialog).getByLabelText('主题'), {
      target: { value: '定时测试' },
    });
    fireEvent.change(within(dialog).getByLabelText('正文'), {
      target: { value: '正文内容' },
    });

    fireEvent.click(within(dialog).getByRole('button', { name: '更多发送选项' }));
    fireEvent.click(screen.getByRole('button', { name: '自定义时间' }));

    const input = screen.getByLabelText('选择定时发送时间');
    fireEvent.change(input, { target: { value: '2020-01-01T09:00' } });
    fireEvent.click(screen.getByRole('button', { name: '定时发送' }));
    expect(screen.getByRole('alert')).toHaveTextContent('请选择 1 分钟后至 365 天内的时间。');
    expect(sendSpy).not.toHaveBeenCalled();

    const futureDate = tomorrowAtPreset(15);
    fireEvent.change(input, { target: { value: toDateTimeLocalValue(futureDate) } });
    fireEvent.click(screen.getByRole('button', { name: '定时发送' }));

    await waitFor(() => {
      expect(sendSpy).toHaveBeenCalledWith(10, 1, {
        to: ['bob@example.com'],
        subject: '定时测试',
        text: '正文内容',
        scheduled_at: futureDate.toISOString(),
      });
    });
    expect(toast.success).toHaveBeenCalledWith(expect.stringMatching(/^已定时于 .+ 发送$/u));
    expect(onClose).toHaveBeenCalledTimes(1);
  });
});

describe('Snooze quick-action popover and Sidebar Snoozed view', () => {
  let serverRows: InboxItem[];
  let counts: InboxCounts;
  let listInboxSpy: ReturnType<typeof vi.spyOn>;
  let updateMessagesSpy: ReturnType<typeof vi.spyOn>;

  beforeEach(() => {
    toast.mockReset();
    toast.success.mockReset();
    toast.error.mockReset();
    navigation.replace.mockClear();
    window.history.replaceState(null, '', '/console/mailboxes');

    const futureSnooze = tomorrowAtPreset(9).toISOString();
    serverRows = [
      makeRow({
        id: 101,
        mailbox_id: 1,
        subject: 'Launch plan',
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
        subject: 'Snoozed follow-up',
        read: true,
        snoozed_until: futureSnooze,
      }),
      makeRow({
        id: 303,
        mailbox_id: 3,
        subject: 'Read-only digest',
      }),
    ];

    counts = {
      views: { all_unread: 2, today: 2, today_unread: 2, pending: 0, sent_waiting: 0, snoozed: 1 },
      roles: [],
      ungrouped_unread: 2,
      mailboxes: [
        { mailbox_id: 1, unread: 1 },
        { mailbox_id: 2, unread: 0 },
        { mailbox_id: 3, unread: 1 },
      ],
      today_starts_at: '2026-11-26T00:00:00Z',
      today_ends_at: '2026-11-27T00:00:00Z',
      triage: null,
    };

    vi.spyOn(mailboxService, 'list').mockResolvedValue([
      makeMailbox(1, 'hello', true),
      makeMailbox(2, 'sales', true),
      makeMailbox(3, 'readonly', false),
    ]);
    vi.spyOn(mailboxService, 'domains').mockResolvedValue([]);
    vi.spyOn(mailboxService, 'listRoles').mockResolvedValue([]);
    listInboxSpy = vi.spyOn(mailboxService, 'listInbox').mockImplementation(async (_orgId, filters) => {
      const items =
        filters.view === 'snoozed'
          ? serverRows.filter(row => Boolean(row.snoozed_until))
          : serverRows.filter(row => !row.snoozed_until);
      return {
        items,
        meta: { per_page: 20, has_more: false, next_cursor: null },
      };
    });
    vi.spyOn(mailboxService, 'inboxCounts').mockImplementation(async () => counts);

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
            ...(input.clear_snooze ? { snoozed_until: null } : {}),
            ...(input.snoozed_until ? { snoozed_until: input.snoozed_until } : {}),
          };
        });
        counts = {
          ...counts,
          views: {
            ...counts.views,
            snoozed: serverRows.filter(row => Boolean(row.snoozed_until)).length,
          },
        };
        return { updated: input.message_ids.length };
      });
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('snoozes a row via Clock popover with snoozed_until, removes optimistically, and supports 5s Undo (clear_snooze)', async () => {
    renderWithLocale(<InboxWorkspace />);

    const snoozeBtn = await screen.findByRole('button', { name: 'Snooze Launch plan' });
    // Read-only row does not get a Snooze button.
    expect(screen.queryByRole('button', { name: 'Snooze Read-only digest' })).toBeNull();

    fireEvent.click(snoozeBtn);
    expect(window.location.search).not.toContain('open=');
    expect(screen.getByText('Later today (+3h)')).toBeInTheDocument();
    expect(screen.getByText('Monday morning (09:00)')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: /Tomorrow morning \(09:00\)/u }));

    await waitFor(() => {
      expect(updateMessagesSpy).toHaveBeenCalledWith(10, 1, {
        message_ids: [100, 101],
        snoozed_until: expect.any(String),
      });
    });
    expect(screen.queryByRole('option', { name: /Launch plan/u })).not.toBeInTheDocument();
    expect(toast.success).toHaveBeenCalledWith(
      expect.stringMatching(/^Snoozed until /u),
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
        clear_snooze: true,
      });
    });
  });

  it('renders Sidebar Snoozed view with live count badge and lets users unsnooze from Snoozed view', async () => {
    renderWithLocale(<InboxWorkspace />);

    const snoozedNav = await screen.findByRole('button', { name: 'Snoozed: 1' });
    expect(snoozedNav).toHaveTextContent('Snoozed');
    expect(snoozedNav).toHaveTextContent('1');

    fireEvent.click(snoozedNav);

    await waitFor(() => {
      expect(window.location.search).toContain('view=snoozed');
      expect(listInboxSpy).toHaveBeenCalledWith(
        10,
        expect.objectContaining({ view: 'snoozed' }),
        undefined,
        expect.any(Number)
      );
    });

    expect(await screen.findByRole('option', { name: /Snoozed follow-up/u })).toBeInTheDocument();
    expect(screen.getByText(/Snoozed until /u)).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Snooze Snoozed follow-up' }));
    fireEvent.click(screen.getByRole('button', { name: 'Unsnooze' }));

    await waitFor(() => {
      expect(updateMessagesSpy).toHaveBeenCalledWith(10, 2, {
        message_ids: [202],
        clear_snooze: true,
      });
    });
    expect(screen.queryByRole('option', { name: /Snoozed follow-up/u })).not.toBeInTheDocument();
    expect(toast.success).toHaveBeenCalledWith(
      'Snooze cleared',
      expect.objectContaining({
        duration: 5000,
        action: expect.objectContaining({ label: 'Undo' }) as unknown,
      })
    );
  });
});
