import { fireEvent, render, screen, within } from '@testing-library/react';
import { NextIntlClientProvider, type AbstractIntlMessages } from 'next-intl';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { OrganizationInvitations } from '@/features/organization/components/organization-invitations';
import { messages } from '@/i18n/modules';

const toast = vi.hoisted(() => ({ success: vi.fn(), warning: vi.fn(), error: vi.fn() }));
const state = vi.hoisted(() => ({
  items: [] as unknown[],
  create: { mutate: vi.fn(), reset: vi.fn(), isPending: false, variables: undefined as unknown },
}));

vi.mock('sonner', () => ({ toast }));
vi.mock('@/features/organization/hooks/use-organizations', () => ({
  useOrganizationInvitations: () => ({
    data: { items: state.items, meta: { total: state.items.length } },
    isPending: false,
    error: null,
    refetch: vi.fn(),
  }),
  useRevokeOrganizationInvitation: () => ({ mutate: vi.fn(), reset: vi.fn(), isPending: false }),
  useResendOrganizationInvitation: () => ({ mutate: vi.fn(), reset: vi.fn(), isPending: false }),
  useCreateOrganizationInvitation: () => state.create,
}));

function invitation(id: number, email: string, status: string, created = '2026-11-20T10:00:00Z') {
  return {
    id,
    organization_id: 4,
    email,
    role: 'member',
    status,
    expires_at: '2026-11-27T10:00:00Z',
    email_send_status: 'accepted_by_provider',
    email_attempted_at: created,
    created_at: created,
    updated_at: created,
  };
}

function renderInvitations() {
  return render(
    <NextIntlClientProvider locale="en-US" messages={messages as unknown as AbstractIntlMessages}>
      <OrganizationInvitations organizationId={4} />
    </NextIntlClientProvider>
  );
}

describe('who joined and who has not', () => {
  beforeEach(() => {
    state.create.mutate.mockReset();
    for (const fn of Object.values(toast)) fn.mockReset();
    state.items = [
      invitation(1, 'ada@acme.com', 'accepted'),
      invitation(2, 'bob@acme.com', 'expired', '2026-11-01T10:00:00Z'),
      invitation(3, 'cy@acme.com', 'pending'),
      invitation(4, 'dee@acme.com', 'revoked'),
      // Dee was invited again later: the revoked one is history, not someone still waiting.
      invitation(5, 'dee@acme.com', 'pending', '2026-11-21T10:00:00Z'),
    ];
  });

  it('sums up joined, waiting, and expired people at a glance', () => {
    renderInvitations();

    const summary = screen.getByRole('list', { name: 'Invitation summary' });
    expect(within(summary).getByText('1 joined')).toBeInTheDocument();
    expect(within(summary).getByText('2 not joined yet')).toBeInTheDocument();
    expect(within(summary).getByText('1 expired')).toBeInTheDocument();
  });

  it('lists people still waiting first', () => {
    renderInvitations();

    const emails = screen
      .getAllByRole('row')
      .slice(1)
      .map(row => within(row).getAllByRole('cell')[0]?.textContent);
    expect(emails.slice(0, 2)).toEqual(['dee@acme.com', 'cy@acme.com']);
  });

  it('invites someone again whose invitation expired, and only them', () => {
    renderInvitations();

    expect(screen.getAllByRole('button', { name: /^Invite .+ again$/u })).toHaveLength(1);
    fireEvent.click(screen.getByRole('button', { name: 'Invite bob@acme.com again' }));

    expect(state.create.mutate).toHaveBeenCalledWith(
      { email: 'bob@acme.com', role: 'member' },
      expect.any(Object)
    );
    const [, options] = state.create.mutate.mock.calls[0] as [
      unknown,
      { onSuccess: (value: unknown) => void },
    ];
    options.onSuccess({
      invitation: invitation(6, 'bob@acme.com', 'pending'),
      email_send_status: 'accepted_by_provider',
    });
    expect(toast.success).toHaveBeenCalledWith('Invitation sent to bob@acme.com again.');
  });
});
