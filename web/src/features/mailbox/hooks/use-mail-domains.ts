'use client';

import { useMutation, useQuery, useQueryClient, type QueryClient } from '@tanstack/react-query';

import { LOCAL_ERROR_HANDLING_META } from '@/config/query-meta';
import { mailboxKeys, validId } from '@/features/mailbox/hooks/mailbox-keys';
import { mailboxService } from '@/features/mailbox/services/mailbox-service';
import type {
  MailDnsRecordInput,
  MailDnsRecordPatch,
  MailDomain,
  MailDomainInput,
} from '@/features/mailbox/types';

/** How often a domain whose setup runs is read again. */
const setupRefreshMs = 3_000;
/**
 * While a managed zone waits for its registrar's name servers, the API looks at it every few
 * minutes; reading the list once a minute shows the change soon after without asking Cloudflare.
 */
const zoneRefreshMs = 60_000;

export function useMailboxDomains(organizationId: number) {
  return useQuery({
    queryKey: mailboxKeys.domains(organizationId),
    queryFn: () => mailboxService.domains(organizationId),
    enabled: validId(organizationId),
    staleTime: 5 * 60_000,
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

/** Connected domains; only loaded when the instance allows them. */
export function useMailDomains(organizationId: number, enabled: boolean) {
  return useQuery({
    queryKey: mailboxKeys.mailDomains(organizationId),
    queryFn: () => mailboxService.mailDomains(organizationId),
    enabled: enabled && validId(organizationId),
    // A setup runs in the background on the API, and a pending zone activates there; follow both.
    refetchInterval: query =>
      query.state.data?.some(domain => domain.setting_up)
        ? setupRefreshMs
        : query.state.data?.some(domain => domain.zone?.status === 'pending')
          ? zoneRefreshMs
          : false,
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

/**
 * A connected domain changes which domains new mailboxes may use; connecting can create a managed
 * zone, a check can activate one, and a setup writes its records.
 */
function refreshDomains(queryClient: QueryClient, organizationId: number) {
  return Promise.all([
    queryClient.invalidateQueries({ queryKey: mailboxKeys.mailDomains(organizationId) }),
    queryClient.invalidateQueries({ queryKey: mailboxKeys.domains(organizationId) }),
    queryClient.invalidateQueries({ queryKey: mailboxKeys.mailZones(organizationId) }),
  ]);
}

/**
 * Puts a domain the API just answered with into the cached list, so its card shows the answer at
 * once instead of waiting for the list to be read again.
 */
function storeDomain(queryClient: QueryClient, organizationId: number, domain: MailDomain) {
  queryClient.setQueryData<MailDomain[]>(mailboxKeys.mailDomains(organizationId), current =>
    current?.map(item => (item.id === domain.id ? domain : item))
  );
}

export function useConnectMailDomain(organizationId: number) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: MailDomainInput) => mailboxService.connectMailDomain(organizationId, input),
    meta: LOCAL_ERROR_HANDLING_META,
    onSettled: () => refreshDomains(queryClient, organizationId),
  });
}

export function useRefreshMailDomain(organizationId: number) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ domain, action }: { domain: MailDomain; action: 'setups' | 'checks' }) =>
      mailboxService.refreshMailDomain(organizationId, domain.id, action),
    meta: LOCAL_ERROR_HANDLING_META,
    onSuccess: domain => storeDomain(queryClient, organizationId, domain),
    onSettled: () => refreshDomains(queryClient, organizationId),
  });
}

export function useRemoveMailDomain(organizationId: number) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (domainId: number) => mailboxService.removeMailDomain(organizationId, domainId),
    meta: LOCAL_ERROR_HANDLING_META,
    onSettled: () => refreshDomains(queryClient, organizationId),
  });
}

/** Changes nothing on the server, so nothing is invalidated. */
export function usePreviewMailDomain(organizationId: number) {
  return useMutation({
    mutationFn: (name: string) => mailboxService.previewMailDomain(organizationId, name),
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

export function useEnableReceiving(organizationId: number) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (domainId: number) => mailboxService.enableReceiving(organizationId, domainId),
    meta: LOCAL_ERROR_HANDLING_META,
    onSuccess: domain => storeDomain(queryClient, organizationId, domain),
    onSettled: () => refreshDomains(queryClient, organizationId),
  });
}

/** Moves the default mark, which changes every listed domain, so both lists are read again. */
export function useSetDefaultMailDomain(organizationId: number) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (domainId: number) => mailboxService.setDefaultMailDomain(organizationId, domainId),
    meta: LOCAL_ERROR_HANDLING_META,
    onSuccess: domain =>
      queryClient.setQueryData<MailDomain[]>(mailboxKeys.mailDomains(organizationId), current =>
        current?.map(item => (item.id === domain.id ? domain : { ...item, default: false }))
      ),
    onSettled: () => refreshDomains(queryClient, organizationId),
  });
}

/** Managed zones; only loaded when the instance allows custom domains. */
export function useListMailZones(organizationId: number, enabled: boolean) {
  return useQuery({
    queryKey: mailboxKeys.mailZones(organizationId),
    queryFn: () => mailboxService.listMailZones(organizationId),
    enabled: enabled && validId(organizationId),
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

export function useMailZone(organizationId: number, zoneId: number) {
  return useQuery({
    queryKey: mailboxKeys.mailZone(organizationId, zoneId),
    queryFn: () => mailboxService.getMailZone(organizationId, zoneId),
    enabled: validId(organizationId) && validId(zoneId),
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

export function useDeleteMailZone(organizationId: number) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (zoneId: number) => mailboxService.deleteMailZone(organizationId, zoneId),
    meta: LOCAL_ERROR_HANDLING_META,
    onSuccess: (_result, zoneId) => {
      queryClient.removeQueries({ queryKey: mailboxKeys.mailZone(organizationId, zoneId) });
    },
    onSettled: () =>
      queryClient.invalidateQueries({ queryKey: mailboxKeys.mailZones(organizationId) }),
  });
}

/** Read from Cloudflare on every request; the API keeps no copy. */
export function useListDnsRecords(organizationId: number, zoneId: number) {
  return useQuery({
    queryKey: mailboxKeys.dnsRecords(organizationId, zoneId),
    queryFn: () => mailboxService.listDnsRecords(organizationId, zoneId),
    enabled: validId(organizationId) && validId(zoneId),
    meta: LOCAL_ERROR_HANDLING_META,
  });
}

function refreshDnsRecords(queryClient: QueryClient, organizationId: number, zoneId: number) {
  return queryClient.invalidateQueries({
    queryKey: mailboxKeys.dnsRecords(organizationId, zoneId),
  });
}

export function useCreateDnsRecord(organizationId: number, zoneId: number) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: MailDnsRecordInput) =>
      mailboxService.createDnsRecord(organizationId, zoneId, input),
    meta: LOCAL_ERROR_HANDLING_META,
    onSettled: () => refreshDnsRecords(queryClient, organizationId, zoneId),
  });
}

export function useUpdateDnsRecord(organizationId: number, zoneId: number) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ recordId, input }: { recordId: string; input: MailDnsRecordPatch }) =>
      mailboxService.updateDnsRecord(organizationId, zoneId, recordId, input),
    meta: LOCAL_ERROR_HANDLING_META,
    onSettled: () => refreshDnsRecords(queryClient, organizationId, zoneId),
  });
}

export function useDeleteDnsRecord(organizationId: number, zoneId: number) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (recordId: string) =>
      mailboxService.deleteDnsRecord(organizationId, zoneId, recordId),
    meta: LOCAL_ERROR_HANDLING_META,
    onSettled: () => refreshDnsRecords(queryClient, organizationId, zoneId),
  });
}
