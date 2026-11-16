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
  MAIL_ATTACHMENTS_MAX_BYTES,
  MAIL_ATTACHMENT_NAME_MAX_LENGTH,
  MAIL_ATTACHMENT_TYPE_MAX_LENGTH,
  MAIL_MAX_ATTACHMENTS,
  MAIL_BODY_MAX_BYTES,
  MAIL_LOG_QUERY_MAX_LENGTH,
  MAIL_MAX_RECIPIENTS,
  MAIL_MESSAGE_ID_MAX_LENGTH,
  MAIL_PAGE_MAX_SIZE,
  MAIL_STATS_DEFAULT_DAYS,
  MAIL_STATS_MAX_DAYS,
  MAIL_SUBJECT_MAX_LENGTH,
  MAIL_TEMPLATE_MAX_VARIABLES,
  MAIL_TEMPLATE_NAME_MAX_LENGTH,
  MAIL_TEMPLATE_TEST_MAX_RECIPIENTS,
  MAIL_TEMPLATE_VALUE_MAX_LENGTH,
  MAIL_TEMPLATE_VARIABLE_PATTERN,
} from '@/features/mailbox/schemas';
import {
  mockMailboxStore,
  type MockMailSendResult,
  type MockMailTemplateResult,
} from '@/features/mailbox/server/mock-mailbox-store';
import { mockOrganizationStore } from '@/features/organization/server/mock-organization-store';
import { parseOrganizationId } from '@/features/organization/server/organization-route';
import type { OrganizationContext } from '@/features/organization/types';
import type {
  CreateMailboxInput,
  MailDirection,
  MailLogFilters,
  MailStatus,
  MailTemplateInput,
  MailTemplateTestInput,
  SendMailMessageInput,
} from '@/features/mailbox/types';
import { ApiErrorCode, type ApiErrorCodeValue } from '@/http/codes';
import { forwardAuthenticatedGoApi } from '@/server/api-adapter/authenticated-route';
import { privateNoStoreHeaders, privateNoStoreResponse } from '@/server/http/private-response';

/**
 * Browser route boundary of the mailbox feature. Every handler requires the `organization` and
 * `mailbox` feature selections, selects the Go API adapter or the development mock, authenticates
 * the caller, and reads the organization from the `Organization-Id` header. Writes check
 * availability, then the browser origin, then authentication, before reading a body. Mailboxes
 * belong to an organization: membership, roles, address rules, and delivery stay authoritative in
 * the API; the mock reproduces them for development.
 */

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
type MailboxTarget =
  | { available: true; backend: 'go-api'; accessToken: string; organizationId: number }
  | { available: true; backend: 'mock'; organizationId: number; context: OrganizationContext }
  | { available: false; response: NextResponse };

type Parsed<T> = { ok: true; value: T } | { ok: false; response: NextResponse };

const routeIdPattern = /^[1-9]\d{0,15}$/u;
const defaultPerPage = 15;
const maxMailboxBodyBytes = 8 * 1_024;
const maxReadStateBodyBytes = 1_024;
// Two 256 KiB bodies and 4 MiB of files as base64; JSON escaping needs headroom.
const maxSendBodyBytes = 8 * 1_024 * 1_024;
// The largest download the API can answer, with room for nothing else.
const maxAttachmentDownloadBytes = MAIL_ATTACHMENTS_MAX_BYTES;
// Two 256 KiB bodies with JSON escaping.
const maxTemplateBodyBytes = 2 * 1_024 * 1_024;
const maxTemplateTestBodyBytes = 512 * 1_024;

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

export async function listMailboxDomainsRoute(request: Request): Promise<NextResponse> {
  const target = await resolveMailboxReadRoute(request);
  if (!target.available) return target.response;

  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'GET',
      path: 'mailbox-domains',
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
    });
  }
  return success(mockMailboxStore.domains());
}

export async function listMailboxesRoute(request: Request): Promise<NextResponse> {
  const target = await resolveMailboxReadRoute(request);
  if (!target.available) return target.response;

  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'GET',
      path: 'mailboxes',
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
    });
  }
  return success(mockMailboxStore.list(target.organizationId));
}

export async function createMailboxRoute(request: Request): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  if (!mayManageMailboxes(target)) return forbidden();
  const input = await mailboxBody(request);
  if (!input.ok) return input.response;

  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'POST',
      path: 'mailboxes',
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      body: input.value,
      fieldMap: { local_part: 'local_part', display_name: 'display_name' },
    });
  }
  const result = mockMailboxStore.create(
    target.organizationId,
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
  if (!mayManageMailboxes(target)) return forbidden();
  const mailboxId = parseRouteId(rawMailboxId);
  if (!mailboxId.ok) return mailboxId.response;

  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'DELETE',
      path: `mailboxes/${mailboxId.value}`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
    });
  }
  return mockMailboxStore.delete(target.organizationId, mailboxId.value)
    ? apiNoContentResponse(privateMailboxHeaders())
    : mailboxNotFound();
}

export async function listMailMessagesRoute(
  request: Request,
  rawMailboxId: string
): Promise<NextResponse> {
  const target = await resolveMailboxReadRoute(request);
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
      organizationId: String(target.organizationId),
      searchParams,
    });
  }
  const result = mockMailboxStore.listMessages(
    target.organizationId,
    mailboxId.value,
    direction.value,
    page,
    perPage
  );
  if (!result) return mailboxNotFound();
  return apiPaginatedResponse(result.items, result.meta, result.links, privateMailboxHeaders());
}

/** The log: every mailbox of the organization, filtered by the whitelisted parameters only. */
export async function listMailLogRoute(request: Request): Promise<NextResponse> {
  const target = await resolveMailboxReadRoute(request);
  if (!target.available) return target.response;
  const query = new URL(request.url).searchParams;
  const filters = parseLogFilters(query);
  if (!filters.ok) return filters.response;
  const page = positiveInteger(query.get('page'), 1);
  const perPage = Math.min(
    positiveInteger(query.get('per_page'), defaultPerPage),
    MAIL_PAGE_MAX_SIZE
  );

  if (target.backend === 'go-api') {
    // Only these six parameters reach the API; anything else in the query is dropped.
    const searchParams = new URLSearchParams({ page: String(page), per_page: String(perPage) });
    const { direction, status, mailboxId, q } = filters.value;
    if (direction) searchParams.set('direction', direction);
    if (status) searchParams.set('status', status);
    if (mailboxId !== undefined) searchParams.set('mailbox_id', String(mailboxId));
    if (q) searchParams.set('q', q);
    return forwardAuthenticatedGoApi(request, {
      method: 'GET',
      path: 'mail-messages',
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      searchParams,
    });
  }
  const result = mockMailboxStore.listLog(target.organizationId, filters.value, page, perPage);
  return apiPaginatedResponse(result.items, result.meta, result.links, privateMailboxHeaders());
}

/** Per-day counts for the last `days` UTC days; the window defaults to a week. */
export async function listMailMessageStatsRoute(request: Request): Promise<NextResponse> {
  const target = await resolveMailboxReadRoute(request);
  if (!target.available) return target.response;
  const days = parseStatsDays(new URL(request.url).searchParams.getAll('days'));
  if (!days.ok) return days.response;

  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'GET',
      path: 'mail-message-stats',
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      searchParams: new URLSearchParams({ days: String(days.value) }),
    });
  }
  return success(mockMailboxStore.stats(target.organizationId, days.value));
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
      organizationId: String(target.organizationId),
      body: input.value,
      fieldMap: {
        to: 'to',
        cc: 'cc',
        subject: 'subject',
        text: 'text',
        html: 'html',
        in_reply_to: 'in_reply_to',
        attachments: 'attachments',
      },
    });
  }
  return mockSendResponse(
    mockMailboxStore.send(target.organizationId, mailboxId.value, input.value)
  );
}

function mockSendResponse(result: MockMailSendResult): NextResponse {
  if (result.ok) return success(result.message, 201);
  switch (result.error) {
    case 'mailbox_not_found':
      return mailboxNotFound();
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

export async function listMailTemplatesRoute(request: Request): Promise<NextResponse> {
  const target = await resolveMailboxReadRoute(request);
  if (!target.available) return target.response;
  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'GET',
      path: 'mail-templates',
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
    });
  }
  return success(mockMailboxStore.listTemplates(target.organizationId));
}

export async function createMailTemplateRoute(request: Request): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  const input = await templateBody(request);
  if (!input.ok) return input.response;
  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'POST',
      path: 'mail-templates',
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      body: input.value,
      fieldMap: templateFieldMap,
    });
  }
  return mockTemplateResponse(
    mockMailboxStore.createTemplate(target.organizationId, input.value),
    201
  );
}

export async function getMailTemplateRoute(
  request: Request,
  rawTemplateId: string
): Promise<NextResponse> {
  const target = await resolveMailboxReadRoute(request);
  if (!target.available) return target.response;
  const templateId = parseRouteId(rawTemplateId);
  if (!templateId.ok) return templateId.response;
  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'GET',
      path: `mail-templates/${templateId.value}`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
    });
  }
  return mockTemplateResponse(
    mockMailboxStore.getTemplate(target.organizationId, templateId.value)
  );
}

export async function updateMailTemplateRoute(
  request: Request,
  rawTemplateId: string
): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  const templateId = parseRouteId(rawTemplateId);
  if (!templateId.ok) return templateId.response;
  const input = await templateBody(request);
  if (!input.ok) return input.response;
  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'PUT',
      path: `mail-templates/${templateId.value}`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      body: input.value,
      fieldMap: templateFieldMap,
    });
  }
  return mockTemplateResponse(
    mockMailboxStore.updateTemplate(target.organizationId, templateId.value, input.value)
  );
}

export async function deleteMailTemplateRoute(
  request: Request,
  rawTemplateId: string
): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  const templateId = parseRouteId(rawTemplateId);
  if (!templateId.ok) return templateId.response;
  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'DELETE',
      path: `mail-templates/${templateId.value}`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
    });
  }
  return mockMailboxStore.deleteTemplate(target.organizationId, templateId.value)
    ? privateMailboxResponse(apiNoContentResponse())
    : templateNotFound();
}

export async function sendMailTemplateTestRoute(
  request: Request,
  rawTemplateId: string
): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  const templateId = parseRouteId(rawTemplateId);
  if (!templateId.ok) return templateId.response;
  const input = await templateTestBody(request);
  if (!input.ok) return input.response;
  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'POST',
      path: `mail-templates/${templateId.value}/tests`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      body: input.value,
      fieldMap: { mailbox_id: 'mailbox_id', to: 'to' },
    });
  }
  return mockSendResponse(
    mockMailboxStore.sendTemplateTest(
      target.organizationId,
      templateId.value,
      input.value.mailbox_id,
      input.value.to,
      input.value.variables ?? {}
    )
  );
}

const templateFieldMap = {
  name: 'name',
  subject: 'subject',
  html: 'html',
  text: 'text',
  sample_variables: 'sample_variables',
} as const;

function mockTemplateResponse(result: MockMailTemplateResult, status: 200 | 201 = 200) {
  if (result.ok) return success(result.template, status);
  switch (result.error) {
    case 'not_found':
      return templateNotFound();
    case 'taken':
      return failure(409, ApiErrorCode.MAILBOX_TEMPLATE_NAME_TAKEN, 'Mail template name is taken');
    case 'limit':
      return failure(
        409,
        ApiErrorCode.MAILBOX_TEMPLATE_LIMIT_REACHED,
        'Mail template limit reached'
      );
    default:
      return failure(422, ApiErrorCode.MAILBOX_TEMPLATE_INVALID, 'Mail template is invalid');
  }
}

function templateNotFound(): NextResponse {
  return failure(404, ApiErrorCode.MAILBOX_TEMPLATE_NOT_FOUND, 'Mail template not found');
}

export async function getMailMessageRoute(
  request: Request,
  rawMailboxId: string,
  rawMessageId: string
): Promise<NextResponse> {
  const target = await resolveMailboxReadRoute(request);
  if (!target.available) return target.response;
  const ids = parseMessageIds(rawMailboxId, rawMessageId);
  if (!ids.ok) return ids.response;

  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'GET',
      path: `mailboxes/${ids.value.mailboxId}/messages/${ids.value.messageId}`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
    });
  }
  const result = mockMailboxStore.getMessage(
    target.organizationId,
    ids.value.mailboxId,
    ids.value.messageId
  );
  return result.ok ? success(result.message) : missing(result.error);
}

export async function downloadMailAttachmentRoute(
  request: Request,
  rawMailboxId: string,
  rawMessageId: string,
  rawAttachmentId: string
): Promise<Response> {
  const target = await resolveMailboxReadRoute(request);
  if (!target.available) return target.response;
  const ids = parseMessageIds(rawMailboxId, rawMessageId);
  if (!ids.ok) return ids.response;
  const attachmentId = parseRouteId(rawAttachmentId);
  if (!attachmentId.ok) return attachmentId.response;

  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'GET',
      path: `mailboxes/${ids.value.mailboxId}/messages/${ids.value.messageId}/attachments/${attachmentId.value}`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      responseType: 'binary',
      maxResponseBytes: maxAttachmentDownloadBytes,
    });
  }
  const result = mockMailboxStore.attachment(
    target.organizationId,
    ids.value.mailboxId,
    ids.value.messageId,
    attachmentId.value
  );
  if (!result.ok) return missing(result.error);
  return new Response(new Uint8Array(result.content), {
    status: 200,
    headers: attachmentHeaders(result.filename),
  });
}

/** Every file is an opaque download: the declared type is never served. */
function attachmentHeaders(filename: string): Headers {
  const headers = privateMailboxHeaders();
  headers.set('content-type', 'application/octet-stream');
  headers.set('x-content-type-options', 'nosniff');
  headers.set('content-security-policy', "default-src 'none'; sandbox");
  headers.set(
    'content-disposition',
    `attachment; filename*=utf-8''${encodeURIComponent(filename)}`
  );
  return headers;
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
      organizationId: String(target.organizationId),
      body: input.value,
      fieldMap: { read: 'read' },
    });
  }
  const result = mockMailboxStore.setRead(
    target.organizationId,
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
      organizationId: String(target.organizationId),
    });
  }
  const result = mockMailboxStore.deleteMessage(
    target.organizationId,
    ids.value.mailboxId,
    ids.value.messageId
  );
  return result.ok ? apiNoContentResponse(privateMailboxHeaders()) : missing(result.error);
}

/** Reads: feature availability, then authentication, then the organization. */
async function resolveMailboxReadRoute(request: Request): Promise<MailboxTarget> {
  const resolution = resolveMailboxRoute();
  if (!resolution.available) return resolution;
  return targetOf(request, await authenticateMailboxBackend(resolution.backend));
}

/**
 * Writes: feature availability, then the same-origin check, then authentication, then the
 * organization. Handlers read the body only after this resolves.
 */
async function resolveMailboxMutationRoute(request: Request): Promise<MailboxTarget> {
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

/** Only owners and admins create and delete mailboxes. With the API backend, the API decides. */
function mayManageMailboxes(target: MailboxTarget & { available: true }): boolean {
  return target.backend === 'go-api' || target.context.role !== 'member';
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
  const { to, cc, subject, text, html, in_reply_to: inReplyTo, attachments, template } = body.value;
  const errors: Record<string, string[]> = {};
  if (!addressList(to) || to.length === 0) errors.to = ['The to field is invalid'];
  if (cc !== undefined && !addressList(cc)) errors.cc = ['The cc field is invalid'];
  // A template supplies the subject; without one the subject is required.
  if (
    template === undefined
      ? !boundedString(subject, MAIL_SUBJECT_MAX_LENGTH) || subject === ''
      : subject !== undefined && !boundedString(subject, MAIL_SUBJECT_MAX_LENGTH)
  ) {
    errors.subject = ['The subject field is invalid'];
  }
  if (template !== undefined && !templateReference(template)) {
    errors.template = ['The template field is invalid'];
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
  if (attachments !== undefined && !attachmentList(attachments)) {
    errors.attachments = ['The attachments field is invalid'];
  }
  if (Object.keys(errors).length > 0) return { ok: false, response: validationFailed(errors) };
  return {
    ok: true,
    value: {
      to: to as string[],
      ...(cc === undefined ? {} : { cc: cc as string[] }),
      ...(typeof subject === 'string' ? { subject } : {}),
      ...(typeof text === 'string' ? { text } : {}),
      ...(typeof html === 'string' ? { html } : {}),
      ...(typeof inReplyTo === 'string' ? { in_reply_to: inReplyTo } : {}),
      ...(attachmentList(attachments) ? { attachments } : {}),
      ...(templateReference(template) ? { template } : {}),
    },
  };
}

function templateReference(value: unknown): value is NonNullable<SendMailMessageInput['template']> {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) return false;
  const { id, variables, ...rest } = value as Record<string, unknown>;
  return (
    Object.keys(rest).length === 0 &&
    typeof id === 'number' &&
    Number.isSafeInteger(id) &&
    id > 0 &&
    (variables === undefined || variableMap(variables))
  );
}

function variableMap(value: unknown): value is Record<string, string> {
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

/** Shape checks only; names, placeholders, and bodies are judged by the API or the mock. */
function templateFields(body: Record<string, unknown>): Parsed<MailTemplateInput> {
  const { name, subject, html, text, sample_variables: samples, ...rest } = body;
  const errors: Record<string, string[]> = {};
  if (Object.keys(rest).length > 0) errors.body = ['Unknown fields'];
  if (!boundedString(name, MAIL_TEMPLATE_NAME_MAX_LENGTH) || name === '') {
    errors.name = ['The name field is invalid'];
  }
  if (!boundedString(subject, MAIL_SUBJECT_MAX_LENGTH) || subject === '') {
    errors.subject = ['The subject field is invalid'];
  }
  if (html !== undefined && !boundedString(html, MAIL_BODY_MAX_BYTES)) {
    errors.html = ['The html field is invalid'];
  }
  if (text !== undefined && !boundedString(text, MAIL_BODY_MAX_BYTES)) {
    errors.text = ['The text field is invalid'];
  }
  if (samples !== undefined && !variableMap(samples)) {
    errors.sample_variables = ['The sample_variables field is invalid'];
  }
  if (Object.keys(errors).length > 0) return { ok: false, response: validationFailed(errors) };
  return {
    ok: true,
    value: {
      name: name as string,
      subject: subject as string,
      ...(typeof html === 'string' ? { html } : {}),
      ...(typeof text === 'string' ? { text } : {}),
      ...(variableMap(samples) ? { sample_variables: samples } : {}),
    },
  };
}

async function templateBody(request: Request): Promise<Parsed<MailTemplateInput>> {
  const body = await jsonObject(request, maxTemplateBodyBytes);
  return body.ok ? templateFields(body.value) : body;
}

async function templateTestBody(request: Request): Promise<Parsed<MailTemplateTestInput>> {
  const body = await jsonObject(request, maxTemplateTestBodyBytes);
  if (!body.ok) return body;
  const { mailbox_id: mailboxId, to, variables, ...rest } = body.value;
  const errors: Record<string, string[]> = {};
  if (Object.keys(rest).length > 0) errors.body = ['Unknown fields'];
  if (!(typeof mailboxId === 'number' && Number.isSafeInteger(mailboxId) && mailboxId > 0)) {
    errors.mailbox_id = ['The mailbox_id field is invalid'];
  }
  if (!addressList(to) || to.length === 0 || to.length > MAIL_TEMPLATE_TEST_MAX_RECIPIENTS) {
    errors.to = ['The to field is invalid'];
  }
  if (variables !== undefined && !variableMap(variables)) {
    errors.variables = ['The variables field is invalid'];
  }
  if (Object.keys(errors).length > 0) return { ok: false, response: validationFailed(errors) };
  return {
    ok: true,
    value: {
      mailbox_id: mailboxId as number,
      to: to as string[],
      ...(variableMap(variables) ? { variables } : {}),
    },
  };
}

type AttachmentInput = NonNullable<SendMailMessageInput['attachments']>[number];

/** Shape checks only; names, base64, and the total size are judged by the API or the mock. */
function attachmentList(value: unknown): value is AttachmentInput[] {
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

const logDirections: readonly MailDirection[] = ['inbound', 'outbound'];
const logStatuses: readonly MailStatus[] = ['received', 'queued', 'sent', 'failed'];

/**
 * As in the API, an absent or empty parameter does not filter. A repeated parameter, an unknown
 * direction or status, a non-positive mailbox, or an over-long search is invalid input.
 */
function parseLogFilters(query: URLSearchParams): Parsed<MailLogFilters> {
  const invalid = (message: string): Parsed<MailLogFilters> => ({
    ok: false,
    response: invalidInput(message),
  });
  const single = (name: string): string | null => {
    const values = query.getAll(name);
    return values.length > 1 ? null : (values[0] ?? '');
  };
  const direction = single('direction');
  if (direction === null || (direction !== '' && !isOneOf(logDirections, direction))) {
    return invalid('Invalid message direction');
  }
  const status = single('status');
  if (status === null || (status !== '' && !isOneOf(logStatuses, status))) {
    return invalid('Invalid message status');
  }
  const mailbox = single('mailbox_id');
  if (mailbox === null) return invalid('Invalid mailbox filter');
  const mailboxId = mailbox === '' ? null : parseRouteId(mailbox);
  if (mailboxId !== null && !mailboxId.ok) return invalid('Invalid mailbox filter');
  const rawSearch = single('q');
  if (rawSearch === null) return invalid('Invalid search');
  const q = rawSearch.trim();
  if (Array.from(q).length > MAIL_LOG_QUERY_MAX_LENGTH) return invalid('Invalid search');

  return {
    ok: true,
    value: {
      ...(direction === '' ? {} : { direction }),
      ...(status === '' ? {} : { status }),
      ...(mailboxId === null ? {} : { mailboxId: mailboxId.value }),
      ...(q === '' ? {} : { q }),
    },
  };
}

function isOneOf<T extends string>(allowed: readonly T[], value: string): value is T {
  return (allowed as readonly string[]).includes(value);
}

/** Absent means the default week; anything but one integer from 1 to 90 is invalid input. */
function parseStatsDays(values: string[]): Parsed<number> {
  if (values.length === 0) return { ok: true, value: MAIL_STATS_DEFAULT_DAYS };
  const [value] = values;
  const days = values.length === 1 && /^[1-9]\d?$/u.test(value) ? Number(value) : Number.NaN;
  return days >= 1 && days <= MAIL_STATS_MAX_DAYS
    ? { ok: true, value: days }
    : { ok: false, response: invalidInput('Invalid statistics window') };
}

/** Mirrors the API: a missing or malformed number uses the fallback. */
function positiveInteger(value: string | null, fallback: number): number {
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

function privateMailboxHeaders(): Headers {
  return privateNoStoreHeaders(undefined, ['Cookie', 'Organization-Id']);
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

function forbidden(): NextResponse {
  return failure(403, ApiErrorCode.PERMISSION_DENIED, 'Mailbox management forbidden');
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
