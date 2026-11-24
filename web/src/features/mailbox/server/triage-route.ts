import 'server-only';

import { NextResponse } from 'next/server';
import {
  MAIL_TRIAGE_DAILY_CAP_MAX,
  MAIL_TRIAGE_OVERRIDE_CATEGORIES,
  type MailTriageOverrideCategory,
} from '@/features/mailbox/triage-schemas';
import { mockTriageStore } from '@/features/mailbox/server/mock-triage-store';
import { mockMailboxStore } from '@/features/mailbox/server/mock-mailbox-store';
import { ApiErrorCode } from '@/http/codes';
import { forwardAuthenticatedGoApi } from '@/server/api-adapter/authenticated-route';
import {
  maxAccessBodyBytes,
  resolveMailboxReadRoute,
  resolveMailboxMutationRoute,
  viewerOf,
  jsonObject,
  parseRouteId,
  parseMessageIds,
  isOneOf,
  success,
  failure,
  missing,
  accessForbidden,
  mailboxNotFound,
  validationFailed,
} from './route-shared';

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
