import 'server-only';

import { NextResponse } from 'next/server';
import { apiNoContentResponse } from '@/app/api/_shared/success-response';
import { MAIL_ATTACHMENTS_MAX_BYTES } from '@/features/mailbox/schemas';
import {
  mockMailboxStore,
  type MockMessageChange,
} from '@/features/mailbox/server/mock-mailbox-store';
import { forwardAuthenticatedGoApi } from '@/server/api-adapter/authenticated-route';
import {
  type Parsed,
  maxOrganizeBodyBytes,
  resolveMailboxReadRoute,
  resolveMailboxMutationRoute,
  viewerOf,
  jsonObject,
  routeIdOrZero,
  idList,
  boundedString,
  parseRouteId,
  parseMessageIds,
  privateMailboxHeaders,
  success,
  missing,
  validationFailed,
  organizeResponse,
  organizeFailure,
} from './route-shared';

const maxMessageChangeBodyBytes = 16 * 1_024;

// The largest download the API can answer, with room for nothing else.
const maxAttachmentDownloadBytes = MAIL_ATTACHMENTS_MAX_BYTES;

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
  snoozed_until: 'snoozed_until',
  clear_snooze: 'clear_snooze',
} as const;

const organizeFieldMap = { name: 'name', color: 'color' } as const;

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
  for (const key of ['read', 'starred', 'clear_snooze'] as const) {
    if (!present(key)) continue;
    if (typeof value[key] === 'boolean') change[key] = value[key];
    else errors[key] = [`The ${key} field must be a boolean`];
  }
  if (present('snoozed_until')) {
    if (boundedString(value.snoozed_until, 64)) change.snoozed_until = value.snoozed_until;
    else errors.snoozed_until = ['The snoozed_until field must be a timestamp string'];
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
