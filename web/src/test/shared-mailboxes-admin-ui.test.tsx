import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { NextIntlClientProvider, type AbstractIntlMessages } from 'next-intl';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { AuthStoreContext, createAuthStore } from '@/features/auth/store/auth-store';
import { SharedMailboxesAdmin } from '@/features/mailbox';
import { messages } from '@/i18n/modules';

/**
 * The shared mailbox admin page with the real hooks and query cache; only the HTTP service and
 * the organization hooks are faked.
 */
const service = vi.hoisted(() => ({
  domains: vi.fn(),
  list: vi.fn(),
  directory: vi.fn(),
  rename: vi.fn(),
  remove: vi.fn(),
  create: vi.fn(),
  members: vi.fn(),
  setMember: vi.fn(),
  removeMember: vi.fn(),
  accessEvents: vi.fn(),
  importMailboxes: vi.fn(),
  exportMailboxes: vi.fn(),
}));
const saved = vi.hoisted(() => ({ blobs: [] as Blob[] }));

vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn(), message: vi.fn() } }));
vi.mock('next/navigation', () => ({
  useRouter: () => ({ replace: vi.fn() }),
  usePathname: () => '/console/admin/mailboxes',
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
vi.mock('@/features/mailbox/services/mailbox-service', () => ({ mailboxService: service }));
vi.mock('@/features/mailbox/services/mailbox-admin-service', () => ({
  mailboxAdminService: service,
}));

const created = '2026-11-16T10:00:00Z';

function entry(id: number, address: string, displayName: string, members = 1, sent = 0) {
  return {
    id,
    address,
    display_name: displayName,
    type: 'shared' as const,
    owner_user_id: null,
    awaiting_handover: false,
    created_at: created,
    member_count: members,
    sent_last_24h: sent,
  };
}

function directoryPage(items: ReturnType<typeof entry>[], nextCursor: string | null) {
  return {
    items,
    meta: {
      per_page: 15,
      has_more: nextCursor !== null,
      next_cursor: nextCursor,
      daily_send_limit: 200,
      sent_last_24h: 37,
      mailbox_count: 3,
      max_mailboxes: 50,
    },
  };
}

function renderPage(adminEmail?: string) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const content = (
    <QueryClientProvider client={client}>
      <NextIntlClientProvider locale="en-US" messages={messages as unknown as AbstractIntlMessages}>
        <SharedMailboxesAdmin />
      </NextIntlClientProvider>
    </QueryClientProvider>
  );
  if (!adminEmail) return render(content);
  const authStore = createAuthStore({
    status: 'authenticated',
    user: { id: '1', email: adminEmail, name: 'Admin' },
  });
  return render(
    <AuthStoreContext.Provider value={authStore}>{content}</AuthStoreContext.Provider>
  );
}

function uploadFile(csv: string) {
  fireEvent.change(screen.getByLabelText('CSV file'), {
    target: { files: [new File([csv], 'mailboxes.csv', { type: 'text/csv' })] },
  });
}

async function openRowMenu(address: string) {
  fireEvent.pointerDown(await screen.findByRole('button', { name: `More actions for ${address}` }));
}

describe('shared mailbox admin', () => {
  beforeEach(() => {
    for (const fn of Object.values(service)) fn.mockReset();
    saved.blobs = [];
    URL.createObjectURL = vi.fn((blob: Blob) => {
      saved.blobs.push(blob);
      return 'blob:test';
    });
    URL.revokeObjectURL = vi.fn();
    service.domains.mockResolvedValue([
      {
        domain: 'mail.example.com',
        kind: 'shared',
        max_mailboxes: 50,
        provider: 'log',
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
    service.directory.mockImplementation(
      async (_organizationId: number, filters: { q?: string }, cursor?: string) => {
        if (filters.q === 'bus')
          return directoryPage([entry(9, 'business@zgi.ai', 'Business')], null);
        if (cursor === 'c2') return directoryPage([entry(8, 'sales@zgi.ai', 'Sales', 2, 4)], null);
        return directoryPage([entry(7, 'docs@zgi.ai', 'Docs', 5, 0)], 'c2');
      }
    );
    service.members.mockResolvedValue([
      { user_id: 2, permission: 'send', granted_by: 1, created_at: created },
    ]);
    service.accessEvents.mockResolvedValue({ items: [], meta: {}, links: {} });
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('shows the organization send total', async () => {
    renderPage();
    expect(await screen.findByText('24-hour sends: 37 / 200')).toBeInTheDocument();
  });

  it('lists shared mailboxes', async () => {
    renderPage();
    const row = (await screen.findByText('docs@zgi.ai')).closest('tr');
    expect(row).not.toBeNull();
    const cells = within(row as HTMLElement).getAllByRole('cell');
    expect(cells.map(cell => cell.textContent)).toEqual(
      expect.arrayContaining(['Docs', 'docs@zgi.ai', '5', '0'])
    );
    expect(screen.getByRole('columnheader', { name: '24-hour send attempts' })).toBeInTheDocument();
    expect(service.directory).toHaveBeenCalledWith(1, { type: 'shared' }, undefined);
  });

  it('searches by name or address', async () => {
    renderPage();
    await screen.findByText('docs@zgi.ai');
    fireEvent.change(screen.getByLabelText('Search by name or address'), {
      target: { value: ' bus ' },
    });
    expect(await screen.findByText('business@zgi.ai')).toBeInTheDocument();
    expect(service.directory).toHaveBeenCalledWith(1, { type: 'shared', q: 'bus' }, undefined);
  });

  it('pages with the cursor', async () => {
    renderPage();
    await screen.findByText('docs@zgi.ai');
    expect(screen.getByRole('button', { name: 'Previous' })).toBeDisabled();
    fireEvent.click(screen.getByRole('button', { name: 'Next' }));
    expect(await screen.findByText('sales@zgi.ai')).toBeInTheDocument();
    expect(service.directory).toHaveBeenLastCalledWith(1, { type: 'shared' }, 'c2');
    expect(screen.getByRole('button', { name: 'Next' })).toBeDisabled();
    fireEvent.click(screen.getByRole('button', { name: 'Previous' }));
    expect(await screen.findByText('docs@zgi.ai')).toBeInTheDocument();
  });

  it('renames a mailbox', async () => {
    service.rename.mockResolvedValue({});
    renderPage();
    fireEvent.click(await screen.findByRole('button', { name: 'Edit the name of docs@zgi.ai' }));
    const dialog = await screen.findByRole('dialog');
    const input = within(dialog).getByLabelText('Name');
    expect(input).toHaveValue('Docs');
    fireEvent.change(input, { target: { value: '  Billing ' } });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(service.rename).toHaveBeenCalledWith(1, 7, 'Billing'));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
  });

  it('refuses a name with angle brackets before saving', async () => {
    renderPage();
    fireEvent.click(await screen.findByRole('button', { name: 'Edit the name of docs@zgi.ai' }));
    const dialog = await screen.findByRole('dialog');
    fireEvent.change(within(dialog).getByLabelText('Name'), { target: { value: '<b>' } });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Save' }));
    expect(
      await within(dialog).findByText(
        'Use a display name of up to 120 characters without <, >, or quotation marks.'
      )
    ).toBeInTheDocument();
    expect(service.rename).not.toHaveBeenCalled();
  });

  it('deletes after confirmation', async () => {
    service.remove.mockResolvedValue(undefined);
    renderPage();
    await openRowMenu('docs@zgi.ai');
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Delete' }));
    const dialog = await screen.findByRole('alertdialog');
    expect(
      within(dialog).getByText(
        'Its mail is deleted too. The address stays reserved and can never be used again.'
      )
    ).toBeInTheDocument();
    expect(service.remove).not.toHaveBeenCalled();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Delete' }));
    await waitFor(() => expect(service.remove).toHaveBeenCalledWith(1, 7));
  });

  it('manages members in a drawer', async () => {
    service.setMember.mockResolvedValue({
      user_id: 3,
      permission: 'view',
      granted_by: 1,
      created_at: created,
    });
    renderPage();
    await openRowMenu('docs@zgi.ai');
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Manage members' }));
    const drawer = await screen.findByRole('dialog', { name: 'Members of docs@zgi.ai' });
    await within(drawer).findByText('operator');
    fireEvent.change(within(drawer).getByLabelText('New member'), { target: { value: '3' } });
    fireEvent.change(within(drawer).getByLabelText('Permission of the new member'), {
      target: { value: 'view' },
    });
    fireEvent.click(within(drawer).getByRole('button', { name: 'Add member' }));
    await waitFor(() => expect(service.setMember).toHaveBeenCalledWith(1, 7, 3, 'view'));
  });

  it('creates only shared mailboxes', async () => {
    service.create.mockImplementation(async () => ({
      id: 11,
      address: 'ops@mail.example.com',
      display_name: '',
      type: 'shared',
      owner_user_id: null,
      awaiting_handover: false,
      sealed_before: null,
      webhooks_enabled: false,
      access: { view: true, send: true, manage_members: true, see_history: true, administer: true },
      unread_count: 0,
      created_at: created,
    }));
    renderPage();
    fireEvent.click(await screen.findByRole('button', { name: 'New shared mailbox' }));
    const dialog = await screen.findByRole('dialog');
    await within(dialog).findByLabelText('Address');
    expect(within(dialog).queryByRole('radiogroup')).toBeNull();
    fireEvent.change(within(dialog).getByLabelText('Address'), { target: { value: 'ops' } });
    fireEvent.change(within(dialog).getByLabelText('Member 1'), { target: { value: '2' } });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Create mailbox' }));
    await waitFor(() =>
      expect(service.create).toHaveBeenCalledWith(1, expect.objectContaining({ type: 'shared' }))
    );
  });

  it('import previews then creates', async () => {
    const failedRow = {
      row: 2,
      address: 'bad@mail.example.com',
      status: 'failed',
      mailbox_id: null,
      error_code: 'MAILBOX.IMPORT_MEMBER_NOT_FOUND',
      missing_emails: ['ghost@people.test'],
    };
    service.importMailboxes.mockImplementation(
      async (_id: number, _rows: unknown, dryRun: boolean) => ({
        dry_run: dryRun,
        succeeded: 1,
        failed: 1,
        rows: [
          {
            row: 1,
            address: 'ops@mail.example.com',
            status: dryRun ? 'valid' : 'created',
            mailbox_id: dryRun ? null : 12,
            error_code: null,
            missing_emails: [],
          },
          failedRow,
        ],
      })
    );
    renderPage();
    fireEvent.click(await screen.findByRole('button', { name: 'Import' }));
    await screen.findByRole('dialog');
    uploadFile(
      'address,display_name,members\r\nops@mail.example.com,Ops,member@people.test\r\nbad@mail.example.com,Bad,ghost@people.test\r\n'
    );

    expect(await screen.findByText('1 ready, 1 with errors')).toBeInTheDocument();
    expect(
      screen.getByText('Not in the organization. Invite them first: ghost@people.test')
    ).toBeInTheDocument();
    const rows = [
      { address: 'ops@mail.example.com', display_name: 'Ops', members: 'member@people.test' },
      { address: 'bad@mail.example.com', display_name: 'Bad', members: 'ghost@people.test' },
    ];
    expect(service.importMailboxes).toHaveBeenCalledWith(1, rows, true);

    fireEvent.click(screen.getByRole('button', { name: 'Create 1 mailbox' }));
    expect(await screen.findByText('1 created, 1 failed')).toBeInTheDocument();
    expect(service.importMailboxes).toHaveBeenLastCalledWith(1, rows, false);

    fireEvent.click(screen.getByRole('button', { name: 'Download failed rows' }));
    expect(saved.blobs).toHaveLength(1);
    const text = new TextDecoder('utf-8', { ignoreBOM: true }).decode(
      await saved.blobs[0].arrayBuffer()
    );
    expect(text).toBe(
      '\ufeffaddress,display_name,members,error\r\n' +
        'bad@mail.example.com,Bad,ghost@people.test,Not in the organization. Invite them first: ghost@people.test\r\n'
    );
  });

  it('defaults blank members to the signed-in admin with :manage', async () => {
    service.importMailboxes.mockImplementation(
      async (_id: number, rows: Array<{ address: string }>, dryRun: boolean) => ({
        dry_run: dryRun,
        succeeded: rows.length,
        failed: 0,
        rows: rows.map((row, index) => ({
          row: index + 1,
          address: row.address,
          status: dryRun ? 'valid' : 'created',
          mailbox_id: dryRun ? null : 20 + index,
          error_code: null,
          missing_emails: [],
        })),
      })
    );
    renderPage('admin@zgi.im');
    fireEvent.click(await screen.findByRole('button', { name: 'Import' }));
    await screen.findByRole('dialog');
    uploadFile(
      'address,display_name,members\r\nsupport@zgi.im,Support,\r\nsales@zgi.im,Sales,alice@zgi.im:send\r\n'
    );

    expect(await screen.findByText('2 ready, 0 with errors')).toBeInTheDocument();
    expect(
      screen.getByText('1 row with empty members defaulted to admin@zgi.im:manage.')
    ).toBeInTheDocument();
    const expectedRows = [
      { address: 'support@zgi.im', display_name: 'Support', members: 'admin@zgi.im:manage' },
      { address: 'sales@zgi.im', display_name: 'Sales', members: 'alice@zgi.im:send' },
    ];
    expect(service.importMailboxes).toHaveBeenCalledWith(1, expectedRows, true);

    fireEvent.click(screen.getByRole('button', { name: 'Create 2 mailboxes' }));
    expect(await screen.findByText('2 created, 0 failed')).toBeInTheDocument();
    expect(service.importMailboxes).toHaveBeenLastCalledWith(1, expectedRows, false);
  });

  it('chunks 12-row commits into 5 + 5 + 2 batches and preserves original row numbers', async () => {
    const csvRows = Array.from(
      { length: 12 },
      (_, index) => `m${index + 1}@mail.example.com,Box ${index + 1},member@people.test`
    );
    let commitCall = 0;
    const resolvers: Array<() => void> = [];

    service.importMailboxes.mockImplementation(
      async (_id: number, rows: Array<{ address: string }>, dryRun: boolean) => {
        if (dryRun) {
          return {
            dry_run: true,
            succeeded: rows.length,
            failed: 0,
            rows: rows.map((row, index) => ({
              row: index + 1,
              address: row.address,
              status: 'valid',
              mailbox_id: null,
              error_code: null,
              missing_emails: [],
            })),
          };
        }
        commitCall += 1;
        const currentCall = commitCall;
        await new Promise<void>(resolve => {
          resolvers.push(resolve);
        });
        return {
          dry_run: false,
          succeeded: currentCall === 2 ? rows.length - 1 : rows.length,
          failed: currentCall === 2 ? 1 : 0,
          rows: rows.map((row, index) =>
            currentCall === 2 && index === 1
              ? {
                  row: index + 1,
                  address: row.address,
                  status: 'failed',
                  mailbox_id: null,
                  error_code: 'MAILBOX.IMPORT_MEMBER_NOT_FOUND',
                  missing_emails: ['ghost@people.test'],
                }
              : {
                  row: index + 1,
                  address: row.address,
                  status: 'created',
                  mailbox_id: currentCall * 100 + index,
                  error_code: null,
                  missing_emails: [],
                }
          ),
        };
      }
    );

    renderPage();
    fireEvent.click(await screen.findByRole('button', { name: 'Import' }));
    await screen.findByRole('dialog');
    uploadFile(`address,display_name,members\n${csvRows.join('\n')}\n`);

    expect(await screen.findByText('12 ready, 0 with errors')).toBeInTheDocument();
    expect(service.importMailboxes).toHaveBeenCalledTimes(1);
    expect(service.importMailboxes.mock.calls[0]?.[1]).toHaveLength(12);
    expect(service.importMailboxes.mock.calls[0]?.[2]).toBe(true);

    fireEvent.click(screen.getByRole('button', { name: 'Create 12 mailboxes' }));
    expect(await screen.findByText('Importing 0 / 12…')).toBeInTheDocument();
    await waitFor(() => expect(resolvers).toHaveLength(1));
    resolvers[0]?.();

    expect(await screen.findByText('Importing 5 / 12…')).toBeInTheDocument();
    await waitFor(() => expect(resolvers).toHaveLength(2));
    resolvers[1]?.();

    expect(await screen.findByText('Importing 10 / 12…')).toBeInTheDocument();
    await waitFor(() => expect(resolvers).toHaveLength(3));
    resolvers[2]?.();

    expect(await screen.findByText('11 created, 1 failed')).toBeInTheDocument();
    expect(screen.getByText('Row 7 · m7@mail.example.com')).toBeInTheDocument();

    const commitCalls = service.importMailboxes.mock.calls.slice(1);
    expect(commitCalls).toHaveLength(3);
    expect(commitCalls.map(call => (call[1] as unknown[]).length)).toEqual([5, 5, 2]);
    expect(commitCalls.every(call => call[2] === false)).toBe(true);
  });

  it('rejects a file over 500 rows before upload', async () => {
    renderPage();
    fireEvent.click(await screen.findByRole('button', { name: 'Import' }));
    await screen.findByRole('dialog');
    const body = Array.from({ length: 501 }, (_, index) => `b${index}@d.test,,a@p.test`).join('\n');
    uploadFile(`address,display_name,members\n${body}\n`);
    expect(
      await screen.findByText('The file has more than 500 rows. Split it and import each part.')
    ).toBeInTheDocument();
    expect(service.importMailboxes).not.toHaveBeenCalled();
  });

  it('downloads the import template', async () => {
    renderPage();
    fireEvent.click(await screen.findByRole('button', { name: 'Import' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Download template' }));
    expect(saved.blobs).toHaveLength(1);
    expect(new TextDecoder().decode(await saved.blobs[0].arrayBuffer())).toBe(
      'address,display_name,members\r\n'
    );
  });

  it('export downloads the server file', async () => {
    service.exportMailboxes.mockResolvedValue({
      blob: new Blob(['address\r\n']),
      filename: 'shared-mailboxes.csv',
    });
    renderPage();
    fireEvent.click(await screen.findByRole('button', { name: 'Export' }));
    await waitFor(() => expect(service.exportMailboxes).toHaveBeenCalledWith(1, 'shared'));
    await waitFor(() => expect(saved.blobs).toHaveLength(1));
  });
});
