import { fireEvent, render, screen, within } from '@testing-library/react';
import { NextIntlClientProvider, type AbstractIntlMessages } from 'next-intl';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { OnboardingChecklist } from '@/features/mailbox/components/onboarding-checklist';
import { messages } from '@/i18n/modules';

const state = vi.hoisted(() => ({
  scope: { status: 'loading' } as unknown,
  mailboxes: [] as unknown[] | undefined,
  members: [] as unknown[],
  invitations: [] as unknown[],
  domains: [] as unknown[],
  mailDomains: [] as unknown[],
  user: { id: '1', name: 'Ada', email: 'ada@example.com' } as unknown,
}));

vi.mock('next/navigation', () => ({
  useRouter: () => ({ push: vi.fn(), replace: vi.fn(), prefetch: vi.fn() }),
}));
vi.mock('@/features/mailbox/hooks/use-mail-organization', () => ({
  useMailOrganization: () => state.scope,
}));
vi.mock('@/features/mailbox/hooks/use-mailbox', () => ({
  useMailboxes: () => ({ data: state.mailboxes }),
  useMailboxDomains: () => ({ data: state.domains }),
  useMailDomains: () => ({ data: state.mailDomains }),
}));
vi.mock('@/features/organization/hooks/use-organizations', () => ({
  useOrganizationMembers: () => ({ data: { items: state.members } }),
  useOrganizationInvitations: () => ({ data: { items: state.invitations } }),
  useCreateOrganization: () => ({ mutate: vi.fn(), reset: vi.fn(), isPending: false, error: null }),
}));
vi.mock('@/features/auth/store/auth-store', () => ({
  useCurrentUser: () => state.user,
}));

function ready(canManage = true) {
  return {
    status: 'ready',
    organization: { id: 4, name: 'Acme', canManage },
    organizations: [{ id: 4 }],
    select: vi.fn(),
  };
}

const shared = { domain: 'send.zmail.im', kind: 'shared', custom_domains_enabled: true };

function renderChecklist() {
  return render(
    <NextIntlClientProvider locale="en-US" messages={messages as unknown as AbstractIntlMessages}>
      <OnboardingChecklist />
    </NextIntlClientProvider>
  );
}

function steps() {
  return within(screen.getByRole('list', { name: /Set up/u })).getAllByRole('listitem');
}

function step(name: RegExp) {
  return screen.getByRole('listitem', { name });
}

function connected(overrides: Record<string, unknown>) {
  return {
    id: 9,
    name: 'acme.com',
    status: 'pending',
    setting_up: false,
    zone: null,
    ...overrides,
  };
}

describe('setup guide on the console home', () => {
  beforeEach(() => {
    window.localStorage.clear();
    state.scope = ready();
    state.mailboxes = [];
    state.members = [{ user_id: 1 }];
    state.invitations = [];
    state.domains = [shared];
    state.mailDomains = [];
  });

  it('asks a user without a team to name one, with the button ready before typing', () => {
    state.scope = { status: 'empty' };
    renderChecklist();

    expect(screen.getByLabelText('Team name')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Create team' })).toBeEnabled();
    // The whole way ahead is visible from the start.
    expect(steps()).toHaveLength(4);
    expect(step(/Create your team/u)).toHaveAttribute('data-state', 'current');
  });

  it('puts the company domain right after the team', () => {
    renderChecklist();

    expect(steps().map(item => item.getAttribute('data-step'))).toEqual([
      'team',
      'domain',
      'mailbox',
      'invite',
    ]);
    expect(step(/Create your team/u)).toHaveAttribute('data-state', 'done');
    const domain = step(/company domain/u);
    expect(domain).toHaveAttribute('data-state', 'current');
    expect(within(domain).getByRole('link', { name: 'Connect domain' })).toHaveAttribute(
      'href',
      '/console/admin/domains'
    );
    expect(screen.getByText('1 of 4 done')).toBeInTheDocument();
  });

  it('lets an individual skip the domain and remembers it', () => {
    renderChecklist();

    fireEvent.click(screen.getByRole('button', { name: /use @send\.zmail\.im/u }));

    expect(step(/company domain/u)).toHaveAttribute('data-state', 'skipped');
    expect(step(/Create your mailbox/u)).toHaveAttribute('data-state', 'current');
    expect(screen.getByText('2 of 4 done')).toBeInTheDocument();
  });

  it('shows a pending zone as in progress with what is left', () => {
    state.mailDomains = [connected({ zone: { id: 3, name: 'acme.com', status: 'pending' } })];
    renderChecklist();

    const domain = step(/company domain/u);
    expect(domain).toHaveAttribute('data-state', 'waiting');
    expect(within(domain).getByText(/name servers/u)).toBeInTheDocument();
  });

  it('shows a domain being set up as in progress', () => {
    state.mailDomains = [connected({ setting_up: true })];
    renderChecklist();

    expect(within(step(/company domain/u)).getByText(/Setting up the mail records/u)).toBeVisible();
  });

  it('offers a mailbox on the verified domain', () => {
    state.mailDomains = [connected({ status: 'verified' })];
    renderChecklist();

    expect(step(/company domain/u)).toHaveAttribute('data-state', 'done');
    const mailbox = step(/Create your mailbox/u);
    expect(mailbox).toHaveAttribute('data-state', 'current');
    expect(within(mailbox).getByRole('link', { name: /@acme\.com/u })).toHaveAttribute(
      'href',
      '/console/mailboxes?org=4&new=mailbox'
    );
  });

  it('shows who joined, who has not, and who still needs a mailbox', () => {
    state.mailDomains = [connected({ status: 'verified' })];
    state.mailboxes = [{ id: 1, type: 'personal', owner_user_id: 1 }];
    state.members = [{ user_id: 1 }, { user_id: 2 }];
    state.invitations = [
      { id: 1, status: 'accepted' },
      { id: 2, status: 'pending' },
      { id: 3, status: 'pending' },
    ];
    renderChecklist();

    const invite = step(/Invite colleagues/u);
    expect(invite).toHaveAttribute('data-state', 'current');
    expect(within(invite).getByText('1 joined · 2 not yet')).toBeInTheDocument();
    expect(within(invite).getByText('1 colleague has no mailbox yet')).toBeInTheDocument();
    expect(within(invite).getByRole('link', { name: 'See who joined' })).toHaveAttribute(
      'href',
      '/console/organizations/4?tab=invitations'
    );
  });

  it('steps aside once everyone has a mailbox on the team domain', () => {
    state.mailDomains = [connected({ status: 'verified' })];
    state.mailboxes = [
      { id: 1, type: 'personal', owner_user_id: 1 },
      { id: 2, type: 'personal', owner_user_id: 2 },
    ];
    state.members = [{ user_id: 1 }, { user_id: 2 }];
    renderChecklist();

    expect(screen.queryByRole('list', { name: /Set up/u })).not.toBeInTheDocument();
  });

  it('leaves the domain out where the server has no own domains', () => {
    state.domains = [{ ...shared, custom_domains_enabled: false }];
    renderChecklist();

    expect(steps().map(item => item.getAttribute('data-step'))).toEqual([
      'team',
      'mailbox',
      'invite',
    ]);
  });

  it('waits for every list before showing anything, so a set-up team sees no flash', () => {
    state.mailboxes = undefined;
    renderChecklist();

    expect(screen.queryByRole('list', { name: /Set up/u })).not.toBeInTheDocument();
  });

  it('stays out of the way of members, who cannot set the team up', () => {
    state.scope = ready(false);
    renderChecklist();

    expect(screen.queryByRole('list', { name: /Set up/u })).not.toBeInTheDocument();
  });
});
