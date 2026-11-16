import { expect } from 'vitest';

/**
 * Runs every mailbox service method against the mock BFF, so a mock response that drifts from the
 * service's schema fails as an invalid response. Loaded by `mock-bff-service-parity.test.ts`.
 * Mailboxes belong to an organization: every call names the harness's organization, of which the
 * harness user is the owner.
 */
export async function runServiceParity(context: { organizationId: number }): Promise<void> {
  const { organizationId } = context;
  const { mailboxService } = await import('@/features/mailbox/services/mailbox-service');
  const { mockMailboxStore } = await import('@/features/mailbox/server/mock-mailbox-store');
  mockMailboxStore.reset();

  const [domain] = await mailboxService.domains(organizationId);
  expect(domain).toMatchObject({
    max_mailboxes: 50,
    provider: 'log',
    inbound_enabled: false,
    daily_send_limit: 200,
  });

  const mailbox = await mailboxService.create(organizationId, {
    local_part: 'Parity',
    display_name: 'Parity Box',
  });
  expect(mailbox.address).toBe(`parity@${domain.domain}`);
  expect((await mailboxService.list(organizationId)).map(item => item.id)).toEqual([mailbox.id]);

  const inbox = await mailboxService.messages(organizationId, mailbox.id, { direction: 'inbound' });
  expect(inbox.items).toHaveLength(1);
  const welcome = await mailboxService.message(organizationId, mailbox.id, inbox.items[0].id);
  expect(welcome.read).toBe(false);
  expect(welcome).toMatchObject({ last_event: '', events: [] });
  expect((await mailboxService.setRead(organizationId, mailbox.id, welcome.id, true)).read).toBe(
    true
  );

  const first = await mailboxService.send(organizationId, mailbox.id, {
    to: ['Bob@Example.com'],
    subject: 'First',
    text: 'Plain body',
  });
  expect(first).toMatchObject({ status: 'sent', to: ['bob@example.com'], read: true });
  expect(first.last_event).toBe('delivered');
  expect(first.events.map(event => [event.type, event.recipient])).toEqual([
    ['accepted', ''],
    ['delivered', 'bob@example.com'],
  ]);
  const second = await mailboxService.send(organizationId, mailbox.id, {
    to: ['bob@example.com'],
    cc: ['carol@example.com'],
    subject: 'Second',
    html: '<p>HTML body</p>',
    in_reply_to: `<${welcome.message_id}>`,
  });
  expect(second.in_reply_to).toBe(welcome.message_id);

  const sent = await mailboxService.messages(organizationId, mailbox.id, {
    direction: 'outbound',
    page: 1,
    perPage: 1,
  });
  expect(sent.items.map(item => item.id)).toEqual([second.id]);
  expect(sent.items[0].last_event).toBe('delivered');
  expect(sent.meta).toMatchObject({ total: 2, last_page: 2 });

  // The log spans every mailbox, names the owner of each entry, and filters like the API.
  const other = await mailboxService.create(organizationId, { local_part: 'parity-two' });
  const log = await mailboxService.listLog(organizationId);
  expect(log.items.map(item => item.id)).toEqual(
    [...log.items.map(item => item.id)].sort((left, right) => right - left)
  );
  expect(log.meta.total).toBe(4);
  expect(log.items.map(item => item.last_event).sort()).toEqual(['', '', 'delivered', 'delivered']);
  expect(new Set(log.items.map(item => item.mailbox_address))).toEqual(
    new Set([mailbox.address, other.address])
  );
  const filtered = await mailboxService.listLog(organizationId, {
    direction: 'outbound',
    status: 'sent',
    mailboxId: mailbox.id,
    q: 'SECOND',
    page: 1,
    perPage: 1,
  });
  expect(filtered.items.map(item => item.id)).toEqual([second.id]);
  expect(filtered.meta).toMatchObject({ total: 1, last_page: 1 });
  expect((await mailboxService.listLog(organizationId, { q: '100%' })).items).toEqual([]);

  const week = await mailboxService.stats(organizationId);
  expect(week).toHaveLength(7);
  const totals = week.reduce(
    (sum, day) => ({
      sent: sum.sent + day.sent,
      failed: sum.failed + day.failed,
      received: sum.received + day.received,
    }),
    { sent: 0, failed: 0, received: 0 }
  );
  expect(totals).toEqual({ sent: 2, failed: 0, received: 2 });
  expect(await mailboxService.stats(organizationId, 1)).toHaveLength(1);

  await mailboxService.remove(organizationId, other.id);
  await mailboxService.removeMessage(organizationId, mailbox.id, first.id);
  await mailboxService.remove(organizationId, mailbox.id);
  expect(await mailboxService.list(organizationId)).toEqual([]);
}
