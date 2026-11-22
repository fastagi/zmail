import { describe, expect, it, vi } from 'vitest';

vi.mock('server-only', () => ({}));

import { mailboxAccessFor } from '@/features/mailbox/server/mock-mailbox-store';

describe('mock connected mailbox access', () => {
  const owner = { userId: 5, role: 'member' } as Parameters<typeof mailboxAccessFor>[1];
  const connected = {
    type: 'personal' as const,
    origin: 'connected' as const,
    ownerUserId: 5,
    members: new Map(),
  };

  it('sends only when the account was bound with its SMTP server', () => {
    expect(mailboxAccessFor(connected, owner).send).toBe(false);
    expect(mailboxAccessFor({ ...connected, connectedSend: true }, owner).send).toBe(true);
    expect(mailboxAccessFor({ ...connected, connectedSend: true }, owner).organize).toBe(false);
  });
});
