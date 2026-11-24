import 'server-only';

import { NextResponse } from 'next/server';
import { apiNoContentResponse, apiPaginatedResponse } from '@/app/api/_shared/success-response';
import { getSessionUser } from '@/features/auth/server/session';
import { MAIL_PAGE_MAX_SIZE } from '@/features/mailbox/schemas';
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
import { mockMailboxStore } from '@/features/mailbox/server/mock-mailbox-store';
import type { Mailbox } from '@/features/mailbox/types';
import { ApiErrorCode } from '@/http/codes';
import { forwardAuthenticatedGoApi } from '@/server/api-adapter/authenticated-route';
import {
  type MailboxTarget,
  defaultPerPage,
  resolveMailboxReadRoute,
  resolveMailboxMutationRoute,
  viewerOf,
  jsonObject,
  parseRouteId,
  positiveInteger,
  privateMailboxHeaders,
  success,
  failure,
  accessForbidden,
  mailboxNotFound,
  validationFailed,
  unauthenticated,
} from './route-shared';

// Twenty filters of each kind at their longest, with JSON escaping.
const maxAgentGrantBodyBytes = 16 * 1_024;

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
