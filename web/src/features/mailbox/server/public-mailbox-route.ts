import 'server-only';

import { NextResponse } from 'next/server';
import { getApiSessionToken } from '@/features/auth/server/api-session';
import { getSessionUser } from '@/features/auth/server/session';
import {
  MAIL_DOMAIN_NAME_MAX_LENGTH,
  MAILBOX_DISPLAY_NAME_MAX_LENGTH,
  MAILBOX_LOCAL_PART_MAX_LENGTH,
} from '@/features/mailbox/schemas';
import { mockPublicMailboxStore } from '@/features/mailbox/server/mock-public-mailbox-store';
import { ApiErrorCode } from '@/http/codes';
import { forwardAuthenticatedGoApi } from '@/server/api-adapter/authenticated-route';
import { privateNoStoreResponse } from '@/server/http/private-response';
import {
  boundedString,
  failure,
  jsonObject,
  maxMailboxBodyBytes,
  resolveMailboxMutationRoute,
  resolveMailboxRoute,
  success,
  unauthenticated,
  validationFailed,
  viewerOf,
} from './route-shared';

export function privatePublicMailboxResponse<T extends Response>(response: T): T {
  return privateNoStoreResponse(response, ['Cookie']);
}

type AuthenticatedUserTarget =
  | { available: true; backend: 'go-api'; accessToken: string }
  | { available: true; backend: 'mock'; userKey: string }
  | { available: false; response: NextResponse };

async function resolvePublicMailboxReadTarget(): Promise<AuthenticatedUserTarget> {
  const resolution = resolveMailboxRoute();
  if (!resolution.available) return resolution;
  if (resolution.backend === 'go-api') {
    const accessToken = await getApiSessionToken();
    return accessToken
      ? { available: true, backend: 'go-api', accessToken }
      : { available: false, response: unauthenticated() };
  }
  const user = await getSessionUser();
  return user
    ? { available: true, backend: 'mock', userKey: user.id }
    : { available: false, response: unauthenticated() };
}

export async function getPublicMailboxOptionsRoute(request: Request): Promise<NextResponse> {
  const target = await resolvePublicMailboxReadTarget();
  if (!target.available) return target.response;

  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'GET',
      path: 'public-mailbox/options',
      accessToken: target.accessToken,
    });
  }
  return success(mockPublicMailboxStore.options(target.userKey));
}

export async function getPublicMailboxAvailabilityRoute(request: Request): Promise<NextResponse> {
  const target = await resolvePublicMailboxReadTarget();
  if (!target.available) return target.response;

  const params = new URL(request.url).searchParams;
  const localPart = params.get('local_part') ?? '';
  const domain = params.get('domain') ?? '';
  if (localPart.length > MAILBOX_LOCAL_PART_MAX_LENGTH) {
    return validationFailed({ local_part: ['The local_part field is invalid'] });
  }
  if (domain.length > MAIL_DOMAIN_NAME_MAX_LENGTH) {
    return validationFailed({ domain: ['The domain field is invalid'] });
  }

  if (target.backend === 'go-api') {
    const searchParams = new URLSearchParams({ local_part: localPart });
    if (domain !== '') searchParams.set('domain', domain);
    return forwardAuthenticatedGoApi(request, {
      method: 'GET',
      path: 'public-mailbox/availability',
      accessToken: target.accessToken,
      searchParams,
    });
  }

  return success(mockPublicMailboxStore.availability(localPart, domain || undefined));
}

export async function claimPublicMailboxRoute(request: Request): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;

  const body = await jsonObject(request, maxMailboxBodyBytes);
  if (!body.ok) return body.response;

  const { local_part: localPart, domain, display_name: displayName } = body.value;
  if (!boundedString(localPart, MAILBOX_LOCAL_PART_MAX_LENGTH) || localPart.trim() === '') {
    return validationFailed({ local_part: ['The local_part field is invalid'] });
  }
  if (domain !== undefined && !boundedString(domain, MAIL_DOMAIN_NAME_MAX_LENGTH)) {
    return validationFailed({ domain: ['The domain field is invalid'] });
  }
  if (displayName !== undefined && !boundedString(displayName, MAILBOX_DISPLAY_NAME_MAX_LENGTH)) {
    return validationFailed({ display_name: ['The display_name field is invalid'] });
  }

  const payload = {
    local_part: localPart,
    ...(domain !== undefined && domain.trim() !== '' ? { domain } : {}),
    ...(displayName !== undefined && displayName.trim() !== '' ? { display_name: displayName } : {}),
  };

  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'POST',
      path: 'public-mailbox/claim',
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      body: payload,
      fieldMap: {
        local_part: 'local_part',
        domain: 'domain',
        display_name: 'display_name',
      },
    });
  }

  const sessionUser = await getSessionUser();
  const userKey = sessionUser?.id ?? String(target.context.user_id);
  const result = mockPublicMailboxStore.claim(
    target.organizationId,
    viewerOf(target),
    userKey,
    payload
  );
  if (result.ok) return success(result.mailbox, 201);
  switch (result.error) {
    case 'disabled':
      return failure(503, ApiErrorCode.COMMON_SERVICE_UNAVAILABLE, 'Public mailbox claiming is disabled');
    case 'domain_invalid':
      return failure(422, ApiErrorCode.MAILBOX_DOMAIN_INVALID, 'Public mailbox domain is invalid');
    case 'taken':
      return failure(409, ApiErrorCode.MAILBOX_ADDRESS_TAKEN, 'Mailbox address is taken');
    case 'reserved':
      return failure(422, ApiErrorCode.MAILBOX_ADDRESS_RESERVED, 'Mailbox address is reserved');
    case 'limit':
      return failure(422, ApiErrorCode.MAILBOX_LIMIT_REACHED, 'Public mailbox limit reached');
    default:
      return failure(422, ApiErrorCode.MAILBOX_ADDRESS_INVALID, 'Mailbox address is invalid');
  }
}
