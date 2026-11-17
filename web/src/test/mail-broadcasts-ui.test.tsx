import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { NextIntlClientProvider, type AbstractIntlMessages } from 'next-intl';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { MailBroadcasts } from '@/features/mailbox/components/mail-broadcasts';
import { UnsubscribePanel } from '@/features/mailbox/components/unsubscribe-panel';
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
  const mutation = () => ({ isPending: false, mutateAsync: vi.fn() });
  return {
    organizations: query(),
    domains: query(),
    mailboxes: query(),
    templates: query(),
    lists: query(),
    broadcasts: query(),
    broadcast: query(),
    recipients: query(),
    recipientCalls: [] as { status: unknown; live: boolean }[],
    save: mutation(),
    send: mutation(),
    cancel: mutation(),
    remove: mutation(),
  };
});

vi.mock('sonner', () => ({ toast }));
vi.mock('next/navigation', () => ({
  useRouter: () => ({ replace: vi.fn() }),
  usePathname: () => '/console/mail-broadcasts',
  useSearchParams: () => new URLSearchParams(),
}));
vi.mock('@/features/organization/hooks/use-organizations', () => ({
  useOrganizations: () => state.organizations,
}));
vi.mock('@/features/mailbox/hooks/use-mailbox', () => ({
  useMailboxDomains: () => state.domains,
  useMailboxes: () => state.mailboxes,
}));
vi.mock('@/features/mailbox/hooks/use-mail-templates', () => ({
  useMailTemplates: () => state.templates,
}));
vi.mock('@/features/mailbox/hooks/use-mail-contacts', () => ({
  useMailContactLists: () => state.lists,
}));
vi.mock('@/features/mailbox/hooks/use-mail-broadcasts', () => ({
  useMailBroadcasts: () => state.broadcasts,
  useMailBroadcast: () => state.broadcast,
  useMailBroadcastRecipients: (
    _organizationId: number,
    _broadcastId: number,
    status: unknown,
    _page: number,
    live: boolean
  ) => {
    state.recipientCalls.push({ status, live });
    return state.recipients;
  },
  useSaveMailBroadcast: () => state.save,
  useSendMailBroadcast: () => state.send,
  useCancelMailBroadcast: () => state.cancel,
  useDeleteMailBroadcast: () => state.remove,
}));

describe('mail broadcasts console', () => {
  beforeEach(() => {
    toast.success.mockReset();
    toast.error.mockReset();
    state.organizations.data = page([
      { id: 1, name: 'Acme', slug: 'acme', role: 'owner', created_at: now(), updated_at: now() },
    ]);
    state.domains.data = [domain(true)];
    state.mailboxes.data = [
      {
        id: 2,
        address: 'news@mail.example.com',
        display_name: '',
        unread_count: 0,
        created_at: now(),
      },
    ];
    state.templates.data = [
      {
        id: 3,
        name: 'Newsletter',
        subject: 's',
        variables: [],
        created_at: now(),
        updated_at: now(),
      },
    ];
    state.lists.data = [{ id: 4, name: 'Customers', contact_count: 12, created_at: now() }];
    state.broadcasts.data = page([
      broadcast({
        id: 9,
        name: 'Launch',
        status: 'sending',
        counts: counts({ total: 10, sent: 4, pending: 6 }),
      }),
      broadcast({ id: 8, name: 'Draft idea' }),
    ]);
    state.broadcast.data = broadcast({ id: 8, name: 'Draft idea' });
    state.recipients.data = page([
      recipient({ id: 1, email: 'ann@example.com', status: 'sent' }),
      recipient({
        id: 2,
        email: 'bob@example.com',
        status: 'failed',
        failure_code: 'transport.permanent_bounce',
      }),
      recipient({
        id: 3,
        email: 'eve@example.com',
        status: 'skipped',
        failure_code: 'provider.custom',
      }),
    ]);
    state.recipientCalls = [];
    for (const mutation of [state.save, state.send, state.cancel, state.remove]) {
      mutation.isPending = false;
      mutation.mutateAsync.mockReset();
    }
  });

  it('lists broadcasts with status and progress', () => {
    renderBroadcasts();
    const row = screen.getByRole('button', { name: 'Open Launch' }).closest('tr') as HTMLElement;
    expect(within(row).getByText('Sending')).toBeInTheDocument();
    expect(within(row).getByText('4 of 10 sent')).toBeInTheDocument();
    expect(within(row).getByRole('progressbar')).toHaveAttribute('aria-valuenow', '4');
  });

  it('explains missing configuration and blocks new broadcasts', () => {
    state.domains.data = [domain(false)];
    renderBroadcasts();
    expect(screen.getByText('Broadcasts are not set up')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'New broadcast' })).toBeDisabled();
  });

  it('creates a draft from a mailbox, a template, and a list', async () => {
    state.save.mutateAsync.mockResolvedValue(broadcast({ id: 11 }));
    renderBroadcasts();
    fireEvent.click(screen.getByRole('button', { name: 'New broadcast' }));
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'November' } });
    fireEvent.change(screen.getByLabelText('From'), { target: { value: '2' } });
    fireEvent.change(screen.getByLabelText('Template'), { target: { value: '3' } });
    fireEvent.change(screen.getByLabelText('Contact list'), { target: { value: '4' } });
    expect(screen.getByRole('option', { name: 'Customers (12 contacts)' })).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText('Unsubscribe link text'), { target: { value: '退订' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save draft' }));

    await waitFor(() =>
      expect(state.save.mutateAsync).toHaveBeenCalledWith({
        input: {
          name: 'November',
          mailbox_id: 2,
          template_id: 3,
          list_id: 4,
          unsubscribe_label: '退订',
        },
      })
    );
    expect(toast.success).toHaveBeenCalledWith('Draft created');
  });

  it('sends a saved draft after confirmation', async () => {
    state.send.mutateAsync.mockResolvedValue(broadcast({ id: 8, status: 'sending' }));
    renderBroadcasts();
    fireEvent.click(screen.getByRole('button', { name: 'Open Draft idea' }));
    expect(screen.getByLabelText('Name')).toHaveValue('Draft idea');
    fireEvent.click(screen.getByRole('button', { name: 'Send now' }));
    const dialog = screen.getByRole('dialog');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Send now' }));
    await waitFor(() =>
      expect(state.send.mutateAsync).toHaveBeenCalledWith({ broadcastId: 8, input: {} })
    );
    expect(toast.success).toHaveBeenCalledWith('Broadcast started');
  });

  it('schedules a draft at the chosen local time', async () => {
    state.send.mutateAsync.mockResolvedValue(broadcast({ id: 8, status: 'scheduled' }));
    renderBroadcasts();
    fireEvent.click(screen.getByRole('button', { name: 'Open Draft idea' }));
    fireEvent.change(screen.getByLabelText('Send at'), { target: { value: '2030-01-02T09:30' } });
    fireEvent.click(screen.getByRole('button', { name: 'Schedule' }));
    fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Schedule' }));
    await waitFor(() =>
      expect(state.send.mutateAsync).toHaveBeenCalledWith({
        broadcastId: 8,
        input: { scheduled_at: new Date('2030-01-02T09:30').toISOString() },
      })
    );
  });

  it('explains a list with nobody to send to', async () => {
    state.send.mutateAsync.mockRejectedValue(
      new ApiError('none', ApiErrorCode.MAILBOX_BROADCAST_NO_RECIPIENTS, 422)
    );
    renderBroadcasts();
    fireEvent.click(screen.getByRole('button', { name: 'Open Draft idea' }));
    fireEvent.click(screen.getByRole('button', { name: 'Send now' }));
    fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Send now' }));
    expect(
      await screen.findByText('The list has no subscribed contacts, so there is no one to send to.')
    ).toBeInTheDocument();
  });

  it('shows progress, a paused send, recipients, and cancels a running broadcast', async () => {
    state.broadcast.data = broadcast({
      id: 9,
      name: 'Launch',
      status: 'sending',
      stop_reason: 'send_limit_reached',
      counts: counts({ total: 3, sent: 1, failed: 1, skipped: 1 }),
    });
    state.cancel.mutateAsync.mockResolvedValue(broadcast({ id: 9, status: 'canceled' }));
    renderBroadcasts();
    fireEvent.click(screen.getByRole('button', { name: 'Open Launch' }));

    expect(
      screen.getByText(/Paused: the organization reached its 24-hour send limit/u)
    ).toBeInTheDocument();
    expect(state.recipientCalls.at(-1)).toEqual({ status: undefined, live: true });
    const bob = screen.getByText('bob@example.com').closest('tr') as HTMLElement;
    expect(within(bob).getByText('Bounced')).toBeInTheDocument();
    expect(screen.getByText('provider.custom')).toBeInTheDocument();

    fireEvent.change(screen.getByLabelText('Recipient status'), { target: { value: 'failed' } });
    expect(state.recipientCalls.at(-1)).toEqual({ status: 'failed', live: true });

    fireEvent.click(screen.getByRole('button', { name: 'Cancel broadcast' }));
    const dialog = screen.getByRole('dialog');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel broadcast' }));
    await waitFor(() => expect(state.cancel.mutateAsync).toHaveBeenCalledWith(9));
    expect(toast.success).toHaveBeenCalledWith('Broadcast canceled');
  });
});

describe('unsubscribe page', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('unsubscribes only after the recipient confirms', async () => {
    const fetchMock = vi.fn(async () => new Response(null, { status: 204 }));
    vi.stubGlobal('fetch', fetchMock);
    const token = `1.2.${'a'.repeat(22)}`;
    renderWithMessages(<UnsubscribePanel token={token} />);
    expect(fetchMock).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'Unsubscribe' }));
    expect(await screen.findByText('You are unsubscribed')).toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledWith(
      `/api/mail-unsubscribes/${token}`,
      expect.objectContaining({ method: 'POST', credentials: 'omit' })
    );
  });

  it('rejects a malformed link without calling the server', () => {
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);
    renderWithMessages(<UnsubscribePanel token="not-a-token" />);
    expect(screen.getByText('This link is not valid')).toBeInTheDocument();
    expect(screen.queryByRole('button')).toBeNull();
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('reports a link the server does not accept', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => new Response(null, { status: 404 }))
    );
    renderWithMessages(<UnsubscribePanel token={`1.2.${'b'.repeat(22)}`} />);
    fireEvent.click(screen.getByRole('button', { name: 'Unsubscribe' }));
    expect(await screen.findByText('This link is not valid')).toBeInTheDocument();
  });
});

function domain(broadcastsEnabled: boolean) {
  return {
    domain: 'mail.example.com',
    kind: 'shared',
    max_mailboxes: 50,
    provider: 'cloudflare',
    inbound_enabled: true,
    daily_send_limit: 200,
    broadcasts_enabled: broadcastsEnabled,
    custom_domains_enabled: false,
  };
}

function counts(overrides: Record<string, number> = {}) {
  return { total: 0, pending: 0, sent: 0, failed: 0, skipped: 0, canceled: 0, ...overrides };
}

function broadcast(overrides: Record<string, unknown> = {}) {
  return {
    id: 8,
    name: 'Draft idea',
    mailbox_id: 2,
    template_id: 3,
    list_id: 4,
    unsubscribe_label: '',
    status: 'draft',
    scheduled_at: null,
    started_at: null,
    finished_at: null,
    stop_reason: '',
    counts: counts(),
    created_at: now(),
    updated_at: now(),
    ...overrides,
  };
}

function recipient(overrides: Record<string, unknown> = {}) {
  return {
    id: 1,
    contact_id: 1,
    email: 'ann@example.com',
    status: 'sent',
    message_id: null,
    failure_code: '',
    updated_at: now(),
    ...overrides,
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

function now() {
  return '2026-11-16T10:00:00Z';
}

function renderWithMessages(children: React.ReactNode) {
  return render(
    <NextIntlClientProvider locale="en-US" messages={messages as unknown as AbstractIntlMessages}>
      {children}
    </NextIntlClientProvider>
  );
}

function renderBroadcasts() {
  return renderWithMessages(<MailBroadcasts />);
}
