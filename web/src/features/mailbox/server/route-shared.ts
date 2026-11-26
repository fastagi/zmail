import 'server-only';

import { NextResponse } from 'next/server';
import { apiErrorResponse, apiJsonBodyErrorResponse } from '@/app/api/_shared/error-response';
import { readJsonBody } from '@/app/api/_shared/json-body';
import { guardSameOriginMutation } from '@/app/api/_shared/mock-bff';
import { apiSuccessResponse } from '@/app/api/_shared/success-response';
import { env } from '@/config/env';
import { isWebFeatureEnabled } from '@/config/features';
import { isMockBffEnabled } from '@/config/mock-bff';
import { serverEnv } from '@/config/server-env';
import { getApiSessionToken } from '@/features/auth/server/api-session';
import { getSessionUser } from '@/features/auth/server/session';
import type { AuthUser } from '@/features/auth/types';
import {
  MAIL_ADDRESS_MAX_LENGTH,
  MAIL_ATTACHMENT_NAME_MAX_LENGTH,
  MAIL_ATTACHMENT_TYPE_MAX_LENGTH,
  MAIL_MAX_ATTACHMENTS,
  MAIL_MAX_RECIPIENTS,
  MAIL_TEMPLATE_MAX_VARIABLES,
  MAIL_TEMPLATE_VALUE_MAX_LENGTH,
  MAIL_TEMPLATE_VARIABLE_PATTERN,
} from '@/features/mailbox/schemas';
import type {
  MailboxViewer,
  MockMailSendResult,
  MockOrganizeError,
  MockOrganizeResult,
} from '@/features/mailbox/server/mock-mailbox-store';
import { mockOrganizationStore } from '@/features/organization/server/mock-organization-store';
import { parseOrganizationId } from '@/features/organization/server/organization-route';
import type { OrganizationContext } from '@/features/organization/types';
import type { SendMailMessageInput } from '@/features/mailbox/types';
import { ApiErrorCode, type ApiErrorCodeValue } from '@/http/codes';
import { privateNoStoreHeaders, privateNoStoreResponse } from '@/server/http/private-response';

type MailboxBackend = 'go-api' | 'mock';

interface MailboxRouteEnvironment {
  adapterEnabled: boolean;
  mailboxFeatureEnabled: boolean;
  mockBffEnabled: boolean;
  nodeEnv: 'development' | 'production' | 'test';
  organizationFeatureEnabled: boolean;
}

type MailboxRouteResolution =
  { available: true; backend: MailboxBackend } | { available: false; response: NextResponse };

type AuthenticatedMailboxBackend =
  | { authenticated: true; backend: 'go-api'; accessToken: string }
  | { authenticated: true; backend: 'mock'; user: AuthUser }
  | { authenticated: false; response: NextResponse };

/** In mock mode `context` is the caller's verified membership; the API verifies its own. */
export type MailboxTarget =
  | { available: true; backend: 'go-api'; accessToken: string; organizationId: number }
  | { available: true; backend: 'mock'; organizationId: number; context: OrganizationContext }
  | { available: false; response: NextResponse };

export type Parsed<T> = { ok: true; value: T } | { ok: false; response: NextResponse };

export const routeIdPattern = /^[1-9]\d{0,15}$/u;

export const defaultPerPage = 15;

export const maxMailboxBodyBytes = 8 * 1_024;

export const maxOrganizeBodyBytes = 4 * 1_024;

export const maxAccessBodyBytes = 1_024;

// Two 256 KiB bodies and 4 MiB of files as base64; JSON escaping needs headroom.
export const maxSendBodyBytes = 8 * 1_024 * 1_024;

export const maxContactBodyBytes = 128 * 1_024;

export function resolveMailboxRoute(
  environment: MailboxRouteEnvironment = currentEnvironment()
): MailboxRouteResolution {
  if (!environment.organizationFeatureEnabled || !environment.mailboxFeatureEnabled) {
    return unavailable('Mailbox Web feature is disabled');
  }
  if (environment.adapterEnabled) return { available: true, backend: 'go-api' };
  if (isMockBffEnabled({ enabled: environment.mockBffEnabled, nodeEnv: environment.nodeEnv })) {
    return { available: true, backend: 'mock' };
  }
  return unavailable('Mailbox backend is unavailable');
}

export function privateMailboxResponse<T extends Response>(response: T): T {
  return privateNoStoreResponse(response, ['Cookie', 'Organization-Id']);
}

/** Reads: feature availability, then authentication, then the organization. */
export async function resolveMailboxReadRoute(request: Request): Promise<MailboxTarget> {
  const resolution = resolveMailboxRoute();
  if (!resolution.available) return resolution;
  return targetOf(request, await authenticateMailboxBackend(resolution.backend));
}

/**
 * Writes: feature availability, then the same-origin check, then authentication, then the
 * organization. Handlers read the body only after this resolves.
 */
export async function resolveMailboxMutationRoute(request: Request): Promise<MailboxTarget> {
  const resolution = resolveMailboxRoute();
  if (!resolution.available) return resolution;
  const guard = guardSameOriginMutation(request);
  if (guard) return { available: false, response: privateMailboxResponse(guard) };
  return targetOf(request, await authenticateMailboxBackend(resolution.backend));
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

/**
 * The organization the request acts in. A missing header and a malformed or repeated one are
 * rejected here; the API decides membership, and the mock resolves it from the organization mock.
 */
function targetOf(request: Request, authentication: AuthenticatedMailboxBackend): MailboxTarget {
  if (!authentication.authenticated) return { available: false, response: authentication.response };
  const rawOrganizationId = request.headers.get('organization-id');
  if (rawOrganizationId === null) {
    return {
      available: false,
      response: failure(
        400,
        ApiErrorCode.ORGANIZATION_CONTEXT_REQUIRED,
        'Organization context is required'
      ),
    };
  }
  const organizationId = parseOrganizationId(rawOrganizationId);
  if (!organizationId) {
    return {
      available: false,
      response: failure(
        400,
        ApiErrorCode.ORGANIZATION_CONTEXT_INVALID,
        'Organization context is invalid'
      ),
    };
  }
  if (authentication.backend === 'go-api') {
    return {
      available: true,
      backend: 'go-api',
      accessToken: authentication.accessToken,
      organizationId,
    };
  }
  const context = mockOrganizationStore.resolveContext(authentication.user, organizationId);
  if (!context) {
    return {
      available: false,
      response: failure(404, ApiErrorCode.ORGANIZATION_NOT_FOUND, 'Organization not found'),
    };
  }
  return { available: true, backend: 'mock', organizationId, context };
}

/** Only owners and admins create mailboxes. With the API backend, the API decides. */
export function mayManageMailboxes(target: MailboxTarget & { available: true }): boolean {
  return target.backend === 'go-api' || target.context.role !== 'member';
}

/** The caller of a mock request, as the store judges access. */
export function viewerOf(target: MailboxTarget & { available: true; backend: 'mock' }): MailboxViewer {
  return { userId: target.context.user_id, role: target.context.role };
}

export async function jsonObject(
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

export function positiveId(value: unknown): value is number {
  return typeof value === 'number' && Number.isSafeInteger(value) && value > 0;
}

/** A JSON id the API can bind; zero is left to the backend's own rule. */
export function routeIdOrZero(value: unknown): value is number {
  return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0;
}

/** Bounded so a list cannot grow past what the API would read; counts are judged upstream. */
export function idList(value: unknown): value is number[] {
  return Array.isArray(value) && value.length <= 1_000 && value.every(routeIdOrZero);
}

export function boundedString(value: unknown, maxLength: number): value is string {
  return typeof value === 'string' && value.length <= maxLength;
}

export function addressList(value: unknown): value is string[] {
  return (
    Array.isArray(value) &&
    value.length <= MAIL_MAX_RECIPIENTS &&
    value.every(item => boundedString(item, MAIL_ADDRESS_MAX_LENGTH) && item !== '')
  );
}

export function parseRouteId(value: string): Parsed<number> {
  const id = routeIdPattern.test(value) ? Number(value) : Number.NaN;
  return Number.isSafeInteger(id)
    ? { ok: true, value: id }
    : { ok: false, response: invalidInput('Invalid ID format') };
}

export function parseMessageIds(
  rawMailboxId: string,
  rawMessageId: string
): Parsed<{ mailboxId: number; messageId: number }> {
  const mailboxId = parseRouteId(rawMailboxId);
  if (!mailboxId.ok) return mailboxId;
  const messageId = parseRouteId(rawMessageId);
  if (!messageId.ok) return messageId;
  return { ok: true, value: { mailboxId: mailboxId.value, messageId: messageId.value } };
}

export function isOneOf<T extends string>(allowed: readonly T[], value: string): value is T {
  return (allowed as readonly string[]).includes(value);
}

export function positiveInteger(value: string | null, fallback: number): number {
  if (!value || !/^[1-9]\d{0,8}$/u.test(value)) return fallback;
  return Number(value);
}

function currentEnvironment(): MailboxRouteEnvironment {
  return {
    adapterEnabled: serverEnv.API_ADAPTER_ENABLED,
    mailboxFeatureEnabled: isWebFeatureEnabled('mailbox'),
    mockBffEnabled: serverEnv.MOCK_BFF_ENABLED,
    nodeEnv: env.NODE_ENV,
    organizationFeatureEnabled: isWebFeatureEnabled('organization'),
  };
}

export function privateMailboxHeaders(): Headers {
  return privateNoStoreHeaders(undefined, ['Cookie', 'Organization-Id']);
}

export function success<T>(data: T, status: 200 | 201 | 202 = 200): NextResponse {
  return apiSuccessResponse(data, { status, headers: privateMailboxHeaders() });
}

export function failure(status: number, errorCode: ApiErrorCodeValue, message: string): NextResponse {
  return apiErrorResponse({ status, errorCode, message, headers: privateMailboxHeaders() });
}

export function missing(error: 'mailbox_not_found' | 'forbidden' | 'not_found'): NextResponse {
  if (error === 'mailbox_not_found') return mailboxNotFound();
  if (error === 'forbidden') return accessForbidden();
  return failure(404, ApiErrorCode.MAILBOX_MESSAGE_NOT_FOUND, 'Mail message not found');
}

/** Some access to the mailbox, but not what the route needs. */
export function accessForbidden(): NextResponse {
  return failure(403, ApiErrorCode.PERMISSION_DENIED, 'Mailbox access forbidden');
}

export function accessInvalid(): NextResponse {
  return failure(422, ApiErrorCode.MAILBOX_ACCESS_INVALID, 'Mailbox access is invalid');
}

export function mailboxNotFound(): NextResponse {
  return failure(404, ApiErrorCode.MAILBOX_NOT_FOUND, 'Mailbox not found');
}

export function mailDomainNotFound(): NextResponse {
  return failure(404, ApiErrorCode.MAILBOX_DOMAIN_NOT_FOUND, 'Mail domain not found');
}

export function contactListNotFound(): NextResponse {
  return failure(404, ApiErrorCode.MAILBOX_CONTACT_LIST_NOT_FOUND, 'Mail contact list not found');
}

export function templateNotFound(): NextResponse {
  return failure(404, ApiErrorCode.MAILBOX_TEMPLATE_NOT_FOUND, 'Mail template not found');
}

export function invalidInput(message: string): NextResponse {
  return failure(400, ApiErrorCode.COMMON_INVALID_INPUT, message);
}

export function validationFailed(errors: Record<string, string[]>): NextResponse {
  return apiErrorResponse({
    status: 422,
    errorCode: ApiErrorCode.COMMON_VALIDATION_FAILED,
    message: 'Validation failed',
    errors,
    headers: privateMailboxHeaders(),
  });
}

export function forbidden(): NextResponse {
  return failure(403, ApiErrorCode.PERMISSION_DENIED, 'Mailbox management forbidden');
}

export function unauthenticated(): NextResponse {
  return failure(401, ApiErrorCode.AUTH_UNAUTHORIZED, 'Authentication required');
}

export function unavailable(message: string): MailboxRouteResolution {
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

export function variableMap(value: unknown): value is Record<string, string> {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) return false;
  const entries = Object.entries(value);
  return (
    entries.length <= MAIL_TEMPLATE_MAX_VARIABLES &&
    entries.every(
      ([name, item]) =>
        MAIL_TEMPLATE_VARIABLE_PATTERN.test(name) &&
        boundedString(item, MAIL_TEMPLATE_VALUE_MAX_LENGTH)
    )
  );
}

type AttachmentInput = NonNullable<SendMailMessageInput['attachments']>[number];

/** Shape checks only; names, base64, and the total size are judged by the API or the mock. */
export function attachmentList(value: unknown): value is AttachmentInput[] {
  return (
    Array.isArray(value) &&
    value.length <= MAIL_MAX_ATTACHMENTS &&
    value.every(item => {
      if (typeof item !== 'object' || item === null || Array.isArray(item)) return false;
      const {
        filename,
        content_type: contentType,
        content,
        ...rest
      } = item as Record<string, unknown>;
      return (
        Object.keys(rest).length === 0 &&
        boundedString(filename, MAIL_ATTACHMENT_NAME_MAX_LENGTH) &&
        filename !== '' &&
        (contentType === undefined ||
          boundedString(contentType, MAIL_ATTACHMENT_TYPE_MAX_LENGTH)) &&
        boundedString(content, maxSendBodyBytes) &&
        content !== ''
      );
    })
  );
}

export function mockSendResponse(result: MockMailSendResult): NextResponse {
  if (result.ok) {
    const status =
      result.message.status === 'queued' && result.message.queued_reason === 'scheduled' ? 202 : 201;
    return success(result.message, status);
  }
  switch (result.error) {
    case 'mailbox_not_found':
      return mailboxNotFound();
    case 'forbidden':
      return accessForbidden();
    case 'template_not_found':
      return templateNotFound();
    case 'variables_missing':
      return apiErrorResponse({
        status: 422,
        errorCode: ApiErrorCode.MAILBOX_TEMPLATE_VARIABLES_MISSING,
        message: 'Template variables are missing',
        errors: Object.fromEntries(
          result.missing.map(name => [`template.variables.${name}`, ['is required']])
        ),
        headers: privateMailboxHeaders(),
      });
    default:
      return failure(422, ApiErrorCode.MAILBOX_MESSAGE_INVALID, 'Mail message is invalid');
  }
}

export function organizeResponse<T>(result: MockOrganizeResult<T>, status: 200 | 201 = 200): NextResponse {
  return result.ok ? success(result.value, status) : organizeFailure(result);
}

export function organizeFailure(
  result:
    | { ok: false; error: MockOrganizeError }
    | { ok: false; error: 'validation'; fields: Record<string, string[]> }
): NextResponse {
  if (result.error === 'validation' && 'fields' in result) return validationFailed(result.fields);
  switch (result.error) {
    case 'folder_not_found':
      return failure(404, ApiErrorCode.MAILBOX_FOLDER_NOT_FOUND, 'Mail folder not found');
    case 'label_not_found':
      return failure(404, ApiErrorCode.MAILBOX_LABEL_NOT_FOUND, 'Mail label not found');
    case 'folder_name_taken':
      return failure(409, ApiErrorCode.MAILBOX_FOLDER_NAME_TAKEN, 'Mail folder name is taken');
    case 'label_name_taken':
      return failure(409, ApiErrorCode.MAILBOX_LABEL_NAME_TAKEN, 'Mail label name is taken');
    case 'folder_limit':
      return failure(422, ApiErrorCode.MAILBOX_FOLDER_LIMIT_REACHED, 'Mail folder limit reached');
    case 'label_limit':
      return failure(422, ApiErrorCode.MAILBOX_LABEL_LIMIT_REACHED, 'Mail label limit reached');
    case 'search_too_broad':
      return failure(422, ApiErrorCode.MAILBOX_SEARCH_TOO_BROAD, 'Search is too broad');
    case 'role_not_found':
      return failure(404, ApiErrorCode.MAILBOX_ROLE_NOT_FOUND, 'Mail role not found');
    case 'role_name_taken':
      return failure(409, ApiErrorCode.MAILBOX_ROLE_NAME_TAKEN, 'Mail role name is taken');
    case 'role_limit':
      return failure(422, ApiErrorCode.MAILBOX_ROLE_LIMIT_REACHED, 'Mail role limit reached');
    case 'mailbox_not_found':
    case 'forbidden':
    case 'not_found':
      return missing(result.error);
    default:
      return validationFailed({});
  }
}

export function encodeDirectoryCursor(id: number): string {
  return Buffer.from(String(id)).toString('base64url');
}

export function decodeDirectoryCursor(token: string): number | null {
  const decoded = Buffer.from(token, 'base64url').toString();
  return /^[1-9]\d{0,15}$/u.test(decoded) && Buffer.from(decoded).toString('base64url') === token
    ? Number(decoded)
    : null;
}
