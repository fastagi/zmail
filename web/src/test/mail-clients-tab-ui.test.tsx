import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { NextIntlClientProvider, type AbstractIntlMessages } from 'next-intl';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { MailClientsTab } from '@/features/mailbox/components/mail-clients-tab';
import {
  mailAppPasswordCreatedSchema,
  mailAppPasswordListSchema,
  mailAppPasswordSchema,
} from '@/features/mailbox/mail-client-schemas';
import { messages } from '@/i18n/modules';

const SECRET = 'abcd efgh ijkl mnop';

const state = vi.hoisted(() => ({
  organization: { id: 12, name: 'Acme', slug: 'acme', role: 'member' } as Record<string, unknown>,
  list: vi.fn(),
  create: vi.fn(),
  revoke: vi.fn(),
  settings: vi.fn(),
  updateSettings: vi.fn(),
  totp: vi.fn(),
  user: { id: '5' } as { id: string } | null,
  mailboxes: [] as Record<string, unknown>[],
}));

vi.mock('@/features/auth/store/auth-store', () => ({
  useCurrentUser: () => state.user,
}));

vi.mock('@/features/mailbox/hooks/use-mailbox', () => ({
  useMailboxes: () => ({ data: state.mailboxes }),
}));

vi.mock('@/features/mailbox/hooks/use-mail-organization', () => ({
  useChosenMailOrganization: () => state.organization,
}));

vi.mock('@/features/mailbox/services/mail-client-service', () => ({
  mailClientService: {
    list: state.list,
    create: state.create,
    revoke: state.revoke,
    settings: state.settings,
    updateSettings: state.updateSettings,
  },
}));

vi.mock('@/features/auth/services/totp-service', () => ({
  totpService: { status: state.totp },
}));

vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

const device = {
  id: 7,
  name: "Alice's iPhone",
  scopes: ['imap', 'smtp'],
  hint: 'wxyz',
  created_at: '2026-12-24T09:00:00Z',
  last_used_at: null,
  last_used_protocol: null,
};

function listOf(overrides: Record<string, unknown> = {}) {
  return {
    items: [device],
    limit: 20,
    access: { enabled: true, reason: null },
    server: {
      imap_host: 'imap.zmail.im',
      imap_port: 993,
      imap_security: 'ssl_tls',
      smtp_host: 'smtp.zmail.im',
      smtp_port: 465,
      smtp_security: 'ssl_tls',
      smtp_starttls_port: 587,
    },
    ...overrides,
  };
}

function renderTab() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const view = render(
    <QueryClientProvider client={client}>
      <NextIntlClientProvider locale="en-US" messages={messages as unknown as AbstractIntlMessages}>
        <MailClientsTab />
      </NextIntlClientProvider>
    </QueryClientProvider>
  );
  return { ...view, client };
}

/** Everything the query and mutation caches still hold, as text. */
function cachedText(client: QueryClient): string {
  const queries = client
    .getQueryCache()
    .getAll()
    .map(query => query.state.data);
  const mutations = client
    .getMutationCache()
    .getAll()
    .map(mutation => [mutation.state.data, mutation.state.variables]);
  return JSON.stringify([queries, mutations]);
}

describe('mail clients settings tab', () => {
  beforeEach(() => {
    state.organization = { id: 12, name: 'Acme', slug: 'acme', role: 'member' };
    state.user = { id: '5' };
    state.mailboxes = [];
    state.list.mockReset().mockResolvedValue(listOf());
    state.create.mockReset();
    state.revoke.mockReset().mockResolvedValue(undefined);
    state.settings.mockReset().mockResolvedValue({ access_enabled: true, updated_at: null });
    state.updateSettings.mockReset();
    state.totp
      .mockReset()
      .mockResolvedValue({ available: true, enabled: false, recovery_codes_remaining: 0 });
  });

  it('keeps the secret only in the creation schema', () => {
    expect(mailAppPasswordSchema.safeParse({ ...device, secret: SECRET }).success).toBe(false);
    expect(mailAppPasswordCreatedSchema.safeParse({ ...device, secret: SECRET }).success).toBe(
      true
    );
    expect(
      mailAppPasswordListSchema.safeParse(listOf({ items: [{ ...device, secret: SECRET }] }))
        .success
    ).toBe(false);
  });

  it('shows the incoming and outgoing servers with copy buttons', async () => {
    renderTab();

    expect(await screen.findByText('imap.zmail.im')).toBeInTheDocument();
    expect(screen.getByText('993')).toBeInTheDocument();
    expect(screen.getByText('smtp.zmail.im')).toBeInTheDocument();
    expect(screen.getByText('465')).toBeInTheDocument();
    expect(screen.getByText('587')).toBeInTheDocument();
    expect(screen.getAllByText('SSL/TLS')).toHaveLength(2);
    expect(screen.getAllByRole('button', { name: 'Copy server' })).toHaveLength(2);
    expect(screen.getByRole('button', { name: 'Copy STARTTLS port' })).toBeInTheDocument();
    expect(screen.queryByText(/Coming soon/)).not.toBeInTheDocument();
    // Without a personal mailbox known, the username row explains what to enter.
    expect(screen.getAllByText('Your full Zmail address, such as you@acme.zmail.im')).toHaveLength(
      2
    );
  });

  it("shows the member's own mailbox addresses as the username", async () => {
    const mailbox = { type: 'personal', origin: 'zmail', owner_user_id: 5 };
    state.mailboxes = [
      { ...mailbox, address: 'alice@acme.zmail.im' },
      { ...mailbox, address: 'alice@acme.com' },
      { ...mailbox, address: 'bob@acme.zmail.im', owner_user_id: 6 },
      { ...mailbox, address: 'team@acme.zmail.im', type: 'shared', owner_user_id: null },
      { ...mailbox, address: 'alice@gmail.com', origin: 'connected' },
    ];
    renderTab();

    expect(await screen.findAllByText('alice@acme.zmail.im')).toHaveLength(2);
    expect(screen.getAllByText('alice@acme.com')).toHaveLength(2);
    expect(screen.getAllByRole('button', { name: 'Copy alice@acme.zmail.im' })).toHaveLength(2);
    for (const other of ['bob@acme.zmail.im', 'team@acme.zmail.im', 'alice@gmail.com']) {
      expect(screen.queryByText(other)).not.toBeInTheDocument();
    }
    expect(screen.queryByText(/such as you@acme/)).not.toBeInTheDocument();
  });

  it('shows the secret once and forgets it after closing', async () => {
    state.create.mockResolvedValue({
      ...device,
      id: 8,
      name: 'Laptop',
      hint: 'mnop',
      secret: SECRET,
    });
    const { client } = renderTab();

    await screen.findByText("Alice's iPhone");
    fireEvent.click(screen.getByRole('button', { name: 'Add device' }));
    const dialog = await screen.findByRole('dialog');
    fireEvent.change(within(dialog).getByLabelText('Device name'), { target: { value: 'Laptop' } });
    fireEvent.change(within(dialog).getByLabelText('Your account password'), {
      target: { value: 'right' },
    });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Create password' }));

    expect(await screen.findByText(SECRET)).toBeInTheDocument();
    expect(state.create).toHaveBeenCalledWith(12, {
      name: 'Laptop',
      scopes: ['imap', 'smtp'],
      current_password: 'right',
    });
    expect(screen.getByText(/will not be shown again/)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Copy password' })).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Done' }));
    await waitFor(() => expect(screen.queryByText(SECRET)).not.toBeInTheDocument());
    expect(document.body.textContent).not.toContain(SECRET);
    await waitFor(() => expect(cachedText(client)).not.toContain(SECRET));
    expect(cachedText(client)).not.toContain('"right"');
  });

  it('offers reading and sending, both on by default, and sends the chosen scopes', async () => {
    state.create.mockResolvedValue({
      ...device,
      id: 8,
      name: 'Laptop',
      scopes: ['smtp'],
      secret: SECRET,
    });
    renderTab();

    await screen.findByText("Alice's iPhone");
    fireEvent.click(screen.getByRole('button', { name: 'Add device' }));
    const dialog = await screen.findByRole('dialog');
    const imap = within(dialog).getByRole('checkbox', { name: /Read mail/ });
    const smtp = within(dialog).getByRole('checkbox', { name: /Send mail/ });
    expect(imap).toBeChecked();
    expect(smtp).toBeChecked();
    expect(smtp).toBeEnabled();
    fireEvent.click(imap);
    fireEvent.change(within(dialog).getByLabelText('Device name'), { target: { value: 'Laptop' } });
    fireEvent.change(within(dialog).getByLabelText('Your account password'), {
      target: { value: 'right' },
    });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Create password' }));

    await waitFor(() =>
      expect(state.create).toHaveBeenCalledWith(12, {
        name: 'Laptop',
        scopes: ['smtp'],
        current_password: 'right',
      })
    );
  });

  it('asks for the authenticator code when two-factor sign-in is on', async () => {
    state.totp.mockResolvedValue({ available: true, enabled: true, recovery_codes_remaining: 8 });
    renderTab();

    await screen.findByText("Alice's iPhone");
    fireEvent.click(screen.getByRole('button', { name: 'Add device' }));
    const dialog = await screen.findByRole('dialog');
    expect(await within(dialog).findByLabelText('Authenticator code')).toBeInTheDocument();
  });

  it('lists devices without secrets and revokes after confirmation', async () => {
    renderTab();

    expect(await screen.findByText("Alice's iPhone")).toBeInTheDocument();
    expect(screen.getByText(/wxyz/)).toBeInTheDocument();
    expect(screen.getByText('Not used yet')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: "Revoke Alice's iPhone" }));
    expect(state.revoke).not.toHaveBeenCalled();
    const confirm = await screen.findByRole('alertdialog');
    fireEvent.click(within(confirm).getByRole('button', { name: 'Revoke' }));

    await waitFor(() => expect(state.revoke).toHaveBeenCalledWith(12, 7));
  });

  it('suggests adding the first device when there is none', async () => {
    state.list.mockResolvedValue(listOf({ items: [] }));
    renderTab();

    expect(await screen.findByText('No devices yet')).toBeInTheDocument();
    expect(screen.getAllByRole('button', { name: 'Add device' }).length).toBeGreaterThan(0);
  });

  it('explains when the organization turned client access off', async () => {
    state.list.mockResolvedValue(
      listOf({ access: { enabled: false, reason: 'organization_disabled' } })
    );
    renderTab();

    expect(
      await screen.findByText('An owner or admin turned off mail app access for this organization.')
    ).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Add device' })).toBeDisabled();
    // A member can still remove a device.
    expect(screen.getByRole('button', { name: "Revoke Alice's iPhone" })).toBeEnabled();
    expect(screen.queryByRole('switch')).not.toBeInTheDocument();
  });

  it('admin toggles organization access', async () => {
    state.organization = { id: 12, name: 'Acme', slug: 'acme', role: 'admin' };
    state.updateSettings.mockResolvedValue({
      access_enabled: false,
      updated_at: '2026-12-24T10:00:00Z',
    });
    renderTab();

    const toggle = await screen.findByRole('switch', { name: 'Allow members to use mail apps' });
    await waitFor(() => expect(toggle).toBeChecked());
    fireEvent.click(toggle);

    await waitFor(() => expect(state.updateSettings).toHaveBeenCalledWith(12, false));
  });
});
