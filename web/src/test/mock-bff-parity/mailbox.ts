import { expect } from 'vitest';

/**
 * Runs every mailbox service method against the mock BFF, so a mock response that drifts from the
 * service's schema fails as an invalid response. Loaded by `mock-bff-service-parity.test.ts`.
 * Mailboxes are user-owned, so the harness's organization is not used.
 */
export async function runServiceParity(): Promise<void> {
  const { mailboxService } = await import('@/features/mailbox/services/mailbox-service');
  const { mockMailboxStore } = await import('@/features/mailbox/server/mock-mailbox-store');
  mockMailboxStore.reset();

  const [domain] = await mailboxService.domains();
  expect(domain.max_mailboxes).toBe(10);

  const mailbox = await mailboxService.create({ local_part: 'Parity', display_name: 'Parity Box' });
  expect(mailbox.address).toBe(`parity@${domain.domain}`);
  expect((await mailboxService.list()).map(item => item.id)).toEqual([mailbox.id]);

  const inbox = await mailboxService.messages(mailbox.id, { direction: 'inbound' });
  expect(inbox.items).toHaveLength(1);
  const welcome = await mailboxService.message(mailbox.id, inbox.items[0].id);
  expect(welcome.read).toBe(false);
  expect((await mailboxService.setRead(mailbox.id, welcome.id, true)).read).toBe(true);

  const first = await mailboxService.send(mailbox.id, {
    to: ['Bob@Example.com'],
    subject: 'First',
    text: 'Plain body',
  });
  expect(first).toMatchObject({ status: 'sent', to: ['bob@example.com'], read: true });
  const second = await mailboxService.send(mailbox.id, {
    to: ['bob@example.com'],
    cc: ['carol@example.com'],
    subject: 'Second',
    html: '<p>HTML body</p>',
    in_reply_to: `<${welcome.message_id}>`,
  });
  expect(second.in_reply_to).toBe(welcome.message_id);

  const sent = await mailboxService.messages(mailbox.id, {
    direction: 'outbound',
    page: 1,
    perPage: 1,
  });
  expect(sent.items.map(item => item.id)).toEqual([second.id]);
  expect(sent.meta).toMatchObject({ total: 2, last_page: 2 });

  await mailboxService.removeMessage(mailbox.id, first.id);
  await mailboxService.remove(mailbox.id);
  expect(await mailboxService.list()).toEqual([]);
}
