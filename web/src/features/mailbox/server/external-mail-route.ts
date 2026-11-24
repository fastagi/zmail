import 'server-only';

import { NextResponse } from 'next/server';
import { apiNoContentResponse } from '@/app/api/_shared/success-response';
import {
  EXTERNAL_MAIL_CONNECTOR_ID_PATTERN,
  EXTERNAL_MAIL_CREDENTIALS_UPSTREAM_TIMEOUT_MS,
  EXTERNAL_MAIL_DIRECTORY_CURSOR_MAX_LENGTH,
  EXTERNAL_MAIL_DIRECTORY_MAX_PAGE_SIZE,
  EXTERNAL_MAIL_DIRECTORY_PAGE_SIZE,
  EXTERNAL_MAIL_HOST_MAX_LENGTH,
  EXTERNAL_MAIL_IMAP_PORT,
  EXTERNAL_MAIL_SMTP_PORTS,
  EXTERNAL_MAIL_DEFAULT_SMTP_PORT,
  EXTERNAL_MAIL_PASSWORD_MAX_LENGTH,
  EXTERNAL_MAIL_USERNAME_MAX_LENGTH,
} from '@/features/mailbox/external-mail-schemas';
import { mockAgentGrantStore } from '@/features/mailbox/server/mock-agent-grant-store';
import { mockMailboxStore } from '@/features/mailbox/server/mock-mailbox-store';
import { mockOrganizationStore } from '@/features/organization/server/mock-organization-store';
import { ApiErrorCode } from '@/http/codes';
import { forwardAuthenticatedGoApi } from '@/server/api-adapter/authenticated-route';
import {
  resolveMailboxReadRoute,
  resolveMailboxMutationRoute,
  mayManageMailboxes,
  jsonObject,
  positiveId,
  boundedString,
  parseRouteId,
  privateMailboxHeaders,
  success,
  failure,
  invalidInput,
  validationFailed,
  forbidden,
  encodeDirectoryCursor,
  decodeDirectoryCursor,
} from './route-shared';

/*
 * External mailboxes (`contracts/MAILBOXES.md`, External Mailboxes): connectors, binding an outside
 * account, and the caller's own connections. The BFF checks shapes; the API, or in development
 * the mock with its fake connectors, judges everything else.
 */

const maxExternalMailBodyBytes = 4 * 1_024;

/** The development mock of external mail loads only when a mock route first needs it. */
async function externalMailMock() {
  return (await import('@/features/mailbox/server/mock-external-mail-store')).mockExternalMailStore;
}

const maxExternalCallbackValueLength = 2_048;

/** The instance's connectors; empty while external mail is off. */
export async function listExternalMailConnectorsRoute(request: Request): Promise<NextResponse> {
  const target = await resolveMailboxReadRoute(request);
  if (!target.available) return target.response;
  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'GET',
      path: 'external-mail-connectors',
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
    });
  }
  return success((await externalMailMock()).connectors());
}

/** The caller's own bound accounts in the organization. */
export async function listExternalMailConnectionsRoute(request: Request): Promise<NextResponse> {
  const target = await resolveMailboxReadRoute(request);
  if (!target.available) return target.response;
  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'GET',
      path: 'external-mail-connections',
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
    });
  }
  return success((await externalMailMock()).list(target.organizationId, target.context.user_id));
}

/** Starts a sign-in with `provider`, or reconnects the caller's `connection_id`. */
export async function startExternalMailAuthorizationRoute(request: Request): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  const body = await jsonObject(request, maxExternalMailBodyBytes);
  if (!body.ok) return body.response;
  const { provider, backfill_days: backfillDays, connection_id: connectionId } = body.value;
  const errors: Record<string, string[]> = {};
  if (
    provider !== undefined &&
    (typeof provider !== 'string' || !EXTERNAL_MAIL_CONNECTOR_ID_PATTERN.test(provider))
  ) {
    errors.provider = ['must be a connector id'];
  }
  if (
    backfillDays !== undefined &&
    (typeof backfillDays !== 'number' ||
      !Number.isSafeInteger(backfillDays) ||
      backfillDays < 1 ||
      backfillDays > 90)
  ) {
    errors.backfill_days = ['must be between 1 and 90'];
  }
  if (connectionId !== undefined && !positiveId(connectionId)) {
    errors.connection_id = ['must be a positive id'];
  }
  if (Object.keys(errors).length > 0) return validationFailed(errors);
  if (provider === undefined && connectionId === undefined) {
    return failure(
      422,
      ApiErrorCode.MAILBOX_EXTERNAL_AUTHORIZATION_INVALID,
      'A provider or a connection is required'
    );
  }
  const input = {
    ...(provider === undefined ? {} : { provider: provider as string }),
    ...(backfillDays === undefined ? {} : { backfill_days: backfillDays as number }),
    ...(connectionId === undefined ? {} : { connection_id: connectionId as number }),
  };

  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'POST',
      path: 'external-mail-authorizations',
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      body: input,
      fieldMap: {
        provider: 'provider',
        backfill_days: 'backfill_days',
        connection_id: 'connection_id',
      },
    });
  }
  const result = (await externalMailMock()).start(
    target.organizationId,
    target.context.user_id,
    new URL(request.url).origin,
    input
  );
  if (result.ok) return success(result.value, 201);
  switch (result.error) {
    case 'provider_unavailable':
      return failure(
        404,
        ApiErrorCode.MAILBOX_EXTERNAL_PROVIDER_UNAVAILABLE,
        'External mail provider is unavailable'
      );
    case 'connection_not_found':
      return externalConnectionNotFound();
    default:
      return failure(
        422,
        ApiErrorCode.MAILBOX_EXTERNAL_AUTHORIZATION_INVALID,
        'External mail authorization is invalid'
      );
  }
}

/** Finishes a sign-in with the `state` and `code` the provider sent back. */
export async function completeExternalMailAuthorizationRoute(
  request: Request
): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  const body = await jsonObject(request, maxExternalMailBodyBytes);
  if (!body.ok) return body.response;
  const { state, code } = body.value;
  const errors: Record<string, string[]> = {};
  if (!boundedString(state, maxExternalCallbackValueLength) || state === '') {
    errors.state = ['is required'];
  }
  if (!boundedString(code, maxExternalCallbackValueLength) || code === '') {
    errors.code = ['is required'];
  }
  if (Object.keys(errors).length > 0) return validationFailed(errors);
  const input = { state: state as string, code: code as string };

  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'POST',
      path: 'external-mail-authorizations/completions',
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      body: input,
      fieldMap: { state: 'state', code: 'code' },
    });
  }
  const userId = target.context.user_id;
  const result = (await externalMailMock()).complete(
    target.organizationId,
    userId,
    mockOrganizationStore.memberEmails(target.organizationId).get(userId) ?? '',
    input,
    {
      create: (organizationId, ownerUserId, address, provider, days) =>
        mockMailboxStore.createExternal(organizationId, ownerUserId, address, provider, days),
      sync: (organizationId, mailboxId, provider, days) =>
        mockMailboxStore.syncExternal(organizationId, mailboxId, provider, days),
    }
  );
  if (result.ok) return success(result.value, result.status);
  switch (result.error) {
    case 'authorization_not_found':
      return failure(
        404,
        ApiErrorCode.MAILBOX_EXTERNAL_AUTHORIZATION_NOT_FOUND,
        'External mail authorization not found'
      );
    case 'provider_unavailable':
      return failure(
        404,
        ApiErrorCode.MAILBOX_EXTERNAL_PROVIDER_UNAVAILABLE,
        'External mail provider is unavailable'
      );
    case 'account_mismatch':
      return failure(
        409,
        ApiErrorCode.MAILBOX_EXTERNAL_ACCOUNT_MISMATCH,
        'Signed in to another account'
      );
    case 'account_taken':
      return failure(
        409,
        ApiErrorCode.MAILBOX_EXTERNAL_ACCOUNT_TAKEN,
        'Account is bound by another member'
      );
    case 'connection_limit':
      return failure(
        409,
        ApiErrorCode.MAILBOX_EXTERNAL_CONNECTION_LIMIT,
        'External connection limit reached'
      );
    default:
      return failure(
        422,
        ApiErrorCode.MAILBOX_EXTERNAL_AUTHORIZATION_FAILED,
        'The provider refused the sign-in'
      );
  }
}

/** Unbinds one connection: the member who bound it, or an owner or admin. */
export async function revokeExternalMailConnectionRoute(
  request: Request,
  rawConnectionId: string
): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  const connectionId = parseRouteId(rawConnectionId);
  if (!connectionId.ok) return connectionId.response;

  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'DELETE',
      path: `external-mail-connections/${connectionId.value}`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
    });
  }
  const external = await externalMailMock();
  const revoked = external.revoke(
    target.organizationId,
    { userId: target.context.user_id, manages: target.context.role !== 'member' },
    connectionId.value
  );
  if (!revoked) return externalConnectionNotFound();
  // A disconnected account ends the agent grants of its mailbox and deletes their drafts.
  const connectedMailboxId = external.mailboxIdOf(target.organizationId, connectionId.value);
  if (connectedMailboxId !== null) {
    mockAgentGrantStore.endMailbox(target.organizationId, connectedMailboxId);
  }
  return apiNoContentResponse(privateMailboxHeaders());
}

/**
 * Binds an account with an app password. The BFF checks shapes only and never logs, stores, or
 * echoes the password; the API (or the mock) signs in once before anything is kept.
 */
export async function bindExternalMailCredentialsRoute(request: Request): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  const body = await jsonObject(request, maxExternalMailBodyBytes);
  if (!body.ok) return body.response;
  const {
    provider,
    host,
    port,
    username,
    password,
    smtp_host: smtpHost,
    smtp_port: smtpPort,
    backfill_days: backfillDays,
    connection_id: connectionId,
  } = body.value;
  const errors: Record<string, string[]> = {};
  if (typeof provider !== 'string' || !EXTERNAL_MAIL_CONNECTOR_ID_PATTERN.test(provider)) {
    errors.provider = ['must be a connector id'];
  }
  if (!credentialText(host, EXTERNAL_MAIL_HOST_MAX_LENGTH)) errors.host = ['is required'];
  if (!credentialText(username, EXTERNAL_MAIL_USERNAME_MAX_LENGTH)) {
    errors.username = ['is required'];
  }
  if (!credentialText(password, EXTERNAL_MAIL_PASSWORD_MAX_LENGTH)) {
    errors.password = ['is required'];
  }
  if (port !== undefined && port !== EXTERNAL_MAIL_IMAP_PORT) {
    errors.port = [`must be ${EXTERNAL_MAIL_IMAP_PORT}`];
  }
  if (smtpHost !== undefined && !credentialText(smtpHost, EXTERNAL_MAIL_HOST_MAX_LENGTH)) {
    errors.smtp_host = ['must be a server name'];
  }
  if (
    smtpPort !== undefined &&
    !(EXTERNAL_MAIL_SMTP_PORTS as readonly unknown[]).includes(smtpPort)
  ) {
    errors.smtp_port = ['must be 465 or 587'];
  }
  if (
    backfillDays !== undefined &&
    (typeof backfillDays !== 'number' ||
      !Number.isSafeInteger(backfillDays) ||
      backfillDays < 1 ||
      backfillDays > 90)
  ) {
    errors.backfill_days = ['must be between 1 and 90'];
  }
  if (connectionId !== undefined && !positiveId(connectionId)) {
    errors.connection_id = ['must be a positive id'];
  }
  if (Object.keys(errors).length > 0) return validationFailed(errors);
  const input = {
    ...(connectionId === undefined ? {} : { connection_id: connectionId as number }),
    provider: provider as string,
    host: host as string,
    port: EXTERNAL_MAIL_IMAP_PORT,
    username: username as string,
    password: password as string,
    ...(smtpHost === undefined
      ? {}
      : {
          smtp_host: smtpHost as string,
          smtp_port: (smtpPort as number | undefined) ?? EXTERNAL_MAIL_DEFAULT_SMTP_PORT,
        }),
    ...(backfillDays === undefined ? {} : { backfill_days: backfillDays as number }),
  };

  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'POST',
      path: 'external-mail-credentials',
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      body: input,
      // The API's sign-in may take 45 seconds; the shared upstream timeout is far shorter.
      timeoutMs: EXTERNAL_MAIL_CREDENTIALS_UPSTREAM_TIMEOUT_MS,
      fieldMap: {
        provider: 'provider',
        host: 'host',
        port: 'port',
        username: 'username',
        password: 'password',
        smtp_host: 'smtp_host',
        smtp_port: 'smtp_port',
        backfill_days: 'backfill_days',
        connection_id: 'connection_id',
      },
    });
  }
  const result = (await externalMailMock()).bindCredentials(
    target.organizationId,
    target.context.user_id,
    input,
    {
      create: (organizationId, ownerUserId, address, connector, days) =>
        mockMailboxStore.createExternal(organizationId, ownerUserId, address, connector, days),
      sync: (organizationId, mailboxId, connector, days) =>
        mockMailboxStore.syncExternal(organizationId, mailboxId, connector, days),
      setSend: (organizationId, mailboxId, sends) =>
        mockMailboxStore.setConnectedSend(organizationId, mailboxId, sends),
    }
  );
  if (result.ok) return success(result.value, result.status);
  switch (result.error) {
    case 'rate_limited': {
      const response = failure(429, ApiErrorCode.COMMON_RATE_LIMITED, 'Too many attempts');
      response.headers.set('Retry-After', String(result.retryAfterSeconds));
      return response;
    }
    case 'provider_unavailable':
      return failure(
        404,
        ApiErrorCode.MAILBOX_EXTERNAL_PROVIDER_UNAVAILABLE,
        'External mail provider is unavailable'
      );
    case 'connection_not_found':
      return externalConnectionNotFound();
    case 'account_mismatch':
      return failure(
        409,
        ApiErrorCode.MAILBOX_EXTERNAL_ACCOUNT_MISMATCH,
        'Signed in to another account'
      );
    case 'server_unreachable':
      return failure(
        422,
        ApiErrorCode.MAILBOX_EXTERNAL_SERVER_UNREACHABLE,
        'The mail server could not be reached'
      );
    case 'authorization_failed':
      return failure(
        422,
        ApiErrorCode.MAILBOX_EXTERNAL_AUTHORIZATION_FAILED,
        'The mail server refused the sign-in'
      );
    case 'account_taken':
      return failure(
        409,
        ApiErrorCode.MAILBOX_EXTERNAL_ACCOUNT_TAKEN,
        'Account is bound by another member'
      );
    case 'connection_limit':
      return failure(
        409,
        ApiErrorCode.MAILBOX_EXTERNAL_CONNECTION_LIMIT,
        'External connection limit reached'
      );
    default:
      return failure(
        422,
        ApiErrorCode.MAILBOX_EXTERNAL_AUTHORIZATION_INVALID,
        'External mail credentials are invalid'
      );
  }
}

function credentialText(value: unknown, maxLength: number): value is string {
  return (
    typeof value === 'string' &&
    value.length > 0 &&
    value.length <= maxLength &&
    !/\p{Cc}/u.test(value)
  );
}

/** The latest sync runs of the caller's own connection; anyone else gets 404. */
export async function listExternalMailSyncRunsRoute(
  request: Request,
  rawConnectionId: string
): Promise<NextResponse> {
  const target = await resolveMailboxReadRoute(request);
  if (!target.available) return target.response;
  const connectionId = parseRouteId(rawConnectionId);
  if (!connectionId.ok) return connectionId.response;
  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'GET',
      path: `external-mail-connections/${connectionId.value}/sync-runs`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
    });
  }
  const runs = (await externalMailMock()).syncRuns(
    target.organizationId,
    target.context.user_id,
    connectionId.value
  );
  return runs ? success(runs) : externalConnectionNotFound();
}

/** Deletes the copies of the caller's own revoked connection at once. */
export async function purgeExternalMailCopiesRoute(
  request: Request,
  rawConnectionId: string
): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  const connectionId = parseRouteId(rawConnectionId);
  if (!connectionId.ok) return connectionId.response;
  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'POST',
      path: `external-mail-connections/${connectionId.value}/purges`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
    });
  }
  const result = (await externalMailMock()).purge(
    target.organizationId,
    target.context.user_id,
    connectionId.value,
    (organizationId, mailboxId) => mockMailboxStore.removeExternal(organizationId, mailboxId)
  );
  if (result.ok) return success(result.value);
  return result.error === 'not_revoked'
    ? failure(
        409,
        ApiErrorCode.MAILBOX_EXTERNAL_CONNECTION_NOT_REVOKED,
        'External mail connection is not revoked'
      )
    : externalConnectionNotFound();
}

/** Owners and admins: a keyset page of every connection, masked; never mail or counts. */
export async function listExternalMailConnectionDirectoryRoute(
  request: Request
): Promise<NextResponse> {
  const target = await resolveMailboxReadRoute(request);
  if (!target.available) return target.response;
  if (!mayManageMailboxes(target)) return forbidden();
  const query = new URL(request.url).searchParams;
  if (query.has('page')) return invalidInput('page is not supported; use cursor');
  const forward = new URLSearchParams();
  const errors: Record<string, string[]> = {};
  let perPage = EXTERNAL_MAIL_DIRECTORY_PAGE_SIZE;
  const rawPerPage = query.get('per_page');
  if (rawPerPage !== null && rawPerPage !== '') {
    const value = /^[1-9]\d{0,8}$/u.test(rawPerPage) ? Number(rawPerPage) : 0;
    if (value < 1 || value > EXTERNAL_MAIL_DIRECTORY_MAX_PAGE_SIZE) {
      errors.per_page = ['The per_page field is invalid'];
    } else {
      perPage = value;
      forward.set('per_page', rawPerPage);
    }
  }
  const cursor = query.get('cursor');
  let beforeId: number | undefined;
  if (cursor !== null && cursor !== '') {
    if (cursor.length > EXTERNAL_MAIL_DIRECTORY_CURSOR_MAX_LENGTH) {
      errors.cursor = ['The cursor field is invalid'];
    } else {
      forward.set('cursor', cursor);
      beforeId = decodeDirectoryCursor(cursor) ?? 0;
    }
  }
  if (Object.keys(errors).length > 0) return validationFailed(errors);
  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'GET',
      path: 'external-mail-connection-directory',
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      searchParams: forward,
    });
  }
  if (beforeId === 0) return invalidInput('Invalid directory cursor');
  const names = mockOrganizationStore.memberDisplayNames(target.organizationId);
  const page = (await externalMailMock()).directory(target.organizationId, {
    beforeId,
    perPage,
    ownerName: userId => names.get(userId) ?? null,
  });
  return NextResponse.json(
    {
      code: 0,
      message: 'success',
      data: page.items,
      meta: {
        per_page: perPage,
        has_more: page.nextBeforeId !== null,
        next_cursor: page.nextBeforeId === null ? null : encodeDirectoryCursor(page.nextBeforeId),
      },
    },
    { headers: privateMailboxHeaders() }
  );
}

/** Owners and admins disconnect any connection of the organization; recorded. */
export async function forceDisconnectExternalMailConnectionRoute(
  request: Request,
  rawConnectionId: string
): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  if (!mayManageMailboxes(target)) return forbidden();
  const connectionId = parseRouteId(rawConnectionId);
  if (!connectionId.ok) return connectionId.response;
  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'DELETE',
      path: `external-mail-connection-directory/${connectionId.value}`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
    });
  }
  const revoked = (await externalMailMock()).revoke(
    target.organizationId,
    { userId: target.context.user_id, manages: true },
    connectionId.value
  );
  return revoked ? apiNoContentResponse(privateMailboxHeaders()) : externalConnectionNotFound();
}

function externalConnectionNotFound(): NextResponse {
  return failure(
    404,
    ApiErrorCode.MAILBOX_EXTERNAL_CONNECTION_NOT_FOUND,
    'External mail connection not found'
  );
}
