import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { NextIntlClientProvider, type AbstractIntlMessages } from 'next-intl';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { MailboxWorkspace } from '@/features/mailbox/components/mailbox-workspace';
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
    domains: query(),
    mailboxes: query(),
    inbox: query(),
    sent: query(),
    detail: query(),
    listCalls: [] as { mailboxId: number; direction: string; page: number }[],
    detailCalls: [] as (number | null)[],
    createMailbox: mutation(),
    deleteMailbox: mutation(),
    send: mutation(),
    update: mutation(),
    removeMessage: mutation(),
  };
});

vi.mock('sonner', () => ({ toast }));
vi.mock('@/features/mailbox/hooks/use-mailbox', () => ({
  useMailboxDomains: () => state.domains,
  useMailboxes: () => state.mailboxes,
  useMailMessages: (mailboxId: number, direction: string, page: number) => {
    state.listCalls.push({ mailboxId, direction, page });
    return direction === 'inbound' ? state.inbox : state.sent;
  },
  useMailMessage: (_mailboxId: number, messageId: number | null) => {
    state.detailCalls.push(messageId);
    return state.detail;
  },
  useCreateMailbox: () => state.createMailbox,
  useDeleteMailbox: () => state.deleteMailbox,
  useSendMailMessage: () => state.send,
  useUpdateMailMessage: () => state.update,
  useDeleteMailMessage: () => state.removeMessage,
}));

describe('mailbox workspace', () => {
  beforeEach(() => {
    toast.success.mockReset();
    toast.error.mockReset();
    state.domains.data = [{ domain: 'mail.example.com', max_mailboxes: 10 }];
    state.mailboxes.data = [mailbox(1, 'hello', 'Hello Team'), mailbox(2, 'sales', '')];
    state.inbox.data = page([
      summary({ id: 11, subject: 'Unread question', read: false }),
      summary({ id: 10, subject: 'Old news', read: true }),
    ]);
    state.sent.data = page([
      summary({
        id: 21,
        direction: 'outbound',
        status: 'failed',
        failure_code: 'provider.rejected',
        subject: 'Bounced offer',
        to: ['bob@example.com'],
        read: true,
      }),
    ]);
    state.detail.data = message();
    for (const query of [state.domains, state.mailboxes, state.inbox, state.sent, state.detail]) {
      query.isPending = false;
      query.error = null;
    }
    state.listCalls = [];
    state.detailCalls = [];
    for (const mutation of [
      state.createMailbox,
      state.deleteMailbox,
      state.send,
      state.update,
      state.removeMessage,
    ]) {
      mutation.isPending = false;
      mutation.mutate.mockReset();
      mutation.mutateAsync.mockReset();
    }
  });

  it('lists mailboxes with the limit and shows the inbox of the first one', () => {
    renderWorkspace();

    expect(screen.getByRole('button', { name: /^hello@mail\.example\.com/u })).toHaveAttribute(
      'aria-current',
      'true'
    );
    // Once in the list and once as the heading of the selected mailbox.
    expect(screen.getAllByText('Hello Team')).toHaveLength(2);
    expect(screen.getByText('No display name')).toBeInTheDocument();
    expect(screen.getByText('2 of 10 used')).toBeInTheDocument();
    expect(screen.getByText('@mail.example.com')).toBeInTheDocument();
    expect(state.listCalls.at(-1)).toEqual({ mailboxId: 1, direction: 'inbound', page: 1 });
    expect(screen.getByText('Unread question')).toBeInTheDocument();
    expect(screen.getAllByText('Unread')).toHaveLength(1);

    fireEvent.click(screen.getByRole('button', { name: /^sales@mail\.example\.com/u }));
    expect(state.listCalls.at(-1)).toEqual({ mailboxId: 2, direction: 'inbound', page: 1 });
  });

  it('shows an unread badge only on mailboxes with unread mail, capped at 99+', () => {
    state.mailboxes.data = [
      mailbox(1, 'hello', 'Hello Team', 3),
      mailbox(2, 'sales', '', 0),
      mailbox(3, 'busy', '', 250),
    ];
    renderWorkspace();

    expect(screen.getByLabelText('3 unread')).toHaveTextContent('3');
    expect(screen.getByLabelText('250 unread')).toHaveTextContent('99+');
    expect(screen.queryByLabelText('0 unread')).not.toBeInTheDocument();
  });

  it('creates a mailbox with a normalized address and selects it', async () => {
    state.createMailbox.mutateAsync.mockResolvedValue(mailbox(3, 'support-team', 'Support Team'));
    renderWorkspace();

    fireEvent.change(screen.getByLabelText('Address'), { target: { value: ' Support-Team ' } });
    fireEvent.change(screen.getByLabelText('Display name (optional)'), {
      target: { value: ' Support   Team ' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Create mailbox' }));

    await waitFor(() =>
      expect(state.createMailbox.mutateAsync).toHaveBeenCalledWith({
        local_part: 'support-team',
        display_name: 'Support Team',
      })
    );
    expect(toast.success).toHaveBeenCalledWith('Mailbox created');
    expect(screen.getByLabelText('Address')).toHaveValue('');
  });

  it('explains address errors without sending and maps the API codes', async () => {
    renderWorkspace();
    const address = screen.getByLabelText('Address');
    const submit = screen.getByRole('button', { name: 'Create mailbox' });

    fireEvent.change(address, { target: { value: 'postmaster' } });
    fireEvent.click(submit);
    expect(await screen.findByRole('alert')).toHaveTextContent(
      /reserved for the mail administrator/u
    );
    fireEvent.change(address, { target: { value: 'a..b' } });
    fireEvent.click(submit);
    expect(await screen.findByRole('alert')).toHaveTextContent(/1 to 64 lowercase letters/u);
    expect(state.createMailbox.mutateAsync).not.toHaveBeenCalled();

    const cases = [
      [ApiErrorCode.MAILBOX_ADDRESS_TAKEN, /already in use or was used before/u],
      [ApiErrorCode.MAILBOX_ADDRESS_INVALID, /1 to 64 lowercase letters/u],
      [ApiErrorCode.MAILBOX_ADDRESS_RESERVED, /reserved for the mail administrator/u],
      [ApiErrorCode.MAILBOX_LIMIT_REACHED, /reached the mailbox limit/u],
    ] as const;
    fireEvent.change(address, { target: { value: 'hello' } });
    for (const [code, copy] of cases) {
      state.createMailbox.mutateAsync.mockRejectedValueOnce(new ApiError('backend text', code));
      fireEvent.click(submit);
      await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent(copy));
    }
    expect(screen.queryByText('backend text')).toBeNull();
  });

  it('replaces the create form with the limit once it is reached', () => {
    state.domains.data = [{ domain: 'mail.example.com', max_mailboxes: 2 }];
    renderWorkspace();

    expect(screen.getByText(/reached the limit of 2 mailboxes/u)).toBeInTheDocument();
    expect(screen.queryByLabelText('Address')).toBeNull();
    expect(screen.queryByRole('button', { name: 'Create mailbox' })).toBeNull();
  });

  it('deletes a mailbox only after confirmation', async () => {
    state.deleteMailbox.mutateAsync.mockResolvedValue(undefined);
    renderWorkspace();

    fireEvent.click(screen.getByRole('button', { name: 'Delete sales@mail.example.com' }));
    expect(state.deleteMailbox.mutateAsync).not.toHaveBeenCalled();
    const dialog = screen.getByRole('dialog');
    expect(dialog).toHaveTextContent(/sales@mail\.example\.com and every message in it/u);
    fireEvent.click(within(dialog).getByRole('button', { name: 'Delete' }));

    await waitFor(() => expect(state.deleteMailbox.mutateAsync).toHaveBeenCalledWith(2));
    expect(toast.success).toHaveBeenCalledWith('Mailbox deleted');
  });

  it('marks an unread inbound message read when it is opened, and only then', () => {
    renderWorkspace();

    fireEvent.click(screen.getByRole('button', { name: /Old news/u }));
    expect(state.detailCalls.at(-1)).toBe(10);
    expect(state.update.mutate).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'Back to list' }));

    fireEvent.click(screen.getByRole('button', { name: /Unread question/u }));
    expect(state.detailCalls.at(-1)).toBe(11);
    expect(state.update.mutate).toHaveBeenCalledTimes(1);
    expect(state.update.mutate).toHaveBeenCalledWith({ messageId: 11, read: true });
  });

  it('renders sender HTML only inside a fully sandboxed frame', () => {
    state.detail.data = message({
      html_body: '<p id="from-sender">Hi</p><script>window.pwned = true</script>',
    });
    const { container } = renderWorkspace();
    fireEvent.click(screen.getByRole('button', { name: /Unread question/u }));

    const frame = screen.getByTitle('Message content');
    expect(frame.tagName).toBe('IFRAME');
    expect(frame.hasAttribute('sandbox')).toBe(true);
    expect(frame.getAttribute('sandbox')).toBe('');
    expect(frame.getAttribute('srcdoc')).toContain('<p id="from-sender">Hi</p>');
    expect(frame.getAttribute('srcdoc')).toContain("default-src 'none'");
    expect(frame.hasAttribute('src')).toBe(false);
    // Nothing from the sender is parsed into the application document.
    expect(container.querySelector('#from-sender')).toBeNull();
    expect(document.querySelector('script')).toBeNull();
    expect(container.querySelectorAll('iframe')).toHaveLength(1);

    fireEvent.click(screen.getByRole('button', { name: 'Plain text' }));
    expect(container.querySelector('iframe')).toBeNull();
    expect(screen.getByText('Plain question')).toBeInTheDocument();
  });

  it('shows headers and the text body, and offers mark unread, reply, and delete', async () => {
    state.detail.data = message({ html_body: '', cc: ['carol@example.com'] });
    state.update.mutateAsync.mockResolvedValue(message());
    state.removeMessage.mutateAsync.mockResolvedValue(undefined);
    const { container } = renderWorkspace();
    fireEvent.click(screen.getByRole('button', { name: /Unread question/u }));

    expect(screen.getByRole('heading', { name: 'Unread question' })).toBeInTheDocument();
    expect(screen.getByText('Bob <bob@example.com>')).toBeInTheDocument();
    expect(screen.getByText('carol@example.com')).toBeInTheDocument();
    expect(screen.getByText('Plain question')).toBeInTheDocument();
    expect(container.querySelector('iframe')).toBeNull();

    fireEvent.click(screen.getByRole('button', { name: 'Mark unread' }));
    await waitFor(() =>
      expect(state.update.mutateAsync).toHaveBeenCalledWith({ messageId: 11, read: false })
    );
    // Marking unread returns to the list instead of reopening (and re-reading) the message.
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Mark unread' })).toBeNull());

    fireEvent.click(screen.getByRole('button', { name: /Unread question/u }));
    fireEvent.click(screen.getByRole('button', { name: 'Delete' }));
    fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Delete' }));
    await waitFor(() => expect(state.removeMessage.mutateAsync).toHaveBeenCalledWith(11));
    expect(toast.success).toHaveBeenCalledWith('Message deleted');
  });

  it('switches to the sent folder and marks failed messages with the failure code', () => {
    renderWorkspace();

    fireEvent.click(screen.getByRole('button', { name: 'Sent' }));

    expect(state.listCalls.at(-1)).toEqual({ mailboxId: 1, direction: 'outbound', page: 1 });
    expect(screen.getByRole('button', { name: 'Sent' })).toHaveAttribute('aria-pressed', 'true');
    expect(screen.getByText('To: bob@example.com')).toBeInTheDocument();
    expect(screen.getByText('Failed')).toBeInTheDocument();
    expect(screen.getByText('provider.rejected')).toBeInTheDocument();
    expect(screen.queryByText('Unread')).toBeNull();

    state.detail.data = message({
      id: 21,
      direction: 'outbound',
      status: 'failed',
      failure_code: 'provider.rejected',
      html_body: '',
    });
    fireEvent.click(screen.getByRole('button', { name: /Bounced offer/u }));
    expect(screen.getByText(/was not delivered\. Reason: provider\.rejected/u)).toBeInTheDocument();
    expect(state.update.mutate).not.toHaveBeenCalled();
    expect(screen.queryByRole('button', { name: 'Reply' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Mark unread' })).toBeNull();
  });

  it('pages through a folder and shows empty states', () => {
    state.inbox.data = page([summary({ id: 11 })], { current_page: 1, last_page: 3 });
    const view = renderWorkspace();

    expect(screen.getByText('Page 1 of 3')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Previous' })).toBeDisabled();
    fireEvent.click(screen.getByRole('button', { name: 'Next' }));
    expect(state.listCalls.at(-1)).toEqual({ mailboxId: 1, direction: 'inbound', page: 2 });

    state.inbox.data = page([]);
    state.sent.data = page([]);
    view.rerender(wrap(<MailboxWorkspace />));
    expect(screen.getByText('Inbox is empty')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Sent' }));
    expect(screen.getByText('Nothing sent yet')).toBeInTheDocument();
  });

  it('validates a message before sending it', async () => {
    renderWorkspace();
    fireEvent.click(screen.getByRole('button', { name: 'Compose' }));
    const dialog = screen.getByRole('dialog');
    const submit = within(dialog).getByRole('button', { name: 'Send' });

    fireEvent.change(within(dialog).getByLabelText('To'), {
      target: { value: 'Bob <bob@example.com>' },
    });
    fireEvent.change(within(dialog).getByLabelText('Subject'), { target: { value: 'Hello' } });
    fireEvent.change(within(dialog).getByLabelText('Message'), { target: { value: 'Body' } });
    fireEvent.click(submit);
    expect(await within(dialog).findByText(/1 to 50 plain email addresses/u)).toBeInTheDocument();

    fireEvent.change(within(dialog).getByLabelText('To'), { target: { value: 'bob@example.com' } });
    fireEvent.change(within(dialog).getByLabelText('Message'), { target: { value: '   ' } });
    fireEvent.click(submit);
    expect(await within(dialog).findByText(/1 to 50 plain email addresses/u)).toBeInTheDocument();
    expect(state.send.mutateAsync).not.toHaveBeenCalled();
  });

  it('sends to comma and newline separated recipients, then shows the sent folder', async () => {
    state.send.mutateAsync.mockResolvedValue(message({ direction: 'outbound', status: 'sent' }));
    renderWorkspace();
    fireEvent.click(screen.getByRole('button', { name: 'Compose' }));
    const dialog = screen.getByRole('dialog');

    fireEvent.change(within(dialog).getByLabelText('To'), {
      target: { value: 'Bob@Example.com, carol@example.com\nbob@example.com' },
    });
    fireEvent.change(within(dialog).getByLabelText('Cc (optional)'), {
      target: { value: 'dave@example.com' },
    });
    fireEvent.change(within(dialog).getByLabelText('Subject'), { target: { value: ' Hello ' } });
    fireEvent.change(within(dialog).getByLabelText('Message'), { target: { value: 'Body text' } });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Send' }));

    await waitFor(() =>
      expect(state.send.mutateAsync).toHaveBeenCalledWith({
        to: ['bob@example.com', 'carol@example.com'],
        cc: ['dave@example.com'],
        subject: 'Hello',
        text: 'Body text',
      })
    );
    expect(toast.success).toHaveBeenCalledWith('Message sent');
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(state.listCalls.at(-1)).toEqual({ mailboxId: 1, direction: 'outbound', page: 1 });
  });

  it('says a rejected send was not delivered and is kept in Sent as failed', async () => {
    state.send.mutateAsync.mockRejectedValue(
      new ApiError('provider said no', ApiErrorCode.MAILBOX_DELIVERY_FAILED, 502)
    );
    renderWorkspace();
    fireEvent.click(screen.getByRole('button', { name: 'Compose' }));
    const dialog = screen.getByRole('dialog');
    fillValidMessage(dialog);
    fireEvent.click(within(dialog).getByRole('button', { name: 'Send' }));

    const alert = await within(dialog).findByRole('alert');
    expect(alert).toHaveTextContent(/NOT delivered/u);
    expect(alert).toHaveTextContent(/kept in Sent as failed/u);
    expect(screen.queryByText(/provider said no/u)).toBeNull();
    expect(toast.success).not.toHaveBeenCalled();
    // The sent folder now holds the failed attempt, so the workspace shows it behind the dialog.
    expect(state.listCalls.at(-1)).toEqual({ mailboxId: 1, direction: 'outbound', page: 1 });
    expect(screen.getByRole('dialog')).toBeInTheDocument();
  });

  it('maps the other send failures to their own copy without leaving the folder', async () => {
    renderWorkspace();
    fireEvent.click(screen.getByRole('button', { name: 'Compose' }));
    const dialog = screen.getByRole('dialog');
    fillValidMessage(dialog);

    const cases = [
      [ApiErrorCode.MAILBOX_MESSAGE_INVALID, /1 to 50 plain email addresses/u],
      [ApiErrorCode.MAILBOX_SEND_LIMIT_REACHED, /24-hour sending limit\. Nothing was sent/u],
    ] as const;
    for (const [code, copy] of cases) {
      state.send.mutateAsync.mockRejectedValueOnce(new ApiError('backend text', code));
      fireEvent.click(within(dialog).getByRole('button', { name: 'Send' }));
      await waitFor(() => expect(within(dialog).getByRole('alert')).toHaveTextContent(copy));
    }
    expect(state.listCalls.at(-1)?.direction).toBe('inbound');

    state.send.mutateAsync.mockRejectedValueOnce(
      new ApiError('backend text', ApiErrorCode.MAILBOX_SENDER_NOT_CONFIGURED, 503)
    );
    fireEvent.click(within(dialog).getByRole('button', { name: 'Send' }));
    await waitFor(() =>
      expect(within(dialog).getByRole('alert')).toHaveTextContent(
        /NOT delivered: sending mail is not set up/u
      )
    );
    expect(state.listCalls.at(-1)?.direction).toBe('outbound');
  });

  it('prefills a reply with the sender, a Re: subject, and the message it answers', async () => {
    state.send.mutateAsync.mockResolvedValue(message({ direction: 'outbound', status: 'sent' }));
    renderWorkspace();
    fireEvent.click(screen.getByRole('button', { name: /Unread question/u }));
    fireEvent.click(screen.getByRole('button', { name: 'Reply' }));
    const dialog = screen.getByRole('dialog');

    expect(within(dialog).getByLabelText('To')).toHaveValue('bob@example.com');
    expect(within(dialog).getByLabelText('Subject')).toHaveValue('Re: Unread question');
    fireEvent.change(within(dialog).getByLabelText('Message'), { target: { value: 'Answer' } });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Send' }));

    await waitFor(() =>
      expect(state.send.mutateAsync).toHaveBeenCalledWith({
        to: ['bob@example.com'],
        subject: 'Re: Unread question',
        text: 'Answer',
        in_reply_to: 'question-1@example.com',
      })
    );
  });

  it('shows a retryable error when mailboxes cannot be loaded', () => {
    state.mailboxes.data = undefined;
    state.mailboxes.error = new ApiError('boom', ApiErrorCode.COMMON_SERVICE_UNAVAILABLE);
    renderWorkspace();

    expect(screen.getByText('The mailbox service is temporarily unavailable.')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(state.mailboxes.refetch).toHaveBeenCalled();
  });
});

function fillValidMessage(dialog: HTMLElement) {
  fireEvent.change(within(dialog).getByLabelText('To'), { target: { value: 'bob@example.com' } });
  fireEvent.change(within(dialog).getByLabelText('Subject'), { target: { value: 'Hello' } });
  fireEvent.change(within(dialog).getByLabelText('Message'), { target: { value: 'Body' } });
}

function mailbox(id: number, localPart: string, displayName: string, unreadCount = 0) {
  return {
    id,
    address: `${localPart}@mail.example.com`,
    display_name: displayName,
    unread_count: unreadCount,
    created_at: now(),
  };
}

function summary(overrides: Record<string, unknown> = {}) {
  return {
    id: 11,
    mailbox_id: 1,
    direction: 'inbound',
    status: 'received',
    from: { address: 'bob@example.com', name: 'Bob' },
    to: ['hello@mail.example.com'],
    cc: [],
    subject: 'Unread question',
    read: false,
    failure_code: '',
    created_at: now(),
    ...overrides,
  };
}

function message(overrides: Record<string, unknown> = {}) {
  return {
    ...summary(),
    text_body: 'Plain question',
    html_body: '<p>Formatted question</p>',
    message_id: 'question-1@example.com',
    in_reply_to: '',
    ...overrides,
  };
}

function page(items: unknown[], meta: Record<string, number> = {}) {
  return {
    items,
    meta: {
      current_page: 1,
      per_page: 20,
      total: items.length,
      last_page: 1,
      from: items.length ? 1 : 0,
      to: items.length,
      ...meta,
    },
    links: { first: '', last: '', prev: null, next: null },
  };
}

function now() {
  return '2026-11-16T10:00:00Z';
}

function wrap(children: React.ReactNode) {
  return (
    <NextIntlClientProvider locale="en-US" messages={messages as unknown as AbstractIntlMessages}>
      {children}
    </NextIntlClientProvider>
  );
}

function renderWorkspace() {
  return render(wrap(<MailboxWorkspace />));
}
