import type { z } from 'zod/mini';

import {
  INBOX_ASSIGNMENTS,
  INBOX_CURSOR_MAX_LENGTH,
  INBOX_TIME_ZONE_MAX_LENGTH,
  INBOX_VIEWS,
  inboxCountsSchema,
  inboxPageEnvelopeSchema,
  isInboxDay,
  MAIL_LOG_QUERY_MAX_LENGTH,
  MAIL_MESSAGE_UPDATES_MAX_IDS,
  MAIL_PAGE_MAX_SIZE,
  MAIL_SEARCH_MAX_LENGTH,
  MAIL_STATS_DEFAULT_DAYS,
  MAIL_STATS_MAX_DAYS,
  MAIL_TRIAGE_CATEGORIES,
  mailDayStatsListSchema,
  mailFolderListSchema,
  mailFolderSchema,
  mailLabelListSchema,
  mailLabelSchema,
  mailLogPageEnvelopeSchema,
  mailMessageCountsSchema,
  mailMessagePageEnvelopeSchema,
  mailMessageSchema,
  mailMessageUpdatesResultSchema,
  mailThreadSchema,
  mailTrashPurgeSchema,
} from '@/features/mailbox/schemas';
import type {
  InboxCounts,
  InboxFilters,
  InboxPage,
  MailAttachment,
  MailDayStats,
  MailFolder,
  MailFolderInput,
  MailLabel,
  MailLabelInput,
  MailLogFilters,
  MailLogPage,
  MailMessage,
  MailMessageCounts,
  MailMessageFilters,
  MailMessagePage,
  MailMessageUpdatesResult,
  MailTrashPurge,
  SendMailMessageInput,
  UpdateMailMessageInput,
  UpdateMailMessagesInput,
} from '@/features/mailbox/types';
import {
  assertContract,
  type Accepts,
  type ContractData,
  type ContractRequest,
  type ContractResponse,
  type Sends,
} from '@/http/contract';
import request from '@/http/request';

import {
  invalidResponse,
  organizationHeaders,
  parseMailMessage,
  parseOrganizeItem,
  validId,
} from './mailbox-service-shared';

interface ListMessagesOptions extends MailMessageFilters {
  page?: number;
  perPage?: number;
}

interface ListLogOptions extends MailLogFilters {
  page?: number;
  perPage?: number;
}

/** Messages, unified inbox, log, stats, attachments, threads, folders, and labels. */
export const mailMessageService = {
  /**
   * One page of a mailbox listing. Without `placement` every placement except the trash is
   * listed; the API refuses filter combinations the contract does not allow.
   */
  async messages(
    organizationId: number,
    mailboxId: number,
    { page = 1, perPage = 20, ...filters }: ListMessagesOptions
  ): Promise<MailMessagePage> {
    const headers = organizationHeaders(organizationId);
    validId(mailboxId);
    validId(page);
    if (!Number.isSafeInteger(perPage) || perPage < 1 || perPage > MAIL_PAGE_MAX_SIZE) {
      throw invalidResponse();
    }
    const value = await request.getEnvelope<unknown>(`/mailboxes/${mailboxId}/messages`, {
      headers,
      params: { ...messageFilterParams(filters), page, per_page: perPage },
    });
    const parsed = mailMessagePageEnvelopeSchema.safeParse(value);
    if (!parsed.success) throw invalidResponse();
    return { items: parsed.data.data, meta: parsed.data.meta, links: parsed.data.links };
  },

  /** Messages across every mailbox of the organization, newest first, without bodies. */
  async listLog(
    organizationId: number,
    { direction, status, mailboxId, q, page = 1, perPage = 20 }: ListLogOptions = {}
  ): Promise<MailLogPage> {
    validId(page);
    if (mailboxId !== undefined) validId(mailboxId);
    if (!Number.isSafeInteger(perPage) || perPage < 1 || perPage > MAIL_PAGE_MAX_SIZE) {
      throw invalidResponse();
    }
    const query = q?.trim() ?? '';
    if (Array.from(query).length > MAIL_LOG_QUERY_MAX_LENGTH) throw invalidResponse();
    const value = await request.getEnvelope<unknown>('/mail-messages', {
      headers: organizationHeaders(organizationId),
      params: {
        ...(direction ? { direction } : {}),
        ...(status ? { status } : {}),
        ...(mailboxId === undefined ? {} : { mailbox_id: mailboxId }),
        ...(query === '' ? {} : { q: query }),
        page,
        per_page: perPage,
      },
    });
    const parsed = mailLogPageEnvelopeSchema.safeParse(value);
    if (!parsed.success) throw invalidResponse();
    return { items: parsed.data.data, meta: parsed.data.meta, links: parsed.data.links };
  },

  /**
   * One cursor page of the unified inbox, newest first; `cursor` is the previous page's
   * `meta.next_cursor`. Without `perPage` the API's default applies. The API refuses filter
   * combinations the contract does not allow.
   */
  async listInbox(
    organizationId: number,
    {
      view,
      roleId,
      mailboxId,
      unread,
      hasAttachments,
      q,
      group,
      triage,
      assignment,
      since,
      until,
      tz,
    }: InboxFilters,
    cursor?: string,
    perPage?: number
  ): Promise<InboxPage> {
    const headers = organizationHeaders(organizationId);
    if (!(INBOX_VIEWS as readonly string[]).includes(view)) throw invalidResponse();
    if (
      assignment !== undefined &&
      !(INBOX_ASSIGNMENTS as readonly string[]).includes(assignment)
    ) {
      throw invalidResponse();
    }
    if (roleId !== undefined && roleId !== 'none') validId(roleId);
    if (mailboxId !== undefined) validId(mailboxId);
    if ([since, until].some(day => day !== undefined && !isInboxDay(day))) throw invalidResponse();
    if (
      triage?.some(category => !(MAIL_TRIAGE_CATEGORIES as readonly string[]).includes(category))
    ) {
      throw invalidResponse();
    }
    const query = q?.trim();
    if (
      query !== undefined &&
      (query === '' || Array.from(q ?? '').length > MAIL_SEARCH_MAX_LENGTH)
    ) {
      throw invalidResponse();
    }
    if (tz === '' || tz.length > INBOX_TIME_ZONE_MAX_LENGTH) throw invalidResponse();
    if (cursor !== undefined && (cursor === '' || cursor.length > INBOX_CURSOR_MAX_LENGTH)) {
      throw invalidResponse();
    }
    if (
      perPage !== undefined &&
      (!Number.isSafeInteger(perPage) || perPage < 1 || perPage > MAIL_PAGE_MAX_SIZE)
    ) {
      throw invalidResponse();
    }
    const value = await request.getEnvelope<unknown>('/inbox-messages', {
      headers,
      params: {
        view,
        ...(roleId === undefined ? {} : { role_id: roleId }),
        ...(mailboxId === undefined ? {} : { mailbox_id: mailboxId }),
        ...(unread === undefined ? {} : { unread }),
        ...(hasAttachments === undefined ? {} : { has_attachments: hasAttachments }),
        ...(query === undefined ? {} : { q: query }),
        ...(group ? { group } : {}),
        ...(triage === undefined ? {} : { triage: triage.join(',') }),
        ...(assignment === undefined ? {} : { assignment }),
        ...(since === undefined ? {} : { since }),
        ...(until === undefined ? {} : { until }),
        tz,
        ...(cursor === undefined ? {} : { cursor }),
        ...(perPage === undefined ? {} : { per_page: perPage }),
      },
    });
    const parsed = inboxPageEnvelopeSchema.safeParse(value);
    if (!parsed.success) throw invalidResponse();
    return { items: parsed.data.data, meta: parsed.data.meta };
  },

  /**
   * The caller's own unified inbox counts: the views, each mail role, the mailboxes without one,
   * and each readable mailbox. `tz` is the IANA zone whose day `today` counts.
   */
  async inboxCounts(organizationId: number, tz: string): Promise<InboxCounts> {
    const headers = organizationHeaders(organizationId);
    if (tz === '' || tz.length > INBOX_TIME_ZONE_MAX_LENGTH) throw invalidResponse();
    const parsed = inboxCountsSchema.safeParse(
      await request.get<unknown>('/inbox-message-counts', { headers, params: { tz } })
    );
    if (!parsed.success) throw invalidResponse();
    return parsed.data;
  },

  /** One entry per UTC day for the last `days` days, oldest first, empty days included. */
  async stats(
    organizationId: number,
    days: number = MAIL_STATS_DEFAULT_DAYS
  ): Promise<MailDayStats[]> {
    if (!Number.isSafeInteger(days) || days < 1 || days > MAIL_STATS_MAX_DAYS) {
      throw invalidResponse();
    }
    const parsed = mailDayStatsListSchema.safeParse(
      await request.get<unknown>('/mail-message-stats', {
        headers: organizationHeaders(organizationId),
        params: { days },
      })
    );
    if (!parsed.success || parsed.data.length !== days) throw invalidResponse();
    return parsed.data;
  },

  /** The message with its masked codes and sign-in links shown; the API logs each call. */
  async revealMessage(
    organizationId: number,
    mailboxId: number,
    messageId: number
  ): Promise<MailMessage> {
    validId(mailboxId);
    validId(messageId);
    const value = await request.post<unknown>(
      `/mailboxes/${mailboxId}/messages/${messageId}/reveal`,
      undefined,
      { headers: organizationHeaders(organizationId) }
    );
    return parseMailMessage(value, messageId);
  },

  async message(
    organizationId: number,
    mailboxId: number,
    messageId: number
  ): Promise<MailMessage> {
    validId(mailboxId);
    validId(messageId);
    const value = await request.get<unknown>(`/mailboxes/${mailboxId}/messages/${messageId}`, {
      headers: organizationHeaders(organizationId),
    });
    return parseMailMessage(value, messageId);
  },

  /** The bytes of one stored file, checked against its listed size; nothing is opened. */
  async fetchAttachment(
    organizationId: number,
    mailboxId: number,
    messageId: number,
    attachment: MailAttachment
  ): Promise<Blob> {
    validId(mailboxId);
    validId(messageId);
    validId(attachment.id);
    const value = await request.get<unknown>(
      `/mailboxes/${mailboxId}/messages/${messageId}/attachments/${attachment.id}`,
      {
        headers: organizationHeaders(organizationId),
        responseType: 'blob',
      }
    );
    if (!(value instanceof Blob) || value.size !== attachment.size) throw invalidResponse();
    return value;
  },

  /**
   * Downloads one attached file and hands it to the browser as a file to save. The request
   * carries the organization header, so a plain link cannot be used. The bytes are never shown
   * in the page: the browser only saves them.
   */
  async downloadAttachment(
    organizationId: number,
    mailboxId: number,
    messageId: number,
    attachment: MailAttachment
  ): Promise<void> {
    const value = await this.fetchAttachment(organizationId, mailboxId, messageId, attachment);
    // A neutral type keeps the browser from opening the file in place.
    const file = new Blob([value], { type: 'application/octet-stream' });
    const objectUrl = URL.createObjectURL(file);
    try {
      const anchor = document.createElement('a');
      anchor.href = objectUrl;
      anchor.download = attachment.filename;
      anchor.rel = 'noopener noreferrer';
      anchor.click();
    } finally {
      // Revoke after the click has been handled, so the download can start.
      setTimeout(() => URL.revokeObjectURL(objectUrl), 0);
    }
  },

  /**
   * Resolves only when the provider accepted the message. A rejected send throws
   * `MAILBOX.DELIVERY_FAILED`; the message then stays in the sent folder as `failed`.
   */
  async send(
    organizationId: number,
    mailboxId: number,
    input: SendMailMessageInput
  ): Promise<MailMessage> {
    validId(mailboxId);
    const value = await request.post<unknown, SendMailMessageInput>(
      `/mailboxes/${mailboxId}/messages`,
      input,
      { headers: organizationHeaders(organizationId) }
    );
    return parseMailMessage(value);
  },

  async setRead(
    organizationId: number,
    mailboxId: number,
    messageId: number,
    read: boolean
  ): Promise<MailMessage> {
    return this.updateMessage(organizationId, mailboxId, messageId, { read });
  },

  /**
   * `read` and `starred` are the caller's own; `placement`, `folder_id`, and `label_ids` (the
   * complete set) change the message for everyone and need `send`.
   */
  async updateMessage(
    organizationId: number,
    mailboxId: number,
    messageId: number,
    input: UpdateMailMessageInput
  ): Promise<MailMessage> {
    const headers = organizationHeaders(organizationId);
    validId(mailboxId);
    validId(messageId);
    const value = await request.put<unknown, UpdateMailMessageInput>(
      `/mailboxes/${mailboxId}/messages/${messageId}`,
      input,
      { headers }
    );
    return parseMailMessage(value, messageId);
  },

  /** One change for 1 to 100 messages, all or nothing; a thread row sends its `message_ids`. */
  async updateMessages(
    organizationId: number,
    mailboxId: number,
    input: UpdateMailMessagesInput
  ): Promise<MailMessageUpdatesResult> {
    const headers = organizationHeaders(organizationId);
    validId(mailboxId);
    const count = input.message_ids.length;
    if (count < 1 || count > MAIL_MESSAGE_UPDATES_MAX_IDS) throw invalidResponse();
    input.message_ids.forEach(validId);
    const parsed = mailMessageUpdatesResultSchema.safeParse(
      await request.post<unknown, UpdateMailMessagesInput>(
        `/mailboxes/${mailboxId}/message-updates`,
        input,
        { headers }
      )
    );
    if (!parsed.success) throw invalidResponse();
    return parsed.data;
  },

  /** Deletes every message in the trash for good; needs `organize`. */
  async purgeTrash(organizationId: number, mailboxId: number): Promise<MailTrashPurge> {
    const headers = organizationHeaders(organizationId);
    validId(mailboxId);
    const parsed = mailTrashPurgeSchema.safeParse(
      await request.post<unknown>(`/mailboxes/${mailboxId}/trash-purges`, undefined, { headers })
    );
    if (!parsed.success) throw invalidResponse();
    return parsed.data;
  },

  /** The thread's messages outside the trash, oldest first, with their bodies. */
  async getThread(
    organizationId: number,
    mailboxId: number,
    threadId: number
  ): Promise<MailMessage[]> {
    const headers = organizationHeaders(organizationId);
    validId(mailboxId);
    validId(threadId);
    const parsed = mailThreadSchema.safeParse(
      await request.get<unknown>(`/mailboxes/${mailboxId}/threads/${threadId}`, { headers })
    );
    if (!parsed.success || parsed.data.some(message => message.thread_id !== threadId)) {
      throw invalidResponse();
    }
    return parsed.data;
  },

  /** The caller's own unread and starred counts for the mailbox's navigation. */
  async getMessageCounts(organizationId: number, mailboxId: number): Promise<MailMessageCounts> {
    const headers = organizationHeaders(organizationId);
    validId(mailboxId);
    const parsed = mailMessageCountsSchema.safeParse(
      await request.get<unknown>(`/mailboxes/${mailboxId}/message-counts`, { headers })
    );
    if (!parsed.success) throw invalidResponse();
    return parsed.data;
  },

  async listFolders(organizationId: number, mailboxId: number): Promise<MailFolder[]> {
    const headers = organizationHeaders(organizationId);
    validId(mailboxId);
    const parsed = mailFolderListSchema.safeParse(
      await request.get<unknown>(`/mailboxes/${mailboxId}/mail-folders`, { headers })
    );
    if (!parsed.success) throw invalidResponse();
    return parsed.data;
  },

  async createFolder(
    organizationId: number,
    mailboxId: number,
    input: MailFolderInput
  ): Promise<MailFolder> {
    const headers = organizationHeaders(organizationId);
    validId(mailboxId);
    return parseOrganizeItem(
      mailFolderSchema,
      await request.post<unknown, MailFolderInput>(`/mailboxes/${mailboxId}/mail-folders`, input, {
        headers,
      })
    );
  },

  async renameFolder(
    organizationId: number,
    mailboxId: number,
    folderId: number,
    input: MailFolderInput
  ): Promise<MailFolder> {
    const headers = organizationHeaders(organizationId);
    validId(mailboxId);
    validId(folderId);
    return parseOrganizeItem(
      mailFolderSchema,
      await request.put<unknown, MailFolderInput>(
        `/mailboxes/${mailboxId}/mail-folders/${folderId}`,
        input,
        { headers }
      ),
      folderId
    );
  },

  /** Its messages return to `default`. */
  async deleteFolder(organizationId: number, mailboxId: number, folderId: number): Promise<void> {
    const headers = organizationHeaders(organizationId);
    validId(mailboxId);
    validId(folderId);
    await request.delete<void>(`/mailboxes/${mailboxId}/mail-folders/${folderId}`, { headers });
  },

  async listLabels(organizationId: number, mailboxId: number): Promise<MailLabel[]> {
    const headers = organizationHeaders(organizationId);
    validId(mailboxId);
    const parsed = mailLabelListSchema.safeParse(
      await request.get<unknown>(`/mailboxes/${mailboxId}/mail-labels`, { headers })
    );
    if (!parsed.success) throw invalidResponse();
    return parsed.data;
  },

  async createLabel(
    organizationId: number,
    mailboxId: number,
    input: MailLabelInput
  ): Promise<MailLabel> {
    const headers = organizationHeaders(organizationId);
    validId(mailboxId);
    return parseOrganizeItem(
      mailLabelSchema,
      await request.post<unknown, MailLabelInput>(`/mailboxes/${mailboxId}/mail-labels`, input, {
        headers,
      })
    );
  },

  /** Replaces both the name and the color. */
  async updateLabel(
    organizationId: number,
    mailboxId: number,
    labelId: number,
    input: MailLabelInput
  ): Promise<MailLabel> {
    const headers = organizationHeaders(organizationId);
    validId(mailboxId);
    validId(labelId);
    return parseOrganizeItem(
      mailLabelSchema,
      await request.put<unknown, MailLabelInput>(
        `/mailboxes/${mailboxId}/mail-labels/${labelId}`,
        input,
        { headers }
      ),
      labelId
    );
  },

  /** Removes the label from every message. */
  async deleteLabel(organizationId: number, mailboxId: number, labelId: number): Promise<void> {
    const headers = organizationHeaders(organizationId);
    validId(mailboxId);
    validId(labelId);
    await request.delete<void>(`/mailboxes/${mailboxId}/mail-labels/${labelId}`, { headers });
  },

  async removeMessage(organizationId: number, mailboxId: number, messageId: number): Promise<void> {
    validId(mailboxId);
    validId(messageId);
    await request.delete<void>(`/mailboxes/${mailboxId}/messages/${messageId}`, {
      headers: organizationHeaders(organizationId),
    });
  },
};

/** The listing filters as the route's query; an invalid id or search never leaves the browser. */
function messageFilterParams(
  filters: MailMessageFilters
): Record<string, string | number | boolean> {
  const { direction, placement, folderId, labelId, starred, unread, hasAttachments, q, group } =
    filters;
  if (folderId !== undefined) validId(folderId);
  if (labelId !== undefined) validId(labelId);
  let search: string | undefined;
  if (q !== undefined) {
    search = q.trim();
    if (search === '' || Array.from(q).length > MAIL_SEARCH_MAX_LENGTH) throw invalidResponse();
  }
  return {
    ...(direction ? { direction } : {}),
    ...(placement ? { placement } : {}),
    ...(folderId === undefined ? {} : { folder_id: folderId }),
    ...(labelId === undefined ? {} : { label_id: labelId }),
    ...(starred === undefined ? {} : { starred }),
    ...(unread === undefined ? {} : { unread }),
    ...(hasAttachments === undefined ? {} : { has_attachments: hasAttachments }),
    ...(search === undefined ? {} : { q: search }),
    ...(group ? { group } : {}),
  };
}

assertContract<
  Accepts<z.input<typeof mailMessagePageEnvelopeSchema>, ContractResponse<'listMailMessages', 200>>
>();
assertContract<Accepts<z.input<typeof mailMessageSchema>, ContractData<'getMailMessage', 200>>>();
assertContract<
  Accepts<z.input<typeof mailMessageSchema>, ContractData<'revealMailMessage', 200>>
>();
assertContract<Accepts<z.input<typeof mailMessageSchema>, ContractData<'sendMailMessage', 201>>>();
assertContract<Sends<SendMailMessageInput, ContractRequest<'sendMailMessage'>>>();
assertContract<
  Accepts<z.input<typeof mailMessageSchema>, ContractData<'updateMailMessage', 200>>
>();
assertContract<Sends<UpdateMailMessageInput, ContractRequest<'updateMailMessage'>>>();
assertContract<
  Accepts<z.input<typeof mailLogPageEnvelopeSchema>, ContractResponse<'listMailLog', 200>>
>();
assertContract<
  Accepts<z.input<typeof inboxPageEnvelopeSchema>, ContractResponse<'listInboxMessages', 200>>
>();
assertContract<
  Accepts<z.input<typeof inboxCountsSchema>, ContractData<'getInboxMessageCounts', 200>>
>();
assertContract<
  Accepts<z.input<typeof mailDayStatsListSchema>, ContractData<'listMailMessageStats', 200>>
>();
assertContract<
  Accepts<z.input<typeof mailMessageUpdatesResultSchema>, ContractData<'updateMailMessages', 200>>
>();
assertContract<Sends<UpdateMailMessagesInput, ContractRequest<'updateMailMessages'>>>();
assertContract<
  Accepts<z.input<typeof mailTrashPurgeSchema>, ContractData<'purgeMailTrash', 200>>
>();
assertContract<Accepts<z.input<typeof mailThreadSchema>, ContractData<'getMailThread', 200>>>();
assertContract<
  Accepts<z.input<typeof mailMessageCountsSchema>, ContractData<'getMailMessageCounts', 200>>
>();
assertContract<
  Accepts<z.input<typeof mailFolderListSchema>, ContractData<'listMailFolders', 200>>
>();
assertContract<Accepts<z.input<typeof mailFolderSchema>, ContractData<'createMailFolder', 201>>>();
assertContract<Accepts<z.input<typeof mailFolderSchema>, ContractData<'renameMailFolder', 200>>>();
assertContract<Sends<MailFolderInput, ContractRequest<'createMailFolder'>>>();
assertContract<Sends<MailFolderInput, ContractRequest<'renameMailFolder'>>>();
assertContract<Accepts<z.input<typeof mailLabelListSchema>, ContractData<'listMailLabels', 200>>>();
assertContract<Accepts<z.input<typeof mailLabelSchema>, ContractData<'createMailLabel', 201>>>();
assertContract<Accepts<z.input<typeof mailLabelSchema>, ContractData<'updateMailLabel', 200>>>();
assertContract<Sends<MailLabelInput, ContractRequest<'createMailLabel'>>>();
assertContract<Sends<MailLabelInput, ContractRequest<'updateMailLabel'>>>();
