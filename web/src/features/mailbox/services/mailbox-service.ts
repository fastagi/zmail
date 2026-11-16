import type { z } from 'zod/mini';

import {
  MAIL_PAGE_MAX_SIZE,
  mailMessagePageEnvelopeSchema,
  mailMessageSchema,
  mailboxDomainListSchema,
  mailboxListSchema,
  mailboxSchema,
} from '@/features/mailbox/schemas';
import type {
  CreateMailboxInput,
  MailDirection,
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

/**
 * Browser service for the mailbox routes in `contracts/MAILBOXES.md`. Mailboxes belong to the
 * signed-in user, so no call carries an organization.
 */
export const mailboxService = {
  async domains(): Promise<MailboxDomain[]> {
    const parsed = mailboxDomainListSchema.safeParse(
      await request.get<unknown>('/mailbox-domains')
    );
    if (!parsed.success) throw invalidResponse();
    return parsed.data;
  },

  async list(): Promise<Mailbox[]> {
    const parsed = mailboxListSchema.safeParse(await request.get<unknown>('/mailboxes'));
    if (!parsed.success) throw invalidResponse();
    return parsed.data;
  },

  async create(input: CreateMailboxInput): Promise<Mailbox> {
    const value = await request.post<unknown, CreateMailboxInput>('/mailboxes', input);
    const parsed = mailboxSchema.safeParse(value);
    if (!parsed.success) throw invalidResponse();
    return parsed.data;
  },

  async remove(mailboxId: number): Promise<void> {
    validId(mailboxId);
    await request.delete<void>(`/mailboxes/${mailboxId}`);
  },

  async messages(
    mailboxId: number,
    { direction, page = 1, perPage = 20 }: ListMessagesOptions
  ): Promise<MailMessagePage> {
    validId(mailboxId);
    validId(page);
    if (!Number.isSafeInteger(perPage) || perPage < 1 || perPage > MAIL_PAGE_MAX_SIZE) {
      throw invalidResponse();
    }
    const value = await request.getEnvelope<unknown>(`/mailboxes/${mailboxId}/messages`, {
      params: { direction, page, per_page: perPage },
    });
    const parsed = mailMessagePageEnvelopeSchema.safeParse(value);
    if (!parsed.success) throw invalidResponse();
    return { items: parsed.data.data, meta: parsed.data.meta, links: parsed.data.links };
  },

  async message(mailboxId: number, messageId: number): Promise<MailMessage> {
    validId(mailboxId);
    validId(messageId);
    const value = await request.get<unknown>(`/mailboxes/${mailboxId}/messages/${messageId}`);
    return parseMailMessage(value, messageId);
  },

  /**
   * Resolves only when the provider accepted the message. A rejected send throws
   * `MAILBOX.DELIVERY_FAILED`; the message then stays in the sent folder as `failed`.
   */
  async send(mailboxId: number, input: SendMailMessageInput): Promise<MailMessage> {
    validId(mailboxId);
    const value = await request.post<unknown, SendMailMessageInput>(
      `/mailboxes/${mailboxId}/messages`,
      input
    );
    return parseMailMessage(value);
  },

  async setRead(mailboxId: number, messageId: number, read: boolean): Promise<MailMessage> {
    validId(mailboxId);
    validId(messageId);
    const value = await request.put<unknown, UpdateMailMessageInput>(
      `/mailboxes/${mailboxId}/messages/${messageId}`,
      { read }
    );
    return parseMailMessage(value, messageId);
  },

  async removeMessage(mailboxId: number, messageId: number): Promise<void> {
    validId(mailboxId);
    validId(messageId);
    await request.delete<void>(`/mailboxes/${mailboxId}/messages/${messageId}`);
  },
};

export function parseMailMessage(value: unknown, expectedId?: number): MailMessage {
  const parsed = mailMessageSchema.safeParse(value);
  if (!parsed.success || (expectedId !== undefined && parsed.data.id !== expectedId)) {
    throw invalidResponse();
  }
  return parsed.data;
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
