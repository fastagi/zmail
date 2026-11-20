import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import type { ReactNode } from 'react';
import '@/i18n';
import { ContentAccessPage } from '@/features/mail-operations/components/content-access-page';
import { OutboundMessagesPage } from '@/features/mail-operations/components/outbound-messages-page';
import { mailOperationsService } from '@/features/mail-operations/services/mail-operations-service';

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

function setup(children: ReactNode) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  render(<QueryClientProvider client={queryClient}>{children}</QueryClientProvider>);
  return queryClient;
}

const baseMessage = {
  organization_id: 7,
  organization_name: 'North',
  mailbox_id: 3,
  mailbox_address: 'team@mail.example',
  from_address: 'team@mail.example',
  status: 'sent',
  failure_code: '',
  last_event: 'delivered',
  created_at: '2026-11-18T09:30:00Z',
  send_attempted_at: '2026-11-18T09:30:01Z',
};
const broadcastMessage = { ...baseMessage, id: 41, subject: 'Launch', broadcast_id: 5 };
const personalMessage = { ...baseMessage, id: 42, subject: 'Invoice', broadcast_id: null };

const content = {
  access_id: 9,
  organization_id: 7,
  message_id: 41,
  broadcast_id: 5,
  subject: 'Launch',
  from_address: 'team@mail.example',
  created_at: '2026-11-18T09:30:00Z',
  text: 'Hello <b>friends</b>\nSee you soon',
  text_source: 'text',
};

function renderLog(fetchMock: ReturnType<typeof vi.fn>) {
  vi.stubGlobal('fetch', fetchMock);
  return setup(
    <OutboundMessagesPage
      search={{ date: '2026-11-18' }}
      onSearchChange={vi.fn()}
      onBack={vi.fn()}
    />,
  );
}

async function openContent() {
  fireEvent.click(await screen.findByRole('button', { name: 'View content of “Launch”' }));
  return screen.findByRole('dialog');
}

describe('Broadcast content', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('offers the action only on broadcast rows', async () => {
    renderLog(vi.fn().mockResolvedValue(json(page([broadcastMessage, personalMessage]))));

    const broadcastRow = (await screen.findByText('Launch')).closest('tr') as HTMLElement;
    const personalRow = screen.getByText('Invoice').closest('tr') as HTMLElement;
    expect(within(broadcastRow).getByRole('button', { name: /view content/i })).toBeInTheDocument();
    expect(within(personalRow).queryByRole('button')).not.toBeInTheDocument();
  });

  it('requires a reason, records it, and shows the text as plain text', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(json(page([broadcastMessage, personalMessage])))
      .mockResolvedValueOnce(json({ code: 0, message: 'success', data: content }));
    const queryClient = renderLog(fetchMock);
    const dialog = await openContent();

    fireEvent.click(within(dialog).getByRole('button', { name: 'Show content' }));
    expect(within(dialog).getByText('Enter a reason.')).toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledTimes(1);

    fireEvent.change(within(dialog).getByLabelText('Reason'), {
      target: { value: '  Complaint ticket 42 ' },
    });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Show content' }));

    const text = await within(dialog).findByText(/Hello <b>friends<\/b>/);
    expect(text.querySelector('b')).toBeNull();
    expect(within(dialog).getByText('Access recorded as #9.')).toBeInTheDocument();
    const [url, init] = fetchMock.mock.calls[1] as [string, RequestInit];
    expect(url).toBe('/api/v1/operator/outbound-messages/7/41/content');
    expect(init.method).toBe('POST');
    expect(JSON.parse(String(init.body))).toEqual({ reason: 'Complaint ticket 42' });

    // The text never enters the query cache.
    for (const query of queryClient.getQueryCache().getAll()) {
      expect(JSON.stringify(query.state.data ?? null)).not.toContain('friends');
    }

    fireEvent.click(within(dialog).getByRole('button', { name: 'Close' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    await waitFor(() => expect(queryClient.getMutationCache().getAll()).toHaveLength(0));

    const reopened = await openContent();
    expect(within(reopened).queryByText(/friends/)).not.toBeInTheDocument();
    expect(within(reopened).getByLabelText('Reason')).toHaveValue('');
  });

  it('shows no text when the access record cannot be stored', async () => {
    renderLog(
      vi
        .fn()
        .mockResolvedValueOnce(json(page([broadcastMessage])))
        .mockResolvedValueOnce(failure(503, 'COMMON.SERVICE_UNAVAILABLE'))
        .mockResolvedValueOnce(json({ code: 0, message: 'success', data: content })),
    );
    const dialog = await openContent();
    fireEvent.change(within(dialog).getByLabelText('Reason'), { target: { value: 'Complaint' } });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Show content' }));

    expect(await within(dialog).findByRole('alert')).toHaveTextContent(/nothing was revealed/i);
    expect(within(dialog).queryByText(/friends/)).not.toBeInTheDocument();

    fireEvent.click(within(dialog).getByRole('button', { name: 'Show content' }));
    expect(await within(dialog).findByText(/Hello <b>friends<\/b>/)).toBeInTheDocument();
  });

  it.each([
    ['OPERATOR.CONTENT_NOT_ELIGIBLE', 403, /only organization broadcast messages/i],
    ['OPERATOR.CAPABILITY_REQUIRED', 403, /does not include reading broadcast content/i],
    ['MAILBOX.MESSAGE_NOT_FOUND', 404, /not in that organization/i],
  ])('explains a refused read (%s)', async (code, status, text) => {
    renderLog(
      vi
        .fn()
        .mockResolvedValueOnce(json(page([broadcastMessage])))
        .mockResolvedValueOnce(failure(status, code)),
    );
    const dialog = await openContent();
    fireEvent.change(within(dialog).getByLabelText('Reason'), { target: { value: 'Complaint' } });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Show content' }));
    expect(await within(dialog).findByRole('alert')).toHaveTextContent(text);
  });
});

describe('Content access records', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  const record = {
    id: 3,
    viewer_user_id: 12,
    organization_id: 7,
    organization_name: 'North',
    message_id: 41,
    reason: 'Complaint ticket 42',
    accessed_at: '2026-11-19T08:00:00Z',
  };

  it('lists records and sends the filters as a UTC range', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValue(json(page([record, { ...record, id: 4, organization_name: '' }])));
    vi.stubGlobal('fetch', fetchMock);
    setup(
      <ContentAccessPage
        search={{ organization_id: 7, viewer_user_id: 12, from: '2026-11-18', to: '2026-11-19' }}
        onSearchChange={vi.fn()}
      />,
    );

    expect(await screen.findAllByText('Complaint ticket 42')).toHaveLength(2);
    expect(screen.getAllByText('User #12')).toHaveLength(2);
    expect(screen.getByText('Deleted organization')).toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledWith(
      '/api/v1/operator/content-access-audits?page=1&per_page=20&organization_id=7&viewer_user_id=12&from=2026-11-18T00%3A00%3A00Z&to=2026-11-20T00%3A00%3A00Z',
      expect.anything(),
    );
  });

  it('distinguishes an empty list', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(json(page([]))));
    setup(<ContentAccessPage search={{}} onSearchChange={vi.fn()} />);
    expect(await screen.findByText(/no body has been read/i)).toBeInTheDocument();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('shows an error with retry', async () => {
    vi.stubGlobal(
      'fetch',
      vi
        .fn()
        .mockResolvedValueOnce(failure(503, 'COMMON.SERVICE_UNAVAILABLE'))
        .mockResolvedValueOnce(json(page([record]))),
    );
    setup(<ContentAccessPage search={{}} onSearchChange={vi.fn()} />);
    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('Access records could not be loaded.');
    fireEvent.click(within(alert).getByRole('button', { name: 'Retry' }));
    expect(await screen.findByText('Complaint ticket 42')).toBeInTheDocument();
  });

  it('shows a not-permitted state for a grant without content or manage', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(failure(403, 'OPERATOR.CAPABILITY_REQUIRED')));
    setup(<ContentAccessPage search={{}} onSearchChange={vi.fn()} />);
    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('Not permitted');
    expect(alert).toHaveTextContent(/does not include content access records/i);
    expect(within(alert).queryByRole('button', { name: 'Retry' })).not.toBeInTheDocument();
  });

  it('ignores an end day before the start day', async () => {
    const fetchMock = vi.fn().mockResolvedValue(json(page([])));
    vi.stubGlobal('fetch', fetchMock);
    await mailOperationsService.contentAccess({ from: '2026-11-19', to: '2026-11-18' });
    expect(fetchMock).toHaveBeenCalledWith(
      '/api/v1/operator/content-access-audits?page=1&per_page=20&from=2026-11-19T00%3A00%3A00Z',
      expect.anything(),
    );
  });
});
