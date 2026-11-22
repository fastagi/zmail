import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { NextIntlClientProvider, type AbstractIntlMessages } from 'next-intl';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { AdminShell } from '@/features/mailbox/components/admin/admin-shell';
import { ExternalConnectionsAdmin } from '@/features/mailbox/components/admin/external-connections-admin';
import type { ExternalMailDirectoryEntry } from '@/features/mailbox/external-mail-schemas';
import { ApiErrorCode } from '@/http/codes';
import { ApiError } from '@/http/request';
import { messages } from '@/i18n/modules';
import zhMailbox from '@/i18n/modules/mailbox/zh-Hans';
import zhNav from '@/i18n/modules/nav/zh-Hans';

const state = vi.hoisted(() => ({
  role: 'owner',
  pathname: '/console/admin/external-connections',
  notFound: vi.fn(),
}));
const service = vi.hoisted(() => ({ directory: vi.fn(), forceDisconnect: vi.fn() }));
const toast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn() }));

vi.mock('sonner', () => ({ toast }));
vi.mock('next/navigation', () => ({
  useRouter: () => ({ replace: vi.fn(), push: vi.fn() }),
  usePathname: () => state.pathname,
  useSearchParams: () => new URLSearchParams('org=1'),
  notFound: () => state.notFound(),
}));
vi.mock('@/features/organization/hooks/use-organizations', () => {
  const now = '2026-11-16T10:00:00Z';
  return {
    useOrganizations: () => ({
      data: {
        items: [
          { id: 1, name: 'Acme', slug: 'acme', role: state.role, created_at: now, updated_at: now },
        ],
        meta: { current_page: 1, per_page: 25, total: 1, last_page: 1, from: 1, to: 1 },
        links: { first: '', last: '', prev: null, next: null },
      },
      isPending: false,
      error: null,
      refetch: vi.fn(),
    }),
  };
});
vi.mock('@/features/mailbox/services/external-mail-service', async importOriginal => ({
  ...(await importOriginal<typeof import('@/features/mailbox/services/external-mail-service')>()),
  externalMailService: service,
}));

let queryClient: QueryClient;

beforeEach(() => {
  queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  state.role = 'owner';
  state.pathname = '/console/admin/external-connections';
  state.notFound.mockReset();
  service.directory.mockReset();
  service.forceDisconnect.mockReset();
  toast.success.mockReset();
  toast.error.mockReset();
});

describe('external connections admin page', () => {
  it('is reachable from the admin navigation by owners and admins only', () => {
    for (const role of ['owner', 'admin']) {
      state.role = role;
      const { unmount } = renderWith(<AdminShell>content</AdminShell>);
      const link = screen.getByRole('link', { name: 'External connections' });
      expect(link).toHaveAttribute('href', '/console/admin/external-connections?org=1');
      expect(link).toHaveAttribute('aria-current', 'page');
      unmount();
    }
    expect(state.notFound).not.toHaveBeenCalled();

    state.role = 'member';
    renderWith(<AdminShell>content</AdminShell>);
    expect(state.notFound).toHaveBeenCalled();
  });

  it('lists masked connections with a privacy note and never message data', async () => {
    service.directory.mockResolvedValue(page([entry({ id: 3 }), entry({ id: 2, owner_display_name: null, owner_user_id: null, status: 'revoked', last_synced_at: null })], null));
    renderWith(<ExternalConnectionsAdmin />);

    expect(
      await screen.findByText(
        'Addresses are masked. Message contents and message counts are never shown here: only the member who connected an account can read its mail.'
      )
    ).toBeInTheDocument();
    const table = await screen.findByRole('table');
    expect(within(table).getAllByRole('columnheader').map(cell => cell.textContent)).toEqual([
      'Account',
      'Provider',
      'Member',
      'Status',
      'Last synced',
      'Connected',
      'Actions',
    ]);
    const first = rowOf(3);
    expect(first).toHaveTextContent('st***@gmail.com');
    expect(first).toHaveTextContent('Gmail');
    expect(first).toHaveTextContent('stark');
    expect(first).toHaveTextContent('Syncing');
    const second = rowOf(2);
    expect(second).toHaveTextContent('Former member');
    expect(second).toHaveTextContent('Disconnected');
    expect(second).toHaveTextContent('Never');
    expect(within(second).queryByRole('button')).not.toBeInTheDocument();
    expect(service.directory).toHaveBeenCalledWith(1, undefined);
    // One page needs no pager.
    expect(screen.queryByRole('navigation', { name: 'Pages' })).not.toBeInTheDocument();
  });

  it('pages forward by cursor and back', async () => {
    service.directory.mockImplementation(async (_organizationId: number, cursor?: string) =>
      cursor === 'c2' ? page([entry({ id: 1 })], null) : page([entry({ id: 3 }), entry({ id: 2 })], 'c2')
    );
    renderWith(<ExternalConnectionsAdmin />);
    const pager = await screen.findByRole('navigation', { name: 'Pages' });
    const previous = within(pager).getByRole('button', { name: 'Previous' });
    const next = within(pager).getByRole('button', { name: 'Next' });
    expect(previous).toBeDisabled();
    await waitFor(() => expect(next).toBeEnabled());

    fireEvent.click(next);
    await waitFor(() => expect(service.directory).toHaveBeenLastCalledWith(1, 'c2'));
    await waitFor(() => expect(document.querySelector('[data-connection="1"]')).not.toBeNull());
    expect(document.querySelector('[data-connection="3"]')).toBeNull();
    expect(within(pager).getByRole('button', { name: 'Next' })).toBeDisabled();

    fireEvent.click(within(pager).getByRole('button', { name: 'Previous' }));
    await waitFor(() => expect(document.querySelector('[data-connection="3"]')).not.toBeNull());
  });

  it('disconnects after an inline confirmation that says the member can connect again', async () => {
    service.directory.mockResolvedValue(page([entry({ id: 3 })], null));
    service.forceDisconnect.mockResolvedValue(undefined);
    renderWith(<ExternalConnectionsAdmin />);
    await screen.findByRole('table');

    const trigger = within(rowOf(3)).getByRole('button', { name: 'Disconnect st***@gmail.com' });
    fireEvent.click(trigger);
    const confirm = screen.getByRole('group', { name: 'Disconnect st***@gmail.com for stark?' });
    expect(confirm).toHaveFocus();
    expect(confirm).toHaveAccessibleDescription(
      'Syncing stops now. The member keeps the mail already synced and can connect the account again. This action is recorded in the audit log.'
    );
    fireEvent.click(within(confirm).getByRole('button', { name: 'Cancel' }));
    expect(screen.queryByRole('group')).not.toBeInTheDocument();
    expect(service.forceDisconnect).not.toHaveBeenCalled();

    fireEvent.click(within(rowOf(3)).getByRole('button', { name: 'Disconnect st***@gmail.com' }));
    fireEvent.click(within(screen.getByRole('group')).getByRole('button', { name: 'Disconnect' }));
    await waitFor(() => expect(service.forceDisconnect).toHaveBeenCalledWith(1, 3));
    await waitFor(() =>
      expect(toast.success).toHaveBeenCalledWith('Disconnected st***@gmail.com')
    );
    // The directory is read again.
    await waitFor(() => expect(service.directory).toHaveBeenCalledTimes(2));
  });

  it('reports a failed disconnect inline and a vanished one as gone', async () => {
    service.directory.mockResolvedValue(page([entry({ id: 3 })], null));
    service.forceDisconnect.mockRejectedValueOnce(
      new ApiError('boom', ApiErrorCode.COMMON_INTERNAL, 500)
    );
    renderWith(<ExternalConnectionsAdmin />);
    await screen.findByRole('table');
    fireEvent.click(within(rowOf(3)).getByRole('button', { name: /Disconnect/u }));
    fireEvent.click(within(screen.getByRole('group')).getByRole('button', { name: 'Disconnect' }));
    expect(await within(screen.getByRole('group')).findByRole('alert')).toHaveTextContent(
      'The account could not be disconnected. Try again.'
    );

    service.forceDisconnect.mockRejectedValueOnce(
      new ApiError('gone', ApiErrorCode.MAILBOX_EXTERNAL_CONNECTION_NOT_FOUND, 404)
    );
    fireEvent.click(within(screen.getByRole('group')).getByRole('button', { name: 'Disconnect' }));
    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith('This connection no longer exists.')
    );
    expect(screen.queryByRole('group')).not.toBeInTheDocument();
  });

  it('shows the empty state and renders in Simplified Chinese', async () => {
    service.directory.mockResolvedValue(page([], null));
    renderWith(<ExternalConnectionsAdmin />, 'zh-Hans');
    expect(await screen.findByText('暂无外部连接')).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: '外部邮箱连接' })).toBeInTheDocument();
  });
});

function entry(overrides: Partial<ExternalMailDirectoryEntry>): ExternalMailDirectoryEntry {
  return {
    id: 1,
    provider: 'gmail',
    status: 'active',
    masked_account_address: 'st***@gmail.com',
    owner_user_id: 5,
    owner_display_name: 'stark',
    last_synced_at: '2026-11-20T08:00:00Z',
    created_at: '2026-11-19T08:00:00Z',
    ...overrides,
  };
}

function page(items: ExternalMailDirectoryEntry[], nextCursor: string | null) {
  return { items, meta: { per_page: 15, has_more: nextCursor !== null, next_cursor: nextCursor } };
}

function rowOf(id: number): HTMLElement {
  const row = document.querySelector(`[data-connection="${id}"]`);
  if (!(row instanceof HTMLElement)) throw new Error(`no row ${id}`);
  return row;
}

function renderWith(children: React.ReactNode, locale: 'en-US' | 'zh-Hans' = 'en-US') {
  const all = messages as unknown as Record<string, unknown>;
  const localized = locale === 'en-US' ? all : { ...all, mailbox: zhMailbox, nav: zhNav };
  return render(
    <QueryClientProvider client={queryClient}>
      <NextIntlClientProvider locale={locale} messages={localized as AbstractIntlMessages}>
        {children}
      </NextIntlClientProvider>
    </QueryClientProvider>
  );
}
