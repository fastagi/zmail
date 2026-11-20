import type { z } from 'zod/mini';

import {
  MAIL_SUPPRESSION_CURSOR_PATTERN,
  MAIL_SUPPRESSION_EMAIL_MAX_LENGTH,
  MAIL_SUPPRESSION_MAX_PAGE_SIZE,
  MAIL_SUPPRESSION_REASONS,
  mailSendLaneUsageResponseSchema,
  mailSuppressionPageSchema,
  mailSuppressionSchema,
  TRANSACTIONAL_LOG_CURSOR_PATTERN,
  TRANSACTIONAL_LOG_MAX_PAGE_SIZE,
  TRANSACTIONAL_LOG_RECIPIENT_MAX_LENGTH,
  TRANSACTIONAL_TEMPLATE_KEY_PATTERN,
  transactionalLogEntryDetailSchema,
  transactionalLogEntrySchema,
  transactionalLogPageSchema,
  transactionalMessageSchema,
} from '@/features/developer/schemas';
import type {
  MailSendLaneUsageList,
  MailSuppressionFilters,
  MailSuppressionPage,
  TransactionalLogEntryDetail,
  TransactionalLogFilters,
  TransactionalLogPage,
  TransactionalMessage,
  TransactionalTestSendInput,
} from '@/features/developer/types';
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

interface PageOptions {
  /** `meta.next_cursor` of the previous page; absent for the newest page. */
  cursor?: string;
  /** Without it the API's default (50) applies. */
  perPage?: number;
}

interface LogPageOptions {
  /** `meta.next_cursor` of the previous page; absent for the newest page. */
  cursor?: string;
  /** Without it the API's default (50) applies. */
  perPage?: number;
}

/**
 * Transactional mail as the developer console reads it: the send log, console test sends, lane
 * usage, and the suppression list. The API key route `POST /v1/transactional-messages` is for
 * backends and has no browser service.
 */
export const developerService = {
  /** One keyset page of the send log, newest first, with the mode's 24-hour count. */
  async log(
    organizationId: number,
    filters: TransactionalLogFilters,
    { cursor, perPage }: LogPageOptions = {}
  ): Promise<TransactionalLogPage> {
    const headers = organizationHeaders(organizationId);
    const to = filters.to?.trim().toLowerCase();
    if (to !== undefined && to.length > TRANSACTIONAL_LOG_RECIPIENT_MAX_LENGTH) {
      throw invalidResponse();
    }
    if (
      filters.template !== undefined &&
      !TRANSACTIONAL_TEMPLATE_KEY_PATTERN.test(filters.template)
    ) {
      throw invalidResponse();
    }
    if (cursor !== undefined && !TRANSACTIONAL_LOG_CURSOR_PATTERN.test(cursor)) {
      throw invalidResponse();
    }
    if (
      perPage !== undefined &&
      (!Number.isSafeInteger(perPage) || perPage < 1 || perPage > TRANSACTIONAL_LOG_MAX_PAGE_SIZE)
    ) {
      throw invalidResponse();
    }
    const value = await request.getEnvelope<unknown>('/transactional-mail-log', {
      headers,
      params: {
        mode: filters.mode,
        ...(filters.status === undefined ? {} : { status: filters.status }),
        ...(filters.lastEvent === undefined ? {} : { last_event: filters.lastEvent }),
        ...(filters.outcome === undefined ? {} : { outcome: filters.outcome }),
        ...(filters.template === undefined ? {} : { template: filters.template }),
        ...(to === undefined || to === '' ? {} : { to }),
        ...(filters.since === undefined ? {} : { since: filters.since }),
        ...(filters.until === undefined ? {} : { until: filters.until }),
        ...(cursor === undefined ? {} : { cursor }),
        ...(perPage === undefined ? {} : { per_page: perPage }),
      },
    });
    const parsed = transactionalLogPageSchema.safeParse(value);
    if (!parsed.success) throw invalidResponse();
    return { items: parsed.data.data, meta: parsed.data.meta };
  },

  /** One entry of either mode with its sender, masked variables, and events. */
  async logEntry(organizationId: number, messageId: number): Promise<TransactionalLogEntryDetail> {
    const headers = organizationHeaders(organizationId);
    validId(messageId);
    const parsed = transactionalLogEntryDetailSchema.safeParse(
      await request.get<unknown>(`/transactional-mail-log/${messageId}`, { headers })
    );
    if (!parsed.success) throw invalidResponse();
    return parsed.data;
  },

  /** Sends a template to the caller's own account address; `test` is simulated as delivered. */
  async testSend(
    organizationId: number,
    input: TransactionalTestSendInput
  ): Promise<TransactionalMessage> {
    const headers = organizationHeaders(organizationId);
    const body: TransactionalTestSendInput = {
      template: input.template,
      mode: input.mode,
      ...(input.variables === undefined ? {} : { variables: input.variables }),
    };
    const parsed = transactionalMessageSchema.safeParse(
      await request.post<unknown, TransactionalTestSendInput>('/transactional-test-sends', body, {
        headers,
      })
    );
    if (!parsed.success) throw invalidResponse();
    return parsed.data;
  },

  /** How full each send lane is; owners and admins only, members get `403 PERMISSION.DENIED`. */
  async sendLanes(organizationId: number): Promise<MailSendLaneUsageList> {
    const headers = organizationHeaders(organizationId);
    const parsed = mailSendLaneUsageResponseSchema.safeParse(
      await request.getEnvelope<unknown>('/mail-send-lanes', { headers })
    );
    if (!parsed.success) throw invalidResponse();
    return parsed.data.data;
  },

  /** One keyset page of the suppression list, newest first; owners and admins only. */
  async suppressions(
    organizationId: number,
    filters: MailSuppressionFilters = {},
    { cursor, perPage }: PageOptions = {}
  ): Promise<MailSuppressionPage> {
    const headers = organizationHeaders(organizationId);
    const email = filters.email?.trim().toLowerCase();
    if (email !== undefined && email.length > MAIL_SUPPRESSION_EMAIL_MAX_LENGTH) {
      throw invalidResponse();
    }
    if (
      filters.reason !== undefined &&
      !(MAIL_SUPPRESSION_REASONS as readonly string[]).includes(filters.reason)
    ) {
      throw invalidResponse();
    }
    if (cursor !== undefined && !MAIL_SUPPRESSION_CURSOR_PATTERN.test(cursor)) {
      throw invalidResponse();
    }
    if (
      perPage !== undefined &&
      (!Number.isSafeInteger(perPage) || perPage < 1 || perPage > MAIL_SUPPRESSION_MAX_PAGE_SIZE)
    ) {
      throw invalidResponse();
    }
    const parsed = mailSuppressionPageSchema.safeParse(
      await request.getEnvelope<unknown>('/mail-suppressions', {
        headers,
        params: {
          ...(email === undefined || email === '' ? {} : { email }),
          ...(filters.reason === undefined ? {} : { reason: filters.reason }),
          ...(cursor === undefined ? {} : { cursor }),
          ...(perPage === undefined ? {} : { per_page: perPage }),
        },
      })
    );
    if (!parsed.success) throw invalidResponse();
    return { items: parsed.data.data, meta: parsed.data.meta };
  },

  /** Takes an entry off the suppression list; the API answers `204` even when it is gone. */
  async removeSuppression(organizationId: number, suppressionId: number): Promise<void> {
    const headers = organizationHeaders(organizationId);
    validId(suppressionId);
    await request.delete<void>(`/mail-suppressions/${suppressionId}`, { headers });
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
    'Developer service returned an invalid response',
    ClientErrorCode.INVALID_RESPONSE
  );
}

assertContract<
  Accepts<
    z.input<typeof transactionalLogPageSchema>,
    ContractResponse<'listTransactionalMailLog', 200>
  >
>();
assertContract<
  Accepts<
    z.input<typeof transactionalLogEntrySchema>,
    ContractData<'listTransactionalMailLog', 200>[number]
  >
>();
assertContract<
  Accepts<
    z.input<typeof transactionalLogEntryDetailSchema>,
    ContractData<'getTransactionalMailLogEntry', 200>
  >
>();
assertContract<
  Accepts<z.input<typeof transactionalMessageSchema>, ContractData<'sendTransactionalTest', 202>>
>();
assertContract<
  Accepts<z.input<typeof transactionalMessageSchema>, ContractData<'getTransactionalMessage', 200>>
>();
assertContract<Sends<TransactionalTestSendInput, ContractRequest<'sendTransactionalTest'>>>();
assertContract<
  Accepts<
    z.input<typeof mailSendLaneUsageResponseSchema>,
    ContractResponse<'listMailSendLanes', 200>
  >
>();
assertContract<
  Accepts<z.input<typeof mailSuppressionPageSchema>, ContractResponse<'listMailSuppressions', 200>>
>();
assertContract<
  Accepts<z.input<typeof mailSuppressionSchema>, ContractData<'listMailSuppressions', 200>[number]>
>();
