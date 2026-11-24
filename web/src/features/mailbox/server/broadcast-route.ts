import 'server-only';

import { NextResponse } from 'next/server';
import { apiNoContentResponse, apiPaginatedResponse } from '@/app/api/_shared/success-response';
import {
  MAIL_BROADCAST_CONTACT_VARIABLES,
  MAIL_BROADCAST_LABEL_MAX_LENGTH,
  MAIL_BROADCAST_NAME_MAX_LENGTH,
  MAIL_BROADCAST_TEST_MAX_PERCENT,
  MAIL_BROADCAST_TEST_WAIT_MAX_MINUTES,
  MAIL_PAGE_MAX_SIZE,
} from '@/features/mailbox/schemas';
import {
  mockBroadcastStore,
  type MockBroadcastResult,
} from '@/features/mailbox/server/mock-broadcast-store';
import type { MailBroadcastInput, MailRecipientStatus } from '@/features/mailbox/types';
import { ApiErrorCode } from '@/http/codes';
import { forwardAuthenticatedGoApi } from '@/server/api-adapter/authenticated-route';
import {
  type Parsed,
  defaultPerPage,
  maxContactBodyBytes,
  privateMailboxResponse,
  resolveMailboxReadRoute,
  resolveMailboxMutationRoute,
  mayManageMailboxes,
  jsonObject,
  boundedString,
  parseRouteId,
  isOneOf,
  positiveInteger,
  privateMailboxHeaders,
  success,
  failure,
  mailboxNotFound,
  contactListNotFound,
  templateNotFound,
  invalidInput,
  validationFailed,
  forbidden,
  variableMap,
} from './route-shared';

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
