import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { NextIntlClientProvider, type AbstractIntlMessages } from 'next-intl';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { OrganizationDirectory } from '@/features/organization/components/organization-directory';
import { OrganizationOverview } from '@/features/organization/components/organization-overview';
import { ApiErrorCode, ClientErrorCode } from '@/http/codes';
import { ApiError } from '@/http/request';
import { messages } from '@/i18n/modules';
import zhHansOrganization from '@/i18n/modules/organization/zh-Hans';

const state = vi.hoisted(() => ({
  organizations: {
    data: undefined as unknown,
    error: null as unknown,
    isPending: false,
    refetch: vi.fn(),
  },
  context: {
    data: undefined as unknown,
    error: null as unknown,
    isPending: false,
    refetch: vi.fn(),
  },
  members: {
    data: undefined as unknown,
    error: null as unknown,
    isPending: false,
    refetch: vi.fn(),
  },
  invitations: {
    data: undefined as unknown,
    error: null as unknown,
    isPending: false,
    refetch: vi.fn(),
  },
  create: {
    error: null as unknown,
    isPending: false,
    mutate: vi.fn(),
    reset: vi.fn(),
  },
  update: {
    error: null as unknown,
    isPending: false,
    mutate: vi.fn(),
    reset: vi.fn(),
  },
  accept: mutationState(),
  invite: mutationState(),
  removeMember: mutationState(),
  revokeInvitation: mutationState(),
  resendInvitation: mutationState(),
  transferOwnership: mutationState(),
  updateMember: mutationState(),
  updateMemberFeatures: mutationState(),
  forgetOrganization: vi.fn(),
}));

const toastError = vi.hoisted(() => vi.fn());
vi.mock('sonner', async (importOriginal) => {
  const original = await importOriginal<typeof import('sonner')>();
  return {
    ...original,
    toast: Object.assign(vi.fn(), original.toast, { error: toastError }),
  };
});

vi.mock('@/features/organization/hooks/use-organizations', () => ({
  useOrganizations: () => state.organizations,
  useOrganizationContext: () => state.context,
  useOrganizationInvitations: () => state.invitations,
  useOrganizationMembers: () => state.members,
  useAcceptOrganizationInvitation: () => state.accept,
  useCreateOrganization: () => state.create,
  useCreateOrganizationInvitation: () => state.invite,
  useRemoveOrganizationMember: () => state.removeMember,
  useRevokeOrganizationInvitation: () => state.revokeInvitation,
  useResendOrganizationInvitation: () => state.resendInvitation,
  useTransferOrganizationOwnership: () => state.transferOwnership,
  useUpdateOrganization: () => state.update,
  useUpdateOrganizationMember: () => state.updateMember,
  useUpdateOrganizationMemberFeatures: () => state.updateMemberFeatures,
  useForgetOrganization: () => state.forgetOrganization,
}));

function withMessages(children: React.ReactNode, locale: 'en-US' | 'zh-Hans' = 'en-US') {
  const localized =
    locale === 'en-US' ? messages : { ...messages, organization: zhHansOrganization };
  return (
    <NextIntlClientProvider locale={locale} messages={localized as unknown as AbstractIntlMessages}>
      {children}
    </NextIntlClientProvider>
  );
}

function renderWithMessages(children: React.ReactNode, locale: 'en-US' | 'zh-Hans' = 'en-US') {
  return render(withMessages(children, locale));
}

describe('organization browser workflow', () => {
  beforeEach(() => {
    state.organizations.data = undefined;
    state.organizations.error = null;
    state.organizations.isPending = false;
    state.organizations.refetch.mockReset();
    state.context.data = undefined;
    state.context.error = null;
    state.context.isPending = false;
    state.context.refetch.mockReset();
    state.members.data = undefined;
    state.members.error = null;
    state.members.isPending = false;
    state.members.refetch.mockReset();
    state.invitations.data = undefined;
    state.invitations.error = null;
    state.invitations.isPending = false;
    state.invitations.refetch.mockReset();
    state.create.error = null;
    state.create.isPending = false;
    state.create.mutate.mockReset();
    state.create.reset.mockReset();
    state.update.error = null;
    state.update.isPending = false;
    state.update.mutate.mockReset();
    state.update.reset.mockReset();
    for (const mutation of [
      state.accept,
      state.invite,
      state.removeMember,
      state.revokeInvitation,
      state.resendInvitation,
      state.transferOwnership,
      state.updateMember,
      state.updateMemberFeatures,
    ]) {
      mutation.error = null;
      mutation.isPending = false;
      mutation.variables = undefined;
      mutation.mutate.mockReset();
      mutation.reset.mockReset();
    }
  });

  it('renders organization identity, immutable slug, and role from the validated page', () => {
    state.organizations.data = {
      items: [
        {
          id: 42,
          name: 'Acme Europe',
          slug: 'acme-europe',
          role: 'owner',
          created_at: '2026-07-15T10:00:00Z',
          updated_at: '2026-07-15T10:00:00Z',
        },
      ],
      meta: { total: 1 },
    };

    renderWithMessages(<OrganizationDirectory />);

    expect(screen.getByRole('heading', { name: 'Organizations' })).toBeInTheDocument();
    expect(screen.getByText('Acme Europe')).toBeInTheDocument();
    expect(screen.getAllByText('acme-europe')).toHaveLength(2);
    expect(screen.getByText('Owner')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Open Acme Europe' })).toHaveAttribute(
      'href',
      '/console/organizations/42'
    );
    expect(screen.getByRole('link', { name: 'Acme Europe' })).toHaveAttribute(
      'href',
      '/console/organizations/42'
    );
  });

  it('uses local copy for malformed successful responses', () => {
    state.organizations.error = new ApiError(
      'raw parser detail',
      ClientErrorCode.INVALID_RESPONSE
    );

    renderWithMessages(<OrganizationDirectory />);

    expect(screen.getByRole('alert')).toHaveTextContent(
      'The organization service returned data this client cannot recognize.'
    );
    expect(screen.queryByText('raw parser detail')).not.toBeInTheDocument();
  });

  it('keeps a member organization profile read-only', () => {
    state.context.data = {
      organization_id: 42,
      organization_name: 'Acme Europe',
      organization_slug: 'acme-europe',
      membership_id: 91,
      user_id: 17,
      role: 'member',
    };

    renderWithMessages(<OrganizationOverview organizationId={42} />);

    expect(screen.getByLabelText('Organization name')).toBeDisabled();
    expect(screen.getByLabelText('Organization slug')).toBeDisabled();
    expect(screen.queryByRole('button', { name: 'Save' })).not.toBeInTheDocument();
    expect(screen.getByText('Context verified')).toBeInTheDocument();
  });

  it('isolates editable state when switching between cached organization contexts', () => {
    state.context.data = {
      organization_id: 42,
      organization_name: 'Acme Europe',
      organization_slug: 'acme-europe',
      membership_id: 91,
      user_id: 17,
      role: 'owner',
    };
    const view = renderWithMessages(<OrganizationOverview organizationId={42} />);

    expect(screen.getByLabelText('Organization name')).toHaveValue('Acme Europe');

    state.context.data = {
      organization_id: 43,
      organization_name: 'Acme Americas',
      organization_slug: 'acme-americas',
      membership_id: 92,
      user_id: 17,
      role: 'admin',
    };
    view.rerender(withMessages(<OrganizationOverview organizationId={43} />));

    expect(screen.getByLabelText('Organization name')).toHaveValue('Acme Americas');
  });

  it('associates server field ownership with the organization name control', () => {
    state.context.data = {
      organization_id: 42,
      organization_name: 'Acme Europe',
      organization_slug: 'acme-europe',
      membership_id: 91,
      user_id: 17,
      role: 'owner',
    };
    state.update.error = new ApiError(
      'raw backend validation detail',
      ApiErrorCode.COMMON_VALIDATION_FAILED,
      422,
      'request-1',
      { name: ['raw field detail'] }
    );

    renderWithMessages(<OrganizationOverview organizationId={42} />);

    const name = screen.getByLabelText('Organization name');
    expect(name).toHaveAttribute('aria-invalid', 'true');
    expect(screen.getByText('Enter an organization name between 2 and 100 characters')).toBeInTheDocument();
    expect(screen.queryByText('raw backend validation detail')).not.toBeInTheDocument();
    fireEvent.change(name, { target: { value: 'Acme Global' } });
    expect(state.update.reset).toHaveBeenCalledTimes(1);
  });

  it('submits a trimmed invitation token without placing it in a URL', () => {
    state.organizations.data = { items: [], meta: { total: 0 } };
    renderWithMessages(<OrganizationDirectory />);

    fireEvent.click(screen.getByRole('button', { name: 'Accept invitation' }));
    const token = screen.getByLabelText('One-time token');
    expect(token).toHaveAttribute('type', 'password');
    fireEvent.change(token, { target: { value: '  oinv_secret.value  ' } });
    fireEvent.click(screen.getAllByRole('button', { name: 'Accept invitation' }).at(-1)!);

    expect(state.accept.mutate).toHaveBeenCalledWith(
      { token: 'oinv_secret.value' },
      expect.objectContaining({ onSuccess: expect.any(Function) })
    );
  });

  it('renders the PII-minimized member directory and owner controls', () => {
    state.context.data = organizationContext('owner');
    state.members.data = {
      items: [
        memberView({ id: 91, user_id: 17, username: 'owner', role: 'owner' }),
        memberView({ id: 92, user_id: 18, username: 'alex', role: 'member' }),
      ],
      meta: { total: 2 },
    };

    renderWithMessages(<OrganizationOverview organizationId={42} />);
    activateTab('Members');

    expect(screen.getByText('@alex')).toBeInTheDocument();
    expect(screen.queryByText('alex@example.com')).not.toBeInTheDocument();
    expect(screen.getByRole('combobox', { name: 'Change role: alex' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Actions: alex' })).toBeInTheDocument();
  });

  describe('member feature grants', () => {
    const grants = { broadcasts: false, contacts: true, templates: true, api_keys: false };
    const allGrants = { broadcasts: true, contacts: true, templates: true, api_keys: true };

    function showMembers(role: 'admin' | 'member' | 'owner') {
      state.context.data = organizationContext(role);
      state.members.data = {
        items: [
          memberView({ id: 91, user_id: 17, username: 'owner', role: 'owner', features: allGrants }),
          memberView({ id: 92, user_id: 18, username: 'alex', role: 'member', features: grants }),
        ],
        meta: { total: 2 },
      };
      renderWithMessages(<OrganizationOverview organizationId={42} />);
      activateTab('Members');
    }

    it.each(['owner', 'admin'] as const)('lets an %s switch each feature of a member', role => {
      showMembers(role);

      const broadcasts = screen.getAllByRole('button', { name: 'Broadcasts for Alex' })[0]!;
      expect(broadcasts).toHaveAttribute('aria-pressed', 'false');
      expect(screen.getAllByRole('button', { name: 'Contacts for Alex' })[0]).toHaveAttribute(
        'aria-pressed',
        'true'
      );
      fireEvent.click(broadcasts);

      expect(state.updateMemberFeatures.mutate).toHaveBeenCalledWith(
        { memberId: 92, input: { broadcasts: true } },
        expect.objectContaining({ onSuccess: expect.any(Function), onError: expect.any(Function) })
      );
      // Owners and admins always have everything, so their row has no switches.
      expect(screen.queryByRole('button', { name: /for owner$/ })).not.toBeInTheDocument();
      expect(screen.getAllByText('Every feature').length).toBeGreaterThan(0);
    });

    it('confirms a saved change and reports a refused one', () => {
      showMembers('owner');
      fireEvent.click(screen.getAllByRole('button', { name: 'API keys for Alex' })[0]!);
      const options = state.updateMemberFeatures.mutate.mock.calls[0]![1] as {
        onError: (error: unknown) => void;
      };
      options.onError(new ApiError('denied', ApiErrorCode.PERMISSION_DENIED, 403));
      expect(toastError).toHaveBeenCalledWith(
        'You do not have permission to perform this operation.'
      );
    });

    it('shows a member the grants read-only', () => {
      showMembers('member');

      expect(screen.queryByRole('button', { name: /for Alex$/ })).not.toBeInTheDocument();
      expect(screen.getAllByText('Contacts').length).toBeGreaterThan(0);
      expect(screen.queryByText('Broadcasts')).not.toBeInTheDocument();
    });

    it('marks a saving switch busy', () => {
      state.updateMemberFeatures.isPending = true;
      state.updateMemberFeatures.variables = { memberId: 92, input: { templates: false } };
      showMembers('owner');

      for (const button of screen.getAllByRole('button', { name: 'Templates for Alex' })) {
        expect(button).toBeDisabled();
      }
      for (const button of screen.getAllByRole('button', { name: 'Contacts for Alex' })) {
        expect(button).toBeEnabled();
      }
    });

    it('speaks zh-Hans', () => {
      state.context.data = organizationContext('owner');
      state.members.data = {
        items: [memberView({ id: 92, features: grants })],
        meta: { total: 1 },
      };
      renderWithMessages(<OrganizationOverview organizationId={42} />, 'zh-Hans');
      activateTab('成员');
      expect(screen.getAllByRole('button', { name: 'Alex 的群发' }).length).toBeGreaterThan(0);
    });
  });

  it('forgets the organization after the user leaves it (#114)', () => {
    state.forgetOrganization.mockReset();
    state.context.data = organizationContext('member');
    state.members.data = {
      items: [memberView({ id: 91, user_id: 17, username: 'me', role: 'member' })],
      meta: { total: 1 },
    };
    renderWithMessages(<OrganizationOverview organizationId={42} />);
    activateTab('Members');

    fireEvent.keyDown(screen.getByRole('button', { name: 'Actions: me' }), { key: 'Enter' });
    fireEvent.click(screen.getByRole('menuitem', { name: 'Leave organization' }));
    fireEvent.click(
      within(screen.getByRole('dialog')).getByRole('button', { name: 'Leave organization' })
    );

    expect(state.removeMember.mutate).toHaveBeenCalledWith(91, expect.any(Object));
    expect(state.forgetOrganization).not.toHaveBeenCalled();
    const options = state.removeMember.mutate.mock.calls[0]?.[1] as { onSuccess: () => void };
    act(() => options.onSuccess());
    expect(state.forgetOrganization).toHaveBeenCalledWith(42);
  });

  it('shows token-free invitation history only to organization managers', () => {
    state.context.data = organizationContext('admin');
    state.invitations.data = {
      items: [
        {
          id: 73,
          organization_id: 42,
          email: 'new.member@example.com',
          role: 'member',
          status: 'pending',
          expires_at: '2026-07-22T10:00:00Z',
          email_send_status: null,
          email_attempted_at: null,
          created_at: '2026-07-15T10:00:00Z',
          updated_at: '2026-07-15T10:00:00Z',
        },
      ],
      meta: { total: 1 },
    };

    const view = renderWithMessages(<OrganizationOverview organizationId={42} />);
    activateTab('Invitations');
    expect(screen.getByText('new.member@example.com')).toBeInTheDocument();
    expect(screen.queryByText(/oinv_/)).not.toBeInTheDocument();

    state.context.data = organizationContext('member');
    view.rerender(withMessages(<OrganizationOverview organizationId={42} />));
    expect(screen.queryByRole('tab', { name: 'Invitations' })).not.toBeInTheDocument();
  });

  it('marks an unsent invitation and resends it', () => {
    state.context.data = organizationContext('admin');
    state.invitations.data = {
      items: [
        {
          id: 73,
          organization_id: 42,
          email: 'new.member@example.com',
          role: 'member',
          status: 'pending',
          expires_at: '2026-07-22T10:00:00Z',
          email_send_status: 'failed',
          email_attempted_at: '2026-07-15T10:00:01Z',
          created_at: '2026-07-15T10:00:00Z',
          updated_at: '2026-07-15T10:00:00Z',
        },
      ],
      meta: { total: 1 },
    };

    renderWithMessages(<OrganizationOverview organizationId={42} />);
    activateTab('Invitations');
    expect(screen.getByText('Email not sent')).toBeInTheDocument();

    fireEvent.click(
      screen.getByRole('button', { name: 'Resend the invitation to new.member@example.com' })
    );
    expect(state.resendInvitation.mutate).toHaveBeenCalledWith(73, expect.any(Object));
  });

  it('waits out the resend cooldown after a successful resend', () => {
    vi.useFakeTimers();
    try {
      state.context.data = organizationContext('owner');
      state.invitations.data = { items: [pendingInvitation(74, 'again@example.com')], meta: { total: 1 } };
      state.resendInvitation.mutate.mockImplementation(
        (_id: number, options: { onSuccess: (value: unknown) => void }) =>
          options.onSuccess({
            invitation: pendingInvitation(74, 'again@example.com'),
            email_send_status: 'accepted_by_provider',
          })
      );

      renderWithMessages(<OrganizationOverview organizationId={42} />);
      activateTab('Invitations');
      const button = screen.getByRole('button', { name: 'Resend the invitation to again@example.com' });
      fireEvent.click(button);
      expect(button).toBeDisabled();
      expect(button).toHaveTextContent('Resend in 60s');

      act(() => {
        vi.advanceTimersByTime(60_000);
      });
      expect(button).toBeEnabled();
      expect(button).toHaveTextContent('Resend');
    } finally {
      vi.useRealTimers();
    }
  });

  it('shows a rate-limited resend and uses its Retry-After as the cooldown', () => {
    state.context.data = organizationContext('owner');
    state.invitations.data = { items: [pendingInvitation(75, 'limited@example.com')], meta: { total: 1 } };
    state.resendInvitation.mutate.mockImplementation(
      (_id: number, options: { onError: (error: unknown) => void }) =>
        options.onError(
          new ApiError('Too many requests', ApiErrorCode.COMMON_RATE_LIMITED, 429, undefined, undefined, 42)
        )
    );

    renderWithMessages(<OrganizationOverview organizationId={42} />);
    activateTab('Invitations');
    const button = screen.getByRole('button', { name: 'Resend the invitation to limited@example.com' });
    fireEvent.click(button);
    expect(button).toBeDisabled();
    expect(button).toHaveTextContent('Resend in 42s');
    expect(toastError).toHaveBeenCalledWith(
      'This invitation was resent recently. Try again in a moment.'
    );
  });

  describe('one-time invitation link (#195)', () => {
    const link = 'https://app.example.com/invite#oinv_abc.0123456789abcdef';
    const clipboard = { writeText: vi.fn() };

    beforeEach(() => {
      clipboard.writeText.mockReset().mockResolvedValue(undefined);
      Object.defineProperty(navigator, 'clipboard', { value: clipboard, configurable: true });
    });

    function inviteWithLink(invitationUrl: string | null = link) {
      state.invite.mutate.mockImplementation(
        (_input: unknown, options: { onSuccess: (value: unknown) => void }) =>
          options.onSuccess({
            invitation: pendingInvitation(80, 'linked@example.com'),
            email_send_status: 'accepted_by_provider',
            ...(invitationUrl ? { invitation_url: invitationUrl } : {}),
          })
      );
      fireEvent.click(screen.getByRole('button', { name: 'Invite member' }));
      fireEvent.change(screen.getByLabelText('Email'), {
        target: { value: 'linked@example.com' },
      });
      fireEvent.click(screen.getByRole('button', { name: 'Send invitation' }));
    }

    it('offers Copy invitation link right after creating an invitation', async () => {
      state.context.data = organizationContext('admin');
      state.invitations.data = { items: [], meta: { total: 0 } };
      renderWithMessages(<OrganizationOverview organizationId={42} />);
      activateTab('Invitations');
      inviteWithLink();

      const dialog = await screen.findByRole('dialog');
      expect(within(dialog).getByText('Invitation created')).toBeInTheDocument();
      expect(within(dialog).queryByDisplayValue(link)).not.toBeInTheDocument();
      const copy = within(dialog).getByRole('button', { name: 'Copy invitation link' });
      copy.focus();
      expect(copy).toHaveFocus();
      fireEvent.click(copy);

      await waitFor(() => expect(clipboard.writeText).toHaveBeenCalledWith(link));
      expect(await within(dialog).findByRole('button', { name: 'Copied' })).toBeInTheDocument();
      expect(within(dialog).getByRole('status')).toHaveTextContent('Invitation link copied');

      state.invite.reset.mockClear();
      fireEvent.click(within(dialog).getByRole('button', { name: 'Done' }));
      await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
      expect(state.invite.reset).toHaveBeenCalled();
    });

    it('closes as before when the response carries no link', async () => {
      state.context.data = organizationContext('admin');
      state.invitations.data = { items: [], meta: { total: 0 } };
      renderWithMessages(<OrganizationOverview organizationId={42} />);
      activateTab('Invitations');
      inviteWithLink(null);

      await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
      expect(screen.queryByRole('button', { name: 'Copy invitation link' })).not.toBeInTheDocument();
    });

    it('offers the new link after a resend and says the previous one stopped working', async () => {
      state.context.data = organizationContext('owner');
      state.invitations.data = { items: [pendingInvitation(74, 'again@example.com')], meta: { total: 1 } };
      state.resendInvitation.mutate.mockImplementation(
        (_id: number, options: { onSuccess: (value: unknown) => void }) =>
          options.onSuccess({
            invitation: pendingInvitation(74, 'again@example.com'),
            email_send_status: 'accepted_by_provider',
            invitation_url: link,
          })
      );
      renderWithMessages(<OrganizationOverview organizationId={42} />);
      activateTab('Invitations');
      fireEvent.click(screen.getByRole('button', { name: 'Resend the invitation to again@example.com' }));

      const dialog = await screen.findByRole('dialog');
      expect(within(dialog).getByText('New invitation link')).toBeInTheDocument();
      expect(dialog).toHaveTextContent('The previous link no longer works.');
      fireEvent.click(within(dialog).getByRole('button', { name: 'Copy invitation link' }));
      await waitFor(() => expect(clipboard.writeText).toHaveBeenCalledWith(link));

      // Closing drops the link from the mutation state too.
      fireEvent.click(within(dialog).getByRole('button', { name: 'Done' }));
      await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
      expect(state.resendInvitation.reset).toHaveBeenCalled();
    });

    it('shows the link selectable when the clipboard refuses', async () => {
      clipboard.writeText.mockRejectedValue(new Error('NotAllowedError'));
      state.context.data = organizationContext('admin');
      state.invitations.data = { items: [], meta: { total: 0 } };
      renderWithMessages(<OrganizationOverview organizationId={42} />);
      activateTab('Invitations');
      inviteWithLink();

      const dialog = await screen.findByRole('dialog');
      fireEvent.click(within(dialog).getByRole('button', { name: 'Copy invitation link' }));
      const field = await within(dialog).findByRole('textbox', {
        name: 'Invitation link for linked@example.com',
      });
      expect(field).toHaveValue(link);
      expect(field).toHaveAttribute('readonly');
      await waitFor(() => expect(field).toHaveFocus());
      expect(within(dialog).getByRole('alert')).toHaveTextContent(
        'Copying failed. Select the link and copy it manually.'
      );
      expect(field).toHaveAccessibleDescription(
        'Copying failed. Select the link and copy it manually.'
      );
    });

    it('shows the link selectable when the Clipboard API is unavailable', async () => {
      Object.defineProperty(navigator, 'clipboard', { value: undefined, configurable: true });
      state.context.data = organizationContext('admin');
      state.invitations.data = { items: [], meta: { total: 0 } };
      renderWithMessages(<OrganizationOverview organizationId={42} />);
      activateTab('Invitations');
      inviteWithLink();

      const dialog = await screen.findByRole('dialog');
      fireEvent.click(within(dialog).getByRole('button', { name: 'Copy invitation link' }));
      expect(
        await within(dialog).findByRole('textbox', { name: 'Invitation link for linked@example.com' })
      ).toHaveValue(link);
    });

    it('tells managers on pending rows how to get a link again', () => {
      state.context.data = organizationContext('admin');
      state.invitations.data = {
        items: [
          pendingInvitation(74, 'again@example.com'),
          { ...pendingInvitation(75, 'unsent@example.com'), email_send_status: 'not_configured' },
        ],
        meta: { total: 2 },
      };
      renderWithMessages(<OrganizationOverview organizationId={42} />);
      activateTab('Invitations');

      // A sent or failed email means a mailer is configured, so a resend replaces the link; whether
      // the new link can be copied depends on MAILBOX_PUBLIC_URL, which the Web cannot see.
      expect(
        screen.getByText(
          'Resending makes a new link; the old one stops working.'
        )
      ).toBeInTheDocument();
      expect(
        screen.getByText(
          'The link can be copied only right after inviting or resending.'
        )
      ).toBeInTheDocument();
      expect(screen.queryByText(/oinv_/)).not.toBeInTheDocument();
    });

    it('speaks zh-Hans', async () => {
      clipboard.writeText.mockRejectedValue(new Error('NotAllowedError'));
      state.context.data = organizationContext('owner');
      state.invitations.data = { items: [pendingInvitation(74, 'again@example.com')], meta: { total: 1 } };
      state.resendInvitation.mutate.mockImplementation(
        (_id: number, options: { onSuccess: (value: unknown) => void }) =>
          options.onSuccess({
            invitation: pendingInvitation(74, 'again@example.com'),
            email_send_status: 'accepted_by_provider',
            invitation_url: link,
          })
      );
      renderWithMessages(<OrganizationOverview organizationId={42} />, 'zh-Hans');
      activateTab('邀请');
      expect(
        screen.getByText('重发会生成新链接，旧链接随即失效。')
      ).toBeInTheDocument();
      fireEvent.click(screen.getByRole('button', { name: '重发给 again@example.com 的邀请' }));

      const dialog = await screen.findByRole('dialog');
      expect(within(dialog).getByText('新的邀请链接')).toBeInTheDocument();
      fireEvent.click(within(dialog).getByRole('button', { name: '复制邀请链接' }));
      expect(
        await within(dialog).findByRole('textbox', { name: 'again@example.com 的邀请链接' })
      ).toHaveValue(link);
      expect(within(dialog).getByRole('alert')).toHaveTextContent('复制失败。请选中链接后手动复制。');
    });
  });
});

function pendingInvitation(id: number, email: string) {
  return {
    id,
    organization_id: 42,
    email,
    role: 'member',
    status: 'pending',
    expires_at: '2026-07-22T10:00:00Z',
    email_send_status: 'accepted_by_provider',
    email_attempted_at: '2026-07-15T10:00:01Z',
    created_at: '2026-07-15T10:00:00Z',
    updated_at: '2026-07-15T10:00:00Z',
  };
}

function mutationState() {
  return {
    error: null as unknown,
    isPending: false,
    variables: undefined as unknown,
    mutate: vi.fn(),
    reset: vi.fn(),
  };
}

function organizationContext(role: 'admin' | 'member' | 'owner') {
  return {
    organization_id: 42,
    organization_name: 'Acme Europe',
    organization_slug: 'acme-europe',
    membership_id: 91,
    user_id: 17,
    role,
  };
}

function memberView(overrides: Record<string, unknown>) {
  return {
    id: 92,
    user_id: 18,
    username: 'alex',
    nickname: 'Alex',
    role: 'member',
    joined_at: '2026-07-15T10:00:00Z',
    updated_at: '2026-07-15T10:00:00Z',
    ...overrides,
  };
}

function activateTab(name: string) {
  const tab = screen.getByRole('tab', { name });
  fireEvent.mouseDown(tab, { button: 0, ctrlKey: false });
  fireEvent.click(tab);
}
