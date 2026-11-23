import { render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { MailClientsAvailable } from '@/features/mailbox/components/mail-clients-available';

const state = vi.hoisted(() => ({
  query: { data: undefined } as { data?: { access: { enabled: boolean; reason?: string } } },
}));
vi.mock('@/features/mailbox/hooks/use-mail-organization', () => ({
  useChosenMailOrganization: () => ({ id: 12, role: 'member' }),
}));
vi.mock('@/features/mailbox/hooks/use-mail-app-passwords', () => ({
  useMailAppPasswords: () => state.query,
}));

function renderGate() {
  return render(
    <MailClientsAvailable>
      <span>Mail apps</span>
    </MailClientsAvailable>
  );
}

describe('MailClientsAvailable', () => {
  it('hides mail apps while the server has no mail gateway', () => {
    state.query = { data: { access: { enabled: false, reason: 'unavailable' } } };
    renderGate();
    expect(screen.queryByText('Mail apps')).not.toBeInTheDocument();
  });

  it('hides mail apps until the server answers', () => {
    state.query = { data: undefined };
    renderGate();
    expect(screen.queryByText('Mail apps')).not.toBeInTheDocument();
  });

  it('shows mail apps when they are switched off for the organization, so members learn why', () => {
    state.query = { data: { access: { enabled: false, reason: 'organization_disabled' } } };
    renderGate();
    expect(screen.getByText('Mail apps')).toBeInTheDocument();
  });

  it('shows mail apps when the server offers them', () => {
    state.query = { data: { access: { enabled: true } } };
    renderGate();
    expect(screen.getByText('Mail apps')).toBeInTheDocument();
  });
});
