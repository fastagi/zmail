import {
  MAIL_FOLDERS_MAX_PER_MAILBOX,
  MAIL_FOLDER_NAME_MAX_LENGTH,
  MAIL_LABELS_MAX_PER_MAILBOX,
  MAIL_LABEL_COLORS,
  MAIL_LABEL_NAME_MAX_LENGTH,
  MAIL_MESSAGE_UPDATES_MAX_IDS,
  MAIL_THREAD_MAX_MESSAGES,
  attachmentsWithinLimits,
  normalizeMailMessage,
} from '@/features/mailbox/schemas';
import type {
  MailAttachment,
  MailEvent,
  MailFolder,
  MailLabel,
  MailLabelColor,
  MailMessage,
  MailMessageCounts,
  MailMessageUpdatesResult,
  MailPlacement,
  MailTrashPurge,
  SendMailMessageInput,
} from '@/features/mailbox/types';
import { renderTemplate } from '@/features/mailbox/utils/template-render';
import {
  changeErrors,
  checkIdList,
  cleanValues,
  isLabelColor,
  nameTaken,
  organizeName,
  positiveDistinct,
  sortedByName,
  toggle,
} from '@/features/mailbox/server/mock-store/helpers';
import type { MockMailboxState } from '@/features/mailbox/server/mock-store/state';
import {
  MOCK_MAILBOX_DOMAIN,
  type MailboxState,
  type MailboxViewer,
  type MockMailAttachmentResult,
  type MockMailDeleteResult,
  type MockMailMessageResult,
  type MockMailSendResult,
  type MockMessageChange,
  type MockOrganizeResult,
} from '@/features/mailbox/server/mock-store/types';

export function getMailboxMessage(
  ctx: MockMailboxState,
  organizationId: number,
  mailboxId: number,
  messageId: number,
  viewer: MailboxViewer
): MockMailMessageResult {
  const found = ctx.authorize(organizationId, mailboxId, viewer, 'view');
  if (!found.ok) return found;
  const message = ctx.visibleMessage(found.state, messageId);
  return message
    ? { ok: true, message: ctx.forViewer(found.state, message, viewer) }
    : { ok: false, error: 'not_found' };
}

/** Applies a change to one message and answers it as the viewer sees it. */
export function updateMailboxMessage(
  ctx: MockMailboxState,
  organizationId: number,
  mailboxId: number,
  messageId: number,
  viewer: MailboxViewer,
  change: MockMessageChange
): MockOrganizeResult<MailMessage> {
  const applied = applyMessageChange(
    ctx,
    organizationId,
    mailboxId,
    viewer,
    [messageId],
    change,
    false
  );
  if (!applied.ok) return applied;
  const message = applied.value.messages.get(messageId);
  return message
    ? { ok: true, value: ctx.forViewer(applied.value, message, viewer) }
    : { ok: false, error: 'not_found' };
}

/** Applies one change to 1 to 100 messages, all or nothing. */
export function updateMailboxMessages(
  ctx: MockMailboxState,
  organizationId: number,
  mailboxId: number,
  viewer: MailboxViewer,
  messageIds: number[],
  change: MockMessageChange
): MockOrganizeResult<MailMessageUpdatesResult> {
  const applied = applyMessageChange(
    ctx,
    organizationId,
    mailboxId,
    viewer,
    messageIds,
    change,
    true
  );
  return applied.ok ? { ok: true, value: { updated: messageIds.length } } : applied;
}

/**
 * Outside the trash a delete moves the message there, which needs `send`; in the trash it
 * deletes the message for good, which needs `organize`.
 */
export function deleteMailboxMessage(
  ctx: MockMailboxState,
  organizationId: number,
  mailboxId: number,
  messageId: number,
  viewer: MailboxViewer
): MockMailDeleteResult {
  const found = ctx.authorize(organizationId, mailboxId, viewer, 'send');
  if (!found.ok) return found;
  const message = ctx.visibleMessage(found.state, messageId);
  if (!message) return { ok: false, error: 'not_found' };
  if (message.placement !== 'trash') {
    found.state.messages.set(messageId, ctx.placed(message, 'trash', null));
    return { ok: true };
  }
  if (!found.access.organize) return { ok: false, error: 'forbidden' };
  ctx.forget(found.state, messageId);
  return { ok: true };
}

/** Deletes every visible message in the trash for good; sealed mail stays. */
export function purgeMailboxTrash(
  ctx: MockMailboxState,
  organizationId: number,
  mailboxId: number,
  viewer: MailboxViewer
): MockOrganizeResult<MailTrashPurge> {
  const found = ctx.authorize(organizationId, mailboxId, viewer, 'organize');
  if (!found.ok) return found;
  let deleted = 0;
  for (const message of ctx.visibleMessages(found.state)) {
    if (message.placement !== 'trash') continue;
    ctx.forget(found.state, message.id);
    deleted += 1;
  }
  return { ok: true, value: { deleted } };
}

/** The thread's messages outside the trash, oldest first, at most the latest 100. */
export function getMailboxThread(
  ctx: MockMailboxState,
  organizationId: number,
  mailboxId: number,
  threadId: number,
  viewer: MailboxViewer
): MockOrganizeResult<MailMessage[]> {
  const found = ctx.authorize(organizationId, mailboxId, viewer, 'view');
  if (!found.ok) return found;
  const messages = ctx
    .visibleMessages(found.state)
    .filter(message => message.thread_id === threadId && message.placement !== 'trash')
    .sort((left, right) => left.id - right.id)
    .slice(-MAIL_THREAD_MAX_MESSAGES)
    .map(message => ctx.forViewer(found.state, message, viewer));
  return messages.length > 0 ? { ok: true, value: messages } : { ok: false, error: 'not_found' };
}

/** The viewer's own unread and starred counts; the trash never counts. */
export function getMailboxCounts(
  ctx: MockMailboxState,
  organizationId: number,
  mailboxId: number,
  viewer: MailboxViewer
): MockOrganizeResult<MailMessageCounts> {
  const found = ctx.authorize(organizationId, mailboxId, viewer, 'view');
  if (!found.ok) return found;
  const { state } = found;
  const folderUnread = new Map(sortedByName(state.folders).map(folder => [folder.id, 0]));
  const value: MailMessageCounts = {
    placements: { default_inbound_unread: 0, archive_unread: 0 },
    folders: [],
    starred: 0,
  };
  for (const stored of ctx.visibleMessages(state)) {
    if (stored.placement === 'trash') continue;
    const message = ctx.forViewer(state, stored, viewer);
    if (message.starred) value.starred += 1;
    if (message.direction !== 'inbound' || message.read) continue;
    if (message.placement === 'default') value.placements.default_inbound_unread += 1;
    else if (message.placement === 'archive') value.placements.archive_unread += 1;
    else if (message.folder_id !== null && folderUnread.has(message.folder_id)) {
      folderUnread.set(message.folder_id, (folderUnread.get(message.folder_id) ?? 0) + 1);
    }
  }
  value.folders = [...folderUnread].map(([folderId, unread]) => ({
    folder_id: folderId,
    unread,
  }));
  return { ok: true, value };
}

/** Every folder of the mailbox by name, ignoring case. */
export function listMailboxFolders(
  ctx: MockMailboxState,
  organizationId: number,
  mailboxId: number,
  viewer: MailboxViewer
): MockOrganizeResult<MailFolder[]> {
  const found = ctx.authorize(organizationId, mailboxId, viewer, 'view');
  if (!found.ok) return found;
  return { ok: true, value: sortedByName(found.state.folders).map(folder => ({ ...folder })) };
}

/** Creates a folder, or renames one when `folderId` is given. Needs `organize`. */
export function saveMailboxFolder(
  ctx: MockMailboxState,
  organizationId: number,
  mailboxId: number,
  viewer: MailboxViewer,
  rawName: string,
  folderId?: number
): MockOrganizeResult<MailFolder> {
  const found = ctx.authorize(organizationId, mailboxId, viewer, 'organize');
  if (!found.ok) return found;
  const { folders } = found.state;
  const existing = folderId === undefined ? undefined : folders.get(folderId);
  if (folderId !== undefined && !existing) return { ok: false, error: 'folder_not_found' };
  const fields: Record<string, string[]> = {};
  const name = organizeName(fields, rawName, MAIL_FOLDER_NAME_MAX_LENGTH);
  if (Object.keys(fields).length > 0) return { ok: false, error: 'validation', fields };
  if (!existing && folders.size >= MAIL_FOLDERS_MAX_PER_MAILBOX) {
    return { ok: false, error: 'folder_limit' };
  }
  if (nameTaken(folders, name, folderId)) return { ok: false, error: 'folder_name_taken' };
  const folder: MailFolder = existing
    ? { ...existing, name }
    : { id: ctx.nextFolderId++, name, created_at: ctx.timestamp() };
  folders.set(folder.id, folder);
  return { ok: true, value: { ...folder } };
}

/** Deletes a folder; its messages go back to `default`. Needs `organize`. */
export function deleteMailboxFolder(
  ctx: MockMailboxState,
  organizationId: number,
  mailboxId: number,
  viewer: MailboxViewer,
  folderId: number
): MockOrganizeResult<null> {
  const found = ctx.authorize(organizationId, mailboxId, viewer, 'organize');
  if (!found.ok) return found;
  const { state } = found;
  if (!state.folders.delete(folderId)) return { ok: false, error: 'folder_not_found' };
  for (const message of state.messages.values()) {
    if (message.folder_id === folderId) {
      state.messages.set(message.id, ctx.placed(message, 'default', null));
    }
  }
  return { ok: true, value: null };
}

/** Every label of the mailbox by name, ignoring case. */
export function listMailboxLabels(
  ctx: MockMailboxState,
  organizationId: number,
  mailboxId: number,
  viewer: MailboxViewer
): MockOrganizeResult<MailLabel[]> {
  const found = ctx.authorize(organizationId, mailboxId, viewer, 'view');
  if (!found.ok) return found;
  return { ok: true, value: sortedByName(found.state.labels).map(label => ({ ...label })) };
}

/** Creates a label, or replaces the name and color of one. Needs `organize`. */
export function saveMailboxLabel(
  ctx: MockMailboxState,
  organizationId: number,
  mailboxId: number,
  viewer: MailboxViewer,
  input: { name: string; color: string },
  labelId?: number
): MockOrganizeResult<MailLabel> {
  const found = ctx.authorize(organizationId, mailboxId, viewer, 'organize');
  if (!found.ok) return found;
  const { labels } = found.state;
  const existing = labelId === undefined ? undefined : labels.get(labelId);
  if (labelId !== undefined && !existing) return { ok: false, error: 'label_not_found' };
  const fields: Record<string, string[]> = {};
  const name = organizeName(fields, input.name, MAIL_LABEL_NAME_MAX_LENGTH);
  if (!isLabelColor(input.color)) {
    fields.color = [`must be one of: ${MAIL_LABEL_COLORS.join(', ')}`];
  }
  if (Object.keys(fields).length > 0) return { ok: false, error: 'validation', fields };
  if (!existing && labels.size >= MAIL_LABELS_MAX_PER_MAILBOX) {
    return { ok: false, error: 'label_limit' };
  }
  if (nameTaken(labels, name, labelId)) return { ok: false, error: 'label_name_taken' };
  const label: MailLabel = existing
    ? { ...existing, name, color: input.color as MailLabelColor }
    : {
        id: ctx.nextLabelId++,
        name,
        color: input.color as MailLabelColor,
        created_at: ctx.timestamp(),
      };
  labels.set(label.id, label);
  return { ok: true, value: { ...label } };
}

/** Deletes a label and removes it from every message. Needs `organize`. */
export function deleteMailboxLabel(
  ctx: MockMailboxState,
  organizationId: number,
  mailboxId: number,
  viewer: MailboxViewer,
  labelId: number
): MockOrganizeResult<null> {
  const found = ctx.authorize(organizationId, mailboxId, viewer, 'organize');
  if (!found.ok) return found;
  const { state } = found;
  if (!state.labels.delete(labelId)) return { ok: false, error: 'label_not_found' };
  for (const message of state.messages.values()) {
    if (message.label_ids.includes(labelId)) {
      state.messages.set(message.id, {
        ...message,
        label_ids: message.label_ids.filter(id => id !== labelId),
      });
    }
  }
  return { ok: true, value: null };
}

export function getMailboxAttachment(
  ctx: MockMailboxState,
  organizationId: number,
  mailboxId: number,
  messageId: number,
  attachmentId: number,
  viewer: MailboxViewer
): MockMailAttachmentResult {
  const found = ctx.authorize(organizationId, mailboxId, viewer, 'view');
  if (!found.ok) return found;
  if (!ctx.visibleMessage(found.state, messageId)) return { ok: false, error: 'not_found' };
  const file = found.state.files.get(messageId)?.get(attachmentId);
  return file ? { ok: true, ...file } : { ok: false, error: 'not_found' };
}

/**
 * Validates like the API and stores the message as `sent`; the mock has no provider. A member
 * needs `send`; without `viewer` (the broadcast worker) access is not checked again.
 */
export function sendMailboxMessage(
  ctx: MockMailboxState,
  organizationId: number,
  mailboxId: number,
  input: SendMailMessageInput,
  viewer?: MailboxViewer
): MockMailSendResult {
  let state: MailboxState | undefined;
  if (viewer) {
    const found = ctx.authorize(organizationId, mailboxId, viewer, 'send');
    if (!found.ok) return found;
    state = found.state;
  } else {
    state = ctx.stateOf(organizationId, mailboxId);
  }
  if (!state) return { ok: false, error: 'mailbox_not_found' };
  let draft: Parameters<typeof normalizeMailMessage>[0] = {
    ...input,
    subject: input.subject ?? '',
  };
  let templateId: number | null = null;
  if (input.template) {
    if (input.subject !== undefined || input.text !== undefined || input.html !== undefined) {
      return { ok: false, error: 'invalid' };
    }
    const template = ctx.templatesOf(organizationId).get(input.template.id);
    if (!template) return { ok: false, error: 'template_not_found' };
    const values = cleanValues(input.template.variables ?? {});
    if (!values) return { ok: false, error: 'invalid' };
    // A real send uses only the given values; sample values are for previews and tests.
    const rendered = renderTemplate(
      { subject: template.subject, html: template.html_body, text: template.text_body },
      values
    );
    if (!rendered) return { ok: false, error: 'invalid' };
    if (rendered.missing.length > 0) {
      return { ok: false, error: 'variables_missing', missing: rendered.missing };
    }
    draft = { ...draft, subject: rendered.subject, text: rendered.text, html: rendered.html };
    templateId = template.id;
  }
  const normalized = normalizeMailMessage(draft);
  if (!normalized) return { ok: false, error: 'invalid' };
  const drafts = input.attachments ?? [];
  if (!attachmentsWithinLimits(drafts)) return { ok: false, error: 'invalid' };

  const id = ctx.nextMessageId++;
  // A reply joins the thread of the message it answers, when that message is in this mailbox.
  const replied =
    normalized.in_reply_to === ''
      ? undefined
      : [...state.messages.values()].find(
          message => message.message_id === normalized.in_reply_to
        );
  const files = new Map<number, { filename: string; content: Uint8Array }>();
  const attachments = drafts.map((file): MailAttachment => {
    const attachmentId = ctx.nextAttachmentId++;
    const content = Uint8Array.from(Buffer.from(file.content, 'base64'));
    const filename = file.filename.replaceAll('\\', '/').split('/').pop()?.trim() ?? '';
    files.set(attachmentId, { filename, content });
    return {
      id: attachmentId,
      filename,
      content_type: /^[\w.+-]+\/[\w.+-]+$/u.test(file.content_type ?? '')
        ? (file.content_type as string)
        : 'application/octet-stream',
      size: content.byteLength,
    };
  });
  const sentAt = ctx.timestamp();
  // As the API's send call does: one message-level `accepted`, then what the provider
  // reported for each recipient. The mock's provider delivers to everyone at once.
  const events: MailEvent[] = [
    { type: 'accepted', recipient: '', detail: '', occurred_at: sentAt },
    ...[...normalized.to, ...normalized.cc].map((recipient): MailEvent => ({
      type: 'delivered',
      recipient,
      detail: '',
      occurred_at: sentAt,
    })),
  ];
  const message: MailMessage = {
    id,
    mailbox_id: mailboxId,
    direction: 'outbound',
    status: 'sent',
    from: { address: state.mailbox.address, name: state.mailbox.display_name },
    to: normalized.to,
    cc: normalized.cc,
    subject: normalized.subject,
    // A member wrote it, so it starts out read.
    read: true,
    failure_code: '',
    queued_reason: null,
    last_event: 'delivered',
    attachment_count: attachments.length,
    template_id: templateId,
    placement: 'default',
    folder_id: null,
    label_ids: [],
    starred: false,
    thread_id: replied?.thread_id ?? id,
    trashed_at: null,
    created_at: sentAt,
    text_body: normalized.text,
    html_body: normalized.html,
    message_id: `mock-${id}@${MOCK_MAILBOX_DOMAIN}`,
    in_reply_to: normalized.in_reply_to,
    events,
    attachments,
  };
  ctx.store(state, message);
  if (files.size > 0) state.files.set(id, files);
  return { ok: true, message };
}

/**
 * The checks of `PUT .../messages/:id` and `POST .../message-updates`, in the API's order:
 * mailbox, permission, an over-long batch, every named message, folder, and label, then the
 * body rules. Nothing changes unless all pass.
 */
function applyMessageChange(
  ctx: MockMailboxState,
  organizationId: number,
  mailboxId: number,
  viewer: MailboxViewer,
  messageIds: number[],
  change: MockMessageChange,
  batch: boolean
): MockOrganizeResult<MailboxState> {
  const shared =
    change.placement !== undefined ||
    change.folder_id !== undefined ||
    change.add_label_ids !== undefined ||
    change.remove_label_ids !== undefined ||
    change.label_ids !== undefined;
  const found = ctx.authorize(organizationId, mailboxId, viewer, shared ? 'send' : 'view');
  if (!found.ok) return found;
  const { state } = found;
  if (batch && messageIds.length > MAIL_MESSAGE_UPDATES_MAX_IDS) {
    const fields: Record<string, string[]> = {};
    checkIdList(fields, 'message_ids', messageIds, 1);
    return { ok: false, error: 'validation', fields };
  }
  const messages = positiveDistinct(messageIds).map(id => ctx.visibleMessage(state, id));
  if (messages.some(message => message === undefined)) return { ok: false, error: 'not_found' };
  if (
    change.folder_id !== undefined &&
    change.folder_id !== 0 &&
    !state.folders.has(change.folder_id)
  ) {
    return { ok: false, error: 'folder_not_found' };
  }
  const labelIds = positiveDistinct([
    ...(change.add_label_ids ?? []),
    ...(change.remove_label_ids ?? []),
    ...(change.label_ids ?? []),
  ]);
  if (labelIds.some(id => !state.labels.has(id))) return { ok: false, error: 'label_not_found' };
  const fields = changeErrors(messageIds, change, batch, shared);
  if (Object.keys(fields).length > 0) return { ok: false, error: 'validation', fields };

  for (const stored of messages as MailMessage[]) {
    let message = stored;
    if (change.read !== undefined && message.direction === 'inbound') {
      toggle(state.reads, message.id, viewer.userId, change.read);
    }
    if (change.starred !== undefined) {
      toggle(state.stars, message.id, viewer.userId, change.starred);
    }
    if (change.placement !== undefined) {
      message = ctx.placed(message, change.placement as MailPlacement, change.folder_id ?? null);
    }
    if (change.label_ids !== undefined || change.add_label_ids || change.remove_label_ids) {
      const next = new Set(change.label_ids ?? message.label_ids);
      for (const id of change.add_label_ids ?? []) next.add(id);
      for (const id of change.remove_label_ids ?? []) next.delete(id);
      message = { ...message, label_ids: [...next].sort((left, right) => left - right) };
    }
    state.messages.set(message.id, message);
  }
  return { ok: true, value: state };
}
