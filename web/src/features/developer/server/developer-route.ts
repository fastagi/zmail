import 'server-only';

import { NextResponse } from 'next/server';

import { apiErrorResponse, apiJsonBodyErrorResponse } from '@/app/api/_shared/error-response';
import { readJsonBody } from '@/app/api/_shared/json-body';
import { guardMockBffRoute, guardSameOriginMutation } from '@/app/api/_shared/mock-bff';
import { apiSuccessResponse } from '@/app/api/_shared/success-response';
import { getApiSessionToken } from '@/features/auth/server/api-session';
import { getSessionUser } from '@/features/auth/server/session';
import type { AuthUser } from '@/features/auth/types';
import {
  TRANSACTIONAL_LOG_CURSOR_PATTERN,
  TRANSACTIONAL_LOG_DEFAULT_RANGE_DAYS,
  TRANSACTIONAL_LOG_EVENTS,
  TRANSACTIONAL_LOG_MAX_PAGE_SIZE,
  TRANSACTIONAL_LOG_MAX_RANGE_DAYS,
  TRANSACTIONAL_LOG_RECIPIENT_MAX_LENGTH,
  TRANSACTIONAL_MODES,
  TRANSACTIONAL_OUTCOMES,
  TRANSACTIONAL_STATUSES,
  TRANSACTIONAL_TEMPLATE_KEY_PATTERN,
} from '@/features/developer/schemas';
import {
  mockTransactionalStore,
  normalizeAddress,
  type MockTransactionalLogQuery,
  type MockTestSendResult,
} from '@/features/developer/server/mock-transactional-store';
import type { TransactionalTestSendInput } from '@/features/developer/types';
import {
  MAIL_TEMPLATE_MAX_VARIABLES,
  MAIL_TEMPLATE_VALUE_MAX_LENGTH,
} from '@/features/mailbox/schemas';
import {
  privateMailboxResponse,
  resolveMailboxRoute,
} from '@/features/mailbox/server/mailbox-route';
import { mockMailboxStore } from '@/features/mailbox/server/mock-mailbox-store';
import { mockOrganizationStore } from '@/features/organization/server/mock-organization-store';
import { parseOrganizationId } from '@/features/organization/server/organization-route';
import { ApiErrorCode, type ApiErrorCodeValue } from '@/http/codes';
import { forwardAuthenticatedGoApi } from '@/server/api-adapter/authenticated-route';
import { privateNoStoreHeaders } from '@/server/http/private-response';

/**
 * Browser route boundary of the developer console's transactional mail: the send log and console
 * test sends. Availability follows the mailbox feature (`resolveMailboxRoute`); the mock backend
 * is also behind `guardMockBffRoute()`. Writes check availability, then the browser origin, then
 * authentication, before reading a body. The API key route `/v1/transactional-messages` has no
 * browser route: backends call the API directly.
 */

type DeveloperTarget =
  | { available: true; backend: 'go-api'; accessToken: string; organizationId: number }
  | { available: true; backend: 'mock'; organizationId: number; user: AuthUser }
  | { available: false; response: NextResponse };

type AuthenticatedDeveloperBackend =
  | { authenticated: true; backend: 'go-api'; accessToken: string }
  | { authenticated: true; backend: 'mock'; user: AuthUser }
  | { authenticated: false; response: NextResponse };

type Parsed<T> = { ok: true; value: T } | { ok: false; response: NextResponse };

const routeIdPattern = /^[1-9]\d{0,15}$/u;
const rfc3339Pattern = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?(?:Z|[+-]\d{2}:\d{2})$/iu;
const dayMs = 24 * 60 * 60 * 1_000;
// The API's 64 KiB request limit for a transactional send.
const maxTestSendBodyBytes = 64 * 1_024;

/** The send log of the organization, filtered by the documented parameters only. */
export async function listTransactionalMailLogRoute(request: Request): Promise<NextResponse> {
  const target = await resolveDeveloperReadRoute(request);
  if (!target.available) return target.response;
  const query = parseLogQuery(new URL(request.url).searchParams);
  if (!query.ok) return query.response;

  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'GET',
      path: 'transactional-mail-log',
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      searchParams: query.value.forward,
    });
  }
  const page = mockTransactionalStore.list(target.organizationId, query.value.mock);
  return NextResponse.json(
    { code: 0, message: 'success', data: page.items, meta: page.meta },
    { headers: privateDeveloperHeaders() }
  );
}

/** One log entry of either mode, with its sender, masked variables, and events. */
export async function getTransactionalMailLogEntryRoute(
  request: Request,
  rawMessageId: string
): Promise<NextResponse> {
  const target = await resolveDeveloperReadRoute(request);
  if (!target.available) return target.response;
  if (!routeIdPattern.test(rawMessageId) || !Number.isSafeInteger(Number(rawMessageId))) {
    return failure(400, ApiErrorCode.COMMON_INVALID_INPUT, 'Invalid ID format');
  }
  const messageId = Number(rawMessageId);

  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'GET',
      path: `transactional-mail-log/${messageId}`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
    });
  }
  const entry = mockTransactionalStore.get(target.organizationId, messageId);
  return entry ? success(entry) : messageNotFound();
}

/** Sends a transactional template to the caller's own account address. */
export async function sendTransactionalTestRoute(request: Request): Promise<NextResponse> {
  const target = await resolveDeveloperMutationRoute(request);
  if (!target.available) return target.response;
  // The instance switch comes before the body, as in the API.
  if (target.backend === 'mock' && !mockTransactionalStore.isEnabled()) return disabled();
  const input = await testSendBody(request);
  if (!input.ok) return input.response;

  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'POST',
      path: 'transactional-test-sends',
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      body: input.value,
      fieldMap: { template: 'template', mode: 'mode', variables: 'variables' },
    });
  }
  const template = mockMailboxStore
    .listTemplates(target.organizationId, 'transactional')
    .find(candidate => candidate.key === input.value.template);
  const found = template ? mockMailboxStore.getTemplate(target.organizationId, template.id) : null;
  if (!found?.ok) {
    return failure(404, ApiErrorCode.MAILBOX_TEMPLATE_NOT_FOUND, 'Mail template not found');
  }
  return testSendResponse(
    mockTransactionalStore.testSend(target.organizationId, {
      template: found.template,
      mode: input.value.mode,
      variables: input.value.variables ?? {},
      to: target.user.email,
      userId: target.user.id,
    })
  );
}

function testSendResponse(result: MockTestSendResult): NextResponse {
  if (result.ok) return success(result.message, 202);
  switch (result.error) {
    case 'disabled':
      return disabled();
    case 'rate_limited': {
      const headers = privateDeveloperHeaders();
      headers.set('retry-after', String(result.retryAfterSeconds));
      return apiErrorResponse({
        status: 429,
        errorCode: ApiErrorCode.COMMON_RATE_LIMITED,
        message: 'Too many test sends',
        headers,
      });
    }
    case 'undeclared':
      return fieldFailure(
        ApiErrorCode.MAILBOX_MESSAGE_INVALID,
        'Variable is not declared by the template',
        result.names
      );
    case 'missing':
      return fieldFailure(
        ApiErrorCode.MAILBOX_TEMPLATE_VARIABLES_MISSING,
        'Required variable is missing',
        result.names
      );
    default:
      return failure(422, ApiErrorCode.MAILBOX_MESSAGE_INVALID, 'Recipient is not usable');
  }
}

interface LogQuery {
  forward: URLSearchParams;
  mock: MockTransactionalLogQuery;
}

/**
 * Checks the log query as the API does, so an unusable one never leaves the BFF, and keeps only
 * the documented parameters. An empty value counts as absent.
 */
function parseLogQuery(query: URLSearchParams, now: Date = new Date()): Parsed<LogQuery> {
  const names = [
    'mode',
    'status',
    'last_event',
    'outcome',
    'template',
    'to',
    'since',
    'until',
    'cursor',
    'per_page',
  ] as const;
  const values: Partial<Record<(typeof names)[number], string>> = {};
  for (const name of names) {
    const all = query.getAll(name);
    if (all.length > 1) return invalidQuery(`Repeated ${name}`);
    if (all.length === 1 && all[0] !== '') values[name] = all[0];
  }
  const { mode = 'live', status, last_event: lastEvent, outcome, template } = values;
  if (!isOneOf(TRANSACTIONAL_MODES, mode)) return invalidQuery('Unknown mode');
  if (status !== undefined && !isOneOf(TRANSACTIONAL_STATUSES, status)) {
    return invalidQuery('Unknown status');
  }
  if (lastEvent !== undefined && !isOneOf(TRANSACTIONAL_LOG_EVENTS, lastEvent)) {
    return invalidQuery('Unknown last event');
  }
  if (outcome !== undefined && !isOneOf(TRANSACTIONAL_OUTCOMES, outcome)) {
    return invalidQuery('Unknown outcome');
  }
  if (template !== undefined && !TRANSACTIONAL_TEMPLATE_KEY_PATTERN.test(template)) {
    return invalidQuery('Malformed template key');
  }
  const to = values.to === undefined ? undefined : normalizeAddress(values.to);
  if (to !== undefined && to.length > TRANSACTIONAL_LOG_RECIPIENT_MAX_LENGTH) {
    return invalidQuery('Recipient too long');
  }
  const since = values.since === undefined ? undefined : instant(values.since);
  const until = values.until === undefined ? undefined : instant(values.until);
  if (since === null) return invalidQuery('since is not a date-time');
  if (until === null) return invalidQuery('until is not a date-time');
  const end = until ?? now;
  const start = since ?? new Date(end.getTime() - TRANSACTIONAL_LOG_DEFAULT_RANGE_DAYS * dayMs);
  if (until !== undefined && start.getTime() >= until.getTime()) {
    return invalidQuery('since must be before until');
  }
  if (end.getTime() - start.getTime() > TRANSACTIONAL_LOG_MAX_RANGE_DAYS * dayMs) {
    return invalidQuery('Range longer than 30 days');
  }
  if (values.cursor !== undefined && !TRANSACTIONAL_LOG_CURSOR_PATTERN.test(values.cursor)) {
    return invalidQuery('Malformed cursor');
  }
  const perPage = values.per_page === undefined ? undefined : Number(values.per_page);
  if (
    perPage !== undefined &&
    (!/^\d{1,3}$/u.test(values.per_page ?? '') ||
      perPage < 1 ||
      perPage > TRANSACTIONAL_LOG_MAX_PAGE_SIZE)
  ) {
    return invalidQuery('per_page out of range');
  }

  const forward = new URLSearchParams();
  for (const name of names) {
    const value = name === 'to' ? to : values[name];
    if (value !== undefined) forward.set(name, value);
  }
  return {
    ok: true,
    value: {
      forward,
      mock: {
        mode,
        ...(status === undefined ? {} : { status }),
        ...(lastEvent === undefined ? {} : { lastEvent }),
        ...(outcome === undefined ? {} : { outcome }),
        ...(template === undefined ? {} : { template }),
        ...(to === undefined ? {} : { to }),
        since: start,
        ...(until === undefined ? {} : { until }),
        ...(values.cursor === undefined ? {} : { cursor: Number(values.cursor) }),
        ...(perPage === undefined ? {} : { perPage }),
      },
    },
  };
}

/** An RFC 3339 instant; null when the value is not one. */
function instant(value: string): Date | null {
  if (!rfc3339Pattern.test(value)) return null;
  const time = Date.parse(value);
  return Number.isNaN(time) ? null : new Date(time);
}

/** Shape checks only; the template, its variables, and the limits belong to the API or the mock. */
async function testSendBody(request: Request): Promise<Parsed<TransactionalTestSendInput>> {
  const payload = await readJsonBody(request, maxTestSendBodyBytes);
  if (!payload.ok) {
    return { ok: false, response: privateMailboxResponse(apiJsonBodyErrorResponse(payload.error)) };
  }
  const value = payload.data;
  if (typeof value !== 'object' || value === null || Array.isArray(value)) {
    return {
      ok: false,
      response: failure(400, ApiErrorCode.COMMON_INVALID_INPUT, 'Malformed JSON body'),
    };
  }
  const { template, mode, variables, ...rest } = value as Record<string, unknown>;
  const errors: Record<string, string[]> = {};
  if (Object.keys(rest).length > 0) errors.body = ['Unknown fields'];
  if (typeof template !== 'string' || !TRANSACTIONAL_TEMPLATE_KEY_PATTERN.test(template)) {
    errors.template = ['The template field must be a template key'];
  }
  if (typeof mode !== 'string' || !isOneOf(TRANSACTIONAL_MODES, mode)) {
    errors.mode = ['The mode field must be live or test'];
  }
  if (variables !== undefined && !variableMap(variables)) {
    errors.variables = ['The variables field is invalid'];
  }
  if (Object.keys(errors).length > 0) {
    return {
      ok: false,
      response: apiErrorResponse({
        status: 422,
        errorCode: ApiErrorCode.COMMON_VALIDATION_FAILED,
        message: 'Validation failed',
        errors,
        headers: privateDeveloperHeaders(),
      }),
    };
  }
  return {
    ok: true,
    value: {
      template: template as string,
      mode: mode as TransactionalTestSendInput['mode'],
      ...(variableMap(variables) ? { variables } : {}),
    },
  };
}

function variableMap(value: unknown): value is Record<string, string> {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) return false;
  const entries = Object.entries(value);
  return (
    entries.length <= MAIL_TEMPLATE_MAX_VARIABLES &&
    entries.every(
      ([, item]) =>
        typeof item === 'string' && Array.from(item).length <= MAIL_TEMPLATE_VALUE_MAX_LENGTH
    )
  );
}

/** Reads: feature availability, then authentication, then the organization. */
async function resolveDeveloperReadRoute(request: Request): Promise<DeveloperTarget> {
  const resolution = resolveMailboxRoute();
  if (!resolution.available) return resolution;
  const mockGuard = resolution.backend === 'mock' ? guardMockBffRoute() : null;
  if (mockGuard) return { available: false, response: privateMailboxResponse(mockGuard) };
  return targetOf(request, await authenticateDeveloperBackend(resolution.backend));
}

/**
 * Writes: feature availability, then the same-origin check, then authentication, then the
 * organization. Handlers read the body only after this resolves.
 */
async function resolveDeveloperMutationRoute(request: Request): Promise<DeveloperTarget> {
  const resolution = resolveMailboxRoute();
  if (!resolution.available) return resolution;
  const mockGuard = resolution.backend === 'mock' ? guardMockBffRoute() : null;
  if (mockGuard) return { available: false, response: privateMailboxResponse(mockGuard) };
  const originGuard = guardSameOriginMutation(request);
  if (originGuard) return { available: false, response: privateMailboxResponse(originGuard) };
  return targetOf(request, await authenticateDeveloperBackend(resolution.backend));
}

async function authenticateDeveloperBackend(
  backend: 'go-api' | 'mock'
): Promise<AuthenticatedDeveloperBackend> {
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
 * The organization the request acts in. The API decides membership; the mock resolves it from the
 * organization mock, and any member may read the log and test-send.
 */
function targetOf(
  request: Request,
  authentication: AuthenticatedDeveloperBackend
): DeveloperTarget {
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
  if (!mockOrganizationStore.resolveContext(authentication.user, organizationId)) {
    return {
      available: false,
      response: failure(404, ApiErrorCode.ORGANIZATION_NOT_FOUND, 'Organization not found'),
    };
  }
  return { available: true, backend: 'mock', organizationId, user: authentication.user };
}

function isOneOf<T extends string>(allowed: readonly T[], value: string): value is T {
  return (allowed as readonly string[]).includes(value);
}

function privateDeveloperHeaders(): Headers {
  return privateNoStoreHeaders(undefined, ['Cookie', 'Organization-Id']);
}

function success<T>(data: T, status: 200 | 202 = 200): NextResponse {
  return apiSuccessResponse(data, { status, headers: privateDeveloperHeaders() });
}

function failure(status: number, errorCode: ApiErrorCodeValue, message: string): NextResponse {
  return apiErrorResponse({ status, errorCode, message, headers: privateDeveloperHeaders() });
}

function fieldFailure(errorCode: ApiErrorCodeValue, message: string, names: string[]) {
  return apiErrorResponse({
    status: 422,
    errorCode,
    message,
    errors: Object.fromEntries(names.map(name => [`variables.${name}`, [message]])),
    headers: privateDeveloperHeaders(),
  });
}

function invalidQuery(message: string): Parsed<never> {
  return { ok: false, response: failure(400, ApiErrorCode.COMMON_INVALID_INPUT, message) };
}

function unauthenticated(): NextResponse {
  return failure(401, ApiErrorCode.AUTH_UNAUTHORIZED, 'Authentication required');
}

function disabled(): NextResponse {
  return failure(
    503,
    ApiErrorCode.MAILBOX_TRANSACTIONAL_DISABLED,
    'Transactional mail is disabled'
  );
}

function messageNotFound(): NextResponse {
  return failure(
    404,
    ApiErrorCode.MAILBOX_TRANSACTIONAL_MESSAGE_NOT_FOUND,
    'Transactional message not found'
  );
}
