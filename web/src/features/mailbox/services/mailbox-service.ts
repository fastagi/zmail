import type { z } from 'zod/mini';

import {
  INBOX_CURSOR_MAX_LENGTH,
  INBOX_TIME_ZONE_MAX_LENGTH,
  INBOX_VIEWS,
  inboxCountsSchema,
  inboxPageEnvelopeSchema,
  MAILBOX_ACCESS_EVENTS_MAX_PAGE_SIZE,
  MAIL_LOG_QUERY_MAX_LENGTH,
  MAIL_MESSAGE_UPDATES_MAX_IDS,
  MAIL_PAGE_MAX_SIZE,
  MAIL_SEARCH_MAX_LENGTH,
  MAIL_STATS_DEFAULT_DAYS,
  MAIL_STATS_MAX_DAYS,
  mailDayStatsListSchema,
  mailLogPageEnvelopeSchema,
  mailMessagePageEnvelopeSchema,
  mailMessageSchema,
  mailboxDomainListSchema,
  mailDomainListSchema,
  mailDomainPreviewSchema,
  mailDomainSchema,
  mailDnsRecordListSchema,
  mailDnsRecordSchema,
  mailZoneListSchema,
  mailZoneSchema,
  MAIL_DNS_RECORD_ID_PATTERN,
  mailboxAccessEventPageEnvelopeSchema,
  mailboxListSchema,
  mailboxMemberListSchema,
  mailboxMemberSchema,
  mailboxSchema,
  mailboxSealedDeletionSchema,
  mailTemplateListSchema,
  mailContactImportSchema,
  mailBroadcastPageEnvelopeSchema,
  mailBroadcastRecipientPageEnvelopeSchema,
  mailBroadcastSchema,
  mailBroadcastStatusOnlySchema,
  mailContactListListSchema,
  mailContactListSchema,
  mailContactPageEnvelopeSchema,
  mailContactSchema,
  mailTemplateSchema,
  mailFolderListSchema,
  mailFolderSchema,
  mailLabelListSchema,
  mailLabelSchema,
  mailRoleListSchema,
  mailRoleSchema,
  mailMessageCountsSchema,
  mailMessageUpdatesResultSchema,
  mailThreadSchema,
  mailTrashPurgeSchema,
} from '@/features/mailbox/schemas';
import type {
  CreateMailboxInput,
  InboxCounts,
  InboxFilters,
  InboxPage,
  MailAttachment,
  MailBroadcast,
  MailBroadcastInput,
  MailBroadcastPage,
  MailBroadcastRecipientPage,
  MailBroadcastSendInput,
  MailBroadcastStatusOnly,
  MailRecipientStatus,
  MailContact,
  MailContactFilters,
  MailContactImport,
  MailContactImportInput,
  MailContactInput,
  MailContactList,
  MailContactListInput,
  MailContactPage,
  MailTemplate,
  MailTemplateInput,
  MailTemplateKind,
  MailTemplateSummary,
  MailTemplateTestInput,
  MailDayStats,
  MailLogFilters,
  MailLogPage,
  MailMessage,
  MailMessagePage,
  Mailbox,
  MailboxAccessEventPage,
  MailboxDomain,
  MailboxMember,
  MailboxPermission,
  MailboxSealedDeletion,
  MailDomain,
  MailDomainInput,
  MailDomainPreview,
  MailDomainPreviewInput,
  MailDnsRecord,
  MailDnsRecordInput,
  MailDnsRecordPatch,
  MailZone,
  SendMailMessageInput,
  UpdateMailMessageInput,
  UpdateMailMessagesInput,
  MailFolder,
  MailFolderInput,
  MailLabel,
  MailLabelInput,
  MailRole,
  MailRoleInput,
  MailMessageCounts,
  MailMessageFilters,
  MailMessageUpdatesResult,
  MailTrashPurge,
} from '@/features/mailbox/types';
import { ClientErrorCode } from '@/http/codes';
import {
  assertContract,
  type Accepts,
  type ContractData,
  type ContractRequest,
  type ContractResponse,
  type Sends,
} from '@/http/contract';
import request, { ApiError } from '@/http/request';

interface ListMessagesOptions extends MailMessageFilters {
  page?: number;
  perPage?: number;
}

interface ListLogOptions extends MailLogFilters {
  page?: number;
  perPage?: number;
}

/**
 * Browser service for the mailbox routes in `contracts/MAILBOXES.md`. Mailboxes belong to an
 * organization, so every call names it in the `Organization-Id` header.
 */
export const mailboxService = {
  async domains(organizationId: number): Promise<MailboxDomain[]> {
    const parsed = mailboxDomainListSchema.safeParse(
      await request.get<unknown>('/mailbox-domains', {
        headers: organizationHeaders(organizationId),
      })
    );
    if (!parsed.success) throw invalidResponse();
    return parsed.data;
  },

  /** Domains the organization connected, with the records of their last check. */
  async mailDomains(organizationId: number): Promise<MailDomain[]> {
    const parsed = mailDomainListSchema.safeParse(
      await request.get<unknown>('/mail-domains', { headers: organizationHeaders(organizationId) })
    );
    if (!parsed.success) throw invalidResponse();
    return parsed.data;
  },

  async connectMailDomain(organizationId: number, input: MailDomainInput): Promise<MailDomain> {
    return parseMailDomain(
      await request.post<unknown, MailDomainInput>('/mail-domains', input, {
        headers: organizationHeaders(organizationId),
      })
    );
  },

  /** Runs the setup again (`setups`) or only re-reads DNS (`checks`). */
  async refreshMailDomain(
    organizationId: number,
    domainId: number,
    action: 'setups' | 'checks'
  ): Promise<MailDomain> {
    validId(domainId);
    return parseMailDomain(
      await request.post<unknown>(`/mail-domains/${domainId}/${action}`, undefined, {
        headers: organizationHeaders(organizationId),
      }),
      domainId
    );
  },

  async removeMailDomain(organizationId: number, domainId: number): Promise<void> {
    validId(domainId);
    await request.delete<void>(`/mail-domains/${domainId}`, {
      headers: organizationHeaders(organizationId),
    });
  },

  /** What connecting the domain would find; changes nothing. */
  async previewMailDomain(organizationId: number, name: string): Promise<MailDomainPreview> {
    const parsed = mailDomainPreviewSchema.safeParse(
      await request.post<unknown, MailDomainPreviewInput>(
        '/mail-domain-previews',
        { name },
        { headers: organizationHeaders(organizationId) }
      )
    );
    if (!parsed.success) throw invalidResponse();
    return parsed.data;
  },

  /** Turns a send-only domain into one that also receives; the setup runs in the background. */
  async enableReceiving(organizationId: number, domainId: number): Promise<MailDomain> {
    validId(domainId);
    return parseMailDomain(
      await request.post<unknown>(`/mail-domains/${domainId}/receiving`, undefined, {
        headers: organizationHeaders(organizationId),
      }),
      domainId
    );
  },

  async listMailZones(organizationId: number): Promise<MailZone[]> {
    const parsed = mailZoneListSchema.safeParse(
      await request.get<unknown>('/mail-zones', { headers: organizationHeaders(organizationId) })
    );
    if (!parsed.success) throw invalidResponse();
    return parsed.data;
  },

  async getMailZone(organizationId: number, zoneId: number): Promise<MailZone> {
    validId(zoneId);
    const parsed = mailZoneSchema.safeParse(
      await request.get<unknown>(`/mail-zones/${zoneId}`, {
        headers: organizationHeaders(organizationId),
      })
    );
    if (!parsed.success || parsed.data.id !== zoneId) throw invalidResponse();
    return parsed.data;
  },

  async deleteMailZone(organizationId: number, zoneId: number): Promise<void> {
    validId(zoneId);
    await request.delete<void>(`/mail-zones/${zoneId}`, {
      headers: organizationHeaders(organizationId),
    });
  },

  async listDnsRecords(organizationId: number, zoneId: number): Promise<MailDnsRecord[]> {
    validId(zoneId);
    const parsed = mailDnsRecordListSchema.safeParse(
      await request.get<unknown>(`/mail-zones/${zoneId}/dns-records`, {
        headers: organizationHeaders(organizationId),
      })
    );
    if (!parsed.success) throw invalidResponse();
    return parsed.data;
  },

  async createDnsRecord(
    organizationId: number,
    zoneId: number,
    input: MailDnsRecordInput
  ): Promise<MailDnsRecord> {
    validId(zoneId);
    return parseDnsRecord(
      await request.post<unknown, MailDnsRecordInput>(`/mail-zones/${zoneId}/dns-records`, input, {
        headers: organizationHeaders(organizationId),
      })
    );
  },

  async updateDnsRecord(
    organizationId: number,
    zoneId: number,
    recordId: string,
    input: MailDnsRecordPatch
  ): Promise<MailDnsRecord> {
    validId(zoneId);
    validRecordId(recordId);
    return parseDnsRecord(
      await request.patch<unknown, MailDnsRecordPatch>(
        `/mail-zones/${zoneId}/dns-records/${recordId}`,
        input,
        { headers: organizationHeaders(organizationId) }
      ),
      recordId
    );
  },

  async deleteDnsRecord(organizationId: number, zoneId: number, recordId: string): Promise<void> {
    validId(zoneId);
    validRecordId(recordId);
    await request.delete<void>(`/mail-zones/${zoneId}/dns-records/${recordId}`, {
      headers: organizationHeaders(organizationId),
    });
  },

  async list(organizationId: number): Promise<Mailbox[]> {
    const parsed = mailboxListSchema.safeParse(
      await request.get<unknown>('/mailboxes', { headers: organizationHeaders(organizationId) })
    );
    if (!parsed.success) throw invalidResponse();
    return parsed.data;
  },

  async create(organizationId: number, input: CreateMailboxInput): Promise<Mailbox> {
    const value = await request.post<unknown, CreateMailboxInput>('/mailboxes', input, {
      headers: organizationHeaders(organizationId),
    });
    const parsed = mailboxSchema.safeParse(value);
    if (!parsed.success) throw invalidResponse();
    return parsed.data;
  },

  async remove(organizationId: number, mailboxId: number): Promise<void> {
    validId(mailboxId);
    await request.delete<void>(`/mailboxes/${mailboxId}`, {
      headers: organizationHeaders(organizationId),
    });
  },

  /** Members of a shared mailbox, ordered by user id; needs `manage_members`. */
  async members(organizationId: number, mailboxId: number): Promise<MailboxMember[]> {
    const headers = organizationHeaders(organizationId);
    validId(mailboxId);
    const parsed = mailboxMemberListSchema.safeParse(
      await request.get<unknown>(`/mailboxes/${mailboxId}/members`, { headers })
    );
    if (!parsed.success) throw invalidResponse();
    return parsed.data;
  },

  /** Adds the user or changes their permission. */
  async setMember(
    organizationId: number,
    mailboxId: number,
    userId: number,
    permission: MailboxPermission
  ): Promise<MailboxMember> {
    const headers = organizationHeaders(organizationId);
    validId(mailboxId);
    validId(userId);
    const parsed = mailboxMemberSchema.safeParse(
      await request.put<unknown, { permission: MailboxPermission }>(
        `/mailboxes/${mailboxId}/members/${userId}`,
        { permission },
        { headers }
      )
    );
    if (!parsed.success || parsed.data.user_id !== userId) throw invalidResponse();
    return parsed.data;
  },

  async removeMember(organizationId: number, mailboxId: number, userId: number): Promise<void> {
    const headers = organizationHeaders(organizationId);
    validId(mailboxId);
    validId(userId);
    await request.delete<void>(`/mailboxes/${mailboxId}/members/${userId}`, { headers });
  },

  /** One page of the access history, newest first; needs `see_history`. */
  async accessEvents(
    organizationId: number,
    mailboxId: number,
    { page = 1, perPage = 20 }: { page?: number; perPage?: number } = {}
  ): Promise<MailboxAccessEventPage> {
    const headers = organizationHeaders(organizationId);
    validId(mailboxId);
    validId(page);
    if (
      !Number.isSafeInteger(perPage) ||
      perPage < 1 ||
      perPage > MAILBOX_ACCESS_EVENTS_MAX_PAGE_SIZE
    ) {
      throw invalidResponse();
    }
    const parsed = mailboxAccessEventPageEnvelopeSchema.safeParse(
      await request.getEnvelope<unknown>(`/mailboxes/${mailboxId}/access-events`, {
        headers,
        params: { page, per_page: perPage },
      })
    );
    if (!parsed.success) throw invalidResponse();
    return { items: parsed.data.data, meta: parsed.data.meta, links: parsed.data.links };
  },

  /** Turns a personal mailbox into a shared one the successor works; answers the mailbox. */
  async handOver(
    organizationId: number,
    mailboxId: number,
    successorUserId: number
  ): Promise<Mailbox> {
    const headers = organizationHeaders(organizationId);
    validId(mailboxId);
    validId(successorUserId);
    const parsed = mailboxSchema.safeParse(
      await request.post<unknown, { successor_user_id: number }>(
        `/mailboxes/${mailboxId}/handovers`,
        { successor_user_id: successorUserId },
        { headers }
      )
    );
    if (!parsed.success || parsed.data.id !== mailboxId) throw invalidResponse();
    return parsed.data;
  },

  /** Deletes the sealed history for good; `confirmAddress` must be the mailbox address. */
  async deleteSealedHistory(
    organizationId: number,
    mailboxId: number,
    confirmAddress: string
  ): Promise<MailboxSealedDeletion> {
    const headers = organizationHeaders(organizationId);
    validId(mailboxId);
    const parsed = mailboxSealedDeletionSchema.safeParse(
      await request.delete<unknown>(`/mailboxes/${mailboxId}/sealed-messages`, {
        headers,
        data: { confirm_address: confirmAddress },
      })
    );
    if (!parsed.success) throw invalidResponse();
    return parsed.data;
  },

  /**
   * The organization's mail roles in the API's order. Owners and admins get every role; members
   * only those holding a mailbox they can see, with only those mailboxes.
   */
  async listRoles(organizationId: number): Promise<MailRole[]> {
    const parsed = mailRoleListSchema.safeParse(
      await request.get<unknown>('/mail-roles', { headers: organizationHeaders(organizationId) })
    );
    if (!parsed.success) throw invalidResponse();
    return parsed.data;
  },

  /** Owners and admins only. */
  async createRole(organizationId: number, input: MailRoleInput): Promise<MailRole> {
    const headers = organizationHeaders(organizationId);
    return parseOrganizeItem(
      mailRoleSchema,
      await request.post<unknown, MailRoleInput>('/mail-roles', input, { headers })
    );
  },

  /** Replaces every field of the role; an omitted field takes its default. */
  async updateRole(
    organizationId: number,
    roleId: number,
    input: MailRoleInput
  ): Promise<MailRole> {
    const headers = organizationHeaders(organizationId);
    validId(roleId);
    return parseOrganizeItem(
      mailRoleSchema,
      await request.put<unknown, MailRoleInput>(`/mail-roles/${roleId}`, input, { headers }),
      roleId
    );
  },

  /** Its mailboxes become ungrouped; no access changes. */
  async deleteRole(organizationId: number, roleId: number): Promise<void> {
    const headers = organizationHeaders(organizationId);
    validId(roleId);
    await request.delete<void>(`/mail-roles/${roleId}`, { headers });
  },

  /** Puts the mailbox in a role, or takes it out with null; needs `administer`. */
  async assignRole(
    organizationId: number,
    mailboxId: number,
    roleId: number | null
  ): Promise<Mailbox> {
    const headers = organizationHeaders(organizationId);
    validId(mailboxId);
    if (roleId !== null) validId(roleId);
    const parsed = mailboxSchema.safeParse(
      await request.put<unknown, { role_id: number | null }>(
        `/mailboxes/${mailboxId}/role-assignment`,
        { role_id: roleId },
        { headers }
      )
    );
    if (!parsed.success || parsed.data.id !== mailboxId) throw invalidResponse();
    return parsed.data;
  },

  /** Turns organization webhooks for a shared mailbox on or off; answers the mailbox. */
  async setWebhooks(organizationId: number, mailboxId: number, enabled: boolean): Promise<Mailbox> {
    const headers = organizationHeaders(organizationId);
    validId(mailboxId);
    const parsed = mailboxSchema.safeParse(
      await request.put<unknown, { enabled: boolean }>(
        `/mailboxes/${mailboxId}/webhook-setting`,
        { enabled },
        { headers }
      )
    );
    if (!parsed.success || parsed.data.id !== mailboxId) throw invalidResponse();
    return parsed.data;
  },

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
    { view, roleId, mailboxId, unread, q, group, tz }: InboxFilters,
    cursor?: string,
    perPage?: number
  ): Promise<InboxPage> {
    const headers = organizationHeaders(organizationId);
    if (!(INBOX_VIEWS as readonly string[]).includes(view)) throw invalidResponse();
    if (roleId !== undefined && roleId !== 'none') validId(roleId);
    if (mailboxId !== undefined) validId(mailboxId);
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
        ...(query === undefined ? {} : { q: query }),
        ...(group ? { group } : {}),
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

  /** One page of broadcasts, newest first, with recipient counts. */
  async broadcasts(organizationId: number, page = 1, perPage = 20): Promise<MailBroadcastPage> {
    validId(page);
    if (!Number.isSafeInteger(perPage) || perPage < 1 || perPage > MAIL_PAGE_MAX_SIZE) {
      throw invalidResponse();
    }
    const value = await request.getEnvelope<unknown>('/mail-broadcasts', {
      headers: organizationHeaders(organizationId),
      params: { page, per_page: perPage },
    });
    const parsed = mailBroadcastPageEnvelopeSchema.safeParse(value);
    if (!parsed.success) throw invalidResponse();
    return { items: parsed.data.data, meta: parsed.data.meta, links: parsed.data.links };
  },

  async broadcast(organizationId: number, broadcastId: number): Promise<MailBroadcast> {
    validId(broadcastId);
    return parseBroadcast(
      await request.get<unknown>(`/mail-broadcasts/${broadcastId}`, {
        headers: organizationHeaders(organizationId),
      }),
      broadcastId
    );
  },

  /** Creates a draft, or replaces one when `broadcastId` is given. */
  async saveBroadcast(
    organizationId: number,
    input: MailBroadcastInput,
    broadcastId?: number
  ): Promise<MailBroadcast> {
    if (broadcastId !== undefined) validId(broadcastId);
    const config = { headers: organizationHeaders(organizationId) };
    const value =
      broadcastId === undefined
        ? await request.post<unknown, MailBroadcastInput>('/mail-broadcasts', input, config)
        : await request.put<unknown, MailBroadcastInput>(
            `/mail-broadcasts/${broadcastId}`,
            input,
            config
          );
    return parseBroadcast(value, broadcastId);
  },

  async deleteBroadcast(organizationId: number, broadcastId: number): Promise<void> {
    validId(broadcastId);
    await request.delete<void>(`/mail-broadcasts/${broadcastId}`, {
      headers: organizationHeaders(organizationId),
    });
  },

  /** Starts a draft now, or schedules it when `scheduled_at` is in the future. */
  async sendBroadcast(
    organizationId: number,
    broadcastId: number,
    input: MailBroadcastSendInput
  ): Promise<MailBroadcast> {
    validId(broadcastId);
    return parseBroadcast(
      await request.post<unknown, MailBroadcastSendInput>(
        `/mail-broadcasts/${broadcastId}/sends`,
        input,
        { headers: organizationHeaders(organizationId) }
      ),
      broadcastId
    );
  },

  async cancelBroadcast(
    organizationId: number,
    broadcastId: number
  ): Promise<MailBroadcast | MailBroadcastStatusOnly> {
    return this.controlBroadcast(organizationId, broadcastId, 'cancellations');
  },

  /**
   * Cancels, pauses, or resumes a broadcast; only owners and admins may. Pausing and cancelling
   * need no access to the sender mailbox; without `view` on it the API answers 204 and only the
   * new status is known.
   */
  async controlBroadcast(
    organizationId: number,
    broadcastId: number,
    action: 'cancellations' | 'pauses' | 'resumptions'
  ): Promise<MailBroadcast | MailBroadcastStatusOnly> {
    validId(broadcastId);
    const value = await request.post<unknown>(
      `/mail-broadcasts/${broadcastId}/${action}`,
      undefined,
      { headers: organizationHeaders(organizationId) }
    );
    // Without `view` on the sender mailbox, pausing and cancelling answer 204 with no body.
    if (action !== 'resumptions' && (value === '' || value === undefined || value === null)) {
      return mailBroadcastStatusOnlySchema.parse({
        id: broadcastId,
        status: action === 'pauses' ? 'paused' : 'canceled',
      });
    }
    return parseBroadcast(value, broadcastId);
  },

  async broadcastRecipients(
    organizationId: number,
    broadcastId: number,
    {
      status,
      page = 1,
      perPage = 25,
    }: { status?: MailRecipientStatus; page?: number; perPage?: number } = {}
  ): Promise<MailBroadcastRecipientPage> {
    validId(broadcastId);
    validId(page);
    const value = await request.getEnvelope<unknown>(`/mail-broadcasts/${broadcastId}/recipients`, {
      headers: organizationHeaders(organizationId),
      params: { ...(status ? { status } : {}), page, per_page: perPage },
    });
    const parsed = mailBroadcastRecipientPageEnvelopeSchema.safeParse(value);
    if (!parsed.success) throw invalidResponse();
    return { items: parsed.data.data, meta: parsed.data.meta, links: parsed.data.links };
  },

  /** One page of contacts, newest first. */
  async contacts(
    organizationId: number,
    {
      q,
      status,
      listId,
      page = 1,
      perPage = 25,
    }: MailContactFilters & {
      page?: number;
      perPage?: number;
    } = {}
  ): Promise<MailContactPage> {
    validId(page);
    if (listId !== undefined) validId(listId);
    if (!Number.isSafeInteger(perPage) || perPage < 1 || perPage > MAIL_PAGE_MAX_SIZE) {
      throw invalidResponse();
    }
    const query = q?.trim() ?? '';
    if (Array.from(query).length > MAIL_LOG_QUERY_MAX_LENGTH) throw invalidResponse();
    const value = await request.getEnvelope<unknown>('/mail-contacts', {
      headers: organizationHeaders(organizationId),
      params: {
        ...(query === '' ? {} : { q: query }),
        ...(status ? { status } : {}),
        ...(listId === undefined ? {} : { list_id: listId }),
        page,
        per_page: perPage,
      },
    });
    const parsed = mailContactPageEnvelopeSchema.safeParse(value);
    if (!parsed.success) throw invalidResponse();
    return { items: parsed.data.data, meta: parsed.data.meta, links: parsed.data.links };
  },

  /** Creates a contact, or replaces one when `contactId` is given. */
  async saveContact(
    organizationId: number,
    input: MailContactInput,
    contactId?: number
  ): Promise<MailContact> {
    if (contactId !== undefined) validId(contactId);
    const config = { headers: organizationHeaders(organizationId) };
    const value =
      contactId === undefined
        ? await request.post<unknown, MailContactInput>('/mail-contacts', input, config)
        : await request.put<unknown, MailContactInput>(
            `/mail-contacts/${contactId}`,
            input,
            config
          );
    const parsed = mailContactSchema.safeParse(value);
    if (!parsed.success) throw invalidResponse();
    return parsed.data;
  },

  async deleteContact(organizationId: number, contactId: number): Promise<void> {
    validId(contactId);
    await request.delete<void>(`/mail-contacts/${contactId}`, {
      headers: organizationHeaders(organizationId),
    });
  },

  async importContacts(
    organizationId: number,
    input: MailContactImportInput
  ): Promise<MailContactImport> {
    const parsed = mailContactImportSchema.safeParse(
      await request.post<unknown, MailContactImportInput>('/mail-contact-imports', input, {
        headers: organizationHeaders(organizationId),
        // A large file takes a while to store.
        timeout: 120_000,
      })
    );
    if (!parsed.success) throw invalidResponse();
    return parsed.data;
  },

  async contactLists(organizationId: number): Promise<MailContactList[]> {
    const parsed = mailContactListListSchema.safeParse(
      await request.get<unknown>('/mail-contact-lists', {
        headers: organizationHeaders(organizationId),
      })
    );
    if (!parsed.success) throw invalidResponse();
    return parsed.data;
  },

  /** Creates a list, or renames one when `listId` is given. */
  async saveContactList(
    organizationId: number,
    input: MailContactListInput,
    listId?: number
  ): Promise<MailContactList> {
    if (listId !== undefined) validId(listId);
    const config = { headers: organizationHeaders(organizationId) };
    const value =
      listId === undefined
        ? await request.post<unknown, MailContactListInput>('/mail-contact-lists', input, config)
        : await request.put<unknown, MailContactListInput>(
            `/mail-contact-lists/${listId}`,
            input,
            config
          );
    const parsed = mailContactListSchema.safeParse(value);
    if (!parsed.success) throw invalidResponse();
    return parsed.data;
  },

  async deleteContactList(organizationId: number, listId: number): Promise<void> {
    validId(listId);
    await request.delete<void>(`/mail-contact-lists/${listId}`, {
      headers: organizationHeaders(organizationId),
    });
  },

  /** The organization's templates by name; only those of `kind` when it is given. */
  async templates(organizationId: number, kind?: MailTemplateKind): Promise<MailTemplateSummary[]> {
    const parsed = mailTemplateListSchema.safeParse(
      await request.get<unknown>('/mail-templates', {
        headers: organizationHeaders(organizationId),
        ...(kind === undefined ? {} : { params: { kind } }),
      })
    );
    if (!parsed.success) throw invalidResponse();
    if (kind !== undefined && parsed.data.some(template => template.kind !== kind)) {
      throw invalidResponse();
    }
    return parsed.data;
  },

  async template(organizationId: number, templateId: number): Promise<MailTemplate> {
    validId(templateId);
    const parsed = mailTemplateSchema.safeParse(
      await request.get<unknown>(`/mail-templates/${templateId}`, {
        headers: organizationHeaders(organizationId),
      })
    );
    if (!parsed.success || parsed.data.id !== templateId) throw invalidResponse();
    return parsed.data;
  },

  /** Creates a template, or replaces one when `templateId` is given. */
  async saveTemplate(
    organizationId: number,
    input: MailTemplateInput,
    templateId?: number
  ): Promise<MailTemplate> {
    if (templateId !== undefined) validId(templateId);
    const config = { headers: organizationHeaders(organizationId) };
    const value =
      templateId === undefined
        ? await request.post<unknown, MailTemplateInput>('/mail-templates', input, config)
        : await request.put<unknown, MailTemplateInput>(
            `/mail-templates/${templateId}`,
            input,
            config
          );
    const parsed = mailTemplateSchema.safeParse(value);
    if (!parsed.success) throw invalidResponse();
    return parsed.data;
  },

  async deleteTemplate(organizationId: number, templateId: number): Promise<void> {
    validId(templateId);
    await request.delete<void>(`/mail-templates/${templateId}`, {
      headers: organizationHeaders(organizationId),
    });
  },

  /** Sends a template to test addresses; sample values fill what `variables` does not. */
  async sendTemplateTest(
    organizationId: number,
    templateId: number,
    input: MailTemplateTestInput
  ): Promise<MailMessage> {
    validId(templateId);
    const value = await request.post<unknown, MailTemplateTestInput>(
      `/mail-templates/${templateId}/tests`,
      input,
      { headers: organizationHeaders(organizationId) }
    );
    return parseMailMessage(value);
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
    return mailboxService.updateMessage(organizationId, mailboxId, messageId, { read });
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

export function parseMailMessage(value: unknown, expectedId?: number): MailMessage {
  const parsed = mailMessageSchema.safeParse(value);
  if (!parsed.success || (expectedId !== undefined && parsed.data.id !== expectedId)) {
    throw invalidResponse();
  }
  return parsed.data;
}

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

function parseOrganizeItem<T extends { id: number }>(
  schema: { safeParse: (value: unknown) => { success: true; data: T } | { success: false } },
  value: unknown,
  expectedId?: number
): T {
  const parsed = schema.safeParse(value);
  if (!parsed.success || (expectedId !== undefined && parsed.data.id !== expectedId)) {
    throw invalidResponse();
  }
  return parsed.data;
}

function parseBroadcast(value: unknown, expectedId?: number): MailBroadcast {
  const parsed = mailBroadcastSchema.safeParse(value);
  if (!parsed.success || (expectedId !== undefined && parsed.data.id !== expectedId)) {
    throw invalidResponse();
  }
  return parsed.data;
}

function parseMailDomain(value: unknown, expectedId?: number): MailDomain {
  const parsed = mailDomainSchema.safeParse(value);
  if (!parsed.success || (expectedId !== undefined && parsed.data.id !== expectedId)) {
    throw invalidResponse();
  }
  return parsed.data;
}

function parseDnsRecord(value: unknown, expectedId?: string): MailDnsRecord {
  const parsed = mailDnsRecordSchema.safeParse(value);
  if (!parsed.success || (expectedId !== undefined && parsed.data.id !== expectedId)) {
    throw invalidResponse();
  }
  return parsed.data;
}

/** A Cloudflare record ID; anything else never leaves the browser. */
function validRecordId(value: string): void {
  if (!MAIL_DNS_RECORD_ID_PATTERN.test(value)) throw invalidResponse();
}

/** The organization the request acts in; an invalid id never leaves the browser. */
function organizationHeaders(organizationId: number): Record<string, string> {
  validId(organizationId);
  return { 'Organization-Id': String(organizationId) };
}

function validId(value: number): void {
  if (!Number.isSafeInteger(value) || value < 1) throw invalidResponse();
}

function invalidResponse(): ApiError {
  return new ApiError(
    'Mailbox service returned an invalid response',
    ClientErrorCode.INVALID_RESPONSE
  );
}

assertContract<
  Accepts<z.input<typeof mailboxDomainListSchema>, ContractData<'listMailboxDomains', 200>>
>();
assertContract<Accepts<z.input<typeof mailboxListSchema>, ContractData<'listMailboxes', 200>>>();
assertContract<
  Accepts<z.input<typeof mailDomainListSchema>, ContractData<'listMailDomains', 200>>
>();
assertContract<Accepts<z.input<typeof mailDomainSchema>, ContractData<'connectMailDomain', 201>>>();
assertContract<Accepts<z.input<typeof mailDomainSchema>, ContractData<'setUpMailDomain', 200>>>();
assertContract<Accepts<z.input<typeof mailDomainSchema>, ContractData<'checkMailDomain', 200>>>();
assertContract<Sends<MailDomainInput, ContractRequest<'connectMailDomain'>>>();
assertContract<
  Accepts<z.input<typeof mailDomainSchema>, ContractData<'enableMailDomainReceiving', 200>>
>();
assertContract<
  Accepts<z.input<typeof mailDomainPreviewSchema>, ContractData<'previewMailDomain', 200>>
>();
assertContract<Sends<MailDomainPreviewInput, ContractRequest<'previewMailDomain'>>>();
assertContract<Accepts<z.input<typeof mailZoneListSchema>, ContractData<'listMailZones', 200>>>();
assertContract<Accepts<z.input<typeof mailZoneSchema>, ContractData<'getMailZone', 200>>>();
assertContract<
  Accepts<z.input<typeof mailDnsRecordListSchema>, ContractData<'listMailDNSRecords', 200>>
>();
assertContract<
  Accepts<z.input<typeof mailDnsRecordSchema>, ContractData<'createMailDNSRecord', 201>>
>();
assertContract<
  Accepts<z.input<typeof mailDnsRecordSchema>, ContractData<'updateMailDNSRecord', 200>>
>();
assertContract<Sends<MailDnsRecordInput, ContractRequest<'createMailDNSRecord'>>>();
assertContract<Sends<MailDnsRecordPatch, ContractRequest<'updateMailDNSRecord'>>>();
assertContract<Accepts<z.input<typeof mailboxSchema>, ContractData<'createMailbox', 201>>>();
assertContract<Sends<CreateMailboxInput, ContractRequest<'createMailbox'>>>();
assertContract<
  Accepts<z.input<typeof mailboxMemberListSchema>, ContractData<'listMailboxMembers', 200>>
>();
assertContract<
  Accepts<z.input<typeof mailboxMemberSchema>, ContractData<'setMailboxMember', 200>>
>();
assertContract<Sends<{ permission: MailboxPermission }, ContractRequest<'setMailboxMember'>>>();
assertContract<
  Accepts<
    z.input<typeof mailboxAccessEventPageEnvelopeSchema>,
    ContractResponse<'listMailboxAccessEvents', 200>
  >
>();
assertContract<Accepts<z.input<typeof mailboxSchema>, ContractData<'handOverMailbox', 201>>>();
assertContract<Sends<{ successor_user_id: number }, ContractRequest<'handOverMailbox'>>>();
assertContract<
  Accepts<
    z.input<typeof mailboxSealedDeletionSchema>,
    ContractData<'deleteMailboxSealedMessages', 200>
  >
>();
assertContract<
  Sends<{ confirm_address: string }, ContractRequest<'deleteMailboxSealedMessages'>>
>();
assertContract<
  Accepts<z.input<typeof mailboxSchema>, ContractData<'setMailboxWebhookSetting', 200>>
>();
assertContract<Sends<{ enabled: boolean }, ContractRequest<'setMailboxWebhookSetting'>>>();
assertContract<
  Accepts<z.input<typeof mailMessagePageEnvelopeSchema>, ContractResponse<'listMailMessages', 200>>
>();
assertContract<Accepts<z.input<typeof mailMessageSchema>, ContractData<'getMailMessage', 200>>>();
assertContract<Accepts<z.input<typeof mailMessageSchema>, ContractData<'revealMailMessage', 200>>>();
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
  Accepts<z.input<typeof mailTemplateListSchema>, ContractData<'listMailTemplates', 200>>
>();
assertContract<Accepts<z.input<typeof mailTemplateSchema>, ContractData<'getMailTemplate', 200>>>();
assertContract<
  Accepts<z.input<typeof mailTemplateSchema>, ContractData<'createMailTemplate', 201>>
>();
assertContract<Sends<MailTemplateInput, ContractRequest<'createMailTemplate'>>>();
assertContract<Sends<MailTemplateInput, ContractRequest<'updateMailTemplate'>>>();
assertContract<Sends<MailTemplateTestInput, ContractRequest<'sendMailTemplateTest'>>>();
assertContract<
  Accepts<z.input<typeof mailContactPageEnvelopeSchema>, ContractResponse<'listMailContacts', 200>>
>();
assertContract<
  Accepts<z.input<typeof mailContactSchema>, ContractData<'createMailContact', 201>>
>();
assertContract<
  Accepts<z.input<typeof mailContactSchema>, ContractData<'updateMailContact', 200>>
>();
assertContract<Sends<MailContactInput, ContractRequest<'createMailContact'>>>();
assertContract<Sends<MailContactInput, ContractRequest<'updateMailContact'>>>();
assertContract<
  Accepts<z.input<typeof mailContactImportSchema>, ContractData<'importMailContacts', 200>>
>();
assertContract<Sends<MailContactImportInput, ContractRequest<'importMailContacts'>>>();
assertContract<
  Accepts<z.input<typeof mailContactListListSchema>, ContractData<'listMailContactLists', 200>>
>();
assertContract<
  Accepts<z.input<typeof mailContactListSchema>, ContractData<'createMailContactList', 201>>
>();
assertContract<Sends<MailContactListInput, ContractRequest<'createMailContactList'>>>();
assertContract<
  Accepts<
    z.input<typeof mailBroadcastPageEnvelopeSchema>,
    ContractResponse<'listMailBroadcasts', 200>
  >
>();
assertContract<
  Accepts<z.input<typeof mailBroadcastSchema>, ContractData<'getMailBroadcast', 200>>
>();
assertContract<
  Accepts<z.input<typeof mailBroadcastSchema>, ContractData<'createMailBroadcast', 201>>
>();
assertContract<Sends<MailBroadcastInput, ContractRequest<'createMailBroadcast'>>>();
assertContract<Sends<MailBroadcastInput, ContractRequest<'updateMailBroadcast'>>>();
assertContract<Sends<MailBroadcastSendInput, ContractRequest<'sendMailBroadcast'>>>();
assertContract<
  Accepts<z.input<typeof mailBroadcastSchema>, ContractData<'sendMailBroadcast', 200>>
>();
assertContract<
  Accepts<z.input<typeof mailBroadcastSchema>, ContractData<'pauseMailBroadcast', 200>>
>();
assertContract<
  Accepts<z.input<typeof mailBroadcastSchema>, ContractData<'cancelMailBroadcast', 200>>
>();
assertContract<
  Accepts<z.input<typeof mailBroadcastSchema>, ContractData<'resumeMailBroadcast', 200>>
>();
assertContract<
  Accepts<
    z.input<typeof mailBroadcastRecipientPageEnvelopeSchema>,
    ContractResponse<'listMailBroadcastRecipients', 200>
  >
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
assertContract<Accepts<z.input<typeof mailRoleListSchema>, ContractData<'listMailRoles', 200>>>();
assertContract<Accepts<z.input<typeof mailRoleSchema>, ContractData<'createMailRole', 201>>>();
assertContract<Accepts<z.input<typeof mailRoleSchema>, ContractData<'updateMailRole', 200>>>();
assertContract<Sends<MailRoleInput, ContractRequest<'createMailRole'>>>();
assertContract<Sends<MailRoleInput, ContractRequest<'updateMailRole'>>>();
assertContract<
  Accepts<z.input<typeof mailboxSchema>, ContractData<'setMailboxRoleAssignment', 200>>
>();
assertContract<Sends<{ role_id: number | null }, ContractRequest<'setMailboxRoleAssignment'>>>();
assertContract<Accepts<z.input<typeof mailLabelSchema>, ContractData<'createMailLabel', 201>>>();
assertContract<Accepts<z.input<typeof mailLabelSchema>, ContractData<'updateMailLabel', 200>>>();
assertContract<Sends<MailLabelInput, ContractRequest<'createMailLabel'>>>();
assertContract<Sends<MailLabelInput, ContractRequest<'updateMailLabel'>>>();
