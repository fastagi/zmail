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
    /** The organization id each mailbox hook was called with, in call order. */
    organizationIds: [] as number[],
    domains: query(),
    mailboxes: query(),
    inbox: query(),
    sent: query(),
    archive: query(),
    trash: query(),
    starred: query(),
    labelled: query(),
    folder: query(),
    search: query(),
    detail: query(),
    thread: query(),
    counts: query(),
    folders: query(),
    labels: query(),
    listCalls: [] as { mailboxId: number; direction: string | undefined; page: number }[],
    filterCalls: [] as { mailboxId: number; filters: Record<string, unknown>; page: number }[],
    detailCalls: [] as (number | null)[],
    threadCalls: [] as (number | null)[],
    createMailbox: mutation(),
    deleteMailbox: mutation(),
    send: mutation(),
    update: mutation(),
    updateMany: mutation(),
    purge: mutation(),
    removeMessage: mutation(),
    createFolder: mutation(),
    renameFolder: mutation(),
    deleteFolder: mutation(),
    createLabel: mutation(),
    updateLabel: mutation(),
    deleteLabel: mutation(),
    reveal: mutation(),
  };
});

/** The fixture a listing hook answers with, chosen by its filters as the API would. */
function listFor(filters: Record<string, unknown>) {
  if (filters.q) return state.search;
  if (filters.placement === 'trash') return state.trash;
  if (filters.placement === 'archive') return state.archive;
  if (filters.placement === 'folder') return state.folder;
  if (filters.starred) return state.starred;
  if (filters.labelId) return state.labelled;
  return filters.direction === 'outbound' ? state.sent : state.inbox;
}

/** A router whose `replace` changes the URL the page reads, as the real one does. */
const navigation = vi.hoisted(() => {
  const listeners = new Set<() => void>();
  return {
    listeners,
    replace: vi.fn((url: string) => {
      window.history.replaceState(null, '', url);
      for (const listener of listeners) listener();
    }),
  };
});

const downloadAttachment = vi.hoisted(() => vi.fn());

vi.mock('sonner', () => ({ toast }));
vi.mock('@/features/mailbox/services/mailbox-service', async importOriginal => {
  const original =
    await importOriginal<typeof import('@/features/mailbox/services/mailbox-service')>();
  return { ...original, mailboxService: { ...original.mailboxService, downloadAttachment } };
});
vi.mock('next/navigation', async () => {
  const { useSyncExternalStore } = await import('react');
  const router = { replace: navigation.replace };
  const subscribe = (listener: () => void) => {
    navigation.listeners.add(listener);
    return () => navigation.listeners.delete(listener);
  };
  return {
    useRouter: () => router,
    usePathname: () => window.location.pathname,
    useSearchParams: () =>
      new URLSearchParams(useSyncExternalStore(subscribe, () => window.location.search)),
  };
});
vi.mock('@/features/organization/hooks/use-organizations', () => ({
  useOrganizations: () => state.organizations,
  useOrganizationMembers: () => ({
    data: page([
      { id: 101, user_id: 1, username: 'admin', nickname: 'Admin User', role: 'owner' },
      { id: 105, user_id: 5, username: 'riley', nickname: 'Riley Chen', role: 'member' },
    ]),
    isPending: false,
    error: null,
    refetch: vi.fn(),
  }),
}));
const templateState = vi.hoisted(() => ({ data: [] as unknown[] }));
vi.mock('@/features/mailbox/hooks/use-mail-templates', () => ({
  useMailTemplates: () => templateState,
}));
// Mailbox administration offers each mailbox's mail role; these tests have no roles.
vi.mock('@/features/mailbox/hooks/use-inbox', () => ({
  useMailRoles: () => ({ data: [], isPending: false, error: null }),
  useAssignMailboxRole: () => ({ isPending: false, mutateAsync: vi.fn() }),
}));
vi.mock('@/features/mailbox/hooks/use-mailbox', () => {
  const scoped = <T,>(organizationId: number, result: T): T => {
    state.organizationIds.push(organizationId);
    return result;
  };
  return {
    useMailboxDomains: (organizationId: number) => scoped(organizationId, state.domains),
    useMailboxes: (organizationId: number) => scoped(organizationId, state.mailboxes),
    useMailMessageList: (
      organizationId: number,
      mailboxId: number,
      filters: Record<string, unknown>,
      page: number
    ) => {
      state.listCalls.push({ mailboxId, direction: filters.direction as string | undefined, page });
      state.filterCalls.push({ mailboxId, filters, page });
      return scoped(organizationId, listFor(filters));
    },
    useMailMessage: (organizationId: number, _mailboxId: number, messageId: number | null) => {
      state.detailCalls.push(messageId);
      return scoped(organizationId, state.detail);
    },
    // Without a thread fixture, a thread is the one message of the detail fixture.
    useMailThread: (organizationId: number, _mailboxId: number, threadId: number | null) => {
      state.threadCalls.push(threadId);
      if (threadId !== null) state.detailCalls.push(threadId);
      if (state.thread.data !== undefined || state.thread.error) {
        return scoped(organizationId, state.thread);
      }
      return scoped(organizationId, {
        ...state.detail,
        data: state.detail.data === undefined ? undefined : [state.detail.data],
      });
    },
    useMailMessageCounts: (organizationId: number) => scoped(organizationId, state.counts),
    useMailFolders: (organizationId: number) => scoped(organizationId, state.folders),
    useMailLabels: (organizationId: number) => scoped(organizationId, state.labels),
    useUpdateMailMessages: (organizationId: number) => scoped(organizationId, state.updateMany),
    usePurgeMailTrash: (organizationId: number) => scoped(organizationId, state.purge),
    useCreateMailFolder: (organizationId: number) => scoped(organizationId, state.createFolder),
    useRenameMailFolder: (organizationId: number) => scoped(organizationId, state.renameFolder),
    useDeleteMailFolder: (organizationId: number) => scoped(organizationId, state.deleteFolder),
    useCreateMailLabel: (organizationId: number) => scoped(organizationId, state.createLabel),
    useUpdateMailLabel: (organizationId: number) => scoped(organizationId, state.updateLabel),
    useDeleteMailLabel: (organizationId: number) => scoped(organizationId, state.deleteLabel),
    useCreateMailbox: (organizationId: number) => scoped(organizationId, state.createMailbox),
    useDeleteMailbox: (organizationId: number) => scoped(organizationId, state.deleteMailbox),
    useSendMailMessage: (organizationId: number) => scoped(organizationId, state.send),
    useUpdateMailMessage: (organizationId: number) => scoped(organizationId, state.update),
    useDeleteMailMessage: (organizationId: number) => scoped(organizationId, state.removeMessage),
    useRevealMailMessage: (organizationId: number) => scoped(organizationId, state.reveal),
  };
});

describe('mailbox workspace', () => {
  beforeEach(() => {
    toast.success.mockReset();
    toast.error.mockReset();
    templateState.data = [];
    navigation.replace.mockClear();
    window.sessionStorage.clear();
    window.localStorage.clear();
    window.history.replaceState(null, '', '/console/mailboxes');
    state.organizations.data = organizations(organization(1, 'Acme', 'owner'));
    state.organizationIds = [];
    state.domains.data = [domain(10)];
    state.mailboxes.data = [mailbox(1, 'hello', 'Hello Team'), mailbox(2, 'sales', '')];
    state.inbox.data = page([
      summary({ id: 11, subject: 'Unread question', read: false }),
      summary({ id: 10, subject: 'Old news', read: true }),
    ]);
    state.sent.data = page([
      summary({
        id: 21,
        direction: 'outbound',
        status: 'failed',
        failure_code: 'provider.rejected',
        subject: 'Bounced offer',
        to: ['bob@example.com'],
        read: true,
      }),
    ]);
    state.detail.data = message();
    state.thread.data = undefined;
    for (const list of [
      state.archive,
      state.trash,
      state.starred,
      state.labelled,
      state.folder,
      state.search,
    ]) {
      list.data = page([]);
    }
    state.counts.data = {
      placements: { default_inbound_unread: 1, archive_unread: 0 },
      folders: [],
      starred: 0,
    };
    state.folders.data = [];
    state.labels.data = [];
    for (const query of [
      state.organizations,
      state.domains,
      state.mailboxes,
      state.inbox,
      state.sent,
      state.archive,
      state.trash,
      state.starred,
      state.labelled,
      state.folder,
      state.search,
      state.detail,
      state.thread,
      state.counts,
      state.folders,
      state.labels,
    ]) {
      query.isPending = false;
      query.error = null;
      query.refetch.mockReset();
    }
    state.listCalls = [];
    state.filterCalls = [];
    state.detailCalls = [];
    state.threadCalls = [];
    for (const mutation of [
      state.createMailbox,
      state.deleteMailbox,
      state.send,
      state.update,
      state.updateMany,
      state.purge,
      state.removeMessage,
      state.createFolder,
      state.renameFolder,
      state.deleteFolder,
      state.createLabel,
      state.updateLabel,
      state.deleteLabel,
    ]) {
      mutation.isPending = false;
      mutation.mutate.mockReset();
      mutation.mutateAsync.mockReset();
    }
  });

  it('shows the inbox of the first mailbox and lists the mailboxes in the switcher', () => {
    renderWorkspace();

    expect(
      screen.getByRole('button', { name: /^Switch mailbox, current: hello@mail\.example\.com/u })
    ).toHaveTextContent('Hello Team');
    expect(state.listCalls.at(-1)).toEqual({ mailboxId: 1, direction: 'inbound', page: 1 });
    expect(screen.getByText('Unread question')).toBeInTheDocument();
    expect(screen.getAllByText('Unread')).toHaveLength(1);

    openSwitcher();
    expect(screen.getByRole('button', { name: /^hello@mail\.example\.com/u })).toHaveAttribute(
      'aria-current',
      'true'
    );
    expect(screen.getByText(/^No display name/u)).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: /^sales@mail\.example\.com/u }));
    expect(state.listCalls.at(-1)).toEqual({ mailboxId: 2, direction: 'inbound', page: 1 });

    // The limit and the domain are part of mailbox administration.
    openManage();
    const dialog = screen.getByRole('dialog', { name: 'Manage mailboxes' });
    expect(within(dialog).getByText('2 of 10 used')).toBeInTheDocument();
    expect(within(dialog).getByText('@mail.example.com')).toBeInTheDocument();
  });

  it('names the only organization as text and asks every hook for it', () => {
    renderWorkspace();

    expect(screen.getByTestId('mail-organization-name')).toHaveTextContent('Acme');
    expect(screen.queryByLabelText('Organization')).toBeNull();
    // The fallback choice is written into the URL, so a reload or a link keeps it.
    expect(navigation.replace).toHaveBeenCalledWith('/console/mailboxes?org=1', { scroll: false });
    expect(window.location.search).toBe('?org=1');
    expect(state.organizationIds.length).toBeGreaterThan(0);
    expect(new Set(state.organizationIds)).toEqual(new Set([1]));
  });

  it('offers a picker with two organizations and shows the chosen one', () => {
    state.organizations.data = organizations(
      organization(1, 'Acme', 'owner'),
      organization(2, 'Globex', 'member')
    );
    window.history.replaceState(null, '', '/console/mailboxes?org=2');
    renderWorkspace();

    const picker = screen.getByLabelText('Organization') as HTMLSelectElement;
    expect([...picker.options].map(option => [option.value, option.text])).toEqual([
      ['1', 'Acme'],
      ['2', 'Globex'],
    ]);
    expect(picker).toHaveValue('2');
    expect(screen.queryByTestId('mail-organization-name')).toBeNull();
    // A valid choice in the URL is used as it is.
    expect(navigation.replace).not.toHaveBeenCalled();
    expect(new Set(state.organizationIds)).toEqual(new Set([2]));

    state.organizationIds = [];
    fireEvent.change(picker, { target: { value: '1' } });
    expect(navigation.replace).toHaveBeenCalledWith('/console/mailboxes?org=1', { scroll: false });
    expect(screen.getByLabelText('Organization')).toHaveValue('1');
    expect(new Set(state.organizationIds)).toEqual(new Set([1]));
  });

  it('falls back to the first organization when the URL names one the user is not in', () => {
    state.organizations.data = organizations(
      organization(4, 'Acme', 'owner'),
      organization(5, 'Globex', 'member')
    );
    for (const search of ['?org=9', '?org=abc', '?org=0', '?org=', '?other=1']) {
      window.sessionStorage.clear();
      window.history.replaceState(null, '', `/console/mailboxes${search}`);
      state.organizationIds = [];
      const view = renderWorkspace();

      expect(screen.getByLabelText('Organization'), search).toHaveValue('4');
      expect(new URLSearchParams(window.location.search).get('org'), search).toBe('4');
      expect(new Set(state.organizationIds), search).toEqual(new Set([4]));
      view.unmount();
    }
    // Other parameters of the page are kept when the organization is written.
    expect(window.location.search).toBe('?other=1&org=4');
  });

  it('restores the last chosen organization from sessionStorage when navigating without ?org=', () => {
    state.organizations.data = organizations(
      organization(1, 'Acme', 'owner'),
      organization(2, 'Globex', 'member')
    );
    window.history.replaceState(null, '', '/console/mailboxes?org=2');
    const firstVisit = renderWorkspace();
    expect(window.sessionStorage.getItem('zmail.activeOrganizationId')).toBe('2');
    firstVisit.unmount();

    navigation.replace.mockClear();
    state.organizationIds = [];
    window.history.replaceState(null, '', '/console/mailboxes');
    const sidebarVisit = renderWorkspace();

    expect(screen.getByLabelText('Organization')).toHaveValue('2');
    expect(navigation.replace).toHaveBeenCalledWith('/console/mailboxes?org=2', { scroll: false });
    expect(window.location.search).toBe('?org=2');
    expect(new Set(state.organizationIds)).toEqual(new Set([2]));
    sidebarVisit.unmount();

    // An explicit ?org= in the URL still overrides sessionStorage and updates it.
    state.organizationIds = [];
    window.history.replaceState(null, '', '/console/mailboxes?org=1');
    renderWorkspace();
    expect(screen.getByLabelText('Organization')).toHaveValue('1');
    expect(window.sessionStorage.getItem('zmail.activeOrganizationId')).toBe('1');
    expect(new Set(state.organizationIds)).toEqual(new Set([1]));
  });

  it('lets an owner and an admin manage mailboxes and gives a member only the mail', () => {
    for (const role of ['owner', 'admin'] as const) {
      state.organizations.data = organizations(organization(1, 'Acme', role));
      const view = renderWorkspace();
      openManage();
      const dialog = screen.getByRole('dialog', { name: 'Manage mailboxes' });
      expect(
        within(dialog).getByRole('button', { name: 'Create mailbox' }),
        role
      ).toBeInTheDocument();
      expect(within(dialog).getAllByRole('button', { name: /^Delete /u }), role).toHaveLength(2);
      view.unmount();
    }

    state.organizations.data = organizations(organization(1, 'Acme', 'member'));
    // A member working both mailboxes with `send` administers neither.
    const memberAccess = {
      view: true,
      send: true,
      manage_members: false,
      administer: false,
      see_history: false,
      organize: false,
    };
    state.mailboxes.data = [mailbox(1, 'hello', 'Hello Team'), mailbox(2, 'sales', '')].map(
      item => ({ ...item, access: memberAccess })
    );
    renderWorkspace();
    openSwitcher();
    expect(screen.queryByRole('button', { name: 'Manage mailboxes' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Create mailbox' })).toBeNull();
    expect(screen.queryByLabelText('Address')).toBeNull();
    expect(screen.queryByRole('button', { name: /^Delete .*@/u })).toBeNull();
    // Reading and sending stay available to a member.
    expect(screen.getByText('Unread question')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Compose' })).toBeEnabled();
    fireEvent.click(rowButton('Unread question'));
    expect(screen.getByRole('button', { name: 'Reply' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Mark unread' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Delete' })).toBeInTheDocument();
  });

  it('tells a member without mailboxes who can create one', () => {
    state.organizations.data = organizations(organization(1, 'Acme', 'member'));
    state.mailboxes.data = [];
    renderWorkspace();

    expect(screen.getByText('No mailboxes yet')).toBeInTheDocument();
    expect(
      screen.getByText('Owners and admins of this organization create and delete mailboxes.')
    ).toBeInTheDocument();
    expect(screen.queryByText('Create an address to start receiving and sending mail.')).toBeNull();
  });

  it('explains that mailboxes need an organization when the user has none', () => {
    state.organizations.data = organizations();
    renderWorkspace();

    expect(screen.getByRole('heading', { name: 'Mailboxes' })).toBeInTheDocument();
    expect(screen.getByText('No organization yet')).toBeInTheDocument();
    expect(screen.getByText(/Mailboxes belong to an organization/u)).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Go to organizations' })).toHaveAttribute(
      'href',
      '/console/organizations'
    );
    expect(screen.queryByLabelText('Organization')).toBeNull();
    // Nothing is asked of the mailbox API without an organization.
    expect(state.organizationIds).toEqual([]);
    expect(navigation.replace).not.toHaveBeenCalled();
  });

  it('shows loading and a retryable error for the organization list', () => {
    state.organizations.data = undefined;
    state.organizations.isPending = true;
    const view = renderWorkspace();
    expect(screen.getByRole('heading', { name: 'Mailboxes' })).toBeInTheDocument();
    expect(screen.queryByText('No organization yet')).toBeNull();
    expect(state.organizationIds).toEqual([]);
    view.unmount();

    state.organizations.isPending = false;
    state.organizations.error = new ApiError('boom', ApiErrorCode.COMMON_SERVICE_UNAVAILABLE);
    renderWorkspace();
    expect(screen.getByText('Your organizations could not be loaded.')).toBeInTheDocument();
    expect(screen.queryByText('boom')).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(state.organizations.refetch).toHaveBeenCalled();
    expect(state.organizationIds).toEqual([]);
  });

  it('leads back to the organizations when access to the organization was lost', () => {
    state.mailboxes.data = undefined;
    state.mailboxes.error = new ApiError('gone', ApiErrorCode.ORGANIZATION_NOT_FOUND, 404);
    renderWorkspace();

    expect(screen.getByText('No access to this organization')).toBeInTheDocument();
    expect(screen.getByText(/you are no longer a member of it/u)).toBeInTheDocument();
    expect(screen.queryByText('gone')).toBeNull();
    expect(screen.getByRole('link', { name: 'Go to organizations' })).toHaveAttribute(
      'href',
      '/console/organizations'
    );
    expect(screen.queryByRole('button', { name: 'Retry' })).toBeNull();
  });

  it('creates a mailbox with a normalized address and selects it', async () => {
    state.createMailbox.mutateAsync.mockResolvedValue(mailbox(3, 'support-team', 'Support Team'));
    renderWorkspace();
    openManage();

    fireEvent.change(screen.getByLabelText('Address'), { target: { value: ' Support-Team ' } });
    fireEvent.change(screen.getByLabelText('Display name (optional)'), {
      target: { value: ' Support   Team ' },
    });
    fireEvent.change(screen.getByLabelText('Member 1'), { target: { value: '5' } });
    fireEvent.click(screen.getByRole('button', { name: 'Create mailbox' }));

    await waitFor(() =>
      expect(state.createMailbox.mutateAsync).toHaveBeenCalledWith({
        local_part: 'support-team',
        display_name: 'Support Team',
        type: 'shared',
        members: [{ user_id: 5, permission: 'send' }],
      })
    );
    expect(toast.success).toHaveBeenCalledWith('Mailbox created');
    expect(screen.getByLabelText('Address')).toHaveValue('');
    // The new mailbox becomes the open one.
    expect(new URLSearchParams(window.location.search).get('mailbox')).toBe('3');
  });

  it('explains address errors without sending and maps the API codes', async () => {
    renderWorkspace();
    openManage();
    const address = screen.getByLabelText('Address');
    const submit = screen.getByRole('button', { name: 'Create mailbox' });

    fireEvent.change(address, { target: { value: 'postmaster' } });
    fireEvent.click(submit);
    expect(await screen.findByRole('alert')).toHaveTextContent(
      /reserved for the mail administrator/u
    );
    fireEvent.change(address, { target: { value: 'a..b' } });
    fireEvent.click(submit);
    expect(await screen.findByRole('alert')).toHaveTextContent(/1 to 64 lowercase letters/u);
    expect(state.createMailbox.mutateAsync).not.toHaveBeenCalled();

    const cases = [
      [ApiErrorCode.MAILBOX_ADDRESS_TAKEN, /already in use or was used before/u],
      [ApiErrorCode.MAILBOX_ADDRESS_INVALID, /1 to 64 lowercase letters/u],
      [ApiErrorCode.MAILBOX_ADDRESS_RESERVED, /reserved for the mail administrator/u],
      [ApiErrorCode.MAILBOX_LIMIT_REACHED, /reached the mailbox limit/u],
      [
        ApiErrorCode.PERMISSION_DENIED,
        /Only owners and admins of the organization can create mailboxes/u,
      ],
    ] as const;
    fireEvent.change(address, { target: { value: 'hello' } });
    fireEvent.change(screen.getByLabelText('Member 1'), { target: { value: '5' } });
    for (const [code, copy] of cases) {
      state.createMailbox.mutateAsync.mockRejectedValueOnce(new ApiError('backend text', code));
      fireEvent.click(submit);
      await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent(copy));
    }
    expect(screen.queryByText('backend text')).toBeNull();
  });

  it('replaces the create form with the limit once it is reached', () => {
    state.domains.data = [domain(2)];
    renderWorkspace();
    openManage();

    expect(screen.getByText(/reached the limit of 2 mailboxes/u)).toBeInTheDocument();
    expect(screen.queryByLabelText('Address')).toBeNull();
    expect(screen.queryByRole('button', { name: 'Create mailbox' })).toBeNull();
  });

  it('deletes a mailbox only after confirmation', async () => {
    state.deleteMailbox.mutateAsync.mockResolvedValue(undefined);
    renderWorkspace();
    openManage();

    fireEvent.click(screen.getByRole('button', { name: 'Delete sales@mail.example.com' }));
    expect(state.deleteMailbox.mutateAsync).not.toHaveBeenCalled();
    const dialog = screen.getByRole('alertdialog');
    expect(dialog).toHaveTextContent(/sales@mail\.example\.com and every message in it/u);
    fireEvent.click(within(dialog).getByRole('button', { name: 'Delete' }));

    await waitFor(() => expect(state.deleteMailbox.mutateAsync).toHaveBeenCalledWith(2));
    expect(toast.success).toHaveBeenCalledWith('Mailbox deleted');
  });

  it('says the mailbox permission is missing when the API refuses a delete', async () => {
    state.deleteMailbox.mutateAsync.mockRejectedValue(
      new ApiError('backend text', ApiErrorCode.PERMISSION_DENIED, 403)
    );
    renderWorkspace();
    openManage();

    fireEvent.click(screen.getByRole('button', { name: 'Delete sales@mail.example.com' }));
    fireEvent.click(
      within(screen.getByRole('alertdialog')).getByRole('button', { name: 'Delete' })
    );

    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith(
        "You don't have permission for this mailbox. Refresh to see your current access."
      )
    );
    expect(toast.success).not.toHaveBeenCalled();
  });

  it('marks an unread inbound message read when it is opened, and only then', () => {
    renderWorkspace();

    fireEvent.click(rowButton('Old news'));
    expect(state.detailCalls.at(-1)).toBe(10);
    expect(state.update.mutate).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'Back to list' }));

    fireEvent.click(rowButton('Unread question'));
    expect(state.detailCalls.at(-1)).toBe(11);
    expect(state.update.mutate).toHaveBeenCalledTimes(1);
    expect(state.update.mutate).toHaveBeenCalledWith({ messageId: 11, read: true });
  });

  it('steps through messages with Previous and Next and marks unread inbound mail read', () => {
    state.inbox.data = page([
      summary({ id: 13, subject: 'Newest unread', read: false }),
      summary({ id: 12, subject: 'Middle read', read: true }),
      summary({ id: 11, subject: 'Oldest unread', read: false }),
    ]);
    renderWorkspace();

    // Start on the middle read message so neither unread neighbor has been opened yet.
    const middleRow = rowButton('Middle read');
    fireEvent.click(middleRow);
    expect(middleRow).toHaveAttribute('aria-current', 'true');
    expect(state.detailCalls.at(-1)).toBe(12);
    expect(state.update.mutate).not.toHaveBeenCalled();

    const prevButton = screen.getByRole('button', { name: 'Previous message' });
    const nextButton = screen.getByRole('button', { name: 'Next message' });
    expect(prevButton).toBeEnabled();
    expect(nextButton).toBeEnabled();

    // Stepping forward opens the older unread message and marks it read.
    fireEvent.click(nextButton);
    expect(state.detailCalls.at(-1)).toBe(11);
    expect(rowButton('Oldest unread')).toHaveAttribute('aria-current', 'true');
    expect(middleRow).not.toHaveAttribute('aria-current');
    expect(state.update.mutate).toHaveBeenCalledTimes(1);
    expect(state.update.mutate).toHaveBeenCalledWith({ messageId: 11, read: true });
    expect(nextButton).toBeDisabled();
    expect(prevButton).toBeEnabled();

    // Stepping back twice returns through the read message to the newest unread message.
    fireEvent.click(prevButton);
    expect(state.detailCalls.at(-1)).toBe(12);
    expect(state.update.mutate).toHaveBeenCalledTimes(1);

    fireEvent.click(prevButton);
    expect(state.detailCalls.at(-1)).toBe(13);
    expect(rowButton('Newest unread')).toHaveAttribute('aria-current', 'true');
    expect(state.update.mutate).toHaveBeenCalledTimes(2);
    expect(state.update.mutate).toHaveBeenLastCalledWith({ messageId: 13, read: true });
    expect(prevButton).toBeDisabled();
    expect(nextButton).toBeEnabled();
  });

  it('renders sender HTML only inside a fully sandboxed frame', () => {
    state.detail.data = message({
      html_body: '<p id="from-sender">Hi</p><script>window.pwned = true</script>',
    });
    const { container } = renderWorkspace();
    fireEvent.click(rowButton('Unread question'));

    const frame = screen.getByTitle('Message content');
    expect(frame.tagName).toBe('IFRAME');
    expect(frame.hasAttribute('sandbox')).toBe(true);
    expect(frame.getAttribute('sandbox')).toBe('');
    expect(frame.getAttribute('srcdoc')).toContain('<p id="from-sender">Hi</p>');
    expect(frame.getAttribute('srcdoc')).toContain("default-src 'none'");
    expect(frame.hasAttribute('src')).toBe(false);
    // Nothing from the sender is parsed into the application document.
    expect(container.querySelector('#from-sender')).toBeNull();
    expect(document.querySelector('script')).toBeNull();
    expect(container.querySelectorAll('iframe')).toHaveLength(1);

    fireEvent.click(screen.getByRole('button', { name: 'Plain text' }));
    expect(container.querySelector('iframe')).toBeNull();
    expect(screen.getByText('Plain question')).toBeInTheDocument();
  });

  it('hides masked codes behind a panel and shows the original only on request', async () => {
    state.detail.data = message({
      subject: 'Your code is ••••••',
      text_body: 'Use code •••••• now',
      html_body: '',
      secrets_masked: true,
    });
    state.reveal.mutateAsync.mockReset();
    state.reveal.mutateAsync.mockResolvedValue(
      message({
        subject: 'Your code is 482913',
        text_body: 'Use code 482913 now',
        html_body: '',
        secrets_masked: false,
      })
    );
    renderWorkspace();
    fireEvent.click(rowButton('Unread question'));

    expect(
      screen.getByText("Codes are hidden. Only this mailbox's members can see them; AI and admins can't.")
    ).toBeInTheDocument();
    expect(screen.getByText('Use code •••••• now')).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Your code is ••••••' })).toBeInTheDocument();
    expect(screen.queryByText(/482913/)).toBeNull();

    fireEvent.click(screen.getByRole('button', { name: 'Show codes' }));
    await waitFor(() => expect(state.reveal.mutateAsync).toHaveBeenCalledWith(11));
    expect(await screen.findByText('Use code 482913 now')).toBeInTheDocument();
    // The subject above the message shows the original too, not the masked copy.
    expect(screen.getByRole('heading', { name: 'Your code is 482913' })).toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: 'Your code is ••••••' })).toBeNull();
    expect(screen.getByText('Codes shown')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Show codes' })).toBeNull();

    // Another message, then this one again: the reader starts masked.
    fireEvent.click(rowButton('Old news'));
    fireEvent.click(rowButton('Unread question'));
    expect(screen.getByRole('heading', { name: 'Your code is ••••••' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Show codes' })).toBeInTheDocument();
  });

  it('keeps Mark unread usable while a star is on its way', () => {
    state.detail.data = message({ html_body: '' });
    renderWorkspace();
    fireEvent.click(rowButton('Unread question'));
    Object.assign(state.update, { isPending: true, variables: { messageId: 11, starred: true } });
    fireEvent.click(rowButton('Old news'));

    expect(screen.getByRole('button', { name: 'Mark unread' })).toBeEnabled();
    Object.assign(state.update, { isPending: false, variables: undefined });
  });

  it('returns the focus to the row when the reader closes', () => {
    state.detail.data = message({ html_body: '' });
    renderWorkspace();
    fireEvent.click(rowButton('Unread question'));
    const back = screen.getByRole('button', { name: 'Back to list' });
    back.focus();

    fireEvent.click(back);

    expect(screen.queryByRole('button', { name: 'Back to list' })).toBeNull();
    expect(rowButton('Unread question')).toHaveFocus();
  });

  it('shows no code panel for a message with nothing masked', () => {
    state.detail.data = message({ html_body: '' });
    renderWorkspace();
    fireEvent.click(rowButton('Unread question'));
    expect(screen.queryByRole('button', { name: 'Show codes' })).toBeNull();
  });

  it('shows headers and the text body, and offers mark unread, reply, and delete', async () => {
    state.detail.data = message({ html_body: '', cc: ['carol@example.com'] });
    state.update.mutateAsync.mockResolvedValue(message());
    state.removeMessage.mutateAsync.mockResolvedValue(undefined);
    const { container } = renderWorkspace();
    fireEvent.click(rowButton('Unread question'));

    expect(screen.getByRole('heading', { name: 'Unread question' })).toBeInTheDocument();
    expect(screen.getByText('Bob <bob@example.com>')).toBeInTheDocument();
    expect(screen.getByText('carol@example.com')).toBeInTheDocument();
    expect(screen.getByText('Plain question')).toBeInTheDocument();
    expect(container.querySelector('iframe')).toBeNull();

    fireEvent.click(screen.getByRole('button', { name: 'Mark unread' }));
    await waitFor(() =>
      expect(state.update.mutateAsync).toHaveBeenCalledWith({ messageId: 11, read: false })
    );
    // Marking unread returns to the list instead of reopening (and re-reading) the message.
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Mark unread' })).toBeNull());

    fireEvent.click(rowButton('Unread question'));
    // Deleting moves the message to the trash at once, with an undo instead of a confirmation.
    fireEvent.click(screen.getByRole('button', { name: 'Delete' }));
    // A move, never DELETE: DELETE on a message someone else already trashed deletes it for good.
    await waitFor(() =>
      expect(state.update.mutateAsync).toHaveBeenCalledWith({ messageId: 11, placement: 'trash' })
    );
    expect(state.removeMessage.mutateAsync).not.toHaveBeenCalled();
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(toast.success).toHaveBeenCalledWith(
      'Moved to the trash',
      expect.objectContaining({ action: expect.objectContaining({ label: 'Undo' }) })
    );
  });

  it('undoes a move to the trash from the reader back to where the message was', async () => {
    state.inbox.data = page([summary({ id: 11, subject: 'Unread question', read: true })]);
    state.detail.data = message({ html_body: '', placement: 'folder', folder_id: 4, read: true });
    state.update.mutateAsync.mockResolvedValue(message());
    renderWorkspace();
    fireEvent.click(rowButton('Unread question'));
    fireEvent.click(screen.getByRole('button', { name: 'Delete' }));
    await waitFor(() => expect(toast.success).toHaveBeenCalled());

    undoLastToast();
    await waitFor(() =>
      expect(state.update.mutateAsync).toHaveBeenLastCalledWith({
        messageId: 11,
        placement: 'folder',
        folder_id: 4,
      })
    );
    expect(state.removeMessage.mutateAsync).not.toHaveBeenCalled();
  });

  it('switches to the sent folder and marks failed messages with the failure code', () => {
    renderWorkspace();

    fireEvent.click(screen.getByRole('button', { name: 'Sent' }));

    expect(state.listCalls.at(-1)).toEqual({ mailboxId: 1, direction: 'outbound', page: 1 });
    expect(screen.getByRole('button', { name: 'Sent' })).toHaveAttribute('aria-current', 'page');
    expect(screen.getByText('To: bob@example.com')).toBeInTheDocument();
    expect(screen.getByText('Failed')).toBeInTheDocument();
    expect(screen.getByText('provider.rejected')).toBeInTheDocument();
    expect(screen.queryByText('Unread')).toBeNull();

    state.detail.data = message({
      id: 21,
      direction: 'outbound',
      status: 'failed',
      failure_code: 'provider.rejected',
      html_body: '',
    });
    fireEvent.click(rowButton('Bounced offer'));
    expect(screen.getByText(/was not delivered\. Reason: provider\.rejected/u)).toBeInTheDocument();
    expect(state.update.mutate).not.toHaveBeenCalled();
    expect(screen.queryByRole('button', { name: 'Reply' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Mark unread' })).toBeNull();
  });

  it('shows the delivery summary of sent messages and keeps a failed send as failed', () => {
    const outbound = { direction: 'outbound', status: 'sent', read: true };
    state.sent.data = page([
      summary({
        ...outbound,
        id: 24,
        subject: 'Gone',
        to: ['a@example.com'],
        last_event: 'bounced',
      }),
      summary({
        ...outbound,
        id: 23,
        subject: 'Arrived',
        to: ['b@example.com'],
        last_event: 'delivered',
      }),
      summary({ ...outbound, id: 22, subject: 'Pending', to: ['c@example.com'] }),
      summary({
        ...outbound,
        id: 21,
        subject: 'Refused',
        to: ['d@example.com'],
        status: 'failed',
        failure_code: 'provider.rejected',
        last_event: 'rejected',
      }),
    ]);
    renderWorkspace();
    fireEvent.click(screen.getByRole('button', { name: 'Sent' }));

    const row = rowButton;
    expect(within(row('Gone')).getByText('Bounced')).toHaveAttribute('data-event', 'bounced');
    expect(within(row('Gone')).getByText('Bounced')).toHaveClass('text-error');
    expect(within(row('Arrived')).getByText('Delivered')).toHaveAttribute(
      'data-event',
      'delivered'
    );
    expect(within(row('Arrived')).getByText('Delivered')).toHaveClass('text-success');
    // Without an event a sent message carries no badge, as before.
    expect(row('Pending').querySelector('[data-slot="badge"]')).toBeNull();
    // The provider never took it: "Failed" and its code win over any event.
    expect(within(row('Refused')).getByText('Failed')).toBeInTheDocument();
    expect(within(row('Refused')).getByText('provider.rejected')).toBeInTheDocument();
    expect(within(row('Refused')).queryByText('Rejected')).toBeNull();
  });

  it('pages through a folder and shows empty states', () => {
    state.inbox.data = page([summary({ id: 11 })], { current_page: 1, last_page: 3 });
    const view = renderWorkspace();

    expect(screen.getByText('Page 1 of 3')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Previous' })).toBeDisabled();
    fireEvent.click(screen.getByRole('button', { name: 'Next' }));
    expect(state.listCalls.at(-1)).toEqual({ mailboxId: 1, direction: 'inbound', page: 2 });

    state.inbox.data = page([]);
    state.sent.data = page([]);
    view.rerender(wrap(<MailboxWorkspace />));
    expect(screen.getByText('Inbox is empty')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Sent' }));
    expect(screen.getByText('Nothing sent yet')).toBeInTheDocument();
  });

  it('validates a message before sending it', async () => {
    renderWorkspace();
    fireEvent.click(screen.getByRole('button', { name: 'Compose' }));
    const dialog = screen.getByRole('dialog');
    const submit = within(dialog).getByRole('button', { name: 'Send' });

    fireEvent.change(within(dialog).getByLabelText('To'), {
      target: { value: 'Bob <bob@example.com>' },
    });
    fireEvent.change(within(dialog).getByLabelText('Subject'), { target: { value: 'Hello' } });
    fireEvent.change(within(dialog).getByLabelText('Message'), { target: { value: 'Body' } });
    fireEvent.click(submit);
    expect(await within(dialog).findByText(/1 to 50 plain email addresses/u)).toBeInTheDocument();

    fireEvent.change(within(dialog).getByLabelText('To'), { target: { value: 'bob@example.com' } });
    fireEvent.change(within(dialog).getByLabelText('Message'), { target: { value: '   ' } });
    fireEvent.click(submit);
    expect(await within(dialog).findByText(/1 to 50 plain email addresses/u)).toBeInTheDocument();
    expect(state.send.mutateAsync).not.toHaveBeenCalled();
  });

  it('sends to comma and newline separated recipients, then shows the sent folder', async () => {
    state.send.mutateAsync.mockResolvedValue(message({ direction: 'outbound', status: 'sent' }));
    renderWorkspace();
    fireEvent.click(screen.getByRole('button', { name: 'Compose' }));
    const dialog = screen.getByRole('dialog');

    fireEvent.change(within(dialog).getByLabelText('To'), {
      target: { value: 'Bob@Example.com, carol@example.com\nbob@example.com' },
    });
    fireEvent.change(within(dialog).getByLabelText('Cc (optional)'), {
      target: { value: 'dave@example.com' },
    });
    fireEvent.change(within(dialog).getByLabelText('Subject'), { target: { value: ' Hello ' } });
    fireEvent.change(within(dialog).getByLabelText('Message'), { target: { value: 'Body text' } });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Send' }));

    await waitFor(() =>
      expect(state.send.mutateAsync).toHaveBeenCalledWith({
        to: ['bob@example.com', 'carol@example.com'],
        cc: ['dave@example.com'],
        subject: 'Hello',
        text: 'Body text',
      })
    );
    expect(toast.success).toHaveBeenCalledWith('Message sent');
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(state.listCalls.at(-1)).toEqual({ mailboxId: 1, direction: 'outbound', page: 1 });
  });

  it('tells the sender a send at the limit is queued, with no usage numbers', async () => {
    state.send.mutateAsync.mockResolvedValue(
      message({ direction: 'outbound', status: 'queued', queued_reason: 'send_limit' })
    );
    renderWorkspace();
    fireEvent.click(screen.getByRole('button', { name: 'Compose' }));
    fillValidMessage(screen.getByRole('dialog'));
    fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Send' }));

    await waitFor(() =>
      expect(toast.success).toHaveBeenCalledWith(
        'Send limit reached. Your message is queued and will be sent automatically.'
      )
    );
    expect(toast.success).not.toHaveBeenCalledWith('Message sent');
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
  });

  it('attaches files as base64, lets one be removed, and refuses too many', async () => {
    state.send.mutateAsync.mockResolvedValue(message({ direction: 'outbound', status: 'sent' }));
    renderWorkspace();
    fireEvent.click(screen.getByRole('button', { name: 'Compose' }));
    const dialog = screen.getByRole('dialog');
    fireEvent.change(within(dialog).getByLabelText('To'), { target: { value: 'bob@example.com' } });
    fireEvent.change(within(dialog).getByLabelText('Subject'), { target: { value: 'Files' } });
    fireEvent.change(within(dialog).getByLabelText('Message'), { target: { value: 'Attached.' } });

    const input = dialog.querySelector<HTMLInputElement>('input[type="file"]');
    if (!input) throw new Error('missing file input');
    fireEvent.change(input, {
      target: {
        files: [
          new File(['hello'], 'notes.txt', { type: 'text/plain' }),
          new File(['drop me'], 'drop.bin'),
        ],
      },
    });
    expect(within(dialog).getByText('notes.txt')).toBeInTheDocument();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Remove drop.bin' }));
    expect(within(dialog).queryByText('drop.bin')).toBeNull();

    fireEvent.click(within(dialog).getByRole('button', { name: 'Send' }));
    await waitFor(() =>
      expect(state.send.mutateAsync).toHaveBeenCalledWith({
        to: ['bob@example.com'],
        subject: 'Files',
        text: 'Attached.',
        attachments: [{ filename: 'notes.txt', content_type: 'text/plain', content: 'aGVsbG8=' }],
      })
    );
  });

  it('does not send more than ten files', async () => {
    renderWorkspace();
    fireEvent.click(screen.getByRole('button', { name: 'Compose' }));
    const dialog = screen.getByRole('dialog');
    fireEvent.change(within(dialog).getByLabelText('To'), { target: { value: 'bob@example.com' } });
    fireEvent.change(within(dialog).getByLabelText('Subject'), { target: { value: 'Files' } });
    fireEvent.change(within(dialog).getByLabelText('Message'), { target: { value: 'Attached.' } });
    const input = dialog.querySelector<HTMLInputElement>('input[type="file"]');
    if (!input) throw new Error('missing file input');
    fireEvent.change(input, {
      target: { files: Array.from({ length: 11 }, (_, index) => new File(['x'], `f${index}.txt`)) },
    });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Send' }));

    expect(
      await within(dialog).findByText('Attach at most 10 non-empty files, 4 MB in total.')
    ).toBeInTheDocument();
    expect(state.send.mutateAsync).not.toHaveBeenCalled();
  });

  it('lists the files of a message and downloads one on request', async () => {
    const attachment = {
      id: 5,
      filename: 'report.pdf',
      content_type: 'application/pdf',
      size: 2_048,
    };
    state.inbox.data = page([summary({ id: 11, subject: 'Unread question', attachment_count: 1 })]);
    state.detail.data = message({ attachment_count: 1, attachments: [attachment] });
    downloadAttachment.mockResolvedValue(undefined);
    renderWorkspace();

    expect(screen.getByRole('img', { name: 'Attachments: 1' })).toBeInTheDocument();
    fireEvent.click(rowButton('Unread question'));
    expect(screen.getByRole('heading', { name: 'Attachments' })).toBeInTheDocument();
    expect(screen.getByText('report.pdf')).toBeInTheDocument();
    expect(screen.getByText('2.0 KB')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Download report.pdf' }));
    await waitFor(() => expect(downloadAttachment).toHaveBeenCalledWith(1, 1, 11, attachment));

    downloadAttachment.mockRejectedValueOnce(new Error('network'));
    fireEvent.click(screen.getByRole('button', { name: 'Download report.pdf' }));
    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith('The file could not be downloaded. Try again.')
    );
  });

  it('sends a template with the variable values that were filled in', async () => {
    templateState.data = [
      {
        id: 4,
        name: 'Welcome',
        subject: 'Welcome {{first_name|there}}',
        variables: ['code', 'first_name'],
        created_at: now(),
        updated_at: now(),
      },
    ];
    state.send.mutateAsync.mockResolvedValue(message({ direction: 'outbound', status: 'sent' }));
    renderWorkspace();
    fireEvent.click(screen.getByRole('button', { name: 'Compose' }));
    const dialog = screen.getByRole('dialog');

    fireEvent.change(within(dialog).getByLabelText('Template'), { target: { value: '4' } });
    expect(within(dialog).queryByLabelText('Subject')).toBeNull();
    expect(within(dialog).queryByLabelText('Message')).toBeNull();
    fireEvent.change(within(dialog).getByLabelText('To'), { target: { value: 'bob@example.com' } });
    fireEvent.change(within(dialog).getByLabelText('code'), { target: { value: ' 1234 ' } });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Send' }));

    // first_name is left empty, so the template's fallback applies.
    await waitFor(() =>
      expect(state.send.mutateAsync).toHaveBeenCalledWith({
        to: ['bob@example.com'],
        template: { id: 4, variables: { code: '1234' } },
      })
    );
  });

  it('explains a template send that lacks variable values', async () => {
    templateState.data = [
      {
        id: 4,
        name: 'Welcome',
        subject: 'Hi',
        variables: ['code'],
        created_at: now(),
        updated_at: now(),
      },
    ];
    state.send.mutateAsync.mockRejectedValue(
      new ApiError('missing', ApiErrorCode.MAILBOX_TEMPLATE_VARIABLES_MISSING, 422)
    );
    renderWorkspace();
    fireEvent.click(screen.getByRole('button', { name: 'Compose' }));
    const dialog = screen.getByRole('dialog');
    fireEvent.change(within(dialog).getByLabelText('Template'), { target: { value: '4' } });
    fireEvent.change(within(dialog).getByLabelText('To'), { target: { value: 'bob@example.com' } });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Send' }));

    expect(
      await within(dialog).findByText(
        'Some variables of the template have no value and no fallback. Fill them in and send again.'
      )
    ).toBeInTheDocument();
  });

  it('says a rejected send was not delivered and is kept in Sent as failed', async () => {
    state.send.mutateAsync.mockRejectedValue(
      new ApiError('provider said no', ApiErrorCode.MAILBOX_DELIVERY_FAILED, 502)
    );
    renderWorkspace();
    fireEvent.click(screen.getByRole('button', { name: 'Compose' }));
    const dialog = screen.getByRole('dialog');
    fillValidMessage(dialog);
    fireEvent.click(within(dialog).getByRole('button', { name: 'Send' }));

    const alert = await within(dialog).findByRole('alert');
    expect(alert).toHaveTextContent(/NOT delivered/u);
    expect(alert).toHaveTextContent(/kept in Sent as failed/u);
    expect(screen.queryByText(/provider said no/u)).toBeNull();
    expect(toast.success).not.toHaveBeenCalled();
    // The sent folder now holds the failed attempt, so the workspace shows it behind the dialog.
    expect(state.listCalls.at(-1)).toEqual({ mailboxId: 1, direction: 'outbound', page: 1 });
    expect(screen.getByRole('dialog')).toBeInTheDocument();
  });

  it('maps the other send failures to their own copy without leaving the folder', async () => {
    renderWorkspace();
    fireEvent.click(screen.getByRole('button', { name: 'Compose' }));
    const dialog = screen.getByRole('dialog');
    fillValidMessage(dialog);

    const cases = [
      [ApiErrorCode.MAILBOX_MESSAGE_INVALID, /1 to 50 plain email addresses/u],
      [ApiErrorCode.MAILBOX_SEND_LIMIT_REACHED, /24-hour sending limit\. Nothing was sent/u],
    ] as const;
    for (const [code, copy] of cases) {
      state.send.mutateAsync.mockRejectedValueOnce(new ApiError('backend text', code));
      fireEvent.click(within(dialog).getByRole('button', { name: 'Send' }));
      await waitFor(() => expect(within(dialog).getByRole('alert')).toHaveTextContent(copy));
    }
    expect(state.listCalls.at(-1)?.direction).toBe('inbound');

    state.send.mutateAsync.mockRejectedValueOnce(
      new ApiError('backend text', ApiErrorCode.MAILBOX_SENDER_NOT_CONFIGURED, 503)
    );
    fireEvent.click(within(dialog).getByRole('button', { name: 'Send' }));
    await waitFor(() =>
      expect(within(dialog).getByRole('alert')).toHaveTextContent(
        /NOT delivered: sending mail is not set up/u
      )
    );
    expect(state.listCalls.at(-1)?.direction).toBe('outbound');
  });

  it('prefills a reply with the sender, a Re: subject, and the message it answers', async () => {
    state.send.mutateAsync.mockResolvedValue(message({ direction: 'outbound', status: 'sent' }));
    renderWorkspace();
    fireEvent.click(rowButton('Unread question'));
    fireEvent.click(screen.getByRole('button', { name: 'Reply' }));
    const dialog = screen.getByRole('dialog');

    expect(within(dialog).getByLabelText('To')).toHaveValue('bob@example.com');
    expect(within(dialog).getByLabelText('Subject')).toHaveValue('Re: Unread question');
    fireEvent.change(within(dialog).getByLabelText('Message'), { target: { value: 'Answer' } });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Send' }));

    await waitFor(() =>
      expect(state.send.mutateAsync).toHaveBeenCalledWith({
        to: ['bob@example.com'],
        subject: 'Re: Unread question',
        text: 'Answer',
        in_reply_to: 'question-1@example.com',
      })
    );
  });

  it('shows a retryable error when mailboxes cannot be loaded', () => {
    state.mailboxes.data = undefined;
    state.mailboxes.error = new ApiError('boom', ApiErrorCode.COMMON_SERVICE_UNAVAILABLE);
    renderWorkspace();

    expect(screen.getByText('The mailbox service is temporarily unavailable.')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(state.mailboxes.refetch).toHaveBeenCalled();
  });

  const viewOnlyAccess = {
    view: true,
    send: false,
    manage_members: false,
    administer: false,
    see_history: false,
    organize: false,
  };
  const sendAccess = { ...viewOnlyAccess, send: true };

  function threadSummary(ids: number[], overrides: Record<string, unknown> = {}) {
    return {
      message_count: ids.length,
      message_ids: ids,
      unread: false,
      starred: false,
      label_ids: [],
      ...overrides,
    };
  }

  function openSwitcher() {
    fireEvent.click(screen.getByRole('button', { name: /^Switch mailbox/u }));
  }

  /** Clicks the Undo action of the last success toast. */
  function undoLastToast() {
    const options = toast.success.mock.calls.at(-1)?.[1] as
      { action?: { onClick: () => void } } | undefined;
    if (!options?.action) throw new Error('the last toast has no undo');
    options.action.onClick();
  }

  /** The bar of actions on the selected rows. */
  function bulk() {
    return within(screen.getByRole('toolbar'));
  }

  function openManage() {
    openSwitcher();
    fireEvent.click(screen.getByRole('button', { name: 'Manage mailboxes' }));
  }

  describe('mailbox organize', () => {
    beforeEach(() => {
      state.organizations.data = organizations(organization(1, 'Acme', 'owner'));
    });

    it('view-only member sees no move or label actions', () => {
      state.organizations.data = organizations(organization(1, 'Acme', 'member'));
      state.mailboxes.data = [{ ...mailbox(1, 'hello', 'Hello Team'), access: viewOnlyAccess }];
      state.folders.data = [folder(4, 'Contracts')];
      state.labels.data = [label(7, 'Urgent', 'red')];
      renderWorkspace();

      fireEvent.click(screen.getByRole('checkbox', { name: 'Select Unread question' }));
      expect(bulk().getByRole('button', { name: 'Mark read' })).toBeInTheDocument();
      expect(bulk().getByRole('button', { name: 'Mark unread' })).toBeInTheDocument();
      for (const name of ['Archive', 'Move to folder', 'Label', 'Delete', 'Delete forever']) {
        expect(bulk().queryByRole('button', { name }), name).toBeNull();
      }
      // Nor on the row itself: only reading state.
      expect(screen.queryByRole('button', { name: 'Archive Unread question' })).toBeNull();
      expect(screen.queryByRole('button', { name: 'Delete Unread question' })).toBeNull();
      expect(screen.getByRole('button', { name: 'Mark Unread question read' })).toBeInTheDocument();
      // Folders and labels are listed, but only `organize` arranges them.
      expect(screen.getByRole('button', { name: /Contracts/u })).toBeInTheDocument();
      expect(screen.queryByRole('button', { name: 'Manage folders' })).toBeNull();
      expect(screen.queryByRole('button', { name: 'Manage labels' })).toBeNull();
      expect(screen.queryByRole('button', { name: 'Compose' })).toBeNull();
    });

    it('archiving a thread row removes it from inbox', async () => {
      state.inbox.data = page([
        summary({
          id: 13,
          subject: 'Quote request',
          thread_id: 11,
          thread: threadSummary([11, 12, 13], { unread: true }),
        }),
        summary({ id: 10, subject: 'Old news', read: true }),
      ]);
      state.updateMany.mutateAsync.mockImplementation(async () => {
        state.inbox.data = page([summary({ id: 10, subject: 'Old news', read: true })]);
        return { updated: 3 };
      });
      renderWorkspace();

      expect(state.filterCalls.at(-1)?.filters).toEqual({
        placement: 'default',
        direction: 'inbound',
        group: 'thread',
      });
      expect(screen.getByLabelText('3 messages in this conversation')).toHaveTextContent('3');
      fireEvent.click(screen.getByRole('checkbox', { name: 'Select Quote request' }));
      expect(screen.getByText('1 selected')).toBeInTheDocument();
      fireEvent.click(bulk().getByRole('button', { name: 'Archive' }));

      await waitFor(() =>
        expect(state.updateMany.mutateAsync).toHaveBeenCalledWith({
          message_ids: [11, 12, 13],
          placement: 'archive',
        })
      );
      expect(toast.success).toHaveBeenCalledWith(
        'Archived',
        expect.objectContaining({ action: expect.objectContaining({ label: 'Undo' }) })
      );
      await waitFor(() => expect(screen.queryByText('Quote request')).toBeNull());
      expect(screen.getByText('Old news')).toBeInTheDocument();
      expect(screen.queryByText('1 selected')).toBeNull();

      // Undo puts the thread back in the Inbox.
      undoLastToast();
      await waitFor(() =>
        expect(state.updateMany.mutateAsync).toHaveBeenLastCalledWith({
          message_ids: [11, 12, 13],
          placement: 'default',
        })
      );
    });

    it('search shows ungrouped results and too-broad notice', async () => {
      state.search.data = page([
        summary({ id: 31, subject: 'Invoice March', placement: 'archive', read: true }),
        summary({
          id: 32,
          subject: 'Re: Invoice March',
          direction: 'outbound',
          status: 'sent',
          to: ['bob@example.com'],
          read: true,
        }),
      ]);
      renderWorkspace();

      const search = screen.getByRole('searchbox', { name: 'Search this mailbox' });
      fireEvent.change(search, { target: { value: '  invoice ' } });
      fireEvent.submit(screen.getByRole('search'));

      expect(state.filterCalls.at(-1)?.filters).toEqual({ q: 'invoice' });
      expect(screen.getByRole('heading', { name: 'Results for “invoice”' })).toBeInTheDocument();
      expect(screen.getByText('Invoice March')).toBeInTheDocument();
      expect(screen.getByText('Re: Invoice March')).toBeInTheDocument();
      expect(screen.queryByLabelText(/messages in this conversation/u)).toBeNull();

      state.search.data = undefined;
      state.search.error = new ApiError('slow', ApiErrorCode.MAILBOX_SEARCH_TOO_BROAD, 422);
      fireEvent.change(search, { target: { value: 'a' } });
      fireEvent.submit(screen.getByRole('search'));
      expect(await screen.findByText(/Enter a longer keyword/u)).toBeInTheDocument();
      expect(screen.queryByText('slow')).toBeNull();

      // Choosing a folder leaves the search.
      fireEvent.click(screen.getByRole('button', { name: /^Inbox/u }));
      expect(search).toHaveValue('');
      expect(state.filterCalls.at(-1)?.filters).toMatchObject({ placement: 'default' });
    });

    it('thread view lists sent reply', () => {
      state.inbox.data = page([
        summary({
          id: 13,
          subject: 'Quote request',
          thread_id: 11,
          thread: threadSummary([11, 13], { unread: true }),
        }),
      ]);
      state.thread.data = [
        message({
          id: 11,
          subject: 'Quote request',
          text_body: 'Can you send a quote?',
          html_body: '',
          read: false,
        }),
        message({
          id: 13,
          direction: 'outbound',
          status: 'sent',
          thread_id: 11,
          from: { address: 'hello@mail.example.com', name: 'Hello Team' },
          to: ['bob@example.com'],
          subject: 'Re: Quote request',
          text_body: 'Here is the quote.',
          html_body: '',
          read: true,
        }),
      ];
      renderWorkspace();

      fireEvent.click(rowButton('Quote request'));
      expect(state.threadCalls.at(-1)).toBe(11);
      expect(state.updateMany.mutate).toHaveBeenCalledWith({ message_ids: [11, 13], read: true });

      const conversation = screen.getByRole('list', { name: 'Conversation' });
      const items = within(conversation).getAllByRole('listitem');
      expect(items).toHaveLength(2);
      // Oldest first; the newest, the sent reply, is open.
      expect(items[0]).toHaveTextContent('Bob');
      expect(items[0]).toHaveTextContent('Can you send a quote?');
      expect(items[1]).toHaveTextContent('Hello Team <hello@mail.example.com>');
      expect(within(items[1]).getByText('Here is the quote.')).toBeInTheDocument();

      // An older message opens on request.
      fireEvent.click(within(items[0]).getByRole('button', { name: /Show the message from Bob/u }));
      expect(within(items[0]).getByText('Bob <bob@example.com>')).toBeInTheDocument();
    });

    it('trash shows restore and, for organize, delete forever', async () => {
      state.trash.data = page([
        summary({
          id: 41,
          subject: 'Spam offer',
          placement: 'trash',
          trashed_at: now(),
          read: true,
        }),
      ]);
      state.updateMany.mutateAsync.mockResolvedValue({ updated: 1 });
      state.removeMessage.mutateAsync.mockResolvedValue(undefined);
      state.purge.mutateAsync.mockResolvedValue({ deleted: 1 });
      const view = renderWorkspace();

      fireEvent.click(screen.getByRole('button', { name: /^Trash/u }));
      expect(state.filterCalls.at(-1)?.filters).toEqual({ placement: 'trash' });
      expect(
        screen.getByText('Messages in the trash are deleted for good after 30 days.')
      ).toBeInTheDocument();

      fireEvent.click(screen.getByRole('checkbox', { name: 'Select Spam offer' }));
      expect(bulk().queryByRole('button', { name: 'Archive' })).toBeNull();
      fireEvent.click(bulk().getByRole('button', { name: 'Restore' }));
      await waitFor(() =>
        expect(state.updateMany.mutateAsync).toHaveBeenCalledWith({
          message_ids: [41],
          placement: 'default',
        })
      );

      fireEvent.click(screen.getByRole('checkbox', { name: 'Select Spam offer' }));
      fireEvent.click(bulk().getByRole('button', { name: 'Delete forever' }));
      expect(state.removeMessage.mutateAsync).not.toHaveBeenCalled();
      fireEvent.click(
        within(screen.getByRole('alertdialog')).getByRole('button', { name: 'Delete forever' })
      );
      await waitFor(() => expect(state.removeMessage.mutateAsync).toHaveBeenCalledWith(41));

      fireEvent.click(screen.getByRole('button', { name: 'Empty trash' }));
      fireEvent.click(
        within(screen.getByRole('alertdialog')).getByRole('button', { name: 'Empty trash' })
      );
      await waitFor(() => expect(state.purge.mutateAsync).toHaveBeenCalled());
      view.unmount();

      // `send` restores, but deleting for good needs `organize`.
      state.mailboxes.data = [{ ...mailbox(1, 'hello', 'Hello Team'), access: sendAccess }];
      renderWorkspace();
      fireEvent.click(screen.getByRole('button', { name: /^Trash/u }));
      fireEvent.click(screen.getByRole('checkbox', { name: 'Select Spam offer' }));
      expect(bulk().getByRole('button', { name: 'Restore' })).toBeInTheDocument();
      expect(bulk().queryByRole('button', { name: 'Delete forever' })).toBeNull();
      expect(screen.queryByRole('button', { name: 'Empty trash' })).toBeNull();
    });

    it('star is per person', () => {
      // Another member's listing of the same message: a star here never reaches it.
      const otherMember = summary({ id: 11, subject: 'Unread question', starred: false });
      const otherBefore = structuredClone(otherMember);
      state.organizations.data = organizations(organization(1, 'Acme', 'member'));
      state.mailboxes.data = [{ ...mailbox(1, 'hello', 'Hello Team'), access: viewOnlyAccess }];
      state.inbox.data = page([
        summary({ id: 11, subject: 'Unread question' }),
        summary({ id: 10, subject: 'Old news', read: true, starred: true }),
      ]);
      renderWorkspace();

      const row = (subject: string) => {
        const item = rowButton(subject).closest('li');
        if (!item) throw new Error(subject);
        return item;
      };
      expect(within(row('Old news')).getByRole('button', { name: 'Remove star' })).toHaveAttribute(
        'aria-pressed',
        'true'
      );
      // Starring needs only `view` and changes nothing but the caller's own star.
      fireEvent.click(within(row('Unread question')).getByRole('button', { name: 'Star' }));
      expect(state.update.mutateAsync).toHaveBeenCalledWith({ messageId: 11, starred: true });
      expect(state.updateMany.mutateAsync).not.toHaveBeenCalled();
      expect(otherMember).toEqual(otherBefore);
    });

    it('folder name taken shows inline error', async () => {
      state.folders.data = [folder(4, 'Contracts')];
      state.createFolder.mutateAsync.mockRejectedValue(
        new ApiError('taken', ApiErrorCode.MAILBOX_FOLDER_NAME_TAKEN, 409)
      );
      renderWorkspace();

      fireEvent.click(screen.getByRole('button', { name: 'Manage folders' }));
      const dialog = screen.getByRole('dialog', { name: 'Folders' });
      fireEvent.change(within(dialog).getByLabelText('New folder'), {
        target: { value: ' contracts ' },
      });
      fireEvent.click(within(dialog).getByRole('button', { name: 'Add folder' }));

      await waitFor(() =>
        expect(state.createFolder.mutateAsync).toHaveBeenCalledWith({ name: 'contracts' })
      );
      expect(await within(dialog).findByRole('alert')).toHaveTextContent(
        'Another folder of this mailbox already has this name.'
      );
      expect(toast.error).not.toHaveBeenCalled();
      expect(screen.getByRole('dialog', { name: 'Folders' })).toBeInTheDocument();
    });

    it('switching mailbox in the switcher changes the list and URL', () => {
      window.history.replaceState(null, '', '/console/mailboxes?org=1');
      renderWorkspace();
      expect(state.listCalls.at(-1)?.mailboxId).toBe(1);

      openSwitcher();
      fireEvent.click(
        within(screen.getByRole('list', { name: 'Shared mailboxes' })).getByRole('button', {
          name: /^sales@mail\.example\.com/u,
        })
      );

      expect(state.listCalls.at(-1)?.mailboxId).toBe(2);
      const search = new URLSearchParams(window.location.search);
      expect(search.get('org')).toBe('1');
      expect(search.get('mailbox')).toBe('2');
      expect(window.localStorage.getItem('zmail.mailbox.1')).toBe('2');
      expect(screen.getByRole('button', { name: /^Switch mailbox/u })).toHaveTextContent(
        'sales@mail.example.com'
      );
    });

    it('switcher shows unread counts', () => {
      state.mailboxes.data = [
        mailbox(1, 'hello', 'Hello Team', 3),
        mailbox(2, 'sales', '', 0),
        mailbox(3, 'busy', '', 250),
      ];
      renderWorkspace();

      // The other mailboxes' unread mail, on the trigger.
      expect(screen.getByLabelText('250 unread in other mailboxes')).toHaveTextContent('99+');
      openSwitcher();
      const list = screen.getByRole('list', { name: 'Shared mailboxes' });
      expect(within(list).getByLabelText('3 unread')).toHaveTextContent('3');
      expect(within(list).getByLabelText('250 unread')).toHaveTextContent('99+');
      expect(within(list).queryByLabelText('0 unread')).toBeNull();
    });

    it('create mailbox lives in the manage dialog for admins and is absent for members', () => {
      const view = renderWorkspace();
      expect(screen.queryByLabelText('Address')).toBeNull();
      expect(screen.queryByRole('button', { name: /^Delete .*@/u })).toBeNull();
      openSwitcher();
      fireEvent.click(screen.getByRole('button', { name: 'Manage mailboxes' }));
      const dialog = screen.getByRole('dialog', { name: 'Manage mailboxes' });
      expect(within(dialog).getByRole('form', { name: 'New mailbox' })).toBeInTheDocument();
      expect(within(dialog).getByText('2 of 10 used')).toBeInTheDocument();
      expect(within(dialog).getAllByRole('button', { name: /^Delete /u })).toHaveLength(2);
      view.unmount();

      state.organizations.data = organizations(organization(1, 'Acme', 'member'));
      renderWorkspace();
      openSwitcher();
      expect(screen.queryByRole('button', { name: 'Manage mailboxes' })).toBeNull();
      expect(screen.queryByLabelText('Address')).toBeNull();
    });
  });
});

function folder(id: number, name: string) {
  return { id, name, created_at: now() };
}

function label(id: number, name: string, color: string) {
  return { id, name, color, created_at: now() };
}

/** The button that opens a row; the row's star and quick actions have their own names. */
function rowButton(subject: string) {
  const row = screen.getByRole('checkbox', { name: `Select ${subject}` }).closest('li');
  if (!row) throw new Error(`no row ${subject}`);
  const button = within(row)
    .getAllByRole('button')
    .find(element => !element.hasAttribute('aria-label'));
  if (!button) throw new Error(`no open button for ${subject}`);
  return button;
}

function fillValidMessage(dialog: HTMLElement) {
  fireEvent.change(within(dialog).getByLabelText('To'), { target: { value: 'bob@example.com' } });
  fireEvent.change(within(dialog).getByLabelText('Subject'), { target: { value: 'Hello' } });
  fireEvent.change(within(dialog).getByLabelText('Message'), { target: { value: 'Body' } });
}

function domain(maxMailboxes: number) {
  return {
    domain: 'mail.example.com',
    kind: 'shared',
    max_mailboxes: maxMailboxes,
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

function organization(id: number, name: string, role: 'owner' | 'admin' | 'member') {
  return { id, name, slug: `org-${id}`, role, created_at: now(), updated_at: now() };
}

function organizations(...items: ReturnType<typeof organization>[]) {
  return page(items);
}

/** A shared mailbox the user works with `manage`, so every message control is shown. */
function mailbox(id: number, localPart: string, displayName: string, unreadCount = 0) {
  return {
    id,
    address: `${localPart}@mail.example.com`,
    display_name: displayName,
    type: 'shared',
    owner_user_id: null,
    awaiting_handover: false,
    sealed_before: null,
    webhooks_enabled: false,
    access: {
      view: true,
      send: true,
      manage_members: true,
      administer: true,
      see_history: true,
      organize: true,
    },
    unread_count: unreadCount,
    created_at: now(),
  };
}

function summary(overrides: Record<string, unknown> = {}) {
  const id = typeof overrides.id === 'number' ? overrides.id : 11;
  return {
    id,
    placement: 'default',
    folder_id: null,
    label_ids: [],
    starred: false,
    thread_id: id,
    trashed_at: null,
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
    created_at: now(),
    ...overrides,
  };
}

function message(overrides: Record<string, unknown> = {}) {
  return {
    ...summary(overrides),
    text_body: 'Plain question',
    html_body: '<p>Formatted question</p>',
    message_id: 'question-1@example.com',
    in_reply_to: '',
    events: [],
    attachments: [],
    ...overrides,
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

function now() {
  return '2026-11-16T10:00:00Z';
}

function wrap(children: React.ReactNode) {
  return (
    <NextIntlClientProvider locale="en-US" messages={messages as unknown as AbstractIntlMessages}>
      {children}
    </NextIntlClientProvider>
  );
}

function renderWorkspace() {
  return render(wrap(<MailboxWorkspace />));
}
