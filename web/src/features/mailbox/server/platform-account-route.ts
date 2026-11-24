import 'server-only';

import { NextResponse } from 'next/server';
import { apiNoContentResponse, apiPaginatedResponse } from '@/app/api/_shared/success-response';
import {
  PLATFORM_ACCOUNT_PAGE_MAX_SIZE,
  PLATFORM_ACCOUNT_QUERY_MAX_LENGTH,
  PLATFORM_ACCOUNT_STATUSES,
  type PlatformAccountFilters,
  type PlatformAccountInput,
  type PlatformAccountStatus,
} from '@/features/mailbox/platform-account-schemas';
import {
  mockLedgerPage,
  mockPlatformAccountStore,
  type MockLedgerResult,
} from '@/features/mailbox/server/mock-platform-account-store';
import { mockMailboxStore } from '@/features/mailbox/server/mock-mailbox-store';
import { ApiErrorCode } from '@/http/codes';
import { forwardAuthenticatedGoApi } from '@/server/api-adapter/authenticated-route';
import {
  type Parsed,
  routeIdPattern,
  defaultPerPage,
  resolveMailboxReadRoute,
  resolveMailboxMutationRoute,
  viewerOf,
  jsonObject,
  positiveId,
  parseRouteId,
  positiveInteger,
  privateMailboxHeaders,
  success,
  failure,
  invalidInput,
  validationFailed,
  forbidden,
} from './route-shared';

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
