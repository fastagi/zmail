import assert from 'node:assert/strict';
import test from 'node:test';

import {
  commitOptimisticOperation,
  createOptimisticQueue,
  enqueueOptimisticOperation,
  hasPendingOptimisticOperation,
  markOptimisticOperationFailed,
  projectMessagesWithOptimisticQueue,
  rollbackOptimisticOperation,
} from '../src/optimistic.ts';
import type { MailMessageSummary } from '../src/transport.ts';

function makeMessage(id: number): MailMessageSummary {
  return {
    id,
    mailbox_id: 1,
    thread_id: id,
    direction: 'inbound',
    status: 'received',
    from: 'sender@example.com',
    to: ['me@zmail.app'],
    cc: [],
    subject: `Subject ${id}`,
    read: false,
    starred: false,
    placement: 'default',
    folder_id: null,
    label_ids: [],
    attachment_count: 0,
    trashed_at: null,
    created_at: '2026-11-24T10:00:00Z',
  };
}

test('optimistic queue projects read, star, archive, and delete in FIFO order', () => {
  const base = [makeMessage(1), makeMessage(2)];
  let queue = createOptimisticQueue();

  queue = enqueueOptimisticOperation(queue, {
    id: 'op_1',
    type: 'read',
    messageIds: [1],
    createdAt: '2026-11-24T10:01:00Z',
  });
  queue = enqueueOptimisticOperation(queue, {
    id: 'op_2',
    type: 'star',
    messageIds: [1],
    createdAt: '2026-11-24T10:01:01Z',
  });
  queue = enqueueOptimisticOperation(queue, {
    id: 'op_3',
    type: 'archive',
    messageIds: [1],
    createdAt: '2026-11-24T10:01:02Z',
  });
  queue = enqueueOptimisticOperation(queue, {
    id: 'op_4',
    type: 'delete',
    messageIds: [2],
    createdAt: '2026-11-24T10:01:03Z',
  });

  const projected = projectMessagesWithOptimisticQueue(base, queue);

  assert.equal(projected[0]?.read, true);
  assert.equal(projected[0]?.starred, true);
  assert.equal(projected[0]?.placement, 'archive');
  assert.equal(projected[1]?.placement, 'trash');
  assert.equal(projected[1]?.trashed_at, '2026-11-24T10:01:03Z');

  // Base server state remains immutable.
  assert.equal(base[0]?.read, false);
  assert.equal(base[1]?.placement, 'default');
  assert.equal(hasPendingOptimisticOperation(queue, 1), true);
});

test('rollback and failure exclude operations from optimistic projection', () => {
  const base = [makeMessage(10)];
  let queue = enqueueOptimisticOperation(createOptimisticQueue(), {
    id: 'op_star',
    type: 'star',
    messageIds: [10],
    createdAt: '2026-11-24T10:05:00Z',
  });

  assert.equal(projectMessagesWithOptimisticQueue(base, queue)[0]?.starred, true);

  const failed = markOptimisticOperationFailed(queue, 'op_star', 'Network error');
  assert.equal(projectMessagesWithOptimisticQueue(base, failed)[0]?.starred, false);
  assert.equal(hasPendingOptimisticOperation(failed, 10), false);
  assert.equal(failed.operations[0]?.errorMessage, 'Network error');

  const rolledBack = rollbackOptimisticOperation(queue, 'op_star');
  assert.equal(rolledBack.operations.length, 0);
  assert.equal(projectMessagesWithOptimisticQueue(base, rolledBack)[0]?.starred, false);

  const committed = commitOptimisticOperation(queue, 'op_star');
  assert.equal(committed.operations.length, 0);
});
