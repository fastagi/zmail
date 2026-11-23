import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { NextIntlClientProvider, type AbstractIntlMessages } from 'next-intl';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { preselectedDomain } from '@/features/mailbox/components/create-mailbox-form';
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
    createMailbox: mutation(),
    setDefault: mutation(),
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
vi.mock('@/features/mailbox/components/mailbox-people', async importOriginal => ({
  ...(await importOriginal<typeof import('@/features/mailbox/components/mailbox-people')>()),
  useOrganizationPeople: () => ({ items: [], status: 'ready', refetch: vi.fn() }),
}));
vi.mock('@/features/mailbox/hooks/use-mailbox', () => ({
  useMailboxDomains: () => state.domains,
  useMailboxes: () => state.mailboxes,
  useCreateMailbox: () => state.createMailbox,
  useMailDomains: (_organizationId: number, enabled: boolean) => {
    state.mailDomainsEnabled.push(enabled);
    return state.mailDomains;
  },
  useConnectMailDomain: () => state.connect,
  useRefreshMailDomain: () => state.refresh,
  useRemoveMailDomain: () => state.remove,
  usePreviewMailDomain: () => state.preview,
  useEnableReceiving: () => state.receiving,
  useSetDefaultMailDomain: () => state.setDefault,
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
      state.setDefault,
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
      within(card).getByText(/cloudflare enable email routing failed: Authentication error/u)
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

  it('explains a failed setup step in plain words and keeps the provider text as details', () => {
    state.mailDomains.data = [
      {
        ...pendingDomain(),
        last_error: 'cloudflare enable email routing failed: Authentication error',
        last_error_code: 'domain.permission_denied',
      },
    ];
    renderPage();

    const card = screen.getByRole('article', { name: 'news.example.org' });
    const alert = within(card).getByTestId('mail-domain-last-error');
    expect(alert).toHaveTextContent('Cloudflare did not allow this step');
    expect(alert).toHaveTextContent(/DNS and Email Routing edit permissions/u);
    const details = within(alert).getByText('Technical details');
    expect(details.closest('details')).not.toHaveAttribute('open');
    expect(within(alert).getByText(/Authentication error/u)).toBeInTheDocument();
  });

  it('falls back to a general explanation for a code it does not know', () => {
    state.mailDomains.data = [
      { ...pendingDomain(), last_error: 'something new', last_error_code: 'domain.brand_new' },
    ];
    renderPage();

    const alert = within(screen.getByRole('article', { name: 'news.example.org' })).getByTestId(
      'mail-domain-last-error'
    );
    expect(alert).toHaveTextContent('A setup step did not finish');
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
      /writing the records for receiving, sending and DMARC/u
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

  it('puts the token-free way first and folds the own-Cloudflare-account way (#130)', async () => {
    state.mailDomains.data = [];
    renderPage();

    const connect = await screen.findByRole('heading', { name: 'Connect your domain' });
    expect(screen.getByText(/Works for most domains and needs no token/u)).toBeInTheDocument();
    const folded = screen
      .getByText(/DNS already in your own Cloudflare account\? Connect with your account/u, {
        selector: 'summary',
      })
      .closest('details');
    expect(folded).not.toHaveAttribute('open');
    // The token-free form comes first in the page.
    expect(
      connect.compareDocumentPosition(folded!) & Node.DOCUMENT_POSITION_FOLLOWING
    ).toBeTruthy();
  });

  it('explains in plain words what each record does', () => {
    renderPage();

    const card = screen.getByRole('article', { name: 'news.example.org' });
    const help = within(card).getByRole('list', { name: 'What these records do' });
    expect(
      within(help).getByText('Receiving (MX): mail sent to @news.example.org arrives in Zmail.')
    ).toBeInTheDocument();
    expect(within(help).getByText(/proves mail really comes from your domain/u)).toBeVisible();
    // Only the purposes this domain has records for: it has no DMARC record.
    expect(within(help).queryByText(/pretends to be from your domain/u)).toBeNull();
  });

  it('announces when the name servers go live and when the domain is ready', () => {
    state.mailDomains.data = [zonePendingDomain()];
    const view = renderPage();
    expect(toast.success).not.toHaveBeenCalled();

    state.mailDomains.data = [
      { ...zonePendingDomain(), zone: { ...pendingZone(), status: 'active' }, setting_up: true },
    ];
    view.rerender(pageTree());
    expect(toast.message).toHaveBeenCalledWith(
      'The name servers of example.org are live. Zmail is setting up mail now.'
    );

    state.mailDomains.data = [
      {
        ...zonePendingDomain(),
        zone: { ...pendingZone(), status: 'active' },
        status: 'verified',
      },
    ];
    view.rerender(pageTree());
    expect(toast.success).toHaveBeenCalledWith(
      'example.org is ready. You can create mailboxes on it.',
      expect.objectContaining({ action: expect.objectContaining({ label: 'Create mailbox' }) })
    );
  });

  it("names the server's Cloudflare account, not a token, when a tokenless connect is refused (#130)", async () => {
    state.connect.mutateAsync.mockRejectedValue(
      new ApiError('refused', ApiErrorCode.MAILBOX_DOMAIN_PROVIDER_FAILED, 502)
    );
    renderPage();

    fireEvent.change(screen.getByLabelText('Domain'), { target: { value: 'devin-qa.example' } });
    fireEvent.click(screen.getByRole('button', { name: 'Connect' }));

    const alert = await screen.findByText(/no token of yours was used/u);
    expect(alert).toHaveAttribute('role', 'alert');
    expect(alert).toHaveTextContent(/reserved names such as \.example/u);
    expect(screen.queryByText(/Check that the API token/u)).toBeNull();
  });

  it('says the pasted token was refused when its zones cannot be listed (#130)', async () => {
    const post = vi
      .spyOn(request, 'post')
      .mockRejectedValue(
        new ApiError('Could not list', ApiErrorCode.COMMON_VALIDATION_FAILED, 422)
      );
    renderPage();

    fireEvent.change(await screen.findByLabelText('Cloudflare API token'), {
      target: { value: 'cf-token-value' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Find accessible zones' }));

    expect(
      await screen.findByText(/Cloudflare did not accept the token you entered/u)
    ).toBeInTheDocument();
    expect(screen.queryByText('Could not list')).toBeNull();
    post.mockRestore();
  });

  it('explains a domain outside the connected zone and a token without DNS access (#130)', async () => {
    vi.spyOn(request, 'get').mockResolvedValue({
      connection: { id: 'c1', zone_name: 'example.org', worker: 'zmail-c1' },
    });
    const post = vi
      .spyOn(request, 'post')
      .mockRejectedValueOnce(new ApiError('zone', ApiErrorCode.MAILBOX_DOMAIN_ZONE_NOT_FOUND, 422))
      .mockRejectedValueOnce(
        new ApiError('provider', ApiErrorCode.MAILBOX_DOMAIN_PROVIDER_FAILED, 502)
      );
    renderPage();

    fireEvent.change(await screen.findByLabelText('Mail domain'), {
      target: { value: 'other.net' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Set up domain' }));
    expect(
      await screen.findByText('Use example.org or a subdomain of it, such as mail.example.org.')
    ).toBeInTheDocument();

    fireEvent.change(screen.getByLabelText('Mail domain'), {
      target: { value: 'mail.example.org' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Set up domain' }));
    expect(
      await screen.findByText(/Give the token DNS Edit and Zone Read on example.org/u)
    ).toBeInTheDocument();
    post.mockRestore();
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
    for (const name of [
      'Tencent Cloud DNSPod',
      'Huawei Cloud',
      'GoDaddy',
      'Namecheap',
      'Cloudflare',
      'Other registrars',
    ]) {
      expect(guide(name)).not.toHaveAttribute('open');
    }
    const next = within(card).getByRole('list', { name: 'What happens next' });
    expect(within(next).getByText(/at most 48/u)).toBeInTheDocument();
    expect(within(next).getByText(/checks every 10 minutes/u)).toBeInTheDocument();

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

  it('shows where a pending domain stands in its setup', () => {
    renderPage();
    const steps = screen.getByRole('list', { name: 'Setup progress' });
    const current = within(steps).getByText('Verify DNS records').closest('li');
    expect(current).toHaveAttribute('aria-current', 'step');
    expect(within(steps).getByText('Connect the domain').closest('li')).toHaveAttribute(
      'data-state',
      'done'
    );
    expect(screen.queryByRole('button', { name: /Create a mailbox @/ })).not.toBeInTheDocument();
  });

  it('opens the mailbox form on a verified domain with that domain chosen', async () => {
    const verified = { ...verifiedSendOnlyDomain(), capability: 'send_receive' };
    state.mailDomains.data = [verified];
    state.domains.data = [
      sharedDomain(true),
      { ...sharedDomain(true), domain: 'example.org', kind: 'organization' },
    ];
    renderPage();
    fireEvent.click(screen.getByRole('button', { name: 'Show DNS records' }));
    const steps = screen.getByRole('list', { name: 'Setup progress' });
    expect(within(steps).getByText('Create a mailbox').closest('li')).toHaveAttribute(
      'aria-current',
      'step'
    );
    fireEvent.click(screen.getByRole('button', { name: 'Create a mailbox @example.org' }));
    const dialog = await screen.findByRole('dialog', { name: 'New mailbox @example.org' });
    expect(within(dialog).getByRole('combobox', { name: 'Domain' })).toHaveValue('example.org');
  });

  it('reloads the mailbox domains when the verified domain is not among them yet', async () => {
    state.mailDomains.data = [{ ...verifiedSendOnlyDomain(), capability: 'send_receive' }];
    state.domains.data = [sharedDomain(true)];
    state.domains.refetch.mockClear();
    renderPage();
    fireEvent.click(screen.getByRole('button', { name: 'Create a mailbox @example.org' }));
    const dialog = await screen.findByRole('dialog', { name: 'New mailbox @example.org' });
    await waitFor(() => expect(state.domains.refetch).toHaveBeenCalled());
    expect(within(dialog).queryByRole('combobox', { name: 'Domain' })).not.toBeInTheDocument();
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
    fireEvent.click(within(card).getByRole('button', { name: 'Show DNS records' }));
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

  it('folds a verified domain into one row and shows its DNS records on request', () => {
    state.mailDomains.data = [
      verifiedDomain(11, 'fast-ui.com', { mailbox_count: 3, default: true }),
      verifiedDomain(12, 'shu.ie', { capability: 'send_only', mailbox_count: 1 }),
    ];
    renderPage();

    const card = screen.getByRole('article', { name: 'fast-ui.com' });
    expect(within(card).getByTestId('mail-domain-status')).toHaveTextContent('Verified');
    expect(within(card).getByText('Send and receive')).toBeInTheDocument();
    expect(within(card).getByText('3 mailboxes')).toBeInTheDocument();
    expect(within(card).getByRole('button', { name: 'Check DNS' })).toBeInTheDocument();
    expect(
      within(card).getByRole('button', { name: 'Create a mailbox @fast-ui.com' })
    ).toBeInTheDocument();
    expect(within(card).getByRole('button', { name: 'Remove' })).toBeInTheDocument();
    // The record table and the test checklist stay folded away.
    expect(within(card).queryByRole('table')).not.toBeInTheDocument();
    expect(within(card).queryByText('Test your domain')).not.toBeInTheDocument();
    const other = screen.getByRole('article', { name: 'shu.ie' });
    expect(within(other).getByText('Send only')).toBeInTheDocument();
    expect(within(other).getByText('1 mailbox')).toBeInTheDocument();

    const toggle = within(card).getByRole('button', { name: 'Show DNS records' });
    expect(toggle).toHaveAttribute('aria-expanded', 'false');
    fireEvent.click(toggle);
    expect(within(card).getByRole('button', { name: 'Hide DNS records' })).toHaveAttribute(
      'aria-expanded',
      'true'
    );
    expect(within(card).getAllByRole('row').length).toBeGreaterThan(1);
    expect(within(card).getByText('Test your domain')).toBeInTheDocument();
    // Each domain remembers its own state.
    expect(within(other).queryByRole('table')).not.toBeInTheDocument();
  });

  it('counts the connected domains and leads to the form for another one', () => {
    state.mailDomains.data = [
      verifiedDomain(11, 'fast-ui.com', { default: true }),
      verifiedDomain(12, 'shu.ie'),
    ];
    const scrollIntoView = vi.fn();
    Element.prototype.scrollIntoView = scrollIntoView;
    renderPage();

    expect(screen.getByText('2 of 10 domains connected')).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Connect another domain' })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Add domain' }));
    expect(scrollIntoView).toHaveBeenCalled();
    expect(screen.getByRole('textbox', { name: 'Domain' })).toHaveFocus();
  });

  it('names the first domain plainly when none is connected yet', () => {
    state.mailDomains.data = [];
    renderPage();

    expect(screen.getByText('0 of 10 domains connected')).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Connect your domain' })).toBeInTheDocument();
  });

  it('stops adding domains at the limit and says why', () => {
    state.mailDomains.data = Array.from({ length: 10 }, (_, index) =>
      verifiedDomain(20 + index, `d${index}.example.org`)
    );
    renderPage();

    expect(screen.getByText('10 of 10 domains connected')).toBeInTheDocument();
    const add = screen.getByRole('button', { name: 'Add domain' });
    expect(add).toBeDisabled();
    expect(add).toHaveAccessibleDescription(
      'An organization can connect up to 10 domains. Remove one to add another.'
    );
  });

  it('marks the default domain and lets an owner choose another', async () => {
    state.mailDomains.data = [
      verifiedDomain(11, 'fast-ui.com', { default: true }),
      verifiedDomain(12, 'shu.ie'),
    ];
    state.setDefault.mutateAsync.mockResolvedValue(verifiedDomain(12, 'shu.ie', { default: true }));
    renderPage();

    const current = screen.getByRole('article', { name: 'fast-ui.com' });
    expect(within(current).getByText('Default')).toBeInTheDocument();
    expect(within(current).queryByRole('button', { name: 'Make default' })).not.toBeInTheDocument();
    const other = screen.getByRole('article', { name: 'shu.ie' });
    fireEvent.click(within(other).getByRole('button', { name: 'Make default' }));

    await waitFor(() => expect(state.setDefault.mutateAsync).toHaveBeenCalledWith(12));
    expect(toast.success).toHaveBeenCalledWith('New mailboxes now use @shu.ie by default');
  });

  it('offers no default choice to members or for a pending domain', () => {
    state.mailDomains.data = [verifiedDomain(12, 'shu.ie'), pendingDomain()];
    const owner = renderPage();
    const pending = screen.getByRole('article', { name: 'news.example.org' });
    expect(within(pending).queryByRole('button', { name: 'Make default' })).not.toBeInTheDocument();
    // A pending domain stays open with its records.
    expect(within(pending).getAllByRole('row').length).toBeGreaterThan(1);
    owner.unmount();

    state.organizations.data = page([organization('member')]);
    renderPage();
    expect(screen.queryByRole('button', { name: 'Make default' })).not.toBeInTheDocument();
  });

  it('explains how to turn the feature on and loads nothing while it is off', () => {
    state.domains.data = [sharedDomain(false)];
    renderPage();

    expect(screen.getByText(/Own domains are not available on this service yet/u)).toBeVisible();
    // Server settings are the operator's business, never the customer's.
    expect(screen.queryByText(/MAILBOX_/u)).toBeNull();
    expect(screen.queryByRole('article')).toBeNull();
    expect(state.mailDomainsEnabled.every(enabled => !enabled)).toBe(true);
  });
});

describe('new mailbox domain', () => {
  const shared = { ...sharedDomain(true), default: false };
  const own = (domain: string, isDefault: boolean) =>
    ({ ...shared, domain, kind: 'organization', default: isDefault }) as const;

  it('preselects the default domain the API marks', () => {
    const domains = [shared, own('fast-ui.com', false), own('shu.ie', true)] as never[];
    expect(preselectedDomain(shared as never, domains)).toBe('shu.ie');
  });

  it('keeps a domain the caller names and falls back to the shared one', () => {
    const domains = [shared, own('fast-ui.com', false), own('shu.ie', true)] as never[];
    expect(preselectedDomain(shared as never, domains, 'fast-ui.com')).toBe('fast-ui.com');
    expect(preselectedDomain(shared as never, [shared] as never[])).toBe('mail.example.com');
  });
});

const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });

function pageTree() {
  return (
    <QueryClientProvider client={queryClient}>
      <NextIntlClientProvider locale="en-US" messages={messages as unknown as AbstractIntlMessages}>
        <MailDomainStatus />
      </NextIntlClientProvider>
    </QueryClientProvider>
  );
}

function renderPage() {
  queryClient.clear();
  return render(pageTree());
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

function verifiedDomain(id: number, name: string, extra: Record<string, unknown> = {}) {
  return {
    ...pendingDomain(),
    id,
    name,
    zone_name: name,
    status: 'verified',
    last_error: undefined,
    verified_at: '2026-11-18T10:05:00Z',
    records: pendingDomain().records.map(record => ({
      ...record,
      name: record.name.replace('news.example.org', name),
      status: 'ok',
      found: undefined,
    })),
    default: false,
    mailbox_count: 0,
    ...extra,
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
