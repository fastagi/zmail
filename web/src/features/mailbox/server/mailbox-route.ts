import 'server-only';

import type { NextResponse } from 'next/server';

import { apiErrorResponse, apiJsonBodyErrorResponse } from '@/app/api/_shared/error-response';
import { readJsonBody } from '@/app/api/_shared/json-body';
import { guardSameOriginMutation } from '@/app/api/_shared/mock-bff';
import {
  apiNoContentResponse,
  apiPaginatedResponse,
  apiSuccessResponse,
} from '@/app/api/_shared/success-response';
import { env } from '@/config/env';
import { isWebFeatureEnabled } from '@/config/features';
import { isMockBffEnabled } from '@/config/mock-bff';
import { serverEnv } from '@/config/server-env';
import { getApiSessionToken } from '@/features/auth/server/api-session';
import { getSessionUser } from '@/features/auth/server/session';
import type { AuthUser } from '@/features/auth/types';
import {
  MAILBOX_DISPLAY_NAME_MAX_LENGTH,
  MAILBOX_LOCAL_PART_MAX_LENGTH,
  MAIL_ADDRESS_MAX_LENGTH,
  MAIL_BODY_MAX_BYTES,
  MAIL_MAX_RECIPIENTS,
  MAIL_MESSAGE_ID_MAX_LENGTH,
  MAIL_PAGE_MAX_SIZE,
  MAIL_SUBJECT_MAX_LENGTH,
} from '@/features/mailbox/schemas';
import { mockMailboxStore } from '@/features/mailbox/server/mock-mailbox-store';
import type {
  CreateMailboxInput,
  MailDirection,
  SendMailMessageInput,
} from '@/features/mailbox/types';
import { ApiErrorCode, type ApiErrorCodeValue } from '@/http/codes';
import { forwardAuthenticatedGoApi } from '@/server/api-adapter/authenticated-route';
import { privateNoStoreHeaders, privateNoStoreResponse } from '@/server/http/private-response';

/**
 * Browser route boundary of the mailbox feature. Every handler requires the explicit `mailbox`
 * feature selection, selects the Go API adapter or the development mock, and authenticates the
 * caller. Writes check availability, then the browser origin, then authentication, before reading
 * a body. Mailboxes are user-owned: no handler reads an organization. Ownership, address rules,
 * and delivery stay authoritative in the API; the mock reproduces them for development.
 */

type MailboxBackend = 'go-api' | 'mock';

interface MailboxRouteEnvironment {
  adapterEnabled: boolean;
  featureEnabled: boolean;
  mockBffEnabled: boolean;
  nodeEnv: 'development' | 'production' | 'test';
}

type MailboxRouteResolution =
  { available: true; backend: MailboxBackend } | { available: false; response: NextResponse };

type AuthenticatedMailboxBackend =
  | { authenticated: true; backend: 'go-api'; accessToken: string }
  | { authenticated: true; backend: 'mock'; user: AuthUser }
  | { authenticated: false; response: NextResponse };

type MailboxTarget =
  | { available: true; backend: 'go-api'; accessToken: string }
  | { available: true; backend: 'mock'; user: AuthUser }
  | { available: false; response: NextResponse };

type Parsed<T> = { ok: true; value: T } | { ok: false; response: NextResponse };

const routeIdPattern = /^[1-9]\d{0,15}$/u;
const defaultPerPage = 15;
const maxMailboxBodyBytes = 8 * 1_024;
const maxReadStateBodyBytes = 1_024;
// Two 256 KiB bodies; JSON escaping needs headroom.
const maxSendBodyBytes = 1_024 * 1_024;

export function resolveMailboxRoute(
  environment: MailboxRouteEnvironment = currentEnvironment()
): MailboxRouteResolution {
  if (!environment.featureEnabled) return unavailable('Mailbox Web feature is disabled');
  if (environment.adapterEnabled) return { available: true, backend: 'go-api' };
  if (isMockBffEnabled({ enabled: environment.mockBffEnabled, nodeEnv: environment.nodeEnv })) {
    return { available: true, backend: 'mock' };
  }
  return unavailable('Mailbox backend is unavailable');
}

export function privateMailboxResponse<T extends Response>(response: T): T {
  return privateNoStoreResponse(response, ['Cookie']);
}

export async function listMailboxDomainsRoute(request: Request): Promise<NextResponse> {
  const target = await resolveMailboxReadRoute();
  if (!target.available) return target.response;

  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'GET',
      path: 'mailbox-domains',
      accessToken: target.accessToken,
    });
  }
  return success(mockMailboxStore.domains());
}

export async function listMailboxesRoute(request: Request): Promise<NextResponse> {
  const target = await resolveMailboxReadRoute();
  if (!target.available) return target.response;

  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'GET',
      path: 'mailboxes',
      accessToken: target.accessToken,
    });
  }
  return success(mockMailboxStore.list(target.user.id));
}

export async function createMailboxRoute(request: Request): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  const input = await mailboxBody(request);
  if (!input.ok) return input.response;

  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'POST',
      path: 'mailboxes',
      accessToken: target.accessToken,
      body: input.value,
      fieldMap: { local_part: 'local_part', display_name: 'display_name' },
    });
  }
  const result = mockMailboxStore.create(
    target.user.id,
    input.value.local_part,
    input.value.display_name ?? ''
  );
  if (result.ok) return success(result.mailbox, 201);
  if (result.error === 'reserved') {
    return failure(422, ApiErrorCode.MAILBOX_ADDRESS_RESERVED, 'Mailbox address is reserved');
  }
  if (result.error === 'taken') {
    return failure(409, ApiErrorCode.MAILBOX_ADDRESS_TAKEN, 'Mailbox address is taken');
  }
  if (result.error === 'limit') {
    return failure(409, ApiErrorCode.MAILBOX_LIMIT_REACHED, 'Mailbox limit reached');
  }
  return failure(422, ApiErrorCode.MAILBOX_ADDRESS_INVALID, 'Mailbox address is invalid');
}

export async function deleteMailboxRoute(
  request: Request,
  rawMailboxId: string
): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  const mailboxId = parseRouteId(rawMailboxId);
  if (!mailboxId.ok) return mailboxId.response;

  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'DELETE',
      path: `mailboxes/${mailboxId.value}`,
      accessToken: target.accessToken,
    });
  }
  return mockMailboxStore.delete(target.user.id, mailboxId.value)
    ? apiNoContentResponse(privateMailboxHeaders())
    : mailboxNotFound();
}

export async function listMailMessagesRoute(
  request: Request,
  rawMailboxId: string
): Promise<NextResponse> {
  const target = await resolveMailboxReadRoute();
  if (!target.available) return target.response;
  const mailboxId = parseRouteId(rawMailboxId);
  if (!mailboxId.ok) return mailboxId.response;
  const query = new URL(request.url).searchParams;
  const direction = parseDirection(query.getAll('direction'));
  if (!direction.ok) return direction.response;
  const page = positiveInteger(query.get('page'), 1);
  const perPage = Math.min(
    positiveInteger(query.get('per_page'), defaultPerPage),
    MAIL_PAGE_MAX_SIZE
  );

  if (target.backend === 'go-api') {
    // Only these three parameters reach the API; anything else in the query is dropped.
    const searchParams = new URLSearchParams({ page: String(page), per_page: String(perPage) });
    if (direction.value !== null) searchParams.set('direction', direction.value);
    return forwardAuthenticatedGoApi(request, {
      method: 'GET',
      path: `mailboxes/${mailboxId.value}/messages`,
      accessToken: target.accessToken,
      searchParams,
    });
  }
  const result = mockMailboxStore.listMessages(
    target.user.id,
    mailboxId.value,
    direction.value,
    page,
    perPage
  );
  if (!result) return mailboxNotFound();
  return apiPaginatedResponse(result.items, result.meta, result.links, privateMailboxHeaders());
}

export async function sendMailMessageRoute(
  request: Request,
  rawMailboxId: string
): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  const mailboxId = parseRouteId(rawMailboxId);
  if (!mailboxId.ok) return mailboxId.response;
  const input = await sendBody(request);
  if (!input.ok) return input.response;

  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'POST',
      path: `mailboxes/${mailboxId.value}/messages`,
      accessToken: target.accessToken,
      body: input.value,
      fieldMap: {
        to: 'to',
        cc: 'cc',
        subject: 'subject',
        text: 'text',
        html: 'html',
        in_reply_to: 'in_reply_to',
      },
    });
  }
  const result = mockMailboxStore.send(target.user.id, mailboxId.value, input.value);
  if (result.ok) return success(result.message, 201);
  return result.error === 'mailbox_not_found'
    ? mailboxNotFound()
    : failure(422, ApiErrorCode.MAILBOX_MESSAGE_INVALID, 'Mail message is invalid');
}

export async function getMailMessageRoute(
  request: Request,
  rawMailboxId: string,
  rawMessageId: string
): Promise<NextResponse> {
  const target = await resolveMailboxReadRoute();
  if (!target.available) return target.response;
  const ids = parseMessageIds(rawMailboxId, rawMessageId);
  if (!ids.ok) return ids.response;

  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'GET',
      path: `mailboxes/${ids.value.mailboxId}/messages/${ids.value.messageId}`,
      accessToken: target.accessToken,
    });
  }
  const result = mockMailboxStore.getMessage(
    target.user.id,
    ids.value.mailboxId,
    ids.value.messageId
  );
  return result.ok ? success(result.message) : missing(result.error);
}

export async function updateMailMessageRoute(
  request: Request,
  rawMailboxId: string,
  rawMessageId: string
): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  const ids = parseMessageIds(rawMailboxId, rawMessageId);
  if (!ids.ok) return ids.response;
  const input = await readStateBody(request);
  if (!input.ok) return input.response;

  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'PUT',
      path: `mailboxes/${ids.value.mailboxId}/messages/${ids.value.messageId}`,
      accessToken: target.accessToken,
      body: input.value,
      fieldMap: { read: 'read' },
    });
  }
  const result = mockMailboxStore.setRead(
    target.user.id,
    ids.value.mailboxId,
    ids.value.messageId,
    input.value.read
  );
  return result.ok ? success(result.message) : missing(result.error);
}

export async function deleteMailMessageRoute(
  request: Request,
  rawMailboxId: string,
  rawMessageId: string
): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  const ids = parseMessageIds(rawMailboxId, rawMessageId);
  if (!ids.ok) return ids.response;

  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'DELETE',
      path: `mailboxes/${ids.value.mailboxId}/messages/${ids.value.messageId}`,
      accessToken: target.accessToken,
    });
  }
  const result = mockMailboxStore.deleteMessage(
    target.user.id,
    ids.value.mailboxId,
    ids.value.messageId
  );
  return result.ok ? apiNoContentResponse(privateMailboxHeaders()) : missing(result.error);
}

/** Reads: feature availability, then authentication. */
async function resolveMailboxReadRoute(): Promise<MailboxTarget> {
  const resolution = resolveMailboxRoute();
  if (!resolution.available) return resolution;
  return targetOf(await authenticateMailboxBackend(resolution.backend));
}

/**
 * Writes: feature availability, then the same-origin check, then authentication. Handlers read
 * the body only after this resolves.
 */
async function resolveMailboxMutationRoute(request: Request): Promise<MailboxTarget> {
  const resolution = resolveMailboxRoute();
  if (!resolution.available) return resolution;
  const guard = guardSameOriginMutation(request);
  if (guard) return { available: false, response: privateMailboxResponse(guard) };
  return targetOf(await authenticateMailboxBackend(resolution.backend));
}

async function authenticateMailboxBackend(
  backend: MailboxBackend
): Promise<AuthenticatedMailboxBackend> {
  if (backend === 'go-api') {
    const accessToken = await getApiSessionToken();
    return accessToken
      ? { authenticated: true, backend, accessToken }
      : { authenticated: false, response: unauthenticated() };
  }
  const user = await getSessionUser();
  return user
    ? { authenticated: true, backend, user }
    : { authenticated: false, response: unauthenticated() };
}

function targetOf(authentication: AuthenticatedMailboxBackend): MailboxTarget {
  if (!authentication.authenticated) return { available: false, response: authentication.response };
  return authentication.backend === 'go-api'
    ? { available: true, backend: 'go-api', accessToken: authentication.accessToken }
    : { available: true, backend: 'mock', user: authentication.user };
}

async function jsonObject(
  request: Request,
  maxBytes: number
): Promise<Parsed<Record<string, unknown>>> {
  const payload = await readJsonBody(request, maxBytes);
  if (!payload.ok) {
    return {
      ok: false,
      response: privateMailboxResponse(apiJsonBodyErrorResponse(payload.error)),
    };
  }
  const value = payload.data;
  if (typeof value !== 'object' || value === null || Array.isArray(value)) {
    return { ok: false, response: invalidInput('Malformed JSON body') };
  }
  return { ok: true, value: value as Record<string, unknown> };
}

/** Shape checks only; the address rules belong to the API and, in development, to the mock. */
async function mailboxBody(request: Request): Promise<Parsed<CreateMailboxInput>> {
  const body = await jsonObject(request, maxMailboxBodyBytes);
  if (!body.ok) return body;
  const { local_part: localPart, display_name: displayName } = body.value;
  const errors: Record<string, string[]> = {};
  if (!boundedString(localPart, MAILBOX_LOCAL_PART_MAX_LENGTH) || localPart === '') {
    errors.local_part = ['The local_part field is invalid'];
  }
  if (displayName !== undefined && !boundedString(displayName, MAILBOX_DISPLAY_NAME_MAX_LENGTH)) {
    errors.display_name = ['The display_name field is invalid'];
  }
  if (Object.keys(errors).length > 0) return { ok: false, response: validationFailed(errors) };
  return {
    ok: true,
    value: {
      local_part: localPart as string,
      ...(typeof displayName === 'string' ? { display_name: displayName } : {}),
    },
  };
}

/** Shape checks only; recipients, subject, and bodies are judged by the API or the mock. */
async function sendBody(request: Request): Promise<Parsed<SendMailMessageInput>> {
  const body = await jsonObject(request, maxSendBodyBytes);
  if (!body.ok) return body;
  const { to, cc, subject, text, html, in_reply_to: inReplyTo } = body.value;
  const errors: Record<string, string[]> = {};
  if (!addressList(to) || to.length === 0) errors.to = ['The to field is invalid'];
  if (cc !== undefined && !addressList(cc)) errors.cc = ['The cc field is invalid'];
  if (!boundedString(subject, MAIL_SUBJECT_MAX_LENGTH) || subject === '') {
    errors.subject = ['The subject field is invalid'];
  }
  if (text !== undefined && !boundedString(text, MAIL_BODY_MAX_BYTES)) {
    errors.text = ['The text field is invalid'];
  }
  if (html !== undefined && !boundedString(html, MAIL_BODY_MAX_BYTES)) {
    errors.html = ['The html field is invalid'];
  }
  if (inReplyTo !== undefined && !boundedString(inReplyTo, MAIL_MESSAGE_ID_MAX_LENGTH)) {
    errors.in_reply_to = ['The in_reply_to field is invalid'];
  }
  if (Object.keys(errors).length > 0) return { ok: false, response: validationFailed(errors) };
  return {
    ok: true,
    value: {
      to: to as string[],
      ...(cc === undefined ? {} : { cc: cc as string[] }),
      subject: subject as string,
      ...(typeof text === 'string' ? { text } : {}),
      ...(typeof html === 'string' ? { html } : {}),
      ...(typeof inReplyTo === 'string' ? { in_reply_to: inReplyTo } : {}),
    },
  };
}

async function readStateBody(request: Request): Promise<Parsed<{ read: boolean }>> {
  const body = await jsonObject(request, maxReadStateBodyBytes);
  if (!body.ok) return body;
  const { read } = body.value;
  if (typeof read !== 'boolean') {
    return { ok: false, response: validationFailed({ read: ['The read field is required'] }) };
  }
  return { ok: true, value: { read } };
}

function boundedString(value: unknown, maxLength: number): value is string {
  return typeof value === 'string' && value.length <= maxLength;
}

function addressList(value: unknown): value is string[] {
  return (
    Array.isArray(value) &&
    value.length <= MAIL_MAX_RECIPIENTS &&
    value.every(item => boundedString(item, MAIL_ADDRESS_MAX_LENGTH) && item !== '')
  );
}

function parseRouteId(value: string): Parsed<number> {
  const id = routeIdPattern.test(value) ? Number(value) : Number.NaN;
  return Number.isSafeInteger(id)
    ? { ok: true, value: id }
    : { ok: false, response: invalidInput('Invalid ID format') };
}

function parseMessageIds(
  rawMailboxId: string,
  rawMessageId: string
): Parsed<{ mailboxId: number; messageId: number }> {
  const mailboxId = parseRouteId(rawMailboxId);
  if (!mailboxId.ok) return mailboxId;
  const messageId = parseRouteId(rawMessageId);
  if (!messageId.ok) return messageId;
  return { ok: true, value: { mailboxId: mailboxId.value, messageId: messageId.value } };
}

/** Absent means every message; any value other than the two folders is invalid input. */
function parseDirection(values: string[]): Parsed<MailDirection | null> {
  if (values.length === 0) return { ok: true, value: null };
  const [value] = values;
  if (values.length === 1 && (value === 'inbound' || value === 'outbound')) {
    return { ok: true, value };
  }
  return { ok: false, response: invalidInput('Invalid message direction') };
}

/** Mirrors the API: a missing or malformed number uses the fallback. */
function positiveInteger(value: string | null, fallback: number): number {
  if (!value || !/^[1-9]\d{0,8}$/u.test(value)) return fallback;
  return Number(value);
}

function currentEnvironment(): MailboxRouteEnvironment {
  return {
    adapterEnabled: serverEnv.API_ADAPTER_ENABLED,
    featureEnabled: isWebFeatureEnabled('mailbox'),
    mockBffEnabled: serverEnv.MOCK_BFF_ENABLED,
    nodeEnv: env.NODE_ENV,
  };
}

function privateMailboxHeaders(): Headers {
  return privateNoStoreHeaders(undefined, ['Cookie']);
}

function success<T>(data: T, status: 200 | 201 = 200): NextResponse {
  return apiSuccessResponse(data, { status, headers: privateMailboxHeaders() });
}

function failure(status: number, errorCode: ApiErrorCodeValue, message: string): NextResponse {
  return apiErrorResponse({ status, errorCode, message, headers: privateMailboxHeaders() });
}

function missing(error: 'mailbox_not_found' | 'not_found'): NextResponse {
  return error === 'mailbox_not_found'
    ? mailboxNotFound()
    : failure(404, ApiErrorCode.MAILBOX_MESSAGE_NOT_FOUND, 'Mail message not found');
}

function mailboxNotFound(): NextResponse {
  return failure(404, ApiErrorCode.MAILBOX_NOT_FOUND, 'Mailbox not found');
}

function invalidInput(message: string): NextResponse {
  return failure(400, ApiErrorCode.COMMON_INVALID_INPUT, message);
}

function validationFailed(errors: Record<string, string[]>): NextResponse {
  return apiErrorResponse({
    status: 422,
    errorCode: ApiErrorCode.COMMON_VALIDATION_FAILED,
    message: 'Validation failed',
    errors,
    headers: privateMailboxHeaders(),
  });
}

function unauthenticated(): NextResponse {
  return failure(401, ApiErrorCode.AUTH_UNAUTHORIZED, 'Authentication required');
}

function unavailable(message: string): MailboxRouteResolution {
  return {
    available: false,
    response: apiErrorResponse({
      status: 503,
      errorCode: ApiErrorCode.COMMON_SERVICE_UNAVAILABLE,
      message,
      headers: new Headers({ 'cache-control': 'no-store', pragma: 'no-cache' }),
    }),
  };
}
