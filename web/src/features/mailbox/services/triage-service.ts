import type { z } from 'zod/mini';

import { mailboxSchema, mailTriageSchema } from '@/features/mailbox/schemas';
import {
  mailReplyDraftSchema,
  mailTriageSettingSchema,
  MAIL_TRIAGE_OVERRIDE_CATEGORIES,
  type MailReplyDraft,
  type MailTriageOverrideInput,
  type MailTriageSetting,
  type MailTriageSettingInput,
} from '@/features/mailbox/triage-schemas';
import type { Mailbox, MailTriage } from '@/features/mailbox/types';
import { ClientErrorCode } from '@/http/codes';
import {
  assertContract,
  type Accepts,
  type ContractData,
  type ContractRequest,
  type Sends,
} from '@/http/contract';
import request, { ApiError } from '@/http/request';

/**
 * Browser service for AI triage in `contracts/MAILBOXES.md` (section AI Triage). Every call names
 * the organization in the `Organization-Id` header; the API decides who may do what.
 */
export const triageService = {
  /** The organization's switches; any member reads them. */
  async setting(organizationId: number): Promise<MailTriageSetting> {
    const parsed = mailTriageSettingSchema.safeParse(
      await request.get<unknown>('/mail-triage-setting', {
        headers: organizationHeaders(organizationId),
      })
    );
    if (!parsed.success) throw invalidResponse();
    return parsed.data;
  },

  /** Replaces the organization's switches; owners and admins only. */
  async saveSetting(
    organizationId: number,
    input: MailTriageSettingInput
  ): Promise<MailTriageSetting> {
    const parsed = mailTriageSettingSchema.safeParse(
      await request.put<unknown, MailTriageSettingInput>('/mail-triage-setting', input, {
        headers: organizationHeaders(organizationId),
      })
    );
    if (!parsed.success) throw invalidResponse();
    return parsed.data;
  },

  /** Turns a mailbox's triage on or off and answers the mailbox. */
  async setMailboxTriage(
    organizationId: number,
    mailboxId: number,
    enabled: boolean
  ): Promise<Mailbox> {
    validId(mailboxId);
    const parsed = mailboxSchema.safeParse(
      await request.put<unknown, { enabled: boolean }>(
        `/mailboxes/${mailboxId}/triage-setting`,
        { enabled },
        { headers: organizationHeaders(organizationId) }
      )
    );
    if (!parsed.success || parsed.data.id !== mailboxId) throw invalidResponse();
    return parsed.data;
  },

  /** Records the reader's own category for an inbound message; the model never overwrites it. */
  async overrideTriage(
    organizationId: number,
    mailboxId: number,
    messageId: number,
    input: MailTriageOverrideInput
  ): Promise<MailTriage> {
    validId(mailboxId);
    validId(messageId);
    if (!(MAIL_TRIAGE_OVERRIDE_CATEGORIES as readonly string[]).includes(input.category)) {
      throw invalidResponse();
    }
    const parsed = mailTriageSchema.safeParse(
      await request.put<unknown, MailTriageOverrideInput>(
        `/mailboxes/${mailboxId}/messages/${messageId}/triage`,
        input,
        { headers: organizationHeaders(organizationId) }
      )
    );
    if (!parsed.success) throw invalidResponse();
    return parsed.data;
  },

  /** A reply draft for the reader to edit; it is never stored or sent. */
  async replyDraft(
    organizationId: number,
    mailboxId: number,
    messageId: number
  ): Promise<MailReplyDraft> {
    validId(mailboxId);
    validId(messageId);
    const parsed = mailReplyDraftSchema.safeParse(
      await request.post<unknown>(
        `/mailboxes/${mailboxId}/messages/${messageId}/reply-drafts`,
        undefined,
        { headers: organizationHeaders(organizationId) }
      )
    );
    if (!parsed.success) throw invalidResponse();
    return parsed.data;
  },
};

function organizationHeaders(organizationId: number): Record<string, string> {
  validId(organizationId);
  return { 'Organization-Id': String(organizationId) };
}

function validId(value: number): void {
  if (!Number.isSafeInteger(value) || value < 1) throw invalidResponse();
}

function invalidResponse(): ApiError {
  return new ApiError(
    'Mail triage service returned an invalid response',
    ClientErrorCode.INVALID_RESPONSE
  );
}

assertContract<
  Accepts<z.input<typeof mailTriageSettingSchema>, ContractData<'getMailTriageSetting', 200>>
>();
assertContract<
  Accepts<z.input<typeof mailTriageSettingSchema>, ContractData<'updateMailTriageSetting', 200>>
>();
assertContract<Sends<MailTriageSettingInput, ContractRequest<'updateMailTriageSetting'>>>();
assertContract<
  Accepts<z.input<typeof mailboxSchema>, ContractData<'setMailboxTriageSetting', 200>>
>();
assertContract<Sends<{ enabled: boolean }, ContractRequest<'setMailboxTriageSetting'>>>();
assertContract<
  Accepts<
    z.input<typeof mailTriageSchema>,
    NonNullable<ContractData<'updateMailMessageTriage', 200>>
  >
>();
assertContract<Sends<MailTriageOverrideInput, ContractRequest<'updateMailMessageTriage'>>>();
assertContract<
  Accepts<z.input<typeof mailReplyDraftSchema>, ContractData<'createMailReplyDraft', 200>>
>();
