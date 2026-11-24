import type { DeltaSyncCursor, DeltaSyncPage } from './delta-sync.ts';

/**
 * Platform-agnostic mail domain models and `MailTransport` contract.
 * Web BFF, Electron Desktop (local SQLite + direct API), and Mobile (React Native)
 * each implement `MailTransport` for their runtime environment.
 */

export type MailPlacement = 'default' | 'archive' | 'folder' | 'trash';
export type MailDirection = 'inbound' | 'outbound';
export type MailTriageCategory = 'reply' | 'deadline' | 'fyi' | 'review';

export interface MailTriage {
  category: MailTriageCategory;
  reason: string;
  due_at: string | null;
  source: 'ai' | 'manual';
}

export interface MailAttachmentSummary {
  id: number;
  filename: string;
  content_type: string;
  size: number;
}

export interface MailThreadSummary {
  message_count: number;
  message_ids: number[];
  unread: boolean;
  starred: boolean;
  label_ids: number[];
}

export interface MailMessageSummary {
  id: number;
  mailbox_id: number;
  thread_id: number;
  direction: MailDirection;
  status: string;
  from: string;
  to: string[];
  cc: string[];
  subject: string;
  read: boolean;
  starred: boolean;
  placement: MailPlacement;
  folder_id: number | null;
  label_ids: number[];
  attachment_count: number;
  trashed_at: string | null;
  created_at: string;
  thread?: MailThreadSummary;
  triage?: MailTriage;
}

export interface MailMessageDetail extends MailMessageSummary {
  text_body: string;
  html_body: string;
  bcc?: string[];
  message_id: string;
  in_reply_to: string;
  attachments: MailAttachmentSummary[];
}

export interface MailThreadDetail {
  thread_id: number;
  subject: string;
  messages: MailMessageDetail[];
}

export interface MailDraft {
  id: number;
  mailbox_id: number;
  to: string;
  cc: string;
  subject: string;
  body: string;
  in_reply_to: string;
  created_at: string;
  updated_at: string;
}

export interface MailDraftSummary {
  id: number;
  mailbox_id: number;
  to: string;
  cc: string;
  subject: string;
  preview: string;
  in_reply_to: string;
  created_at: string;
  updated_at: string;
}

export interface MailDraftInput {
  mailbox_id: number;
  to: string;
  cc: string;
  subject: string;
  body: string;
  in_reply_to: string;
}

export interface SendMailInput {
  mailbox_id: number;
  to: string[];
  cc?: string[];
  bcc?: string[];
  subject: string;
  text_body?: string;
  html_body?: string;
  in_reply_to?: string;
  draft_id?: number;
}

export interface SendMailResult {
  message: MailMessageSummary;
}

export interface InboxListQuery {
  organization_id: number;
  mailbox_id?: number;
  placement?: MailPlacement;
  folder_id?: number;
  label_id?: number;
  unread?: boolean;
  starred?: boolean;
  q?: string;
  group?: 'thread' | 'message';
  cursor?: string | null;
  per_page?: number;
}

export interface InboxListPage {
  items: MailMessageSummary[];
  has_more: boolean;
  next_cursor: string | null;
  total?: number;
}

export interface MailMessagePatch {
  read?: boolean;
  starred?: boolean;
  placement?: MailPlacement;
  folder_id?: number | null;
  label_ids?: number[];
}

export interface BulkMailMessageUpdate extends MailMessagePatch {
  message_ids: readonly number[];
}

export interface BulkMailMessageUpdateResult {
  updated_count: number;
}

/** Minimal cancellation signal shape compatible with Web, Node, Electron, and React Native. */
export interface AbortSignalLike {
  readonly aborted: boolean;
}

export interface MailTransportRequestOptions {
  organization_id?: number;
  signal?: AbortSignalLike;
}

export interface DeltaSyncRequestOptions extends MailTransportRequestOptions {
  mailbox_id?: number;
  limit?: number;
}

/**
 * Transport seam implemented per platform (Web BFF, Electron SQLite+Direct, Mobile RN).
 */
export interface MailTransport {
  listInbox(query: InboxListQuery, options?: MailTransportRequestOptions): Promise<InboxListPage>;
  getThread(threadId: number, options?: MailTransportRequestOptions): Promise<MailThreadDetail>;
  getMessage(messageId: number, options?: MailTransportRequestOptions): Promise<MailMessageDetail>;
  updateMessage(
    messageId: number,
    patch: MailMessagePatch,
    options?: MailTransportRequestOptions
  ): Promise<MailMessageSummary>;
  updateMessages(
    update: BulkMailMessageUpdate,
    options?: MailTransportRequestOptions
  ): Promise<BulkMailMessageUpdateResult>;
  archiveMessages(
    messageIds: readonly number[],
    options?: MailTransportRequestOptions
  ): Promise<BulkMailMessageUpdateResult>;
  deleteMessages(
    messageIds: readonly number[],
    options?: MailTransportRequestOptions
  ): Promise<BulkMailMessageUpdateResult>;
  sendMessage(
    input: SendMailInput,
    options?: MailTransportRequestOptions
  ): Promise<SendMailResult>;
  listDrafts(options?: MailTransportRequestOptions): Promise<MailDraftSummary[]>;
  getDraft(draftId: number, options?: MailTransportRequestOptions): Promise<MailDraft>;
  createDraft(input: MailDraftInput, options?: MailTransportRequestOptions): Promise<MailDraft>;
  updateDraft(
    draftId: number,
    input: MailDraftInput,
    options?: MailTransportRequestOptions
  ): Promise<MailDraft>;
  deleteDraft(draftId: number, options?: MailTransportRequestOptions): Promise<void>;
  syncDelta(cursor: DeltaSyncCursor, options?: DeltaSyncRequestOptions): Promise<DeltaSyncPage>;
}
