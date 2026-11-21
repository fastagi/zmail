import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { NextIntlClientProvider, type AbstractIntlMessages } from 'next-intl';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { MailDnsRecordsPage } from '@/features/mailbox/components/mail-dns-records';
import { MailDomainStatus } from '@/features/mailbox/components/mail-domain-status';
import { ApiErrorCode } from '@/http/codes';
import request, { ApiError } from '@/http/request';
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
    zones: query(),
    zone: query(),
    dnsRecords: query(),
    connect: mutation(),
    refresh: mutation(),
    remove: mutation(),
    preview: mutation(),
    receiving: mutation(),
    deleteZone: mutation(),
    createRecord: mutation(),
    updateRecord: mutation(),
    deleteRecord: mutation(),
  };
});

vi.mock('sonner', () => ({ toast }));
vi.mock('next/navigation', () => ({
  useRouter: () => ({ replace: vi.fn() }),
  usePathname: () => '/console/admin/domains',
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
  usePreviewMailDomain: () => state.preview,
  useEnableReceiving: () => state.receiving,
  useListMailZones: () => state.zones,
  useMailZone: () => state.zone,
  useDeleteMailZone: () => state.deleteZone,
  useListDnsRecords: () => state.dnsRecords,
  useCreateDnsRecord: () => state.createRecord,
  useUpdateDnsRecord: () => state.updateRecord,
  useDeleteDnsRecord: () => state.deleteRecord,
}));

describe('organization mail domains', () => {
  beforeEach(() => {
    vi.spyOn(request, 'get').mockResolvedValue({ connection: null });
    for (const fn of [toast.success, toast.error, toast.message]) fn.mockReset();
    state.organizations.data = page([organization('owner')]);
    state.domains.data = [sharedDomain(true)];
    state.mailboxes.data = [];
    state.mailDomains.data = [pendingDomain()];
    state.mailDomains.error = null;
    state.mailDomainsEnabled = [];
    state.zones.data = [];
    state.zone.data = pendingZone();
    state.dnsRecords.data = importedRecords();
    for (const mutation of [
      state.connect,
      state.refresh,
      state.remove,
      state.preview,
      state.receiving,
      state.deleteZone,
      state.createRecord,
      state.updateRecord,
      state.deleteRecord,
    ]) {
      mutation.mutateAsync.mockReset();
      mutation.isPending = false;
    }
    // By default a name has no mail service elsewhere.
    state.preview.mutateAsync.mockImplementation(async (name: string) => ({
      name,
      apex: name,
      existing_mx: [],
      suggested_capability: 'send_receive',
    }));
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

    await waitFor(() =>
      expect(state.connect.mutateAsync).toHaveBeenCalledWith({
        name: 'example.org',
        capability: 'send_receive',
      })
    );
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

  it('suggests send-only for foreign MX', async () => {
    state.preview.mutateAsync.mockResolvedValue({
      name: 'example.org',
      apex: 'example.org',
      existing_mx: ['mxbiz1.qq.com', 'mxbiz2.qq.com'],
      suggested_capability: 'send_only',
    });
    state.connect.mutateAsync.mockResolvedValue(pendingDomain());
    renderPage();

    const input = screen.getByLabelText('Domain');
    fireEvent.change(input, { target: { value: 'example.org' } });
    expect(screen.getByRole('radio', { name: /Send and receive/u })).toBeChecked();
    fireEvent.blur(input);

    await waitFor(() => expect(state.preview.mutateAsync).toHaveBeenCalledWith('example.org'));
    expect(await screen.findByRole('radio', { name: /Send only/u })).toBeChecked();
    const hint = screen.getByTestId('mail-domain-subdomain-hint');
    expect(hint).toHaveTextContent('mxbiz1.qq.com');
    expect(hint).toHaveTextContent('mxbiz2.qq.com');
    expect(hint).toHaveTextContent(/mail\.example\.org/u);

    fireEvent.click(screen.getByRole('button', { name: 'Connect' }));
    await waitFor(() =>
      expect(state.connect.mutateAsync).toHaveBeenCalledWith({
        name: 'example.org',
        capability: 'send_only',
      })
    );
  });

  it('looks a name up before connecting it when the field was never left', async () => {
    state.preview.mutateAsync.mockImplementation(async (name: string) => ({
      name,
      apex: name,
      existing_mx: ['mxbiz1.qq.com'],
      suggested_capability: 'send_only',
    }));
    state.connect.mutateAsync.mockResolvedValue(pendingDomain());
    renderPage();

    // Typing and pressing Enter submits without a blur.
    fireEvent.change(screen.getByLabelText('Domain'), { target: { value: 'example.org' } });
    fireEvent.submit(screen.getByRole('button', { name: 'Connect' }).closest('form')!);

    await waitFor(() => expect(state.preview.mutateAsync).toHaveBeenCalledWith('example.org'));
    expect(await screen.findByRole('radio', { name: /Send only/u })).toBeChecked();
    expect(screen.getByTestId('mail-domain-subdomain-hint')).toHaveTextContent('mxbiz1.qq.com');
    expect(state.connect.mutateAsync).not.toHaveBeenCalled();

    // A second submit confirms the suggestion.
    fireEvent.click(screen.getByRole('button', { name: 'Connect' }));
    await waitFor(() =>
      expect(state.connect.mutateAsync).toHaveBeenCalledWith({
        name: 'example.org',
        capability: 'send_only',
      })
    );
    expect(state.preview.mutateAsync).toHaveBeenCalledTimes(1);
  });

  it('sends one connect for a double submit while the lookup runs', async () => {
    let answer: (value: unknown) => void = () => undefined;
    state.preview.mutateAsync.mockImplementation(
      (name: string) =>
        new Promise(resolve => {
          answer = () =>
            resolve({ name, apex: name, existing_mx: [], suggested_capability: 'send_receive' });
        })
    );
    state.connect.mutateAsync.mockResolvedValue(pendingDomain());
    renderPage();
    const input = screen.getByLabelText('Domain');
    const form = screen.getByRole('button', { name: 'Connect' }).closest('form')!;

    fireEvent.change(input, { target: { value: 'mail.example.org' } });
    fireEvent.submit(form);
    fireEvent.submit(form);
    // The form is held while it submits.
    await waitFor(() => expect(screen.getByLabelText('Domain')).toBeDisabled());
    expect(screen.getByRole('radio', { name: /Send only/u })).toBeDisabled();

    answer(undefined);
    await waitFor(() => expect(toast.success).toHaveBeenCalledWith('Domain connected'));
    expect(state.connect.mutateAsync).toHaveBeenCalledTimes(1);
    expect(within(form).queryByRole('alert')).toBeNull();
    expect(screen.getByLabelText('Domain')).not.toBeDisabled();
  });

  it('asks to confirm a capability the lookup changed', async () => {
    state.preview.mutateAsync.mockImplementation(async (name: string) => ({
      name,
      apex: name,
      existing_mx: ['mxbiz1.qq.com'],
      suggested_capability: 'send_only',
    }));
    renderPage();

    fireEvent.change(screen.getByLabelText('Domain'), { target: { value: 'example.org' } });
    fireEvent.submit(screen.getByRole('button', { name: 'Connect' }).closest('form')!);

    expect(
      await screen.findByText('Review the choice above, then press Connect again.')
    ).toBeInTheDocument();
    expect(state.connect.mutateAsync).not.toHaveBeenCalled();
  });

  it('connects at once when the lookup agrees with the chosen capability', async () => {
    state.connect.mutateAsync.mockResolvedValue(pendingDomain());
    renderPage();

    fireEvent.change(screen.getByLabelText('Domain'), { target: { value: 'mail.example.org' } });
    fireEvent.submit(screen.getByRole('button', { name: 'Connect' }).closest('form')!);

    await waitFor(() =>
      expect(state.connect.mutateAsync).toHaveBeenCalledWith({
        name: 'mail.example.org',
        capability: 'send_receive',
      })
    );
  });

  it('keeps an explicit capability choice over the lookup', async () => {
    state.preview.mutateAsync.mockImplementation(async (name: string) => ({
      name,
      apex: name,
      existing_mx: ['mxbiz1.qq.com'],
      suggested_capability: 'send_only',
    }));
    state.connect.mutateAsync.mockResolvedValue(pendingDomain());
    renderPage();

    fireEvent.change(screen.getByLabelText('Domain'), { target: { value: 'example.org' } });
    fireEvent.click(screen.getByRole('radio', { name: /Send and receive/u }));
    fireEvent.submit(screen.getByRole('button', { name: 'Connect' }).closest('form')!);

    await waitFor(() =>
      expect(state.connect.mutateAsync).toHaveBeenCalledWith({
        name: 'example.org',
        capability: 'send_receive',
      })
    );
  });

  it('drops a suggested capability when the name changes', async () => {
    state.preview.mutateAsync.mockImplementation(async (name: string) => ({
      name,
      apex: 'example.org',
      existing_mx: name === 'example.org' ? ['mxbiz1.qq.com'] : [],
      suggested_capability: name === 'example.org' ? 'send_only' : 'send_receive',
    }));
    renderPage();
    const input = screen.getByLabelText('Domain');

    fireEvent.change(input, { target: { value: 'example.org' } });
    fireEvent.blur(input);
    expect(await screen.findByRole('radio', { name: /Send only/u })).toBeChecked();

    fireEvent.change(input, { target: { value: 'mail.example.org' } });
    expect(screen.getByRole('radio', { name: /Send and receive/u })).toBeChecked();
    expect(screen.queryByTestId('mail-domain-subdomain-hint')).toBeNull();

    // A capability the user picked stays when the name changes.
    fireEvent.click(screen.getByRole('radio', { name: /Send only/u }));
    fireEvent.change(input, { target: { value: 'news.example.org' } });
    expect(screen.getByRole('radio', { name: /Send only/u })).toBeChecked();
  });

  it('shows nameservers and opens matching registrar guide', () => {
    state.mailDomains.data = [zonePendingDomain()];
    renderPage();

    const card = screen.getByRole('article', { name: 'example.org' });
    const servers = within(card).getByRole('list', { name: 'Cloudflare name servers' });
    expect(within(servers).getByText('ada.ns.cloudflare.com')).toBeInTheDocument();
    expect(within(servers).getByText('bob.ns.cloudflare.com')).toBeInTheDocument();
    expect(
      within(servers).getByRole('button', { name: 'Copy ada.ns.cloudflare.com' })
    ).toBeInTheDocument();

    const guide = (name: string) =>
      within(card).getByText(name, { selector: 'summary' }).closest('details');
    expect(guide('Alibaba Cloud')).toHaveAttribute('open');
    for (const name of ['Tencent Cloud DNSPod', 'Huawei Cloud', 'GoDaddy', 'Namecheap']) {
      expect(guide(name)).not.toHaveAttribute('open');
    }

    const imported = within(card).getByRole('table', { name: 'Imported records' });
    expect(within(imported).getByText('192.0.2.10')).toBeInTheDocument();
    // Links keep the organization the user works in.
    expect(within(card).getByRole('link', { name: 'Review records' })).toHaveAttribute(
      'href',
      '/console/admin/domains/9/dns?org=1'
    );
    expect(within(card).getByRole('link', { name: 'DNS records' })).toHaveAttribute(
      'href',
      '/console/admin/domains/9/dns?org=1'
    );
    // A pending zone has nothing to set up yet.
    expect(within(card).queryByRole('button', { name: 'Set up again' })).toBeNull();
  });

  it('check now shows rate-limit notice', async () => {
    state.mailDomains.data = [zonePendingDomain()];
    state.refresh.mutateAsync.mockResolvedValue({
      ...zonePendingDomain(),
      zone_check_limited: true,
    });
    renderPage();
    const card = screen.getByRole('article', { name: 'example.org' });

    fireEvent.click(within(card).getByRole('button', { name: 'Check now' }));

    await waitFor(() =>
      expect(state.refresh.mutateAsync).toHaveBeenCalledWith({
        domain: zonePendingDomain(),
        action: 'checks',
      })
    );
    expect(
      await within(card).findByText('Cloudflare limits re-checks; try again in a few minutes.')
    ).toBeInTheDocument();
  });

  it('receiving dialog lists current MX', async () => {
    state.mailDomains.data = [verifiedSendOnlyDomain()];
    state.preview.mutateAsync.mockResolvedValue({
      name: 'example.org',
      apex: 'example.org',
      existing_mx: ['mxbiz1.qq.com', 'mxbiz2.qq.com'],
      suggested_capability: 'send_only',
    });
    state.receiving.mutateAsync.mockResolvedValue({
      ...verifiedSendOnlyDomain(),
      capability: 'send_receive',
      setting_up: true,
    });
    renderPage();
    const card = screen.getByRole('article', { name: 'example.org' });
    expect(within(card).getByRole('list', { name: 'Test your domain' })).toBeInTheDocument();

    fireEvent.click(within(card).getByRole('button', { name: 'Start receiving in Zmail' }));
    const dialog = await screen.findByRole('dialog');
    await waitFor(() => expect(state.preview.mutateAsync).toHaveBeenCalledWith('example.org'));
    expect(await within(dialog).findByText('mxbiz1.qq.com')).toBeInTheDocument();
    expect(within(dialog).getByText('mxbiz2.qq.com')).toBeInTheDocument();

    fireEvent.click(within(dialog).getByRole('button', { name: 'Start receiving' }));
    await waitFor(() => expect(state.receiving.mutateAsync).toHaveBeenCalledWith(7));
  });

  it('still-delegated removal shows original nameservers', async () => {
    state.mailDomains.data = [];
    state.zones.data = [
      { ...pendingZone(), status: 'active', activated_at: '2026-11-18T10:00:00Z' },
    ];
    state.deleteZone.mutateAsync.mockRejectedValue(
      new ApiError('delegated', ApiErrorCode.MAILBOX_ZONE_STILL_DELEGATED)
    );
    renderPage();

    const zone = screen.getByRole('article', { name: 'example.org' });
    expect(within(zone).getByRole('link', { name: 'DNS records' })).toHaveAttribute(
      'href',
      '/console/admin/domains/9/dns?org=1'
    );
    fireEvent.click(within(zone).getByRole('button', { name: 'Remove from Zmail' }));
    const dialog = await screen.findByRole('dialog');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Remove from Zmail' }));

    await waitFor(() => expect(state.deleteZone.mutateAsync).toHaveBeenCalledWith(9));
    const alert = await within(zone).findByRole('alert');
    expect(alert).toHaveTextContent('dns9.hichina.com');
    expect(alert).toHaveTextContent('dns10.hichina.com');
  });

  it('locked DNS rows have no actions', () => {
    state.dnsRecords.data = [
      ...importedRecords(),
      {
        id: 'mx1',
        type: 'MX',
        name: 'example.org',
        content: 'route1.mx.cloudflare.net',
        ttl: 1,
        priority: 12,
        proxied: false,
        locked: true,
        locked_reason: 'mail_receiving',
      },
    ];
    renderDnsPage();

    const table = screen.getByRole('table', { name: 'DNS records' });
    const locked = within(table)
      .getAllByRole('row')
      .find(row => row.getAttribute('data-record-id') === 'mx1')!;
    expect(within(locked).getByText(/Used by Zmail mail/u)).toBeInTheDocument();
    expect(within(locked).getByText(/receiving/iu)).toBeInTheDocument();
    expect(within(locked).queryByRole('button')).toBeNull();

    const open = within(table)
      .getAllByRole('row')
      .find(row => row.getAttribute('data-record-id') === 'a1')!;
    expect(within(open).getByRole('button', { name: 'Edit A example.org' })).toBeInTheDocument();
    expect(within(open).getByRole('button', { name: 'Delete A example.org' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Add record' })).toBeInTheDocument();
  });

  it('links back to the domains of the same organization', () => {
    renderDnsPage();

    expect(screen.getByRole('link', { name: 'Back to domains' })).toHaveAttribute(
      'href',
      '/console/admin/domains?org=1'
    );
  });

  it('member cannot add DNS records', () => {
    state.organizations.data = page([organization('member')]);
    renderDnsPage();

    expect(screen.getByRole('table', { name: 'DNS records' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Add record' })).toBeNull();
    expect(screen.queryByRole('button', { name: /^Edit / })).toBeNull();
    expect(screen.queryByRole('button', { name: /^Delete / })).toBeNull();
    expect(screen.getByText('Only owners and admins can change DNS records.')).toBeInTheDocument();
  });

  it('shows the reason Cloudflare rejected a record in the dialog', async () => {
    state.createRecord.mutateAsync.mockRejectedValue(
      new ApiError(
        'Content for A record is invalid.',
        ApiErrorCode.MAILBOX_DNS_RECORD_REJECTED,
        422
      )
    );
    renderDnsPage();

    fireEvent.click(screen.getByRole('button', { name: 'Add record' }));
    const dialog = await screen.findByRole('dialog');
    fireEvent.change(within(dialog).getByLabelText('Name'), { target: { value: 'api' } });
    fireEvent.change(within(dialog).getByLabelText('IPv4 address'), {
      target: { value: '192.0.2.5' },
    });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Save' }));

    await waitFor(() =>
      expect(state.createRecord.mutateAsync).toHaveBeenCalledWith({
        type: 'A',
        name: 'api',
        content: '192.0.2.5',
        ttl: 1,
        proxied: false,
      })
    );
    expect(await within(dialog).findByRole('alert')).toHaveTextContent(
      'Cloudflare refused the record: Content for A record is invalid.'
    );
  });

  it('checks the TTL and the MX priority before saving', async () => {
    renderDnsPage();

    fireEvent.click(screen.getByRole('button', { name: 'Add record' }));
    const dialog = await screen.findByRole('dialog');
    fireEvent.change(within(dialog).getByLabelText('Type'), { target: { value: 'MX' } });
    fireEvent.change(within(dialog).getByLabelText('Name'), { target: { value: '@' } });
    fireEvent.change(within(dialog).getByLabelText('Mail server'), {
      target: { value: 'mx.example.net' },
    });
    fireEvent.change(within(dialog).getByLabelText('Priority'), { target: { value: '' } });
    fireEvent.change(within(dialog).getByLabelText('TTL (seconds)'), {
      target: { value: 'soon' },
    });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Save' }));

    expect(within(dialog).getByLabelText('Priority')).toHaveAttribute('aria-invalid', 'true');
    expect(within(dialog).getByLabelText('TTL (seconds)')).toHaveAttribute('aria-invalid', 'true');
    expect(within(dialog).getByText('Enter a whole number from 0 to 65535.')).toBeInTheDocument();
    expect(
      within(dialog).getByText('Use a whole number from 60 to 86400, or leave it empty.')
    ).toBeInTheDocument();
    expect(state.createRecord.mutateAsync).not.toHaveBeenCalled();

    fireEvent.change(within(dialog).getByLabelText('Priority'), { target: { value: '70000' } });
    fireEvent.change(within(dialog).getByLabelText('TTL (seconds)'), { target: { value: '300' } });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Save' }));
    expect(within(dialog).getByLabelText('Priority')).toHaveAttribute('aria-invalid', 'true');
    expect(within(dialog).getByLabelText('TTL (seconds)')).not.toHaveAttribute('aria-invalid');
    expect(state.createRecord.mutateAsync).not.toHaveBeenCalled();

    fireEvent.change(within(dialog).getByLabelText('Priority'), { target: { value: '5' } });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Save' }));
    await waitFor(() =>
      expect(state.createRecord.mutateAsync).toHaveBeenCalledWith({
        type: 'MX',
        name: '@',
        content: 'mx.example.net',
        ttl: 300,
        priority: 5,
        proxied: false,
      })
    );
  });

  it('explains that an MX at a receiving domain can only be deleted', async () => {
    state.dnsRecords.data = [
      {
        id: 'mx9',
        type: 'MX',
        name: 'example.org',
        content: 'mxbiz1.qq.com',
        ttl: 1,
        priority: 5,
        proxied: false,
        locked: false,
      },
    ];
    state.updateRecord.mutateAsync.mockRejectedValue(
      new ApiError('locked', ApiErrorCode.MAILBOX_DNS_RECORD_LOCKED, 409)
    );
    renderDnsPage();

    fireEvent.click(screen.getByRole('button', { name: 'Edit MX example.org' }));
    const dialog = await screen.findByRole('dialog');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Save' }));

    expect(await within(dialog).findByRole('alert')).toHaveTextContent(
      /can only be deleted, not edited/u
    );
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
    <QueryClientProvider
      client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}
    >
      <NextIntlClientProvider locale="en-US" messages={messages as unknown as AbstractIntlMessages}>
        <MailDomainStatus />
      </NextIntlClientProvider>
    </QueryClientProvider>
  );
}

function renderDnsPage() {
  return render(
    <NextIntlClientProvider locale="en-US" messages={messages as unknown as AbstractIntlMessages}>
      <MailDnsRecordsPage zoneId={9} />
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
    daily_broadcast_limit: 200,
    daily_transactional_limit: 200,
    transactional_enabled: true,
    transactional_recipient_limit_per_minute: 3,
    transactional_key_limit_per_second: 20,
    simulator_domain: 'test.zmail.im',
    broadcasts_enabled: true,
    custom_domains_enabled: customDomainsEnabled,
  };
}

function pendingDomain() {
  return {
    id: 4,
    name: 'news.example.org',
    zone_name: 'example.org',
    capability: 'send_receive',
    zone: null,
    status: 'pending',
    setting_up: false,
    zone_check_limited: false,
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

function pendingZone() {
  return {
    id: 9,
    name: 'example.org',
    status: 'pending',
    name_servers: ['ada.ns.cloudflare.com', 'bob.ns.cloudflare.com'],
    original_name_servers: ['dns9.hichina.com', 'dns10.hichina.com'],
    activated_at: null,
    last_checked_at: null,
    created_at: '2026-11-17T09:59:00Z',
  };
}

function zonePendingDomain() {
  return {
    ...pendingDomain(),
    id: 7,
    name: 'example.org',
    zone: pendingZone(),
    records: [],
    last_error: undefined,
  };
}

function verifiedSendOnlyDomain() {
  return {
    ...pendingDomain(),
    id: 7,
    name: 'example.org',
    capability: 'send_only',
    zone: { ...pendingZone(), status: 'active', activated_at: '2026-11-18T10:00:00Z' },
    status: 'verified',
    records: [],
    last_error: undefined,
    verified_at: '2026-11-18T10:05:00Z',
  };
}

function importedRecords() {
  return [
    {
      id: 'a1',
      type: 'A',
      name: 'example.org',
      content: '192.0.2.10',
      ttl: 1,
      proxied: false,
      locked: false,
    },
    {
      id: 'c1',
      type: 'CNAME',
      name: 'www.example.org',
      content: 'example.com',
      ttl: 1,
      proxied: false,
      locked: false,
    },
  ];
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
