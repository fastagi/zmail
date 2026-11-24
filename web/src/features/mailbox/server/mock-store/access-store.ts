import {
  MAIL_LABEL_COLORS,
  MAIL_ROLES_MAX_PER_ORGANIZATION,
  MAIL_ROLE_DESCRIPTION_MAX_LENGTH,
  MAIL_ROLE_NAME_MAX_LENGTH,
  MAILBOX_MAX_MEMBERS,
} from '@/features/mailbox/schemas';
import type {
  MailLabelColor,
  MailRole,
  MailRoleInput,
  Mailbox,
  MailboxAccessEventPage,
  MailboxMember,
  MailboxSealedDeletion,
} from '@/features/mailbox/types';
import {
  addField,
  anyAccess,
  forgetReads,
  isLabelColor,
  isPermission,
  mailboxAccessFor,
  managesOrganization,
  nameTaken,
  organizeName,
  paginated,
  sortedByName,
  sortedMembers,
} from '@/features/mailbox/server/mock-store/helpers';
import type { MockMailboxState } from '@/features/mailbox/server/mock-store/state';
import type {
  MailboxViewer,
  MockAccessResult,
  MockMailRoleInput,
  MockOrganizeResult,
  StoredRole,
} from '@/features/mailbox/server/mock-store/types';

/**
 * The organization's mail roles by name ignoring case, then id. Owners and admins see every role
 * with every mailbox in it; members only roles holding a mailbox they can see, with only those.
 * An owner who left the organization reads as none.
 */
export function listMailRoles(
  ctx: MockMailboxState,
  organizationId: number,
  viewer: MailboxViewer
): MailRole[] {
  return sortedByName(ctx.rolesOf(organizationId))
    .map(role => roleView(ctx, organizationId, role, viewer))
    .filter(role => managesOrganization(viewer) || role.mailbox_ids.length > 0);
}

/** Owners and admins only; the limit is judged before the name, as the API's insert does. */
export function createMailRole(
  ctx: MockMailboxState,
  organizationId: number,
  viewer: MailboxViewer,
  input: MockMailRoleInput
): MockOrganizeResult<MailRole> {
  if (!managesOrganization(viewer)) return { ok: false, error: 'forbidden' };
  const checked = checkRole(ctx, organizationId, input);
  if (!checked.ok) return checked;
  const roles = ctx.rolesOf(organizationId);
  if (roles.size >= MAIL_ROLES_MAX_PER_ORGANIZATION) return { ok: false, error: 'role_limit' };
  if (nameTaken(roles, checked.value.name)) return { ok: false, error: 'role_name_taken' };
  const now = ctx.timestamp();
  const role: StoredRole = {
    id: ctx.nextRoleId++,
    ...checked.value,
    created_at: now,
    updated_at: now,
  };
  roles.set(role.id, role);
  return { ok: true, value: roleView(ctx, organizationId, role, viewer) };
}

/** Replaces every field; the last write wins. Owners and admins only. */
export function updateMailRole(
  ctx: MockMailboxState,
  organizationId: number,
  viewer: MailboxViewer,
  roleId: number,
  input: MockMailRoleInput
): MockOrganizeResult<MailRole> {
  if (!managesOrganization(viewer)) return { ok: false, error: 'forbidden' };
  const roles = ctx.rolesOf(organizationId);
  const existing = roles.get(roleId);
  if (!existing) return { ok: false, error: 'role_not_found' };
  const checked = checkRole(ctx, organizationId, input);
  if (!checked.ok) return checked;
  if (nameTaken(roles, checked.value.name, roleId)) return { ok: false, error: 'role_name_taken' };
  const role: StoredRole = { ...existing, ...checked.value, updated_at: ctx.timestamp() };
  roles.set(roleId, role);
  return { ok: true, value: roleView(ctx, organizationId, role, viewer) };
}

/** Deletes the role; its mailboxes become ungrouped, and no access changes. */
export function deleteMailRole(
  ctx: MockMailboxState,
  organizationId: number,
  viewer: MailboxViewer,
  roleId: number
): MockOrganizeResult<null> {
  if (!managesOrganization(viewer)) return { ok: false, error: 'forbidden' };
  if (!ctx.rolesOf(organizationId).delete(roleId)) return { ok: false, error: 'role_not_found' };
  for (const state of ctx.mailboxesOf(organizationId).values()) {
    if (state.roleId === roleId) state.roleId = null;
  }
  return { ok: true, value: null };
}

/**
 * Puts the mailbox in a role, or takes it out with null; needs `administer`. Setting the current
 * value succeeds. Answers the mailbox as the caller sees it.
 */
export function assignMailboxRole(
  ctx: MockMailboxState,
  organizationId: number,
  mailboxId: number,
  viewer: MailboxViewer,
  roleId: number | null
): MockOrganizeResult<Mailbox> {
  const found = ctx.authorize(organizationId, mailboxId, viewer, 'administer');
  if (!found.ok) return found;
  if (roleId !== null && !ctx.rolesOf(organizationId).has(roleId)) {
    return { ok: false, error: 'role_not_found' };
  }
  found.state.roleId = roleId;
  return { ok: true, value: ctx.view(found.state, viewer, found.access) };
}

/** Members of a shared mailbox, ordered by user id. */
export function listMailboxMembers(
  ctx: MockMailboxState,
  organizationId: number,
  mailboxId: number,
  viewer: MailboxViewer
): MockAccessResult<MailboxMember[]> {
  const found = ctx.authorizeMembers(organizationId, mailboxId, viewer);
  if (!found.ok) return found;
  return { ok: true, value: sortedMembers(found.state) };
}

/** Adds the user or changes their permission. */
export function setMailboxMember(
  ctx: MockMailboxState,
  organizationId: number,
  mailboxId: number,
  viewer: MailboxViewer,
  userId: number,
  permission: unknown
): MockAccessResult<MailboxMember> {
  const found = ctx.authorizeMembers(organizationId, mailboxId, viewer);
  if (!found.ok) return found;
  const membershipId = ctx.dependencies.membershipOf(organizationId, userId);
  if (!isPermission(permission) || membershipId === null) {
    return { ok: false, error: 'invalid' };
  }
  const { members } = found.state.access;
  const existing = members.get(userId);
  if (!existing && members.size >= MAILBOX_MAX_MEMBERS) return { ok: false, error: 'invalid' };
  const member: MailboxMember = {
    user_id: userId,
    permission,
    granted_by: viewer.userId,
    created_at: existing?.created_at ?? ctx.timestamp(),
  };
  members.set(userId, member);
  ctx.record(found.state, 'member_set', viewer.userId, userId, {
    membership_id: membershipId,
    permission,
  });
  return { ok: true, value: { ...member } };
}

/** Removes a member together with their read state in the mailbox. */
export function removeMailboxMember(
  ctx: MockMailboxState,
  organizationId: number,
  mailboxId: number,
  viewer: MailboxViewer,
  userId: number
): MockAccessResult<null> {
  const found = ctx.authorizeMembers(organizationId, mailboxId, viewer);
  if (!found.ok) return found;
  if (!found.state.access.members.delete(userId)) {
    return { ok: false, error: 'member_not_found' };
  }
  forgetReads(found.state, userId);
  ctx.record(found.state, 'member_removed', viewer.userId, userId, {});
  return { ok: true, value: null };
}

/** The access history, newest first. Needs `see_history`. */
export function listMailboxAccessEvents(
  ctx: MockMailboxState,
  organizationId: number,
  mailboxId: number,
  viewer: MailboxViewer,
  page: number,
  perPage: number
): MockAccessResult<MailboxAccessEventPage> {
  const found = ctx.authorize(organizationId, mailboxId, viewer, 'see_history');
  if (!found.ok) return found;
  const events = [...found.state.access.events].sort((left, right) => right.id - left.id);
  return {
    ok: true,
    value: paginated(events, page, perPage, `/api/mailboxes/${mailboxId}/access-events?`),
  };
}

/**
 * Turns a personal mailbox into a shared one the successor works with `send`. Mail from before
 * the owner left, or before now while the owner is still a member, is sealed for good.
 */
export function handOverMailbox(
  ctx: MockMailboxState,
  organizationId: number,
  mailboxId: number,
  viewer: MailboxViewer,
  successorUserId: number
): MockAccessResult<Mailbox> {
  const found = ctx.authorize(organizationId, mailboxId, viewer, 'administer');
  if (!found.ok) return found;
  if (ctx.dependencies.membershipOf(organizationId, successorUserId) === null) {
    return { ok: false, error: 'invalid' };
  }
  const { access } = found.state;
  if (access.type !== 'personal' || access.origin === 'connected') {
    return { ok: false, error: 'conflict' };
  }

  const formerOwner = access.ownerUserId;
  const boundary = access.ownerLeft ?? {
    at: ctx.timestamp(),
    throughMessageId: ctx.nextMessageId - 1,
  };
  access.type = 'shared';
  access.ownerUserId = null;
  access.ownerLeft = null;
  access.sealedBefore = boundary.at;
  access.sealedThroughMessageId = boundary.throughMessageId;
  if (formerOwner !== null) forgetReads(found.state, formerOwner);
  access.members.set(successorUserId, {
    user_id: successorUserId,
    permission: 'send',
    granted_by: viewer.userId,
    created_at: ctx.timestamp(),
  });
  ctx.record(found.state, 'handed_over', viewer.userId, successorUserId, {
    former_owner_user_id: formerOwner,
    sealed_before: boundary.at,
  });
  return { ok: true, value: ctx.view(found.state, viewer) };
}

/** Deletes the sealed mail for good; the boundary stays. The mock has no sealed broadcasts. */
export function deleteMailboxSealedHistory(
  ctx: MockMailboxState,
  organizationId: number,
  mailboxId: number,
  viewer: MailboxViewer,
  confirmAddress: string
): MockAccessResult<MailboxSealedDeletion> {
  const found = ctx.authorize(organizationId, mailboxId, viewer, 'administer');
  if (!found.ok) return found;
  const { state } = found;
  if (state.access.sealedBefore === null) return { ok: false, error: 'conflict' };
  if (confirmAddress.trim().toLowerCase() !== state.mailbox.address.toLowerCase()) {
    return { ok: false, error: 'invalid' };
  }
  let deleted = 0;
  for (const messageId of [...state.messages.keys()]) {
    if (messageId <= state.access.sealedThroughMessageId) {
      ctx.forget(state, messageId);
      deleted += 1;
    }
  }
  const counts = { deleted_messages: deleted, deleted_broadcasts: 0 };
  ctx.record(state, 'sealed_deleted', viewer.userId, null, counts);
  return { ok: true, value: counts };
}

/** Turns organization webhooks of a shared mailbox on or off; only a change is recorded. */
export function setMailboxWebhooks(
  ctx: MockMailboxState,
  organizationId: number,
  mailboxId: number,
  viewer: MailboxViewer,
  enabled: boolean
): MockAccessResult<Mailbox> {
  const found = ctx.authorizeMembers(organizationId, mailboxId, viewer);
  if (!found.ok) return found;
  if (found.state.access.webhooksEnabled !== enabled) {
    found.state.access.webhooksEnabled = enabled;
    ctx.record(found.state, 'webhooks_changed', viewer.userId, null, { enabled });
  }
  return { ok: true, value: ctx.view(found.state, viewer) };
}

/** The role as the viewer sees it: the mailboxes they can see, and only a current owner. */
function roleView(
  ctx: MockMailboxState,
  organizationId: number,
  role: StoredRole,
  viewer: MailboxViewer
): MailRole {
  const mailboxIds = ctx
    .statesOf(organizationId)
    .filter(state => state.roleId === role.id && anyAccess(mailboxAccessFor(state.access, viewer)))
    .map(state => state.mailbox.id)
    .sort((left, right) => left - right);
  const owner =
    role.owner_user_id !== null &&
    ctx.dependencies.membershipOf(organizationId, role.owner_user_id) !== null
      ? role.owner_user_id
      : null;
  return { ...role, owner_user_id: owner, mailbox_ids: mailboxIds };
}

/** The field rules of a role write, with the defaults applied. */
function checkRole(
  ctx: MockMailboxState,
  organizationId: number,
  input: MockMailRoleInput
):
  | { ok: true; value: Required<MailRoleInput> & { color: MailLabelColor } }
  | { ok: false; error: 'validation'; fields: Record<string, string[]> } {
  const fields: Record<string, string[]> = {};
  const name = organizeName(fields, input.name, MAIL_ROLE_NAME_MAX_LENGTH);
  const color = input.color ?? 'gray';
  if (!isLabelColor(color)) {
    addField(fields, 'color', `must be one of: ${MAIL_LABEL_COLORS.join(', ')}`);
  }
  const description = (input.description ?? '').trim();
  if (
    Array.from(description).length > MAIL_ROLE_DESCRIPTION_MAX_LENGTH ||
    /\p{Cc}/u.test(description)
  ) {
    addField(
      fields,
      'description',
      `must be at most ${MAIL_ROLE_DESCRIPTION_MAX_LENGTH} characters without control characters`
    );
  }
  const owner = input.owner_user_id ?? null;
  if (owner !== null && ctx.dependencies.membershipOf(organizationId, owner) === null) {
    addField(fields, 'owner_user_id', 'must be a member of the organization');
  }
  if (Object.keys(fields).length > 0) return { ok: false, error: 'validation', fields };
  return {
    ok: true,
    value: { name, color: color as MailLabelColor, description, owner_user_id: owner },
  };
}
