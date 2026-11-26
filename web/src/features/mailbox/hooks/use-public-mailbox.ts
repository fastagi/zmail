'use client';

import { useEffect, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';

import { LOCAL_ERROR_HANDLING_META } from '@/config/query-meta';
import { mailboxKeys } from '@/features/mailbox/hooks/mailbox-keys';
import {
  publicMailboxService,
  type ClaimPublicMailboxInput,
} from '@/features/mailbox/services/public-mailbox-service';

export const PUBLIC_MAILBOX_AVAILABILITY_DEBOUNCE_MS = 300;

export const publicMailboxKeys = {
  all: ['public-mailbox'] as const,
  options: () => [...publicMailboxKeys.all, 'options'] as const,
  availability: (localPart: string, domain: string) =>
    [...publicMailboxKeys.all, 'availability', localPart, domain] as const,
};

export function usePublicMailboxOptions({ enabled = true }: { enabled?: boolean } = {}) {
  return useQuery({
    queryKey: publicMailboxKeys.options(),
    queryFn: () => publicMailboxService.options(),
    enabled,
    staleTime: 60_000,
    retry: false,
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

export function usePublicMailboxAvailability(
  rawLocalPart: string,
  domain: string,
  {
    enabled = true,
    debounceMs = PUBLIC_MAILBOX_AVAILABILITY_DEBOUNCE_MS,
  }: { enabled?: boolean; debounceMs?: number } = {}
) {
  const normalized = rawLocalPart.trim().toLowerCase();
  const normalizedDomain = domain.trim().toLowerCase();
  const [debounced, setDebounced] = useState(normalized);

  useEffect(() => {
    if (normalized === '') {
      setDebounced('');
      return;
    }
    if (debounceMs <= 0) {
      setDebounced(normalized);
      return;
    }
    const timer = window.setTimeout(() => setDebounced(normalized), debounceMs);
    return () => window.clearTimeout(timer);
  }, [normalized, debounceMs]);

  const query = useQuery({
    queryKey: publicMailboxKeys.availability(debounced, normalizedDomain),
    queryFn: () => publicMailboxService.availability(debounced, normalizedDomain || undefined),
    enabled: enabled && debounced.length > 0 && normalizedDomain.length > 0,
    staleTime: 10_000,
    retry: false,
    meta: LOCAL_ERROR_HANDLING_META,
  });

  return {
    ...query,
    debouncedLocalPart: debounced,
    isDebouncing: normalized.length > 0 && normalized !== debounced,
  };
}

export function useClaimPublicMailbox() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({
      organizationId,
      input,
    }: {
      organizationId: number;
      input: ClaimPublicMailboxInput;
    }) => publicMailboxService.claim(organizationId, input),
    meta: LOCAL_ERROR_HANDLING_META,
    onSuccess: (_mailbox, { organizationId }) =>
      Promise.all([
        queryClient.invalidateQueries({ queryKey: publicMailboxKeys.options() }),
        queryClient.invalidateQueries({ queryKey: mailboxKeys.list(organizationId) }),
        queryClient.invalidateQueries({ queryKey: mailboxKeys.inboxes(organizationId) }),
        queryClient.invalidateQueries({ queryKey: mailboxKeys.allInboxCounts(organizationId) }),
        queryClient.invalidateQueries({ queryKey: mailboxKeys.directories(organizationId) }),
      ]),
  });
}
