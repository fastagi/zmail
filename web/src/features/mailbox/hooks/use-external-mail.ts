'use client';

import { keepPreviousData, useQuery } from '@tanstack/react-query';

import { LOCAL_ERROR_HANDLING_META } from '@/config/query-meta';
import { liveRefresh, mailboxKeys, validId } from '@/features/mailbox/hooks/mailbox-keys';

/**
 * The service, with its response schemas, loads on first use: the inbox reads the connector list
 * on every visit, but nothing else of external mail belongs in its first load.
 */
const service = async () =>
  (await import('@/features/mailbox/services/external-mail-service')).externalMailService;

export const externalMailKeys = {
  all: (organizationId: number) =>
    [...mailboxKeys.organization(organizationId), 'external-mail'] as const,
  connectors: (organizationId: number) =>
    [...externalMailKeys.all(organizationId), 'connectors'] as const,
  connections: (organizationId: number) =>
    [...externalMailKeys.all(organizationId), 'connections'] as const,
  syncRuns: (organizationId: number, connectionId: number) =>
    [...externalMailKeys.all(organizationId), 'sync-runs', connectionId] as const,
  directories: (organizationId: number) =>
    [...externalMailKeys.all(organizationId), 'directory'] as const,
  directory: (organizationId: number, cursor: string | undefined) =>
    [...externalMailKeys.directories(organizationId), cursor ?? null] as const,
};

/**
 * The instance's connectors. They change only with the instance's configuration, so they are read
 * once per page; an empty list turns every external-mailbox control off.
 */
export function useExternalMailConnectors(organizationId: number) {
  return useQuery({
    queryKey: externalMailKeys.connectors(organizationId),
    queryFn: async () => (await service()).connectors(organizationId),
    enabled: validId(organizationId),
    staleTime: 5 * 60_000,
    retry: false,
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

/**
 * The caller's own connections. The route answers 404 while external mail is off, so it is read
 * only once the connector list says the feature is on. Refreshed like the inbox, since a sync can
 * turn a connection to `needs_reconnect` at any time.
 */
export function useExternalMailConnections(organizationId: number, { enabled = true } = {}) {
  return useQuery({
    queryKey: externalMailKeys.connections(organizationId),
    queryFn: async () => (await service()).connections(organizationId),
    enabled: enabled && validId(organizationId),
    staleTime: 15_000,
    ...liveRefresh,
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

/**
 * The latest sync runs of one of the caller's own connections, newest first. Read while the
 * connected-mailboxes dialog shows the connection, and refreshed like the connection list.
 */
export function useExternalMailSyncRuns(
  organizationId: number,
  connectionId: number,
  { enabled = true } = {}
) {
  return useQuery({
    queryKey: externalMailKeys.syncRuns(organizationId, connectionId),
    queryFn: async () => (await service()).syncRuns(organizationId, connectionId),
    enabled: enabled && validId(organizationId) && validId(connectionId),
    staleTime: 15_000,
    ...liveRefresh,
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

/** One page of the owners' and admins' connection directory; the previous page stays shown. */
export function useExternalMailDirectory(organizationId: number, cursor: string | undefined) {
  return useQuery({
    queryKey: externalMailKeys.directory(organizationId, cursor),
    queryFn: async () => (await service()).directory(organizationId, cursor),
    enabled: validId(organizationId),
    placeholderData: keepPreviousData,
    staleTime: 15_000,
    meta: LOCAL_ERROR_HANDLING_META,
  });
}
