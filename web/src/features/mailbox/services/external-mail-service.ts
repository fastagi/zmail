import type { AxiosResponse } from 'axios';
import type { z } from 'zod/mini';

import {
  EXTERNAL_MAIL_CONNECTOR_ID_PATTERN,
  EXTERNAL_MAIL_DIRECTORY_CURSOR_MAX_LENGTH,
  EXTERNAL_MAIL_DIRECTORY_PAGE_SIZE,
  EXTERNAL_MAIL_HOST_MAX_LENGTH,
  EXTERNAL_MAIL_IMAP_PORT,
  EXTERNAL_MAIL_PASSWORD_MAX_LENGTH,
  EXTERNAL_MAIL_USERNAME_MAX_LENGTH,
  externalMailAuthorizationSchema,
  externalMailDirectoryPageEnvelopeSchema,
  externalMailPurgeSchema,
  externalMailSyncRunListSchema,
  externalMailConnectionListSchema,
  externalMailConnectionSchema,
  externalMailConnectorListSchema,
  type BindExternalMailCredentialsInput,
  type CompleteExternalMailAuthorizationInput,
  type ExternalMailAuthorization,
  type ExternalMailCompletion,
  type ExternalMailConnection,
  type ExternalMailConnector,
  type ExternalMailDirectoryPage,
  type ExternalMailPurge,
  type ExternalMailSyncRun,
  type StartExternalMailAuthorizationInput,
} from '@/features/mailbox/external-mail-schemas';
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
    return completionOf(response.status, response.data);
  },

  /**
   * Binds an account with an app password (`auth_kind: "password"`). The server signs in once
   * before anything is stored and derives the account from the login. Only the status and the
   * connection are read from the answer; nothing of the request, the password above all, is kept.
   */
  async bindCredentials(
    organizationId: number,
    input: BindExternalMailCredentialsInput
  ): Promise<ExternalMailCompletion> {
    const headers = organizationHeaders(organizationId);
    if (!validCredentials(input)) throw invalidRequest();
    const body: BindExternalMailCredentialsInput = {
      provider: input.provider,
      host: input.host,
      port: input.port,
      username: input.username,
      password: input.password,
      ...(input.backfill_days === undefined ? {} : { backfill_days: input.backfill_days }),
    };
    const response = await request.post<AxiosResponse<unknown>, BindExternalMailCredentialsInput>(
      '/external-mail-credentials',
      body,
      { headers, fastbuildResponseMode: 'response' }
    );
    return completionOf(response.status, response.data);
  },

  /** The latest sync runs of one of the caller's own connections, newest first. */
  async syncRuns(organizationId: number, connectionId: number): Promise<ExternalMailSyncRun[]> {
    const headers = organizationHeaders(organizationId);
    checkedId(connectionId);
    const parsed = externalMailSyncRunListSchema.safeParse(
      await request.get<unknown>(`/external-mail-connections/${connectionId}/sync-runs`, {
        headers,
      })
    );
    if (!parsed.success) throw invalidResponse();
    return parsed.data;
  },

  /**
   * Deletes the copies of one of the caller's revoked connections at once. `completed` is false
   * when more copies remain than one request deletes; the worker finishes them.
   */
  async purge(organizationId: number, connectionId: number): Promise<ExternalMailPurge> {
    const headers = organizationHeaders(organizationId);
    checkedId(connectionId);
    const parsed = externalMailPurgeSchema.safeParse(
      await request.post<unknown>(`/external-mail-connections/${connectionId}/purges`, undefined, {
        headers,
      })
    );
    if (!parsed.success) throw invalidResponse();
    return parsed.data;
  },

  /** One keyset page of the organization's connections, newest first; owners and admins only. */
  async directory(organizationId: number, cursor?: string): Promise<ExternalMailDirectoryPage> {
    const headers = organizationHeaders(organizationId);
    if (
      cursor !== undefined &&
      (cursor === '' || cursor.length > EXTERNAL_MAIL_DIRECTORY_CURSOR_MAX_LENGTH)
    ) {
      throw invalidRequest();
    }
    const parsed = externalMailDirectoryPageEnvelopeSchema.safeParse(
      await request.getEnvelope<unknown>('/external-mail-connection-directory', {
        headers,
        params: {
          per_page: EXTERNAL_MAIL_DIRECTORY_PAGE_SIZE,
          ...(cursor === undefined ? {} : { cursor }),
        },
      })
    );
    if (!parsed.success) throw invalidResponse();
    return { items: parsed.data.data, meta: parsed.data.meta };
  },

  /** Disconnects another member's account; the member may connect it again. */
  async forceDisconnect(organizationId: number, connectionId: number): Promise<void> {
    const headers = organizationHeaders(organizationId);
    checkedId(connectionId);
    await request.delete<void>(`/external-mail-connection-directory/${connectionId}`, {
      headers,
    });
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

/**
 * A provider page the browser may be sent to: `https:` only, and plain `http:` only on this machine
 * (`localhost`, `*.localhost`, `127.0.0.1`, `[::1]`), where the development mock answers its own
 * callback page.
 */
export function isNavigableUrl(value: string): boolean {
  let url: URL;
  try {
    url = new URL(value);
  } catch {
    return false;
  }
  if (url.protocol === 'https:') return true;
  if (url.protocol !== 'http:') return false;
  const host = url.hostname.toLowerCase();
  return (
    host === 'localhost' ||
    host.endsWith('.localhost') ||
    host === '127.0.0.1' ||
    host === '[::1]'
  );
}

/** `201` is a new mailbox, `200` the caller's own account restored; anything else is refused. */
function completionOf(status: number, body: unknown): ExternalMailCompletion {
  const data =
    typeof body === 'object' && body !== null && 'data' in body
      ? (body as { data: unknown }).data
      : undefined;
  const parsed = externalMailConnectionSchema.safeParse(data);
  if (!parsed.success || (status !== 200 && status !== 201)) throw invalidResponse();
  return { outcome: status === 201 ? 'created' : 'restored', connection: parsed.data };
}

function validCredentials(input: BindExternalMailCredentialsInput): boolean {
  const days = input.backfill_days;
  return (
    EXTERNAL_MAIL_CONNECTOR_ID_PATTERN.test(input.provider) &&
    boundedText(input.host, EXTERNAL_MAIL_HOST_MAX_LENGTH) &&
    boundedText(input.username, EXTERNAL_MAIL_USERNAME_MAX_LENGTH) &&
    boundedText(input.password, EXTERNAL_MAIL_PASSWORD_MAX_LENGTH) &&
    input.port === EXTERNAL_MAIL_IMAP_PORT &&
    (days === undefined || (Number.isSafeInteger(days) && days >= 1 && days <= 90))
  );
}

function boundedText(value: string, max: number): boolean {
  return value.length > 0 && value.length <= max && !/\p{Cc}/u.test(value);
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
assertContract<
  Sends<BindExternalMailCredentialsInput, ContractRequest<'bindExternalMailCredentials'>>
>();
assertContract<
  Accepts<
    z.input<typeof externalMailConnectionSchema>,
    ContractData<'bindExternalMailCredentials', 201>
  >
>();
assertContract<
  Accepts<
    z.input<typeof externalMailSyncRunListSchema>,
    ContractData<'listExternalMailSyncRuns', 200>
  >
>();
assertContract<
  Accepts<z.input<typeof externalMailPurgeSchema>, ContractData<'purgeExternalMailCopies', 200>>
>();
assertContract<
  Accepts<z.input<typeof externalMailPurgeSchema>, ContractData<'purgeExternalMailCopies', 202>>
>();
assertContract<
  Accepts<
    z.input<typeof externalMailDirectoryPageEnvelopeSchema>,
    ContractResponse<'listExternalMailConnectionDirectory', 200>
  >
>();
