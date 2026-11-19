import type { z } from 'zod/mini';

import {
  MAILBOX_ACCESS_EVENTS_MAX_PAGE_SIZE,
  MAIL_LOG_QUERY_MAX_LENGTH,
  MAIL_PAGE_MAX_SIZE,
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
} from '@/features/mailbox/schemas';
import type {
  CreateMailboxInput,
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
  MailTemplateSummary,
  MailTemplateTestInput,
  MailDayStats,
  MailDirection,
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

interface ListMessagesOptions {
  direction: MailDirection;
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

  async messages(
    organizationId: number,
    mailboxId: number,
    { direction, page = 1, perPage = 20 }: ListMessagesOptions
  ): Promise<MailMessagePage> {
    validId(mailboxId);
    validId(page);
    if (!Number.isSafeInteger(perPage) || perPage < 1 || perPage > MAIL_PAGE_MAX_SIZE) {
      throw invalidResponse();
    }
    const value = await request.getEnvelope<unknown>(`/mailboxes/${mailboxId}/messages`, {
      headers: organizationHeaders(organizationId),
      params: { direction, page, per_page: perPage },
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

  async templates(organizationId: number): Promise<MailTemplateSummary[]> {
    const parsed = mailTemplateListSchema.safeParse(
      await request.get<unknown>('/mail-templates', {
        headers: organizationHeaders(organizationId),
      })
    );
    if (!parsed.success) throw invalidResponse();
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
    validId(mailboxId);
    validId(messageId);
    const value = await request.put<unknown, UpdateMailMessageInput>(
      `/mailboxes/${mailboxId}/messages/${messageId}`,
      { read },
      { headers: organizationHeaders(organizationId) }
    );
    return parseMailMessage(value, messageId);
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

/** The organization the request acts in; an invalid id never leaves the browser. */
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
