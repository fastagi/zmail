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
  MAIL_BROADCAST_CONTACT_VARIABLES,
  MAIL_BROADCAST_LABEL_MAX_LENGTH,
  MAIL_BROADCAST_NAME_MAX_LENGTH,
  MAIL_BROADCAST_TEST_MAX_PERCENT,
  MAIL_BROADCAST_TEST_WAIT_MAX_MINUTES,
  MAIL_CONTACT_IMPORT_MAX_BYTES,
  MAIL_CONTACT_LIST_NAME_MAX_LENGTH,
  MAIL_CONTACT_MAX_LISTS,
  MAIL_CONTACT_MAX_PROPERTIES,
  MAIL_DOMAIN_NAME_MAX_LENGTH,
  MAIL_DNS_RECORD_CONTENT_MAX_LENGTH,
  MAIL_DNS_RECORD_ID_PATTERN,
  MAIL_DNS_RECORD_NAME_MAX_LENGTH,
  MAILBOX_ACCESS_EVENTS_MAX_PAGE_SIZE,
  MAIL_ATTACHMENT_NAME_MAX_LENGTH,
  MAIL_ATTACHMENT_TYPE_MAX_LENGTH,
  MAIL_MAX_ATTACHMENTS,
  MAIL_BODY_MAX_BYTES,
  MAIL_TEMPLATE_DESIGN_MAX_BYTES,
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
  mockBroadcastStore,
  type MockBroadcastResult,
} from '@/features/mailbox/server/mock-broadcast-store';
import {
  mockContactStore,
  type MockContactResult,
} from '@/features/mailbox/server/mock-contact-store';
import {
  mockDomainStore,
  type MockDomainResult,
} from '@/features/mailbox/server/mock-domain-store';
import {
  mockZoneStore,
  type MockDnsRecordCreate,
  type MockDnsRecordListResult,
  type MockDnsRecordRemoveResult,
  type MockDnsRecordResult,
} from '@/features/mailbox/server/mock-zone-store';
import {
  mockMailboxStore,
  type MailboxViewer,
  type MockAccessError,
  type MockAccessResult,
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
  MailBroadcastInput,
  MailRecipientStatus,
  MailContactFilters,
  MailContactImportInput,
  MailContactInput,
  MailContactStatus,
  MailDomainCapability,
  MailDomainInput,
  MailDnsRecordInput,
  MailDnsRecordPatch,
  MailDnsRecordType,
  MailboxPermission,
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
// A 4096-character record content with JSON escaping.
const maxDnsRecordBodyBytes = 32 * 1_024;
const maxReadStateBodyBytes = 1_024;
const maxAccessBodyBytes = 1_024;
// Two 256 KiB bodies and 4 MiB of files as base64; JSON escaping needs headroom.
const maxSendBodyBytes = 8 * 1_024 * 1_024;
// The largest download the API can answer, with room for nothing else.
const maxAttachmentDownloadBytes = MAIL_ATTACHMENTS_MAX_BYTES;
// Two 256 KiB bodies with JSON escaping.
const maxTemplateBodyBytes = 2 * 1_024 * 1_024;
const maxTemplateTestBodyBytes = 512 * 1_024;
const maxContactBodyBytes = 128 * 1_024;
// The CSV is at most 5 MiB; JSON escaping can roughly double quotes and line breaks.
const maxContactImportBodyBytes = 2 * MAIL_CONTACT_IMPORT_MAX_BYTES + 64 * 1_024;
const contactStatuses: readonly MailContactStatus[] = ['subscribed', 'unsubscribed', 'suppressed'];

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
  return success(mockMailboxStore.domains(mockDomainStore.verifiedNames(target.organizationId)));
}

export async function listMailDomainsRoute(request: Request): Promise<NextResponse> {
  const target = await resolveMailboxReadRoute(request);
  if (!target.available) return target.response;
  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'GET',
      path: 'mail-domains',
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
    });
  }
  return success(mockDomainStore.list(target.organizationId));
}

export async function getMailDomainRoute(
  request: Request,
  rawDomainId: string
): Promise<NextResponse> {
  const target = await resolveMailboxReadRoute(request);
  if (!target.available) return target.response;
  const domainId = parseRouteId(rawDomainId);
  if (!domainId.ok) return domainId.response;
  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'GET',
      path: `mail-domains/${domainId.value}`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
    });
  }
  const domain = mockDomainStore.get(target.organizationId, domainId.value);
  return domain ? success(domain) : mailDomainNotFound();
}

export async function connectMailDomainRoute(request: Request): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  if (!mayManageMailboxes(target)) return forbidden();
  const input = await mailDomainBody(request);
  if (!input.ok) return input.response;
  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'POST',
      path: 'mail-domains',
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      body: input.value,
      fieldMap: { name: 'name', capability: 'capability' },
    });
  }
  return mockDomainResponse(
    mockDomainStore.connect(target.organizationId, input.value.name, input.value.capability),
    201
  );
}

/** Runs the setup again (`setups`) or re-reads DNS (`checks`); neither reads a body. */
export async function refreshMailDomainRoute(
  request: Request,
  rawDomainId: string,
  action: 'setups' | 'checks'
): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  if (!mayManageMailboxes(target)) return forbidden();
  const domainId = parseRouteId(rawDomainId);
  if (!domainId.ok) return domainId.response;
  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'POST',
      path: `mail-domains/${domainId.value}/${action}`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
    });
  }
  return mockDomainResponse(mockDomainStore.refresh(target.organizationId, domainId.value, action));
}

/** Turns a send-only domain into one that also receives; reads no body. */
export async function enableMailDomainReceivingRoute(
  request: Request,
  rawDomainId: string
): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  if (!mayManageMailboxes(target)) return forbidden();
  const domainId = parseRouteId(rawDomainId);
  if (!domainId.ok) return domainId.response;
  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'POST',
      path: `mail-domains/${domainId.value}/receiving`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
    });
  }
  return mockDomainResponse(mockDomainStore.enableReceiving(target.organizationId, domainId.value));
}

/** Changes nothing, so members may preview too. */
export async function previewMailDomainRoute(request: Request): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  const body = await jsonObject(request, maxMailboxBodyBytes);
  if (!body.ok) return body.response;
  const { name } = body.value;
  if (!boundedString(name, MAIL_DOMAIN_NAME_MAX_LENGTH) || name.trim() === '') {
    return validationFailed({ name: ['The name field is invalid'] });
  }
  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'POST',
      path: 'mail-domain-previews',
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      body: { name },
      fieldMap: { name: 'name' },
    });
  }
  const result = mockDomainStore.preview(name);
  if (result.ok) return success(result.preview);
  return result.error === 'taken'
    ? failure(409, ApiErrorCode.MAILBOX_DOMAIN_TAKEN, 'Mail domain is already connected')
    : failure(422, ApiErrorCode.MAILBOX_DOMAIN_INVALID, 'Mail domain name is invalid');
}

export async function deleteMailDomainRoute(
  request: Request,
  rawDomainId: string
): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  if (!mayManageMailboxes(target)) return forbidden();
  const domainId = parseRouteId(rawDomainId);
  if (!domainId.ok) return domainId.response;
  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'DELETE',
      path: `mail-domains/${domainId.value}`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
    });
  }
  const result = mockDomainStore.remove(target.organizationId, domainId.value);
  if (result.ok) return apiNoContentResponse(privateMailboxHeaders());
  return result.error === 'in_use'
    ? failure(409, ApiErrorCode.MAILBOX_DOMAIN_IN_USE, 'Mail domain still has mailboxes')
    : mailDomainNotFound();
}

function mockDomainResponse(result: MockDomainResult, status: 200 | 201 = 200): NextResponse {
  if (result.ok) return success(result.domain, status);
  switch (result.error) {
    case 'not_found':
      return mailDomainNotFound();
    case 'taken':
      return failure(409, ApiErrorCode.MAILBOX_DOMAIN_TAKEN, 'Mail domain is already connected');
    case 'limit':
      return failure(422, ApiErrorCode.MAILBOX_DOMAIN_LIMIT_REACHED, 'Mail domain limit reached');
    case 'zone_not_found':
      return failure(
        422,
        ApiErrorCode.MAILBOX_DOMAIN_ZONE_NOT_FOUND,
        'No manageable DNS zone contains the mail domain'
      );
    case 'zone_pending':
      return failure(409, ApiErrorCode.MAILBOX_ZONE_PENDING, 'Managed zone is not active yet');
    case 'zone_limit':
      return failure(422, ApiErrorCode.MAILBOX_ZONE_LIMIT_REACHED, 'Managed zone limit reached');
    default:
      return failure(422, ApiErrorCode.MAILBOX_DOMAIN_INVALID, 'Mail domain name is invalid');
  }
}

function mailDomainNotFound(): NextResponse {
  return failure(404, ApiErrorCode.MAILBOX_DOMAIN_NOT_FOUND, 'Mail domain not found');
}

async function mailDomainBody(request: Request): Promise<Parsed<MailDomainInput>> {
  const body = await jsonObject(request, maxMailboxBodyBytes);
  if (!body.ok) return body;
  const { name, capability } = body.value;
  const errors: Record<string, string[]> = {};
  if (!boundedString(name, MAIL_DOMAIN_NAME_MAX_LENGTH) || name.trim() === '') {
    errors.name = ['The name field is invalid'];
  }
  const validCapability =
    typeof capability === 'string' && isOneOf(mailDomainCapabilities, capability);
  if (capability !== undefined && !validCapability) {
    errors.capability = ['The capability field is invalid'];
  }
  if (!boundedString(name, MAIL_DOMAIN_NAME_MAX_LENGTH) || Object.keys(errors).length > 0) {
    return { ok: false, response: validationFailed(errors) };
  }
  return { ok: true, value: { name, ...(validCapability ? { capability } : {}) } };
}

export async function listMailZonesRoute(request: Request): Promise<NextResponse> {
  const target = await resolveMailboxReadRoute(request);
  if (!target.available) return target.response;
  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'GET',
      path: 'mail-zones',
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
    });
  }
  return success(mockZoneStore.list(target.organizationId));
}

export async function getMailZoneRoute(request: Request, rawZoneId: string): Promise<NextResponse> {
  const target = await resolveMailboxReadRoute(request);
  if (!target.available) return target.response;
  const zoneId = parseRouteId(rawZoneId);
  if (!zoneId.ok) return zoneId.response;
  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'GET',
      path: `mail-zones/${zoneId.value}`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
    });
  }
  const zone = mockZoneStore.get(target.organizationId, zoneId.value);
  return zone ? success(zone) : mailZoneNotFound();
}

export async function deleteMailZoneRoute(
  request: Request,
  rawZoneId: string
): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  if (!mayManageMailboxes(target)) return forbidden();
  const zoneId = parseRouteId(rawZoneId);
  if (!zoneId.ok) return zoneId.response;
  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'DELETE',
      path: `mail-zones/${zoneId.value}`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
    });
  }
  const result = mockZoneStore.remove(
    target.organizationId,
    zoneId.value,
    mockDomainStore.inZone(target.organizationId, zoneId.value)
  );
  if (result.ok) return apiNoContentResponse(privateMailboxHeaders());
  switch (result.error) {
    case 'in_use':
      return failure(
        409,
        ApiErrorCode.MAILBOX_ZONE_IN_USE,
        'A mail domain still lives in the zone'
      );
    case 'still_delegated':
      return failure(
        409,
        ApiErrorCode.MAILBOX_ZONE_STILL_DELEGATED,
        'The registrar still delegates the zone to Cloudflare'
      );
    default:
      return mailZoneNotFound();
  }
}

export async function listMailDnsRecordsRoute(
  request: Request,
  rawZoneId: string
): Promise<NextResponse> {
  const target = await resolveMailboxReadRoute(request);
  if (!target.available) return target.response;
  const zoneId = parseRouteId(rawZoneId);
  if (!zoneId.ok) return zoneId.response;
  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'GET',
      path: `mail-zones/${zoneId.value}/dns-records`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
    });
  }
  const result: MockDnsRecordListResult = mockZoneStore.listRecords(
    target.organizationId,
    zoneId.value,
    mockDomainStore.inZone(target.organizationId, zoneId.value)
  );
  return result.ok ? success(result.records) : mailZoneNotFound();
}

export async function createMailDnsRecordRoute(
  request: Request,
  rawZoneId: string
): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  if (!mayManageMailboxes(target)) return forbidden();
  const zoneId = parseRouteId(rawZoneId);
  if (!zoneId.ok) return zoneId.response;
  const input = await dnsRecordBody(request, 'create');
  if (!input.ok) return input.response;
  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'POST',
      path: `mail-zones/${zoneId.value}/dns-records`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      body: input.value,
      fieldMap: dnsRecordFieldMap,
    });
  }
  return mockDnsRecordResponse(
    mockZoneStore.createRecord(
      target.organizationId,
      zoneId.value,
      input.value as MockDnsRecordCreate,
      mockDomainStore.inZone(target.organizationId, zoneId.value)
    ),
    201
  );
}

export async function updateMailDnsRecordRoute(
  request: Request,
  rawZoneId: string,
  rawRecordId: string
): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  if (!mayManageMailboxes(target)) return forbidden();
  const ids = parseDnsRecordIds(rawZoneId, rawRecordId);
  if (!ids.ok) return ids.response;
  const input = await dnsRecordBody(request, 'update');
  if (!input.ok) return input.response;
  const { zoneId, recordId } = ids.value;
  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'PATCH',
      path: `mail-zones/${zoneId}/dns-records/${recordId}`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      body: input.value,
      fieldMap: dnsRecordFieldMap,
    });
  }
  return mockDnsRecordResponse(
    mockZoneStore.updateRecord(
      target.organizationId,
      zoneId,
      recordId,
      input.value,
      mockDomainStore.inZone(target.organizationId, zoneId)
    )
  );
}

export async function deleteMailDnsRecordRoute(
  request: Request,
  rawZoneId: string,
  rawRecordId: string
): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  if (!mayManageMailboxes(target)) return forbidden();
  const ids = parseDnsRecordIds(rawZoneId, rawRecordId);
  if (!ids.ok) return ids.response;
  const { zoneId, recordId } = ids.value;
  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'DELETE',
      path: `mail-zones/${zoneId}/dns-records/${recordId}`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
    });
  }
  const result: MockDnsRecordRemoveResult = mockZoneStore.deleteRecord(
    target.organizationId,
    zoneId,
    recordId,
    mockDomainStore.inZone(target.organizationId, zoneId)
  );
  return result.ok ? apiNoContentResponse(privateMailboxHeaders()) : mockDnsRecordResponse(result);
}

const dnsRecordFieldMap = {
  type: 'type',
  name: 'name',
  content: 'content',
  ttl: 'ttl',
  priority: 'priority',
  proxied: 'proxied',
} as const;

function mockDnsRecordResponse(
  result: MockDnsRecordResult | (MockDnsRecordRemoveResult & { ok: false }),
  status: 200 | 201 = 200
): NextResponse {
  if (result.ok) return success(result.record, status);
  switch (result.error) {
    case 'zone_not_found':
      return mailZoneNotFound();
    case 'not_found':
      return failure(404, ApiErrorCode.MAILBOX_DNS_RECORD_NOT_FOUND, 'DNS record not found');
    case 'locked':
      return failure(
        409,
        ApiErrorCode.MAILBOX_DNS_RECORD_LOCKED,
        'Zmail mail relies on the record'
      );
    default:
      return apiErrorResponse({
        status: 422,
        errorCode: ApiErrorCode.MAILBOX_DNS_RECORD_INVALID,
        message: 'DNS record is invalid',
        errors: { [result.field]: [result.message] },
        headers: privateMailboxHeaders(),
      });
  }
}

function mailZoneNotFound(): NextResponse {
  return failure(404, ApiErrorCode.MAILBOX_ZONE_NOT_FOUND, 'Managed zone not found');
}

function parseDnsRecordIds(
  rawZoneId: string,
  rawRecordId: string
): Parsed<{ zoneId: number; recordId: string }> {
  const zoneId = parseRouteId(rawZoneId);
  if (!zoneId.ok) return zoneId;
  if (!MAIL_DNS_RECORD_ID_PATTERN.test(rawRecordId)) {
    return { ok: false, response: invalidInput('Invalid ID format') };
  }
  return { ok: true, value: { zoneId: zoneId.value, recordId: rawRecordId } };
}

/**
 * Shape checks only; the record rules belong to the API and, in development, to the mock. A
 * create needs `type`, `name`, and `content`; a change takes any of the fields.
 */
async function dnsRecordBody(
  request: Request,
  mode: 'create' | 'update'
): Promise<Parsed<MailDnsRecordPatch>> {
  const body = await jsonObject(request, maxDnsRecordBodyBytes);
  if (!body.ok) return body;
  const { type, name, content, ttl, priority, proxied } = body.value;
  const required = mode === 'create';
  // Field, value, whether a present value is well-formed, and whether an absent one is missing.
  const rules: Array<[keyof MailDnsRecordInput, unknown, boolean, boolean]> = [
    ['type', type, typeof type === 'string' && isOneOf(dnsRecordTypes, type), required],
    ['name', name, boundedString(name, MAIL_DNS_RECORD_NAME_MAX_LENGTH) && name !== '', required],
    [
      'content',
      content,
      boundedString(content, MAIL_DNS_RECORD_CONTENT_MAX_LENGTH) && content !== '',
      required,
    ],
    ['ttl', ttl, Number.isSafeInteger(ttl), false],
    ['priority', priority, Number.isSafeInteger(priority), false],
    ['proxied', proxied, typeof proxied === 'boolean', false],
  ];
  const errors: Record<string, string[]> = {};
  const value: Record<string, unknown> = {};
  for (const [field, item, valid, needed] of rules) {
    if (item === undefined ? needed : !valid) errors[field] = [`The ${field} field is invalid`];
    if (item !== undefined) value[field] = item;
  }
  if (Object.keys(errors).length > 0) return { ok: false, response: validationFailed(errors) };
  return { ok: true, value: value as MailDnsRecordPatch };
}

const mailDomainCapabilities: readonly MailDomainCapability[] = ['send_receive', 'send_only'];
const dnsRecordTypes: readonly MailDnsRecordType[] = ['A', 'AAAA', 'CNAME', 'TXT', 'MX'];

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
  return success(mockMailboxStore.list(target.organizationId, viewerOf(target)));
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
      fieldMap: {
        local_part: 'local_part',
        display_name: 'display_name',
        domain: 'domain',
        type: 'type',
        owner_user_id: 'owner_user_id',
        members: 'members',
      },
    });
  }
  const placement = mockDomainStore.resolve(target.organizationId, input.value.domain ?? '');
  if (!placement.ok) {
    return placement.error === 'not_verified'
      ? failure(422, ApiErrorCode.MAILBOX_DOMAIN_NOT_VERIFIED, 'Mail domain is not verified')
      : mailDomainNotFound();
  }
  const result = mockMailboxStore.create(
    target.organizationId,
    viewerOf(target),
    input.value,
    placement.name
  );
  if (result.ok) return success(result.mailbox, 201);
  if (result.error === 'access_invalid') return accessInvalid();
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
      organizationId: String(target.organizationId),
    });
  }
  // `administer`: no access at all is a missing mailbox, a member with access is refused.
  const result = mockMailboxStore.delete(target.organizationId, mailboxId.value, viewerOf(target));
  return result.ok ? apiNoContentResponse(privateMailboxHeaders()) : missing(result.error);
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
    viewerOf(target),
    direction.value,
    page,
    perPage
  );
  if (!result.ok) return missing(result.error);
  const { items, meta, links } = result.page;
  return apiPaginatedResponse(items, meta, links, privateMailboxHeaders());
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
  const result = mockMailboxStore.listLog(
    target.organizationId,
    viewerOf(target),
    filters.value,
    page,
    perPage
  );
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
  return success(mockMailboxStore.stats(target.organizationId, viewerOf(target), days.value));
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
    mockMailboxStore.send(target.organizationId, mailboxId.value, input.value, viewerOf(target))
  );
}

function mockSendResponse(result: MockMailSendResult): NextResponse {
  if (result.ok) return success(result.message, 201);
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

export async function listMailBroadcastsRoute(request: Request): Promise<NextResponse> {
  const target = await resolveMailboxReadRoute(request);
  if (!target.available) return target.response;
  const query = new URL(request.url).searchParams;
  const page = positiveInteger(query.get('page'), 1);
  const perPage = Math.min(
    positiveInteger(query.get('per_page'), defaultPerPage),
    MAIL_PAGE_MAX_SIZE
  );
  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'GET',
      path: 'mail-broadcasts',
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      searchParams: new URLSearchParams({ page: String(page), per_page: String(perPage) }),
    });
  }
  const result = mockBroadcastStore.list(target.organizationId, page, perPage);
  return apiPaginatedResponse(result.items, result.meta, result.links, privateMailboxHeaders());
}

export async function getMailBroadcastRoute(
  request: Request,
  rawBroadcastId: string
): Promise<NextResponse> {
  const target = await resolveMailboxReadRoute(request);
  if (!target.available) return target.response;
  const broadcastId = parseRouteId(rawBroadcastId);
  if (!broadcastId.ok) return broadcastId.response;
  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'GET',
      path: `mail-broadcasts/${broadcastId.value}`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
    });
  }
  return mockBroadcastResponse(mockBroadcastStore.get(target.organizationId, broadcastId.value));
}

export async function saveMailBroadcastRoute(
  request: Request,
  rawBroadcastId?: string
): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  let broadcastId: number | undefined;
  if (rawBroadcastId !== undefined) {
    const parsed = parseRouteId(rawBroadcastId);
    if (!parsed.ok) return parsed.response;
    broadcastId = parsed.value;
  }
  const input = await broadcastBody(request);
  if (!input.ok) return input.response;
  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: broadcastId === undefined ? 'POST' : 'PUT',
      path: broadcastId === undefined ? 'mail-broadcasts' : `mail-broadcasts/${broadcastId}`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      body: input.value,
      fieldMap: {
        name: 'name',
        mailbox_id: 'mailbox_id',
        template_id: 'template_id',
        list_id: 'list_id',
        unsubscribe_label: 'unsubscribe_label',
        test_percent: 'test_percent',
        test_wait_minutes: 'test_wait_minutes',
        variables: 'variables',
      },
    });
  }
  return mockBroadcastResponse(
    mockBroadcastStore.save(target.organizationId, input.value, broadcastId),
    broadcastId === undefined ? 201 : 200
  );
}

export async function deleteMailBroadcastRoute(
  request: Request,
  rawBroadcastId: string
): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  const broadcastId = parseRouteId(rawBroadcastId);
  if (!broadcastId.ok) return broadcastId.response;
  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'DELETE',
      path: `mail-broadcasts/${broadcastId.value}`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
    });
  }
  const result = mockBroadcastStore.remove(target.organizationId, broadcastId.value);
  if (result === 'ok') return privateMailboxResponse(apiNoContentResponse());
  return result === 'not_found' ? broadcastNotFound() : broadcastNotEditable();
}

export async function sendMailBroadcastRoute(
  request: Request,
  rawBroadcastId: string
): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  // Mail to a whole list: only owners and admins start it. With the API backend, the API decides.
  if (!mayManageMailboxes(target)) return forbidden();
  const broadcastId = parseRouteId(rawBroadcastId);
  if (!broadcastId.ok) return broadcastId.response;
  const body = await jsonObject(request, maxContactBodyBytes);
  if (!body.ok) return body.response;
  const { scheduled_at: scheduledAt, ...rest } = body.value;
  if (
    Object.keys(rest).length > 0 ||
    !(
      scheduledAt === undefined ||
      scheduledAt === null ||
      (typeof scheduledAt === 'string' &&
        scheduledAt.length <= 40 &&
        !Number.isNaN(Date.parse(scheduledAt)))
    )
  ) {
    return validationFailed({ scheduled_at: ['The scheduled_at field is invalid'] });
  }
  const at = typeof scheduledAt === 'string' ? new Date(scheduledAt).toISOString() : null;
  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'POST',
      path: `mail-broadcasts/${broadcastId.value}/sends`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      body: at === null ? {} : { scheduled_at: at },
      fieldMap: { scheduled_at: 'scheduled_at' },
    });
  }
  return mockBroadcastResponse(
    mockBroadcastStore.send(target.organizationId, broadcastId.value, at)
  );
}

/** Cancels, pauses, or resumes a broadcast; only owners and admins may, and none reads a body. */
export async function controlMailBroadcastRoute(
  request: Request,
  rawBroadcastId: string,
  action: 'cancellations' | 'pauses' | 'resumptions'
): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  if (!mayManageMailboxes(target)) return forbidden();
  const broadcastId = parseRouteId(rawBroadcastId);
  if (!broadcastId.ok) return broadcastId.response;
  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'POST',
      path: `mail-broadcasts/${broadcastId.value}/${action}`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
    });
  }
  const result =
    action === 'cancellations'
      ? mockBroadcastStore.cancel(target.organizationId, broadcastId.value)
      : action === 'pauses'
        ? mockBroadcastStore.pause(target.organizationId, broadcastId.value)
        : mockBroadcastStore.resume(target.organizationId, broadcastId.value);
  return mockBroadcastResponse(result);
}

export async function listMailBroadcastRecipientsRoute(
  request: Request,
  rawBroadcastId: string
): Promise<NextResponse> {
  const target = await resolveMailboxReadRoute(request);
  if (!target.available) return target.response;
  const broadcastId = parseRouteId(rawBroadcastId);
  if (!broadcastId.ok) return broadcastId.response;
  const query = new URL(request.url).searchParams;
  const status = query.get('status') ?? '';
  if (status !== '' && !isOneOf(recipientStatuses, status)) {
    return invalidInput('Invalid status filter');
  }
  const page = positiveInteger(query.get('page'), 1);
  const perPage = Math.min(
    positiveInteger(query.get('per_page'), defaultPerPage),
    MAIL_PAGE_MAX_SIZE
  );
  if (target.backend === 'go-api') {
    const searchParams = new URLSearchParams({ page: String(page), per_page: String(perPage) });
    if (status !== '') searchParams.set('status', status);
    return forwardAuthenticatedGoApi(request, {
      method: 'GET',
      path: `mail-broadcasts/${broadcastId.value}/recipients`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      searchParams,
    });
  }
  const result = mockBroadcastStore.recipients(
    target.organizationId,
    broadcastId.value,
    status === '' ? undefined : (status as MailRecipientStatus),
    page,
    perPage
  );
  if (!result) return broadcastNotFound();
  return apiPaginatedResponse(result.items, result.meta, result.links, privateMailboxHeaders());
}

const recipientStatuses: readonly MailRecipientStatus[] = [
  'pending',
  'sent',
  'failed',
  'skipped',
  'canceled',
];

function mockBroadcastResponse(result: MockBroadcastResult, status: 200 | 201 = 200): NextResponse {
  if (result.ok) return success(result.broadcast, status);
  switch (result.error) {
    case 'not_found':
      return broadcastNotFound();
    case 'not_editable':
      return broadcastNotEditable();
    case 'no_recipients':
      return failure(
        422,
        ApiErrorCode.MAILBOX_BROADCAST_NO_RECIPIENTS,
        'Mail broadcast has no subscribed recipients'
      );
    case 'mailbox_not_found':
      return mailboxNotFound();
    case 'template_not_found':
      return templateNotFound();
    case 'list_not_found':
      return contactListNotFound();
    default:
      return failure(422, ApiErrorCode.MAILBOX_BROADCAST_INVALID, 'Mail broadcast is invalid');
  }
}

function broadcastNotFound(): NextResponse {
  return failure(404, ApiErrorCode.MAILBOX_BROADCAST_NOT_FOUND, 'Mail broadcast not found');
}

function broadcastNotEditable(): NextResponse {
  return failure(
    409,
    ApiErrorCode.MAILBOX_BROADCAST_NOT_EDITABLE,
    'Mail broadcast cannot be changed in its current state'
  );
}

/** Shape checks only; references and states are judged by the API or the mock. */
async function broadcastBody(request: Request): Promise<Parsed<MailBroadcastInput>> {
  const body = await jsonObject(request, maxContactBodyBytes);
  if (!body.ok) return body;
  const {
    name,
    mailbox_id: mailboxId,
    template_id: templateId,
    list_id: listId,
    unsubscribe_label: label,
    test_percent: testPercent,
    test_wait_minutes: testWait,
    variables,
    ...rest
  } = body.value;
  const errors: Record<string, string[]> = {};
  const positiveId = (value: unknown): value is number =>
    typeof value === 'number' && Number.isSafeInteger(value) && value > 0;
  if (Object.keys(rest).length > 0) errors.body = ['Unknown fields'];
  if (!boundedString(name, MAIL_BROADCAST_NAME_MAX_LENGTH * 4) || name === '') {
    errors.name = ['The name field is invalid'];
  }
  if (!positiveId(mailboxId)) errors.mailbox_id = ['The mailbox_id field is invalid'];
  if (!positiveId(templateId)) errors.template_id = ['The template_id field is invalid'];
  if (!positiveId(listId)) errors.list_id = ['The list_id field is invalid'];
  if (label !== undefined && !boundedString(label, MAIL_BROADCAST_LABEL_MAX_LENGTH * 4)) {
    errors.unsubscribe_label = ['The unsubscribe_label field is invalid'];
  }
  const wholeNumber = (value: unknown, max: number): value is number =>
    typeof value === 'number' && Number.isInteger(value) && value >= 0 && value <= max;
  if (testPercent !== undefined && !wholeNumber(testPercent, MAIL_BROADCAST_TEST_MAX_PERCENT)) {
    errors.test_percent = ['The test_percent field is invalid'];
  }
  if (testWait !== undefined && !wholeNumber(testWait, MAIL_BROADCAST_TEST_WAIT_MAX_MINUTES)) {
    errors.test_wait_minutes = ['The test_wait_minutes field is invalid'];
  }
  if (
    variables !== undefined &&
    (!variableMap(variables) ||
      MAIL_BROADCAST_CONTACT_VARIABLES.some(field => Object.hasOwn(variables, field)))
  ) {
    errors.variables = ['The variables field is invalid'];
  }
  if (Object.keys(errors).length > 0) return { ok: false, response: validationFailed(errors) };
  return {
    ok: true,
    value: {
      name: name as string,
      mailbox_id: mailboxId as number,
      template_id: templateId as number,
      list_id: listId as number,
      ...(typeof label === 'string' ? { unsubscribe_label: label } : {}),
      ...(typeof testPercent === 'number' ? { test_percent: testPercent } : {}),
      ...(typeof testWait === 'number' ? { test_wait_minutes: testWait } : {}),
      ...(variableMap(variables) ? { variables } : {}),
    },
  };
}

export async function listMailContactsRoute(request: Request): Promise<NextResponse> {
  const target = await resolveMailboxReadRoute(request);
  if (!target.available) return target.response;
  const query = new URL(request.url).searchParams;
  const filters = parseContactFilters(query);
  if (!filters.ok) return filters.response;
  const page = positiveInteger(query.get('page'), 1);
  const perPage = Math.min(
    positiveInteger(query.get('per_page'), defaultPerPage),
    MAIL_PAGE_MAX_SIZE
  );
  if (target.backend === 'go-api') {
    const searchParams = new URLSearchParams();
    if (filters.value.q) searchParams.set('q', filters.value.q);
    if (filters.value.status) searchParams.set('status', filters.value.status);
    if (filters.value.listId !== undefined)
      searchParams.set('list_id', String(filters.value.listId));
    searchParams.set('page', String(page));
    searchParams.set('per_page', String(perPage));
    return forwardAuthenticatedGoApi(request, {
      method: 'GET',
      path: 'mail-contacts',
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      searchParams,
    });
  }
  const result = mockContactStore.list(target.organizationId, filters.value, page, perPage);
  return apiPaginatedResponse(result.items, result.meta, result.links, privateMailboxHeaders());
}

export async function createMailContactRoute(request: Request): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  const input = await contactBody(request);
  if (!input.ok) return input.response;
  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'POST',
      path: 'mail-contacts',
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      body: input.value,
      fieldMap: contactFieldMap,
    });
  }
  return mockContactResponse(mockContactStore.create(target.organizationId, input.value), 201);
}

export async function updateMailContactRoute(
  request: Request,
  rawContactId: string
): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  const contactId = parseRouteId(rawContactId);
  if (!contactId.ok) return contactId.response;
  const input = await contactBody(request);
  if (!input.ok) return input.response;
  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'PUT',
      path: `mail-contacts/${contactId.value}`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      body: input.value,
      fieldMap: contactFieldMap,
    });
  }
  return mockContactResponse(
    mockContactStore.update(target.organizationId, contactId.value, input.value)
  );
}

export async function deleteMailContactRoute(
  request: Request,
  rawContactId: string
): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  const contactId = parseRouteId(rawContactId);
  if (!contactId.ok) return contactId.response;
  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'DELETE',
      path: `mail-contacts/${contactId.value}`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
    });
  }
  return mockContactStore.remove(target.organizationId, contactId.value)
    ? privateMailboxResponse(apiNoContentResponse())
    : contactNotFound();
}

export async function importMailContactsRoute(request: Request): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  const input = await contactImportBody(request);
  if (!input.ok) return input.response;
  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'POST',
      path: 'mail-contact-imports',
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      body: input.value,
      fieldMap: { csv: 'csv', list_id: 'list_id' },
    });
  }
  const result = mockContactStore.import(target.organizationId, input.value);
  if (result.ok) return success(result.result);
  return result.error === 'list_not_found'
    ? contactListNotFound()
    : failure(422, ApiErrorCode.MAILBOX_CONTACT_IMPORT_INVALID, 'Mail contact import is invalid');
}

export async function listMailContactListsRoute(request: Request): Promise<NextResponse> {
  const target = await resolveMailboxReadRoute(request);
  if (!target.available) return target.response;
  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'GET',
      path: 'mail-contact-lists',
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
    });
  }
  return success(mockContactStore.lists(target.organizationId));
}

export async function saveMailContactListRoute(
  request: Request,
  rawListId?: string
): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  let listId: number | undefined;
  if (rawListId !== undefined) {
    const parsed = parseRouteId(rawListId);
    if (!parsed.ok) return parsed.response;
    listId = parsed.value;
  }
  const input = await contactListBody(request);
  if (!input.ok) return input.response;
  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: listId === undefined ? 'POST' : 'PUT',
      path: listId === undefined ? 'mail-contact-lists' : `mail-contact-lists/${listId}`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      body: input.value,
      fieldMap: { name: 'name' },
    });
  }
  const result = mockContactStore.saveList(target.organizationId, input.value.name, listId);
  if (result.ok) return success(result.list, listId === undefined ? 201 : 200);
  switch (result.error) {
    case 'not_found':
      return contactListNotFound();
    case 'taken':
      return failure(
        409,
        ApiErrorCode.MAILBOX_CONTACT_LIST_NAME_TAKEN,
        'Mail contact list name is taken'
      );
    case 'limit':
      return failure(
        409,
        ApiErrorCode.MAILBOX_CONTACT_LIST_LIMIT_REACHED,
        'Mail contact list limit reached'
      );
    default:
      return failure(422, ApiErrorCode.MAILBOX_CONTACT_INVALID, 'Mail contact list is invalid');
  }
}

export async function deleteMailContactListRoute(
  request: Request,
  rawListId: string
): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  const listId = parseRouteId(rawListId);
  if (!listId.ok) return listId.response;
  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'DELETE',
      path: `mail-contact-lists/${listId.value}`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
    });
  }
  return mockContactStore.removeList(target.organizationId, listId.value)
    ? privateMailboxResponse(apiNoContentResponse())
    : contactListNotFound();
}

const contactFieldMap = {
  email: 'email',
  first_name: 'first_name',
  last_name: 'last_name',
  properties: 'properties',
  status: 'status',
  list_ids: 'list_ids',
} as const;

function mockContactResponse(result: MockContactResult, status: 200 | 201 = 200): NextResponse {
  if (result.ok) return success(result.contact, status);
  switch (result.error) {
    case 'not_found':
      return contactNotFound();
    case 'exists':
      return failure(409, ApiErrorCode.MAILBOX_CONTACT_EXISTS, 'Mail contact already exists');
    case 'list_not_found':
      return contactListNotFound();
    default:
      return failure(422, ApiErrorCode.MAILBOX_CONTACT_INVALID, 'Mail contact is invalid');
  }
}

function contactNotFound(): NextResponse {
  return failure(404, ApiErrorCode.MAILBOX_CONTACT_NOT_FOUND, 'Mail contact not found');
}

function contactListNotFound(): NextResponse {
  return failure(404, ApiErrorCode.MAILBOX_CONTACT_LIST_NOT_FOUND, 'Mail contact list not found');
}

function parseContactFilters(query: URLSearchParams): Parsed<MailContactFilters> {
  for (const name of ['q', 'status', 'list_id']) {
    if (query.getAll(name).length > 1)
      return { ok: false, response: invalidInput('Repeated filter') };
  }
  const q = query.get('q')?.trim() ?? '';
  const status = query.get('status') ?? '';
  const rawListId = query.get('list_id');
  if (Array.from(q).length > MAIL_LOG_QUERY_MAX_LENGTH) {
    return { ok: false, response: invalidInput('Query is too long') };
  }
  if (status !== '' && !isOneOf(contactStatuses, status)) {
    return { ok: false, response: invalidInput('Invalid status filter') };
  }
  let listId: number | undefined;
  if (rawListId !== null) {
    const parsed = parseRouteId(rawListId);
    if (!parsed.ok) return parsed;
    listId = parsed.value;
  }
  return {
    ok: true,
    value: {
      ...(q === '' ? {} : { q }),
      ...(status === '' ? {} : { status: status as MailContactStatus }),
      ...(listId === undefined ? {} : { listId }),
    },
  };
}

/** Shape checks only; addresses, names, and properties are judged by the API or the mock. */
async function contactBody(request: Request): Promise<Parsed<MailContactInput>> {
  const body = await jsonObject(request, maxContactBodyBytes);
  if (!body.ok) return body;
  const {
    email,
    first_name: firstName,
    last_name: lastName,
    properties,
    status,
    list_ids: listIds,
    ...rest
  } = body.value;
  const errors: Record<string, string[]> = {};
  if (Object.keys(rest).length > 0) errors.body = ['Unknown fields'];
  if (email !== undefined && !boundedString(email, MAIL_ADDRESS_MAX_LENGTH)) {
    errors.email = ['The email field is invalid'];
  }
  if (firstName !== undefined && !boundedString(firstName, 400)) {
    errors.first_name = ['The first_name field is invalid'];
  }
  if (lastName !== undefined && !boundedString(lastName, 400)) {
    errors.last_name = ['The last_name field is invalid'];
  }
  if (properties !== undefined && !propertyMap(properties)) {
    errors.properties = ['The properties field is invalid'];
  }
  if (status !== undefined && status !== 'subscribed' && status !== 'unsubscribed') {
    errors.status = ['The status field is invalid'];
  }
  if (
    listIds !== undefined &&
    !(
      Array.isArray(listIds) &&
      listIds.length <= MAIL_CONTACT_MAX_LISTS &&
      listIds.every(id => typeof id === 'number' && Number.isSafeInteger(id) && id > 0)
    )
  ) {
    errors.list_ids = ['The list_ids field is invalid'];
  }
  if (Object.keys(errors).length > 0) return { ok: false, response: validationFailed(errors) };
  return {
    ok: true,
    value: {
      ...(typeof email === 'string' ? { email } : {}),
      ...(typeof firstName === 'string' ? { first_name: firstName } : {}),
      ...(typeof lastName === 'string' ? { last_name: lastName } : {}),
      ...(propertyMap(properties) ? { properties } : {}),
      ...(status === 'subscribed' || status === 'unsubscribed' ? { status } : {}),
      ...(Array.isArray(listIds) ? { list_ids: listIds as number[] } : {}),
    },
  };
}

function propertyMap(value: unknown): value is Record<string, string> {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) return false;
  const entries = Object.entries(value);
  return (
    entries.length <= MAIL_CONTACT_MAX_PROPERTIES &&
    entries.every(([name, item]) => name.length <= 64 && boundedString(item, 2_000))
  );
}

async function contactImportBody(request: Request): Promise<Parsed<MailContactImportInput>> {
  const body = await jsonObject(request, maxContactImportBodyBytes);
  if (!body.ok) return body;
  const { csv, list_id: listId, ...rest } = body.value;
  const errors: Record<string, string[]> = {};
  if (Object.keys(rest).length > 0) errors.body = ['Unknown fields'];
  if (typeof csv !== 'string' || csv === '') errors.csv = ['The csv field is invalid'];
  if (
    listId !== undefined &&
    !(typeof listId === 'number' && Number.isSafeInteger(listId) && listId > 0)
  ) {
    errors.list_id = ['The list_id field is invalid'];
  }
  if (Object.keys(errors).length > 0) return { ok: false, response: validationFailed(errors) };
  return {
    ok: true,
    value: { csv: csv as string, ...(typeof listId === 'number' ? { list_id: listId } : {}) },
  };
}

async function contactListBody(request: Request): Promise<Parsed<{ name: string }>> {
  const body = await jsonObject(request, maxContactBodyBytes);
  if (!body.ok) return body;
  const { name, ...rest } = body.value;
  if (
    Object.keys(rest).length > 0 ||
    !boundedString(name, MAIL_CONTACT_LIST_NAME_MAX_LENGTH * 4) ||
    name === ''
  ) {
    return { ok: false, response: validationFailed({ name: ['The name field is invalid'] }) };
  }
  return { ok: true, value: { name } };
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
      input.value.variables ?? {},
      viewerOf(target)
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
    ids.value.messageId,
    viewerOf(target)
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
    attachmentId.value,
    viewerOf(target)
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
    viewerOf(target),
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
    ids.value.messageId,
    viewerOf(target)
  );
  return result.ok ? apiNoContentResponse(privateMailboxHeaders()) : missing(result.error);
}

/** Members of a shared mailbox; needs `manage_members`. */
export async function listMailboxMembersRoute(
  request: Request,
  rawMailboxId: string
): Promise<NextResponse> {
  const target = await resolveMailboxReadRoute(request);
  if (!target.available) return target.response;
  const mailboxId = parseRouteId(rawMailboxId);
  if (!mailboxId.ok) return mailboxId.response;

  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'GET',
      path: `mailboxes/${mailboxId.value}/members`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
    });
  }
  return accessResponse(
    mockMailboxStore.members(target.organizationId, mailboxId.value, viewerOf(target))
  );
}

export async function setMailboxMemberRoute(
  request: Request,
  rawMailboxId: string,
  rawUserId: string
): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  const mailboxId = parseRouteId(rawMailboxId);
  if (!mailboxId.ok) return mailboxId.response;
  const userId = parseRouteId(rawUserId);
  if (!userId.ok) return userId.response;
  const body = await jsonObject(request, maxAccessBodyBytes);
  if (!body.ok) return body.response;
  const { permission } = body.value;
  // The permission's value is judged by the API, which answers `MAILBOX.ACCESS_INVALID`.
  if (!boundedString(permission, 32)) {
    return validationFailed({ permission: ['The permission field is required'] });
  }

  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'PUT',
      path: `mailboxes/${mailboxId.value}/members/${userId.value}`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      body: { permission },
      fieldMap: { permission: 'permission' },
    });
  }
  return accessResponse(
    mockMailboxStore.setMember(
      target.organizationId,
      mailboxId.value,
      viewerOf(target),
      userId.value,
      permission
    )
  );
}

export async function removeMailboxMemberRoute(
  request: Request,
  rawMailboxId: string,
  rawUserId: string
): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  const mailboxId = parseRouteId(rawMailboxId);
  if (!mailboxId.ok) return mailboxId.response;
  const userId = parseRouteId(rawUserId);
  if (!userId.ok) return userId.response;

  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'DELETE',
      path: `mailboxes/${mailboxId.value}/members/${userId.value}`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
    });
  }
  const result = mockMailboxStore.removeMember(
    target.organizationId,
    mailboxId.value,
    viewerOf(target),
    userId.value
  );
  return result.ok ? apiNoContentResponse(privateMailboxHeaders()) : accessFailure(result.error);
}

/** The access history, newest first; needs `see_history`. */
export async function listMailboxAccessEventsRoute(
  request: Request,
  rawMailboxId: string
): Promise<NextResponse> {
  const target = await resolveMailboxReadRoute(request);
  if (!target.available) return target.response;
  const mailboxId = parseRouteId(rawMailboxId);
  if (!mailboxId.ok) return mailboxId.response;
  const query = new URL(request.url).searchParams;
  const page = positiveInteger(query.get('page'), 1);
  const perPage = Math.min(
    positiveInteger(query.get('per_page'), defaultPerPage),
    MAILBOX_ACCESS_EVENTS_MAX_PAGE_SIZE
  );

  if (target.backend === 'go-api') {
    // Only the page parameters reach the API; anything else in the query is dropped.
    return forwardAuthenticatedGoApi(request, {
      method: 'GET',
      path: `mailboxes/${mailboxId.value}/access-events`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      searchParams: new URLSearchParams({ page: String(page), per_page: String(perPage) }),
    });
  }
  const result = mockMailboxStore.accessEvents(
    target.organizationId,
    mailboxId.value,
    viewerOf(target),
    page,
    perPage
  );
  if (!result.ok) return accessFailure(result.error);
  const { items, meta, links } = result.value;
  return apiPaginatedResponse(items, meta, links, privateMailboxHeaders());
}

/** Turns a personal mailbox into a shared one; owners and admins only. */
export async function handOverMailboxRoute(
  request: Request,
  rawMailboxId: string
): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  const mailboxId = parseRouteId(rawMailboxId);
  if (!mailboxId.ok) return mailboxId.response;
  const body = await jsonObject(request, maxAccessBodyBytes);
  if (!body.ok) return body.response;
  const { successor_user_id: successor } = body.value;
  if (typeof successor !== 'number' || !Number.isSafeInteger(successor) || successor < 1) {
    return validationFailed({ successor_user_id: ['The successor_user_id field is invalid'] });
  }

  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'POST',
      path: `mailboxes/${mailboxId.value}/handovers`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      body: { successor_user_id: successor },
      fieldMap: { successor_user_id: 'successor_user_id' },
    });
  }
  return accessResponse(
    mockMailboxStore.handOver(target.organizationId, mailboxId.value, viewerOf(target), successor),
    201
  );
}

/** Deletes the sealed history for good; the body repeats the mailbox address. */
export async function deleteMailboxSealedMessagesRoute(
  request: Request,
  rawMailboxId: string
): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  const mailboxId = parseRouteId(rawMailboxId);
  if (!mailboxId.ok) return mailboxId.response;
  const body = await jsonObject(request, maxAccessBodyBytes);
  if (!body.ok) return body.response;
  const { confirm_address: confirmAddress } = body.value;
  if (!boundedString(confirmAddress, MAIL_ADDRESS_MAX_LENGTH)) {
    return validationFailed({ confirm_address: ['The confirm_address field is required'] });
  }

  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'DELETE',
      path: `mailboxes/${mailboxId.value}/sealed-messages`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      body: { confirm_address: confirmAddress },
      fieldMap: { confirm_address: 'confirm_address' },
    });
  }
  return accessResponse(
    mockMailboxStore.deleteSealedHistory(
      target.organizationId,
      mailboxId.value,
      viewerOf(target),
      confirmAddress
    )
  );
}

/** Turns organization webhooks for a shared mailbox on or off; needs `manage_members`. */
export async function setMailboxWebhookSettingRoute(
  request: Request,
  rawMailboxId: string
): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  const mailboxId = parseRouteId(rawMailboxId);
  if (!mailboxId.ok) return mailboxId.response;
  const body = await jsonObject(request, maxAccessBodyBytes);
  if (!body.ok) return body.response;
  const { enabled } = body.value;
  if (typeof enabled !== 'boolean') {
    return validationFailed({ enabled: ['The enabled field is required'] });
  }

  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'PUT',
      path: `mailboxes/${mailboxId.value}/webhook-setting`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      body: { enabled },
      fieldMap: { enabled: 'enabled' },
    });
  }
  return accessResponse(
    mockMailboxStore.setWebhooks(target.organizationId, mailboxId.value, viewerOf(target), enabled)
  );
}

function accessResponse<T>(result: MockAccessResult<T>, status: 200 | 201 = 200): NextResponse {
  return result.ok ? success(result.value, status) : accessFailure(result.error);
}

function accessFailure(error: MockAccessError): NextResponse {
  switch (error) {
    case 'conflict':
      return failure(409, ApiErrorCode.MAILBOX_ACCESS_CONFLICT, 'Mailbox access changed');
    case 'invalid':
      return accessInvalid();
    case 'member_not_found':
      return mailboxNotFound();
    default:
      return missing(error);
  }
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

/** Only owners and admins create mailboxes. With the API backend, the API decides. */
function mayManageMailboxes(target: MailboxTarget & { available: true }): boolean {
  return target.backend === 'go-api' || target.context.role !== 'member';
}

/** The caller of a mock request, as the store judges access. */
function viewerOf(target: MailboxTarget & { available: true; backend: 'mock' }): MailboxViewer {
  return { userId: target.context.user_id, role: target.context.role };
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
async function mailboxBody(
  request: Request
): Promise<Parsed<Omit<CreateMailboxInput, 'type'> & { type?: CreateMailboxInput['type'] }>> {
  const body = await jsonObject(request, maxMailboxBodyBytes);
  if (!body.ok) return body;
  const {
    local_part: localPart,
    display_name: displayName,
    domain,
    type,
    owner_user_id: ownerUserId,
    members,
  } = body.value;
  const errors: Record<string, string[]> = {};
  // Shapes only: a missing or unknown type and invalid owners or members are judged by the API,
  // which answers `MAILBOX.ACCESS_INVALID`.
  if (type !== undefined && !boundedString(type, 32)) {
    errors.type = ['The type field is invalid'];
  }
  if (ownerUserId !== undefined && !positiveId(ownerUserId)) {
    errors.owner_user_id = ['The owner_user_id field is invalid'];
  }
  if (members !== undefined && !memberList(members)) {
    errors.members = ['The members field is invalid'];
  }
  if (domain !== undefined && !boundedString(domain, MAIL_DOMAIN_NAME_MAX_LENGTH)) {
    errors.domain = ['The domain field is invalid'];
  }
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
      ...(typeof domain === 'string' && domain !== '' ? { domain } : {}),
      ...(typeof type === 'string' ? { type: type as CreateMailboxInput['type'] } : {}),
      ...(positiveId(ownerUserId) ? { owner_user_id: ownerUserId } : {}),
      ...(memberList(members)
        ? {
            members: members.map(member => ({
              user_id: member.user_id,
              permission: member.permission as MailboxPermission,
            })),
          }
        : {}),
    },
  };
}

function positiveId(value: unknown): value is number {
  return typeof value === 'number' && Number.isSafeInteger(value) && value > 0;
}

function memberList(value: unknown): value is { user_id: number; permission: string }[] {
  return (
    Array.isArray(value) &&
    value.every(
      item =>
        typeof item === 'object' &&
        item !== null &&
        positiveId((item as Record<string, unknown>).user_id) &&
        boundedString((item as Record<string, unknown>).permission, 32)
    )
  );
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
  const { name, subject, html, text, sample_variables: samples, design, ...rest } = body;
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
  if (design !== undefined && design !== null && !templateDesign(design)) {
    errors.design = ['The design field is invalid'];
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
      ...(templateDesign(design) ? { design } : {}),
    },
  };
}

/** A design document as the contract describes it: an object with a positive integer version. */
function templateDesign(value: unknown): value is { version: number; [key: string]: unknown } {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) return false;
  const { version } = value as { version?: unknown };
  if (typeof version !== 'number' || !Number.isInteger(version) || version < 1) return false;
  return new TextEncoder().encode(JSON.stringify(value)).length <= MAIL_TEMPLATE_DESIGN_MAX_BYTES;
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

function missing(error: 'mailbox_not_found' | 'forbidden' | 'not_found'): NextResponse {
  if (error === 'mailbox_not_found') return mailboxNotFound();
  if (error === 'forbidden') return accessForbidden();
  return failure(404, ApiErrorCode.MAILBOX_MESSAGE_NOT_FOUND, 'Mail message not found');
}

/** Some access to the mailbox, but not what the route needs. */
function accessForbidden(): NextResponse {
  return failure(403, ApiErrorCode.PERMISSION_DENIED, 'Mailbox access forbidden');
}

function accessInvalid(): NextResponse {
  return failure(422, ApiErrorCode.MAILBOX_ACCESS_INVALID, 'Mailbox access is invalid');
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
