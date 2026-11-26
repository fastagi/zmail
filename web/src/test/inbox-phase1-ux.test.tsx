import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { NextIntlClientProvider, type AbstractIntlMessages } from 'next-intl';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { InboxListHeader } from '@/features/mailbox/components/inbox-list';
import { InboxReadingPane } from '@/features/mailbox/components/inbox-reading-pane';
import { InlineQuickReply } from '@/features/mailbox/components/inline-quick-reply';
import { MailOrganizationBoundary } from '@/features/mailbox/components/mail-organization';
import { mailboxKeys } from '@/features/mailbox/hooks/use-mailbox';
import { mailboxService } from '@/features/mailbox/services/mailbox-service';
import type { InboxItem, MailMessage } from '@/features/mailbox/types';
import { parseSearchOperators } from '@/features/mailbox/utils/search-query-parser';
import { ApiErrorCode } from '@/http/codes';
import { ApiError } from '@/http/request';
import { messages } from '@/i18n/modules';
import zhHansMailbox from '@/i18n/modules/mailbox/zh-Hans';

const toast = vi.hoisted(() =>
  Object.assign(vi.fn(), { success: vi.fn(), error: vi.fn(), dismiss: vi.fn() })
);

vi.mock('sonner', () => ({ toast }));
vi.mock('@/features/mailbox/hooks/use-thread-assignment', () => ({
  useThreadAssignment: () => ({ data: undefined, isSuccess: false }),
  useSaveThreadAssignment: () => ({ mutate: vi.fn(), isPending: false, isError: false }),
}));
vi.mock('@/features/mailbox/hooks/use-thread-notes', () => ({
  useThreadNotes: () => ({ data: undefined, isSuccess: false }),
  useAddThreadNote: () => ({ mutate: vi.fn(), isPending: false, isError: false }),
  useDeleteThreadNote: () => ({ mutate: vi.fn(), isPending: false, isError: false }),
}));

function makeMessage(overrides: Partial<MailMessage> = {}): MailMessage {
  return {
    id: 42,
    mailbox_id: 1,
    direction: 'inbound',
    status: 'received',
    from: { address: 'alice@example.com', name: 'Alice' },
    to: ['hello@zmail.test'],
    cc: [],
    subject: 'Q4 roadmap',
    read: true,
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
    thread_id: 42,
    trashed_at: null,
    text_body: 'Let us align on Q4.',
    html_body: '',
    message_id: 'msg-42@example.com',
    in_reply_to: '',
    events: [],
    attachments: [],
    ...overrides,
  };
}

function makeInboxItem(overrides: Partial<InboxItem> = {}): InboxItem {
  return {
    id: 42,
    mailbox_id: 1,
    direction: 'inbound',
    status: 'received',
    from: { address: 'alice@example.com', name: 'Alice' },
    to: ['hello@zmail.test'],
    cc: [],
    subject: 'Q4 roadmap',
    read: true,
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
    thread_id: 42,
    trashed_at: null,
    mailbox_address: 'hello@zmail.test',
    role_id: null,
    snippet: 'Let us align on Q4.',
    ...overrides,
  };
}

function renderWithProviders(
  ui: React.ReactNode,
  {
    locale = 'en-US',
    queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    }),
  }: { locale?: 'en-US' | 'zh-Hans'; queryClient?: QueryClient } = {}
) {
  const localeMessages =
    locale === 'zh-Hans'
      ? ({ ...messages, mailbox: zhHansMailbox } as unknown as AbstractIntlMessages)
      : (messages as unknown as AbstractIntlMessages);

  return {
    queryClient,
    ...render(
      <QueryClientProvider client={queryClient}>
        <NextIntlClientProvider locale={locale} timeZone="UTC" messages={localeMessages}>
          <MailOrganizationBoundary
            scope={{
              status: 'ready',
              organization: {
                id: 10,
                name: 'Acme',
                canManage: true,
                features: {
                  broadcasts: true,
                  contacts: true,
                  templates: true,
                  api_keys: true,
                  mail_clients: true,
                  developer: true,
                },
              },
              organizations: [
                {
                  id: 10,
                  name: 'Acme',
                  slug: 'acme',
                  role: 'owner',
                  created_at: '2026-11-20T00:00:00Z',
                  updated_at: '2026-11-20T00:00:00Z',
                },
              ],
              select: () => undefined,
            }}
          >
            {() => ui}
          </MailOrganizationBoundary>
        </NextIntlClientProvider>
      </QueryClientProvider>
    ),
  };
}

describe('parseSearchOperators', () => {
  it('returns trimmed query with no flags when no operators are present', () => {
    expect(parseSearchOperators('')).toEqual({ cleanQuery: '' });
    expect(parseSearchOperators('   ')).toEqual({ cleanQuery: '' });
    expect(parseSearchOperators('  quarterly   budget ')).toEqual({
      cleanQuery: 'quarterly budget',
    });
  });

  it('extracts individual Gmail-style operators and preserves remaining keywords', () => {
    expect(parseSearchOperators('is:unread invoice')).toEqual({
      cleanQuery: 'invoice',
      unread: true,
    });
    expect(parseSearchOperators('invoice has:attachment report')).toEqual({
      cleanQuery: 'invoice report',
      attachments: true,
    });
    expect(parseSearchOperators('has:attachments spec')).toEqual({
      cleanQuery: 'spec',
      attachments: true,
    });
    expect(parseSearchOperators('after:7d launch')).toEqual({
      cleanQuery: 'launch',
      dateRange: 'week',
    });
    expect(parseSearchOperators('after:30d retro')).toEqual({
      cleanQuery: 'retro',
      dateRange: 'month',
    });
  });

  it('extracts combined operators case-insensitively and ignores unknown or embedded tokens', () => {
    expect(parseSearchOperators('IS:UNREAD Has:Attachment AFTER:7D   project   alpha  ')).toEqual({
      cleanQuery: 'project alpha',
      unread: true,
      attachments: true,
      dateRange: 'week',
    });
    expect(parseSearchOperators('is:unread')).toEqual({
      cleanQuery: '',
      unread: true,
    });
    expect(parseSearchOperators('from:alice@example.com emailis:unread after:90d')).toEqual({
      cleanQuery: 'from:alice@example.com emailis:unread after:90d',
    });
  });
});

describe('InboxListHeader smart search quick-filter chips and operators', () => {
  it('shows quick-filter chips on focus/toggle and toggles filters without losing typed text', () => {
    const onSearch = vi.fn();
    const onUnreadChange = vi.fn();
    const onAttachmentsChange = vi.fn();
    const onDateRangeChange = vi.fn();

    renderWithProviders(
      <InboxListHeader
        title="Inbox"
        mailboxSelected={false}
        unread={false}
        onUnreadChange={onUnreadChange}
        attachments={false}
        onAttachmentsChange={onAttachmentsChange}
        dateRange={null}
        onDateRangeChange={onDateRangeChange}
        q=""
        onSearch={onSearch}
        onRefresh={vi.fn()}
        refreshing={false}
      />
    );

    expect(screen.queryByRole('group', { name: 'Quick search filters' })).not.toBeInTheDocument();

    const input = screen.getByRole('searchbox', { name: 'Search all mail' });
    fireEvent.focus(input);
    fireEvent.change(input, { target: { value: 'design review' } });

    const chipGroup = screen.getByRole('group', { name: 'Quick search filters' });
    expect(chipGroup).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Unread' }));
    expect(onUnreadChange).toHaveBeenCalledWith(true);
    expect(input).toHaveValue('design review');

    fireEvent.click(screen.getByRole('button', { name: 'Has attachment' }));
    expect(onAttachmentsChange).toHaveBeenCalledWith(true);
    expect(input).toHaveValue('design review');

    fireEvent.click(screen.getByRole('button', { name: 'Past 7 days' }));
    expect(onDateRangeChange).toHaveBeenCalledWith('week');

    fireEvent.click(screen.getByRole('button', { name: 'Past 30 days' }));
    expect(onDateRangeChange).toHaveBeenCalledWith('month');
    expect(input).toHaveValue('design review');
  });

  it('toggles active chips off and supports zh-Hans labels via the filter trigger button', () => {
    const onUnreadChange = vi.fn();
    const onDateRangeChange = vi.fn();

    renderWithProviders(
      <InboxListHeader
        title="收件"
        mailboxSelected={false}
        unread={true}
        onUnreadChange={onUnreadChange}
        attachments={false}
        onAttachmentsChange={vi.fn()}
        dateRange="week"
        onDateRangeChange={onDateRangeChange}
        q=""
        onSearch={vi.fn()}
        onRefresh={vi.fn()}
        refreshing={false}
      />,
      { locale: 'zh-Hans' }
    );

    fireEvent.click(screen.getByRole('button', { name: '展开或收起快捷筛选' }));

    const unreadChip = screen.getByRole('button', { name: '未读' });
    const weekChip = screen.getByRole('button', { name: '近 7 天' });
    expect(unreadChip).toHaveAttribute('aria-pressed', 'true');
    expect(weekChip).toHaveAttribute('aria-pressed', 'true');
    expect(screen.getByRole('button', { name: '有附件' })).toHaveAttribute('aria-pressed', 'false');
    expect(screen.getByRole('button', { name: '近 30 天' })).toHaveAttribute(
      'aria-pressed',
      'false'
    );

    fireEvent.click(unreadChip);
    expect(onUnreadChange).toHaveBeenCalledWith(false);

    fireEvent.click(weekChip);
    expect(onDateRangeChange).toHaveBeenCalledWith(null);
  });

  it('parses Gmail-style operators on search submit and applies filters + clean query', () => {
    const onSearch = vi.fn();
    const onUnreadChange = vi.fn();
    const onAttachmentsChange = vi.fn();
    const onDateRangeChange = vi.fn();

    renderWithProviders(
      <InboxListHeader
        title="Inbox"
        mailboxSelected={false}
        unread={false}
        onUnreadChange={onUnreadChange}
        attachments={false}
        onAttachmentsChange={onAttachmentsChange}
        dateRange={null}
        onDateRangeChange={onDateRangeChange}
        q=""
        onSearch={onSearch}
        onRefresh={vi.fn()}
        refreshing={false}
      />
    );

    const input = screen.getByRole('searchbox', { name: 'Search all mail' });
    fireEvent.change(input, { target: { value: 'is:unread has:attachment after:30d invoice' } });
    fireEvent.submit(input);

    expect(onUnreadChange).toHaveBeenCalledWith(true);
    expect(onAttachmentsChange).toHaveBeenCalledWith(true);
    expect(onDateRangeChange).toHaveBeenCalledWith('month');
    expect(onSearch).toHaveBeenCalledWith('invoice');
    expect(input).toHaveValue('invoice');
  });
});

describe('InlineQuickReply', () => {
  beforeEach(() => {
    toast.mockReset();
    toast.success.mockReset();
    toast.error.mockReset();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('hides Reply all for a single recipient, expands inline on Reply, supports Escape, and sends with Cmd+Enter', async () => {
    const sendSpy = vi
      .spyOn(mailboxService, 'send')
      .mockResolvedValue(makeMessage({ id: 99, direction: 'outbound' }));
    const onPopOutReply = vi.fn();
    const onPopOutReplyAll = vi.fn();
    const onForward = vi.fn();
    const message = makeMessage();

    const { queryClient } = renderWithProviders(
      <InlineQuickReply
        organizationId={10}
        mailboxId={1}
        mailboxAddress="hello@zmail.test"
        replyTarget={message}
        forwardTarget={message}
        onPopOutReply={onPopOutReply}
        onPopOutReplyAll={onPopOutReplyAll}
        onForward={onForward}
      />
    );
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries');

    // Only one recipient -> Reply all pill is omitted.
    expect(screen.getByRole('button', { name: 'Reply inline' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Reply all inline' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Forward message' })).toBeInTheDocument();

    // Forward delegates directly without expanding inline.
    fireEvent.click(screen.getByRole('button', { name: 'Forward message' }));
    expect(onForward).toHaveBeenCalledWith(message);

    // Clicking Reply expands inline without invoking pop-out composer.
    fireEvent.click(screen.getByRole('button', { name: 'Reply inline' }));
    expect(onPopOutReply).not.toHaveBeenCalled();
    expect(screen.getByText('Reply to Alice <alice@example.com>')).toBeInTheDocument();

    const textarea = screen.getByRole('textbox', { name: 'Write a quick reply…' });
    expect(textarea).toHaveFocus();

    // Escape collapses when empty.
    fireEvent.keyDown(textarea, { key: 'Escape' });
    expect(screen.queryByRole('textbox', { name: 'Write a quick reply…' })).not.toBeInTheDocument();

    // Re-open, type content, verify Escape does NOT collapse when non-empty, then send via Cmd+Enter.
    fireEvent.click(screen.getByRole('button', { name: 'Reply inline' }));
    const openTextarea = screen.getByRole('textbox', { name: 'Write a quick reply…' });
    fireEvent.change(openTextarea, { target: { value: 'Sounds good, see you then!' } });
    fireEvent.keyDown(openTextarea, { key: 'Escape' });
    expect(openTextarea).toBeInTheDocument();

    fireEvent.keyDown(openTextarea, { key: 'Enter', metaKey: true });

    await waitFor(() => {
      expect(sendSpy).toHaveBeenCalledWith(10, 1, {
        to: ['alice@example.com'],
        subject: 'Re: Q4 roadmap',
        text: 'Sounds good, see you then!',
        in_reply_to: 'msg-42@example.com',
      });
    });

    await waitFor(() => {
      expect(toast.success).toHaveBeenCalledWith('Message sent');
    });
    expect(invalidateSpy).toHaveBeenCalledWith({
      queryKey: mailboxKeys.threads(10, 1),
    });
    expect(invalidateSpy).toHaveBeenCalledWith({
      queryKey: mailboxKeys.inboxes(10),
    });
    expect(screen.queryByRole('textbox', { name: 'Write a quick reply…' })).not.toBeInTheDocument();
  });

  it('shows Reply all when multiple recipients exist, sends with cc, and supports Pop out', async () => {
    const sendSpy = vi
      .spyOn(mailboxService, 'send')
      .mockResolvedValue(makeMessage({ id: 100, direction: 'outbound' }));
    const onPopOutReply = vi.fn();
    const onPopOutReplyAll = vi.fn();
    const multiRecipientMessage = makeMessage({
      to: ['hello@zmail.test', 'bob@example.com'],
      cc: ['carol@example.com'],
      subject: 'Re: Sprint sync',
    });

    renderWithProviders(
      <InlineQuickReply
        organizationId={10}
        mailboxId={1}
        mailboxAddress="hello@zmail.test"
        replyTarget={multiRecipientMessage}
        forwardTarget={multiRecipientMessage}
        onPopOutReply={onPopOutReply}
        onPopOutReplyAll={onPopOutReplyAll}
        onForward={vi.fn()}
      />
    );

    const replyAllBtn = screen.getByRole('button', { name: 'Reply all inline' });
    fireEvent.click(replyAllBtn);

    expect(screen.getByText('Reply all to Alice <alice@example.com>')).toBeInTheDocument();
    expect(screen.getByText('Cc: bob@example.com, carol@example.com')).toBeInTheDocument();

    // Pop out collapses inline and delegates to navigation.onReplyAll.
    fireEvent.click(screen.getByRole('button', { name: 'Pop out' }));
    expect(onPopOutReplyAll).toHaveBeenCalledWith(multiRecipientMessage);
    expect(screen.queryByRole('textbox', { name: 'Write a quick reply…' })).not.toBeInTheDocument();

    // Re-expand Reply all and send via button click.
    fireEvent.click(screen.getByRole('button', { name: 'Reply all inline' }));
    const textarea = screen.getByRole('textbox', { name: 'Write a quick reply…' });
    fireEvent.change(textarea, { target: { value: 'Looping everyone in.' } });
    fireEvent.click(screen.getByRole('button', { name: 'Send' }));

    await waitFor(() => {
      expect(sendSpy).toHaveBeenCalledWith(10, 1, {
        to: ['alice@example.com'],
        cc: ['bob@example.com', 'carol@example.com'],
        subject: 'Re: Sprint sync',
        text: 'Looping everyone in.',
        in_reply_to: 'msg-42@example.com',
      });
    });
  });

  it('keeps the draft text and shows an error toast if sending fails', async () => {
    vi.spyOn(mailboxService, 'send').mockRejectedValue(
      new ApiError('down', ApiErrorCode.COMMON_SERVICE_UNAVAILABLE, 503)
    );
    const message = makeMessage();

    renderWithProviders(
      <InlineQuickReply
        organizationId={10}
        mailboxId={1}
        mailboxAddress="hello@zmail.test"
        replyTarget={message}
        forwardTarget={message}
        onPopOutReply={vi.fn()}
        onPopOutReplyAll={vi.fn()}
        onForward={vi.fn()}
      />
    );

    fireEvent.click(screen.getByRole('button', { name: 'Reply inline' }));
    const textarea = screen.getByRole('textbox', { name: 'Write a quick reply…' });
    fireEvent.change(textarea, { target: { value: 'Keep my draft safe' } });
    fireEvent.click(screen.getByRole('button', { name: 'Send' }));

    await waitFor(() => {
      expect(toast.error).toHaveBeenCalled();
    });
    expect(screen.getByRole('textbox', { name: 'Write a quick reply…' })).toHaveValue(
      'Keep my draft safe'
    );
  });

  it('renders inside InboxReadingPane when canReply is true and hides when false', async () => {
    const message = makeMessage();
    vi.spyOn(mailboxService, 'message').mockResolvedValue(message);
    vi.spyOn(mailboxService, 'listFolders').mockResolvedValue([]);
    vi.spyOn(mailboxService, 'listLabels').mockResolvedValue([]);
    const nav = {
      onPrevious: null,
      onNext: null,
      onClose: vi.fn(),
      onReply: vi.fn(),
      onReplyAll: vi.fn(),
      onForward: vi.fn(),
    };

    const { unmount } = renderWithProviders(
      <InboxReadingPane
        item={makeInboxItem()}
        access={{
          view: true,
          send: true,
          organize: true,
          manage_members: false,
          administer: false,
          see_history: false,
        }}
        navigation={nav}
        onUnavailable={vi.fn()}
      />
    );

    expect(await screen.findByRole('button', { name: 'Reply inline' })).toBeInTheDocument();
    unmount();

    renderWithProviders(
      <InboxReadingPane
        item={makeInboxItem()}
        access={{
          view: true,
          send: false,
          organize: false,
          manage_members: false,
          administer: false,
          see_history: false,
        }}
        navigation={nav}
        onUnavailable={vi.fn()}
      />
    );

    await waitFor(() => {
      expect(screen.getByRole('article')).toBeInTheDocument();
    });
    expect(screen.queryByRole('button', { name: 'Reply inline' })).not.toBeInTheDocument();
  });
});
