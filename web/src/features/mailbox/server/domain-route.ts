import 'server-only';

import { NextResponse } from 'next/server';
import { apiErrorResponse } from '@/app/api/_shared/error-response';
import { apiNoContentResponse } from '@/app/api/_shared/success-response';
import {
  MAIL_DOMAIN_NAME_MAX_LENGTH,
  MAIL_DNS_RECORD_CONTENT_MAX_LENGTH,
  MAIL_DNS_RECORD_ID_PATTERN,
  MAIL_DNS_RECORD_NAME_MAX_LENGTH,
} from '@/features/mailbox/schemas';
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
import { mockMailboxStore } from '@/features/mailbox/server/mock-mailbox-store';
import type {
  MailDomainCapability,
  MailDomainInput,
  MailDnsRecordInput,
  MailDnsRecordPatch,
  MailDnsRecordType,
} from '@/features/mailbox/types';
import { ApiErrorCode } from '@/http/codes';
import { forwardAuthenticatedGoApi } from '@/server/api-adapter/authenticated-route';
import {
  type MailboxTarget,
  type Parsed,
  maxMailboxBodyBytes,
  resolveMailboxReadRoute,
  resolveMailboxMutationRoute,
  mayManageMailboxes,
  jsonObject,
  boundedString,
  parseRouteId,
  isOneOf,
  privateMailboxHeaders,
  success,
  failure,
  mailDomainNotFound,
  invalidInput,
  validationFailed,
  forbidden,
} from './route-shared';

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
  return success(
    mockMailboxStore.domains(
      mockDomainStore.verifiedNames(target.organizationId),
      mockDomainStore.defaultName(target.organizationId)
    )
  );
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

/** Makes a verified domain the organization's default; reads no body. */
export async function setDefaultMailDomainRoute(
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
      method: 'PUT',
      path: `mail-domains/${domainId.value}/default`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
    });
  }
  return mockDomainResponse(mockDomainStore.setDefault(target.organizationId, domainId.value));
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
    case 'not_verified':
      return failure(422, ApiErrorCode.MAILBOX_DOMAIN_NOT_VERIFIED, 'Mail domain is not verified');
    default:
      return failure(422, ApiErrorCode.MAILBOX_DOMAIN_INVALID, 'Mail domain name is invalid');
  }
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

const mailDomainCapabilities: readonly MailDomainCapability[] = ['send_receive', 'send_only'];

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

const dnsRecordTypes: readonly MailDnsRecordType[] = ['A', 'AAAA', 'CNAME', 'TXT', 'MX'];

// A 4096-character record content with JSON escaping.
const maxDnsRecordBodyBytes = 32 * 1_024;

/** Fixed routes only. Organization and upstream credentials come from the signed-in session. */
export async function getCloudflareConnectionRoute(request: Request) {
  const target = await resolveMailboxReadRoute(request);
  return forwardCloudflareManagement(request, target, 'status');
}

export async function changeCloudflareConnectionRoute(
  request: Request,
  action: 'preview' | 'bind' | 'disconnect' | 'check' | 'domain',
  id?: string
) {
  const target = await resolveMailboxMutationRoute(request);
  return forwardCloudflareManagement(request, target, action, id);
}

async function forwardCloudflareManagement(
  request: Request,
  target: MailboxTarget,
  action: 'status' | 'preview' | 'bind' | 'disconnect' | 'check' | 'domain',
  id?: string
) {
  if (!target.available) return target.response;
  if (!mayManageMailboxes(target)) return forbidden();
  if (target.backend !== 'go-api')
    return failure(
      503,
      ApiErrorCode.COMMON_SERVICE_UNAVAILABLE,
      'Cloudflare connections require the API backend.'
    );
  if (
    (action === 'disconnect' || action === 'check' || action === 'domain') &&
    !/^[a-f0-9]{8}(-[a-f0-9]{4}){3}-[a-f0-9]{12}$/i.test(id ?? '')
  )
    return validationFailed({ id: ['Invalid connection'] });
  const path =
    action === 'preview'
      ? 'mail-cloudflare-previews'
      : 'mail-cloudflare-connection' +
        (id ? '/' + id : '') +
        (action === 'domain' ? '/domains' : action === 'check' ? '/checks' : '');
  const method = action === 'status' ? 'GET' : action === 'disconnect' ? 'DELETE' : 'POST';
  let body;
  if (method === 'POST') {
    const parsed = await jsonObject(request, 2048);
    if (!parsed.ok) return parsed.response;
    body = parsed.value;
  }
  return forwardAuthenticatedGoApi(request, {
    method,
    path,
    body,
    accessToken: target.accessToken,
    organizationId: String(target.organizationId),
  });
}
