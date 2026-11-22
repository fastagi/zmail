'use client';

import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';

import { LOCAL_ERROR_HANDLING_META } from '@/config/query-meta';
import type { MailAgentGrantInput } from '@/features/mailbox/agent-grant-schemas';
import {
  agentGrantKeys,
  invalidateAgentGrantQueries,
} from '@/features/mailbox/hooks/agent-grant-keys';
import { mailboxKeys, validId } from '@/features/mailbox/hooks/mailbox-keys';
import { agentGrantService } from '@/features/mailbox/services/agent-grant-service';

export {
  agentGrantKeys,
  invalidateAgentGrantQueries,
} from '@/features/mailbox/hooks/agent-grant-keys';

/**
 * Every grant of the mailbox, newest first, read page by page, so the active count, the limit,
 * and the "already granted" check see them all. Only asked for when the caller may manage them.
 */
export function useAgentGrants(organizationId: number, mailboxId: number, enabled: boolean) {
  return useQuery({
    queryKey: agentGrantKeys.grants(organizationId, mailboxId),
    queryFn: () => agentGrantService.listAll(organizationId, mailboxId),
    enabled: enabled && validId(organizationId) && validId(mailboxId),
    staleTime: 15_000,
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

/** One grant's newest reads and drafts; asked for when the grant is selected. */
export function useAgentGrantActivity(
  organizationId: number,
  mailboxId: number,
  grantId: number | null
) {
  return useQuery({
    queryKey: agentGrantKeys.activity(organizationId, mailboxId, grantId ?? 0),
    queryFn: () => agentGrantService.activity(organizationId, mailboxId, grantId ?? 0),
    enabled: validId(organizationId) && validId(mailboxId) && grantId !== null,
    staleTime: 15_000,
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

/** One page of the reply drafts agents stored for the mailbox; only asked for with `send`. */
export function useAgentDrafts(
  organizationId: number,
  mailboxId: number,
  page: number,
  enabled: boolean
) {
  return useQuery({
    queryKey: agentGrantKeys.draftPage(organizationId, mailboxId, page),
    queryFn: () => agentGrantService.drafts(organizationId, mailboxId, page),
    enabled: enabled && validId(organizationId) && validId(mailboxId),
    placeholderData: keepPreviousData,
    staleTime: 15_000,
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

/**
 * A grant change writes an access event, so the history refreshes with the grants. Ending a
 * grant also ends what it may draft, so the drafts refresh too.
 */
function useGrantMutation<Variables, Result>(
  organizationId: number,
  mailboxId: number,
  mutationFn: (variables: Variables) => Promise<Result>
) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn,
    meta: LOCAL_ERROR_HANDLING_META,
    onSettled: () =>
      Promise.all([
        invalidateAgentGrantQueries(queryClient, organizationId, mailboxId),
        queryClient.invalidateQueries({
          queryKey: mailboxKeys.accessEvents(organizationId, mailboxId),
        }),
      ]),
  });
}

export function useIssueAgentGrant(organizationId: number, mailboxId: number) {
  return useGrantMutation(organizationId, mailboxId, (input: MailAgentGrantInput) =>
    agentGrantService.issue(organizationId, mailboxId, input)
  );
}

export function useRevokeAgentGrant(organizationId: number, mailboxId: number) {
  return useGrantMutation(organizationId, mailboxId, (grantId: number) =>
    agentGrantService.revoke(organizationId, mailboxId, grantId)
  );
}
