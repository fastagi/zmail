import 'server-only';

import { NextResponse } from 'next/server';

import { apiErrorResponse, apiJsonBodyErrorResponse } from '@/app/api/_shared/error-response';
import { readJsonBody } from '@/app/api/_shared/json-body';
import { guardSameOriginMutation } from '@/app/api/_shared/mock-bff';
import {
  apiCursorPageResponse,
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
  INBOX_TIME_ZONE_MAX_LENGTH,
  INBOX_ASSIGNMENTS,
  INBOX_VIEWS,
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
  MAILBOX_DIRECTORY_CURSOR_MAX_LENGTH,
  MAILBOX_DIRECTORY_DEFAULT_PAGE_SIZE,
  MAILBOX_DIRECTORY_MAX_PAGE_SIZE,
  MAILBOX_DIRECTORY_QUERY_MAX_LENGTH,
  MAILBOX_EXPORT_MAX_BYTES,
  MAILBOX_IMPORT_ADDRESS_MAX_LENGTH,
  MAILBOX_IMPORT_DISPLAY_NAME_MAX_LENGTH,
  MAILBOX_IMPORT_MAX_ROWS,
  MAILBOX_IMPORT_MEMBERS_MAX_LENGTH,
  MAILBOX_LIST_SCOPES,
  MAILBOX_MAX_PER_ORGANIZATION,
  MAIL_ATTACHMENT_NAME_MAX_LENGTH,
  MAIL_ATTACHMENT_TYPE_MAX_LENGTH,
  MAIL_MAX_ATTACHMENTS,
  MAIL_BODY_MAX_BYTES,
  MAIL_TEMPLATE_DESIGN_MAX_BYTES,
  MAIL_LOG_QUERY_MAX_LENGTH,
  MAIL_MAX_RECIPIENTS,
  MAIL_PLACEMENTS,
  MAIL_SEARCH_MAX_LENGTH,
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
  MAIL_TRIAGE_CATEGORIES,
} from '@/features/mailbox/schemas';
import {
  MAIL_TRIAGE_DAILY_CAP_MAX,
  MAIL_TRIAGE_OVERRIDE_CATEGORIES,
  type MailTriageOverrideCategory,
} from '@/features/mailbox/triage-schemas';
import {
  PLATFORM_ACCOUNT_PAGE_MAX_SIZE,
  PLATFORM_ACCOUNT_QUERY_MAX_LENGTH,
  PLATFORM_ACCOUNT_STATUSES,
  type PlatformAccountFilters,
  type PlatformAccountInput,
  type PlatformAccountStatus,
} from '@/features/mailbox/platform-account-schemas';
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
import { decodeInboxCursor, encodeInboxCursor } from '@/features/mailbox/server/inbox-cursor';
import {
  MAIL_SIGNATURE_MAX_LENGTH,
  normalizeMailSignature,
} from '@/features/mailbox/signature-schemas';
import { mockAssignmentStore } from '@/features/mailbox/server/mock-assignment-store';
import { mockNoteStore } from '@/features/mailbox/server/mock-note-store';
import { mockSignatureStore } from '@/features/mailbox/server/mock-signature-store';
import { mockDraftStore } from '@/features/mailbox/server/mock-draft-store';
import { MAIL_DRAFT_LIMITS, draftRecipientLine } from '@/features/mailbox/draft-schemas';
import { mockTriageStore } from '@/features/mailbox/server/mock-triage-store';
import { mailAgentGrantInput } from '@/features/mailbox/server/agent-grant-input';
import {
  mockAgentGrantStore,
  mockAgentPage,
  type MockAgentGrantError,
} from '@/features/mailbox/server/mock-agent-grant-store';
import { mockApiKeyStore } from '@/features/api-key/server/mock-api-key-store';
import {
  canManageAgentGrants,
  needsOwnMembership,
} from '@/features/mailbox/utils/agent-grant-access';
import {
  mockLedgerPage,
  mockPlatformAccountStore,
  type MockLedgerResult,
} from '@/features/mailbox/server/mock-platform-account-store';
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
  MOCK_DAILY_SEND_LIMIT,
  mockMailboxStore,
  type MailboxViewer,
  type MockDirectoryQuery,
  type MockInboxQuery,
  type MockAccessError,
  type MockAccessResult,
  type MockMailRoleInput,
  type MockMailSendResult,
  type MockMailTemplateResult,
  type MockMessageChange,
  type MockOrganizeError,
  type MockOrganizeResult,
} from '@/features/mailbox/server/mock-mailbox-store';
import { mockOrganizationStore } from '@/features/organization/server/mock-organization-store';
import { parseOrganizationId } from '@/features/organization/server/organization-route';
import type { OrganizationContext } from '@/features/organization/types';
import type {
  CreateMailboxInput,
  Mailbox,
  InboxAssignment,
  InboxView,
  MailDirection,
  MailLogFilters,
  MailMessageFilters,
  MailPlacement,
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
  MailboxImportRowInput,
  MailboxListScope,
  MailboxPermission,
  MailTemplateInput,
  MailTemplateKind,
  MailTemplateTestInput,
  MailTriageCategory,
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
// 500 rows at their longest cells, with JSON escaping.
const maxMailboxImportBodyBytes = 4 * 1_024 * 1_024;
const maxMessageChangeBodyBytes = 16 * 1_024;
const maxOrganizeBodyBytes = 4 * 1_024;
// A 4096-character record content with JSON escaping.
const maxDnsRecordBodyBytes = 32 * 1_024;
const maxAccessBodyBytes = 1_024;
/** 2000 characters of up to four UTF-8 bytes, JSON-escaped, with room for the envelope. */
const maxSignatureBodyBytes = 16_384;
// Twenty filters of each kind at their longest, with JSON escaping.
const maxAgentGrantBodyBytes = 16 * 1_024;
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
  const rawScope = new URL(request.url).searchParams.get('scope') ?? '';
  if (rawScope !== '' && !(MAILBOX_LIST_SCOPES as readonly string[]).includes(rawScope)) {
    return validationFailed({ scope: ['The scope field is invalid'] });
  }
  const scope = rawScope === '' ? undefined : (rawScope as MailboxListScope);

  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'GET',
      path: 'mailboxes',
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      ...(scope ? { searchParams: new URLSearchParams({ scope }) } : {}),
    });
  }
  return success(mockMailboxStore.list(target.organizationId, viewerOf(target), scope));
}

/**
 * One page of the administration directory; owners and admins only. Only `type`, `q`, `cursor`,
 * and `per_page` reach the API, checked here as the API checks them.
 */
export async function listMailboxDirectoryRoute(request: Request): Promise<NextResponse> {
  const target = await resolveMailboxReadRoute(request);
  if (!target.available) return target.response;
  if (!mayManageMailboxes(target)) return forbidden();
  const query = parseDirectoryQuery(new URL(request.url).searchParams);
  if (!query.ok) return query.response;

  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'GET',
      path: 'mailbox-directory',
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      searchParams: query.value.forward,
    });
  }
  if (query.value.mock.beforeId === 0) return invalidInput('Invalid directory cursor');
  const page = mockMailboxStore.directory(
    target.organizationId,
    viewerOf(target),
    query.value.mock
  );
  if (!page.ok) return forbidden();
  const { items, nextBeforeId, mailboxCount, sentLast24h } = page.value;
  return NextResponse.json(
    {
      code: 0,
      message: 'success',
      data: items,
      meta: {
        per_page: query.value.mock.perPage,
        has_more: nextBeforeId !== null,
        next_cursor: nextBeforeId === null ? null : encodeDirectoryCursor(nextBeforeId),
        daily_send_limit: MOCK_DAILY_SEND_LIMIT,
        sent_last_24h: sentLast24h,
        mailbox_count: mailboxCount,
        max_mailboxes: MAILBOX_MAX_PER_ORGANIZATION,
      },
    },
    { headers: privateMailboxHeaders() }
  );
}

/** Renames a mailbox; owners and admins only, refused before the body is read. */
export async function updateMailboxRoute(
  request: Request,
  rawMailboxId: string
): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  if (!mayManageMailboxes(target)) return forbidden();
  const mailboxId = parseRouteId(rawMailboxId);
  if (!mailboxId.ok) return mailboxId.response;
  const body = await jsonObject(request, maxMailboxBodyBytes);
  if (!body.ok) return body.response;
  const { display_name: displayName } = body.value;
  if (!boundedString(displayName, MAILBOX_IMPORT_DISPLAY_NAME_MAX_LENGTH)) {
    return validationFailed({ display_name: ['The display_name field is invalid'] });
  }

  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'PUT',
      path: `mailboxes/${mailboxId.value}`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      body: { display_name: displayName },
      fieldMap: { display_name: 'display_name' },
    });
  }
  const result = mockMailboxStore.rename(
    target.organizationId,
    mailboxId.value,
    viewerOf(target),
    displayName
  );
  if (result.ok) return success(result.mailbox);
  if (result.error === 'forbidden') return forbidden();
  if (result.error === 'mailbox_not_found') return mailboxNotFound();
  return failure(422, ApiErrorCode.MAILBOX_ADDRESS_INVALID, 'Mailbox display name is invalid');
}

/** Checks or creates shared mailboxes from spreadsheet rows; owners and admins only. */
export async function importMailboxesRoute(request: Request): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  if (!mayManageMailboxes(target)) return forbidden();
  const body = await jsonObject(request, maxMailboxImportBodyBytes);
  if (!body.ok) return body.response;
  const input = mailboxImportBody(body.value);
  if (!input.ok) return input.response;

  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'POST',
      path: 'mailbox-imports',
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      body: input.value,
      fieldMap: { dry_run: 'dry_run', rows: 'rows' },
    });
  }
  const organizationId = target.organizationId;
  const result = mockMailboxStore.importRows(
    organizationId,
    viewerOf(target),
    input.value.rows,
    input.value.dry_run,
    name => mockDomainStore.resolve(organizationId, name)
  );
  return result.ok ? success(result.value) : forbidden();
}

/**
 * The CSV file of every mailbox of one type; owners and admins only. An API file is passed on as
 * an opaque download with its attachment name.
 */
export async function exportMailboxesRoute(request: Request): Promise<NextResponse> {
  const target = await resolveMailboxReadRoute(request);
  if (!target.available) return target.response;
  if (!mayManageMailboxes(target)) return forbidden();
  const type = new URL(request.url).searchParams.get('type');
  if (type !== 'shared' && type !== 'personal') {
    return validationFailed({ type: ['The type field is invalid'] });
  }

  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'GET',
      path: 'mailbox-exports',
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      searchParams: new URLSearchParams({ type }),
      responseType: 'binary',
      maxResponseBytes: MAILBOX_EXPORT_MAX_BYTES,
    });
  }
  const file = mockMailboxStore.exportCsv(target.organizationId, viewerOf(target), type);
  if (!file.ok) return forbidden();
  const headers = privateMailboxHeaders();
  headers.set('content-type', 'application/octet-stream');
  headers.set('x-content-type-options', 'nosniff');
  headers.set('content-security-policy', "default-src 'none'; sandbox");
  headers.set('content-disposition', `attachment; filename="${file.filename}"`);
  return new NextResponse(new TextEncoder().encode(file.csv), { status: 200, headers });
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
  if (!result.ok) return missing(result.error);
  mockAgentGrantStore.removeMailbox(target.organizationId, mailboxId.value);
  return apiNoContentResponse(privateMailboxHeaders());
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
  const filters = parseMessageFilters(query);
  if (!filters.ok) return filters.response;
  const page = positiveInteger(query.get('page'), 1);
  const perPage = Math.min(
    positiveInteger(query.get('per_page'), defaultPerPage),
    MAIL_PAGE_MAX_SIZE
  );

  if (target.backend === 'go-api') {
    // Only the documented parameters reach the API; anything else in the query is dropped.
    const searchParams = new URLSearchParams({ page: String(page), per_page: String(perPage) });
    for (const [name, value] of messageFilterParams(filters.value)) searchParams.set(name, value);
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
    filters.value,
    page,
    perPage
  );
  if (!result.ok) return organizeFailure(result);
  const { items, meta, links } = result.value;
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

/**
 * The unified inbox: one cursor page across the mailboxes the caller reads. The query is checked
 * here as the API checks it, so a malformed one never leaves the BFF, and only the nine documented
 * parameters reach the API.
 */
export async function listInboxMessagesRoute(request: Request): Promise<NextResponse> {
  const target = await resolveMailboxReadRoute(request);
  if (!target.available) return target.response;
  const query = parseInboxQuery(new URL(request.url).searchParams);
  if (!query.ok) return query.response;

  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'GET',
      path: 'inbox-messages',
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      searchParams: query.value.forward,
    });
  }
  const { triage, assignment } = query.value;
  if (!triage && !assignment) {
    const page = mockMailboxStore.listInbox(
      target.organizationId,
      viewerOf(target),
      query.value.mock
    );
    if (!page.ok) return organizeFailure(page);
    return apiCursorPageResponse(
      mockTriageStore.decorate(target.organizationId, page.value.items),
      page.value.meta,
      privateMailboxHeaders()
    );
  }
  // The triage and assignment filters apply before paging: every row after the cursor, then the
  // page of matches. A conversation never assigned is open and unassigned.
  const { perPage } = query.value.mock;
  const all = mockMailboxStore.listInbox(target.organizationId, viewerOf(target), {
    ...query.value.mock,
    perPage: Number.MAX_SAFE_INTEGER - 1,
  });
  if (!all.ok) return organizeFailure(all);
  const userId = target.context.user_id;
  const matching = all.value.items.filter(item => {
    if (triage && !mockTriageStore.matches(target.organizationId, item, triage)) return false;
    if (!assignment) return true;
    const state = mockAssignmentStore.read(
      target.organizationId,
      item.mailbox_id,
      item.thread_id,
      []
    );
    if (assignment === 'closed') return state.status === 'closed';
    if (state.status !== 'open') return false;
    return assignment === 'mine'
      ? state.assignee_user_id === userId
      : state.assignee_user_id === null;
  });
  const items = mockTriageStore.decorate(target.organizationId, matching.slice(0, perPage));
  const hasMore = matching.length > perPage;
  const last = items.at(-1);
  return apiCursorPageResponse(
    items,
    {
      per_page: perPage,
      has_more: hasMore,
      next_cursor:
        hasMore && last
          ? encodeInboxCursor({
              createdAt: BigInt(Date.parse(last.created_at)) * BigInt(1_000_000),
              id: BigInt(last.id),
            })
          : null,
    },
    privateMailboxHeaders()
  );
}

/**
 * The caller's own unified inbox counts. Only `tz` reaches the API, checked as the inbox checks
 * it; other parameters are dropped.
 */
export async function getInboxMessageCountsRoute(request: Request): Promise<NextResponse> {
  const target = await resolveMailboxReadRoute(request);
  if (!target.available) return target.response;
  const zones = new URL(request.url).searchParams.getAll('tz');
  if (zones.length > 1) return invalidInput('Repeated time zone');
  const [tz] = zones;
  if (tz !== undefined && !isTimeZone(tz)) return invalidInput('Invalid time zone');

  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'GET',
      path: 'inbox-message-counts',
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      searchParams: new URLSearchParams(tz === undefined ? {} : { tz }),
    });
  }
  const viewer = viewerOf(target);
  const unreadInbox = mockMailboxStore.listInbox(target.organizationId, viewer, {
    view: 'all',
    unread: true,
    tz: tz ?? 'UTC',
    perPage: Number.MAX_SAFE_INTEGER - 1,
  });
  return success({
    ...mockMailboxStore.inboxCounts(target.organizationId, viewer, tz ?? 'UTC'),
    triage: mockTriageStore.counts(
      target.organizationId,
      unreadInbox.ok ? unreadInbox.value.items : []
    ),
  });
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

const templateKinds: readonly MailTemplateKind[] = ['standard', 'transactional'];

export async function listMailTemplatesRoute(request: Request): Promise<NextResponse> {
  const target = await resolveMailboxReadRoute(request);
  if (!target.available) return target.response;
  const kind = new URL(request.url).searchParams.get('kind') ?? '';
  if (kind !== '' && !isOneOf(templateKinds, kind)) return invalidInput('Invalid template kind');
  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'GET',
      path: 'mail-templates',
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      // Only the kind filter reaches the API; anything else in the query is dropped.
      ...(kind === '' ? {} : { searchParams: new URLSearchParams({ kind }) }),
    });
  }
  return success(
    mockMailboxStore.listTemplates(target.organizationId, kind === '' ? undefined : kind)
  );
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
  kind: 'kind',
  key: 'key',
  from_address: 'from_address',
  from_name: 'from_name',
  variable_definitions: 'variable_definitions',
} as const;

function mockTemplateResponse(result: MockMailTemplateResult, status: 200 | 201 = 200) {
  if (result.ok) return success(result.template, status);
  switch (result.error) {
    case 'not_found':
      return templateNotFound();
    case 'taken':
      return failure(409, ApiErrorCode.MAILBOX_TEMPLATE_NAME_TAKEN, 'Mail template name is taken');
    case 'key_taken':
      return failure(409, ApiErrorCode.MAILBOX_TEMPLATE_KEY_TAKEN, 'Mail template key is taken');
    case 'kind_immutable':
      return failure(
        409,
        ApiErrorCode.MAILBOX_TEMPLATE_KIND_IMMUTABLE,
        'Mail template kind cannot change'
      );
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

/** Read state and star for the caller; placement, folder, and labels for everyone. */
export async function updateMailMessageRoute(
  request: Request,
  rawMailboxId: string,
  rawMessageId: string
): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  const ids = parseMessageIds(rawMailboxId, rawMessageId);
  if (!ids.ok) return ids.response;
  const input = await messageChangeBody(request, false);
  if (!input.ok) return input.response;

  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'PUT',
      path: `mailboxes/${ids.value.mailboxId}/messages/${ids.value.messageId}`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      body: input.value.change,
      fieldMap: messageChangeFieldMap,
    });
  }
  return organizeResponse(
    mockMailboxStore.updateMessage(
      target.organizationId,
      ids.value.mailboxId,
      ids.value.messageId,
      viewerOf(target),
      input.value.change
    )
  );
}

/** One change for 1 to 100 messages of the mailbox, applied all or nothing. */
export async function updateMailMessagesRoute(
  request: Request,
  rawMailboxId: string
): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  const mailboxId = parseRouteId(rawMailboxId);
  if (!mailboxId.ok) return mailboxId.response;
  const input = await messageChangeBody(request, true);
  if (!input.ok) return input.response;
  const messageIds = input.value.messageIds ?? [];

  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'POST',
      path: `mailboxes/${mailboxId.value}/message-updates`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      body: { message_ids: messageIds, ...input.value.change },
      fieldMap: messageChangeFieldMap,
    });
  }
  return organizeResponse(
    mockMailboxStore.updateMessages(
      target.organizationId,
      mailboxId.value,
      viewerOf(target),
      messageIds,
      input.value.change
    )
  );
}

/** A message with its masked codes shown, to anyone who may read it; a POST, so same-origin only. */
export async function revealMailMessageRoute(
  request: Request,
  rawMailboxId: string,
  rawMessageId: string
): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  const ids = parseMessageIds(rawMailboxId, rawMessageId);
  if (!ids.ok) return ids.response;

  if (target.backend === 'go-api') {
    // The API reads no body here, so none is forwarded.
    return forwardAuthenticatedGoApi(request, {
      method: 'POST',
      path: `mailboxes/${ids.value.mailboxId}/messages/${ids.value.messageId}/reveal`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
    });
  }
  // The mock store never masks, so its message is already the original.
  const result = mockMailboxStore.getMessage(
    target.organizationId,
    ids.value.mailboxId,
    ids.value.messageId,
    viewerOf(target)
  );
  return result.ok ? success(result.message) : missing(result.error);
}

/** Deletes every message in the trash for good; needs `organize`. */
export async function purgeMailTrashRoute(
  request: Request,
  rawMailboxId: string
): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  const mailboxId = parseRouteId(rawMailboxId);
  if (!mailboxId.ok) return mailboxId.response;

  if (target.backend === 'go-api') {
    // The API reads no body here, so none is forwarded.
    return forwardAuthenticatedGoApi(request, {
      method: 'POST',
      path: `mailboxes/${mailboxId.value}/trash-purges`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
    });
  }
  return organizeResponse(
    mockMailboxStore.purgeTrash(target.organizationId, mailboxId.value, viewerOf(target))
  );
}

/** A thread's messages outside the trash, oldest first, with their bodies. */
export async function getMailThreadRoute(
  request: Request,
  rawMailboxId: string,
  rawThreadId: string
): Promise<NextResponse> {
  const target = await resolveMailboxReadRoute(request);
  if (!target.available) return target.response;
  const ids = parseMessageIds(rawMailboxId, rawThreadId);
  if (!ids.ok) return ids.response;

  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'GET',
      path: `mailboxes/${ids.value.mailboxId}/threads/${ids.value.messageId}`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
    });
  }
  return organizeResponse(
    mockMailboxStore.thread(
      target.organizationId,
      ids.value.mailboxId,
      ids.value.messageId,
      viewerOf(target)
    )
  );
}

/** The caller's own unread and starred counts for the mailbox's navigation. */
export async function getMailMessageCountsRoute(
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
      path: `mailboxes/${mailboxId.value}/message-counts`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
    });
  }
  return organizeResponse(
    mockMailboxStore.counts(target.organizationId, mailboxId.value, viewerOf(target))
  );
}

export async function listMailFoldersRoute(
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
      path: `mailboxes/${mailboxId.value}/mail-folders`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
    });
  }
  return organizeResponse(
    mockMailboxStore.folders(target.organizationId, mailboxId.value, viewerOf(target))
  );
}

/** Creates a folder; needs `organize`. */
export async function createMailFolderRoute(
  request: Request,
  rawMailboxId: string
): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  const mailboxId = parseRouteId(rawMailboxId);
  if (!mailboxId.ok) return mailboxId.response;
  const input = await organizeBody(request, false);
  if (!input.ok) return input.response;

  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'POST',
      path: `mailboxes/${mailboxId.value}/mail-folders`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      body: { name: input.value.name },
      fieldMap: organizeFieldMap,
    });
  }
  return organizeResponse(
    mockMailboxStore.saveFolder(
      target.organizationId,
      mailboxId.value,
      viewerOf(target),
      input.value.name
    ),
    201
  );
}

/** Renames a folder; needs `organize`. */
export async function renameMailFolderRoute(
  request: Request,
  rawMailboxId: string,
  rawFolderId: string
): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  const ids = parseMessageIds(rawMailboxId, rawFolderId);
  if (!ids.ok) return ids.response;
  const input = await organizeBody(request, false);
  if (!input.ok) return input.response;

  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'PUT',
      path: `mailboxes/${ids.value.mailboxId}/mail-folders/${ids.value.messageId}`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      body: { name: input.value.name },
      fieldMap: organizeFieldMap,
    });
  }
  return organizeResponse(
    mockMailboxStore.saveFolder(
      target.organizationId,
      ids.value.mailboxId,
      viewerOf(target),
      input.value.name,
      ids.value.messageId
    )
  );
}

/** Deletes a folder; its messages return to `default`. Needs `organize`. */
export async function deleteMailFolderRoute(
  request: Request,
  rawMailboxId: string,
  rawFolderId: string
): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  const ids = parseMessageIds(rawMailboxId, rawFolderId);
  if (!ids.ok) return ids.response;

  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'DELETE',
      path: `mailboxes/${ids.value.mailboxId}/mail-folders/${ids.value.messageId}`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
    });
  }
  const result = mockMailboxStore.deleteFolder(
    target.organizationId,
    ids.value.mailboxId,
    viewerOf(target),
    ids.value.messageId
  );
  return result.ok ? apiNoContentResponse(privateMailboxHeaders()) : organizeFailure(result);
}

export async function listMailLabelsRoute(
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
      path: `mailboxes/${mailboxId.value}/mail-labels`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
    });
  }
  return organizeResponse(
    mockMailboxStore.labels(target.organizationId, mailboxId.value, viewerOf(target))
  );
}

/** Creates a label; needs `organize`. */
export async function createMailLabelRoute(
  request: Request,
  rawMailboxId: string
): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  const mailboxId = parseRouteId(rawMailboxId);
  if (!mailboxId.ok) return mailboxId.response;
  const input = await organizeBody(request, true);
  if (!input.ok) return input.response;

  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'POST',
      path: `mailboxes/${mailboxId.value}/mail-labels`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      body: input.value,
      fieldMap: organizeFieldMap,
    });
  }
  return organizeResponse(
    mockMailboxStore.saveLabel(
      target.organizationId,
      mailboxId.value,
      viewerOf(target),
      input.value
    ),
    201
  );
}

/** Replaces the name and color of a label; needs `organize`. */
export async function updateMailLabelRoute(
  request: Request,
  rawMailboxId: string,
  rawLabelId: string
): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  const ids = parseMessageIds(rawMailboxId, rawLabelId);
  if (!ids.ok) return ids.response;
  const input = await organizeBody(request, true);
  if (!input.ok) return input.response;

  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'PUT',
      path: `mailboxes/${ids.value.mailboxId}/mail-labels/${ids.value.messageId}`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      body: input.value,
      fieldMap: organizeFieldMap,
    });
  }
  return organizeResponse(
    mockMailboxStore.saveLabel(
      target.organizationId,
      ids.value.mailboxId,
      viewerOf(target),
      input.value,
      ids.value.messageId
    )
  );
}

/** Deletes a label and removes it from every message; needs `organize`. */
export async function deleteMailLabelRoute(
  request: Request,
  rawMailboxId: string,
  rawLabelId: string
): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  const ids = parseMessageIds(rawMailboxId, rawLabelId);
  if (!ids.ok) return ids.response;

  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'DELETE',
      path: `mailboxes/${ids.value.mailboxId}/mail-labels/${ids.value.messageId}`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
    });
  }
  const result = mockMailboxStore.deleteLabel(
    target.organizationId,
    ids.value.mailboxId,
    viewerOf(target),
    ids.value.messageId
  );
  return result.ok ? apiNoContentResponse(privateMailboxHeaders()) : organizeFailure(result);
}

/**
 * Outside the trash this moves the message there (`send`); in the trash it deletes the message
 * for good (`organize`).
 */
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

/** A mailbox's agent grants, newest first; its personal owner or a shared mailbox's manager. */
export async function listMailAgentGrantsRoute(
  request: Request,
  rawMailboxId: string
): Promise<NextResponse> {
  const target = await resolveMailboxReadRoute(request);
  if (!target.available) return target.response;
  const mailboxId = parseRouteId(rawMailboxId);
  if (!mailboxId.ok) return mailboxId.response;
  const { page, perPage } = agentPageOf(request);

  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'GET',
      path: `mailboxes/${mailboxId.value}/agent-grants`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      searchParams: new URLSearchParams({ page: String(page), per_page: String(perPage) }),
    });
  }
  const mailbox = agentManagedMailbox(target, mailboxId.value);
  if (!mailbox.ok) return mailbox.response;
  const { items, meta, links } = mockAgentPage(
    mockAgentGrantStore.list(target.organizationId, mailboxId.value),
    page,
    perPage,
    `/api/mailboxes/${mailboxId.value}/agent-grants`
  );
  return apiPaginatedResponse(items, meta, links, privateMailboxHeaders());
}

/** Issues an agent grant; shape-checked here, judged by the API or, in development, the mock. */
export async function issueMailAgentGrantRoute(
  request: Request,
  rawMailboxId: string
): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  const mailboxId = parseRouteId(rawMailboxId);
  if (!mailboxId.ok) return mailboxId.response;
  const body = await jsonObject(request, maxAgentGrantBodyBytes);
  if (!body.ok) return body.response;
  const input = mailAgentGrantInput(body.value);
  if (!input.ok) return validationFailed(input.errors);

  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'POST',
      path: `mailboxes/${mailboxId.value}/agent-grants`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      body: input.value,
      fieldMap: {
        api_key_id: 'api_key_id',
        capabilities: 'capabilities',
        reveal_secrets: 'reveal_secrets',
        filters: 'filters',
        'filters.senders': 'filters.senders',
        'filters.label_ids': 'filters.label_ids',
        expires_at: 'expires_at',
      },
    });
  }
  const mailbox = agentManagedMailbox(target, mailboxId.value);
  if (!mailbox.ok) return mailbox.response;
  const user = await getSessionUser();
  if (!user) return unauthenticated();
  const viewer = viewerOf(target);
  const labels = mockMailboxStore.labels(target.organizationId, mailboxId.value, viewer);
  const inbound = mockMailboxStore.listMessages(
    target.organizationId,
    mailboxId.value,
    viewer,
    { direction: 'inbound' },
    1,
    1
  );
  const result = mockAgentGrantStore.issue(
    target.organizationId,
    mailbox.value,
    {
      userId: viewer.userId,
      keys: mockApiKeyStore.list(user, 1, 100).items,
      labelIds: new Set(labels.ok ? labels.value.map(label => label.id) : []),
      newestInbound: inbound.ok ? (inbound.value.items[0] ?? null) : null,
    },
    input.value
  );
  return result.ok ? success(result.value, 201) : agentGrantFailure(result.error);
}

/** Revokes an agent grant at once; an ended grant answers `204` again. */
export async function revokeMailAgentGrantRoute(
  request: Request,
  rawMailboxId: string,
  rawGrantId: string
): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  const mailboxId = parseRouteId(rawMailboxId);
  if (!mailboxId.ok) return mailboxId.response;
  const grantId = parseRouteId(rawGrantId);
  if (!grantId.ok) return grantId.response;

  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'DELETE',
      path: `mailboxes/${mailboxId.value}/agent-grants/${grantId.value}`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
    });
  }
  const mailbox = agentManagedMailbox(target, mailboxId.value);
  if (!mailbox.ok) return mailbox.response;
  const result = mockAgentGrantStore.revoke(target.organizationId, mailboxId.value, grantId.value);
  return result.ok
    ? apiNoContentResponse(privateMailboxHeaders())
    : agentGrantFailure(result.error);
}

/** A grant's newest 100 audit rows, newest first; they hold no subject or body. */
export async function listMailAgentGrantActivityRoute(
  request: Request,
  rawMailboxId: string,
  rawGrantId: string
): Promise<NextResponse> {
  const target = await resolveMailboxReadRoute(request);
  if (!target.available) return target.response;
  const mailboxId = parseRouteId(rawMailboxId);
  if (!mailboxId.ok) return mailboxId.response;
  const grantId = parseRouteId(rawGrantId);
  if (!grantId.ok) return grantId.response;

  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'GET',
      path: `mailboxes/${mailboxId.value}/agent-grants/${grantId.value}/activity`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
    });
  }
  const mailbox = agentManagedMailbox(target, mailboxId.value);
  if (!mailbox.ok) return mailbox.response;
  const result = mockAgentGrantStore.activity(
    target.organizationId,
    mailboxId.value,
    grantId.value
  );
  return result.ok ? success(result.value) : agentGrantFailure(result.error);
}

/** The reply drafts agents stored for a mailbox, newest first; needs `send`. */
export async function listMailAgentDraftsRoute(
  request: Request,
  rawMailboxId: string
): Promise<NextResponse> {
  const target = await resolveMailboxReadRoute(request);
  if (!target.available) return target.response;
  const mailboxId = parseRouteId(rawMailboxId);
  if (!mailboxId.ok) return mailboxId.response;
  const { page, perPage } = agentPageOf(request);

  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'GET',
      path: `mailboxes/${mailboxId.value}/agent-drafts`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      searchParams: new URLSearchParams({ page: String(page), per_page: String(perPage) }),
    });
  }
  const viewer = viewerOf(target);
  const mailbox = mockMailboxStore
    .list(target.organizationId, viewer)
    .find(item => item.id === mailboxId.value);
  if (!mailbox) return mailboxNotFound();
  if (!mailbox.access.send) return accessForbidden();
  const drafts = mockAgentGrantStore.drafts(
    target.organizationId,
    mailboxId.value,
    messageId =>
      mockMailboxStore.getMessage(target.organizationId, mailboxId.value, messageId, viewer).ok
  );
  const { items, meta, links } = mockAgentPage(
    drafts,
    page,
    perPage,
    `/api/mailboxes/${mailboxId.value}/agent-drafts`
  );
  return apiPaginatedResponse(items, meta, links, privateMailboxHeaders());
}

/** A mailbox the mock caller sees and may manage the agent grants of: `404` unseen, `403` else. */
function agentManagedMailbox(
  target: MailboxTarget & { available: true; backend: 'mock' },
  mailboxId: number
): { ok: true; value: Mailbox } | { ok: false; response: NextResponse } {
  const mailbox = mockMailboxStore
    .list(target.organizationId, viewerOf(target))
    .find(item => item.id === mailboxId);
  if (!mailbox) return { ok: false, response: mailboxNotFound() };
  // On a connected shared mailbox only the caller's own membership tells a manager apart.
  const members = needsOwnMembership(mailbox)
    ? mockMailboxStore.members(target.organizationId, mailboxId, viewerOf(target))
    : null;
  const ownPermission =
    members?.ok === true
      ? (members.value.find(member => member.user_id === target.context.user_id)?.permission ??
        null)
      : null;
  if (!canManageAgentGrants(mailbox, ownPermission)) {
    return { ok: false, response: accessForbidden() };
  }
  return { ok: true, value: mailbox };
}

function agentPageOf(request: Request): { page: number; perPage: number } {
  const query = new URL(request.url).searchParams;
  return {
    page: positiveInteger(query.get('page'), 1),
    perPage: Math.min(positiveInteger(query.get('per_page'), defaultPerPage), MAIL_PAGE_MAX_SIZE),
  };
}

function agentGrantFailure(error: MockAgentGrantError): NextResponse {
  switch (error) {
    case 'invalid':
      return failure(422, ApiErrorCode.MAILBOX_AGENT_GRANT_INVALID, 'Agent grant is invalid');
    case 'exists':
      return failure(409, ApiErrorCode.MAILBOX_AGENT_GRANT_EXISTS, 'Agent grant exists');
    case 'limit':
      return failure(409, ApiErrorCode.MAILBOX_AGENT_GRANT_LIMIT, 'Agent grant limit reached');
    case 'not_found':
      return failure(404, ApiErrorCode.MAILBOX_AGENT_GRANT_NOT_FOUND, 'Agent grant not found');
  }
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

/** The organization's mail roles; every member may read them, each seeing what they may see. */
export async function listMailRolesRoute(request: Request): Promise<NextResponse> {
  const target = await resolveMailboxReadRoute(request);
  if (!target.available) return target.response;

  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'GET',
      path: 'mail-roles',
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
    });
  }
  return success(mockMailboxStore.listRoles(target.organizationId, viewerOf(target)));
}

/** Creates a mail role; owners and admins only, as the API decides. */
export async function createMailRoleRoute(request: Request): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  const input = await roleBody(request);
  if (!input.ok) return input.response;

  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'POST',
      path: 'mail-roles',
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      body: input.value,
      fieldMap: roleFieldMap,
    });
  }
  return organizeResponse(
    mockMailboxStore.createRole(target.organizationId, viewerOf(target), input.value),
    201
  );
}

/** Replaces every field of a mail role; owners and admins only. */
export async function updateMailRoleRoute(
  request: Request,
  rawRoleId: string
): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  const roleId = parseRouteId(rawRoleId);
  if (!roleId.ok) return roleId.response;
  const input = await roleBody(request);
  if (!input.ok) return input.response;

  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'PUT',
      path: `mail-roles/${roleId.value}`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      body: input.value,
      fieldMap: roleFieldMap,
    });
  }
  return organizeResponse(
    mockMailboxStore.updateRole(target.organizationId, viewerOf(target), roleId.value, input.value)
  );
}

/** Deletes a mail role; its mailboxes become ungrouped. Owners and admins only. */
export async function deleteMailRoleRoute(
  request: Request,
  rawRoleId: string
): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  const roleId = parseRouteId(rawRoleId);
  if (!roleId.ok) return roleId.response;

  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'DELETE',
      path: `mail-roles/${roleId.value}`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
    });
  }
  const result = mockMailboxStore.deleteRole(target.organizationId, viewerOf(target), roleId.value);
  return result.ok ? apiNoContentResponse(privateMailboxHeaders()) : organizeFailure(result);
}

/**
 * Puts a mailbox in a mail role (`{ role_id: id }`) or takes it out (`{ role_id: null }`); the
 * field must be present. Needs `administer`; grants and revokes nothing.
 */
export async function setMailboxRoleRoute(
  request: Request,
  rawMailboxId: string
): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  const mailboxId = parseRouteId(rawMailboxId);
  if (!mailboxId.ok) return mailboxId.response;
  const body = await jsonObject(request, maxOrganizeBodyBytes);
  if (!body.ok) return body.response;
  const { role_id: roleId } = body.value;
  if (roleId !== null && !positiveId(roleId)) {
    return validationFailed({ role_id: ['The role_id field must be an id or null'] });
  }

  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'PUT',
      path: `mailboxes/${mailboxId.value}/role-assignment`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      body: { role_id: roleId },
      fieldMap: { role_id: 'role_id' },
    });
  }
  return organizeResponse(
    mockMailboxStore.assignRole(target.organizationId, mailboxId.value, viewerOf(target), roleId)
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

function organizeResponse<T>(result: MockOrganizeResult<T>, status: 200 | 201 = 200): NextResponse {
  return result.ok ? success(result.value, status) : organizeFailure(result);
}

function organizeFailure(
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
  const {
    name,
    subject,
    html,
    text,
    sample_variables: samples,
    design,
    kind,
    key,
    from_address: fromAddress,
    from_name: fromName,
    variable_definitions: definitions,
    ...rest
  } = body;
  const errors: Record<string, string[]> = {};
  if (Object.keys(rest).length > 0) errors.body = ['Unknown fields'];
  if (kind !== undefined && kind !== 'standard' && kind !== 'transactional') {
    errors.kind = ['The kind field is invalid'];
  }
  if (key !== undefined && !(typeof key === 'string' && MAIL_TEMPLATE_KEY_PATTERN.test(key))) {
    errors.key = ['The key field is invalid'];
  }
  if (
    fromAddress !== undefined &&
    !boundedString(fromAddress, MAIL_TEMPLATE_FROM_ADDRESS_MAX_LENGTH)
  ) {
    errors.from_address = ['The from_address field is invalid'];
  }
  if (fromName !== undefined && !boundedString(fromName, MAIL_TEMPLATE_FROM_NAME_MAX_LENGTH)) {
    errors.from_name = ['The from_name field is invalid'];
  }
  if (definitions !== undefined && !variableDefinitions(definitions)) {
    errors.variable_definitions = ['The variable_definitions field is invalid'];
  }
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
      ...(kind === 'standard' || kind === 'transactional' ? { kind } : {}),
      ...(typeof key === 'string' ? { key } : {}),
      ...(typeof fromAddress === 'string' ? { from_address: fromAddress } : {}),
      ...(typeof fromName === 'string' ? { from_name: fromName } : {}),
      ...(variableDefinitions(definitions) ? { variable_definitions: definitions } : {}),
    },
  };
}

const MAIL_TEMPLATE_KEY_PATTERN = /^[a-z][a-z0-9-]{0,63}$/u;
const MAIL_TEMPLATE_FROM_ADDRESS_MAX_LENGTH = 320;
const MAIL_TEMPLATE_FROM_NAME_MAX_LENGTH = 100;

/** Shape checks only; the declaration rules are judged by the API or the mock. */
function variableDefinitions(
  value: unknown
): value is { name: string; secret: boolean; required: boolean }[] {
  return (
    Array.isArray(value) &&
    value.length <= MAIL_TEMPLATE_MAX_VARIABLES &&
    value.every(item => {
      if (typeof item !== 'object' || item === null || Array.isArray(item)) return false;
      const { name, secret, required, ...rest } = item as Record<string, unknown>;
      return (
        Object.keys(rest).length === 0 &&
        typeof name === 'string' &&
        MAIL_TEMPLATE_VARIABLE_PATTERN.test(name) &&
        typeof secret === 'boolean' &&
        typeof required === 'boolean'
      );
    })
  );
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

/** Upstream field errors of the message change routes, under the same keys. */
const messageChangeFieldMap = {
  change: 'change',
  message_ids: 'message_ids',
  read: 'read',
  starred: 'starred',
  placement: 'placement',
  folder_id: 'folder_id',
  label_ids: 'label_ids',
  add_label_ids: 'add_label_ids',
  remove_label_ids: 'remove_label_ids',
} as const;

const organizeFieldMap = { name: 'name', color: 'color' } as const;
const roleFieldMap = {
  name: 'name',
  color: 'color',
  description: 'description',
  owner_user_id: 'owner_user_id',
} as const;

/**
 * Shape checks only: each field the right JSON type, ids non-negative integers. Whether the
 * change is complete, the folder rule, the id counts, and the placement value are judged by the
 * API or the mock after access and lookups, as the contract orders them. Unknown keys and nulls
 * are dropped.
 */
async function messageChangeBody(
  request: Request,
  batch: boolean
): Promise<Parsed<{ messageIds?: number[]; change: MockMessageChange }>> {
  const body = await jsonObject(request, maxMessageChangeBodyBytes);
  if (!body.ok) return body;
  const value = body.value;
  const errors: Record<string, string[]> = {};
  const present = (key: string) => value[key] !== undefined && value[key] !== null;
  const change: MockMessageChange = {};
  for (const key of ['read', 'starred'] as const) {
    if (!present(key)) continue;
    if (typeof value[key] === 'boolean') change[key] = value[key];
    else errors[key] = [`The ${key} field must be a boolean`];
  }
  if (present('placement')) {
    if (boundedString(value.placement, 32)) change.placement = value.placement;
    else errors.placement = ['The placement field must be a string'];
  }
  if (present('folder_id')) {
    if (routeIdOrZero(value.folder_id)) change.folder_id = value.folder_id;
    else errors.folder_id = ['The folder_id field must be an id'];
  }
  const lists = batch ? (['add_label_ids', 'remove_label_ids'] as const) : (['label_ids'] as const);
  for (const key of lists) {
    if (!present(key)) continue;
    if (idList(value[key])) change[key] = value[key];
    else errors[key] = [`The ${key} field must be a list of ids`];
  }
  let messageIds: number[] | undefined;
  if (batch) {
    if (idList(value.message_ids)) messageIds = value.message_ids;
    else errors.message_ids = ['The message_ids field must be a list of ids'];
  }
  if (Object.keys(errors).length > 0) return { ok: false, response: validationFailed(errors) };
  return { ok: true, value: { messageIds, change } };
}

/** A folder `{ name }` or a label `{ name, color }`; a missing string is sent as empty. */
async function organizeBody(
  request: Request,
  withColor: true
): Promise<Parsed<{ name: string; color: string }>>;
async function organizeBody(request: Request, withColor: false): Promise<Parsed<{ name: string }>>;
async function organizeBody(
  request: Request,
  withColor: boolean
): Promise<Parsed<{ name: string; color?: string }>> {
  const body = await jsonObject(request, maxOrganizeBodyBytes);
  if (!body.ok) return body;
  const { name = '', color = '' } = body.value;
  const errors: Record<string, string[]> = {};
  if (typeof name !== 'string') errors.name = ['The name field must be a string'];
  if (withColor && typeof color !== 'string') errors.color = ['The color field must be a string'];
  if (Object.keys(errors).length > 0) return { ok: false, response: validationFailed(errors) };
  return {
    ok: true,
    value: withColor ? { name: name as string, color: color as string } : { name: name as string },
  };
}

/**
 * A mail role write: shape checks only, with the documented defaults filled in (gray, empty, no
 * owner) so the whole role is replaced. The name, palette, length, and owner rules are the API's
 * and, in development, the mock's. Unknown keys are dropped.
 */
async function roleBody(request: Request): Promise<Parsed<Required<MockMailRoleInput>>> {
  const body = await jsonObject(request, maxOrganizeBodyBytes);
  if (!body.ok) return body;
  const { name = '', color = null, description = null, owner_user_id: owner = null } = body.value;
  const errors: Record<string, string[]> = {};
  if (typeof name !== 'string') errors.name = ['The name field must be a string'];
  if (color !== null && typeof color !== 'string') {
    errors.color = ['The color field must be a string'];
  }
  if (description !== null && typeof description !== 'string') {
    errors.description = ['The description field must be a string'];
  }
  if (owner !== null && !positiveId(owner)) {
    errors.owner_user_id = ['The owner_user_id field must be an id or null'];
  }
  if (Object.keys(errors).length > 0) return { ok: false, response: validationFailed(errors) };
  return {
    ok: true,
    value: {
      name: name as string,
      color: (color as string | null) ?? 'gray',
      description: (description as string | null) ?? '',
      owner_user_id: owner as number | null,
    },
  };
}

/** A JSON id the API can bind; zero is left to the backend's own rule. */
function routeIdOrZero(value: unknown): value is number {
  return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0;
}

/** Bounded so a list cannot grow past what the API would read; counts are judged upstream. */
function idList(value: unknown): value is number[] {
  return Array.isArray(value) && value.length <= 1_000 && value.every(routeIdOrZero);
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

/**
 * The listing's query, as `contracts/MAILBOXES.md` allows it: each parameter at most once, known
 * values only, `folder_id` exactly with `placement=folder`, a search of at most 200 characters as
 * sent that is not empty after trimming and not in the trash, and `group=thread` only for
 * placement views. Anything else is invalid input; the API checks the same again.
 */
function parseMessageFilters(query: URLSearchParams): Parsed<MailMessageFilters> {
  const invalid = (message: string): Parsed<MailMessageFilters> => ({
    ok: false,
    response: invalidInput(message),
  });
  const names = [
    'direction',
    'placement',
    'folder_id',
    'label_id',
    'starred',
    'unread',
    'has_attachments',
    'q',
    'group',
  ];
  for (const name of names) {
    if (query.getAll(name).length > 1) return invalid(`Repeated ${name} filter`);
  }
  const filters: MailMessageFilters = {};
  const direction = query.get('direction');
  if (direction !== null) {
    if (!isOneOf(logDirections, direction)) return invalid('Invalid message direction');
    filters.direction = direction;
  }
  const placement = query.get('placement');
  if (placement !== null) {
    if (!isOneOf<MailPlacement>(MAIL_PLACEMENTS, placement)) return invalid('Invalid placement');
    filters.placement = placement;
  }
  for (const [name, key] of [
    ['folder_id', 'folderId'],
    ['label_id', 'labelId'],
  ] as const) {
    const raw = query.get(name);
    if (raw === null) continue;
    const id = parseRouteId(raw);
    if (!id.ok) return invalid(`Invalid ${name} filter`);
    filters[key] = id.value;
  }
  for (const [name, key] of [
    ['starred', 'starred'],
    ['unread', 'unread'],
    ['has_attachments', 'hasAttachments'],
  ] as const) {
    const raw = query.get(name);
    if (raw === null) continue;
    if (raw !== 'true' && raw !== 'false') return invalid(`Invalid ${name} filter`);
    filters[key] = raw === 'true';
  }
  const rawSearch = query.get('q');
  if (rawSearch !== null) {
    const q = rawSearch.trim();
    if (q === '' || Array.from(rawSearch).length > MAIL_SEARCH_MAX_LENGTH) {
      return invalid('Invalid search');
    }
    filters.q = q;
  }
  const group = query.get('group');
  if (group !== null) {
    if (group !== 'thread') return invalid('Invalid grouping');
    filters.group = group;
  }

  if ((filters.placement === 'folder') !== (filters.folderId !== undefined)) {
    return invalid('folder_id goes exactly with placement=folder');
  }
  if (filters.q !== undefined && filters.placement === 'trash') {
    return invalid('The trash cannot be searched');
  }
  if (filters.group === 'thread') {
    const view = filters.placement;
    if (view !== 'default' && view !== 'archive' && view !== 'folder') {
      return invalid('group=thread needs a placement view');
    }
    if (view === 'default' && filters.direction === undefined) {
      return invalid('group=thread on default needs direction');
    }
    if (
      filters.labelId !== undefined ||
      filters.starred !== undefined ||
      filters.unread !== undefined ||
      filters.hasAttachments !== undefined ||
      filters.q !== undefined
    ) {
      return invalid('group=thread lists placement views only');
    }
  }
  return { ok: true, value: filters };
}

/** The filters as the API's query, in a fixed order. */
function messageFilterParams(filters: MailMessageFilters): [string, string][] {
  const params: [string, string][] = [];
  if (filters.direction) params.push(['direction', filters.direction]);
  if (filters.placement) params.push(['placement', filters.placement]);
  if (filters.folderId !== undefined) params.push(['folder_id', String(filters.folderId)]);
  if (filters.labelId !== undefined) params.push(['label_id', String(filters.labelId)]);
  if (filters.starred !== undefined) params.push(['starred', String(filters.starred)]);
  if (filters.unread !== undefined) params.push(['unread', String(filters.unread)]);
  if (filters.hasAttachments !== undefined) {
    params.push(['has_attachments', String(filters.hasAttachments)]);
  }
  if (filters.q !== undefined) params.push(['q', filters.q]);
  if (filters.group) params.push(['group', filters.group]);
  return params;
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

/** The unified inbox parameters, in the order they are forwarded; each may appear once. */
const inboxParameters = [
  'view',
  'role_id',
  'mailbox_id',
  'unread',
  'has_attachments',
  'q',
  'group',
  'triage',
  'assignment',
  'tz',
  'cursor',
  'per_page',
] as const;
const inboxTimeZonePattern = /^[A-Za-z][A-Za-z0-9_+\-/]*$/u;
const integerPattern = /^[+-]?\d+$/u;

/**
 * The unified inbox query rules of `contracts/MAILBOXES.md`, as the API applies them: `page`, a
 * repeated parameter, an empty or unknown value, a malformed cursor, an unknown zone, or a
 * combination outside the rules is invalid input; an integer `per_page` out of range is clamped.
 * Unknown parameters are dropped. Answers the query to forward and the query for the mock.
 */
function parseInboxQuery(query: URLSearchParams): Parsed<{
  forward: URLSearchParams;
  mock: MockInboxQuery;
  triage?: MailTriageCategory[];
  assignment?: InboxAssignment;
}> {
  const invalid = (message: string) => ({ ok: false as const, response: invalidInput(message) });
  if (query.has('page')) return invalid('Use cursor instead of page');
  for (const name of inboxParameters) {
    if (query.getAll(name).length > 1) return invalid('Repeated inbox parameter');
  }
  const value = (name: (typeof inboxParameters)[number]) => query.get(name);
  const forward = new URLSearchParams();
  const mock: MockInboxQuery = { view: 'all', tz: 'UTC', perPage: defaultPerPage };

  const view = value('view');
  if (view !== null) {
    if (!isOneOf<InboxView>(INBOX_VIEWS, view)) return invalid('Invalid inbox view');
    mock.view = view;
    forward.set('view', view);
  }
  const role = value('role_id');
  if (role !== null) {
    const roleId = role === 'none' ? null : parseRouteId(role);
    if (roleId !== null && !roleId.ok) return invalid('Invalid role filter');
    mock.roleId = roleId === null ? 'none' : roleId.value;
    forward.set('role_id', role);
  }
  const mailbox = value('mailbox_id');
  if (mailbox !== null) {
    const mailboxId = parseRouteId(mailbox);
    if (!mailboxId.ok) return invalid('Invalid mailbox filter');
    mock.mailboxId = mailboxId.value;
    forward.set('mailbox_id', mailbox);
  }
  const unread = value('unread');
  if (unread !== null) {
    if (unread !== 'true' && unread !== 'false') return invalid('Invalid unread filter');
    mock.unread = unread === 'true';
    forward.set('unread', unread);
  }
  const hasAttachments = value('has_attachments');
  if (hasAttachments !== null) {
    if (hasAttachments !== 'true' && hasAttachments !== 'false') {
      return invalid('Invalid attachments filter');
    }
    mock.hasAttachments = hasAttachments === 'true';
    forward.set('has_attachments', hasAttachments);
  }
  const rawSearch = value('q');
  if (rawSearch !== null) {
    // As in the mailbox search: the length counts as sent, the rest after trimming.
    const q = rawSearch.trim();
    if (q === '' || Array.from(rawSearch).length > MAIL_SEARCH_MAX_LENGTH || /\p{Cc}/u.test(q)) {
      return invalid('Invalid search');
    }
    mock.q = q;
    forward.set('q', q);
  }
  const group = value('group');
  if (group !== null) {
    if (group !== 'thread') return invalid('Invalid grouping');
    mock.group = group;
    forward.set('group', group);
  }
  let triage: MailTriageCategory[] | undefined;
  const rawTriage = value('triage');
  if (rawTriage !== null) {
    const categories = rawTriage.split(',');
    if (
      new Set(categories).size !== categories.length ||
      !categories.every(category => isOneOf(MAIL_TRIAGE_CATEGORIES, category))
    ) {
      return invalid('Invalid triage filter');
    }
    triage = categories as MailTriageCategory[];
    forward.set('triage', rawTriage);
  }
  const assignment = value('assignment');
  if (assignment !== null) {
    if (!isOneOf<InboxAssignment>(INBOX_ASSIGNMENTS, assignment)) {
      return invalid('Invalid assignment filter');
    }
    forward.set('assignment', assignment);
  }
  const tz = value('tz');
  if (tz !== null) {
    if (!isTimeZone(tz)) return invalid('Invalid time zone');
    mock.tz = tz;
    forward.set('tz', tz);
  }
  const cursor = value('cursor');
  if (cursor !== null) {
    const after = decodeInboxCursor(cursor);
    if (!after) return invalid('Invalid cursor');
    mock.after = after;
    forward.set('cursor', cursor);
  }
  const perPage = value('per_page');
  if (perPage !== null) {
    if (!integerPattern.test(perPage)) return invalid('Invalid page size');
    mock.perPage = Math.min(Math.max(Number(perPage), 1), MAIL_PAGE_MAX_SIZE);
  }
  forward.set('per_page', String(mock.perPage));

  if (mock.q !== undefined && mock.view !== 'all') return invalid('Search needs view all');
  if (mock.group && mock.view !== 'all' && mock.view !== 'sent') {
    return invalid('Grouping needs view all or sent');
  }
  if (
    mock.group &&
    (mock.unread !== undefined || mock.hasAttachments !== undefined || mock.q !== undefined)
  ) {
    return invalid('Grouping cannot be combined with unread, attachments or search');
  }
  if (
    triage &&
    ((mock.view !== 'all' && mock.view !== 'today') || mock.group || mock.q !== undefined)
  ) {
    return invalid('Triage needs view all or today, without grouping or search');
  }
  return {
    ok: true,
    value: {
      forward,
      mock,
      ...(triage ? { triage } : {}),
      ...(assignment === null ? {} : { assignment }),
    },
  };
}

/** An IANA zone name; `Local` and the empty name are never a viewer's zone. */
function isTimeZone(value: string): boolean {
  if (
    value === 'Local' ||
    value.length > INBOX_TIME_ZONE_MAX_LENGTH ||
    !inboxTimeZonePattern.test(value)
  ) {
    return false;
  }
  try {
    new Intl.DateTimeFormat('en-US', { timeZone: value });
    return true;
  } catch {
    return false;
  }
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
interface DirectoryQuery {
  forward: URLSearchParams;
  mock: MockDirectoryQuery;
}

/** The directory query as the API checks it; anything undocumented is dropped. */
function parseDirectoryQuery(query: URLSearchParams): Parsed<DirectoryQuery> {
  const forward = new URLSearchParams();
  const mock: MockDirectoryQuery = { perPage: MAILBOX_DIRECTORY_DEFAULT_PAGE_SIZE };
  const errors: Record<string, string[]> = {};
  const type = query.get('type');
  if (type !== null && type !== '') {
    if (type === 'shared' || type === 'personal') {
      forward.set('type', type);
      mock.type = type;
    } else {
      errors.type = ['The type field is invalid'];
    }
  }
  const q = query.get('q');
  if (q !== null && q !== '') {
    if (Array.from(q).length > MAILBOX_DIRECTORY_QUERY_MAX_LENGTH) {
      errors.q = ['The q field is too long'];
    } else {
      forward.set('q', q);
      mock.q = q;
    }
  }
  const cursor = query.get('cursor');
  if (cursor !== null && cursor !== '') {
    if (cursor.length > MAILBOX_DIRECTORY_CURSOR_MAX_LENGTH) {
      errors.cursor = ['The cursor field is invalid'];
    } else {
      forward.set('cursor', cursor);
      // The mock reads its own tokens; the route refuses a malformed one, as the API does.
      mock.beforeId = decodeDirectoryCursor(cursor) ?? 0;
    }
  }
  const perPage = query.get('per_page');
  if (perPage !== null && perPage !== '') {
    const value = /^[1-9]\d{0,8}$/u.test(perPage) ? Number(perPage) : 0;
    if (value < 1 || value > MAILBOX_DIRECTORY_MAX_PAGE_SIZE) {
      errors.per_page = ['The per_page field is invalid'];
    } else {
      forward.set('per_page', perPage);
      mock.perPage = value;
    }
  }
  if (Object.keys(errors).length > 0) return { ok: false, response: validationFailed(errors) };
  return { ok: true, value: { forward, mock } };
}

function encodeDirectoryCursor(id: number): string {
  return Buffer.from(String(id)).toString('base64url');
}

function decodeDirectoryCursor(token: string): number | null {
  const decoded = Buffer.from(token, 'base64url').toString();
  return /^[1-9]\d{0,15}$/u.test(decoded) && Buffer.from(decoded).toString('base64url') === token
    ? Number(decoded)
    : null;
}

/** The import body as the API checks it, keeping only the documented fields. */
function mailboxImportBody(
  value: Record<string, unknown>
): Parsed<{ dry_run: boolean; rows: MailboxImportRowInput[] }> {
  const { dry_run: dryRun, rows } = value;
  if (typeof dryRun !== 'boolean') {
    return {
      ok: false,
      response: validationFailed({ dry_run: ['The dry_run field is required'] }),
    };
  }
  if (!Array.isArray(rows) || rows.length === 0 || rows.length > MAILBOX_IMPORT_MAX_ROWS) {
    return { ok: false, response: validationFailed({ rows: ['The rows field is invalid'] }) };
  }
  const clean: MailboxImportRowInput[] = [];
  for (const row of rows as unknown[]) {
    const item = (row !== null && typeof row === 'object' ? row : {}) as Record<string, unknown>;
    const displayName = item.display_name ?? '';
    if (
      !boundedString(item.address, MAILBOX_IMPORT_ADDRESS_MAX_LENGTH) ||
      item.address === '' ||
      !boundedString(displayName, MAILBOX_IMPORT_DISPLAY_NAME_MAX_LENGTH) ||
      !boundedString(item.members, MAILBOX_IMPORT_MEMBERS_MAX_LENGTH) ||
      item.members === ''
    ) {
      return { ok: false, response: validationFailed({ rows: ['A row is invalid'] }) };
    }
    clean.push({ address: item.address, display_name: displayName, members: item.members });
  }
  return { ok: true, value: { dry_run: dryRun, rows: clean } };
}

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

/*
 * Platform account ledger (`contracts/MAILBOXES.md`, Platform Accounts). Owners and admins write;
 * members read only the accounts registered with a mailbox they may read or that they own. The
 * BFF checks shapes; the API, or in development the mock, judges everything else.
 */

const maxPlatformAccountBodyBytes = 8 * 1_024;

/** The ledger entries the caller may see, one page at a time. */
export async function listPlatformAccountsRoute(request: Request): Promise<NextResponse> {
  const target = await resolveMailboxReadRoute(request);
  if (!target.available) return target.response;
  const query = new URL(request.url).searchParams;
  const filters = platformAccountFilters(query);
  if (!filters.ok) return filters.response;
  const page = positiveInteger(query.get('page'), 1);
  const perPage = Math.min(
    positiveInteger(query.get('per_page'), defaultPerPage),
    PLATFORM_ACCOUNT_PAGE_MAX_SIZE
  );

  if (target.backend === 'go-api') {
    // Only the known filters and the page parameters reach the API.
    const searchParams = new URLSearchParams({ page: String(page), per_page: String(perPage) });
    const { q, roleId, mailboxId, ownerUserId, status } = filters.value;
    if (q) searchParams.set('q', q);
    if (roleId !== undefined) searchParams.set('role_id', String(roleId));
    if (mailboxId !== undefined) searchParams.set('mailbox_id', String(mailboxId));
    if (ownerUserId !== undefined) searchParams.set('owner_user_id', String(ownerUserId));
    if (status !== undefined) searchParams.set('status', status);
    return forwardAuthenticatedGoApi(request, {
      method: 'GET',
      path: 'platform-accounts',
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      searchParams,
    });
  }
  const viewer = viewerOf(target);
  const accounts = mockPlatformAccountStore.list(
    target.organizationId,
    viewer,
    mockMailboxStore.ledgerDirectory(target.organizationId, viewer),
    filters.value
  );
  const { items, meta, links } = mockLedgerPage(accounts, page, perPage, '/api/platform-accounts?');
  return apiPaginatedResponse(items, meta, links, privateMailboxHeaders());
}

/** Adds an entry to the ledger; owners and admins only, as the API decides. */
export async function createPlatformAccountRoute(request: Request): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  const input = await platformAccountBody(request);
  if (!input.ok) return input.response;

  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'POST',
      path: 'platform-accounts',
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      body: input.value,
      fieldMap: platformAccountFieldMap,
    });
  }
  const viewer = viewerOf(target);
  return ledgerResponse(
    mockPlatformAccountStore.create(
      target.organizationId,
      viewer,
      mockMailboxStore.ledgerDirectory(target.organizationId, viewer),
      input.value
    ),
    201
  );
}

/** Replaces every field of an entry; owners and admins only. */
export async function updatePlatformAccountRoute(
  request: Request,
  rawAccountId: string
): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  const accountId = parseRouteId(rawAccountId);
  if (!accountId.ok) return accountId.response;
  const input = await platformAccountBody(request);
  if (!input.ok) return input.response;

  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'PUT',
      path: `platform-accounts/${accountId.value}`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      body: input.value,
      fieldMap: platformAccountFieldMap,
    });
  }
  const viewer = viewerOf(target);
  return ledgerResponse(
    mockPlatformAccountStore.update(
      target.organizationId,
      viewer,
      mockMailboxStore.ledgerDirectory(target.organizationId, viewer),
      accountId.value,
      input.value
    )
  );
}

/** Deletes an entry after recording its deletion; owners and admins only. */
export async function deletePlatformAccountRoute(
  request: Request,
  rawAccountId: string
): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  const accountId = parseRouteId(rawAccountId);
  if (!accountId.ok) return accountId.response;

  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'DELETE',
      path: `platform-accounts/${accountId.value}`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
    });
  }
  const result = mockPlatformAccountStore.remove(
    target.organizationId,
    viewerOf(target),
    accountId.value
  );
  return result.ok ? apiNoContentResponse(privateMailboxHeaders()) : ledgerFailure(result);
}

/** An entry's handover and status history, newest first, with the listing's visibility. */
export async function listPlatformAccountEventsRoute(
  request: Request,
  rawAccountId: string
): Promise<NextResponse> {
  const target = await resolveMailboxReadRoute(request);
  if (!target.available) return target.response;
  const accountId = parseRouteId(rawAccountId);
  if (!accountId.ok) return accountId.response;
  const query = new URL(request.url).searchParams;
  const page = positiveInteger(query.get('page'), 1);
  const perPage = Math.min(
    positiveInteger(query.get('per_page'), defaultPerPage),
    PLATFORM_ACCOUNT_PAGE_MAX_SIZE
  );

  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'GET',
      path: `platform-accounts/${accountId.value}/events`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      searchParams: new URLSearchParams({ page: String(page), per_page: String(perPage) }),
    });
  }
  const viewer = viewerOf(target);
  const result = mockPlatformAccountStore.eventsOf(
    target.organizationId,
    viewer,
    mockMailboxStore.ledgerDirectory(target.organizationId, viewer),
    accountId.value
  );
  if (!result.ok) return ledgerFailure(result);
  const { items, meta, links } = mockLedgerPage(
    result.value,
    page,
    perPage,
    `/api/platform-accounts/${accountId.value}/events?`
  );
  return apiPaginatedResponse(items, meta, links, privateMailboxHeaders());
}

/** Moves every entry of one owner to another; owners and admins only. */
export async function handOverPlatformAccountsRoute(request: Request): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  const body = await jsonObject(request, maxPlatformAccountBodyBytes);
  if (!body.ok) return body.response;
  const { from_user_id: fromUserId = null, to_user_id: toUserId = null } = body.value;
  const errors: Record<string, string[]> = {};
  if (!positiveId(fromUserId)) errors.from_user_id = ['must be a positive user id'];
  if (!positiveId(toUserId)) errors.to_user_id = ['must be a current member of the organization'];
  if (Object.keys(errors).length > 0) return validationFailed(errors);
  const input = { from_user_id: fromUserId as number, to_user_id: toUserId as number };

  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'POST',
      path: 'platform-account-handovers',
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      body: input,
      fieldMap: { from_user_id: 'from_user_id', to_user_id: 'to_user_id' },
    });
  }
  const viewer = viewerOf(target);
  return ledgerResponse(
    mockPlatformAccountStore.handOver(
      target.organizationId,
      viewer,
      mockMailboxStore.ledgerDirectory(target.organizationId, viewer),
      input.from_user_id,
      input.to_user_id
    ),
    201
  );
}

const platformAccountFieldMap = {
  platform: 'platform',
  handle: 'handle',
  profile_url: 'profile_url',
  role_id: 'role_id',
  mailbox_id: 'mailbox_id',
  owner_user_id: 'owner_user_id',
  status: 'status',
  notes: 'notes',
} as const;

/** Query filters as the API reads them; anything malformed is `400 COMMON.INVALID_INPUT`. */
function platformAccountFilters(query: URLSearchParams): Parsed<PlatformAccountFilters> {
  const filters: PlatformAccountFilters = {};
  const q = (query.get('q') ?? '').trim();
  if (q.length > PLATFORM_ACCOUNT_QUERY_MAX_LENGTH) {
    return { ok: false, response: invalidInput('Invalid platform account filter') };
  }
  if (q) filters.q = q;
  const ids = [
    ['role_id', 'roleId'],
    ['mailbox_id', 'mailboxId'],
    ['owner_user_id', 'ownerUserId'],
  ] as const;
  for (const [name, key] of ids) {
    const raw = query.get(name);
    if (raw === null) continue;
    const id = routeIdPattern.test(raw) ? Number(raw) : Number.NaN;
    if (!Number.isSafeInteger(id)) {
      return { ok: false, response: invalidInput('Invalid platform account filter') };
    }
    filters[key] = id;
  }
  const status = query.get('status');
  if (status !== null) {
    if (!(PLATFORM_ACCOUNT_STATUSES as readonly string[]).includes(status)) {
      return { ok: false, response: invalidInput('Invalid platform account filter') };
    }
    filters.status = status as PlatformAccountStatus;
  }
  return { ok: true, value: filters };
}

/**
 * Shape checks only, as the API decodes the body: text fields strings, references ids or null,
 * the status a string. A wrong JSON type is `400 COMMON.INVALID_INPUT`; unknown keys are dropped.
 */
async function platformAccountBody(request: Request): Promise<Parsed<PlatformAccountInput>> {
  const body = await jsonObject(request, maxPlatformAccountBodyBytes);
  if (!body.ok) return body;
  const value = body.value;
  const text = (key: string) => value[key] === undefined || typeof value[key] === 'string';
  const reference = (key: string) =>
    value[key] === undefined || value[key] === null || positiveId(value[key]);
  const shaped =
    ['platform', 'handle', 'profile_url', 'status', 'notes'].every(text) &&
    ['role_id', 'mailbox_id', 'owner_user_id'].every(reference);
  if (!shaped) return { ok: false, response: invalidInput('Malformed JSON body') };
  const input: PlatformAccountInput = {
    platform: (value.platform as string | undefined) ?? '',
    handle: (value.handle as string | undefined) ?? '',
    profile_url: (value.profile_url as string | undefined) ?? '',
    role_id: (value.role_id as number | null | undefined) ?? null,
    mailbox_id: (value.mailbox_id as number | null | undefined) ?? null,
    owner_user_id: (value.owner_user_id as number | null | undefined) ?? null,
    notes: (value.notes as string | undefined) ?? '',
  };
  if (value.status !== undefined && value.status !== '') {
    if (!(PLATFORM_ACCOUNT_STATUSES as readonly string[]).includes(value.status as string)) {
      return {
        ok: false,
        response: validationFailed({ status: ['must be a known status'] }),
      };
    }
    input.status = value.status as PlatformAccountStatus;
  }
  return { ok: true, value: input };
}

function ledgerResponse<T>(result: MockLedgerResult<T>, status: 200 | 201 = 200): NextResponse {
  return result.ok ? success(result.value, status) : ledgerFailure(result);
}

function ledgerFailure(result: Exclude<MockLedgerResult<unknown>, { ok: true }>): NextResponse {
  switch (result.error) {
    case 'forbidden':
      return forbidden();
    case 'not_found':
      return failure(
        404,
        ApiErrorCode.MAILBOX_PLATFORM_ACCOUNT_NOT_FOUND,
        'Platform account not found'
      );
    case 'taken':
      return failure(
        409,
        ApiErrorCode.MAILBOX_PLATFORM_ACCOUNT_TAKEN,
        'Platform account already in the ledger'
      );
    case 'validation':
      return validationFailed(result.fields);
  }
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

/** The organization's AI triage switches; any member reads them. */
export async function getMailTriageSettingRoute(request: Request): Promise<NextResponse> {
  const target = await resolveMailboxReadRoute(request);
  if (!target.available) return target.response;

  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'GET',
      path: 'mail-triage-setting',
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
    });
  }
  return success(mockTriageStore.setting(target.organizationId));
}

/** Replaces the organization's AI triage switches; owners and admins, every field required. */
export async function updateMailTriageSettingRoute(request: Request): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  const body = await jsonObject(request, maxAccessBodyBytes);
  if (!body.ok) return body.response;
  const { enabled, daily_message_cap: cap, draft_enabled: draftEnabled } = body.value;
  const errors: Record<string, string[]> = {};
  if (typeof enabled !== 'boolean') errors.enabled = ['The enabled field is required'];
  if (
    typeof cap !== 'number' ||
    !Number.isInteger(cap) ||
    cap < 1 ||
    cap > MAIL_TRIAGE_DAILY_CAP_MAX
  ) {
    errors.daily_message_cap = ['The daily message cap must be between 1 and 100000'];
  }
  if (typeof draftEnabled !== 'boolean') {
    errors.draft_enabled = ['The draft enabled field is required'];
  }
  if (Object.keys(errors).length > 0) return validationFailed(errors);
  const input = {
    enabled: enabled as boolean,
    daily_message_cap: cap as number,
    draft_enabled: draftEnabled as boolean,
  };

  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'PUT',
      path: 'mail-triage-setting',
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      body: input,
      fieldMap: {
        enabled: 'enabled',
        daily_message_cap: 'daily_message_cap',
        draft_enabled: 'draft_enabled',
      },
    });
  }
  const saved = mockTriageStore.saveSetting(target.organizationId, viewerOf(target), input);
  return saved.ok ? success(saved.value) : triageFailure(saved.error);
}

/**
 * Turns a mailbox's AI triage on or off: a personal mailbox's owner, or those who manage a shared
 * mailbox's members.
 */
export async function setMailboxTriageSettingRoute(
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
      path: `mailboxes/${mailboxId.value}/triage-setting`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      body: { enabled },
      fieldMap: { enabled: 'enabled' },
    });
  }
  const viewer = viewerOf(target);
  const mailbox = mockMailboxStore
    .list(target.organizationId, viewer)
    .find(item => item.id === mailboxId.value);
  if (!mailbox) return mailboxNotFound();
  const result = mockTriageStore.setMailbox(target.organizationId, mailbox, viewer, enabled);
  return result.ok ? success(result.value) : triageFailure(result.error);
}

/** A reader's own triage category for an inbound message; needs `view` on the mailbox. */
export async function updateMailMessageTriageRoute(
  request: Request,
  rawMailboxId: string,
  rawMessageId: string
): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  const ids = parseMessageIds(rawMailboxId, rawMessageId);
  if (!ids.ok) return ids.response;
  const body = await jsonObject(request, maxAccessBodyBytes);
  if (!body.ok) return body.response;
  const { category, due_at: dueAt } = body.value;
  if (typeof category !== 'string' || !isOneOf(MAIL_TRIAGE_OVERRIDE_CATEGORIES, category)) {
    return validationFailed({ category: ['The category must be reply, deadline, or fyi'] });
  }
  if (
    dueAt !== undefined &&
    dueAt !== null &&
    (typeof dueAt !== 'string' || category !== 'deadline' || Number.isNaN(Date.parse(dueAt)))
  ) {
    return validationFailed({ due_at: ['A due date is only for a deadline'] });
  }
  const input: { category: MailTriageOverrideCategory; due_at?: string | null } = {
    category,
    ...(dueAt === undefined ? {} : { due_at: dueAt as string | null }),
  };

  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'PUT',
      path: `mailboxes/${ids.value.mailboxId}/messages/${ids.value.messageId}/triage`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      body: input,
      fieldMap: { category: 'category', due_at: 'due_at' },
    });
  }
  const found = mockMailboxStore.getMessage(
    target.organizationId,
    ids.value.mailboxId,
    ids.value.messageId,
    viewerOf(target)
  );
  if (!found.ok) return missing(found.error);
  if (found.message.direction !== 'inbound') {
    return validationFailed({ message: ['Only inbound mail is triaged'] });
  }
  const result = mockTriageStore.override(
    target.organizationId,
    ids.value.mailboxId,
    ids.value.messageId,
    input.category,
    input.due_at ?? null
  );
  return result.ok ? success(result.value) : triageFailure(result.error);
}

/**
 * Drafts a reply to an inbound message; needs `send` and the organization's draft switch. The
 * draft is answered once and never stored. The request has no body.
 */
export async function createMailReplyDraftRoute(
  request: Request,
  rawMailboxId: string,
  rawMessageId: string
): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  const ids = parseMessageIds(rawMailboxId, rawMessageId);
  if (!ids.ok) return ids.response;

  if (target.backend === 'go-api') {
    // The API reads no body here, so none is forwarded.
    return forwardAuthenticatedGoApi(request, {
      method: 'POST',
      path: `mailboxes/${ids.value.mailboxId}/messages/${ids.value.messageId}/reply-drafts`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
    });
  }
  const viewer = viewerOf(target);
  const found = mockMailboxStore.getMessage(
    target.organizationId,
    ids.value.mailboxId,
    ids.value.messageId,
    viewer
  );
  if (!found.ok) return missing(found.error);
  if (!mockTriageStore.draftEnabled(target.organizationId)) return triageFailure('unavailable');
  const mailbox = mockMailboxStore
    .list(target.organizationId, viewer)
    .find(item => item.id === ids.value.mailboxId);
  if (!mailbox?.access.send) return accessForbidden();
  if (found.message.direction !== 'inbound') {
    return validationFailed({ message: ['Only inbound mail gets a reply draft'] });
  }
  return success(mockTriageStore.draft(found.message));
}

function triageFailure(error: 'unavailable' | 'forbidden'): NextResponse {
  return error === 'forbidden'
    ? accessForbidden()
    : failure(409, ApiErrorCode.MAILBOX_TRIAGE_UNAVAILABLE, 'AI triage is off');
}

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

/** Reads the caller's signature for a mailbox they may send from (`contracts/MAILBOXES.md`). */
export async function getMailSignatureRoute(
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
      path: `mailboxes/${mailboxId.value}/signature`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
    });
  }
  const viewer = viewerOf(target);
  const mailbox = mockMailboxStore
    .list(target.organizationId, viewer)
    .find(item => item.id === mailboxId.value);
  if (!mailbox) return mailboxNotFound();
  if (!mailbox.access.send) return forbidden();
  return success(mockSignatureStore.read(target.organizationId, mailbox.id, viewer.userId));
}

/** Replaces the caller's signature; an empty body removes it. */
export async function updateMailSignatureRoute(
  request: Request,
  rawMailboxId: string
): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  const mailboxId = parseRouteId(rawMailboxId);
  if (!mailboxId.ok) return mailboxId.response;
  const body = await jsonObject(request, maxSignatureBodyBytes);
  if (!body.ok) return body.response;
  const { body: raw } = body.value;
  if (typeof raw !== 'string') {
    return validationFailed({ body: ['The body field is required'] });
  }
  const signature = normalizeMailSignature(raw);
  if ([...signature].length > MAIL_SIGNATURE_MAX_LENGTH) {
    return validationFailed({ body: ['must be at most 2000 characters'] });
  }
  if (/[\u0000-\u0008\u000b-\u001f\u007f-\u009f]/.test(signature)) {
    return validationFailed({ body: ['must not contain control characters'] });
  }

  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'PUT',
      path: `mailboxes/${mailboxId.value}/signature`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      body: { body: raw },
      fieldMap: { body: 'body' },
    });
  }
  const viewer = viewerOf(target);
  const mailbox = mockMailboxStore
    .list(target.organizationId, viewer)
    .find(item => item.id === mailboxId.value);
  if (!mailbox) return mailboxNotFound();
  if (!mailbox.access.send) return forbidden();
  return success(
    mockSignatureStore.save(target.organizationId, mailbox.id, viewer.userId, signature)
  );
}

/* Thread assignment (`contracts/MAILBOXES.md`, Assignment): shared Zmail mailboxes only. */

const maxAssignmentBodyBytes = 1024;

/** Reads who works a thread of a shared mailbox; it needs `view`. */
export async function getMailThreadAssignmentRoute(
  request: Request,
  rawMailboxId: string,
  rawThreadId: string
): Promise<NextResponse> {
  const target = await resolveMailboxReadRoute(request);
  if (!target.available) return target.response;
  const ids = parseMessageIds(rawMailboxId, rawThreadId);
  if (!ids.ok) return ids.response;
  const { mailboxId, messageId: threadId } = ids.value;

  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'GET',
      path: `mailboxes/${mailboxId}/threads/${threadId}/assignment`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
    });
  }
  const scope = mockAssignableThread(target.organizationId, mailboxId, threadId, viewerOf(target));
  if (!scope.ok) return scope.response;
  return success(
    mockAssignmentStore.read(target.organizationId, mailboxId, threadId, scope.assignable)
  );
}

/** Assigns a thread of a shared mailbox and sets its status; it needs `send`. */
export async function updateMailThreadAssignmentRoute(
  request: Request,
  rawMailboxId: string,
  rawThreadId: string
): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  const ids = parseMessageIds(rawMailboxId, rawThreadId);
  if (!ids.ok) return ids.response;
  const { mailboxId, messageId: threadId } = ids.value;
  const body = await jsonObject(request, maxAssignmentBodyBytes);
  if (!body.ok) return body.response;
  const { assignee_user_id: assignee, status } = body.value;
  const fields: Record<string, string[]> = {};
  if (status !== 'open' && status !== 'closed') fields.status = ['must be open or closed'];
  if (assignee !== null && !(Number.isSafeInteger(assignee) && (assignee as number) > 0)) {
    fields.assignee_user_id = ['must be a user id or null'];
  }
  if (Object.keys(fields).length > 0) return validationFailed(fields);
  const input = {
    assignee_user_id: assignee as number | null,
    status: status as 'open' | 'closed',
  };

  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'PUT',
      path: `mailboxes/${mailboxId}/threads/${threadId}/assignment`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      body: input,
      fieldMap: { assignee_user_id: 'assignee_user_id', status: 'status' },
    });
  }
  const viewer = viewerOf(target);
  const scope = mockAssignableThread(target.organizationId, mailboxId, threadId, viewer);
  if (!scope.ok) return scope.response;
  if (!scope.canSend) return forbidden();
  const kept =
    mockAssignmentStore.read(target.organizationId, mailboxId, threadId, scope.assignable)
      .assignee_user_id === input.assignee_user_id;
  if (
    input.assignee_user_id !== null &&
    !kept &&
    !scope.assignable.includes(input.assignee_user_id)
  ) {
    return validationFailed({ assignee_user_id: ['must be a member of the mailbox who may send'] });
  }
  return success(
    mockAssignmentStore.save(
      target.organizationId,
      mailboxId,
      threadId,
      input,
      viewer.userId,
      scope.assignable
    )
  );
}

/** The mock's shared-mailbox check; the caller is the only member it can assign to. */
function mockAssignableThread(
  organizationId: number,
  mailboxId: number,
  threadId: number,
  viewer: MailboxViewer
): { ok: true; canSend: boolean; assignable: number[] } | { ok: false; response: NextResponse } {
  const mailbox = mockMailboxStore.list(organizationId, viewer).find(item => item.id === mailboxId);
  if (!mailbox) return { ok: false, response: mailboxNotFound() };
  if (mailbox.type !== 'shared' || mailbox.origin === 'connected') {
    return {
      ok: false,
      response: failure(
        409,
        ApiErrorCode.MAILBOX_ASSIGNMENT_UNAVAILABLE,
        'Only a shared Zmail mailbox assigns its threads'
      ),
    };
  }
  const thread = mockMailboxStore.thread(organizationId, mailboxId, threadId, viewer);
  if (!thread.ok) return { ok: false, response: organizeFailure(thread) };
  return {
    ok: true,
    canSend: mailbox.access.send,
    assignable: mailbox.access.send ? [viewer.userId] : [],
  };
}

/* Thread notes (`contracts/MAILBOXES.md`, Thread Notes): internal, shared mailboxes only. */

const maxNoteBodyBytes = 16 * 1024;

/** Lists the notes on a thread of a shared mailbox; it needs `view`. */
export async function listMailThreadNotesRoute(
  request: Request,
  rawMailboxId: string,
  rawThreadId: string
): Promise<NextResponse> {
  const target = await resolveMailboxReadRoute(request);
  if (!target.available) return target.response;
  const ids = parseMessageIds(rawMailboxId, rawThreadId);
  if (!ids.ok) return ids.response;
  const { mailboxId, messageId: threadId } = ids.value;
  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'GET',
      path: `mailboxes/${mailboxId}/threads/${threadId}/notes`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
    });
  }
  const scope = mockAssignableThread(target.organizationId, mailboxId, threadId, viewerOf(target));
  if (!scope.ok) return scope.response;
  return success({ items: mockNoteStore.list(target.organizationId, mailboxId, threadId) });
}

/** Leaves a note on a thread of a shared mailbox; it needs `send`. */
export async function createMailThreadNoteRoute(
  request: Request,
  rawMailboxId: string,
  rawThreadId: string
): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  const ids = parseMessageIds(rawMailboxId, rawThreadId);
  if (!ids.ok) return ids.response;
  const { mailboxId, messageId: threadId } = ids.value;
  const parsed = await jsonObject(request, maxNoteBodyBytes);
  if (!parsed.ok) return parsed.response;
  const raw = parsed.value.body;
  const body = typeof raw === 'string' ? raw.replace(/\r\n/g, '\n').trim() : '';
  if (!body || [...body].length > 2000 || /[\u0000-\u0008\u000b-\u001f\u007f-\u009f]/.test(body)) {
    return validationFailed({ body: ['must be 1 to 2000 characters without control characters'] });
  }
  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'POST',
      path: `mailboxes/${mailboxId}/threads/${threadId}/notes`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      body: { body },
      fieldMap: { body: 'body' },
    });
  }
  const viewer = viewerOf(target);
  const scope = mockAssignableThread(target.organizationId, mailboxId, threadId, viewer);
  if (!scope.ok) return scope.response;
  if (!scope.canSend) return forbidden();
  const note = mockNoteStore.add(target.organizationId, mailboxId, threadId, viewer.userId, body);
  if (!note) {
    return failure(
      409,
      ApiErrorCode.MAILBOX_NOTE_LIMIT_REACHED,
      'This conversation keeps the most notes allowed'
    );
  }
  return success(note, 201);
}

/** Deletes one of the caller's own notes. */
export async function deleteMailThreadNoteRoute(
  request: Request,
  rawMailboxId: string,
  rawThreadId: string,
  rawNoteId: string
): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  const ids = parseMessageIds(rawMailboxId, rawThreadId);
  if (!ids.ok) return ids.response;
  const noteId = parseRouteId(rawNoteId);
  if (!noteId.ok) return noteId.response;
  const { mailboxId, messageId: threadId } = ids.value;
  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'DELETE',
      path: `mailboxes/${mailboxId}/threads/${threadId}/notes/${noteId.value}`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
    });
  }
  const viewer = viewerOf(target);
  const scope = mockAssignableThread(target.organizationId, mailboxId, threadId, viewer);
  if (!scope.ok) return scope.response;
  if (
    !mockNoteStore.remove(target.organizationId, mailboxId, threadId, noteId.value, viewer.userId)
  ) {
    return failure(404, ApiErrorCode.MAILBOX_NOTE_NOT_FOUND, 'Note not found');
  }
  return new NextResponse(null, { status: 204, headers: privateMailboxHeaders() });
}

/* Drafts (`contracts/MAILBOXES.md`, Drafts): each visible to its writer only. */

const maxDraftBodyBytes = 512 * 1024;

/** Lists the caller's drafts in the organization, the latest change first. */
export async function listMailDraftsRoute(request: Request): Promise<NextResponse> {
  const target = await resolveMailboxReadRoute(request);
  if (!target.available) return target.response;
  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'GET',
      path: 'mail-drafts',
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
    });
  }
  return success(mockDraftStore.list(target.organizationId, viewerOf(target).userId));
}

/** One of the caller's drafts in full. */
export async function showMailDraftRoute(
  request: Request,
  rawDraftId: string
): Promise<NextResponse> {
  const target = await resolveMailboxReadRoute(request);
  if (!target.available) return target.response;
  const draftId = parseRouteId(rawDraftId);
  if (!draftId.ok) return draftId.response;
  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'GET',
      path: `mail-drafts/${draftId.value}`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
    });
  }
  const draft = mockDraftStore.get(target.organizationId, viewerOf(target).userId, draftId.value);
  return draft ? success(draft) : draftNotFound();
}

/** Saves a new draft from a mailbox the caller may send from. */
export async function createMailDraftRoute(request: Request): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  return saveMailDraft(request, target, null);
}

/** Replaces one of the caller's drafts. */
export async function updateMailDraftRoute(
  request: Request,
  rawDraftId: string
): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  const draftId = parseRouteId(rawDraftId);
  if (!draftId.ok) return draftId.response;
  return saveMailDraft(request, target, draftId.value);
}

/** Deletes one of the caller's drafts. */
export async function deleteMailDraftRoute(
  request: Request,
  rawDraftId: string
): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  const draftId = parseRouteId(rawDraftId);
  if (!draftId.ok) return draftId.response;
  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'DELETE',
      path: `mail-drafts/${draftId.value}`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
    });
  }
  if (!mockDraftStore.remove(target.organizationId, viewerOf(target).userId, draftId.value)) {
    return draftNotFound();
  }
  return new NextResponse(null, { status: 204, headers: privateMailboxHeaders() });
}

async function saveMailDraft(
  request: Request,
  target: Extract<MailboxTarget, { available: true }>,
  id: number | null
): Promise<NextResponse> {
  const body = await jsonObject(request, maxDraftBodyBytes);
  if (!body.ok) return body.response;
  const raw = body.value;
  const errors: Record<string, string[]> = {};
  const mailboxId = raw.mailbox_id;
  if (typeof mailboxId !== 'number' || !Number.isSafeInteger(mailboxId) || mailboxId < 1) {
    errors.mailbox_id = ['The mailbox_id field is required'];
  }
  const text = (field: keyof typeof MAIL_DRAFT_LIMITS): string => {
    const value = raw[field] ?? '';
    if (typeof value !== 'string') {
      errors[field] = ['must be a string'];
      return '';
    }
    if ([...value].length > MAIL_DRAFT_LIMITS[field]) errors[field] = ['is too long'];
    return value;
  };
  const input = {
    mailbox_id: mailboxId as number,
    to: text('to'),
    cc: text('cc'),
    subject: text('subject'),
    body: text('body'),
    in_reply_to: text('in_reply_to'),
  };
  if (Object.keys(errors).length > 0) return validationFailed(errors);

  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: id === null ? 'POST' : 'PUT',
      path: id === null ? 'mail-drafts' : `mail-drafts/${id}`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      body: input,
      fieldMap: {
        mailbox_id: 'mailbox_id',
        to: 'to',
        cc: 'cc',
        subject: 'subject',
        body: 'body',
        in_reply_to: 'in_reply_to',
      },
    });
  }
  const viewer = viewerOf(target);
  const mailbox = mockMailboxStore
    .list(target.organizationId, viewer)
    .find(item => item.id === input.mailbox_id);
  if (!mailbox) return mailboxNotFound();
  if (!mailbox.access.send) return forbidden();
  const saved = mockDraftStore.save(
    target.organizationId,
    viewer.userId,
    {
      ...input,
      to: draftRecipientLine(input.to),
      cc: draftRecipientLine(input.cc),
      body: input.body.replace(/\r\n?/g, '\n'),
      in_reply_to: input.in_reply_to.trim(),
    },
    id
  );
  if (saved === 'limit') {
    return failure(409, ApiErrorCode.MAILBOX_DRAFT_LIMIT_REACHED, 'Draft limit reached');
  }
  if (saved === null) return draftNotFound();
  return success(saved, id === null ? 201 : 200);
}

function draftNotFound(): NextResponse {
  return failure(404, ApiErrorCode.MAILBOX_DRAFT_NOT_FOUND, 'Mail draft not found');
}
