import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import type { ReactNode } from 'react';
import '@/i18n';
import { OrganizationControls } from '@/features/organizations/components/organization-controls';
import { parseSendLimit } from '@/features/organizations/send-limit';
import { OrganizationDetailPage } from '@/features/organizations/components/organization-detail-page';
import { OrganizationsPage } from '@/features/organizations/components/organizations-page';

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    headers: { 'content-type': 'application/json' },
    status,
  });
}

function page<T>(data: T[], total = data.length, lastPage = 1) {
  return {
    code: 0,
    message: 'success',
    data,
    meta: { current_page: 1, last_page: lastPage, per_page: 20, total },
    links: { first: '', last: '', prev: null, next: null },
  };
}

function withQueryClient(children: ReactNode) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
}

const acme = {
  id: 7,
  name: 'Acme',
  slug: 'acme',
  created_by: 3,
  member_count: 2,
  suspended_at: null,
  suspension_reason: '',
  daily_send_limit: null,
  created_at: '2026-09-01T00:00:00Z',
  updated_at: '2026-09-02T00:00:00Z',
};

const members = [
  {
    id: 11,
    user_id: 3,
    username: 'owner',
    nickname: 'Olive',
    email: 'owner@example.test',
    role: 'owner',
    joined_at: '2026-09-01T00:00:00Z',
  },
  {
    id: 12,
    user_id: 4,
    username: 'member',
    nickname: '',
    email: 'member@example.test',
    role: 'member',
    joined_at: '2026-09-03T00:00:00Z',
  },
];

describe('OrganizationsPage', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('lists organizations and opens one', async () => {
    const fetchMock = vi.fn().mockImplementation(() => Promise.resolve(json(page([acme], 41, 3))));
    vi.stubGlobal('fetch', fetchMock);
    const onOpen = vi.fn();
    const onSearchChange = vi.fn();
    render(
      withQueryClient(
        <OrganizationsPage search={{ q: 'ac' }} onSearchChange={onSearchChange} onOpen={onOpen} />,
      ),
    );

    const row = (await screen.findByText('Acme')).closest('tr') as HTMLElement;
    expect(within(row).getByText('acme · #7')).toBeInTheDocument();
    expect(screen.getByText('41 organizations')).toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledWith(
      '/api/v1/operator/organizations?page=1&per_page=20&q=ac',
      expect.anything(),
    );

    fireEvent.click(within(row).getByRole('button', { name: 'Open Acme' }));
    expect(onOpen).toHaveBeenCalledWith(7);

    fireEvent.click(screen.getByRole('button', { name: 'Next' }));
    expect(onSearchChange).toHaveBeenCalledWith({ q: 'ac', page: 2 });
  });

  it('moves the search into the URL state and resets the page', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockImplementation(() => Promise.resolve(json(page([])))),
    );
    const onSearchChange = vi.fn();
    render(
      withQueryClient(
        <OrganizationsPage search={{ page: 3 }} onSearchChange={onSearchChange} onOpen={vi.fn()} />,
      ),
    );

    expect(await screen.findByText(/no organizations match/i)).toBeInTheDocument();
    fireEvent.change(screen.getByPlaceholderText(/name or slug/i), { target: { value: ' glo ' } });
    fireEvent.submit(screen.getByRole('search'));
    expect(onSearchChange).toHaveBeenCalledWith({ q: 'glo', page: undefined });
  });
});

describe('OrganizationDetailPage', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('shows the organization, its members with email, and sections from other features', async () => {
    vi.stubGlobal(
      'fetch',
      vi
        .fn()
        .mockImplementation((url: string) =>
          Promise.resolve(
            url.includes('/members')
              ? json(page(members))
              : json({ code: 0, message: 'success', data: acme }),
          ),
        ),
    );
    render(
      withQueryClient(
        <OrganizationDetailPage organizationId={7} onBack={vi.fn()}>
          <p>starter section</p>
        </OrganizationDetailPage>,
      ),
    );

    expect(await screen.findByRole('heading', { level: 1, name: 'Acme' })).toBeInTheDocument();
    expect(await screen.findByText('owner · owner@example.test')).toBeInTheDocument();
    expect(screen.getByText('Olive')).toBeInTheDocument();
    expect(screen.getByText('Owner')).toBeInTheDocument();
    expect(screen.getByText('2 members')).toBeInTheDocument();
    expect(screen.getByText('starter section')).toBeInTheDocument();
  });

  it('explains an unknown organization and loads nothing beneath it', async () => {
    const fetchMock = vi
      .fn()
      .mockImplementation(() =>
        Promise.resolve(
          json({ code: 404, error_code: 'ORGANIZATION.NOT_FOUND', message: 'missing' }, 404),
        ),
      );
    vi.stubGlobal('fetch', fetchMock);
    const onBack = vi.fn();
    render(
      withQueryClient(
        <OrganizationDetailPage organizationId={99} onBack={onBack}>
          <p>starter section</p>
        </OrganizationDetailPage>,
      ),
    );

    expect(await screen.findByRole('alert')).toHaveTextContent(/does not exist/i);
    expect(screen.queryByText('starter section')).not.toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledTimes(1);

    fireEvent.click(screen.getByRole('button', { name: /all organizations/i }));
    expect(onBack).toHaveBeenCalled();
  });
});

describe('OrganizationControls', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('parses the send limit field', () => {
    expect(parseSendLimit('')).toBeNull();
    expect(parseSendLimit(' 5000 ')).toBe(5000);
    expect(parseSendLimit('1000000')).toBe(1000000);
    expect(parseSendLimit('1000001')).toBeUndefined();
    expect(parseSendLimit('0')).toBeUndefined();
    expect(parseSendLimit('-1')).toBeUndefined();
    expect(parseSendLimit('1.5')).toBeUndefined();
    expect(parseSendLimit('abc')).toBeUndefined();
  });

  it('suspends only with a reason, after confirmation', async () => {
    const fetchMock = vi.fn().mockImplementation(() =>
      Promise.resolve(
        json({
          code: 0,
          message: 'success',
          data: { ...acme, suspended_at: '2026-11-22T00:00:00Z', suspension_reason: 'spam' },
        }),
      ),
    );
    vi.stubGlobal('fetch', fetchMock);
    render(withQueryClient(<OrganizationControls organization={acme} />));

    fireEvent.click(screen.getByRole('button', { name: 'Suspend organization' }));
    const dialog = await screen.findByRole('dialog');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Suspend organization' }));
    expect(await within(dialog).findByRole('alert')).toHaveTextContent('Enter a reason');
    expect(fetchMock).not.toHaveBeenCalled();

    fireEvent.change(within(dialog).getByLabelText('Reason'), { target: { value: '  spam  ' } });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Suspend organization' }));
    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        '/api/v1/operator/organizations/7/suspend',
        expect.objectContaining({ method: 'POST', body: JSON.stringify({ reason: 'spam' }) }),
      ),
    );
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
  });

  it('offers restore for a suspended organization', () => {
    render(
      withQueryClient(
        <OrganizationControls
          organization={{ ...acme, suspended_at: '2026-11-22T00:00:00Z', suspension_reason: 'x' }}
        />,
      ),
    );
    expect(screen.getByRole('button', { name: 'Restore organization' })).toBeInTheDocument();
  });

  it('saves and clears the daily send limit', async () => {
    const fetchMock = vi.fn().mockImplementation((_url: string, init: RequestInit) =>
      Promise.resolve(
        json({
          code: 0,
          message: 'success',
          data: { ...acme, ...(JSON.parse(String(init.body)) as object) },
        }),
      ),
    );
    vi.stubGlobal('fetch', fetchMock);
    render(withQueryClient(<OrganizationControls organization={acme} />));

    const field = screen.getByLabelText('Daily send limit');
    fireEvent.change(field, { target: { value: '12x' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save limit' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('whole number');
    expect(fetchMock).not.toHaveBeenCalled();

    fireEvent.change(field, { target: { value: '5000' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save limit' }));
    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        '/api/v1/operator/organizations/7/send-limit',
        expect.objectContaining({
          method: 'PUT',
          body: JSON.stringify({ daily_send_limit: 5000 }),
        }),
      ),
    );
  });
});
