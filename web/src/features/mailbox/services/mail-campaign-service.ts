import type { z } from 'zod/mini';

import {
  MAIL_LOG_QUERY_MAX_LENGTH,
  MAIL_PAGE_MAX_SIZE,
  mailBroadcastPageEnvelopeSchema,
  mailBroadcastRecipientPageEnvelopeSchema,
  mailBroadcastSchema,
  mailBroadcastStatusOnlySchema,
  mailContactImportSchema,
  mailContactListListSchema,
  mailContactListSchema,
  mailContactPageEnvelopeSchema,
  mailContactSchema,
  mailTemplateListSchema,
  mailTemplateSchema,
} from '@/features/mailbox/schemas';
import type {
  MailBroadcast,
  MailBroadcastInput,
  MailBroadcastPage,
  MailBroadcastRecipientPage,
  MailBroadcastSendInput,
  MailBroadcastStatusOnly,
  MailContact,
  MailContactFilters,
  MailContactImport,
  MailContactImportInput,
  MailContactInput,
  MailContactList,
  MailContactListInput,
  MailContactPage,
  MailMessage,
  MailRecipientStatus,
  MailTemplate,
  MailTemplateInput,
  MailTemplateKind,
  MailTemplateSummary,
  MailTemplateTestInput,
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
  validId,
} from './mailbox-service-shared';

/** Broadcasts, contacts, contact lists, and templates. */
export const mailCampaignService = {
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
};

function parseBroadcast(value: unknown, expectedId?: number): MailBroadcast {
  const parsed = mailBroadcastSchema.safeParse(value);
  if (!parsed.success || (expectedId !== undefined && parsed.data.id !== expectedId)) {
    throw invalidResponse();
  }
  return parsed.data;
}

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
