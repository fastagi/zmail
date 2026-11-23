import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { NextIntlClientProvider, type AbstractIntlMessages } from 'next-intl';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { MailDomainStatus } from '@/features/mailbox/components/mail-domain-status';
import { messages } from '@/i18n/modules';

/**
 * The domain page with the real hooks and query cache; only the HTTP service is faked. It proves
 * that a check's outcome reaches the rendered card without a remount.
 */
const server = vi.hoisted(() => ({ zoneStatus: 'pending' as 'pending' | 'active' }));
const service = vi.hoisted(() => ({
  domains: vi.fn(),
  list: vi.fn(),
  mailDomains: vi.fn(),
  listMailZones: vi.fn(),
  listDnsRecords: vi.fn(),
  refreshMailDomain: vi.fn(),
}));

vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn(), message: vi.fn() } }));
vi.mock('next/navigation', () => ({
  useRouter: () => ({ replace: vi.fn() }),
  usePathname: () => '/console/admin/domains',
  useSearchParams: () => new URLSearchParams(),
}));
vi.mock('@/features/organization/hooks/use-organizations', () => ({
  useOrganizations: () => ({
    data: {
      items: [
        {
          id: 1,
          name: 'Acme',
          slug: 'acme',
          role: 'owner',
          created_at: '2026-11-16T10:00:00Z',
          updated_at: '2026-11-16T10:00:00Z',
        },
      ],
      meta: { current_page: 1, per_page: 25, total: 1, last_page: 1, from: 1, to: 1 },
      links: { first: '', last: '', prev: null, next: null },
    },
    isPending: false,
    error: null,
    refetch: vi.fn(),
  }),
}));
vi.mock('@/features/mailbox/services/mailbox-service', () => ({ mailboxService: service }));

describe('checking a pending zone', () => {
  beforeEach(() => {
    server.zoneStatus = 'pending';
    service.domains.mockResolvedValue([
      {
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
        broadcasts_enabled: true,
        custom_domains_enabled: true,
      },
    ]);
    service.list.mockResolvedValue([]);
    service.mailDomains.mockImplementation(async () => [domain()]);
    service.listMailZones.mockImplementation(async () => [zone()]);
    service.listDnsRecords.mockResolvedValue([]);
    service.refreshMailDomain.mockImplementation(async () => {
      server.zoneStatus = 'active';
      return domain();
    });
  });

  it('shows the verified domain once the check activates its zone, without a remount', async () => {
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    });
    render(
      <QueryClientProvider client={client}>
        <NextIntlClientProvider
          locale="en-US"
          messages={messages as unknown as AbstractIntlMessages}
        >
          <MailDomainStatus />
        </NextIntlClientProvider>
      </QueryClientProvider>
    );

    const card = await screen.findByRole('article', { name: 'example.org' });
    expect(within(card).getByTestId('mail-domain-status')).toHaveTextContent('Pending');
    fireEvent.click(within(card).getByRole('button', { name: 'Check now' }));

    await waitFor(() =>
      expect(within(card).getByTestId('mail-domain-status')).toHaveTextContent('Verified')
    );
    expect(screen.getByRole('article', { name: 'example.org' })).toBe(card);
    expect(within(card).queryByRole('button', { name: 'Check now' })).toBeNull();
    expect(within(card).queryByText('Change the name servers of example.org')).toBeNull();
    // A verified domain folds into one row; its records open on request.
    expect(within(card).queryByRole('table')).toBeNull();
    fireEvent.click(within(card).getByRole('button', { name: 'Show DNS records' }));
    expect(within(card).getByRole('list', { name: 'Test your domain' })).toBeInTheDocument();
    expect(within(card).getAllByRole('row').length).toBeGreaterThan(1);
  });

  it("shows the check's answer before the domain list is read again", async () => {
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    });
    render(
      <QueryClientProvider client={client}>
        <NextIntlClientProvider
          locale="en-US"
          messages={messages as unknown as AbstractIntlMessages}
        >
          <MailDomainStatus />
        </NextIntlClientProvider>
      </QueryClientProvider>
    );
    const card = await screen.findByRole('article', { name: 'example.org' });
    // The list refetch after the check never answers; the card follows the check itself.
    service.mailDomains.mockImplementation(() => new Promise(() => undefined));

    fireEvent.click(within(card).getByRole('button', { name: 'Check now' }));

    await waitFor(() =>
      expect(within(card).getByTestId('mail-domain-status')).toHaveTextContent('Verified')
    );
    expect(within(card).queryByText('Change the name servers of example.org')).toBeNull();
  });
});

function zone() {
  const active = server.zoneStatus === 'active';
  return {
    id: 9,
    name: 'example.org',
    status: server.zoneStatus,
    name_servers: ['ada.ns.cloudflare.com', 'bob.ns.cloudflare.com'],
    original_name_servers: ['dns9.hichina.com'],
    activated_at: active ? '2026-11-18T10:00:00Z' : null,
    last_checked_at: null,
    created_at: '2026-11-17T09:59:00Z',
  };
}

function domain() {
  const active = server.zoneStatus === 'active';
  return {
    id: 7,
    name: 'example.org',
    zone_name: 'example.org',
    capability: 'send_receive',
    zone: zone(),
    status: active ? 'verified' : 'pending',
    records: active
      ? [
          {
            purpose: 'dmarc',
            type: 'TXT',
            name: '_dmarc.example.org',
            content: 'v=DMARC1; p=none',
            status: 'ok',
          },
        ]
      : [],
    setting_up: false,
    zone_check_limited: false,
    checked_at: '2026-11-17T10:00:00Z',
    verified_at: active ? '2026-11-18T10:00:00Z' : null,
    created_at: '2026-11-17T09:59:00Z',
  };
}
