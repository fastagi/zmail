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
    template: query(),
    lists: query(),
    broadcasts: query(),
    broadcast: query(),
    recipients: query(),
    recipientCalls: [] as { status: unknown; live: boolean }[],
    searchParams: new URLSearchParams(),
    save: mutation(),
    send: mutation(),
    cancel: mutation(),
    pause: mutation(),
    resume: mutation(),
    remove: mutation(),
  };
});

vi.mock('sonner', () => ({ toast }));
vi.mock('next/navigation', () => ({
  useRouter: () => ({ replace: vi.fn() }),
  usePathname: () => '/console/mail-broadcasts',
  useSearchParams: () => state.searchParams,
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
  useMailTemplate: () => state.template,
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
  usePauseMailBroadcast: () => state.pause,
  useResumeMailBroadcast: () => state.resume,
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
        },
        unread_count: 0,
        created_at: now(),
      },
    ];
    state.templates.data = [
      {
        id: 3,
        name: 'Newsletter',
        subject: '{{first_name|Hi}}, use {{code}}',
        variables: ['code', 'ends', 'first_name', 'unsubscribe_url'],
        created_at: now(),
        updated_at: now(),
      },
    ];
    state.template.data = {
      id: 3,
      name: 'Newsletter',
      subject: '{{first_name|Hi}}, use {{code}}',
      variables: ['code', 'ends', 'first_name', 'unsubscribe_url'],
      html_body: '<p>{{code}} until {{ends|Friday}}</p><a href="{{unsubscribe_url}}">x</a>',
      text_body: '',
      sample_variables: {},
      design: null,
      created_at: now(),
      updated_at: now(),
      kind: 'standard',
      key: null,
      from: null,
      variable_definitions: [],
    };
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
      recipient({
        id: 1,
        email: 'ann@example.com',
        status: 'sent',
        last_event: 'delivered',
        contact_status: 'unsubscribed',
      }),
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
    state.searchParams = new URLSearchParams();
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
    expect(screen.getByText("Broadcasts aren't available yet")).toBeInTheDocument();
    expect(screen.getByText(/Ask them to finish broadcast setup/u)).toBeInTheDocument();
    // Setup names belong to the administrator's runbook, not to the console.
    expect(document.body.textContent).not.toMatch(/MAILBOX_|_SECRET|_URL/u);
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

    // Contact fields fill themselves; the other variables can be shared by every message.
    expect(
      screen.getByText('Filled from each contact: first_name, unsubscribe_url.')
    ).toBeInTheDocument();
    const code = screen.getByRole('textbox', { name: 'code' });
    expect(code).toHaveAccessibleDescription(
      'No value and no fallback: contacts without the "code" field will not get this broadcast.'
    );
    expect(screen.getByRole('textbox', { name: 'ends' })).toHaveAttribute(
      'placeholder',
      'Leave empty to use "Friday"'
    );
    fireEvent.change(code, { target: { value: 'NOV20' } });
    expect(code).not.toHaveAccessibleDescription();
    fireEvent.click(screen.getByRole('button', { name: 'Save draft' }));

    await waitFor(() =>
      expect(state.save.mutateAsync).toHaveBeenCalledWith({
        input: {
          name: 'November',
          mailbox_id: 2,
          template_id: 3,
          list_id: 4,
          unsubscribe_label: '退订',
          test_percent: 0,
          test_wait_minutes: 30,
          variables: { code: 'NOV20' },
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
    const at = localDateTime(30);
    renderBroadcasts();
    fireEvent.click(screen.getByRole('button', { name: 'Open Draft idea' }));
    fireEvent.change(screen.getByLabelText('Send at'), { target: { value: at } });
    fireEvent.click(screen.getByRole('button', { name: 'Schedule' }));
    fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Schedule' }));
    await waitFor(() =>
      expect(state.send.mutateAsync).toHaveBeenCalledWith({
        broadcastId: 8,
        input: { scheduled_at: new Date(at).toISOString() },
      })
    );
  });

  it('caps scheduling at one year ahead and names the date when it is exceeded (#96)', () => {
    renderBroadcasts();
    fireEvent.click(screen.getByRole('button', { name: 'Open Draft idea' }));
    const sendAt = screen.getByLabelText('Send at');
    const max = new Date(sendAt.getAttribute('max') ?? '').getTime();
    expect(Math.abs(max - new Date(localDateTime(365)).getTime())).toBeLessThanOrEqual(60_000);

    fireEvent.change(sendAt, { target: { value: localDateTime(367) } });
    fireEvent.click(screen.getByRole('button', { name: 'Schedule' }));

    expect(screen.queryByRole('dialog')).toBeNull();
    expect(state.send.mutateAsync).not.toHaveBeenCalled();
    expect(screen.getByText('Schedule this broadcast at most one year ahead.')).toBeInTheDocument();
    expect(screen.queryByText(/Check the broadcast/u)).toBeNull();
  });

  it('names the date when the API rejects a scheduled send as invalid (#96)', async () => {
    state.send.mutateAsync.mockRejectedValue(
      new ApiError('invalid', ApiErrorCode.MAILBOX_BROADCAST_INVALID, 422)
    );
    renderBroadcasts();
    fireEvent.click(screen.getByRole('button', { name: 'Open Draft idea' }));
    fireEvent.change(screen.getByLabelText('Send at'), { target: { value: localDateTime(300) } });
    fireEvent.click(screen.getByRole('button', { name: 'Schedule' }));
    fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Schedule' }));

    expect(
      await screen.findByText('Schedule this broadcast at most one year ahead.')
    ).toBeInTheDocument();
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

  it('keeps a clean finished broadcast to the counts that matter', () => {
    state.broadcast.data = broadcast({
      id: 9,
      name: 'Launch',
      status: 'sent',
      counts: counts({ total: 3, sent: 3 }),
    });
    renderBroadcasts();
    fireEvent.click(screen.getByRole('button', { name: 'Open Launch' }));

    const stats = screen.getByTestId('broadcast-stats');
    for (const label of ['Queue progress', 'Send success rate', 'Recipients', 'Sent']) {
      expect(within(stats).getByText(label)).toBeInTheDocument();
    }
    for (const label of ['Failed', 'Skipped', 'Pending', 'Canceled', 'Bounced', 'Spam reports']) {
      expect(within(stats).queryByText(label)).not.toBeInTheDocument();
    }
    expect(screen.getByText('No failures, bounces, or spam reports.')).toBeInTheDocument();
  });

  it('shows a count once it is not zero', () => {
    state.broadcast.data = broadcast({
      id: 9,
      name: 'Launch',
      status: 'sent',
      counts: counts({ total: 3, sent: 3, bounced: 1 }),
    });
    renderBroadcasts();
    fireEvent.click(screen.getByRole('button', { name: 'Open Launch' }));

    const stats = screen.getByTestId('broadcast-stats');
    expect(within(stats).getByText('Bounced').nextElementSibling).toHaveTextContent('1');
    expect(within(stats).queryByText('Spam reports')).not.toBeInTheDocument();
    expect(screen.queryByText('No failures, bounces, or spam reports.')).not.toBeInTheDocument();
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

    expect(screen.getByTestId('broadcast-limit')).toHaveTextContent(
      /Waiting for the daily send limit.*nothing is lost.*once the 24-hour window has room/u
    );
    expect(state.recipientCalls.at(-1)).toEqual({ status: undefined, live: true });
    expect(screen.getByText('Queue progress').nextElementSibling).toHaveTextContent('100%');
    expect(screen.getByText('Send success rate').nextElementSibling).toHaveTextContent('50%');
    const bob = screen.getByText('bob@example.com').closest('tr') as HTMLElement;
    expect(within(bob).getByText("The address doesn't exist or refuses mail")).toBeInTheDocument();
    // A provider's own code is not translated: it says "Not sent" and keeps the code for support.
    const eve = screen.getByText('eve@example.com').closest('tr') as HTMLElement;
    expect(within(eve).getByText('Not sent')).toBeInTheDocument();
    expect(within(eve).getByText('provider.custom')).toBeInTheDocument();
    const ann = screen.getByText('ann@example.com').closest('tr') as HTMLElement;
    expect(within(ann).getByText('Delivered')).toBeInTheDocument();
    expect(within(ann).getByText('Unsubscribed')).toBeInTheDocument();

    const filters = screen.getByRole('tablist', { name: 'Recipient status' });
    expect(within(filters).getByRole('tab', { name: 'All 3' })).toHaveAttribute(
      'aria-selected',
      'true'
    );
    // Canceled appears only once a recipient was canceled.
    expect(within(filters).queryByRole('tab', { name: /Canceled/u })).not.toBeInTheDocument();
    fireEvent.mouseDown(within(filters).getByRole('tab', { name: 'Failed 1' }));
    expect(state.recipientCalls.at(-1)).toEqual({ status: 'failed', live: true });

    fireEvent.click(screen.getByRole('button', { name: 'Cancel broadcast' }));
    const dialog = screen.getByRole('dialog');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel broadcast' }));
    await waitFor(() => expect(state.cancel.mutateAsync).toHaveBeenCalledWith(9));
    expect(toast.success).toHaveBeenCalledWith('Broadcast canceled');
  });

  it('shows the send rate, the estimated finish, and when the limit lets it continue', () => {
    state.broadcast.data = broadcast({
      id: 9,
      name: 'Launch',
      status: 'sending',
      started_at: '2026-11-23T07:50:00Z',
      counts: counts({ total: 500, sent: 200, pending: 300 }),
      progress: {
        rate_per_minute: 42.25,
        rate_window_minutes: 10,
        estimated_finish_at: '2026-11-24T08:39:00Z',
        send_limit_pauses: true,
        send_limit_resumes_at: '2026-11-24T08:00:00Z',
        computed_at: '2026-11-23T08:00:00Z',
      },
    });
    state.searchParams = new URLSearchParams('org=1&open=9');
    renderBroadcasts();

    // The notice link opened the broadcast directly.
    expect(screen.getByRole('heading', { name: 'Launch' })).toBeInTheDocument();
    expect(screen.getByTestId('broadcast-rate')).toHaveTextContent('42.3 per minute');
    expect(screen.getByTestId('broadcast-eta')).toHaveTextContent(/Nov 24, 2026.*\(tomorrow\)/u);
    expect(screen.getByTestId('broadcast-limit-ahead')).toHaveTextContent(
      /The daily send limit will pause this broadcast before it finishes/u
    );
    expect(screen.getByText(/in-app notice and an email/u)).toBeInTheDocument();
  });

  it('says when a broadcast held by the limit continues', () => {
    state.broadcast.data = broadcast({
      id: 9,
      name: 'Launch',
      status: 'sending',
      stop_reason: 'send_limit_reached',
      counts: counts({ total: 5, sent: 3, pending: 2 }),
      progress: {
        rate_per_minute: null,
        rate_window_minutes: 10,
        estimated_finish_at: '2026-11-24T08:00:05Z',
        send_limit_pauses: true,
        send_limit_resumes_at: '2026-11-24T08:00:00Z',
        computed_at: '2026-11-23T08:00:00Z',
      },
    });
    state.searchParams = new URLSearchParams('open=9');
    renderBroadcasts();
    expect(screen.getByTestId('broadcast-limit')).toHaveTextContent(
      /2 recipients are waiting and nothing is lost: sending continues on its own at about/u
    );
    expect(screen.queryByTestId('broadcast-limit-ahead')).not.toBeInTheDocument();
    expect(screen.getByTestId('broadcast-rate')).toHaveTextContent('Measuring');
  });

  it('has no estimate while a person holds the broadcast', () => {
    state.broadcast.data = broadcast({
      id: 9,
      name: 'Launch',
      status: 'paused',
      stop_reason: 'paused_by_owner',
      counts: counts({ total: 5, sent: 3, pending: 2 }),
      progress: {
        rate_per_minute: null,
        rate_window_minutes: 10,
        estimated_finish_at: null,
        send_limit_pauses: false,
        send_limit_resumes_at: null,
        computed_at: '2026-11-23T08:00:00Z',
      },
    });
    state.searchParams = new URLSearchParams('open=9');
    renderBroadcasts();
    expect(screen.getByTestId('broadcast-eta')).toHaveTextContent('None while paused');
  });

  it('ignores an open parameter that is not an id', () => {
    state.searchParams = new URLSearchParams('open=abc');
    renderBroadcasts();
    expect(screen.getByRole('button', { name: 'Open Launch' })).toBeInTheDocument();
  });

  it('labels when a canceled broadcast ended as canceled, not finished (#192)', () => {
    state.broadcast.data = broadcast({
      id: 9,
      name: 'Launch',
      status: 'canceled',
      finished_at: now(),
      counts: counts({ total: 2, canceled: 2 }),
    });
    renderBroadcasts();
    fireEvent.click(screen.getByRole('button', { name: 'Open Launch' }));

    expect(screen.getAllByText(/^Canceled /u).length).toBeGreaterThan(0);
    expect(screen.queryByText(/^Finished /u)).toBeNull();
  });

  // Issue #143: a resumed test wave waiting on its window is not shown as paused.
  it('labels a test wave waiting on its window apart from a manual pause', () => {
    state.broadcast.data = broadcast({
      id: 9,
      name: 'Launch',
      status: 'paused',
      stop_reason: 'test_wave_complete',
      counts: counts({ total: 3, sent: 1 }),
    });
    renderBroadcasts();
    fireEvent.click(screen.getByRole('button', { name: 'Open Launch' }));

    expect(screen.getAllByText('Watching test group').length).toBeGreaterThan(0);
    expect(screen.queryByText('Paused')).toBeNull();
  });

  it('shows a test wave resumed by hand as watching again, with when the rest is sent (#143)', async () => {
    const resumeAt = '2026-11-24T08:30:00Z';
    const staged = {
      id: 9,
      name: 'Launch',
      status: 'paused',
      test_percent: 50,
      resume_at: resumeAt,
      counts: counts({ total: 2, sent: 1, pending: 1 }),
    };
    state.broadcast.data = broadcast({ ...staged, stop_reason: 'paused_by_owner' });
    state.resume.mutateAsync.mockImplementation(async () => {
      const waiting = broadcast({ ...staged, stop_reason: 'test_wave_complete' });
      state.broadcast.data = waiting;
      return waiting;
    });
    const view = renderBroadcasts();
    fireEvent.click(screen.getByRole('button', { name: 'Open Launch' }));
    expect(screen.getAllByText('Paused').length).toBeGreaterThan(0);
    expect(screen.queryByRole('button', { name: 'Pause' })).toBeNull();

    fireEvent.click(screen.getByRole('button', { name: 'Resume' }));
    await waitFor(() =>
      expect(toast.success).toHaveBeenCalledWith(
        expect.stringMatching(/^Resumed\. Watching the test group; the rest is sent at .+\.$/u)
      )
    );
    // The app refetches after the mutation; here the mocked query is re-read on a render.
    view.rerender(
      <NextIntlClientProvider locale="en-US" messages={messages as unknown as AbstractIntlMessages}>
        <MailBroadcasts />
      </NextIntlClientProvider>
    );
    expect(screen.getAllByText('Watching test group').length).toBeGreaterThan(0);
    expect(screen.queryByText('Paused')).toBeNull();
    expect(screen.getByRole('button', { name: 'Pause' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Send to everyone now' })).toBeInTheDocument();
    expect(screen.getByTestId('broadcast-paused')).toHaveTextContent(/everyone else gets it at/u);
  });

  it('saves and sends a brand-new broadcast in one step', async () => {
    state.save.mutateAsync.mockResolvedValue(broadcast({ id: 15, name: 'Flash sale' }));
    state.send.mutateAsync.mockImplementation(async () => {
      const started = broadcast({
        id: 15,
        name: 'Flash sale',
        status: 'sending',
        counts: counts({ total: 12, pending: 12 }),
      });
      state.broadcast.data = started;
      return started;
    });

    renderBroadcasts();
    fireEvent.click(screen.getByRole('button', { name: 'New broadcast' }));
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Flash sale' } });
    fireEvent.change(screen.getByLabelText('From'), { target: { value: '2' } });
    fireEvent.change(screen.getByLabelText('Template'), { target: { value: '3' } });
    fireEvent.change(screen.getByLabelText('Contact list'), { target: { value: '4' } });
    fireEvent.change(screen.getByRole('textbox', { name: 'code' }), {
      target: { value: 'FLASH50' },
    });

    fireEvent.click(screen.getByRole('button', { name: 'Send now' }));
    const dialog = screen.getByRole('dialog');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Send now' }));

    await waitFor(() =>
      expect(state.save.mutateAsync).toHaveBeenCalledWith({
        input: {
          name: 'Flash sale',
          mailbox_id: 2,
          template_id: 3,
          list_id: 4,
          unsubscribe_label: '',
          test_percent: 0,
          test_wait_minutes: 30,
          variables: { code: 'FLASH50' },
        },
      })
    );
    await waitFor(() =>
      expect(state.send.mutateAsync).toHaveBeenCalledWith({ broadcastId: 15, input: {} })
    );
    expect(toast.success).toHaveBeenCalledWith('Broadcast started');
    expect(await screen.findByRole('heading', { name: 'Flash sale' })).toBeInTheDocument();
  });

  it('renders a live preview with sample variables, shared values, and unsubscribe placeholder', () => {
    state.template.data = {
      ...(state.template.data as Record<string, unknown>),
      sample_variables: { first_name: 'Ann' },
    };

    renderBroadcasts();
    fireEvent.click(screen.getByRole('button', { name: 'New broadcast' }));
    expect(screen.queryByRole('region', { name: 'Preview' })).toBeNull();

    fireEvent.change(screen.getByLabelText('Template'), { target: { value: '3' } });
    const preview = screen.getByRole('region', { name: 'Preview' });
    expect(
      within(preview).getByText(
        'Rendered with the shared template values above; contact fields use sample or fallback text.'
      )
    ).toBeInTheDocument();
    expect(within(preview).getByText('Ann, use')).toBeInTheDocument();

    fireEvent.change(screen.getByRole('textbox', { name: 'code' }), {
      target: { value: 'SAVE30' },
    });
    expect(within(preview).getByText('Ann, use SAVE30')).toBeInTheDocument();
    const frame = within(preview).getByTitle('Preview');
    expect(frame).toHaveAttribute('srcdoc', expect.stringContaining('SAVE30 until Friday'));
    expect(frame).toHaveAttribute('srcdoc', expect.stringContaining('href="#unsubscribe"'));
  });

  it('displays the local timezone hint and rejects past schedule times', () => {
    renderBroadcasts();
    fireEvent.click(screen.getByRole('button', { name: 'Open Draft idea' }));

    const sendAt = screen.getByLabelText('Send at');
    expect(sendAt).toHaveAccessibleDescription(/^Local time \(.+\)$/u);

    fireEvent.change(sendAt, { target: { value: '2020-01-01T09:00' } });
    fireEvent.click(screen.getByRole('button', { name: 'Schedule' }));

    expect(screen.queryByRole('dialog')).toBeNull();
    expect(state.send.mutateAsync).not.toHaveBeenCalled();
    expect(
      screen.getByText('Choose a future time to schedule this broadcast.')
    ).toBeInTheDocument();
  });

  it('confirms before discarding unsaved edits on Back and guards window unload', () => {
    renderBroadcasts();
    fireEvent.click(screen.getByRole('button', { name: 'Open Draft idea' }));
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Updated draft' } });

    const unloadEvent = new Event('beforeunload', { cancelable: true });
    window.dispatchEvent(unloadEvent);
    expect(unloadEvent.defaultPrevented).toBe(true);

    fireEvent.click(screen.getByRole('button', { name: 'Back to broadcasts' }));
    const keepDialog = screen.getByRole('dialog');
    expect(within(keepDialog).getByText('Discard unsaved changes?')).toBeInTheDocument();
    fireEvent.click(within(keepDialog).getByRole('button', { name: 'Keep it' }));
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(screen.getByLabelText('Name')).toHaveValue('Updated draft');

    fireEvent.click(screen.getByRole('button', { name: 'Back to broadcasts' }));
    const discardDialog = screen.getByRole('dialog');
    fireEvent.click(within(discardDialog).getByRole('button', { name: 'Discard changes' }));
    expect(screen.getByRole('button', { name: 'New broadcast' })).toBeInTheDocument();
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
    daily_broadcast_limit: 200,
    daily_transactional_limit: 200,
    transactional_enabled: true,
    transactional_recipient_limit_per_minute: 3,
    transactional_key_limit_per_second: 20,
    simulator_domain: 'test.zmail.im',
    broadcasts_enabled: broadcastsEnabled,
    custom_domains_enabled: false,
  };
}

function counts(overrides: Record<string, number> = {}) {
  return {
    total: 0,
    pending: 0,
    sent: 0,
    failed: 0,
    skipped: 0,
    canceled: 0,
    bounced: 0,
    complained: 0,
    ...overrides,
  };
}

function broadcast(overrides: Record<string, unknown> = {}) {
  return {
    id: 8,
    name: 'Draft idea',
    mailbox_id: 2,
    template_id: 3,
    list_id: 4,
    unsubscribe_label: '',
    variables: {},
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
    last_event: '',
    contact_status: 'subscribed',
    queued_reason: null,
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

/** A `datetime-local` value the given number of days from now, in the local time zone. */
function localDateTime(days: number): string {
  const at = new Date(Date.now() + days * 24 * 60 * 60 * 1_000);
  at.setSeconds(0, 0);
  return new Date(at.getTime() - at.getTimezoneOffset() * 60_000).toISOString().slice(0, 16);
}
