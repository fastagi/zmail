import { expect } from 'vitest';

/**
 * Runs every mailbox service method against the mock BFF, so a mock response that drifts from the
 * service's schema fails as an invalid response. Loaded by `mock-bff-service-parity.test.ts`.
 * Mailboxes belong to an organization: every call names the harness's organization, of which the
 * harness user (user 1) is the owner; users 2 and 3 are the seeded admin and member.
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
    type: 'shared',
    members: [{ user_id: 1, permission: 'manage' }],
  });
  expect(mailbox).toMatchObject({ type: 'shared', webhooks_enabled: false, sealed_before: null });
  expect(mailbox.access).toEqual({
    view: true,
    send: true,
    manage_members: true,
    administer: true,
    see_history: true,
    organize: true,
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

  // Placements, stars, folders, labels, threads, counts, search, and the trash.
  expect(second.thread_id).toBe(welcome.id);
  const folder = await mailboxService.createFolder(organizationId, mailbox.id, { name: 'Kept' });
  expect(
    await mailboxService.renameFolder(organizationId, mailbox.id, folder.id, { name: 'Saved' })
  ).toMatchObject({ id: folder.id, name: 'Saved' });
  expect(await mailboxService.listFolders(organizationId, mailbox.id)).toHaveLength(1);
  const label = await mailboxService.createLabel(organizationId, mailbox.id, {
    name: 'Urgent',
    color: 'red',
  });
  expect(
    await mailboxService.updateLabel(organizationId, mailbox.id, label.id, {
      name: 'Later',
      color: 'blue',
    })
  ).toMatchObject({ id: label.id, color: 'blue' });
  expect(await mailboxService.listLabels(organizationId, mailbox.id)).toHaveLength(1);
  expect(
    await mailboxService.updateMessage(organizationId, mailbox.id, welcome.id, {
      starred: true,
      placement: 'folder',
      folder_id: folder.id,
      label_ids: [label.id],
    })
  ).toMatchObject({
    starred: true,
    placement: 'folder',
    folder_id: folder.id,
    label_ids: [label.id],
  });
  expect(
    await mailboxService.updateMessages(organizationId, mailbox.id, {
      message_ids: [first.id, second.id],
      placement: 'archive',
      add_label_ids: [label.id],
    })
  ).toEqual({ updated: 2 });
  const archived = await mailboxService.messages(organizationId, mailbox.id, {
    placement: 'archive',
    group: 'thread',
  });
  expect(archived.items.map(item => item.thread?.message_count)).toEqual([1, 1]);
  expect(
    (await mailboxService.messages(organizationId, mailbox.id, { q: 'second' })).items
  ).toHaveLength(1);
  expect(
    (await mailboxService.getThread(organizationId, mailbox.id, welcome.id)).map(item => item.id)
  ).toEqual([welcome.id, second.id]);
  expect(await mailboxService.getMessageCounts(organizationId, mailbox.id)).toEqual({
    placements: { default_inbound_unread: 0, archive_unread: 0 },
    folders: [{ folder_id: folder.id, unread: 0 }],
    starred: 1,
  });
  await mailboxService.deleteLabel(organizationId, mailbox.id, label.id);
  await mailboxService.deleteFolder(organizationId, mailbox.id, folder.id);
  await mailboxService.updateMessages(organizationId, mailbox.id, {
    message_ids: [first.id, second.id],
    placement: 'default',
  });

  // The log spans every mailbox, names the owner of each entry, and filters like the API.
  const other = await mailboxService.create(organizationId, {
    local_part: 'parity-two',
    type: 'personal',
    owner_user_id: 1,
  });
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

  // The unified inbox: cursor pages across the mailboxes the caller reads.
  const sentThreads = await mailboxService.listInbox(organizationId, {
    view: 'sent',
    group: 'thread',
    tz: 'UTC',
  });
  expect(sentThreads.items.map(item => item.id)).toEqual([second.id, first.id]);
  expect(sentThreads.items.map(item => item.snippet)).toEqual(['', 'Plain body']);
  expect(sentThreads.items[0]).toMatchObject({
    mailbox_address: mailbox.address,
    role_id: null,
    thread: { message_count: 1, message_ids: [second.id] },
  });
  expect(sentThreads.meta).toEqual({ per_page: 15, has_more: false, next_cursor: null });
  const newest = await mailboxService.listInbox(
    organizationId,
    { view: 'sent', tz: 'UTC' },
    undefined,
    1
  );
  expect(newest.items.map(item => item.id)).toEqual([second.id]);
  expect(newest.meta.has_more).toBe(true);
  const older = await mailboxService.listInbox(
    organizationId,
    { view: 'sent', tz: 'UTC' },
    newest.meta.next_cursor ?? undefined,
    1
  );
  expect(older.items.map(item => item.id)).toEqual([first.id]);
  expect(older.meta).toEqual({ per_page: 1, has_more: false, next_cursor: null });
  const received = await mailboxService.listInbox(organizationId, {
    view: 'all',
    mailboxId: other.id,
    roleId: 'none',
    tz: 'UTC',
  });
  expect(received.items.map(item => [item.mailbox_address, item.direction])).toEqual([
    [other.address, 'inbound'],
  ]);

  const today = await mailboxService.listInbox(organizationId, { view: 'today', tz: 'UTC' });
  expect(today.items.map(item => item.direction)).toEqual(['inbound', 'inbound']);
  const searched = await mailboxService.listInbox(organizationId, {
    view: 'all',
    q: 'FIRST',
    unread: false,
    tz: 'UTC',
  });
  expect(searched.items.map(item => item.id)).toEqual([first.id]);
  const counts = await mailboxService.inboxCounts(organizationId, 'Asia/Shanghai');
  expect(counts.views.all_unread).toBe(
    counts.mailboxes.reduce((sum, item) => sum + item.unread, 0)
  );
  expect(counts.mailboxes.map(item => item.mailbox_id)).toEqual([mailbox.id, other.id]);
  expect(counts.views.today).toBe(2);

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

  // Members, the webhook switch, and the access history of the shared mailbox.
  expect(await mailboxService.setMember(organizationId, mailbox.id, 3, 'view')).toMatchObject({
    user_id: 3,
    permission: 'view',
    granted_by: 1,
  });
  expect(
    (await mailboxService.members(organizationId, mailbox.id)).map(member => member.user_id)
  ).toEqual([1, 3]);
  await mailboxService.removeMember(organizationId, mailbox.id, 3);
  expect(
    (await mailboxService.setWebhooks(organizationId, mailbox.id, true)).webhooks_enabled
  ).toBe(true);
  const history = await mailboxService.accessEvents(organizationId, mailbox.id, { perPage: 2 });
  expect(history.items.map(event => event.type)).toEqual(['webhooks_changed', 'member_removed']);
  expect(history.meta.total).toBe(5);

  // Handover of the personal mailbox seals its earlier mail, which can then be deleted.
  const handed = await mailboxService.handOver(organizationId, other.id, 2);
  expect(handed).toMatchObject({ id: other.id, type: 'shared', owner_user_id: null });
  expect(handed.sealed_before).not.toBeNull();
  expect(
    await mailboxService.deleteSealedHistory(organizationId, other.id, other.address.toUpperCase())
  ).toEqual({ deleted_messages: 1, deleted_broadcasts: 0 });

  await mailboxService.remove(organizationId, other.id);
  // The first delete moves the message to the trash; emptying the trash deletes it for good.
  await mailboxService.removeMessage(organizationId, mailbox.id, first.id);
  expect(await mailboxService.purgeTrash(organizationId, mailbox.id)).toEqual({ deleted: 1 });
  await mailboxService.remove(organizationId, mailbox.id);
  expect(await mailboxService.list(organizationId)).toEqual([]);
}
