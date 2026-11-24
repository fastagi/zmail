import type { HttpClient, HttpRequestOptions } from './client.ts';
import type { CursorPage, PaginatedResponse } from './types.ts';

/** Query parameters for listing unified inbox items. */
export interface InboxListParams {
  mailbox_id?: number;
  placement?: 'default' | 'archive' | 'folder' | 'trash';
  folder_id?: number;
  label_id?: number;
  unread?: boolean;
  starred?: boolean;
  q?: string;
  group?: 'thread' | 'message';
  page?: number;
  per_page?: number;
  cursor?: string;
}

/** Minimal inbox item representation exposed by `@zmail/api-client/inbox`. */
export interface InboxMessageRecord {
  id: number;
  mailbox_id: number;
  thread_id: number;
  direction: 'inbound' | 'outbound';
  status: string;
  from: string;
  to: string[];
  cc: string[];
  subject: string;
  read: boolean;
  starred: boolean;
  placement: 'default' | 'archive' | 'folder' | 'trash';
  folder_id: number | null;
  label_ids: number[];
  attachment_count: number;
  trashed_at: string | null;
  created_at: string;
}

export interface InboxMessagePatchPayload {
  read?: boolean;
  starred?: boolean;
  placement?: 'default' | 'archive' | 'folder' | 'trash';
  folder_id?: number | null;
  label_ids?: number[];
}

export interface InboxBulkUpdatePayload extends InboxMessagePatchPayload {
  message_ids: number[];
}

export interface InboxBulkUpdateResult {
  updated_count: number;
}

export interface InboxApiClient {
  list(
    params?: InboxListParams,
    options?: HttpRequestOptions
  ): Promise<PaginatedResponse<InboxMessageRecord>>;
  listByCursor(
    params?: InboxListParams,
    options?: HttpRequestOptions
  ): Promise<CursorPage<InboxMessageRecord>>;
  updateMessage(
    messageId: number,
    patch: InboxMessagePatchPayload,
    options?: HttpRequestOptions
  ): Promise<InboxMessageRecord>;
  bulkUpdate(
    payload: InboxBulkUpdatePayload,
    options?: HttpRequestOptions
  ): Promise<InboxBulkUpdateResult>;
}

/**
 * Creates a typed inbox API client backed by a shared `HttpClient`.
 */
export function createInboxClient(client: HttpClient): InboxApiClient {
  return {
    list(params = {}, options = {}) {
      return client.getPaginated<InboxMessageRecord>('/inbox', {
        ...options,
        query: { ...params, ...options.query },
      });
    },

    listByCursor(params = {}, options = {}) {
      return client.getCursorPage<InboxMessageRecord>('/inbox', {
        ...options,
        query: { ...params, ...options.query },
      });
    },

    updateMessage(messageId, patch, options = {}) {
      return client.patch<InboxMessageRecord, InboxMessagePatchPayload>(
        `/inbox/messages/${messageId}`,
        patch,
        options
      );
    },

    bulkUpdate(payload, options = {}) {
      return client.post<InboxBulkUpdateResult, InboxBulkUpdatePayload>(
        '/inbox/message-updates',
        payload,
        options
      );
    },
  };
}
