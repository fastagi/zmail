import type { AxiosResponse } from 'axios';
import type { z } from 'zod/mini';

import {
  EXTERNAL_MAIL_CONNECTOR_ID_PATTERN,
  externalMailAuthorizationSchema,
  externalMailConnectionListSchema,
  externalMailConnectionSchema,
  externalMailConnectorListSchema,
  type CompleteExternalMailAuthorizationInput,
  type ExternalMailAuthorization,
  type ExternalMailCompletion,
  type ExternalMailConnection,
  type ExternalMailConnector,
  type StartExternalMailAuthorizationInput,
} from '@/features/mailbox/external-mail-schemas';
import { ClientErrorCode } from '@/http/codes';
import {
  assertContract,
  type Accepts,
  type ContractData,
  type ContractRequest,
  type Sends,
} from '@/http/contract';
import request, { ApiError } from '@/http/request';

/** The contract caps `state` and `code` well below this; longer values are never sent. */
const MAX_CALLBACK_VALUE_LENGTH = 2_048;

/**
 * Browser service for external mailboxes (`contracts/MAILBOXES.md`, External Mailboxes). Every call
 * names the organization in the `Organization-Id` header and acts for the signed-in member only.
 */
export const externalMailService = {
  /** The instance's connectors in a stable order; empty while external mail is off. */
  async connectors(organizationId: number): Promise<ExternalMailConnector[]> {
    const parsed = externalMailConnectorListSchema.safeParse(
      await request.get<unknown>('/external-mail-connectors', {
        headers: organizationHeaders(organizationId),
      })
    );
    if (!parsed.success) throw invalidResponse();
    return parsed.data;
  },

  /** The caller's own bound accounts, live ones first. */
  async connections(organizationId: number): Promise<ExternalMailConnection[]> {
    const parsed = externalMailConnectionListSchema.safeParse(
      await request.get<unknown>('/external-mail-connections', {
        headers: organizationHeaders(organizationId),
      })
    );
    if (!parsed.success) throw invalidResponse();
    return parsed.data;
  },

  /**
   * Starts a sign-in with a connector, or reconnects one of the caller's connections. The answer
   * names the provider page to send the browser to; it must be an absolute http(s) URL.
   */
  async start(
    organizationId: number,
    input: StartExternalMailAuthorizationInput
  ): Promise<ExternalMailAuthorization> {
    const headers = organizationHeaders(organizationId);
    if ('connection_id' in input) {
      checkedId(input.connection_id);
    } else {
      if (!EXTERNAL_MAIL_CONNECTOR_ID_PATTERN.test(input.provider)) throw invalidRequest();
      const days = input.backfill_days;
      if (days !== undefined && (!Number.isSafeInteger(days) || days < 1 || days > 90)) {
        throw invalidRequest();
      }
    }
    const parsed = externalMailAuthorizationSchema.safeParse(
      await request.post<unknown, StartExternalMailAuthorizationInput>(
        '/external-mail-authorizations',
        input,
        { headers }
      )
    );
    if (!parsed.success || !isNavigableUrl(parsed.data.authorization_url)) {
      throw invalidResponse();
    }
    return parsed.data;
  },

  /**
   * Finishes a sign-in with what the provider returned. `201` is a new external mailbox, `200` the
   * caller's own account restored with its mailbox.
   */
  async complete(
    organizationId: number,
    input: CompleteExternalMailAuthorizationInput
  ): Promise<ExternalMailCompletion> {
    const headers = organizationHeaders(organizationId);
    if (!validCallbackValue(input.state) || !validCallbackValue(input.code)) {
      throw invalidRequest();
    }
    const response = await request.post<
      AxiosResponse<unknown>,
      CompleteExternalMailAuthorizationInput
    >(
      '/external-mail-authorizations/completions',
      { state: input.state, code: input.code },
      { headers, fastbuildResponseMode: 'response' }
    );
    const body: unknown = response.data;
    const data =
      typeof body === 'object' && body !== null && 'data' in body
        ? (body as { data: unknown }).data
        : undefined;
    const parsed = externalMailConnectionSchema.safeParse(data);
    if (!parsed.success || (response.status !== 200 && response.status !== 201)) {
      throw invalidResponse();
    }
    return {
      outcome: response.status === 201 ? 'created' : 'restored',
      connection: parsed.data,
    };
  },

  /** Unbinds an account; syncing stops and the copies already synced stay. */
  async disconnect(organizationId: number, connectionId: number): Promise<void> {
    const headers = organizationHeaders(organizationId);
    checkedId(connectionId);
    await request.delete<void>(`/external-mail-connections/${connectionId}`, { headers });
  },
};

/** The `state` the provider sent back, read from the authorization URL the start answered. */
export function authorizationState(authorizationUrl: string): string | null {
  try {
    const state = new URL(authorizationUrl).searchParams.get('state');
    return state && validCallbackValue(state) ? state : null;
  } catch {
    return null;
  }
}

function isNavigableUrl(value: string): boolean {
  try {
    const url = new URL(value);
    return url.protocol === 'https:' || url.protocol === 'http:';
  } catch {
    return false;
  }
}

function validCallbackValue(value: string): boolean {
  return value.length > 0 && value.length <= MAX_CALLBACK_VALUE_LENGTH && !/\p{Cc}/u.test(value);
}

function organizationHeaders(organizationId: number): Record<string, string> {
  checkedId(organizationId);
  return { 'Organization-Id': String(organizationId) };
}

function checkedId(value: number): number {
  if (!Number.isSafeInteger(value) || value < 1) throw invalidRequest();
  return value;
}

/** An argument the API would refuse; reported before any request. */
function invalidRequest(): ApiError {
  return new ApiError('External mail request is invalid', ClientErrorCode.INVALID_RESPONSE);
}

function invalidResponse(): ApiError {
  return new ApiError(
    'External mail service returned an invalid response',
    ClientErrorCode.INVALID_RESPONSE
  );
}

assertContract<
  Accepts<
    z.input<typeof externalMailConnectorListSchema>,
    ContractData<'listExternalMailConnectors', 200>
  >
>();
assertContract<
  Accepts<
    z.input<typeof externalMailConnectionListSchema>,
    ContractData<'listExternalMailConnections', 200>
  >
>();
assertContract<
  Accepts<
    z.input<typeof externalMailAuthorizationSchema>,
    ContractData<'startExternalMailAuthorization', 201>
  >
>();
assertContract<
  Accepts<
    z.input<typeof externalMailConnectionSchema>,
    ContractData<'completeExternalMailAuthorization', 201>
  >
>();
assertContract<
  Accepts<
    z.input<typeof externalMailConnectionSchema>,
    ContractData<'completeExternalMailAuthorization', 200>
  >
>();
assertContract<
  Sends<StartExternalMailAuthorizationInput, ContractRequest<'startExternalMailAuthorization'>>
>();
assertContract<
  Sends<
    CompleteExternalMailAuthorizationInput,
    ContractRequest<'completeExternalMailAuthorization'>
  >
>();
