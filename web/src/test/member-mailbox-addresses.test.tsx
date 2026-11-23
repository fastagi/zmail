import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen } from '@testing-library/react';
import { NextIntlClientProvider, type AbstractIntlMessages } from 'next-intl';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { Mailbox } from '@/features/mailbox/types';
import { messages } from '@/i18n/modules';

const service = vi.hoisted(() => ({ list: vi.fn() }));
vi.mock('@/features/mailbox/services/mailbox-service', () => ({ mailboxService: service }));

const { MemberMailboxAddresses, ownedPersonalAddresses } =
  await import('@/features/mailbox/components/admin/member-mailbox-addresses');

function mailbox(id: number, address: string, extra: Partial<Mailbox> = {}): Mailbox {
  return {
    id,
    address,
    display_name: '',
    type: 'personal',
    origin: 'zmail',
    owner_user_id: 5,
    awaiting_handover: false,
    sealed_before: null,
    webhooks_enabled: false,
    access: {
      view: false,
      send: false,
      manage_members: false,
      administer: true,
      see_history: true,
      organize: false,
    },
    unread_count: 0,
    created_at: '2026-11-23T08:00:00Z',
    ...extra,
  };
}

const mailboxes = [
  mailbox(1, 'ann@acme.example'),
  mailbox(2, 'team@acme.example', { type: 'shared', owner_user_id: null }),
  mailbox(3, 'ann.old@acme.example', { awaiting_handover: true }),
  mailbox(4, 'ann@gmail.com', { origin: 'connected' }),
  mailbox(5, 'bo@acme.example', { owner_user_id: 6 }),
  mailbox(6, 'ann.sales@acme.example'),
];

function renderAddresses(userId: number) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <NextIntlClientProvider locale="en-US" messages={messages as unknown as AbstractIntlMessages}>
        <MemberMailboxAddresses organizationId={7} userId={userId} />
      </NextIntlClientProvider>
    </QueryClientProvider>
  );
}

describe('member mailbox addresses', () => {
  beforeEach(() => service.list.mockReset());

  it('keeps only the member own Zmail personal mailboxes', () => {
    expect(ownedPersonalAddresses(mailboxes, 5)).toEqual([
      'ann@acme.example',
      'ann.sales@acme.example',
    ]);
    expect(ownedPersonalAddresses(mailboxes, 9)).toEqual([]);
  });

  it('shows the first address and counts the rest on one line', async () => {
    service.list.mockResolvedValue(mailboxes);
    renderAddresses(5);
    const first = await screen.findByText('ann@acme.example');
    expect(first).toHaveClass('truncate');
    expect(screen.getByText('+1 more')).toBeInTheDocument();
    expect(first.parentElement).toHaveAttribute(
      'title',
      'ann@acme.example, ann.sales@acme.example'
    );
  });

  it('shows nothing for a member without a mailbox', async () => {
    service.list.mockResolvedValue(mailboxes);
    const { container } = renderAddresses(9);
    await vi.waitFor(() => expect(service.list).toHaveBeenCalled());
    expect(container).toBeEmptyDOMElement();
  });
});
