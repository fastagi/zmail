import type { ApiKey } from '@/features/api-key/types';
import type { Mailbox, MailboxPermission } from '@/features/mailbox/types';

/** The scope a key needs for a grant; `*` includes it. Kept here so the access panel stays small. */
export const MAIL_AGENT_SCOPE = 'mail:agent';

type AgentAccessMailbox = Pick<Mailbox, 'type' | 'origin' | 'access' | 'awaiting_handover'>;

/**
 * Whether the caller may issue, list, and revoke the mailbox's agent grants: the rule the API
 * applies (`contracts/MAILBOXES.md`, Agent Grants), read from the `access` it returns.
 *
 * - Personal, Zmail or connected: its owner alone. Only the owner has `view` on a personal
 *   mailbox; owners and admins never do, and nobody does while it awaits a handover.
 * - Shared Zmail: a `manage` member, the one shared caller with `organize` (an owner or admin who
 *   is not a `manage` member has `manage_members` without it).
 * - Shared connected: nobody has `organize` there, and an owner or admin who is only a `view` or
 *   `send` member has `view` and `manage_members` like a `manage` member. So the caller's own
 *   membership decides: `ownPermission` is the caller's row in the member list, `undefined`
 *   while it is not known yet, which counts as no.
 *
 * The API stays the authority on every route.
 */
export function canManageAgentGrants(
  mailbox: AgentAccessMailbox,
  ownPermission?: MailboxPermission | null
): boolean {
  const { access } = mailbox;
  if (mailbox.type === 'personal') return access.view && !mailbox.awaiting_handover;
  if (mailbox.origin === 'connected')
    return needsOwnMembership(mailbox) && ownPermission === 'manage';
  return access.organize && access.manage_members;
}

/**
 * A connected shared mailbox where only the caller's own membership can tell whether they may
 * manage its agent grants (see `canManageAgentGrants`).
 */
export function needsOwnMembership(mailbox: AgentAccessMailbox): boolean {
  return (
    mailbox.type === 'shared' &&
    mailbox.origin === 'connected' &&
    mailbox.access.view &&
    mailbox.access.manage_members
  );
}

/** Whether the caller may review the drafts agents wrote for the mailbox: `send` on it. */
export function canReviewAgentDrafts(mailbox: Pick<Mailbox, 'access'>): boolean {
  return mailbox.access.send;
}

/**
 * A key the API would take for a grant: live (not test), unexpired, unrevoked, and holding
 * `mail:agent` or `*`. The picker shows only these.
 */
export function isUsableAgentKey(
  key: Pick<ApiKey, 'mode' | 'revoked_at' | 'expires_at' | 'scopes'>,
  now: Date
): boolean {
  return (
    key.mode === 'live' &&
    !key.revoked_at &&
    (!key.expires_at || Date.parse(key.expires_at) > now.getTime()) &&
    (key.scopes.includes(MAIL_AGENT_SCOPE) || key.scopes.includes('*'))
  );
}
