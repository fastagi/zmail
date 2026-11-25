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
  readinessRequests: [] as (number | undefined)[],
  organizations: { status: 'ready', organizations: [] } as unknown,
  domains: {} as Record<number, unknown>,
}));

vi.mock('@/features/api-key/hooks/use-restriction-options', () => ({
  useRestrictionOrganizations: () => state.organizations,
  useRestrictionDomains: (organizationId: number) => {
    return organizationId > 0
      ? (state.domains[organizationId] ?? { status: 'loading' })
      : { status: 'unavailable' };
  },
}));

vi.mock('@/features/api-key/hooks/use-send-readiness', () => ({
  useSendReadiness: (organizationId?: number) => {
    state.readinessRequests.push(organizationId);
    return state.readiness;
  },
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

function chooseOption(trigger: HTMLElement, optionName: string) {
  fireEvent.keyDown(trigger, { key: 'ArrowDown' });
  fireEvent.click(screen.getByRole('option', { name: optionName }));
}

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
    state.readinessRequests = [];
    state.organizations = {
      status: 'ready',
      organizations: [
        { id: 12, name: 'Acme' },
        { id: 13, name: 'Globex' },
      ],
    };
    state.domains = {
      12: { status: 'ready', domains: ['acme.zmail.im', 'mail.acme.example'] },
      13: { status: 'ready', domains: [] },
    };
  });

  it('creates an unrestricted key unless an organization is chosen', async () => {
    state.create.mutateAsync.mockResolvedValue({ api_key: apiKey, plaintext_key: 'x'.repeat(32) });
    renderWithMessages(<ApiKeyPanel />);

    fireEvent.click(screen.getByRole('button', { name: 'Create key' }));
    const organization = screen.getByRole('combobox', { name: 'Use only in one organization' });
    expect(organization).toHaveTextContent('Any organization');
    expect(screen.queryByRole('group', { name: 'Send only from these domains' })).not.toBeInTheDocument();
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Deploy' } });
    fireEvent.click(screen.getAllByRole('button', { name: 'Create key' }).at(-1)!);

    await waitFor(() =>
      expect(state.create.mutateAsync).toHaveBeenCalledWith({ name: 'Deploy', scopes: ['mail:send'] })
    );
  });

  it('limits a key to one organization and to the domains chosen from its own list', async () => {
    state.create.mutateAsync.mockResolvedValue({ api_key: apiKey, plaintext_key: 'x'.repeat(32) });
    renderWithMessages(<ApiKeyPanel />);

    fireEvent.click(screen.getByRole('button', { name: 'Create key' }));
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Deploy' } });
    chooseOption(screen.getByRole('combobox', { name: 'Use only in one organization' }), 'Acme');
    const domains = screen.getByRole('group', { name: 'Send only from these domains' });
    expect(within(domains).getAllByRole('checkbox')).toHaveLength(2);
    fireEvent.click(within(domains).getByRole('checkbox', { name: 'mail.acme.example' }));
    fireEvent.click(screen.getAllByRole('button', { name: 'Create key' }).at(-1)!);

    await waitFor(() =>
      expect(state.create.mutateAsync).toHaveBeenCalledWith({
        name: 'Deploy',
        scopes: ['mail:send'],
        organization_id: 12,
        sending_domains: ['mail.acme.example'],
      })
    );
  });

  it('drops chosen domains when the organization changes', async () => {
    state.create.mutateAsync.mockResolvedValue({ api_key: apiKey, plaintext_key: 'x'.repeat(32) });
    renderWithMessages(<ApiKeyPanel />);

    fireEvent.click(screen.getByRole('button', { name: 'Create key' }));
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Deploy' } });
    const organization = screen.getByRole('combobox', { name: 'Use only in one organization' });
    chooseOption(organization, 'Acme');
    fireEvent.click(screen.getByRole('checkbox', { name: 'acme.zmail.im' }));
    chooseOption(organization, 'Globex');
    expect(screen.getByText('This organization has no domains to send from yet.')).toBeInTheDocument();
    fireEvent.click(screen.getAllByRole('button', { name: 'Create key' }).at(-1)!);

    await waitFor(() =>
      expect(state.create.mutateAsync).toHaveBeenCalledWith({
        name: 'Deploy',
        scopes: ['mail:send'],
        organization_id: 13,
      })
    );
  });

  it('shows what a limited key may reach on its row, and nothing on an unrestricted one', () => {
    state.apiKeys.data = {
      items: [
        { ...apiKey, id: 43, name: 'Billing', organization_id: 12, sending_domains: ['mail.acme.example'] },
        { ...apiKey, id: 44, name: 'Archived org', organization_id: 99 },
        apiKey,
      ],
      meta: { total: 3 },
    };
    renderWithMessages(<ApiKeyPanel />);

    expect(screen.getByText('Only in Acme')).toBeInTheDocument();
    expect(screen.getByText('Sends only from mail.acme.example')).toBeInTheDocument();
    expect(screen.getByText('Only in organization #99')).toBeInTheDocument();
    expect(screen.getAllByText(/^Only in /)).toHaveLength(2);
  });

  it('hides the limits when organizations are not available', () => {
    state.organizations = { status: 'unavailable' };
    renderWithMessages(<ApiKeyPanel />);

    fireEvent.click(screen.getByRole('button', { name: 'Create key' }));
    expect(screen.queryByRole('combobox', { name: 'Use only in one organization' })).not.toBeInTheDocument();
  });

  it('renders metadata without fabricating or exposing a plaintext key', () => {
    renderWithMessages(<ApiKeyPanel />);

    expect(screen.getByText('Deployment')).toBeInTheDocument();
    expect(screen.getByText('zmail_abcdef123456')).toBeInTheDocument();
    expect(screen.getByText('mail:send')).toBeInTheDocument();
    expect(screen.queryByText(/sk_demo/)).not.toBeInTheDocument();
  });

  it('creates a sending key by default, with no expiry unless one is chosen', async () => {
    const plaintextKey = 'zmail_abcdef123456.0123456789abcdef0123456789abcdef0123456789abcdef';
    state.create.mutateAsync.mockResolvedValue({ api_key: apiKey, plaintext_key: plaintextKey });
    renderWithMessages(<ApiKeyPanel />);

    fireEvent.click(screen.getByRole('button', { name: 'Create key' }));
    expect(screen.queryByPlaceholderText(/models:/)).not.toBeInTheDocument();
    expect(screen.getByRole('radio', { name: 'Send mail (mail:send)' })).toBeChecked();
    expect(screen.queryByRole('checkbox')).not.toBeInTheDocument();
    expect(screen.getByRole('radio', { name: 'Never' })).toBeChecked();
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Deploy' } });
    fireEvent.click(screen.getAllByRole('button', { name: 'Create key' }).at(-1)!);

    await waitFor(() => expect(screen.getByDisplayValue(plaintextKey)).toBeInTheDocument());
    expect(state.create.mutateAsync).toHaveBeenCalledWith({ name: 'Deploy', scopes: ['mail:send'] });
    expect(state.create.reset).toHaveBeenCalled();
  });

  it('offers sending plus status lookups as one purpose', async () => {
    state.create.mutateAsync.mockResolvedValue({ api_key: apiKey, plaintext_key: 'x'.repeat(32) });
    renderWithMessages(<ApiKeyPanel />);

    fireEvent.click(screen.getByRole('button', { name: 'Create key' }));
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Deploy' } });
    fireEvent.click(
      screen.getByRole('radio', { name: 'Send mail and check status (mail:send + mail:read)' })
    );
    fireEvent.click(screen.getAllByRole('button', { name: 'Create key' }).at(-1)!);

    await waitFor(() =>
      expect(state.create.mutateAsync).toHaveBeenCalledWith({
        name: 'Deploy',
        scopes: ['mail:send', 'mail:read'],
      })
    );
  });

  it('reveals only the scopes the API accepts when the purpose is custom', async () => {
    state.create.mutateAsync.mockResolvedValue({ api_key: apiKey, plaintext_key: 'x'.repeat(32) });
    renderWithMessages(<ApiKeyPanel />);

    fireEvent.click(screen.getByRole('button', { name: 'Create key' }));
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Deploy' } });
    fireEvent.click(screen.getByRole('radio', { name: 'Custom' }));
    const boxes = screen.getAllByRole('checkbox');
    expect(boxes).toHaveLength(5);
    expect(screen.getByRole('checkbox', { name: /Send mail/ })).toBeChecked();
    expect(screen.queryByRole('textbox', { name: /scope/i })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('checkbox', { name: /Transactional mail/ }));
    fireEvent.click(screen.getAllByRole('button', { name: 'Create key' }).at(-1)!);

    await waitFor(() =>
      expect(state.create.mutateAsync).toHaveBeenCalledWith({
        name: 'Deploy',
        scopes: ['mail:send', 'mail:transactional'],
      })
    );
  });

  it('offers a template-creation key for AI tools under the custom purpose', async () => {
    state.create.mutateAsync.mockResolvedValue({ api_key: apiKey, plaintext_key: 'x'.repeat(32) });
    renderWithMessages(<ApiKeyPanel />);

    fireEvent.click(screen.getByRole('button', { name: 'Create key' }));
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'AI tool' } });
    fireEvent.click(screen.getByRole('radio', { name: 'Custom' }));
    const templates = screen.getByRole('checkbox', { name: /Create mail templates/ });
    expect(templates).not.toBeChecked();
    expect(templates.closest('label')).toHaveTextContent('templates:create');
    fireEvent.click(screen.getByRole('checkbox', { name: /Send mail/ }));
    fireEvent.click(templates);
    fireEvent.click(screen.getAllByRole('button', { name: 'Create key' }).at(-1)!);

    await waitFor(() =>
      expect(state.create.mutateAsync).toHaveBeenCalledWith({
        name: 'AI tool',
        scopes: ['templates:create'],
      })
    );
  });

  it('refuses a custom key without any scope, next to the permissions', async () => {
    renderWithMessages(<ApiKeyPanel />);

    fireEvent.click(screen.getByRole('button', { name: 'Create key' }));
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Deploy' } });
    fireEvent.click(screen.getByRole('radio', { name: 'Custom' }));
    fireEvent.click(screen.getByRole('checkbox', { name: /Send mail/ }));
    fireEvent.click(screen.getAllByRole('button', { name: 'Create key' }).at(-1)!);

    expect(await screen.findByText('Choose at least one permission.')).toBeInTheDocument();
    expect(state.create.mutateAsync).not.toHaveBeenCalled();
  });

  it('reports a missing name on the name field', async () => {
    renderWithMessages(<ApiKeyPanel />);

    fireEvent.click(screen.getByRole('button', { name: 'Create key' }));
    fireEvent.click(screen.getAllByRole('button', { name: 'Create key' }).at(-1)!);

    expect(await screen.findByText('Enter a name of up to 100 characters.')).toBeInTheDocument();
    expect(screen.getByLabelText('Name')).toHaveAttribute('aria-invalid', 'true');
    expect(state.create.mutateAsync).not.toHaveBeenCalled();
  });

  it.each([
    ['30 days', '2026-12-23T12:00:00.000Z'],
    ['90 days', '2027-02-21T12:00:00.000Z'],
  ])('turns the %s preset into an expiry', async (preset, expected) => {
    vi.useFakeTimers({ toFake: ['Date'] });
    vi.setSystemTime(new Date('2026-11-23T12:00:00Z'));
    try {
      state.create.mutateAsync.mockResolvedValue({ api_key: apiKey, plaintext_key: 'x'.repeat(32) });
      renderWithMessages(<ApiKeyPanel />);

      fireEvent.click(screen.getByRole('button', { name: 'Create key' }));
      fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Deploy' } });
      fireEvent.click(screen.getByRole('radio', { name: preset }));
      fireEvent.click(screen.getAllByRole('button', { name: 'Create key' }).at(-1)!);

      await waitFor(() =>
        expect(state.create.mutateAsync).toHaveBeenCalledWith({
          name: 'Deploy',
          scopes: ['mail:send'],
          expires_at: expected,
        })
      );
    } finally {
      vi.useRealTimers();
    }
  });

  it('asks for a future custom expiry on the date field', async () => {
    renderWithMessages(<ApiKeyPanel />);

    fireEvent.click(screen.getByRole('button', { name: 'Create key' }));
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Deploy' } });
    fireEvent.click(screen.getByRole('radio', { name: 'Custom date' }));
    fireEvent.click(screen.getAllByRole('button', { name: 'Create key' }).at(-1)!);
    expect(await screen.findByText('Choose an expiry date.')).toBeInTheDocument();

    fireEvent.change(screen.getByLabelText('Expiry date'), {
      target: { value: '2020-01-01T00:00' },
    });
    fireEvent.click(screen.getAllByRole('button', { name: 'Create key' }).at(-1)!);
    expect(await screen.findByText('Choose a time in the future.')).toBeInTheDocument();
    expect(screen.getByLabelText('Expiry date')).toHaveAttribute('aria-invalid', 'true');
    expect(state.create.mutateAsync).not.toHaveBeenCalled();
  });

  it('keeps the plaintext key dialog open until the key is copied or saved', async () => {
    const plaintextKey = 'zmail_abcdef123456.0123456789abcdef0123456789abcdef0123456789abcdef';
    state.create.mutateAsync.mockResolvedValue({ api_key: apiKey, plaintext_key: plaintextKey });
    renderWithMessages(<ApiKeyPanel />);

    fireEvent.click(screen.getByRole('button', { name: 'Create key' }));
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Deploy' } });
    fireEvent.click(screen.getAllByRole('button', { name: 'Create key' }).at(-1)!);
    const dialog = await screen.findByRole('dialog');

    fireEvent.keyDown(dialog, { key: 'Escape' });
    expect(screen.getByDisplayValue(plaintextKey)).toBeInTheDocument();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Done' }));
    expect(screen.getByDisplayValue(plaintextKey)).toBeInTheDocument();
    expect(
      within(dialog).getByText('Copy the key, or confirm you have stored it, before closing.')
    ).toHaveAttribute('role', 'alert');

    fireEvent.click(within(dialog).getByRole('checkbox', { name: 'I have stored this key safely' }));
    fireEvent.click(within(dialog).getByRole('button', { name: 'Done' }));
    expect(screen.queryByDisplayValue(plaintextKey)).not.toBeInTheDocument();
  });

  it('lets the plaintext key dialog close once the key is copied', async () => {
    const plaintextKey = 'zmail_abcdef123456.0123456789abcdef0123456789abcdef0123456789abcdef';
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true });
    state.create.mutateAsync.mockResolvedValue({ api_key: apiKey, plaintext_key: plaintextKey });
    renderWithMessages(<ApiKeyPanel />);

    fireEvent.click(screen.getByRole('button', { name: 'Create key' }));
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Deploy' } });
    fireEvent.click(screen.getAllByRole('button', { name: 'Create key' }).at(-1)!);
    const dialog = await screen.findByRole('dialog');

    fireEvent.click(within(dialog).getByRole('button', { name: 'Copy key' }));
    await waitFor(() => expect(writeText).toHaveBeenCalledWith(plaintextKey));
    await waitFor(() =>
      expect(within(dialog).getByRole('checkbox', { name: 'I have stored this key safely' })).toBeChecked()
    );
    fireEvent.keyDown(dialog, { key: 'Escape' });
    await waitFor(() => expect(screen.queryByDisplayValue(plaintextKey)).not.toBeInTheDocument());
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

  it('drops the Organization-Id header from the example of an organization-bound key', async () => {
    const plaintextKey = 'zmail_abcdef123456.0123456789abcdef0123456789abcdef0123456789abcdef';
    state.create.mutateAsync.mockResolvedValue({
      api_key: { ...apiKey, organization_id: 12 },
      plaintext_key: plaintextKey,
    });
    renderWithMessages(<ApiKeyPanel />);

    fireEvent.click(screen.getByRole('button', { name: 'Create key' }));
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Deploy' } });
    fireEvent.click(screen.getAllByRole('button', { name: 'Create key' }).at(-1)!);

    const dialog = await screen.findByRole('dialog');
    expect(state.readinessRequests).toContain(12);
    for (const language of ['curl', 'Node.js', 'Python']) {
      fireEvent.click(within(dialog).getByRole('button', { name: language }));
      const sample = within(dialog).getByLabelText('Code sample');
      expect(sample).toHaveTextContent(plaintextKey);
      expect(sample).not.toHaveTextContent('Organization-Id');
    }
    expect(
      within(dialog).getByText(
        'This key only works in Acme, so the example sends no Organization-Id header.'
      )
    ).toBeInTheDocument();
    expect(within(dialog).queryByText(/one key works in every organization/)).not.toBeInTheDocument();
  });

  it('sends the example of a domain-restricted key from an allowed domain', async () => {
    state.readiness = { ...ready, senders: ['dev@acme.zmail.im', 'billing@mail.acme.example'] };
    state.create.mutateAsync.mockResolvedValue({
      api_key: { ...apiKey, organization_id: 12, sending_domains: ['mail.acme.example'] },
      plaintext_key: 'x'.repeat(32),
    });
    renderWithMessages(<ApiKeyPanel />);

    fireEvent.click(screen.getByRole('button', { name: 'Create key' }));
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Deploy' } });
    fireEvent.click(screen.getAllByRole('button', { name: 'Create key' }).at(-1)!);

    const dialog = await screen.findByRole('dialog');
    const sample = within(dialog).getByLabelText('Code sample');
    expect(sample).toHaveTextContent('billing@mail.acme.example');
    expect(sample).not.toHaveTextContent('dev@acme.zmail.im');
    expect(within(dialog).queryByText(/you have no mailbox there yet/)).not.toBeInTheDocument();
  });

  it('keeps the own mailbox with a hint when no mailbox is on an allowed domain', async () => {
    state.create.mutateAsync.mockResolvedValue({
      api_key: { ...apiKey, organization_id: 12, sending_domains: ['mail.acme.example'] },
      plaintext_key: 'x'.repeat(32),
    });
    renderWithMessages(<ApiKeyPanel />);

    fireEvent.click(screen.getByRole('button', { name: 'Create key' }));
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Deploy' } });
    fireEvent.click(screen.getAllByRole('button', { name: 'Create key' }).at(-1)!);

    const dialog = await screen.findByRole('dialog');
    expect(within(dialog).getByLabelText('Code sample')).toHaveTextContent('dev@acme.zmail.im');
    expect(
      within(dialog).getByText(/This key only sends from mail\.acme\.example, and you have no mailbox there yet/)
    ).toBeInTheDocument();
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

  it('names the key it is about to revoke', async () => {
    state.revoke.mutateAsync.mockResolvedValue(undefined);
    renderWithMessages(<ApiKeyPanel />);

    fireEvent.click(screen.getByRole('button', { name: 'Revoke Deployment' }));
    const dialog = screen.getByRole('dialog');
    expect(dialog).toHaveTextContent('Deployment');
    expect(dialog).toHaveTextContent('zmail_abcdef123456');
    expect(state.revoke.mutateAsync).not.toHaveBeenCalled();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Revoke' }));

    await waitFor(() => expect(state.revoke.mutateAsync).toHaveBeenCalledWith(42));
  });

  it('offers to create the first key from the empty state', () => {
    state.apiKeys.data = { items: [], meta: { total: 0 } };
    renderWithMessages(<ApiKeyPanel />);

    const buttons = screen.getAllByRole('button', { name: 'Create key' });
    expect(buttons).toHaveLength(2);
    fireEvent.click(buttons[1]!);
    expect(screen.getByRole('dialog')).toHaveTextContent('Create API key');
  });

  it('allows an expired key to be explicitly revoked', () => {
    state.apiKeys.data = {
      items: [{ ...apiKey, expires_at: '2020-01-01T00:00:00Z' }],
      meta: { total: 1 },
    };
    renderWithMessages(<ApiKeyPanel />);

    expect(screen.getByRole('button', { name: 'Revoke Deployment' })).toBeEnabled();
  });
});

function renderWithMessages(children: React.ReactNode) {
  return render(
    <NextIntlClientProvider locale="en-US" messages={messages as unknown as AbstractIntlMessages}>
      {children}
    </NextIntlClientProvider>
  );
}
