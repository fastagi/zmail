import 'server-only';

import { NextResponse } from 'next/server';
import { apiNoContentResponse } from '@/app/api/_shared/success-response';
import {
  MAILBOX_DISPLAY_NAME_MAX_LENGTH,
  MAILBOX_LOCAL_PART_MAX_LENGTH,
  MAIL_DOMAIN_NAME_MAX_LENGTH,
  MAILBOX_DIRECTORY_CURSOR_MAX_LENGTH,
  MAILBOX_DIRECTORY_DEFAULT_PAGE_SIZE,
  MAILBOX_DIRECTORY_MAX_PAGE_SIZE,
  MAILBOX_DIRECTORY_QUERY_MAX_LENGTH,
  MAILBOX_EXPORT_MAX_BYTES,
  MAILBOX_IMPORT_ADDRESS_MAX_LENGTH,
  MAILBOX_IMPORT_DISPLAY_NAME_MAX_LENGTH,
  MAILBOX_IMPORT_MAX_ROWS,
  MEMBER_IMPORT_DOMAIN_MAX_LENGTH,
  MEMBER_IMPORT_EMAIL_MAX_LENGTH,
  MEMBER_IMPORT_MAILBOX_MAX_LENGTH,
  MEMBER_IMPORT_MAX_ROWS,
  MEMBER_IMPORT_NAME_MAX_LENGTH,
  MEMBER_IMPORT_ROLE_MAX_LENGTH,
  MAILBOX_IMPORT_MEMBERS_MAX_LENGTH,
  MAILBOX_LIST_SCOPES,
  MAILBOX_MAX_PER_ORGANIZATION,
} from '@/features/mailbox/schemas';
import { mockAgentGrantStore } from '@/features/mailbox/server/mock-agent-grant-store';
import { mockDomainStore } from '@/features/mailbox/server/mock-domain-store';
import {
  MOCK_DAILY_SEND_LIMIT,
  mockMailboxStore,
  type MockDirectoryQuery,
} from '@/features/mailbox/server/mock-mailbox-store';
import type {
  CreateMailboxInput,
  MailboxImportRowInput,
  MemberImportFeatures,
  MemberImportInput,
  MemberImportRowInput,
  MailboxListScope,
  MailboxPermission,
} from '@/features/mailbox/types';
import { ApiErrorCode } from '@/http/codes';
import { forwardAuthenticatedGoApi } from '@/server/api-adapter/authenticated-route';
import {
  type Parsed,
  maxMailboxBodyBytes,
  resolveMailboxReadRoute,
  resolveMailboxMutationRoute,
  mayManageMailboxes,
  viewerOf,
  jsonObject,
  positiveId,
  boundedString,
  parseRouteId,
  privateMailboxHeaders,
  success,
  failure,
  missing,
  accessInvalid,
  mailboxNotFound,
  mailDomainNotFound,
  invalidInput,
  validationFailed,
  forbidden,
  encodeDirectoryCursor,
  decodeDirectoryCursor,
} from './route-shared';

// 500 rows at their longest cells, with JSON escaping.
const maxMailboxImportBodyBytes = 4 * 1_024 * 1_024;

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
 * A roster import: invites each row, reserves each a personal mailbox, and starts each invitation
 * with the chosen grants. Owners and admins only.
 */
export async function importMembersRoute(request: Request): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  if (!mayManageMailboxes(target)) return forbidden();
  const body = await jsonObject(request, maxMailboxImportBodyBytes);
  if (!body.ok) return body.response;
  const input = memberImportBody(body.value);
  if (!input.ok) return input.response;

  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'POST',
      path: 'member-imports',
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      body: {
        dry_run: input.value.dryRun,
        domain: input.value.domain,
        features: input.value.features,
        rows: input.value.rows,
      },
      fieldMap: { dry_run: 'dry_run', domain: 'domain', features: 'features', rows: 'rows' },
    });
  }
  const organizationId = target.organizationId;
  const result = mockMailboxStore.importMembers(
    organizationId,
    viewerOf(target),
    input.value,
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

function memberImportBody(value: Record<string, unknown>): Parsed<MemberImportInput> {
  const { dry_run: dryRun, domain, features, rows } = value;
  if (typeof dryRun !== 'boolean') {
    return {
      ok: false,
      response: validationFailed({ dry_run: ['The dry_run field is required'] }),
    };
  }
  if (!boundedString(domain, MEMBER_IMPORT_DOMAIN_MAX_LENGTH) || domain.trim() === '') {
    return { ok: false, response: validationFailed({ domain: ['The domain field is required'] }) };
  }
  let grants: MemberImportFeatures = {
    broadcasts: false,
    contacts: true,
    templates: true,
    api_keys: false,
    mail_clients: true,
  };
  if (features !== undefined && features !== null) {
    const item = (typeof features === 'object' ? features : {}) as Record<string, unknown>;
    const keys = ['broadcasts', 'contacts', 'templates', 'api_keys'] as const;
    // mail_clients came later: absent means granted, as the API reads it.
    if (
      keys.some(key => typeof item[key] !== 'boolean') ||
      (item.mail_clients !== undefined && typeof item.mail_clients !== 'boolean') ||
      (item.developer !== undefined && typeof item.developer !== 'boolean')
    ) {
      return {
        ok: false,
        response: validationFailed({ features: ['The features field is invalid'] }),
      };
    }
    grants = {
      broadcasts: item.broadcasts === true,
      contacts: item.contacts === true,
      templates: item.templates === true,
      api_keys: item.api_keys === true,
      mail_clients: item.mail_clients !== false,
      ...(item.developer === true ? { developer: true } : {}),
    };
  }
  if (!Array.isArray(rows) || rows.length === 0 || rows.length > MEMBER_IMPORT_MAX_ROWS) {
    return { ok: false, response: validationFailed({ rows: ['The rows field is invalid'] }) };
  }
  const clean: MemberImportRowInput[] = [];
  for (const row of rows as unknown[]) {
    const item = (row !== null && typeof row === 'object' ? row : {}) as Record<string, unknown>;
    const name = item.name ?? '';
    const role = item.role ?? '';
    const mailbox = item.mailbox ?? '';
    if (item.developer !== undefined && typeof item.developer !== 'boolean') {
      return { ok: false, response: validationFailed({ rows: ['A row is invalid'] }) };
    }
    if (
      !boundedString(item.email, MEMBER_IMPORT_EMAIL_MAX_LENGTH) ||
      item.email === '' ||
      !boundedString(name, MEMBER_IMPORT_NAME_MAX_LENGTH) ||
      !boundedString(role, MEMBER_IMPORT_ROLE_MAX_LENGTH) ||
      !boundedString(mailbox, MEMBER_IMPORT_MAILBOX_MAX_LENGTH)
    ) {
      return { ok: false, response: validationFailed({ rows: ['A row is invalid'] }) };
    }
    clean.push({ email: item.email, name, role, mailbox, ...(item.developer === true ? { developer: true } : {}) });
  }
  return { ok: true, value: { dryRun, domain: domain.trim(), features: grants, rows: clean } };
}
