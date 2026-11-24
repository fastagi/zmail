import type { MailMessageSummary, MailPlacement, MailThreadSummary } from './transport.ts';

/**
 * Opaque server-issued delta sync cursor for incremental local-first inbox caching.
 */
export interface DeltaSyncCursor {
  token: string | null;
  updated_at: string | null;
}

export interface MessageUpsertedDeltaEvent {
  type: 'message.upserted';
  event_id: string;
  occurred_at: string;
  message: MailMessageSummary;
}

export interface MessageDeletedDeltaEvent {
  type: 'message.deleted';
  event_id: string;
  occurred_at: string;
  message_id: number;
}

export interface MessageFlagsUpdatedDeltaEvent {
  type: 'message.flags_updated';
  event_id: string;
  occurred_at: string;
  message_id: number;
  read?: boolean;
  starred?: boolean;
  label_ids?: number[];
}

export interface MessageMovedDeltaEvent {
  type: 'message.moved';
  event_id: string;
  occurred_at: string;
  message_id: number;
  placement: MailPlacement;
  folder_id: number | null;
  trashed_at: string | null;
}

export interface ThreadUpdatedDeltaEvent {
  type: 'thread.updated';
  event_id: string;
  occurred_at: string;
  thread_id: number;
  thread: MailThreadSummary;
}

export type DeltaChangeEvent =
  | MessageUpsertedDeltaEvent
  | MessageDeletedDeltaEvent
  | MessageFlagsUpdatedDeltaEvent
  | MessageMovedDeltaEvent
  | ThreadUpdatedDeltaEvent;

/**
 * One page of incremental changes returned by a delta sync endpoint.
 */
export interface DeltaSyncPage {
  events: readonly DeltaChangeEvent[];
  next_cursor: string | null;
  has_more: boolean;
  /** When true, local cache must discard existing rows before applying `events`. */
  reset_required?: boolean;
  synced_at: string;
}

/**
 * Normalized local inbox cache state maintained by `applyDeltaChanges`.
 */
export interface DeltaInboxState {
  messagesById: Readonly<Record<number, MailMessageSummary>>;
  orderedIds: readonly number[];
  cursor: DeltaSyncCursor;
}

export function createInitialDeltaCursor(): DeltaSyncCursor {
  return {
    token: null,
    updated_at: null,
  };
}

export function createEmptyDeltaInboxState(): DeltaInboxState {
  return {
    messagesById: {},
    orderedIds: [],
    cursor: createInitialDeltaCursor(),
  };
}

function compareNewestFirst(a: MailMessageSummary, b: MailMessageSummary): number {
  const timeDelta = Date.parse(b.created_at) - Date.parse(a.created_at);
  if (!Number.isNaN(timeDelta) && timeDelta !== 0) {
    return timeDelta;
  }
  return b.id - a.id;
}

function applySingleEvent(
  messagesById: Record<number, MailMessageSummary>,
  event: DeltaChangeEvent
): void {
  switch (event.type) {
    case 'message.upserted': {
      messagesById[event.message.id] = {
        ...event.message,
        to: [...event.message.to],
        cc: [...event.message.cc],
        label_ids: [...event.message.label_ids],
      };
      return;
    }

    case 'message.deleted': {
      delete messagesById[event.message_id];
      return;
    }

    case 'message.flags_updated': {
      const existing = messagesById[event.message_id];
      if (!existing) return;
      messagesById[event.message_id] = {
        ...existing,
        read: event.read ?? existing.read,
        starred: event.starred ?? existing.starred,
        label_ids: event.label_ids ? [...event.label_ids] : existing.label_ids,
      };
      return;
    }

    case 'message.moved': {
      const existing = messagesById[event.message_id];
      if (!existing) return;
      messagesById[event.message_id] = {
        ...existing,
        placement: event.placement,
        folder_id: event.folder_id,
        trashed_at: event.trashed_at,
      };
      return;
    }

    case 'thread.updated': {
      for (const message of Object.values(messagesById)) {
        if (message.thread_id === event.thread_id) {
          messagesById[message.id] = {
            ...message,
            thread: {
              ...event.thread,
              message_ids: [...event.thread.message_ids],
              label_ids: [...event.thread.label_ids],
            },
          };
        }
      }
      return;
    }
  }
}

/**
 * Pure reducer that applies a `DeltaSyncPage` onto the current `DeltaInboxState`.
 * Returns a fresh immutable state without mutating the input state.
 */
export function applyDeltaChanges(
  state: DeltaInboxState,
  page: DeltaSyncPage
): DeltaInboxState {
  const nextMessages: Record<number, MailMessageSummary> = page.reset_required
    ? {}
    : { ...state.messagesById };

  for (const event of page.events) {
    applySingleEvent(nextMessages, event);
  }

  const orderedIds = Object.values(nextMessages)
    .sort(compareNewestFirst)
    .map((message) => message.id);

  const nextToken =
    page.next_cursor !== null
      ? page.next_cursor
      : page.reset_required
        ? null
        : state.cursor.token;

  return {
    messagesById: nextMessages,
    orderedIds,
    cursor: {
      token: nextToken,
      updated_at: page.synced_at,
    },
  };
}
