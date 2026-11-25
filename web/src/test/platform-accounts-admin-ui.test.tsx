import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { NextIntlClientProvider, type AbstractIntlMessages } from 'next-intl';
import { toast } from 'sonner';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { PlatformAccountsAdmin } from '@/features/mailbox/components/admin/platform-accounts-admin';
import { ManageRolesDialog } from '@/features/mailbox/components/manage-roles-dialog';
import { MailOrganizationBoundary } from '@/features/mailbox/components/mail-organization';
import type { MailOrganizationScope } from '@/features/mailbox/hooks/use-mail-organization';
import { ApiErrorCode } from '@/http/codes';
import { ApiError } from '@/http/request';
import { messages } from '@/i18n/modules';

/**
 * The platform account admin page with the real hooks and query cache; only the HTTP services and
 * the organization hooks are faked.
 */
const ledger = vi.hoisted(() => ({
  list: vi.fn(),
  create: vi.fn(),
  update: vi.fn(),
  remove: vi.fn(),
  events: vi.fn(),
  handOver: vi.fn(),
}));
const mail = vi.hoisted(() => ({
  list: vi.fn(),
  listRoles: vi.fn(),
}));

vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn(), message: vi.fn() } }));
vi.mock('next/navigation', () => ({
  useRouter: () => ({ replace: vi.fn() }),
  usePathname: () => '/console/admin/platform-accounts',
  useSearchParams: () => new URLSearchParams(),
}));
vi.mock('@/features/organization/hooks/use-organizations', () => {
  const now = '2026-11-16T10:00:00Z';
  const person = (userId: number, username: string) => ({
    id: userId,
    user_id: userId,
    username,
    role: 'member',
    joined_at: now,
    updated_at: now,
  });
  const meta = { current_page: 1, per_page: 25, total: 1, last_page: 1, from: 1, to: 1 };
  const links = { first: '', last: '', prev: null, next: null };
  return {
    useOrganizations: () => ({
      data: {
        items: [
          { id: 1, name: 'Acme', slug: 'acme', role: 'owner', created_at: now, updated_at: now },
        ],
        meta,
        links,
      },
      isPending: false,
      error: null,
      refetch: vi.fn(),
    }),
    useOrganizationMembers: () => ({
      data: {
        items: [person(1, 'admin'), person(2, 'operator'), person(3, 'member')],
        meta,
        links,
      },
      isPending: false,
      error: null,
      refetch: vi.fn(),
    }),
  };
});
vi.mock('@/features/mailbox/services/platform-account-service', () => ({
  platformAccountService: ledger,
}));
vi.mock('@/features/mailbox/services/mailbox-service', () => ({ mailboxService: mail }));

const created = '2026-11-16T10:00:00Z';

function account(overrides: Partial<Record<string, unknown>> = {}) {
  return {
    id: 12,
    platform: 'Medium',
    handle: '@acme',
    profile_url: 'https://medium.com/@acme',
    role_id: 3,
    mailbox_id: 14,
    mailbox_address: 'design@mail.example.com',
    owner_user_id: 2,
    status: 'active',
    notes: '',
    created_at: created,
    updated_at: created,
    ...overrides,
  };
}

function page(items: unknown[], currentPage = 1, lastPage = 1) {
  return {
    code: 0,
    message: 'ok',
    data: items,
    meta: {
      current_page: currentPage,
      per_page: 15,
      total: items.length + (lastPage - 1) * 15,
      last_page: lastPage,
      from: 1,
      to: items.length,
    },
    links: { first: '', last: '', prev: null, next: null },
  };
}

function mailbox(id: number, address: string) {
  return {
    id,
    address,
    display_name: '',
    type: 'shared',
    owner_user_id: null,
    awaiting_handover: false,
    sealed_before: null,
    webhooks_enabled: false,
    access: { view: true, send: true, manage_members: true, see_history: true, administer: true },
    unread_count: 0,
    created_at: created,
    role_id: null,
  };
}

function chooseOption(trigger: HTMLElement, optionName: string) {
  fireEvent.keyDown(trigger, { key: 'ArrowDown' });
  fireEvent.click(screen.getByRole('option', { name: optionName }));
}

function renderPage() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <NextIntlClientProvider locale="en-US" messages={messages as unknown as AbstractIntlMessages}>
        <PlatformAccountsAdmin />
      </NextIntlClientProvider>
    </QueryClientProvider>
  );
}

async function openRowMenu(name: string) {
  fireEvent.pointerDown(await screen.findByRole('button', { name: `More actions for ${name}` }));
}

describe('platform account admin', () => {
  beforeEach(() => {
    for (const fn of [...Object.values(ledger), ...Object.values(mail)]) fn.mockReset();
    vi.mocked(toast.success).mockReset();
    mail.list.mockResolvedValue([
      mailbox(14, 'design@mail.example.com'),
      mailbox(15, 'ops@mail.example.com'),
    ]);
    mail.listRoles.mockResolvedValue([
      {
        id: 3,
        name: 'Design',
        color: 'blue',
        description: '',
        owner_user_id: null,
        mailbox_ids: [14],
        created_at: created,
        updated_at: created,
      },
    ]);
    ledger.list.mockImplementation(
      async (_organizationId: number, _filters: unknown, options: { page: number }) =>
        options.page === 2
          ? page([account({ id: 13, platform: 'X', handle: '@acme_x', role_id: null })], 2, 2)
          : page([account()], 1, 2)
    );
    ledger.events.mockResolvedValue(page([]));
  });

  it('lists accounts with their role, mailbox, owner, and status', async () => {
    renderPage();
    const row = (await screen.findByText('@acme')).closest('tr');
    expect(row).not.toBeNull();
    const cells = within(row as HTMLElement).getAllByRole('cell');
    await waitFor(() =>
      expect(cells.map(cell => cell.textContent)).toEqual(
        expect.arrayContaining([
          'Medium',
          '@acme',
          'Design',
          'design@mail.example.com',
          'operator',
          'Active',
        ])
      )
    );
    expect(screen.getByRole('columnheader', { name: 'Registration mailbox' })).toBeInTheDocument();
    expect(ledger.list).toHaveBeenCalledWith(1, {}, { page: 1, perPage: 15 });
  });

  it('searches and filters, starting again at the first page', async () => {
    renderPage();
    await screen.findByText('@acme');
    fireEvent.click(screen.getByRole('button', { name: 'Next' }));
    expect(await screen.findByText('@acme_x')).toBeInTheDocument();

    fireEvent.change(screen.getByLabelText('Search by platform, handle, or address'), {
      target: { value: ' med ' },
    });
    await waitFor(() =>
      expect(ledger.list).toHaveBeenLastCalledWith(1, { q: 'med' }, { page: 1, perPage: 15 })
    );

    chooseOption(screen.getByLabelText('Status'), 'Needs review');
    chooseOption(screen.getByLabelText('Role'), 'Design');
    await waitFor(() => chooseOption(screen.getByLabelText('Mailbox'), 'ops@mail.example.com'));
    chooseOption(screen.getByLabelText('Owner'), 'operator');
    await waitFor(() =>
      expect(ledger.list).toHaveBeenLastCalledWith(
        1,
        { q: 'med', status: 'needs_review', roleId: 3, mailboxId: 15, ownerUserId: 2 },
        { page: 1, perPage: 15 }
      )
    );
  });

  it('pages by number', async () => {
    renderPage();
    await screen.findByText('@acme');
    expect(screen.getByText('Page 1 of 2')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Previous' })).toBeDisabled();
    fireEvent.click(screen.getByRole('button', { name: 'Next' }));
    expect(await screen.findByText('@acme_x')).toBeInTheDocument();
    expect(ledger.list).toHaveBeenLastCalledWith(1, {}, { page: 2, perPage: 15 });
    expect(screen.getByRole('button', { name: 'Next' })).toBeDisabled();
  });

  it('creates an account in the drawer', async () => {
    ledger.create.mockResolvedValue(account({ id: 20 }));
    renderPage();
    fireEvent.click(await screen.findByRole('button', { name: 'New account' }));
    const drawer = await screen.findByRole('dialog', { name: 'New platform account' });
    fireEvent.change(within(drawer).getByLabelText('Platform'), { target: { value: ' Behance ' } });
    fireEvent.change(within(drawer).getByLabelText('Handle'), { target: { value: 'acme' } });
    await waitFor(() =>
      chooseOption(within(drawer).getByLabelText('Registration mailbox'), 'ops@mail.example.com')
    );
    chooseOption(within(drawer).getByLabelText('Owner'), 'member');
    chooseOption(within(drawer).getByLabelText('Status'), 'Invited');
    fireEvent.click(within(drawer).getByRole('button', { name: 'Save' }));
    await waitFor(() =>
      expect(ledger.create).toHaveBeenCalledWith(1, {
        platform: 'Behance',
        handle: 'acme',
        profile_url: '',
        role_id: null,
        mailbox_id: 15,
        owner_user_id: 3,
        status: 'invited',
        notes: '',
      })
    );
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
  });

  it('requires a platform and a handle before saving', async () => {
    renderPage();
    fireEvent.click(await screen.findByRole('button', { name: 'New account' }));
    const drawer = await screen.findByRole('dialog', { name: 'New platform account' });
    fireEvent.click(within(drawer).getByRole('button', { name: 'Save' }));
    expect(
      await within(drawer).findByText('Enter the platform, up to 64 characters.')
    ).toBeInTheDocument();
    expect(within(drawer).getByText('Enter the handle, up to 200 characters.')).toBeInTheDocument();
    expect(ledger.create).not.toHaveBeenCalled();
  });

  it('shows a taken platform and handle under the handle', async () => {
    ledger.create.mockRejectedValue(
      new ApiError('taken', ApiErrorCode.MAILBOX_PLATFORM_ACCOUNT_TAKEN, 409)
    );
    renderPage();
    fireEvent.click(await screen.findByRole('button', { name: 'New account' }));
    const drawer = await screen.findByRole('dialog', { name: 'New platform account' });
    fireEvent.change(within(drawer).getByLabelText('Platform'), { target: { value: 'Medium' } });
    fireEvent.change(within(drawer).getByLabelText('Handle'), { target: { value: '@acme' } });
    fireEvent.click(within(drawer).getByRole('button', { name: 'Save' }));
    const message = await within(drawer).findByText(
      'This platform already has an account with this handle.'
    );
    expect(message).toHaveClass('text-error');
    expect(screen.getByRole('dialog', { name: 'New platform account' })).toBeInTheDocument();
  });

  it('edits an account and shows its history', async () => {
    ledger.update.mockResolvedValue(account({ status: 'retired' }));
    ledger.events.mockResolvedValue(
      page([
        {
          id: 41,
          account_id: 12,
          kind: 'owner_changed',
          actor_user_id: 1,
          from: '3',
          to: '2',
          created_at: created,
        },
        {
          id: 40,
          account_id: 12,
          kind: 'created',
          actor_user_id: 1,
          from: null,
          to: 'active',
          created_at: created,
        },
      ])
    );
    const consoleError = vi.spyOn(console, 'error');
    renderPage();
    fireEvent.click(await screen.findByRole('button', { name: 'Edit Medium @acme' }));
    const drawer = await screen.findByRole('dialog', { name: 'Edit Medium @acme' });
    expect(within(drawer).getByLabelText('Platform')).toHaveValue('Medium');
    expect(within(drawer).getByLabelText('Profile URL')).toHaveValue('https://medium.com/@acme');

    const history = within(drawer).getByRole('region', { name: 'History' });
    expect(await within(history).findByText('Owner: member → operator')).toBeInTheDocument();
    expect(within(history).getByText('Added as Active')).toBeInTheDocument();
    expect(ledger.events).toHaveBeenCalledWith(1, 12, { page: 1 });
    // The form and the history are siblings; React warns about clashing keys.
    expect(consoleError).not.toHaveBeenCalledWith(
      expect.stringContaining('same key'),
      expect.anything()
    );
    consoleError.mockRestore();

    await waitFor(() =>
      expect(within(drawer).getByLabelText('Role')).toHaveTextContent('Design')
    );
    chooseOption(within(drawer).getByLabelText('Status'), 'Retired');
    fireEvent.click(within(drawer).getByRole('button', { name: 'Save' }));
    await waitFor(() =>
      expect(ledger.update).toHaveBeenCalledWith(1, 12, {
        platform: 'Medium',
        handle: '@acme',
        profile_url: 'https://medium.com/@acme',
        role_id: 3,
        mailbox_id: 14,
        owner_user_id: 2,
        status: 'retired',
        notes: '',
      })
    );
  });

  it('deletes after confirmation', async () => {
    ledger.remove.mockResolvedValue(undefined);
    renderPage();
    await openRowMenu('Medium @acme');
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Delete' }));
    const dialog = await screen.findByRole('alertdialog');
    expect(within(dialog).getByText('Delete Medium @acme?')).toBeInTheDocument();
    expect(ledger.remove).not.toHaveBeenCalled();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Delete' }));
    await waitFor(() => expect(ledger.remove).toHaveBeenCalledWith(1, 12));
  });

  it('hands over every account of one person', async () => {
    ledger.handOver.mockResolvedValue({ from_user_id: 2, to_user_id: 3, account_ids: [12, 13] });
    renderPage();
    fireEvent.click(await screen.findByRole('button', { name: 'Hand over' }));
    const dialog = await screen.findByRole('dialog', { name: 'Hand over accounts' });
    chooseOption(within(dialog).getByLabelText('From'), 'operator');
    const to = within(dialog).getByLabelText('To');
    fireEvent.keyDown(to, { key: 'ArrowDown' });
    expect(screen.queryByRole('option', { name: 'operator' })).toBeNull();
    fireEvent.click(screen.getByRole('option', { name: 'member' }));
    fireEvent.click(within(dialog).getByRole('button', { name: 'Hand over' }));
    await waitFor(() =>
      expect(ledger.handOver).toHaveBeenCalledWith(1, { from_user_id: 2, to_user_id: 3 })
    );
    await waitFor(() => expect(toast.success).toHaveBeenCalledWith('2 accounts handed over'));
  });

  it('explains an empty ledger', async () => {
    ledger.list.mockResolvedValue(page([]));
    renderPage();
    expect(await screen.findByText('No platform accounts yet')).toBeInTheDocument();
  });
});

describe('mail role dialog', () => {
  beforeEach(() => {
    for (const fn of [...Object.values(ledger), ...Object.values(mail)]) fn.mockReset();
    mail.list.mockResolvedValue([]);
    mail.listRoles.mockResolvedValue([
      {
        id: 3,
        name: 'Design',
        color: 'blue',
        description: '',
        owner_user_id: null,
        mailbox_ids: [14],
        created_at: created,
        updated_at: created,
      },
    ]);
  });

  it('counts the platform accounts of each role', async () => {
    ledger.list.mockResolvedValue({ ...page([account()]), meta: { ...page([]).meta, total: 4 } });
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const scope = {
      status: 'ready',
      organization: { id: 1, name: 'Acme', slug: 'acme', role: 'owner', canManage: true },
    } as unknown as MailOrganizationScope;
    render(
      <QueryClientProvider client={client}>
        <NextIntlClientProvider
          locale="en-US"
          messages={messages as unknown as AbstractIntlMessages}
        >
          <MailOrganizationBoundary scope={scope}>
            {() => <ManageRolesDialog open onOpenChange={() => undefined} />}
          </MailOrganizationBoundary>
        </NextIntlClientProvider>
      </QueryClientProvider>
    );
    expect(await screen.findByText('4 platform accounts')).toBeInTheDocument();
    expect(ledger.list).toHaveBeenCalledWith(1, { roleId: 3 }, { page: 1, perPage: 1 });
  });
});
