import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { NextIntlClientProvider, type AbstractIntlMessages } from 'next-intl';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { MailDomainStatus } from '@/features/mailbox/components/mail-domain-status';
import { ApiErrorCode } from '@/http/codes';
import { ApiError } from '@/http/request';
import { messages } from '@/i18n/modules';

const toast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn(), message: vi.fn() }));
const state = vi.hoisted(() => {
  const query = () => ({
    data: undefined as unknown,
    isPending: false,
    error: null as unknown,
    refetch: vi.fn(),
  });
  const mutation = () => ({
    isPending: false,
    variables: undefined as unknown,
    mutateAsync: vi.fn(),
  });
  return {
    organizations: query(),
    domains: query(),
    mailboxes: query(),
    mailDomains: query(),
    mailDomainsEnabled: [] as boolean[],
    connect: mutation(),
    refresh: mutation(),
    remove: mutation(),
  };
});

vi.mock('sonner', () => ({ toast }));
vi.mock('next/navigation', () => ({
  useRouter: () => ({ replace: vi.fn() }),
  usePathname: () => '/console/mail-domain',
  useSearchParams: () => new URLSearchParams(),
}));
vi.mock('@/features/organization/hooks/use-organizations', () => ({
  useOrganizations: () => state.organizations,
}));
vi.mock('@/features/mailbox/hooks/use-mailbox', () => ({
  useMailboxDomains: () => state.domains,
  useMailboxes: () => state.mailboxes,
  useMailDomains: (_organizationId: number, enabled: boolean) => {
    state.mailDomainsEnabled.push(enabled);
    return state.mailDomains;
  },
  useConnectMailDomain: () => state.connect,
  useRefreshMailDomain: () => state.refresh,
  useRemoveMailDomain: () => state.remove,
}));

describe('organization mail domains', () => {
  beforeEach(() => {
    for (const fn of [toast.success, toast.error, toast.message]) fn.mockReset();
    state.organizations.data = page([organization('owner')]);
    state.domains.data = [sharedDomain(true)];
    state.mailboxes.data = [];
    state.mailDomains.data = [pendingDomain()];
    state.mailDomains.error = null;
    state.mailDomainsEnabled = [];
    for (const mutation of [state.connect, state.refresh, state.remove]) {
      mutation.mutateAsync.mockReset();
      mutation.isPending = false;
    }
  });

  it('lists each record a connected domain needs with its status', () => {
    renderPage();

    const card = screen.getByRole('article', { name: 'news.example.org' });
    expect(within(card).getByTestId('mail-domain-status')).toHaveTextContent('Pending');
    expect(within(card).getByText('Cloudflare zone: example.org')).toBeInTheDocument();
    expect(
      within(card).getByText(/last setup step failed: cloudflare enable email routing/u)
    ).toBeInTheDocument();
    const rows = within(card).getAllByRole('row').slice(1);
    expect(rows.map(row => row.getAttribute('data-record-status'))).toEqual([
      'ok',
      'missing',
      'conflict',
    ]);
    expect(within(rows[0]!).getByText('10 route1.mx.cloudflare.net')).toBeInTheDocument();
    expect(
      within(rows[2]!).getByText('Found instead: v=spf1 include:other.test -all')
    ).toBeInTheDocument();
    expect(within(card).getByRole('button', { name: 'Set up again' })).toBeInTheDocument();
  });

  it('connects a domain and reports whether it verified', async () => {
    state.connect.mutateAsync.mockResolvedValue({ ...pendingDomain(), status: 'verified' });
    renderPage();

    fireEvent.change(screen.getByLabelText('Domain'), { target: { value: '  example.org ' } });
    fireEvent.click(screen.getByRole('button', { name: 'Connect' }));

    await waitFor(() => expect(state.connect.mutateAsync).toHaveBeenCalledWith('example.org'));
    await waitFor(() => expect(toast.success).toHaveBeenCalledWith('Domain verified'));
  });

  it('shows a domain whose setup still runs and holds its actions', async () => {
    state.connect.mutateAsync.mockResolvedValue({
      ...pendingDomain(),
      records: [],
      setting_up: true,
    });
    state.mailDomains.data = [{ ...pendingDomain(), records: [], setting_up: true }];
    renderPage();

    const card = screen.getByRole('article', { name: 'news.example.org' });
    expect(within(card).getByTestId('mail-domain-status')).toHaveTextContent('Setting up');
    expect(within(card).getByRole('status')).toHaveTextContent(
      /setting up receiving, sending, and DMARC/u
    );
    expect(within(card).queryByRole('table')).toBeNull();
    expect(within(card).getByRole('button', { name: 'Check DNS' })).toBeDisabled();

    fireEvent.change(screen.getByLabelText('Domain'), { target: { value: 'example.org' } });
    fireEvent.click(screen.getByRole('button', { name: 'Connect' }));
    await waitFor(() =>
      expect(toast.success).toHaveBeenCalledWith('Setting up the domain in Cloudflare')
    );
  });

  it('shows why a connection was refused', async () => {
    state.connect.mutateAsync.mockRejectedValue(
      new ApiError('zone', ApiErrorCode.MAILBOX_DOMAIN_ZONE_NOT_FOUND)
    );
    renderPage();

    fireEvent.change(screen.getByLabelText('Domain'), { target: { value: 'elsewhere.test' } });
    fireEvent.click(screen.getByRole('button', { name: 'Connect' }));

    expect(
      await screen.findByText(/cannot reach a zone that contains this domain/u)
    ).toHaveAttribute('role', 'alert');
  });

  it('checks a domain and removes it after confirmation', async () => {
    state.refresh.mutateAsync.mockResolvedValue({ ...pendingDomain(), status: 'pending' });
    state.remove.mutateAsync.mockResolvedValue(undefined);
    renderPage();
    const card = screen.getByRole('article', { name: 'news.example.org' });

    fireEvent.click(within(card).getByRole('button', { name: 'Check DNS' }));
    await waitFor(() =>
      expect(state.refresh.mutateAsync).toHaveBeenCalledWith({
        domain: pendingDomain(),
        action: 'checks',
      })
    );
    await waitFor(() =>
      expect(toast.message).toHaveBeenCalledWith('Some records are not in place yet')
    );

    fireEvent.click(within(card).getByRole('button', { name: 'Remove' }));
    const dialog = await screen.findByRole('dialog');
    expect(dialog).toHaveTextContent('Remove news.example.org');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Remove' }));
    await waitFor(() => expect(state.remove.mutateAsync).toHaveBeenCalledWith(4));
    await waitFor(() => expect(toast.success).toHaveBeenCalledWith('Domain removed'));
  });

  it('lets members read domains without any action', () => {
    state.organizations.data = page([organization('member')]);
    renderPage();

    expect(screen.getByRole('article', { name: 'news.example.org' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Connect' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Check DNS' })).toBeNull();
    expect(screen.getByText('Only owners and admins can connect domains.')).toBeInTheDocument();
  });

  it('explains how to turn the feature on and loads nothing while it is off', () => {
    state.domains.data = [sharedDomain(false)];
    renderPage();

    expect(screen.getByText(/MAILBOX_CUSTOM_DOMAINS_ENABLED/u)).toBeInTheDocument();
    expect(screen.queryByRole('article')).toBeNull();
    expect(state.mailDomainsEnabled.every(enabled => !enabled)).toBe(true);
  });
});

function renderPage() {
  return render(
    <NextIntlClientProvider locale="en-US" messages={messages as unknown as AbstractIntlMessages}>
      <MailDomainStatus />
    </NextIntlClientProvider>
  );
}

function sharedDomain(customDomainsEnabled: boolean) {
  return {
    domain: 'mail.example.com',
    kind: 'shared',
    max_mailboxes: 50,
    provider: 'cloudflare',
    inbound_enabled: true,
    daily_send_limit: 200,
    broadcasts_enabled: true,
    custom_domains_enabled: customDomainsEnabled,
  };
}

function pendingDomain() {
  return {
    id: 4,
    name: 'news.example.org',
    zone_name: 'example.org',
    status: 'pending',
    setting_up: false,
    last_error: 'cloudflare enable email routing failed: Authentication error',
    records: [
      {
        purpose: 'receiving',
        type: 'MX',
        name: 'news.example.org',
        content: 'route1.mx.cloudflare.net',
        priority: 10,
        status: 'ok',
      },
      {
        purpose: 'sending',
        type: 'TXT',
        name: 'cf2024-1._domainkey.news.example.org',
        content: 'v=DKIM1; p=abc',
        status: 'missing',
      },
      {
        purpose: 'receiving',
        type: 'TXT',
        name: 'news.example.org',
        content: 'v=spf1 include:_spf.mx.cloudflare.net ~all',
        status: 'conflict',
        found: 'v=spf1 include:other.test -all',
      },
    ],
    checked_at: '2026-11-17T10:00:00Z',
    verified_at: null,
    created_at: '2026-11-17T09:59:00Z',
  };
}

function organization(role: string) {
  const now = '2026-11-16T10:00:00Z';
  return { id: 1, name: 'Acme', slug: 'acme', role, created_at: now, updated_at: now };
}

function page(items: unknown[]) {
  return {
    items,
    meta: {
      current_page: 1,
      per_page: 25,
      total: items.length,
      last_page: 1,
      from: 1,
      to: items.length,
    },
    links: { first: '', last: '', prev: null, next: null },
  };
}
