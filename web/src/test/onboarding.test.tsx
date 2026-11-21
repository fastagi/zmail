import { act, fireEvent, render, screen } from '@testing-library/react';
import { NextIntlClientProvider, type AbstractIntlMessages } from 'next-intl';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { OrganizationDirectory } from '@/features/organization/components/organization-directory';
import { TeamSetupCard } from '@/features/organization/components/team-setup-card';
import {
  readInvitationFragment,
  stashPendingInvitation,
  takePendingInvitation,
} from '@/features/organization/utils/pending-invitation';
import { messages } from '@/i18n/modules';

const nav = vi.hoisted(() => ({
  push: vi.fn(),
  replace: vi.fn(),
  params: new URLSearchParams(),
}));
const state = vi.hoisted(() => ({
  organizations: { data: undefined as unknown, error: null, isPending: false, refetch: vi.fn() },
  create: { error: null as unknown, isPending: false, mutate: vi.fn(), reset: vi.fn() },
  accept: { error: null as unknown, isPending: false, mutate: vi.fn(), reset: vi.fn() },
}));

vi.mock('next/navigation', () => ({
  useRouter: () => ({ push: nav.push, replace: nav.replace, prefetch: vi.fn() }),
  useSearchParams: () => nav.params,
  usePathname: () => '/console/organizations',
}));
vi.mock('@/features/organization/hooks/use-organizations', () => ({
  useOrganizations: () => state.organizations,
  useCreateOrganization: () => state.create,
  useAcceptOrganizationInvitation: () => state.accept,
}));

function renderWithMessages(children: React.ReactNode) {
  return render(
    <NextIntlClientProvider locale="en-US" messages={messages as unknown as AbstractIntlMessages}>
      {children}
    </NextIntlClientProvider>
  );
}

function succeed(mutation: { mutate: ReturnType<typeof vi.fn> }, organization: { id: number }) {
  const [, options] = mutation.mutate.mock.calls[0] as [
    unknown,
    { onSuccess: (value: unknown) => void },
  ];
  act(() => options.onSuccess(organization));
}

describe('onboarding a new user into a team', () => {
  beforeEach(() => {
    nav.push.mockReset();
    nav.replace.mockReset();
    nav.params = new URLSearchParams();
    state.organizations.data = { items: [], meta: { total: 0 } };
    for (const mutation of [state.create, state.accept]) {
      mutation.error = null;
      mutation.isPending = false;
      mutation.mutate.mockReset();
      mutation.reset.mockReset();
    }
    window.sessionStorage.clear();
  });

  it('names the team in one field and goes on to create the first mailbox', () => {
    renderWithMessages(<TeamSetupCard />);

    fireEvent.change(screen.getByLabelText('Team name'), { target: { value: '  Acme  ' } });
    fireEvent.click(screen.getByRole('button', { name: 'Create team' }));

    expect(state.create.mutate).toHaveBeenCalledWith(
      { name: 'Acme' },
      expect.objectContaining({ onSuccess: expect.any(Function) })
    );
    succeed(state.create, { id: 7 });
    expect(nav.push).toHaveBeenCalledWith('/console/mailboxes?org=7&new=mailbox');
  });

  it('refuses a one-letter team name without calling the API', () => {
    renderWithMessages(<TeamSetupCard />);

    fireEvent.change(screen.getByLabelText('Team name'), { target: { value: 'A' } });
    fireEvent.click(screen.getByRole('button', { name: 'Create team' }));

    expect(state.create.mutate).not.toHaveBeenCalled();
    expect(screen.getByText(/between 2 and 100 characters/u)).toBeInTheDocument();
  });

  it('shows the team setup in the empty organization list', () => {
    renderWithMessages(<OrganizationDirectory />);

    expect(screen.getByLabelText('Team name')).toBeInTheDocument();
  });

  it('reads the invitation token from the link fragment only', () => {
    expect(readInvitationFragment('#oinv_abc.def%2B%2F%3D')).toBe('oinv_abc.def+/=');
    expect(readInvitationFragment('')).toBeNull();
    expect(readInvitationFragment('#')).toBeNull();
    expect(readInvitationFragment('#%E0%A4%A')).toBeNull();
  });

  it('opens the accept dialog with the token an invitation link left in this tab', () => {
    stashPendingInvitation('oinv_secret.value');
    nav.params = new URLSearchParams('accept=1');
    renderWithMessages(<OrganizationDirectory />);

    const token = screen.getByLabelText('One-time token');
    expect(token).toHaveValue('oinv_secret.value');
    fireEvent.click(screen.getAllByRole('button', { name: 'Accept invitation' }).at(-1)!);

    expect(state.accept.mutate).toHaveBeenCalledWith(
      { token: 'oinv_secret.value' },
      expect.objectContaining({ onSuccess: expect.any(Function) })
    );
    succeed(state.accept, { id: 9 });
    // Joined, the invitee starts in the team's mail, and the token is gone from the tab.
    expect(nav.push).toHaveBeenCalledWith('/console/mailboxes?org=9');
    expect(takePendingInvitation()).toBeNull();
  });

  it('leaves the accept dialog closed without a stored token', () => {
    nav.params = new URLSearchParams('accept=1');
    renderWithMessages(<OrganizationDirectory />);

    expect(screen.queryByLabelText('One-time token')).not.toBeInTheDocument();
  });
});
