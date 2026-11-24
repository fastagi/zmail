import assert from 'node:assert/strict';
import test from 'node:test';

import {
  applyDeltaChanges,
  createEmptyDeltaInboxState,
  type DeltaSyncPage,
} from '../src/delta-sync.ts';
import type { MailMessageSummary } from '../src/transport.ts';

function makeMessage(overrides: Partial<MailMessageSummary> & { id: number }): MailMessageSummary {
  return {
    id: overrides.id,
    mailbox_id: overrides.mailbox_id ?? 1,
    thread_id: overrides.thread_id ?? overrides.id,
    direction: overrides.direction ?? 'inbound',
    status: overrides.status ?? 'received',
    from: overrides.from ?? 'alice@example.com',
    to: overrides.to ?? ['me@zmail.app'],
    cc: overrides.cc ?? [],
    subject: overrides.subject ?? `Message ${overrides.id}`,
    read: overrides.read ?? false,
    starred: overrides.starred ?? false,
    placement: overrides.placement ?? 'default',
    folder_id: overrides.folder_id ?? null,
    label_ids: overrides.label_ids ?? [],
    attachment_count: overrides.attachment_count ?? 0,
    trashed_at: overrides.trashed_at ?? null,
    created_at: overrides.created_at ?? '2026-11-24T10:00:00Z',
    thread: overrides.thread,
  };
}

test('applyDeltaChanges upserts messages sorted newest-first and updates cursor', () => {
  const initial = createEmptyDeltaInboxState();
  const page: DeltaSyncPage = {
    events: [
      {
        type: 'message.upserted',
        event_id: 'evt_1',
        occurred_at: '2026-11-24T10:01:00Z',
        message: makeMessage({ id: 10, created_at: '2026-11-24T09:00:00Z' }),
      },
      {
        type: 'message.upserted',
        event_id: 'evt_2',
        occurred_at: '2026-11-24T10:02:00Z',
        message: makeMessage({ id: 11, created_at: '2026-11-24T10:00:00Z' }),
      },
    ],
    next_cursor: 'cur_abc',
    has_more: false,
    synced_at: '2026-11-24T10:05:00Z',
  };

  const next = applyDeltaChanges(initial, page);

  assert.deepEqual(next.orderedIds, [11, 10]);
  assert.deepEqual(next.cursor, { token: 'cur_abc', updated_at: '2026-11-24T10:05:00Z' });
  assert.equal(initial.orderedIds.length, 0);
});

test('applyDeltaChanges handles flags, moves, thread updates, and deletions', () => {
  const seeded = applyDeltaChanges(createEmptyDeltaInboxState(), {
    events: [
      {
        type: 'message.upserted',
        event_id: 'evt_1',
        occurred_at: '2026-11-24T10:00:00Z',
        message: makeMessage({ id: 1, thread_id: 100, created_at: '2026-11-24T10:00:00Z' }),
      },
      {
        type: 'message.upserted',
        event_id: 'evt_2',
        occurred_at: '2026-11-24T10:01:00Z',
        message: makeMessage({ id: 2, thread_id: 200, created_at: '2026-11-24T10:01:00Z' }),
      },
    ],
    next_cursor: 'cur_1',
    has_more: false,
    synced_at: '2026-11-24T10:01:00Z',
  });

  const updated = applyDeltaChanges(seeded, {
    events: [
      {
        type: 'message.flags_updated',
        event_id: 'evt_3',
        occurred_at: '2026-11-24T10:02:00Z',
        message_id: 1,
        read: true,
        starred: true,
        label_ids: [7, 8],
      },
      {
        type: 'message.moved',
        event_id: 'evt_4',
        occurred_at: '2026-11-24T10:03:00Z',
        message_id: 1,
        placement: 'archive',
        folder_id: null,
        trashed_at: null,
      },
      {
        type: 'thread.updated',
        event_id: 'evt_5',
        occurred_at: '2026-11-24T10:04:00Z',
        thread_id: 100,
        thread: {
          message_count: 2,
          message_ids: [1, 3],
          unread: false,
          starred: true,
          label_ids: [7, 8],
        },
      },
      {
        type: 'message.deleted',
        event_id: 'evt_6',
        occurred_at: '2026-11-24T10:05:00Z',
        message_id: 2,
      },
    ],
    next_cursor: 'cur_2',
    has_more: false,
    synced_at: '2026-11-24T10:05:00Z',
  });

  assert.deepEqual(updated.orderedIds, [1]);
  assert.equal(updated.messagesById[1]?.read, true);
  assert.equal(updated.messagesById[1]?.starred, true);
  assert.deepEqual(updated.messagesById[1]?.label_ids, [7, 8]);
  assert.equal(updated.messagesById[1]?.placement, 'archive');
  assert.equal(updated.messagesById[1]?.thread?.message_count, 2);
  assert.equal(updated.messagesById[2], undefined);
});

test('applyDeltaChanges resets local state when reset_required is true', () => {
  const seeded = applyDeltaChanges(createEmptyDeltaInboxState(), {
    events: [
      {
        type: 'message.upserted',
        event_id: 'evt_1',
        occurred_at: '2026-11-24T10:00:00Z',
        message: makeMessage({ id: 1 }),
      },
    ],
    next_cursor: 'cur_old',
    has_more: false,
    synced_at: '2026-11-24T10:00:00Z',
  });

  const reset = applyDeltaChanges(seeded, {
    events: [
      {
        type: 'message.upserted',
        event_id: 'evt_2',
        occurred_at: '2026-11-24T11:00:00Z',
        message: makeMessage({ id: 99 }),
      },
    ],
    next_cursor: 'cur_new',
    has_more: false,
    reset_required: true,
    synced_at: '2026-11-24T11:00:00Z',
  });

  assert.deepEqual(reset.orderedIds, [99]);
  assert.equal(reset.messagesById[1], undefined);
  assert.equal(reset.cursor.token, 'cur_new');
});
