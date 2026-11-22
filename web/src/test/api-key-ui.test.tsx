import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { NextIntlClientProvider, type AbstractIntlMessages } from 'next-intl';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { ApiKeyPanel } from '@/features/api-key/components/api-key-panel';
import { messages } from '@/i18n/modules';

const state = vi.hoisted(() => ({
  apiKeys: {
    data: undefined as unknown,
    isPending: false,
    isError: false,
    refetch: vi.fn(),
  },
  create: {
    isPending: false,
    mutateAsync: vi.fn(),
    reset: vi.fn(),
  },
  revoke: {
    isPending: false,
    mutateAsync: vi.fn(),
  },
  readiness: { status: 'loading' } as unknown,
}));

vi.mock('@/features/api-key/hooks/use-send-readiness', () => ({
  useSendReadiness: () => state.readiness,
}));

vi.mock('@/features/api-key/hooks/use-api-keys', () => ({
  useApiKeys: () => state.apiKeys,
  useCreateApiKey: () => state.create,
  useRevokeApiKey: () => state.revoke,
}));

const apiKey = {
  id: 42,
  user_id: 7,
  name: 'Deployment',
  key_prefix: 'zmail_abcdef123456',
  mode: 'live',
  scopes: ['mail:send'],
  created_at: '2026-07-15T10:00:00Z',
  updated_at: '2026-07-15T10:00:00Z',
};

const ready = {
  status: 'ready',
  organization: { id: 12, name: 'Acme', canManage: true },
  senders: ['dev@acme.zmail.im', 'support@acme.zmail.im'],
  recipient: 'me@example.com',
};

describe('API key settings workflow', () => {
  beforeEach(() => {
    state.apiKeys.data = { items: [apiKey], meta: { total: 1 } };
    state.apiKeys.isPending = false;
    state.apiKeys.isError = false;
    state.apiKeys.refetch.mockReset();
    state.create.isPending = false;
    state.create.mutateAsync.mockReset();
    state.create.reset.mockReset();
    state.revoke.isPending = false;
    state.revoke.mutateAsync.mockReset();
    state.readiness = ready;
  });

  it('renders metadata without fabricating or exposing a plaintext key', () => {
    renderWithMessages(<ApiKeyPanel />);

    expect(screen.getByText('Deployment')).toBeInTheDocument();
    expect(screen.getByText('zmail_abcdef123456')).toBeInTheDocument();
    expect(screen.getByText('mail:send')).toBeInTheDocument();
    expect(screen.queryByText(/sk_demo/)).not.toBeInTheDocument();
  });

  it('creates a key from scope checkboxes, sending mail by default', async () => {
    const plaintextKey = 'zmail_abcdef123456.0123456789abcdef0123456789abcdef0123456789abcdef';
    state.create.mutateAsync.mockResolvedValue({ api_key: apiKey, plaintext_key: plaintextKey });
    renderWithMessages(<ApiKeyPanel />);

    fireEvent.click(screen.getByRole('button', { name: 'Create key' }));
    expect(screen.queryByPlaceholderText(/models:/)).not.toBeInTheDocument();
    expect(screen.getByRole('checkbox', { name: /Send mail/ })).toBeChecked();
    expect(screen.getByRole('checkbox', { name: /Read sent mail/ })).not.toBeChecked();
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Deploy' } });
    fireEvent.click(screen.getByRole('checkbox', { name: /Read sent mail/ }));
    fireEvent.click(screen.getAllByRole('button', { name: 'Create key' }).at(-1)!);

    await waitFor(() => expect(screen.getByDisplayValue(plaintextKey)).toBeInTheDocument());
    expect(state.create.mutateAsync).toHaveBeenCalledWith({
      name: 'Deploy',
      scopes: ['mail:send', 'mail:read'],
    });
    expect(state.create.reset).toHaveBeenCalled();

    fireEvent.click(screen.getByRole('button', { name: 'Done' }));
    expect(screen.queryByDisplayValue(plaintextKey)).not.toBeInTheDocument();
  });

  it('refuses a key without any scope', async () => {
    renderWithMessages(<ApiKeyPanel />);

    fireEvent.click(screen.getByRole('button', { name: 'Create key' }));
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Deploy' } });
    fireEvent.click(screen.getByRole('checkbox', { name: /Send mail/ }));
    fireEvent.click(screen.getAllByRole('button', { name: 'Create key' }).at(-1)!);

    expect(await screen.findByRole('alert')).toHaveTextContent('Choose at least one permission.');
    expect(state.create.mutateAsync).not.toHaveBeenCalled();
  });

  it('shows a ready-to-run send snippet with the new key, organization, and own mailbox', async () => {
    const plaintextKey = 'zmail_abcdef123456.0123456789abcdef0123456789abcdef0123456789abcdef';
    state.create.mutateAsync.mockResolvedValue({ api_key: apiKey, plaintext_key: plaintextKey });
    renderWithMessages(<ApiKeyPanel />);

    fireEvent.click(screen.getByRole('button', { name: 'Create key' }));
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Deploy' } });
    fireEvent.click(screen.getAllByRole('button', { name: 'Create key' }).at(-1)!);

    const dialog = await screen.findByRole('dialog');
    const sample = within(dialog).getByLabelText('Code sample');
    expect(sample).toHaveTextContent(`X-API-Key: ${plaintextKey}`);
    expect(sample).toHaveTextContent('Organization-Id: 12');
    expect(sample).toHaveTextContent('dev@acme.zmail.im');
    expect(sample).toHaveTextContent('me@example.com');
    expect(within(dialog).getByRole('link', { name: 'Emails' })).toHaveAttribute(
      'href',
      '/console/emails?org=12'
    );
  });

  it('keeps the plaintext key out of the page once the dialog closes', () => {
    renderWithMessages(<ApiKeyPanel />);

    const sample = screen.getByLabelText('Code sample');
    expect(sample).toHaveTextContent('X-API-Key: YOUR_API_KEY');
    expect(sample).toHaveTextContent('Organization-Id: 12');
    expect(screen.getByText(/says which organization the key acts in/)).toBeInTheDocument();
  });

  it('says an organization comes first when the user has none', () => {
    state.readiness = { status: 'no-organization' };
    renderWithMessages(<ApiKeyPanel />);

    expect(screen.getByText('Create an organization first')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Create organization' })).toHaveAttribute(
      'href',
      '/console/organizations'
    );
    expect(screen.queryByLabelText('Code sample')).not.toBeInTheDocument();
  });

  it('points managers to create a mailbox and members to ask for one', () => {
    state.readiness = {
      status: 'no-mailbox',
      organization: { id: 12, name: 'Acme', canManage: true },
    };
    const { unmount } = renderWithMessages(<ApiKeyPanel />);
    expect(screen.getByText('No mailbox to send from')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Create mailbox' })).toHaveAttribute(
      'href',
      '/console/mailboxes?org=12&new=mailbox'
    );
    unmount();

    state.readiness = {
      status: 'no-mailbox',
      organization: { id: 12, name: 'Acme', canManage: false },
    };
    renderWithMessages(<ApiKeyPanel />);
    expect(screen.getByText(/Ask an owner or admin of Acme/)).toBeInTheDocument();
    expect(screen.queryByRole('link', { name: 'Create mailbox' })).not.toBeInTheDocument();
  });

  it('requires confirmation before revoking an active key', async () => {
    state.revoke.mutateAsync.mockResolvedValue(undefined);
    renderWithMessages(<ApiKeyPanel />);

    fireEvent.click(screen.getByRole('button', { name: 'Revoke' }));
    expect(state.revoke.mutateAsync).not.toHaveBeenCalled();
    fireEvent.click(screen.getAllByRole('button', { name: 'Revoke' }).at(-1)!);

    await waitFor(() => expect(state.revoke.mutateAsync).toHaveBeenCalledWith(42));
  });

  it('allows an expired key to be explicitly revoked', () => {
    state.apiKeys.data = {
      items: [{ ...apiKey, expires_at: '2020-01-01T00:00:00Z' }],
      meta: { total: 1 },
    };
    renderWithMessages(<ApiKeyPanel />);

    expect(screen.getByRole('button', { name: 'Revoke' })).toBeEnabled();
  });
});

function renderWithMessages(children: React.ReactNode) {
  return render(
    <NextIntlClientProvider locale="en-US" messages={messages as unknown as AbstractIntlMessages}>
      {children}
    </NextIntlClientProvider>
  );
}
