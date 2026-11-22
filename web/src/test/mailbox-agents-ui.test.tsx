import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { NextIntlClientProvider, type AbstractIntlMessages } from 'next-intl';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { MailboxAccessPanel } from '@/features/mailbox/components/mailbox-access-panel';
import { MailOrganizationBoundary } from '@/features/mailbox/components/mail-organization';
import type { MailOrganizationScope } from '@/features/mailbox/hooks/use-mail-organization';
import type { Mailbox } from '@/features/mailbox/types';
import { ApiErrorCode } from '@/http/codes';
import { ApiError } from '@/http/request';
import { messages } from '@/i18n/modules';

const toast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn() }));
const service = vi.hoisted(() => ({
  list: vi.fn(),
  issue: vi.fn(),
  revoke: vi.fn(),
  activity: vi.fn(),
  drafts: vi.fn(),
}));
const keys = vi.hoisted(() => ({ list: vi.fn() }));
const mail = vi.hoisted(() => ({
  labels: [] as { id: number; name: string }[],
  message: null as unknown,
  send: vi.fn(),
}));
const composer = vi.hoisted(() => ({ props: null as null | Record<string, unknown> }));

vi.mock('sonner', () => ({ toast }));
vi.mock('@/features/mailbox/services/agent-grant-service', () => ({
  agentGrantService: service,
}));
vi.mock('@/features/api-key/services/api-key-service', () => ({ apiKeyService: keys }));
vi.mock('@/features/organization/hooks/use-organizations', () => ({
  useOrganizations: () => ({ data: undefined, isPending: true, error: null }),
  useOrganizationMembers: () => ({ data: undefined, isPending: false, error: null }),
}));
vi.mock('@/features/mailbox/hooks/use-mail-triage', () => ({
  useMailTriageSetting: () => ({ data: undefined, isPending: true, error: null }),
  useSetMailboxTriage: () => ({ isPending: false, mutateAsync: vi.fn() }),
}));
vi.mock('@/features/mailbox/hooks/use-mailbox', async () => {
  const keysModule = await vi.importActual<typeof import('@/features/mailbox/hooks/mailbox-keys')>(
    '@/features/mailbox/hooks/mailbox-keys'
  );
  const query = (data: unknown) => ({ data, isPending: false, error: null, refetch: vi.fn() });
  const mutation = { isPending: false, mutateAsync: vi.fn() };
  return {
    mailboxKeys: keysModule.mailboxKeys,
    validId: keysModule.validId,
    useMailboxMembers: () => query([]),
    useMailboxAccessEvents: () => query(undefined),
    useSetMailboxMember: () => mutation,
    useRemoveMailboxMember: () => mutation,
    useHandOverMailbox: () => mutation,
    useDeleteMailboxSealedHistory: () => mutation,
    useSetMailboxWebhooks: () => mutation,
    useMailLabels: () => query(mail.labels),
    useMailMessage: () => query(mail.message),
    useSendMailMessage: () => ({ isPending: false, mutateAsync: mail.send }),
  };
});
// The composer has its own tests; this stand-in shows what it was handed and sends nothing.
vi.mock('@/features/mailbox/components/compose-dialog', () => ({
  ComposeDialog: (props: Record<string, unknown>) => {
    composer.props = props;
    const draft = props.draft as { to: string; subject: string; body: string };
    return (
      <div role="dialog" aria-label="Composer">
        <p>{draft.to}</p>
        <p>{draft.subject}</p>
        <textarea aria-label="Composer body" readOnly value={draft.body} />
        <button type="button" onClick={props.onClose as () => void}>
          Close composer
        </button>
      </div>
    );
  },
}));

const now = Date.now();
const iso = (offsetDays: number) => new Date(now + offsetDays * 86_400_000).toISOString();

const ownerAccess = {
  view: true,
  send: true,
  manage_members: false,
  administer: false,
  see_history: true,
  organize: true,
};

describe('mailbox Agent tab', () => {
  beforeEach(() => {
    for (const fn of Object.values(service)) fn.mockReset();
    keys.list.mockReset();
    toast.success.mockReset();
    mail.labels = [{ id: 4, name: 'Partners' }];
    mail.message = message();
    mail.send.mockReset();
    composer.props = null;
    service.list.mockResolvedValue(grantPage([]));
    service.drafts.mockResolvedValue(grantPage([]));
    keys.list.mockResolvedValue(keyPage([apiKey(12, 'Support agent', ['mail:agent'])]));
  });

  it('shows the Agent tab only to who may manage the grants', async () => {
    const { unmount } = renderPanel(mailbox({ type: 'personal', owner_user_id: 4 }));
    expect(screen.getByRole('tab', { name: 'Agents' })).toBeInTheDocument();
    unmount();

    // An organization admin on a shared mailbox without a manage membership.
    renderPanel(
      mailbox({
        access: { ...ownerAccess, organize: false, manage_members: true, administer: true },
      })
    );
    expect(screen.queryByRole('tab', { name: 'Agents' })).not.toBeInTheDocument();
    expect(screen.queryByRole('tablist')).not.toBeInTheDocument();
    expect(service.list).not.toHaveBeenCalled();
  });

  it('lists grants with key, capabilities, filters, expiry, and status', async () => {
    service.list.mockResolvedValue(
      grantPage([
        grant({
          id: 3,
          capabilities: ['read', 'draft'],
          reveal_secrets: true,
          filters: { senders: ['example.com', 'billing@vendor.test'], label_ids: [] },
        }),
        grant({ id: 2, api_key_name: 'Old agent', status: 'expired', expires_at: iso(-1) }),
        grant({
          id: 1,
          api_key_name: 'Gone agent',
          status: 'revoked',
          revoked_at: iso(-2),
          revoke_reason: 'member_removed',
        }),
      ])
    );
    await openAgents(mailbox());

    const list = await screen.findByRole('list', { name: 'Authorized agents' });
    const rows = within(list)
      .getAllByRole('listitem')
      .filter(row => row.parentElement === list);
    expect(rows).toHaveLength(3);
    expect(within(rows[0]!).getByText('Support agent · sk_live_ab')).toBeInTheDocument();
    expect(within(rows[0]!).getByText('Active')).toBeInTheDocument();
    expect(within(rows[0]!).getByText('Read new mail')).toBeInTheDocument();
    expect(within(rows[0]!).getByText('Draft replies')).toBeInTheDocument();
    expect(within(rows[0]!).getByText('Reveal codes')).toBeInTheDocument();
    expect(
      within(rows[0]!).getByText('2 sender filters: example.com, billing@vendor.test')
    ).toBeInTheDocument();
    expect(within(rows[0]!).getByText(/^Expires /u)).toBeInTheDocument();
    expect(within(rows[0]!).getByRole('button', { name: 'Revoke Support agent' })).toBeEnabled();

    expect(
      within(rows[1]!).getByText('Expired', { selector: '[data-slot="badge"]' })
    ).toBeInTheDocument();
    expect(within(rows[1]!).getByText('Whole mailbox')).toBeInTheDocument();
    expect(within(rows[1]!).queryByRole('button', { name: /^Revoke/u })).not.toBeInTheDocument();
    expect(
      within(rows[2]!).getByText('Revoked', { selector: '[data-slot="badge"]' })
    ).toBeInTheDocument();
    expect(
      within(rows[2]!).getByText(/The key owner was removed from the mailbox/u)
    ).toBeInTheDocument();
    expect(screen.getByText('1 of 10 active')).toBeInTheDocument();
  });

  it('validates the form, then sends only the documented payload', async () => {
    service.issue.mockResolvedValue(grant());
    keys.list.mockResolvedValue(
      keyPage([
        apiKey(12, 'Support agent', ['mail:agent']),
        apiKey(13, 'Reader', ['mail:read']),
        apiKey(14, 'Test agent', ['mail:agent'], { mode: 'test' }),
        apiKey(15, 'Old agent', ['*'], { revoked_at: iso(-1) }),
      ])
    );
    await openAgents(mailbox());
    const dialog = await openForm();

    const select = await within(dialog).findByRole('combobox', { name: 'Agent key' });
    expect(
      within(select)
        .getAllByRole('option')
        .map(option => option.textContent)
    ).toEqual(['Choose a key', 'Support agent · fastbuild_12']);
    fireEvent.click(within(dialog).getByRole('button', { name: 'Authorize' }));
    expect(await within(dialog).findByText('Choose an agent key.')).toBeInTheDocument();
    expect(service.issue).not.toHaveBeenCalled();

    fireEvent.change(select, { target: { value: '12' } });
    fireEvent.click(within(dialog).getByRole('radio', { name: 'Only mail matching filters' }));
    expect(within(dialog).getByText(/they are not authentication/u)).toBeInTheDocument();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Authorize' }));
    expect(
      await within(dialog).findByText('Add at least one sender or label, or choose all new mail.')
    ).toBeInTheDocument();

    fireEvent.change(within(dialog).getByRole('textbox', { name: 'Senders' }), {
      target: { value: 'Example.com, not a domain' },
    });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Authorize' }));
    expect(
      await within(dialog).findByText('"not" is not an address or a domain.')
    ).toBeInTheDocument();

    fireEvent.change(within(dialog).getByRole('textbox', { name: 'Senders' }), {
      target: { value: 'Example.com\nbilling@vendor.test' },
    });
    fireEvent.click(within(dialog).getByRole('checkbox', { name: 'Partners' }));
    fireEvent.click(
      within(dialog).getByRole('checkbox', { name: 'Draft replies for a person to review' })
    );
    fireEvent.click(
      within(dialog).getByRole('checkbox', { name: 'Reveal verification codes and sign-in links' })
    );
    expect(within(dialog).getByText(/recorded separately/u)).toBeInTheDocument();
    fireEvent.click(within(dialog).getByRole('radio', { name: '7 days' }));
    const before = Date.now();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Authorize' }));

    await waitFor(() => expect(service.issue).toHaveBeenCalledTimes(1));
    const [organizationId, mailboxId, input] = service.issue.mock.calls[0]!;
    expect([organizationId, mailboxId]).toEqual([1, 3]);
    expect(input).toEqual({
      api_key_id: 12,
      capabilities: ['read', 'draft'],
      reveal_secrets: true,
      filters: { senders: ['example.com', 'billing@vendor.test'], label_ids: [4] },
      expires_at: expect.any(String),
    });
    const expires = Date.parse(input.expires_at);
    expect(expires).toBeGreaterThan(before + 7 * 86_400_000 - 120_000);
    expect(expires).toBeLessThanOrEqual(Date.now() + 7 * 86_400_000);
    await waitFor(() =>
      expect(screen.queryByRole('dialog', { name: 'Authorize an agent' })).not.toBeInTheDocument()
    );
    expect(toast.success).toHaveBeenCalledWith('Agent authorized');
    // Focus returns to the control that opened the form.
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Authorize an agent' })).toHaveFocus()
    );
  });

  it('explains how to get a key when none carries mail:agent', async () => {
    keys.list.mockResolvedValue(keyPage([apiKey(13, 'Reader', ['mail:read'])]));
    await openAgents(mailbox());
    const dialog = await openForm();
    expect(await within(dialog).findByText(/You have no live API key/u)).toBeInTheDocument();
    expect(within(dialog).getByRole('link', { name: 'Open API keys' })).toHaveAttribute(
      'href',
      '/console/settings'
    );
    expect(within(dialog).getByRole('button', { name: 'Authorize' })).toBeDisabled();
  });

  it('blocks a tenth-plus grant and a key that already has one', async () => {
    service.list.mockResolvedValue(
      grantPage(
        Array.from({ length: 10 }, (_, index) => grant({ id: index + 1, api_key_id: 50 + index }))
      )
    );
    await openAgents(mailbox());
    expect(await screen.findByText('10 of 10 active')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Authorize an agent' })).toBeDisabled();
    expect(
      screen.getByText(
        'This mailbox already has 10 active agents. Revoke one before authorizing another.'
      )
    ).toBeInTheDocument();
  });

  it('disables drafting with an explanation on a connected mailbox', async () => {
    await openAgents(
      mailbox({
        type: 'personal',
        origin: 'connected',
        owner_user_id: 4,
        access: { ...ownerAccess, send: false, organize: false },
      })
    );
    const dialog = await openForm();
    expect(
      within(dialog).getByRole('checkbox', { name: 'Draft replies for a person to review' })
    ).toBeDisabled();
    expect(
      within(dialog).getByText(
        'A connected mailbox is read-only, so agents cannot draft replies for it.'
      )
    ).toBeInTheDocument();
    // Without send there are no drafts to review either.
    expect(
      screen.queryByRole('heading', { name: 'Drafts awaiting review' })
    ).not.toBeInTheDocument();
    expect(service.drafts).not.toHaveBeenCalled();
  });

  it('revokes only after the inline confirmation', async () => {
    service.list.mockResolvedValue(grantPage([grant({ id: 7 })]));
    service.revoke.mockResolvedValue(undefined);
    await openAgents(mailbox());
    const revoke = await screen.findByRole('button', { name: 'Revoke Support agent' });

    fireEvent.click(revoke);
    const confirm = screen.getByRole('group', {
      name: 'Revoke Support agent? It stops reading this mailbox at once.',
    });
    expect(within(confirm).getByRole('button', { name: 'Cancel' })).toHaveFocus();
    fireEvent.click(within(confirm).getByRole('button', { name: 'Cancel' }));
    expect(service.revoke).not.toHaveBeenCalled();
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Revoke Support agent' })).toHaveFocus()
    );

    fireEvent.click(screen.getByRole('button', { name: 'Revoke Support agent' }));
    // The list is fetched again after the revoke and shows the grant ended.
    service.list.mockResolvedValue(
      grantPage([grant({ id: 7, status: 'revoked', revoked_at: iso(0), revoke_reason: 'revoked' })])
    );
    fireEvent.click(screen.getByRole('button', { name: 'Revoke now' }));
    await waitFor(() => expect(service.revoke).toHaveBeenCalledWith(1, 3, 7));
    expect(toast.success).toHaveBeenCalledWith('Agent revoked');
    expect(await screen.findByText(/Revoked by a manager/u)).toBeInTheDocument();
    // Revoke is gone, so the row's Activity button takes the focus.
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Recent activity of Support agent' })).toHaveFocus()
    );
  });

  it('shows a refused revoke with the copy of its error code', async () => {
    service.list.mockResolvedValue(grantPage([grant({ id: 7 })]));
    service.revoke.mockRejectedValue(
      new ApiError('gone', ApiErrorCode.MAILBOX_AGENT_GRANT_NOT_FOUND, 404)
    );
    await openAgents(mailbox());
    fireEvent.click(await screen.findByRole('button', { name: 'Revoke Support agent' }));
    fireEvent.click(screen.getByRole('button', { name: 'Revoke now' }));
    expect(
      await screen.findByText('This grant no longer exists. Refresh to see the current list.')
    ).toBeInTheDocument();
  });

  it('shows an issue refusal with the copy of its error code', async () => {
    service.issue.mockRejectedValue(
      new ApiError('exists', ApiErrorCode.MAILBOX_AGENT_GRANT_EXISTS, 409)
    );
    await openAgents(mailbox());
    const dialog = await openForm();
    fireEvent.change(await within(dialog).findByRole('combobox', { name: 'Agent key' }), {
      target: { value: '12' },
    });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Authorize' }));
    expect(
      await within(dialog).findByText('This key already has an active grant on this mailbox.')
    ).toBeInTheDocument();
    expect(screen.getByRole('dialog', { name: 'Authorize an agent' })).toBeInTheDocument();
  });

  it('shows a grant’s recent activity without message content', async () => {
    service.list.mockResolvedValue(grantPage([grant({ id: 7 })]));
    service.activity.mockResolvedValue([audit(2, 'read_revealed', 5512), audit(1, 'draft', 5511)]);
    await openAgents(mailbox());
    const toggle = await screen.findByRole('button', { name: 'Recent activity of Support agent' });
    expect(toggle).toHaveAttribute('aria-expanded', 'false');
    fireEvent.click(toggle);
    expect(toggle).toHaveAttribute('aria-expanded', 'true');

    const region = await screen.findByRole('region', { name: 'Recent activity of Support agent' });
    expect(await within(region).findByText('Read with codes revealed')).toBeInTheDocument();
    expect(within(region).getByText('Message #5512')).toBeInTheDocument();
    expect(within(region).getByText('Drafted a reply')).toBeInTheDocument();
    expect(service.activity).toHaveBeenCalledWith(1, 3, 7);
  });

  it('hands an agent draft to the composer and never sends it', async () => {
    service.list.mockResolvedValue(grantPage([grant({ id: 7 })]));
    service.drafts.mockResolvedValue(
      grantPage([
        {
          id: 21,
          mailbox_id: 3,
          grant_id: 7,
          message_id: 11,
          api_key_id: 12,
          text: 'Thanks, we will look into it.',
          html: '',
          status: 'pending',
          created_at: iso(0),
          updated_at: iso(0),
        },
      ])
    );
    await openAgents(mailbox());

    const drafts = await screen.findByRole('list', { name: 'Drafts awaiting review' });
    expect(await within(drafts).findByText('Thanks, we will look into it.')).toBeInTheDocument();
    expect(within(drafts).getByText('Unread question')).toBeInTheDocument();
    expect(within(drafts).getByText('By Support agent')).toBeInTheDocument();

    const open = within(drafts).getByRole('button', {
      name: 'Open the draft replying to Unread question in the composer',
    });
    open.focus();
    fireEvent.click(open);
    const composerDialog = await screen.findByRole('dialog', { name: 'Composer' });
    expect(within(composerDialog).getByText('bob@example.com')).toBeInTheDocument();
    expect(within(composerDialog).getByText('Re: Unread question')).toBeInTheDocument();
    const body = within(composerDialog).getByRole('textbox', { name: 'Composer body' });
    expect((body as HTMLTextAreaElement).value).toMatch(/^Thanks, we will look into it\.\n\n/u);
    expect((body as HTMLTextAreaElement).value).toContain('> Plain question');
    expect(composer.props?.mailbox).toMatchObject({ id: 3 });
    expect(mail.send).not.toHaveBeenCalled();

    fireEvent.click(within(composerDialog).getByRole('button', { name: 'Close composer' }));
    await waitFor(() => expect(open).toHaveFocus());
    expect(mail.send).not.toHaveBeenCalled();
  });
});

function renderPanel(target: Mailbox) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const scope = {
    status: 'ready',
    organization: { id: 1, name: 'Acme', slug: 'acme', role: 'member', canManage: false },
  } as unknown as MailOrganizationScope;
  return render(
    <QueryClientProvider client={client}>
      <NextIntlClientProvider locale="en-US" messages={messages as unknown as AbstractIntlMessages}>
        <MailOrganizationBoundary scope={scope}>
          {() => (
            <MailboxAccessPanel
              mailbox={target}
              onClose={() => undefined}
              onRefresh={() => undefined}
            />
          )}
        </MailOrganizationBoundary>
      </NextIntlClientProvider>
    </QueryClientProvider>
  );
}

async function openAgents(target: Mailbox) {
  renderPanel(target);
  const tab = screen.getByRole('tab', { name: 'Agents' });
  fireEvent.mouseDown(tab);
  fireEvent.click(tab);
  await screen.findByRole('heading', { name: 'Authorized agents' });
}

async function openForm() {
  const trigger = await screen.findByRole('button', { name: 'Authorize an agent' });
  await waitFor(() => expect(trigger).toBeEnabled());
  trigger.focus();
  fireEvent.click(trigger);
  return screen.findByRole('dialog', { name: 'Authorize an agent' });
}

function mailbox(overrides: Partial<Mailbox> = {}): Mailbox {
  return {
    id: 3,
    address: 'support@mail.example.com',
    display_name: '',
    type: 'shared',
    origin: 'zmail',
    owner_user_id: null,
    awaiting_handover: false,
    sealed_before: null,
    webhooks_enabled: false,
    ai_triage_enabled: false,
    access: { ...ownerAccess, manage_members: true },
    unread_count: 0,
    created_at: iso(-30),
    role_id: null,
    ...overrides,
  } as Mailbox;
}

function grant(overrides: Record<string, unknown> = {}) {
  return {
    id: 7,
    mailbox_id: 3,
    api_key_id: 12,
    api_key_name: 'Support agent',
    api_key_prefix: 'sk_live_ab',
    key_user_id: 4,
    granted_by: 4,
    capabilities: ['read'],
    reveal_secrets: false,
    filters: { senders: [], label_ids: [] },
    status: 'active',
    expires_at: iso(20),
    revoked_at: null,
    revoke_reason: null,
    created_at: iso(-1),
    ...overrides,
  };
}

function audit(id: number, action: string, messageId: number) {
  return { id, grant_id: 7, api_key_id: 12, message_id: messageId, action, created_at: iso(0) };
}

function grantPage(items: unknown[]) {
  return {
    items,
    meta: {
      current_page: 1,
      per_page: 50,
      total: items.length,
      last_page: 1,
      from: 1,
      to: items.length,
    },
  };
}

function apiKey(
  id: number,
  name: string,
  scopes: string[],
  overrides: Record<string, unknown> = {}
) {
  return {
    id,
    user_id: 4,
    name,
    key_prefix: `fastbuild_${id}`,
    mode: 'live',
    scopes,
    created_at: iso(-3),
    updated_at: iso(-3),
    ...overrides,
  };
}

function keyPage(items: unknown[]) {
  return {
    items,
    meta: {
      current_page: 1,
      per_page: 100,
      total: items.length,
      last_page: 1,
      from: 1,
      to: items.length,
    },
    links: { first: '', last: '', prev: null, next: null },
  };
}

function message() {
  return {
    id: 11,
    mailbox_id: 3,
    direction: 'inbound',
    status: 'received',
    from: { address: 'bob@example.com', name: 'Bob' },
    to: ['support@mail.example.com'],
    cc: [],
    subject: 'Unread question',
    read: false,
    failure_code: '',
    queued_reason: null,
    last_event: '',
    attachment_count: 0,
    template_id: null,
    created_at: iso(-1),
    placement: 'default',
    folder_id: null,
    label_ids: [],
    starred: false,
    thread_id: 11,
    trashed_at: null,
    text_body: 'Plain question',
    html_body: '',
    message_id: 'question-1@example.com',
    in_reply_to: '',
    events: [],
    attachments: [],
  };
}
