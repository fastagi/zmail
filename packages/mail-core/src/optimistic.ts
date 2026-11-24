import type { MailMessageSummary } from './transport.ts';

/**
 * Pure optimistic mutation queue for 0ms perceived latency on high-frequency mail actions.
 * Base server/cached state remains untouched; UI projects pending operations on top so
 * rollback simply removes the failed or cancelled operation from the queue.
 */

export type OptimisticMailActionType =
  | 'archive'
  | 'read'
  | 'unread'
  | 'star'
  | 'unstar'
  | 'delete';

export type OptimisticOperationStatus = 'pending' | 'failed';

export interface OptimisticMailOperation {
  id: string;
  type: OptimisticMailActionType;
  messageIds: readonly number[];
  createdAt: string;
  status: OptimisticOperationStatus;
  errorMessage?: string;
}

export interface OptimisticQueueState {
  operations: readonly OptimisticMailOperation[];
}

export interface EnqueueOptimisticInput {
  id: string;
  type: OptimisticMailActionType;
  messageIds: readonly number[];
  createdAt: string;
}

export function createOptimisticQueue(): OptimisticQueueState {
  return { operations: [] };
}

/**
 * Appends a new pending optimistic operation to the queue.
 * Duplicate IDs replace the prior entry to keep retries idempotent.
 */
export function enqueueOptimisticOperation(
  queue: OptimisticQueueState,
  input: EnqueueOptimisticInput
): OptimisticQueueState {
  const uniqueIds = Array.from(new Set(input.messageIds));
  if (uniqueIds.length === 0) {
    return queue;
  }

  const nextOp: OptimisticMailOperation = {
    id: input.id,
    type: input.type,
    messageIds: uniqueIds,
    createdAt: input.createdAt,
    status: 'pending',
  };

  const filtered = queue.operations.filter((op) => op.id !== input.id);
  return {
    operations: [...filtered, nextOp],
  };
}

/**
 * Removes a confirmed operation after the server acknowledges it.
 */
export function commitOptimisticOperation(
  queue: OptimisticQueueState,
  operationId: string
): OptimisticQueueState {
  const nextOps = queue.operations.filter((op) => op.id !== operationId);
  if (nextOps.length === queue.operations.length) {
    return queue;
  }
  return { operations: nextOps };
}

/**
 * Discards an optimistic operation, immediately restoring base server state on next projection.
 */
export function rollbackOptimisticOperation(
  queue: OptimisticQueueState,
  operationId: string
): OptimisticQueueState {
  return commitOptimisticOperation(queue, operationId);
}

/**
 * Marks an operation as failed so the UI can surface a retry/undo banner while keeping auditability.
 * Failed operations are excluded from optimistic projection.
 */
export function markOptimisticOperationFailed(
  queue: OptimisticQueueState,
  operationId: string,
  errorMessage: string
): OptimisticQueueState {
  let changed = false;
  const nextOps = queue.operations.map((op) => {
    if (op.id !== operationId) return op;
    changed = true;
    return {
      ...op,
      status: 'failed' as const,
      errorMessage,
    };
  });

  return changed ? { operations: nextOps } : queue;
}

/**
 * Applies a single optimistic operation onto one message summary.
 */
export function applyOptimisticOperationToMessage(
  message: MailMessageSummary,
  operation: OptimisticMailOperation
): MailMessageSummary {
  if (operation.status !== 'pending' || !operation.messageIds.includes(message.id)) {
    return message;
  }

  switch (operation.type) {
    case 'archive':
      return {
        ...message,
        placement: 'archive',
        folder_id: null,
        trashed_at: null,
      };
    case 'read':
      return {
        ...message,
        read: true,
      };
    case 'unread':
      return {
        ...message,
        read: false,
      };
    case 'star':
      return {
        ...message,
        starred: true,
      };
    case 'unstar':
      return {
        ...message,
        starred: false,
      };
    case 'delete':
      return {
        ...message,
        placement: 'trash',
        folder_id: null,
        trashed_at: operation.createdAt,
      };
  }
}

/**
 * Projects a base message list through all pending optimistic operations in FIFO order.
 */
export function projectMessagesWithOptimisticQueue(
  messages: readonly MailMessageSummary[],
  queue: OptimisticQueueState
): MailMessageSummary[] {
  const pendingOps = queue.operations.filter((op) => op.status === 'pending');
  if (pendingOps.length === 0) {
    return [...messages];
  }

  return messages.map((message) => {
    let projected = message;
    for (const op of pendingOps) {
      projected = applyOptimisticOperationToMessage(projected, op);
    }
    return projected;
  });
}

/**
 * Returns true if any pending optimistic operation targets the given message ID.
 */
export function hasPendingOptimisticOperation(
  queue: OptimisticQueueState,
  messageId: number
): boolean {
  return queue.operations.some(
    (op) => op.status === 'pending' && op.messageIds.includes(messageId)
  );
}
