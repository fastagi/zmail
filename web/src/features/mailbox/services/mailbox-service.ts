import type { z } from 'zod/mini';

import {
  MAIL_LOG_QUERY_MAX_LENGTH,
  MAIL_PAGE_MAX_SIZE,
  MAIL_STATS_DEFAULT_DAYS,
  MAIL_STATS_MAX_DAYS,
  mailDayStatsListSchema,
  mailLogPageEnvelopeSchema,
  mailMessagePageEnvelopeSchema,
  mailMessageSchema,
  mailboxDomainListSchema,
  mailboxListSchema,
  mailboxSchema,
} from '@/features/mailbox/schemas';
import type {
  CreateMailboxInput,
  MailDayStats,
  MailDirection,
  MailLogFilters,
  MailLogPage,
  MailMessage,
  MailMessagePage,
  Mailbox,
  MailboxDomain,
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
assertContract<Accepts<z.input<typeof mailboxSchema>, ContractData<'createMailbox', 201>>>();
assertContract<Sends<CreateMailboxInput, ContractRequest<'createMailbox'>>>();
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
