import { keepPreviousData, useMutation, useQuery } from '@tanstack/react-query';
import { mailOperationsService } from '@/features/mail-operations/services/mail-operations-service';
import type {
  ContentAccessSearch,
  MailOrganizationSearch,
  OutboundMessageSearch,
} from '@/features/mail-operations/types';
import { ApiErrorCode } from '@/http/codes';
import { ApiError } from '@/http/client';

const mailOperationsQueryKey = ['operator', 'mail-operations'] as const;

export function useMailOverview(date: string | undefined) {
  return useQuery({
    queryKey: [...mailOperationsQueryKey, 'overview', date ?? 'today'],
    queryFn: ({ signal }) => mailOperationsService.overview(date, signal),
  });
}

export function useMailOrganizations(search: MailOrganizationSearch) {
  return useQuery({
    queryKey: [...mailOperationsQueryKey, 'organizations', search],
    queryFn: ({ signal }) => mailOperationsService.organizations(search, signal),
    placeholderData: keepPreviousData,
  });
}

export function useOutboundMessages(search: OutboundMessageSearch) {
  return useQuery({
    queryKey: [...mailOperationsQueryKey, 'outbound', search],
    queryFn: ({ signal }) => mailOperationsService.outbound(search, signal),
    placeholderData: keepPreviousData,
  });
}

/**
 * Reads one broadcast body. It is a mutation, not a query, so the text never enters the query
 * cache; `gcTime: 0` drops the finished mutation (and its text) as soon as nothing observes it.
 */
export function useBroadcastContent() {
  return useMutation({
    mutationFn: ({
      organizationId,
      messageId,
      reason,
    }: {
      organizationId: number;
      messageId: number;
      reason: string;
    }) => mailOperationsService.readContent(organizationId, messageId, reason),
    gcTime: 0,
  });
}

export function useContentAccessAudits(search: ContentAccessSearch) {
  return useQuery({
    queryKey: [...mailOperationsQueryKey, 'content-access', search],
    queryFn: ({ signal }) => mailOperationsService.contentAccess(search, signal),
    placeholderData: keepPreviousData,
  });
}

/** The session is valid but its grant does not cover this view. */
export function isCapabilityError(error: unknown): boolean {
  return error instanceof ApiError && error.errorCode === ApiErrorCode.OPERATOR_CAPABILITY_REQUIRED;
}
