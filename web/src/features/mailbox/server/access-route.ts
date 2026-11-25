import 'server-only';

import { NextResponse } from 'next/server';
import { apiNoContentResponse, apiPaginatedResponse } from '@/app/api/_shared/success-response';
import {
  MAIL_ADDRESS_MAX_LENGTH,
  MAILBOX_ACCESS_EVENTS_MAX_PAGE_SIZE,
} from '@/features/mailbox/schemas';
import {
  mockMailboxStore,
  type MockAccessError,
  type MockAccessResult,
  type MockMailRoleInput,
} from '@/features/mailbox/server/mock-mailbox-store';
import { ApiErrorCode } from '@/http/codes';
import { forwardAuthenticatedGoApi } from '@/server/api-adapter/authenticated-route';
import {
  type Parsed,
  defaultPerPage,
  maxOrganizeBodyBytes,
  maxAccessBodyBytes,
  resolveMailboxReadRoute,
  resolveMailboxMutationRoute,
  viewerOf,
  jsonObject,
  positiveId,
  boundedString,
  parseRouteId,
  positiveInteger,
  privateMailboxHeaders,
  success,
  failure,
  missing,
  accessInvalid,
  mailboxNotFound,
  validationFailed,
  organizeResponse,
  organizeFailure,
} from './route-shared';

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

/** Queues a shared-mailbox member grant for an invited email; owners and admins only. */
export async function setPendingMailboxMemberRoute(
  request: Request,
  rawMailboxId: string
): Promise<NextResponse> {
  const target = await resolveMailboxMutationRoute(request);
  if (!target.available) return target.response;
  const mailboxId = parseRouteId(rawMailboxId);
  if (!mailboxId.ok) return mailboxId.response;
  const body = await jsonObject(request, maxAccessBodyBytes);
  if (!body.ok) return body.response;
  const { email, permission } = body.value;
  const errors: Record<string, string[]> = {};
  if (!boundedString(email, MAIL_ADDRESS_MAX_LENGTH)) {
    errors.email = ['The email field is required'];
  }
  if (!boundedString(permission, 32)) {
    errors.permission = ['The permission field is required'];
  }
  if (Object.keys(errors).length > 0) return validationFailed(errors);

  if (target.backend === 'go-api') {
    return forwardAuthenticatedGoApi(request, {
      method: 'POST',
      path: `mailboxes/${mailboxId.value}/pending-members`,
      accessToken: target.accessToken,
      organizationId: String(target.organizationId),
      body: { email, permission },
      fieldMap: { email: 'email', permission: 'permission' },
    });
  }
  const now = new Date().toISOString();
  return success(
    {
      id: 1,
      mailbox_id: mailboxId.value,
      email: (email as string).trim().toLowerCase(),
      permission,
      granted_by: target.context.user_id,
      created_at: now,
      updated_at: now,
    },
    201
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

const roleFieldMap = {
  name: 'name',
  color: 'color',
  description: 'description',
  owner_user_id: 'owner_user_id',
} as const;

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
