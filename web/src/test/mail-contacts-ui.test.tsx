import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { NextIntlClientProvider, type AbstractIntlMessages } from 'next-intl';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { MailContacts } from '@/features/mailbox/components/mail-contacts';
import { ApiErrorCode } from '@/http/codes';
import { ApiError } from '@/http/request';
import { messages } from '@/i18n/modules';

const toast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn() }));
const state = vi.hoisted(() => {
  const query = () => ({
    data: undefined as unknown,
    isPending: false,
    error: null as unknown,
    refetch: vi.fn(),
  });
  const mutation = () => ({ isPending: false, mutateAsync: vi.fn() });
  return {
    organizations: query(),
    contacts: query(),
    lists: query(),
    contactCalls: [] as { filters: Record<string, unknown>; page: number }[],
    saveContact: mutation(),
    deleteContact: mutation(),
    importContacts: mutation(),
    saveList: mutation(),
    deleteList: mutation(),
  };
});

vi.mock('sonner', () => ({ toast }));
vi.mock('next/navigation', () => ({
  useRouter: () => ({ replace: vi.fn() }),
  usePathname: () => '/console/mail-contacts',
  useSearchParams: () => new URLSearchParams(),
}));
vi.mock('@/features/organization/hooks/use-organizations', () => ({
  useOrganizations: () => state.organizations,
}));
vi.mock('@/features/mailbox/hooks/use-mail-contacts', () => ({
  useMailContacts: (_organizationId: number, filters: Record<string, unknown>, page: number) => {
    state.contactCalls.push({ filters, page });
    return state.contacts;
  },
  useMailContactLists: () => state.lists,
  useSaveMailContact: () => state.saveContact,
  useDeleteMailContact: () => state.deleteContact,
  useImportMailContacts: () => state.importContacts,
  useSaveMailContactList: () => state.saveList,
  useDeleteMailContactList: () => state.deleteList,
}));

describe('mail contacts console', () => {
  beforeEach(() => {
    toast.success.mockReset();
    toast.error.mockReset();
    state.organizations.data = page([
      { id: 1, name: 'Acme', slug: 'acme', role: 'owner', created_at: now(), updated_at: now() },
    ]);
    state.lists.data = [{ id: 7, name: 'VIP', contact_count: 1, created_at: now() }];
    state.contacts.data = page([
      contact({ id: 2, email: 'bob@example.com', status: 'unsubscribed', status_reason: 'manual' }),
      contact({
        id: 1,
        email: 'ann@example.com',
        first_name: 'Ann',
        last_name: 'Lee',
        list_ids: [7],
      }),
    ]);
    state.contacts.error = null;
    state.contactCalls = [];
    for (const mutation of [
      state.saveContact,
      state.deleteContact,
      state.importContacts,
      state.saveList,
      state.deleteList,
    ]) {
      mutation.isPending = false;
      mutation.mutateAsync.mockReset();
    }
  });

  it('lists contacts with their status and lists', () => {
    renderContacts();
    const row = screen.getByRole('button', { name: 'Edit ann@example.com' }).closest('tr');
    expect(row).not.toBeNull();
    expect(within(row as HTMLElement).getByText('Ann Lee')).toBeInTheDocument();
    expect(within(row as HTMLElement).getByText('Subscribed')).toBeInTheDocument();
    expect(within(row as HTMLElement).getByText('VIP')).toBeInTheDocument();
    const bob = screen.getByRole('button', { name: 'Edit bob@example.com' }).closest('tr');
    expect(within(bob as HTMLElement).getByText('Unsubscribed')).toBeInTheDocument();
    expect(screen.getByText('1 to 2 of 2')).toBeInTheDocument();
  });

  it('filters by list, status, and search, starting again at page 1', () => {
    renderContacts();
    fireEvent.click(screen.getByRole('button', { name: /VIP/u }));
    expect(state.contactCalls.at(-1)).toEqual({ filters: { listId: 7 }, page: 1 });
    expect(screen.getByRole('button', { name: 'Rename list' })).toBeInTheDocument();

    fireEvent.change(screen.getByLabelText('Status'), { target: { value: 'unsubscribed' } });
    expect(state.contactCalls.at(-1)?.filters).toEqual({ listId: 7, status: 'unsubscribed' });

    fireEvent.change(screen.getByLabelText('Search contacts'), { target: { value: ' ann ' } });
    expect(state.contactCalls.at(-1)?.filters).toMatchObject({ q: 'ann' });
  });

  it('adds a contact with fields and lists', async () => {
    state.saveContact.mutateAsync.mockResolvedValue(contact({ id: 3 }));
    renderContacts();
    fireEvent.click(screen.getAllByRole('button', { name: 'Add contact' })[0]);
    const dialog = screen.getByRole('dialog');
    fireEvent.change(within(dialog).getByLabelText('Email'), {
      target: { value: 'carol@example.com' },
    });
    fireEvent.change(within(dialog).getByLabelText('First name'), { target: { value: 'Carol' } });
    fireEvent.click(within(dialog).getByLabelText('VIP'));
    fireEvent.click(within(dialog).getByRole('button', { name: 'Add field' }));
    fireEvent.change(within(dialog).getByLabelText('Field name'), { target: { value: 'plan' } });
    fireEvent.change(within(dialog).getByLabelText('Value'), { target: { value: 'pro' } });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Save' }));

    await waitFor(() =>
      expect(state.saveContact.mutateAsync).toHaveBeenCalledWith({
        input: {
          email: 'carol@example.com',
          first_name: 'Carol',
          last_name: '',
          properties: { plan: 'pro' },
          status: 'subscribed',
          list_ids: [7],
        },
      })
    );
    expect(toast.success).toHaveBeenCalledWith('Contact added');
  });

  it('edits a contact without changing its address and shows API errors', async () => {
    state.saveContact.mutateAsync.mockRejectedValueOnce(
      new ApiError('invalid', ApiErrorCode.MAILBOX_CONTACT_INVALID, 422)
    );
    renderContacts();
    fireEvent.click(screen.getByRole('button', { name: 'Edit ann@example.com' }));
    const dialog = screen.getByRole('dialog');
    expect(within(dialog).getByLabelText('Email')).toHaveAttribute('readonly');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Save' }));
    expect(await within(dialog).findByText(/Check the contact/u)).toBeInTheDocument();
    expect(state.saveContact.mutateAsync).toHaveBeenCalledWith(
      expect.objectContaining({
        contactId: 1,
        input: expect.not.objectContaining({ email: expect.anything() }),
      })
    );
  });

  it('keeps a suppressed contact suppressed unless another status is chosen', async () => {
    state.contacts.data = page([
      contact({ id: 5, email: 'gone@example.com', status: 'suppressed', status_reason: 'bounced' }),
    ]);
    state.saveContact.mutateAsync.mockResolvedValue(contact({ id: 5 }));
    renderContacts();
    fireEvent.click(screen.getByRole('button', { name: 'Edit gone@example.com' }));
    const dialog = screen.getByRole('dialog');
    expect(within(dialog).getByText(/Zmail suppressed this address/u)).toBeInTheDocument();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(state.saveContact.mutateAsync).toHaveBeenCalled());
    const [{ input }] = state.saveContact.mutateAsync.mock.calls[0] as [
      { input: Record<string, unknown> },
    ];
    expect(input.status).toBeUndefined();
  });

  it('imports a CSV file into a list and reports skipped rows', async () => {
    state.importContacts.mutateAsync.mockResolvedValue({
      rows: 3,
      created: 1,
      updated: 1,
      skipped_count: 1,
      skipped: [{ line: 3, reason: 'invalid_email' }],
      ignored_columns: ['Phone #'],
    });
    renderContacts();
    fireEvent.click(screen.getByRole('button', { name: 'Import CSV' }));
    const dialog = screen.getByRole('dialog');
    const csv = 'email,first_name\nann@example.com,Ann\n';
    fireEvent.change(within(dialog).getByLabelText('CSV file'), {
      target: { files: [new File([csv], 'contacts.csv', { type: 'text/csv' })] },
    });
    fireEvent.change(within(dialog).getByLabelText('Add to list'), { target: { value: '7' } });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Import' }));

    await waitFor(() =>
      expect(state.importContacts.mutateAsync).toHaveBeenCalledWith({ csv, list_id: 7 })
    );
    expect(
      await within(dialog).findByText('3 rows read: 1 added, 1 updated, 1 skipped.')
    ).toBeInTheDocument();
    expect(within(dialog).getByText('Line 3')).toBeInTheDocument();
    expect(within(dialog).getByText('Not a usable address')).toBeInTheDocument();
    expect(within(dialog).getByText('Ignored columns: Phone #')).toBeInTheDocument();
  });

  it('creates a list and selects it', async () => {
    state.saveList.mutateAsync.mockResolvedValue({
      id: 8,
      name: 'News',
      contact_count: 0,
      created_at: now(),
    });
    renderContacts();
    fireEvent.change(screen.getByLabelText('List name'), { target: { value: 'News' } });
    fireEvent.click(screen.getByRole('button', { name: 'Add list' }));
    await waitFor(() => expect(state.saveList.mutateAsync).toHaveBeenCalledWith({ name: 'News' }));
    await waitFor(() => expect(state.contactCalls.at(-1)?.filters).toEqual({ listId: 8 }));
  });

  it('shows an empty state with actions when there are no contacts', () => {
    state.contacts.data = page([]);
    renderContacts();
    expect(screen.getByText('No contacts yet')).toBeInTheDocument();
  });
});

function contact(overrides: Record<string, unknown> = {}) {
  return {
    id: 1,
    email: 'ann@example.com',
    first_name: '',
    last_name: '',
    properties: {},
    status: 'subscribed',
    status_reason: '',
    list_ids: [],
    created_at: now(),
    updated_at: now(),
    ...overrides,
  };
}

function page(items: unknown[]) {
  return {
    items,
    meta: {
      current_page: 1,
      per_page: 25,
      total: items.length,
      last_page: 1,
      from: items.length ? 1 : 0,
      to: items.length,
    },
    links: { first: '', last: '', prev: null, next: null },
  };
}

function now() {
  return '2026-11-16T10:00:00Z';
}

function renderContacts() {
  return render(
    <NextIntlClientProvider locale="en-US" messages={messages as unknown as AbstractIntlMessages}>
      <MailContacts />
    </NextIntlClientProvider>
  );
}
