import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { NextIntlClientProvider, type AbstractIntlMessages } from 'next-intl';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { LimitsTab } from '@/features/developer/components/limits-tab';
import type { MailboxDomain } from '@/features/mailbox/types';
import { ApiErrorCode } from '@/http/codes';
import { ApiError } from '@/http/request';
import { messages } from '@/i18n/modules';

const developer = vi.hoisted(() => ({
  sendLanes: vi.fn(),
  suppressions: vi.fn(),
  removeSuppression: vi.fn(),
}));

vi.mock('@/features/developer/services/developer-service', () => ({
  developerService: developer,
}));

describe('developer limits and protection tab', () => {
  beforeEach(() => {
    developer.sendLanes.mockReset().mockResolvedValue(lanes());
    developer.suppressions.mockReset().mockResolvedValue({
      items: [
        suppression({ id: 3, email: 'full-box@example.org' }),
        suppression({ id: 2, email: 'complainer@example.net', reason: 'complained' }),
      ],
      meta: { next_cursor: null },
    });
    developer.removeSuppression.mockReset().mockResolvedValue(undefined);
  });

  it('admin sees used and limit with a progress bar per lane', async () => {
    renderTab({ canManage: true });

    const list = await screen.findByRole('list', { name: 'Send lanes' });
    const items = within(list).getAllByRole('listitem');
    expect(items).toHaveLength(3);
    await waitFor(() => expect(within(items[0]).getByText('37 / 500')).toBeInTheDocument());
    expect(within(items[1]).getByText('500 / 500')).toBeInTheDocument();
    expect(within(items[1]).getByText('412 recipients waiting')).toBeInTheDocument();
    expect(within(items[2]).getByText('12 / 200')).toBeInTheDocument();

    const bars = within(list).getAllByRole('progressbar');
    expect(bars).toHaveLength(3);
    expect(bars[0]).toHaveAttribute('aria-valuenow', '37');
    expect(bars[0]).toHaveAttribute('aria-valuemax', '500');
    expect(screen.queryByText('Usage is visible to admins and developers.')).not.toBeInTheDocument();
  });

  it('member sees limits but no usage', async () => {
    renderTab({ canManage: false });

    const list = screen.getByRole('list', { name: 'Send lanes' });
    const items = within(list).getAllByRole('listitem');
    expect(items).toHaveLength(3);
    // The limits come from mailbox-domains: transactional 500, broadcast 400, personal 200.
    expect(within(items[0]).getByText('500 per 24 hours')).toBeInTheDocument();
    expect(within(items[1]).getByText('400 per 24 hours')).toBeInTheDocument();
    expect(within(items[2]).getByText('200 per 24 hours')).toBeInTheDocument();
    expect(within(list).queryByRole('progressbar')).not.toBeInTheDocument();
    expect(screen.getByText('Usage is visible to admins and developers.')).toBeInTheDocument();

    // Neither usage nor the suppression list is requested for a member.
    expect(developer.sendLanes).not.toHaveBeenCalled();
    expect(developer.suppressions).not.toHaveBeenCalled();
    expect(screen.queryByRole('table', { name: 'Suppression list' })).not.toBeInTheDocument();
  });

  it('a developer member reads usage and manages the suppression list', async () => {
    renderTab({ canManage: false, developer: true });

    const list = await screen.findByRole('list', { name: 'Send lanes' });
    await waitFor(() =>
      expect(within(within(list).getAllByRole('listitem')[0]!).getByText('37 / 500')).toBeInTheDocument()
    );
    expect(developer.sendLanes).toHaveBeenCalled();
    expect(developer.suppressions).toHaveBeenCalled();
  });

  it('protection rules show the configured values', () => {
    renderTab({ canManage: false });

    const rules = screen.getByRole('list', { name: 'Protection rules' });
    const items = within(rules).getAllByRole('listitem');
    expect(items.map(item => item.querySelector('[data-rule-value]')?.textContent)).toEqual([
      '7 / minute',
      '45 / second',
      '24 hours',
      'Automatic',
      '••••••',
    ]);
    expect(within(items[0]).getByText('Same recipient')).toBeInTheDocument();
    expect(within(items[1]).getByText('One API key')).toBeInTheDocument();
  });

  it('admin removes a suppression after confirming', async () => {
    renderTab({ canManage: true });

    const table = await screen.findByRole('table', { name: 'Suppression list' });
    const row = within(table).getByText('full-box@example.org').closest('tr');
    expect(row).not.toBeNull();
    fireEvent.click(
      within(row as HTMLElement).getByRole('button', { name: 'Remove full-box@example.org' })
    );

    const dialog = await screen.findByRole('alertdialog');
    expect(within(dialog).getByText(/may bounce again/u)).toBeInTheDocument();
    expect(developer.removeSuppression).not.toHaveBeenCalled();

    developer.suppressions.mockResolvedValue({
      items: [suppression({ id: 2, email: 'complainer@example.net', reason: 'complained' })],
      meta: { next_cursor: null },
    });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Remove' }));

    await waitFor(() => expect(developer.removeSuppression).toHaveBeenCalledWith(1, 3));
    // The list is read again after the removal.
    await waitFor(() => expect(screen.queryByText('full-box@example.org')).not.toBeInTheDocument());
    expect(developer.suppressions.mock.calls.length).toBeGreaterThanOrEqual(2);
  });

  it('a failed removal offers to reload the list, not to retry the removal', async () => {
    developer.removeSuppression.mockRejectedValue(
      new ApiError('down', ApiErrorCode.COMMON_SERVICE_UNAVAILABLE, 503)
    );
    renderTab({ canManage: true });

    fireEvent.click(await screen.findByRole('button', { name: 'Remove full-box@example.org' }));
    const dialog = await screen.findByRole('alertdialog');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Remove' }));

    const reload = await screen.findByRole('button', { name: 'Reload list' });
    expect(screen.queryByRole('button', { name: 'Retry' })).toBeNull();
    const reads = developer.suppressions.mock.calls.length;
    fireEvent.click(reload);
    await waitFor(() => expect(developer.suppressions.mock.calls.length).toBeGreaterThan(reads));
    expect(developer.removeSuppression).toHaveBeenCalledTimes(1);
  });

  it('cancelling the confirmation removes nothing', async () => {
    renderTab({ canManage: true });

    fireEvent.click(await screen.findByRole('button', { name: 'Remove complainer@example.net' }));
    const dialog = await screen.findByRole('alertdialog');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }));
    await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument());
    expect(developer.removeSuppression).not.toHaveBeenCalled();
  });

  it('searches the suppression list by exact address', async () => {
    renderTab({ canManage: true });
    await screen.findByRole('table', { name: 'Suppression list' });

    fireEvent.change(screen.getByLabelText('Find an address'), {
      target: { value: '  Full-Box@Example.ORG ' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Find' }));

    await waitFor(() =>
      expect(developer.suppressions).toHaveBeenLastCalledWith(
        1,
        { email: 'full-box@example.org' },
        expect.anything()
      )
    );
  });

  it('shows the admin-required copy when usage is refused', async () => {
    developer.sendLanes.mockRejectedValue(
      new ApiError('denied', ApiErrorCode.PERMISSION_DENIED, 403)
    );
    renderTab({ canManage: true });

    expect(await screen.findByRole('alert')).toBeInTheDocument();
    // The limits stay readable from mailbox-domains.
    const list = screen.getByRole('list', { name: 'Send lanes' });
    expect(within(list).getAllByText(/per 24 hours/u)).toHaveLength(3);
  });
});

const allFeatures = {
  broadcasts: true,
  contacts: true,
  templates: true,
  api_keys: true,
  mail_clients: true,
  developer: false,
};

function renderTab({ canManage, developer = false }: { canManage: boolean; developer?: boolean }) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <NextIntlClientProvider locale="en-US" messages={messages as unknown as AbstractIntlMessages}>
        <LimitsTab
          organization={{
            id: 1,
            name: 'Acme',
            canManage,
            features: { ...allFeatures, developer },
            developerScope: developer ? { all_domains: true, domains: [] } : null,
          }}
          domains={[domain({ domain: 'mail.example.com', kind: 'shared' })]}
        />
      </NextIntlClientProvider>
    </QueryClientProvider>
  );
}

function lanes() {
  return {
    lanes: [
      { lane: 'transactional', limit: 500, used: 37, waiting: 0 },
      { lane: 'broadcast', limit: 500, used: 500, waiting: 412 },
      { lane: 'personal', limit: 200, used: 12, waiting: 0 },
    ],
    window_hours: 24,
    computed_at: '2026-11-23T08:00:00Z',
  };
}

function suppression(overrides: Record<string, unknown> = {}) {
  return {
    id: 1,
    email: 'old-bounce@example.com',
    reason: 'bounced',
    source: 'transactional',
    created_at: '2026-11-18T08:00:00Z',
    ...overrides,
  };
}

function domain(overrides: Partial<MailboxDomain> = {}): MailboxDomain {
  return {
    domain: 'mail.example.com',
    kind: 'shared',
    max_mailboxes: 10,
    provider: 'log',
    inbound_enabled: false,
    daily_send_limit: 200,
    daily_broadcast_limit: 400,
    daily_transactional_limit: 500,
    transactional_recipient_limit_per_minute: 7,
    transactional_key_limit_per_second: 45,
    simulator_domain: 'test.zmail.im',
    broadcasts_enabled: true,
    custom_domains_enabled: true,
    transactional_enabled: true,
    ...overrides,
  };
}
