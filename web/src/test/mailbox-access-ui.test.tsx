import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { NextIntlClientProvider, type AbstractIntlMessages } from 'next-intl';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { MailboxWorkspace } from '@/features/mailbox/components/mailbox-workspace';
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
  const mutation = () => ({ isPending: false, mutate: vi.fn(), mutateAsync: vi.fn() });
  return {
    organizations: query(),
    organizationMembers: query(),
    domains: query(),
    mailboxes: query(),
    inbox: query(),
    sent: query(),
    detail: query(),
    members: query(),
    events: query(),
    listCalls: [] as { mailboxId: number; direction: string; page: number }[],
    eventCalls: [] as { mailboxId: number; page: number; enabled: boolean }[],
    memberCalls: [] as { mailboxId: number; enabled: boolean }[],
    createMailbox: mutation(),
    reveal: mutation(),
    deleteMailbox: mutation(),
    send: mutation(),
    update: mutation(),
    removeMessage: mutation(),
    setMember: mutation(),
    removeMember: mutation(),
    handOver: mutation(),
    deleteSealed: mutation(),
    setWebhooks: mutation(),
  };
});

vi.mock('@/features/mailbox/hooks/use-thread-assignment', () => ({
  useThreadAssignment: () => ({ data: undefined, isSuccess: false }),
  useSaveThreadAssignment: () => ({ mutate: vi.fn(), isPending: false, isError: false }),
}));
vi.mock('@/features/mailbox/hooks/use-thread-notes', () => ({
  useThreadNotes: () => ({ data: undefined, isSuccess: false }),
  useAddThreadNote: () => ({ mutate: vi.fn(), isPending: false, isError: false }),
  useDeleteThreadNote: () => ({ mutate: vi.fn(), isPending: false, isError: false }),
}));
vi.mock('sonner', () => ({ toast }));
vi.mock('next/navigation', () => ({
  useRouter: () => ({ replace: vi.fn() }),
  usePathname: () => '/console/mailboxes',
  useSearchParams: () => new URLSearchParams('org=1'),
}));
vi.mock('@/features/organization/hooks/use-organizations', () => ({
  useOrganizations: () => state.organizations,
  useOrganizationMembers: () => state.organizationMembers,
}));
vi.mock('@/features/mailbox/hooks/use-mail-templates', () => ({
  useMailTemplates: () => ({ data: [] }),
}));
// Mailbox administration offers each mailbox's mail role; these tests have no roles.
vi.mock('@/features/mailbox/hooks/use-inbox', () => ({
  useMailRoles: () => ({ data: [], isPending: false, error: null }),
  useAssignMailboxRole: () => ({ isPending: false, mutateAsync: vi.fn() }),
}));
// The AI triage switch has its own tests; the organization has triage off unless a test says so.
const triage = vi.hoisted(() => ({
  setting: { data: undefined as unknown, isPending: true, error: null },
  setMailbox: { isPending: false, mutateAsync: vi.fn() },
}));
vi.mock('@/features/mailbox/hooks/use-mail-triage', () => ({
  useMailTriageSetting: () => triage.setting,
  useSetMailboxTriage: () => triage.setMailbox,
}));
vi.mock('@/features/mailbox/hooks/use-mail-signature', () => ({
  useMailSignature: () => ({
    data: { mailbox_id: 1, body: '', updated_at: null },
    isPending: false,
    isError: false,
  }),
  useSaveMailSignature: () => ({ mutateAsync: vi.fn(), isPending: false }),
}));
vi.mock('@/features/mailbox/hooks/use-mailbox', () => ({
  useMailboxDomains: () => state.domains,
  useMailboxes: () => state.mailboxes,
  useMailMessageList: (
    _organizationId: number,
    mailboxId: number,
    filters: { direction?: string },
    page: number
  ) => {
    state.listCalls.push({ mailboxId, direction: filters.direction ?? '', page });
    return filters.direction === 'outbound' ? state.sent : state.inbox;
  },
  useMailMessage: () => state.detail,
  useMailThread: () => ({ ...state.detail, data: [state.detail.data] }),
  useMailMessageCounts: () => ({ data: undefined, isPending: true, error: null, refetch: vi.fn() }),
  useMailFolders: () => ({ data: [], isPending: false, error: null, refetch: vi.fn() }),
  useMailLabels: () => ({ data: [], isPending: false, error: null, refetch: vi.fn() }),
  useUpdateMailMessages: () => state.update,
  usePurgeMailTrash: () => state.update,
  useCreateMailFolder: () => state.update,
  useRenameMailFolder: () => state.update,
  useDeleteMailFolder: () => state.update,
  useCreateMailLabel: () => state.update,
  useUpdateMailLabel: () => state.update,
  useDeleteMailLabel: () => state.update,
  useCreateMailbox: () => state.createMailbox,
  useDeleteMailbox: () => state.deleteMailbox,
  useSendMailMessage: () => state.send,
  useUpdateMailMessage: () => state.update,
  useDeleteMailMessage: () => state.removeMessage,
  useRevealMailMessage: () => state.reveal,
  useMailboxMembers: (_organizationId: number, mailboxId: number, enabled: boolean) => {
    state.memberCalls.push({ mailboxId, enabled });
    return state.members;
  },
  useMailboxAccessEvents: (
    _organizationId: number,
    mailboxId: number,
    page: number,
    enabled: boolean
  ) => {
    state.eventCalls.push({ mailboxId, page, enabled });
    return state.events;
  },
  useSetMailboxMember: () => state.setMember,
  useRemoveMailboxMember: () => state.removeMember,
  useHandOverMailbox: () => state.handOver,
  useDeleteMailboxSealedHistory: () => state.deleteSealed,
  useSetMailboxWebhooks: () => state.setWebhooks,
}));

const fullAccess = {
  view: true,
  send: true,
  manage_members: true,
  administer: true,
  see_history: true,
  organize: true,
};
const adminOnly = {
  view: false,
  send: false,
  manage_members: true,
  administer: true,
  see_history: true,
  organize: false,
};
const viewOnly = {
  view: true,
  send: false,
  manage_members: false,
  administer: false,
  see_history: false,
  organize: false,
};

/** Mailbox administration lives in the dialog behind the mailbox switcher. */
function openManage() {
  fireEvent.click(screen.getByRole('button', { name: /^Switch mailbox|^Manage mailboxes$/u }));
  const item = screen.queryByRole('button', { name: 'Manage mailboxes' });
  if (item) fireEvent.click(item);
  return screen.getByRole('dialog', { name: 'Manage mailboxes' });
}

describe('mailbox access in the console', () => {
  beforeEach(() => {
    triage.setting = { data: undefined, isPending: true, error: null };
    triage.setMailbox.mutateAsync.mockReset();
    toast.success.mockReset();
    toast.error.mockReset();
    state.organizations.data = page([organization('owner')]);
    state.organizationMembers.data = page([
      orgMember(1, 'admin', 'Admin User'),
      orgMember(5, 'riley', 'Riley Chen'),
      orgMember(9, 'morgan', 'Morgan Lee'),
    ]);
    state.domains.data = [domain()];
    state.mailboxes.data = [mailbox(1, 'hello', { access: fullAccess })];
    state.inbox.data = page([summary()]);
    state.sent.data = page([]);
    state.detail.data = message();
    state.members.data = [
      { user_id: 5, permission: 'view', granted_by: 1, created_at: now },
      { user_id: 9, permission: 'manage', granted_by: 1, created_at: now },
    ];
    state.events.data = page(
      [
        event(3, 'webhooks_changed', 9, null, { enabled: true }),
        event(2, 'member_set', 1, 5, { membership_id: 30, permission: 'view' }),
        event(1, 'created', 1, null, { type: 'shared' }),
      ],
      { last_page: 2, total: 4 }
    );
    for (const query of [
      state.organizations,
      state.organizationMembers,
      state.domains,
      state.mailboxes,
      state.inbox,
      state.sent,
      state.detail,
      state.members,
      state.events,
    ]) {
      query.isPending = false;
      query.error = null;
      query.refetch.mockReset();
    }
    state.listCalls = [];
    state.eventCalls = [];
    state.memberCalls = [];
    for (const mutation of [
      state.createMailbox,
      state.deleteMailbox,
      state.send,
      state.update,
      state.removeMessage,
      state.setMember,
      state.removeMember,
      state.handOver,
      state.deleteSealed,
      state.setWebhooks,
    ]) {
      mutation.isPending = false;
      mutation.mutate.mockReset();
      mutation.mutateAsync.mockReset();
    }
  });

  it('marks personal, shared, awaiting, and locked mailboxes in the list', () => {
    state.mailboxes.data = [
      mailbox(1, 'hello', { access: fullAccess }),
      mailbox(2, 'riley', { type: 'personal', owner_user_id: 5, access: adminOnly }),
      mailbox(3, 'gone', {
        type: 'personal',
        owner_user_id: null,
        awaiting_handover: true,
        access: adminOnly,
      }),
    ];
    renderWorkspace();

    const list = within(openManage()).getByRole('list', { name: 'Mailboxes' });
    const items = within(list).getAllByRole('listitem');
    expect(within(items[0]).getByText('Shared mailbox')).toBeInTheDocument();
    expect(within(items[0]).queryByText('No access to mail')).toBeNull();
    expect(within(items[1]).getByText('Personal mailbox')).toBeInTheDocument();
    expect(within(items[1]).getByText('No access to mail')).toBeInTheDocument();
    expect(within(items[2]).getByText('Awaiting handover')).toBeInTheDocument();
    expect(within(items[2]).getByText('No access to mail')).toBeInTheDocument();
  });

  it('shows a locked mailbox without its messages or compose', () => {
    state.mailboxes.data = [
      mailbox(2, 'riley', { type: 'personal', owner_user_id: 5, access: adminOnly }),
    ];
    renderWorkspace();

    // A mailbox the admin cannot read is not offered in the switcher.
    expect(screen.getByText('No mailbox to read')).toBeInTheDocument();
    expect(state.listCalls).toEqual([]);
    expect(screen.queryByRole('button', { name: 'Compose' })).toBeNull();
    expect(screen.queryByRole('navigation', { name: 'Mail views' })).toBeNull();
    // The admin still reaches the access panel and the delete control.
    const dialog = openManage();
    expect(
      within(dialog).getByRole('button', { name: 'Access to riley@mail.example.com' })
    ).toBeEnabled();
    expect(
      within(dialog).getByRole('button', { name: 'Delete riley@mail.example.com' })
    ).toBeEnabled();
  });

  it('hides compose, reply, and delete without send', () => {
    state.organizations.data = page([organization('member')]);
    state.mailboxes.data = [mailbox(1, 'hello', { access: viewOnly })];
    renderWorkspace();

    expect(screen.getByText('Unread question')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Compose' })).toBeNull();
    // Nothing to manage or read in the access panel.
    expect(screen.queryByRole('button', { name: /^Access to/u })).toBeNull();
    fireEvent.click(
      within(
        screen
          .getByRole('checkbox', { name: 'Select Unread question' })
          .closest('li') as HTMLElement
      )
        .getAllByRole('button')
        .find(button => !button.hasAttribute('aria-label')) as HTMLElement
    );
    expect(screen.getByRole('button', { name: 'Mark unread' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Reply' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Reply all' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Forward' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Delete' })).toBeNull();
  });

  it('requires an owner for a personal mailbox and members for a shared one', async () => {
    state.createMailbox.mutateAsync.mockResolvedValue(mailbox(4, 'team'));
    renderWorkspace();
    const form = within(openManage()).getByRole('form', { name: 'New mailbox' });

    fireEvent.change(within(form).getByLabelText('Address'), { target: { value: 'team' } });
    expect(within(form).getByRole('radio', { name: 'Shared' })).toBeChecked();
    fireEvent.click(within(form).getByRole('button', { name: 'Create mailbox' }));
    expect(await within(form).findByRole('alert')).toHaveTextContent(
      'Add at least one member, each only once.'
    );

    fireEvent.click(within(form).getByRole('radio', { name: 'Personal' }));
    fireEvent.click(within(form).getByRole('button', { name: 'Create mailbox' }));
    expect(await within(form).findByRole('alert')).toHaveTextContent(
      'Choose the owner of the personal mailbox.'
    );
    expect(state.createMailbox.mutateAsync).not.toHaveBeenCalled();

    const owner = within(form).getByLabelText('Owner') as HTMLSelectElement;
    expect([...owner.options].map(option => option.text)).toEqual([
      'Choose a member',
      'Admin User',
      'Riley Chen',
      'Morgan Lee',
    ]);
    fireEvent.change(owner, { target: { value: '5' } });
    fireEvent.click(within(form).getByRole('button', { name: 'Create mailbox' }));
    await waitFor(() =>
      expect(state.createMailbox.mutateAsync).toHaveBeenCalledWith({
        local_part: 'team',
        type: 'personal',
        owner_user_id: 5,
      })
    );
  });

  it('creates a shared mailbox with each member and permission', async () => {
    state.createMailbox.mutateAsync.mockResolvedValue(mailbox(4, 'team'));
    renderWorkspace();
    const form = within(openManage()).getByRole('form', { name: 'New mailbox' });

    fireEvent.change(within(form).getByLabelText('Address'), { target: { value: 'team' } });
    fireEvent.change(within(form).getByLabelText('Member 1'), { target: { value: '5' } });
    fireEvent.change(within(form).getByLabelText('Permission of member 1'), {
      target: { value: 'manage' },
    });
    fireEvent.click(within(form).getByRole('button', { name: 'Add another member' }));
    fireEvent.change(within(form).getByLabelText('Member 2'), { target: { value: '5' } });
    fireEvent.click(within(form).getByRole('button', { name: 'Create mailbox' }));
    // The same person twice is refused here, as the API would.
    expect(await within(form).findByRole('alert')).toHaveTextContent(
      'Add at least one member, each only once.'
    );

    fireEvent.change(within(form).getByLabelText('Member 2'), { target: { value: '9' } });
    expect(within(form).getByLabelText('Permission of member 2')).toHaveValue('send');
    fireEvent.click(within(form).getByRole('button', { name: 'Create mailbox' }));
    await waitFor(() =>
      expect(state.createMailbox.mutateAsync).toHaveBeenCalledWith({
        local_part: 'team',
        type: 'shared',
        members: [
          { user_id: 5, permission: 'manage' },
          { user_id: 9, permission: 'send' },
        ],
      })
    );
    expect(toast.success).toHaveBeenCalledWith('Mailbox created', {
      description: 'It starts receiving mail in a minute or two.',
    });
  });

  it('lists members, edits a permission, removes one, and adds one', async () => {
    state.setMember.mutateAsync.mockResolvedValue({});
    state.removeMember.mutateAsync.mockResolvedValue(undefined);
    renderWorkspace();
    fireEvent.click(screen.getByRole('button', { name: 'Access to hello@mail.example.com' }));
    const panel = screen.getByRole('dialog');

    expect(within(panel).getByText('Riley Chen')).toBeInTheDocument();
    expect(within(panel).getByText('Morgan Lee')).toBeInTheDocument();
    expect(within(panel).getByLabelText('Permission of Riley Chen')).toHaveValue('view');
    expect(state.memberCalls.at(-1)).toEqual({ mailboxId: 1, enabled: true });

    fireEvent.change(within(panel).getByLabelText('Permission of Riley Chen'), {
      target: { value: 'send' },
    });
    await waitFor(() =>
      expect(state.setMember.mutateAsync).toHaveBeenCalledWith({ userId: 5, permission: 'send' })
    );

    fireEvent.click(within(panel).getByRole('button', { name: 'Remove Morgan Lee' }));
    await waitFor(() => expect(state.removeMember.mutateAsync).toHaveBeenCalledWith(9));

    // Only organization members who are not members yet can be added.
    const candidate = within(panel).getByLabelText('New member') as HTMLSelectElement;
    expect([...candidate.options].map(option => option.text)).toEqual([
      'Choose a member',
      'Admin User',
    ]);
    fireEvent.change(candidate, { target: { value: '1' } });
    fireEvent.change(within(panel).getByLabelText('Permission of the new member'), {
      target: { value: 'view' },
    });
    fireEvent.click(within(panel).getByRole('button', { name: 'Add member' }));
    await waitFor(() =>
      expect(state.setMember.mutateAsync).toHaveBeenLastCalledWith({
        userId: 1,
        permission: 'view',
      })
    );
  });

  it('shows the access history with names and pages through it', () => {
    renderWorkspace();
    fireEvent.click(screen.getByRole('button', { name: 'Access to hello@mail.example.com' }));
    const panel = screen.getByRole('dialog');

    const history = within(panel).getByRole('list', { name: 'Access history' });
    const entries = within(history).getAllByRole('listitem');
    expect(entries[0]).toHaveTextContent('Morgan Lee turned organization webhooks on');
    expect(entries[1]).toHaveTextContent('Admin User set Riley Chen to View');
    expect(entries[2]).toHaveTextContent('Admin User created the mailbox as a shared mailbox');
    expect(state.eventCalls.at(-1)).toEqual({ mailboxId: 1, page: 1, enabled: true });

    fireEvent.click(within(panel).getByRole('button', { name: 'Older' }));
    expect(state.eventCalls.at(-1)).toEqual({ mailboxId: 1, page: 2, enabled: true });
  });

  it('switches organization webhooks only with manage_members on a shared mailbox', async () => {
    state.setWebhooks.mutateAsync.mockResolvedValue(mailbox(1, 'hello'));
    renderWorkspace();
    fireEvent.click(screen.getByRole('button', { name: 'Access to hello@mail.example.com' }));
    const toggle = within(screen.getByRole('dialog')).getByRole('switch', {
      name: 'Push to organization webhooks',
    });
    expect(toggle).not.toBeChecked();
    fireEvent.click(toggle);
    await waitFor(() => expect(state.setWebhooks.mutateAsync).toHaveBeenCalledWith(true));
  });

  it('offers the AI triage switch beside webhooks while the organization has triage on', async () => {
    triage.setting = {
      data: {
        available: true,
        enabled: true,
        daily_message_cap: 500,
        draft_enabled: false,
        instance_daily_cap: 500,
        updated_at: null,
      },
      isPending: false,
      error: null,
    };
    triage.setMailbox.mutateAsync.mockResolvedValue(mailbox(1, 'hello'));
    renderWorkspace();
    fireEvent.click(screen.getByRole('button', { name: 'Access to hello@mail.example.com' }));
    const toggle = within(screen.getByRole('dialog')).getByRole('switch', {
      name: 'AI triage for this mailbox',
    });
    fireEvent.click(toggle);
    await waitFor(() => expect(triage.setMailbox.mutateAsync).toHaveBeenCalledWith(true));
  });

  it('shows the owner but no members or webhooks on a personal mailbox', () => {
    state.mailboxes.data = [
      mailbox(2, 'riley', { type: 'personal', owner_user_id: 5, access: adminOnly }),
    ];
    renderWorkspace();
    fireEvent.click(
      within(openManage()).getByRole('button', { name: 'Access to riley@mail.example.com' })
    );
    const panel = screen.getByRole('dialog');

    expect(within(panel).getByText('Owner: Riley Chen')).toBeInTheDocument();
    expect(within(panel).queryByRole('switch')).toBeNull();
    expect(within(panel).queryByLabelText('New member')).toBeNull();
    expect(state.memberCalls.every(call => !call.enabled)).toBe(true);
  });

  it('hands over an awaiting mailbox to a successor after explaining the seal', async () => {
    state.handOver.mutateAsync.mockResolvedValue(mailbox(3, 'gone'));
    state.mailboxes.data = [
      mailbox(3, 'gone', {
        type: 'personal',
        owner_user_id: null,
        awaiting_handover: true,
        access: adminOnly,
      }),
    ];
    renderWorkspace();
    fireEvent.click(
      within(openManage()).getByRole('button', { name: 'Access to gone@mail.example.com' })
    );
    const panel = screen.getByRole('dialog');

    expect(within(panel).getByText(/owner has left the organization/u)).toBeInTheDocument();
    expect(within(panel).getByText(/Mail from before .* is sealed/u)).toBeInTheDocument();
    const submit = within(panel).getByRole('button', { name: 'Hand over' });
    expect(submit).toBeDisabled();
    fireEvent.change(within(panel).getByLabelText('Successor'), { target: { value: '9' } });
    fireEvent.click(submit);
    expect(state.handOver.mutateAsync).not.toHaveBeenCalled();
    fireEvent.click(within(panel).getByRole('button', { name: 'Confirm handover' }));
    await waitFor(() => expect(state.handOver.mutateAsync).toHaveBeenCalledWith(9));
    expect(toast.success).toHaveBeenCalledWith('Mailbox handed over');
  });

  it('offers no handover to someone who does not administer the mailbox', () => {
    state.organizations.data = page([organization('member')]);
    state.mailboxes.data = [
      mailbox(2, 'riley', {
        type: 'personal',
        owner_user_id: 5,
        access: { ...fullAccess, manage_members: false, administer: false },
      }),
    ];
    renderWorkspace();
    fireEvent.click(screen.getByRole('button', { name: 'Access to riley@mail.example.com' }));
    expect(within(screen.getByRole('dialog')).queryByLabelText('Successor')).toBeNull();
  });

  it('deletes sealed history only after the address is typed', async () => {
    state.deleteSealed.mutateAsync.mockResolvedValue({
      deleted_messages: 3,
      deleted_broadcasts: 1,
    });
    state.mailboxes.data = [
      mailbox(1, 'hello', { sealed_before: '2026-11-18T09:00:00Z', access: fullAccess }),
    ];
    renderWorkspace();
    fireEvent.click(screen.getByRole('button', { name: 'Access to hello@mail.example.com' }));
    fireEvent.click(
      within(screen.getByRole('dialog')).getByRole('button', { name: 'Delete sealed history' })
    );
    const confirm = screen.getByRole('alertdialog');
    expect(confirm).toHaveTextContent(/cannot be undone/u);

    const remove = within(confirm).getByRole('button', { name: 'Delete for good' });
    expect(remove).toBeDisabled();
    const input = within(confirm).getByLabelText('Mailbox address');
    fireEvent.change(input, { target: { value: 'hello@mail.example' } });
    expect(remove).toBeDisabled();
    fireEvent.change(input, { target: { value: ' HELLO@mail.example.com ' } });
    expect(remove).toBeEnabled();
    fireEvent.click(remove);
    await waitFor(() =>
      expect(state.deleteSealed.mutateAsync).toHaveBeenCalledWith('HELLO@mail.example.com')
    );
    expect(toast.success).toHaveBeenCalledWith('Deleted 3 sealed messages and 1 broadcasts.');
  });

  it('keeps the sealed-history confirmation open when the deletion fails', async () => {
    state.deleteSealed.mutateAsync.mockRejectedValueOnce(
      new ApiError('backend text', ApiErrorCode.MAILBOX_ACCESS_INVALID, 422)
    );
    state.mailboxes.data = [
      mailbox(1, 'hello', { sealed_before: '2026-11-18T09:00:00Z', access: fullAccess }),
    ];
    renderWorkspace();
    fireEvent.click(screen.getByRole('button', { name: 'Access to hello@mail.example.com' }));
    fireEvent.click(
      within(screen.getByRole('dialog')).getByRole('button', { name: 'Delete sealed history' })
    );
    const confirm = screen.getByRole('alertdialog');
    fireEvent.change(within(confirm).getByLabelText('Mailbox address'), {
      target: { value: 'hello@mail.example.com' },
    });
    fireEvent.click(within(confirm).getByRole('button', { name: 'Delete for good' }));

    expect(await within(confirm).findByRole('alert')).toBeInTheDocument();
    expect(screen.getByRole('alertdialog')).toBe(confirm);
    expect(within(confirm).getByLabelText('Mailbox address')).toHaveValue('hello@mail.example.com');
    expect(toast.success).not.toHaveBeenCalled();
  });

  it('closes the sealed-history confirmation and asks for a refresh when access was lost', async () => {
    state.deleteSealed.mutateAsync.mockRejectedValueOnce(
      new ApiError('backend text', ApiErrorCode.PERMISSION_DENIED, 403)
    );
    state.mailboxes.data = [
      mailbox(1, 'hello', { sealed_before: '2026-11-18T09:00:00Z', access: fullAccess }),
    ];
    renderWorkspace();
    fireEvent.click(screen.getByRole('button', { name: 'Access to hello@mail.example.com' }));
    fireEvent.click(
      within(screen.getByRole('dialog')).getByRole('button', { name: 'Delete sealed history' })
    );
    const confirm = screen.getByRole('alertdialog');
    fireEvent.change(within(confirm).getByLabelText('Mailbox address'), {
      target: { value: 'hello@mail.example.com' },
    });
    fireEvent.click(within(confirm).getByRole('button', { name: 'Delete for good' }));

    await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull());
    const panel = screen.getByRole('dialog');
    expect(within(panel).getByRole('alert')).toHaveTextContent(
      "You don't have permission for this mailbox. Refresh to see your current access."
    );
    expect(within(panel).getByRole('button', { name: 'Refresh' })).toBeInTheDocument();
  });

  it('offers a refresh on a refused change and refetches only the sections it shows', async () => {
    state.handOver.mutateAsync.mockRejectedValue(
      new ApiError('backend text', ApiErrorCode.PERMISSION_DENIED, 403)
    );
    state.mailboxes.data = [
      mailbox(1, 'hello', {
        type: 'personal',
        owner_user_id: 5,
        awaiting_handover: true,
        access: { ...fullAccess, see_history: false },
      }),
    ];
    state.members.refetch.mockReset();
    state.events.refetch.mockReset();
    state.mailboxes.refetch.mockReset();
    renderWorkspace();
    fireEvent.click(screen.getByRole('button', { name: 'Access to hello@mail.example.com' }));
    const panel = screen.getByRole('dialog');
    fireEvent.change(within(panel).getByLabelText('Successor'), { target: { value: '9' } });
    fireEvent.click(within(panel).getByRole('button', { name: 'Hand over' }));
    fireEvent.click(within(panel).getByRole('button', { name: 'Confirm handover' }));

    expect(await within(panel).findByRole('alert')).toHaveTextContent(
      "You don't have permission for this mailbox. Refresh to see your current access."
    );
    fireEvent.click(within(panel).getByRole('button', { name: 'Refresh' }));
    expect(state.mailboxes.refetch).toHaveBeenCalled();
    expect(state.members.refetch).not.toHaveBeenCalled();
    expect(state.events.refetch).not.toHaveBeenCalled();
  });

  it('asks for a refresh when the access changed in the meantime', async () => {
    state.setMember.mutateAsync.mockRejectedValue(
      new ApiError('backend text', ApiErrorCode.MAILBOX_ACCESS_CONFLICT, 409)
    );
    renderWorkspace();
    fireEvent.click(screen.getByRole('button', { name: 'Access to hello@mail.example.com' }));
    const panel = screen.getByRole('dialog');
    fireEvent.change(within(panel).getByLabelText('Permission of Riley Chen'), {
      target: { value: 'manage' },
    });

    expect(await within(panel).findByRole('alert')).toHaveTextContent(
      'This mailbox changed in the meantime. Refresh to see its current access.'
    );
    expect(screen.queryByText('backend text')).toBeNull();
    fireEvent.click(within(panel).getByRole('button', { name: 'Refresh' }));
    expect(state.mailboxes.refetch).toHaveBeenCalled();
    expect(state.members.refetch).toHaveBeenCalled();
    expect(state.events.refetch).toHaveBeenCalled();
  });
});

const now = '2026-11-19T10:00:00Z';

function domain() {
  return {
    domain: 'mail.example.com',
    kind: 'shared',
    max_mailboxes: 50,
    provider: 'log',
    inbound_enabled: false,
    daily_send_limit: 200,
    daily_broadcast_limit: 200,
    daily_transactional_limit: 200,
    transactional_enabled: true,
    transactional_recipient_limit_per_minute: 3,
    transactional_key_limit_per_second: 20,
    simulator_domain: 'test.zmail.im',
    broadcasts_enabled: true,
    custom_domains_enabled: false,
  };
}

function organization(role: 'owner' | 'admin' | 'member') {
  return { id: 1, name: 'Acme', slug: 'acme', role, created_at: now, updated_at: now };
}

function orgMember(userId: number, username: string, nickname: string) {
  return {
    id: userId + 100,
    user_id: userId,
    username,
    nickname,
    role: 'member',
    joined_at: now,
    updated_at: now,
  };
}

function mailbox(id: number, localPart: string, overrides: Record<string, unknown> = {}) {
  return {
    id,
    address: `${localPart}@mail.example.com`,
    display_name: '',
    type: 'shared',
    owner_user_id: null,
    awaiting_handover: false,
    sealed_before: null,
    webhooks_enabled: false,
    access: fullAccess,
    unread_count: 0,
    created_at: now,
    ...overrides,
  };
}

function event(
  id: number,
  type: string,
  actor: number | null,
  subject: number | null,
  detail: Record<string, unknown>
) {
  return { id, type, actor_user_id: actor, subject_user_id: subject, detail, created_at: now };
}

function summary() {
  return {
    id: 11,
    mailbox_id: 1,
    direction: 'inbound',
    status: 'received',
    from: { address: 'bob@example.com', name: 'Bob' },
    to: ['hello@mail.example.com'],
    cc: [],
    subject: 'Unread question',
    read: false,
    failure_code: '',
    queued_reason: null,
    last_event: '',
    attachment_count: 0,
    template_id: null,
    created_at: now,
    placement: 'default',
    folder_id: null,
    label_ids: [],
    starred: false,
    thread_id: 11,
    trashed_at: null,
  };
}

function message() {
  return {
    ...summary(),
    text_body: 'Plain question',
    html_body: '',
    message_id: 'question-1@example.com',
    in_reply_to: '',
    events: [],
    attachments: [],
  };
}

function page(items: unknown[], meta: Record<string, number> = {}) {
  return {
    items,
    meta: {
      current_page: 1,
      per_page: 20,
      total: items.length,
      last_page: 1,
      from: items.length ? 1 : 0,
      to: items.length,
      ...meta,
    },
    links: { first: '', last: '', prev: null, next: null },
  };
}

function renderWorkspace() {
  return render(
    <NextIntlClientProvider locale="en-US" messages={messages as unknown as AbstractIntlMessages}>
      <MailboxWorkspace />
    </NextIntlClientProvider>
  );
}
