import type { QueryClient } from '@tanstack/react-query';

import { mailboxKeys } from '@/features/mailbox/hooks/mailbox-keys';

/**
 * Cache keys of a mailbox's agent grants, their activity, and its agent drafts, apart from the
 * hooks and the service so that code which only ends grants (disconnecting or purging a connected
 * account, deleting a mailbox) can refresh them without loading the Agent tab.
 */
export const agentGrantKeys = {
  grants: (organizationId: number, mailboxId: number) =>
    [...mailboxKeys.mailbox(organizationId, mailboxId), 'agent-grants'] as const,
  activity: (organizationId: number, mailboxId: number, grantId: number) =>
    [...agentGrantKeys.grants(organizationId, mailboxId), grantId, 'activity'] as const,
  drafts: (organizationId: number, mailboxId: number) =>
    [...mailboxKeys.mailbox(organizationId, mailboxId), 'agent-drafts'] as const,
  draftPage: (organizationId: number, mailboxId: number, page: number) =>
    [...agentGrantKeys.drafts(organizationId, mailboxId), page] as const,
};

/** Refreshes the agent grants, their activity, and the agent drafts of one mailbox. */
export function invalidateAgentGrantQueries(
  queryClient: QueryClient,
  organizationId: number,
  mailboxId: number
) {
  return Promise.all([
    queryClient.invalidateQueries({ queryKey: agentGrantKeys.grants(organizationId, mailboxId) }),
    queryClient.invalidateQueries({ queryKey: agentGrantKeys.drafts(organizationId, mailboxId) }),
  ]);
}
