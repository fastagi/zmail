import 'server-only';

import { NextResponse } from 'next/server';
import { apiNoContentResponse, apiPaginatedResponse } from '@/app/api/_shared/success-response';
import {
  MAIL_ADDRESS_MAX_LENGTH,
  MAIL_CONTACT_IMPORT_MAX_BYTES,
  MAIL_CONTACT_LIST_NAME_MAX_LENGTH,
  MAIL_CONTACT_MAX_LISTS,
  MAIL_CONTACT_MAX_PROPERTIES,
  MAIL_LOG_QUERY_MAX_LENGTH,
  MAIL_PAGE_MAX_SIZE,
} from '@/features/mailbox/schemas';
import {
  mockContactStore,
  type MockContactResult,
} from '@/features/mailbox/server/mock-contact-store';
import type {
  MailContactFilters,
  MailContactImportInput,
  MailContactInput,
  MailContactStatus,
} from '@/features/mailbox/types';
import { ApiErrorCode } from '@/http/codes';
import { forwardAuthenticatedGoApi } from '@/server/api-adapter/authenticated-route';
import {
  type Parsed,
  defaultPerPage,
  maxContactBodyBytes,
  privateMailboxResponse,
  resolveMailboxReadRoute,
  resolveMailboxMutationRoute,
  jsonObject,
  boundedString,
  parseRouteId,
  isOneOf,
  positiveInteger,
  privateMailboxHeaders,
  success,
  failure,
  contactListNotFound,
  invalidInput,
  validationFailed,
} from './route-shared';

// The CSV is at most 5 MiB; JSON escaping can roughly double quotes and line breaks.
const maxContactImportBodyBytes = 2 * MAIL_CONTACT_IMPORT_MAX_BYTES + 64 * 1_024;

const contactStatuses: readonly MailContactStatus[] = ['subscribed', 'unsubscribed', 'suppressed'];

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
