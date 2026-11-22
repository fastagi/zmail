import type { ApiKey } from '@/features/api-key/types';
import type { Mailbox } from '@/features/mailbox/types';

/** The scope a key needs for a grant; `*` includes it. Kept here so the access panel stays small. */
export const MAIL_AGENT_SCOPE = 'mail:agent';

/**
 * Whether the caller may issue, list, and revoke the mailbox's agent grants, derived from the
 * `access` the API returns (`contracts/MAILBOXES.md`, Agent Grants):
 *
 * - Personal, Zmail or connected: its owner alone. Only the owner has `view` on a personal
 *   mailbox; owners and admins never do, and nobody does while it awaits a handover.
 * - Shared Zmail: a `manage` member, the one shared caller with `organize` (an owner or admin who
 *   is not a `manage` member has `manage_members` without it).
 * - Shared connected: nobody has `organize` there, so `view` with `manage_members` is the closest
 *   signal. An owner or admin who is only a `view` or `send` member also has both; the API answers
 *   them `403 PERMISSION.DENIED`, which the tab shows.
 *
 * The API stays the authority on every route.
 */
export function canManageAgentGrants(
  mailbox: Pick<Mailbox, 'type' | 'origin' | 'access' | 'awaiting_handover'>
): boolean {
  const { access } = mailbox;
  if (mailbox.type === 'personal') return access.view && !mailbox.awaiting_handover;
  if (mailbox.origin === 'connected') return access.view && access.manage_members;
  return access.organize && access.manage_members;
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
