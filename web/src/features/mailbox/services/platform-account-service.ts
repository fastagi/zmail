import type { z } from 'zod/mini';

import {
  PLATFORM_ACCOUNT_PAGE_MAX_SIZE,
  PLATFORM_ACCOUNT_QUERY_MAX_LENGTH,
  platformAccountEventPageEnvelopeSchema,
  platformAccountHandoverSchema,
  platformAccountPageEnvelopeSchema,
  platformAccountSchema,
  type PlatformAccount,
  type PlatformAccountEventPage,
  type PlatformAccountFilters,
  type PlatformAccountHandover,
  type PlatformAccountHandoverInput,
  type PlatformAccountInput,
  type PlatformAccountPage,
} from '@/features/mailbox/platform-account-schemas';
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
  page?: number;
  perPage?: number;
}

/**
 * Browser service for the platform account ledger in `contracts/MAILBOXES.md` (section Platform
 * Accounts). Every call names the organization in the `Organization-Id` header. Owners and admins
 * write; members read only the accounts the API lets them see.
 */
export const platformAccountService = {
  /** One page of the ledger entries the caller may see, in the API's order. */
  async list(
    organizationId: number,
    filters: PlatformAccountFilters = {},
    { page = 1, perPage = 15 }: PageOptions = {}
  ): Promise<PlatformAccountPage> {
    const headers = organizationHeaders(organizationId);
    const params: Record<string, string | number> = {
      page: validPage(page),
      per_page: validPerPage(perPage),
    };
    const q = filters.q?.trim() ?? '';
    if (q) {
      if (q.length > PLATFORM_ACCOUNT_QUERY_MAX_LENGTH) throw invalidRequest();
      params.q = q;
    }
    if (filters.roleId !== undefined) params.role_id = checkedId(filters.roleId);
    if (filters.mailboxId !== undefined) params.mailbox_id = checkedId(filters.mailboxId);
    if (filters.ownerUserId !== undefined) params.owner_user_id = checkedId(filters.ownerUserId);
    if (filters.status !== undefined) params.status = filters.status;
    const parsed = platformAccountPageEnvelopeSchema.safeParse(
      await request.getEnvelope<unknown>('/platform-accounts', { headers, params })
    );
    if (!parsed.success) throw invalidResponse();
    return parsed.data;
  },

  /** Owners and admins only. */
  async create(organizationId: number, input: PlatformAccountInput): Promise<PlatformAccount> {
    const headers = organizationHeaders(organizationId);
    return parseAccount(
      await request.post<unknown, PlatformAccountInput>('/platform-accounts', input, { headers })
    );
  },

  /** Replaces every field; an omitted field takes its default. Owners and admins only. */
  async update(
    organizationId: number,
    accountId: number,
    input: PlatformAccountInput
  ): Promise<PlatformAccount> {
    const headers = organizationHeaders(organizationId);
    checkedId(accountId);
    return parseAccount(
      await request.put<unknown, PlatformAccountInput>(`/platform-accounts/${accountId}`, input, {
        headers,
      }),
      accountId
    );
  },

  /** Records a `deleted` event and removes the entry. Owners and admins only. */
  async remove(organizationId: number, accountId: number): Promise<void> {
    const headers = organizationHeaders(organizationId);
    checkedId(accountId);
    await request.delete<void>(`/platform-accounts/${accountId}`, { headers });
  },

  /** One page of the account's handover and status history, newest first. */
  async events(
    organizationId: number,
    accountId: number,
    { page = 1, perPage = 15 }: PageOptions = {}
  ): Promise<PlatformAccountEventPage> {
    const headers = organizationHeaders(organizationId);
    checkedId(accountId);
    const parsed = platformAccountEventPageEnvelopeSchema.safeParse(
      await request.getEnvelope<unknown>(`/platform-accounts/${accountId}/events`, {
        headers,
        params: { page: validPage(page), per_page: validPerPage(perPage) },
      })
    );
    if (!parsed.success || parsed.data.data.some(event => event.account_id !== accountId)) {
      throw invalidResponse();
    }
    return parsed.data;
  },

  /** Moves every account of one owner to another. Owners and admins only. */
  async handOver(
    organizationId: number,
    input: PlatformAccountHandoverInput
  ): Promise<PlatformAccountHandover> {
    const headers = organizationHeaders(organizationId);
    checkedId(input.from_user_id);
    checkedId(input.to_user_id);
    const parsed = platformAccountHandoverSchema.safeParse(
      await request.post<unknown, PlatformAccountHandoverInput>(
        '/platform-account-handovers',
        input,
        { headers }
      )
    );
    if (
      !parsed.success ||
      parsed.data.from_user_id !== input.from_user_id ||
      parsed.data.to_user_id !== input.to_user_id
    ) {
      throw invalidResponse();
    }
    return parsed.data;
  },
};

function parseAccount(value: unknown, expectedId?: number): PlatformAccount {
  const parsed = platformAccountSchema.safeParse(value);
  if (!parsed.success || (expectedId !== undefined && parsed.data.id !== expectedId)) {
    throw invalidResponse();
  }
  return parsed.data;
}

function organizationHeaders(organizationId: number): Record<string, string> {
  checkedId(organizationId);
  return { 'Organization-Id': String(organizationId) };
}

function checkedId(value: number): number {
  if (!Number.isSafeInteger(value) || value < 1) throw invalidRequest();
  return value;
}

function validPage(page: number): number {
  return Number.isSafeInteger(page) && page >= 1 ? page : 1;
}

function validPerPage(perPage: number): number {
  if (!Number.isSafeInteger(perPage) || perPage < 1) return 15;
  return Math.min(perPage, PLATFORM_ACCOUNT_PAGE_MAX_SIZE);
}

/** An argument the API would refuse; reported like the mailbox service does, before any request. */
function invalidRequest(): ApiError {
  return new ApiError('Platform account request is invalid', ClientErrorCode.INVALID_RESPONSE);
}

function invalidResponse(): ApiError {
  return new ApiError(
    'Platform account service returned an invalid response',
    ClientErrorCode.INVALID_RESPONSE
  );
}

assertContract<
  Accepts<
    z.input<typeof platformAccountPageEnvelopeSchema>,
    ContractResponse<'listPlatformAccounts', 200>
  >
>();
assertContract<
  Accepts<z.input<typeof platformAccountSchema>, ContractData<'createPlatformAccount', 201>>
>();
assertContract<
  Accepts<z.input<typeof platformAccountSchema>, ContractData<'updatePlatformAccount', 200>>
>();
assertContract<Sends<PlatformAccountInput, ContractRequest<'createPlatformAccount'>>>();
assertContract<Sends<PlatformAccountInput, ContractRequest<'updatePlatformAccount'>>>();
assertContract<
  Accepts<
    z.input<typeof platformAccountEventPageEnvelopeSchema>,
    ContractResponse<'listPlatformAccountEvents', 200>
  >
>();
assertContract<
  Accepts<
    z.input<typeof platformAccountHandoverSchema>,
    ContractData<'handOverPlatformAccounts', 201>
  >
>();
assertContract<Sends<PlatformAccountHandoverInput, ContractRequest<'handOverPlatformAccounts'>>>();
