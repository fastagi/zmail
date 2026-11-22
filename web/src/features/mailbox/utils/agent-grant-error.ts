import { ApiErrorCode } from '@/http/codes';
import { ApiError } from '@/http/request';
import {
  resolveMailboxErrorKey,
  type MailboxErrorKey,
} from '@/features/mailbox/utils/mailbox-error';

/** Copy of the failures the Agent Grants section of `contracts/MAILBOXES.md` documents. */
export type AgentGrantErrorKey =
  | 'agents.errors.notFound'
  | 'agents.errors.invalid'
  | 'agents.errors.limit'
  | 'agents.errors.exists'
  | 'agents.errors.permissionDenied'
  | 'agents.errors.mailboxNotFound'
  | 'agents.errors.validation'
  | 'agents.errors.rateLimited';

const agentErrorKeys: Partial<Record<string, AgentGrantErrorKey>> = {
  [ApiErrorCode.MAILBOX_AGENT_GRANT_NOT_FOUND]: 'agents.errors.notFound',
  [ApiErrorCode.MAILBOX_AGENT_GRANT_INVALID]: 'agents.errors.invalid',
  [ApiErrorCode.MAILBOX_AGENT_GRANT_LIMIT]: 'agents.errors.limit',
  [ApiErrorCode.MAILBOX_AGENT_GRANT_EXISTS]: 'agents.errors.exists',
  [ApiErrorCode.PERMISSION_DENIED]: 'agents.errors.permissionDenied',
  [ApiErrorCode.MAILBOX_NOT_FOUND]: 'agents.errors.mailboxNotFound',
  [ApiErrorCode.COMMON_VALIDATION_FAILED]: 'agents.errors.validation',
  [ApiErrorCode.COMMON_RATE_LIMITED]: 'agents.errors.rateLimited',
};

/**
 * Maps a failure of an agent grant route to stable local copy: the agent codes and the access
 * failures get wording of their own, anything else the mailbox copy. Backend text is never shown.
 */
export function resolveAgentGrantErrorKey(error: unknown): AgentGrantErrorKey | MailboxErrorKey {
  if (error instanceof ApiError) {
    const key = agentErrorKeys[error.errorCode];
    if (key) return key;
  }
  return resolveMailboxErrorKey(error);
}
