import { render, screen, within } from '@testing-library/react';
import { NextIntlClientProvider, type AbstractIntlMessages } from 'next-intl';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { OnboardingChecklist } from '@/features/mailbox/components/onboarding-checklist';
import { messages } from '@/i18n/modules';

const state = vi.hoisted(() => ({
  scope: { status: 'loading' } as unknown,
  mailboxes: [] as unknown[],
  members: [] as unknown[],
  domains: [] as unknown[],
}));

vi.mock('@/features/mailbox/hooks/use-mail-organization', () => ({
  useMailOrganization: () => state.scope,
}));
vi.mock('@/features/mailbox/hooks/use-mailbox', () => ({
  useMailboxes: () => ({ data: state.mailboxes }),
  useMailboxDomains: () => ({ data: state.domains }),
}));
vi.mock('@/features/organization/hooks/use-organizations', () => ({
  useOrganizationMembers: () => ({ data: { items: state.members } }),
  useCreateOrganization: () => ({ mutate: vi.fn(), reset: vi.fn(), isPending: false, error: null }),
}));

function ready(canManage = true) {
  return {
    status: 'ready',
    organization: { id: 4, name: 'Acme', canManage },
    organizations: [{ id: 4 }],
    select: vi.fn(),
  };
}

function renderChecklist() {
  return render(
    <NextIntlClientProvider locale="en-US" messages={messages as unknown as AbstractIntlMessages}>
      <OnboardingChecklist />
    </NextIntlClientProvider>
  );
}

function step(name: RegExp) {
  return screen.getByRole('listitem', { name });
}

describe('getting started checklist', () => {
  beforeEach(() => {
    state.scope = ready();
    state.mailboxes = [];
    state.members = [{ user_id: 1 }];
    state.domains = [{ domain: 'send.zmail.im', kind: 'shared' }];
  });

  it('asks a user without a team to name one first', () => {
    state.scope = { status: 'empty' };
    renderChecklist();

    expect(screen.getByLabelText('Team name')).toBeInTheDocument();
  });

  it('ticks what a new team has done and links the next step', () => {
    renderChecklist();

    expect(step(/Create your team/u)).toHaveAttribute('data-done', 'true');
    const mailbox = step(/first mailbox/u);
    expect(mailbox).toHaveAttribute('data-done', 'false');
    expect(within(mailbox).getByRole('link')).toHaveAttribute(
      'href',
      '/console/mailboxes?org=4&new=mailbox'
    );
    expect(step(/Invite your colleagues/u)).toHaveAttribute('data-done', 'false');
    expect(within(step(/Invite your colleagues/u)).getByRole('link')).toHaveAttribute(
      'href',
      '/console/organizations/4?tab=invitations'
    );
    expect(step(/your own domain/u)).toHaveAttribute('data-done', 'false');
  });

  it('ticks mailboxes, colleagues, and an own domain as they happen', () => {
    state.mailboxes = [{ id: 1 }];
    state.members = [{ user_id: 1 }, { user_id: 2 }];
    state.domains = [
      { domain: 'send.zmail.im', kind: 'shared' },
      { domain: 'acme.com', kind: 'organization' },
    ];
    renderChecklist();

    // Everything done: the checklist steps aside.
    expect(screen.queryByRole('list', { name: /Getting started/u })).not.toBeInTheDocument();
  });

  it('stays out of the way of members, who cannot set the team up', () => {
    state.scope = ready(false);
    renderChecklist();

    expect(screen.queryByRole('list', { name: /Getting started/u })).not.toBeInTheDocument();
  });
});
