import 'server-only';

import { NextResponse } from 'next/server';
import { mockAssignmentStore } from '@/features/mailbox/server/mock-assignment-store';
import { mockNoteStore } from '@/features/mailbox/server/mock-note-store';
import { mockDraftStore } from '@/features/mailbox/server/mock-draft-store';
import { MAIL_DRAFT_LIMITS, draftRecipientLine } from '@/features/mailbox/draft-schemas';
import { mockMailboxStore, type MailboxViewer } from '@/features/mailbox/server/mock-mailbox-store';
import { ApiErrorCode } from '@/http/codes';
import { forwardAuthenticatedGoApi } from '@/server/api-adapter/authenticated-route';
import {
  type MailboxTarget,
  resolveMailboxReadRoute,
  resolveMailboxMutationRoute,
  viewerOf,
  jsonObject,
  parseRouteId,
  parseMessageIds,
  privateMailboxHeaders,
  success,
  failure,
  mailboxNotFound,
  validationFailed,
  forbidden,
  organizeFailure,
} from './route-shared';

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
