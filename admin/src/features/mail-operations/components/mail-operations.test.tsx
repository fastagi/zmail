import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, within } from '@testing-library/react';
import type { ReactNode } from 'react';
import '@/i18n';
import { MailOrganizationsPage } from '@/features/mail-operations/components/mail-organizations-page';
import { MailOverviewPage } from '@/features/mail-operations/components/mail-overview-page';
import { OutboundMessagesPage } from '@/features/mail-operations/components/outbound-messages-page';
import { isSelectableDay } from '@/features/mail-operations/types';

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    headers: { 'content-type': 'application/json' },
    status,
  });
}

function failure(status: number, errorCode: string) {
  return json({ code: status, message: 'failed', error_code: errorCode }, status);
}

function page<T>(data: T[], total = data.length) {
  return {
    code: 0,
    message: 'success',
    data,
    meta: { current_page: 1, last_page: 1, per_page: 20, total },
    links: { first: '', last: '', prev: null, next: null },
  };
}

function withQueryClient(children: ReactNode) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
}

const metrics = {
  date: '2026-11-18',
  organizations: 2,
  members: 4,
  disabled_members: 1,
  mailboxes: 3,
  day: { sent: 2, failed: 1, queued: 0, delivered: 1, bounced: 1, complained: 0 },
  trend: [
    { date: '2026-11-17', sent: 0, failed: 0, queued: 0 },
    { date: '2026-11-18', sent: 2, failed: 1, queued: 0 },
  ],
};

const message = {
  id: 41,
  organization_id: 7,
  organization_name: 'North',
  mailbox_id: 3,
  mailbox_address: 'team@mail.example',
  from_address: 'team@mail.example',
  subject: 'Launch',
  status: 'sent',
  failure_code: '',
  last_event: 'delivered',
  created_at: '2026-11-18T09:30:00Z',
  send_attempted_at: '2026-11-18T09:30:01Z',
  broadcast_id: null,
};

describe('Mail operations', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('shows the day, the trend, and opens a day', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValue(json({ code: 0, message: 'success', data: metrics }));
    vi.stubGlobal('fetch', fetchMock);
    const onOpenDay = vi.fn();
    render(
      withQueryClient(
        <MailOverviewPage
          search={{ date: '2026-11-18' }}
          onSearchChange={vi.fn()}
          onOpenDay={onOpenDay}
        />,
      ),
    );

    expect(await screen.findByText('1 disabled account')).toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledWith(
      '/api/v1/operator/mail-metrics?days=7&date=2026-11-18',
      expect.anything(),
    );
    const day = screen.getByRole('region', { name: 'Outbound mail on 2026-11-18' });
    expect(within(day).getByText('Sent').nextSibling).toHaveTextContent('2');
    fireEvent.click(screen.getByRole('button', { name: /2026-11-17/ }));
    expect(onOpenDay).toHaveBeenCalledWith('2026-11-17');
  });

  it('shows an error with retry instead of zeros when the API fails', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(failure(503, 'COMMON.SERVICE_UNAVAILABLE'))
      .mockResolvedValueOnce(json({ code: 0, message: 'success', data: metrics }));
    vi.stubGlobal('fetch', fetchMock);
    render(
      withQueryClient(
        <MailOverviewPage search={{}} onSearchChange={vi.fn()} onOpenDay={vi.fn()} />,
      ),
    );

    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent(/could not be loaded/i);
    expect(screen.queryByText('Organizations')).not.toBeInTheDocument();
    expect(screen.queryByText('0')).not.toBeInTheDocument();
    fireEvent.click(within(alert).getByRole('button', { name: 'Retry' }));
    expect(await screen.findByText('1 disabled account')).toBeInTheDocument();
  });

  it('shows a not-permitted state for a grant without mail metrics', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(failure(403, 'OPERATOR.CAPABILITY_REQUIRED')));
    render(
      withQueryClient(
        <MailOrganizationsPage
          search={{ date: '2026-11-18' }}
          onSearchChange={vi.fn()}
          onBack={vi.fn()}
          onOpenMembers={vi.fn()}
          onOpenMessages={vi.fn()}
        />,
      ),
    );

    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('Not permitted');
    expect(within(alert).queryByRole('button', { name: 'Retry' })).not.toBeInTheDocument();
  });

  it('lists organizations and links members and sent mail', async () => {
    const north = { id: 7, name: 'North', members: 2, mailboxes: 1, sent: 2, failed: 1, queued: 0 };
    const quiet = { id: 9, name: 'Quiet', members: 1, mailboxes: 0, sent: 0, failed: 0, queued: 0 };
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(json(page([north, quiet]))));
    const onOpenMembers = vi.fn();
    const onOpenMessages = vi.fn();
    render(
      withQueryClient(
        <MailOrganizationsPage
          search={{ date: '2026-11-18' }}
          onSearchChange={vi.fn()}
          onBack={vi.fn()}
          onOpenMembers={onOpenMembers}
          onOpenMessages={onOpenMessages}
        />,
      ),
    );

    expect(await screen.findByText('Quiet')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Members of North: 2' }));
    expect(onOpenMembers).toHaveBeenCalledWith(7);
    fireEvent.click(screen.getByRole('button', { name: 'Mail sent by North: 2' }));
    expect(onOpenMessages).toHaveBeenCalledWith(7);
  });

  it('distinguishes an empty log and keeps the day when filters are cleared', async () => {
    const fetchMock = vi.fn().mockResolvedValue(json(page([])));
    vi.stubGlobal('fetch', fetchMock);
    const onSearchChange = vi.fn();
    render(
      withQueryClient(
        <OutboundMessagesPage
          search={{ date: '2026-11-18', organization_id: 7, status: 'failed' }}
          onSearchChange={onSearchChange}
          onBack={vi.fn()}
        />,
      ),
    );

    expect(await screen.findByText(/no outbound mail matches/i)).toBeInTheDocument();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledWith(
      '/api/v1/operator/outbound-messages?page=1&per_page=20&date=2026-11-18&organization_id=7&status=failed',
      expect.anything(),
    );
    fireEvent.click(screen.getByRole('button', { name: 'Clear filters' }));
    expect(onSearchChange).toHaveBeenLastCalledWith({ date: '2026-11-18' });
  });

  it('shows log metadata without content', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(json(page([message]))));
    render(
      withQueryClient(
        <OutboundMessagesPage
          search={{ date: '2026-11-18' }}
          onSearchChange={vi.fn()}
          onBack={vi.fn()}
        />,
      ),
    );

    const row = (await screen.findByText('Launch')).closest('tr') as HTMLElement;
    expect(within(row).getByText('09:30:00 UTC')).toBeInTheDocument();
    expect(within(row).getByText('Delivered')).toBeInTheDocument();
    expect(within(row).getByText('North')).toBeInTheDocument();
  });

  it('accepts only real days up to today', () => {
    const now = new Date('2026-11-20T12:00:00Z');
    expect(isSelectableDay('2026-11-20', now)).toBe(true);
    expect(isSelectableDay('2026-11-21', now)).toBe(false);
    expect(isSelectableDay('2026-02-30', now)).toBe(false);
    expect(isSelectableDay('20-11-2026', now)).toBe(false);
  });
});
